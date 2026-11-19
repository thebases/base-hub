package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the apidoc route surface.
// - Key symbols: `RegisterAPIDocRoutes`, `apidocRequestContext`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"errors"
	"io/fs"
	"strings"

	"git.thebase.vn/base_hub/internal/apidoc"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/documents"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/gofiber/fiber/v3"
)

// RegisterAPIDocRoutes exposes documentation endpoints backed by
// the runtime registry and protected by the same RPC authentication contract.
func RegisterAPIDocRoutes(router fiber.Router) {
	service := apidoc.NewService(
		apidoc.WithConfig(apidoc.Config{
			Title:    config.Get().AppName(),
			Version:  config.Get().AppVersion(),
			BasePath: config.Get().Server.BasePath,
		}),
		apidoc.WithAccessChecker(func(ctx context.Context, model orm.ModelMetadata) error {
			liveModel, ok := orm.LookupModel(model.Name)
			if !ok {
				return fiber.ErrNotFound
			}
			method, ok := liveModel.LookupRuntimeMethod("read")
			if !ok {
				return errors.New("apidoc: read method is not registered")
			}
			return orm.EnsureRuntimeModelAccess(ctx, liveModel, method, nil)
		}),
	)

	router.Get("/doc", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		requestCtx = apidoc.ContextWithLanguage(requestCtx, apidocLanguage(c))
		index, err := service.Index(requestCtx)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(fiber.Map{
			"title":        config.Get().AppName(),
			"version":      config.Get().AppVersion(),
			"models_count": len(index.Models),
			"modules":      index.Modules,
			"endpoints": fiber.Map{
				"index":     "/doc/index.json",
				"languages": "/doc/languages.json",
				"model":     "/doc/{model}.json",
				"openapi":   "/doc/openapi.json",
				"guide":     "/doc/guide.md",
			},
		})
	})

	// Lists the supported documentation languages (first entry is the default shown on first start).
	router.Get("/doc/languages.json", func(c fiber.Ctx) error {
		if _, err := apidocRequestContext(c); err != nil {
			return err
		}
		return c.JSON(fiber.Map{"default": apidoc.DefaultLanguage, "languages": apidoc.Languages})
	})

	// Serves the embedded usage guide (documents/base/<lang>/README.md) that the API docs page shows in its Guide tab.
	router.Get("/doc/guide.md", func(c fiber.Ctx) error {
		if _, err := apidocRequestContext(c); err != nil {
			return err
		}
		body, ok := apidoc.ReadDocument(apidocLanguage(c), "base/README.md")
		if !ok {
			return fiber.ErrNotFound
		}
		c.Set(fiber.HeaderContentType, "text/markdown; charset=utf-8")
		return c.SendString(body)
	})

	// Instruction documents: kernel docs (internal/documents/helper) plus the helper docs of installed modules.
	router.Get("/doc/instructions.json", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		kernel, modulesFS, installed := instructionSources(requestCtx)
		list, err := documents.ListHelper(kernel, modulesFS, installed, apidoc.NormalizeLanguage(apidocLanguage(c)))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(fiber.Map{"modules": list})
	})

	router.Get("/doc/instruction.md", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		kernel, modulesFS, installed := instructionSources(requestCtx)
		body, ok, err := documents.ReadHelper(kernel, modulesFS, installed, c.Query("module"), apidoc.NormalizeLanguage(apidocLanguage(c)), c.Query("path"))
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		if !ok {
			return fiber.ErrNotFound
		}
		c.Set(fiber.HeaderContentType, "text/markdown; charset=utf-8")
		return c.SendString(body)
	})

	router.Get("/doc/index.json", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		requestCtx = apidoc.ContextWithLanguage(requestCtx, apidocLanguage(c))
		index, err := service.Index(requestCtx)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(index)
	})

	router.Get("/doc/openapi.json", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		requestCtx = apidoc.ContextWithLanguage(requestCtx, apidocLanguage(c))
		spec, err := service.OpenAPI(requestCtx)
		if err != nil {
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(spec)
	})

	router.Get("/doc/:model.json", func(c fiber.Ctx) error {
		requestCtx, err := apidocRequestContext(c)
		if err != nil {
			return err
		}
		requestCtx = apidoc.ContextWithLanguage(requestCtx, apidocLanguage(c))
		modelDoc, err := service.Model(requestCtx, c.Params("model"))
		if err != nil {
			if strings.Contains(err.Error(), "is not registered") {
				return fiber.ErrNotFound
			}
			return fiber.NewError(fiber.StatusBadRequest, err.Error())
		}
		return c.JSON(modelDoc)
	})
}

// apidocLanguage reads the documentation language from ?lang=; absent means the default (English).
// instructionSources returns the embedded kernel helper docs, the module tree (embedded, or on disk for
// `go run`) and the per-request install check that gates module docs.
func instructionSources(ctx context.Context) (fs.FS, fs.FS, documents.Installed) {
	kernel, _ := fs.Sub(documents.FS, "helper")
	return kernel, kernelmodules.ModuleTreeFS(), func(name string) (bool, error) {
		return kernelmodules.InstalledModule(ctx, name)
	}
}

func apidocLanguage(c fiber.Ctx) string {
	return c.Query("lang")
}

func apidocRequestContext(c fiber.Ctx) (context.Context, error) {
	identity, err := requireRPCIdentity(c, false)
	if err != nil {
		return nil, err
	}
	return orm.ContextWithEnvironment(c.Context(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID:       identity.UserID,
		DBName:       identity.DBName,
		Login:        identity.Username,
		RoleID:       identity.RoleID,
		RoleName:     identity.RoleName,
		GroupIDs:     append([]int64(nil), identity.GroupIDs...),
		CompanyID:    identity.CompanyID,
		CompanyIDs:   append([]int64(nil), identity.CompanyIDs...),
		AuthMode:     identity.AuthMode,
		IsSuperadmin: identity.Sudo,
	})), nil
}

// fiber:context-methods migrated
