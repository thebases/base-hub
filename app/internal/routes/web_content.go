package routes

// File overview:
// - Responsibility: implements the /web/content HTTP surface for sys.attachment (SRS-F-019) —
//   authenticated upload and access-checked, checksum-verified download.
// - Key symbols: `RegisterWebContentRoutes`, `handleWebContentDownload`, `handleWebContentUpload`.
// - Pattern: Keeps transport concerns at the HTTP boundary: all access decisions flow through the
//   sys.attachment runtime dispatch (ACL + record rules + target-record delegation), so this file
//   never re-implements authorization.
// - Serving rules: ETag from checksum, server-derived Content-Type, HTML-ish types always download,
//   and access denial is indistinguishable from a missing attachment (404, no existence leak).

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"strings"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/filestore"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

// webContentUnauthenticatedDB resolves the request's target database without
// requiring identity — needed to check is_public before deciding whether
// download requires authentication at all (mirrors modules/base-attachment's
// former requestDB helper, folded in here on the 2026-08-01 merge).
func webContentUnauthenticatedDB(c fiber.Ctx) (string, *bun.DB, error) {
	dbName, err := ResolveRPCDBName(RequestDBNameRaw(c))
	if err != nil {
		return "", nil, fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	// dbName is unauthenticated request input — NoCreate so it can't
	// spray-create a database.
	db, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		return "", nil, fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	return dbName, db.DB, nil
}

const webContentAttachmentModel = "sys.attachment"

// RegisterWebContentRoutes mounts the attachment transport. The /web/content
// prefix is already excluded from the SPA fallback in server.go (SRS-F-012).
func RegisterWebContentRoutes(router fiber.Router) {
	router.Get("/web/content/:id", handleWebContentDownload)
	router.Post("/web/content", handleWebContentUpload)
}

// webContentEnvironment authenticates the caller and rebuilds the ORM
// environment, mirroring handleModelJSONRPC so /web/content shares the exact
// auth and RBAC semantics of the RPC surface.
func webContentEnvironment(c fiber.Ctx) (*rpcRequestIdentity, fiber.Ctx, error) {
	identity, err := requireRPCIdentity(c, false)
	if err != nil {
		return nil, nil, err
	}
	requestCtx := orm.ContextWithEnvironment(c.Context(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
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
	}))
	c.SetContext(requestCtx)
	return identity, c, nil
}

// handleWebContentDownload serves one attachment's bytes. Every failure after
// authentication returns the same 404 so a caller can never distinguish "does
// not exist" from "not allowed" (SRS-F-019 acceptance). A row with
// is_public=true (base.attachment's scope rule, merged 2026-08-01) skips
// authentication entirely; whether a row exists or is public is itself
// checked unauthenticated but never surfaced as a distinct outcome — a
// missing row and a private row both fall through to requiring identity, so
// an anonymous prober cannot distinguish the two by status code.
func handleWebContentDownload(c fiber.Ctx) error {
	ids, err := parseRPCPathIDs(c.Params("id"), false)
	if err != nil || len(ids) != 1 {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}

	dbName, db, err := webContentUnauthenticatedDB(c)
	if err != nil {
		return err
	}
	var isPublic bool
	publicScanErr := db.NewRaw(`SELECT is_public FROM sys_attachment WHERE id = ?`, ids[0]).Scan(c.Context(), &isPublic)

	var requestCtx context.Context
	if publicScanErr == nil && isPublic {
		requestCtx = orm.ContextWithEnvironment(c.Context(), orm.NewEnvironmentWithDBName(db, dbName, 0, nil, true))
	} else {
		_, authenticatedCtx, err := webContentEnvironment(c)
		if err != nil {
			return err
		}
		c = authenticatedCtx
		requestCtx = c.Context()
	}

	meta, ok := orm.LookupModel(webContentAttachmentModel)
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	readMethod, ok := meta.LookupRuntimeMethod("read")
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	// The access check runs the sys.attachment access handler, which delegates
	// to the bound record's ACL + record rules + company scope. Denial maps to
	// 404 on purpose — no existence leak.
	if err := orm.EnsureRuntimeModelAccess(requestCtx, meta, readMethod, ids); err != nil {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}

	result, err := modelruntime.NewService().Execute(requestCtx, modelruntime.Request{
		Model:  webContentAttachmentModel,
		Method: "read",
		IDs:    ids,
	})
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	record, ok := firstWebContentRecord(result)
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}

	checksum := strings.TrimSpace(webContentString(record["checksum"]))
	mimetype := strings.TrimSpace(webContentString(record["mimetype"]))
	name := strings.TrimSpace(webContentString(record["name"]))
	if checksum == "" {
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}

	// ETag short-circuit: the checksum IS the content identity, so a matching
	// If-None-Match can answer without touching the filestore.
	etag := `"` + checksum + `"`
	if strings.Contains(c.Get(fiber.HeaderIfNoneMatch), checksum) {
		c.Set(fiber.HeaderETag, etag)
		return c.SendStatus(fiber.StatusNotModified)
	}

	file, err := filestore.Open(requestCtx, dbName, checksum)
	if err != nil {
		// A row without its blob is operationally missing content; keep the
		// uniform 404 outward while logging the real cause for ops.
		log.Errorf("web/content: blob %s for attachment %s missing: %v", checksum, ids[0], err)
		return fiber.NewError(fiber.StatusNotFound, "attachment not found")
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		log.Errorf("web/content: read blob %s: %v", checksum, err)
		return fiber.NewError(fiber.StatusInternalServerError, "attachment read failed")
	}

	// Verify content against its address before serving: a mismatch means disk
	// corruption or tampering, so the blob is quarantined and ops alerted
	// instead of silently serving wrong bytes (SRS-F-019 error contract).
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), checksum) {
		if quarantineErr := filestore.Quarantine(requestCtx, dbName, checksum); quarantineErr != nil {
			log.Errorf("web/content: quarantine corrupted blob %s: %v", checksum, quarantineErr)
		}
		log.Errorf("web/content: checksum mismatch for blob %s on %q", checksum, dbName)
		// Best-effort ops alert on the global channel; serving must fail either way.
		if _, alertErr := orm.ExecuteRuntimeMethod(requestCtx, "base.service.bus", "publish", nil, []any{
			[]string{"servicebus:global"},
			"attachment.checksum_mismatch",
			map[string]any{
				"title":    "Attachment blob corrupted",
				"message":  fmt.Sprintf("Blob %s on database %q failed checksum verification and was quarantined.", checksum, dbName),
				"checksum": checksum,
				"database": dbName,
			},
		}, nil); alertErr != nil {
			log.Warnf("web/content: publish checksum alert: %v", alertErr)
		}
		return fiber.NewError(fiber.StatusInternalServerError, "attachment content verification failed")
	}

	if mimetype == "" {
		mimetype = "application/octet-stream"
	}
	// HTML-ish content always downloads: serving it inline would execute
	// user-uploaded markup in the app origin (stored XSS).
	disposition := "inline"
	if c.Query("download") == "1" || models.IsHTMLishMimetype(mimetype) {
		disposition = "attachment"
	}
	if name == "" {
		name = checksum
	}
	c.Set(fiber.HeaderETag, etag)
	c.Set(fiber.HeaderContentType, mimetype)
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`%s; filename="%s"`, disposition, sanitizeContentFilename(name)))
	// Never let browsers second-guess the served type back into something
	// executable.
	c.Set("X-Content-Type-Options", "nosniff")
	return c.Send(data)
}

// handleWebContentUpload accepts one attachment as multipart form data (field
// `ufile` or `file`) or as a JSON body with base64 `datas`, and creates the
// sys.attachment row through the runtime dispatch so validation, size limits,
// and access checks apply identically to RPC creates.
func handleWebContentUpload(c fiber.Ctx) error {
	_, c, err := webContentEnvironment(c)
	if err != nil {
		return err
	}
	requestCtx := c.Context()

	values, err := webContentUploadValues(c)
	if err != nil {
		return err
	}

	meta, ok := orm.LookupModel(webContentAttachmentModel)
	if !ok {
		return fiber.NewError(fiber.StatusServiceUnavailable, "attachment model is not available")
	}
	createMethod, ok := meta.LookupRuntimeMethod("create")
	if !ok {
		return fiber.NewError(fiber.StatusServiceUnavailable, "attachment model is not available")
	}
	if err := orm.EnsureRuntimeModelAccess(requestCtx, meta, createMethod, nil); err != nil {
		return rpcAccessDeniedError(err)
	}

	result, err := modelruntime.NewService().Execute(requestCtx, modelruntime.Request{
		Model:  webContentAttachmentModel,
		Method: "create",
		Args:   []any{values},
	})
	if err != nil {
		// The model layer is transport-agnostic, so the size violation surfaces
		// as a plain error; map it to the SRS-mandated 413 at the HTTP boundary.
		if strings.Contains(err.Error(), "exceeds the maximum size") {
			return fiber.NewError(fiber.StatusRequestEntityTooLarge, err.Error())
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(fiber.Map{"result": result})
}

// webContentUploadValues normalizes the two accepted upload shapes into one
// sys.attachment create payload with base64 datas.
func webContentUploadValues(c fiber.Ctx) (map[string]any, error) {
	// Multipart form upload is the browser-native path.
	for _, field := range []string{"ufile", "file"} {
		fileHeader, err := c.FormFile(field)
		if err != nil || fileHeader == nil {
			continue
		}
		file, err := fileHeader.Open()
		if err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "cannot open uploaded file")
		}
		data, err := io.ReadAll(file)
		_ = file.Close()
		if err != nil {
			return nil, fiber.NewError(fiber.StatusBadRequest, "cannot read uploaded file")
		}
		name := strings.TrimSpace(c.FormValue("name"))
		if name == "" {
			name = fileHeader.Filename
		}
		values := map[string]any{
			"name":  name,
			"datas": base64.StdEncoding.EncodeToString(data),
		}
		// Binding fields are optional; the model layer validates them.
		if resModel := strings.TrimSpace(c.FormValue("sys_model")); resModel != "" {
			values["sys_model"] = resModel
		}
		if modelID := strings.TrimSpace(c.FormValue("model_id")); modelID != "" {
			values["model_id"] = modelID
		}
		if mimetype := strings.TrimSpace(fileHeader.Header.Get(fiber.HeaderContentType)); mimetype != "" {
			values["mimetype"] = mimetype
		}
		// Visibility scope is optional; the model layer validates mutual
		// exclusivity and self-scope authorization (base.attachment merge).
		webContentScopeFormValues(c, values)
		return values, nil
	}

	// JSON body fallback mirrors the RPC create payload shape.
	var payload struct {
		Name      string `json:"name"`
		Datas     string `json:"datas"`
		ResModel  string `json:"sys_model"`
		ModelId   any    `json:"model_id"`
		Mimetype  string `json:"mimetype"`
		CompanyID any    `json:"company_id"`
		UserID    any    `json:"user_id"`
		GroupID   any    `json:"group_id"`
		IsPublic  bool   `json:"is_public"`
	}
	if err := c.Bind().Body(&payload); err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, "upload requires a multipart file or a JSON body with base64 datas")
	}
	if strings.TrimSpace(payload.Datas) == "" {
		return nil, fiber.NewError(fiber.StatusBadRequest, "upload requires a multipart file or a JSON body with base64 datas")
	}
	values := map[string]any{
		"name":  strings.TrimSpace(payload.Name),
		"datas": strings.TrimSpace(payload.Datas),
	}
	if resModel := strings.TrimSpace(payload.ResModel); resModel != "" {
		values["sys_model"] = resModel
	}
	if payload.ModelId != nil {
		values["model_id"] = payload.ModelId
	}
	if mimetype := strings.TrimSpace(payload.Mimetype); mimetype != "" {
		values["mimetype"] = mimetype
	}
	// Visibility scope is optional; the model layer validates mutual
	// exclusivity and self-scope authorization (base.attachment merge).
	if payload.CompanyID != nil {
		values["company_id"] = payload.CompanyID
	}
	if payload.UserID != nil {
		values["user_id"] = payload.UserID
	}
	if payload.GroupID != nil {
		values["group_id"] = payload.GroupID
	}
	if payload.IsPublic {
		values["is_public"] = true
	}
	return values, nil
}

// webContentScopeFormValues reads the optional company_id/user_id/group_id/
// is_public multipart form fields (base.attachment's visibility scope,
// merged 2026-08-01) into values. Mutual exclusivity and self-scope
// authorization are enforced by the model layer (validateSysAttachmentScope),
// not here — this is transport-level parsing only.
func webContentScopeFormValues(c fiber.Ctx, values map[string]any) {
	if companyID := strings.TrimSpace(c.FormValue("company_id")); companyID != "" {
		values["company_id"] = companyID
	}
	if userID := strings.TrimSpace(c.FormValue("user_id")); userID != "" {
		values["user_id"] = userID
	}
	if groupID := strings.TrimSpace(c.FormValue("group_id")); groupID != "" {
		values["group_id"] = groupID
	}
	if isPublic := strings.TrimSpace(c.FormValue("is_public")); isPublic == "1" || strings.EqualFold(isPublic, "true") {
		values["is_public"] = true
	}
}

// firstWebContentRecord normalizes the runtime read result shapes into one row.
func firstWebContentRecord(result any) (orm.RecordMap, bool) {
	switch typed := result.(type) {
	case []orm.RecordMap:
		if len(typed) > 0 {
			return typed[0], true
		}
	case orm.RecordMap:
		return typed, true
	case []any:
		if len(typed) > 0 {
			if record, ok := typed[0].(orm.RecordMap); ok {
				return record, true
			}
			if raw, ok := typed[0].(map[string]any); ok {
				return orm.RecordMap(raw), true
			}
		}
	case map[string]any:
		return orm.RecordMap(typed), true
	}
	return nil, false
}

// webContentString extracts a string field tolerating the runtime's
// false-normalization (absent values serialize as boolean false).
func webContentString(raw any) string {
	if text, ok := raw.(string); ok {
		return text
	}
	return ""
}

// sanitizeContentFilename keeps Content-Disposition header-safe: quotes and
// control characters would let an uploaded name break out of the header value.
func sanitizeContentFilename(name string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '"' || r == '\\' || r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, name)
	if strings.TrimSpace(cleaned) == "" {
		return "download"
	}
	return cleaned
}

// fiber:context-methods migrated
