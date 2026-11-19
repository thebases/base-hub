package main

// File overview:
// - Responsibility: owns compile-time asset embedding and registers the resulting filesystems with runtime packages before commands execute.
// - Key symbols: `embeddedAssets`, `init`.
// - Pattern: Keeps `//go:embed` close to the backend entrypoint while isolating bundle preparation from the smaller process entrypoint in `main.go`.

import (
	"embed"
	"fmt"
	"io/fs"

	"git.thebase.vn/base_hub/internal/cmd"
	"git.thebase.vn/base_hub/internal/documents"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
)

// embeddedAssets keeps the bundled markdown and module data inside the Go
// binary so production boots do not depend on runtime file layout.
//
// The frontend lives directly in ./frontend and is served at "/" and "/web" —
// it is a static, no-bundler SPA (plain ES modules loaded by the browser via
// its own import map), so it is embedded straight from source with no
// build/copy step. The whole ./frontend tree is embedded (`all:` also keeps dot- and
// underscore-prefixed files), so any new file there ships automatically —
// never put node_modules, package tooling, or secrets (.env) in ./frontend,
// because everything in it ends up in the binary and is publicly served.
//
//go:embed modules
//go:embed all:frontend
var embeddedAssets embed.FS

// init registers embedded assets during bootstrap so commands and HTTP routes
// resolve one shared bundle instead of configuring each embedded subtree separately.
func init() {
	frontendDistFS, err := fs.Sub(embeddedAssets, "frontend")
	if err != nil {
		panic(fmt.Errorf("prepare embedded frontend bundle: %w", err))
	}

	moduleFS, err := fs.Sub(embeddedAssets, "modules")
	if err != nil {
		panic(fmt.Errorf("prepare embedded module bundle: %w", err))
	}

	// Bundle internal/documents/apidoc so ModelSpec.Description can reference text/markdown files.
	apiDocsFS, err := fs.Sub(documents.FS, "apidoc")
	if err != nil {
		panic(fmt.Errorf("prepare embedded api documents: %w", err))
	}
	orm.SetDescriptionFS(apiDocsFS)
	// Module api documents (modules/<m>/apidoc) ride in the already-embedded modules bundle.
	orm.SetModuleDescriptionFS(moduleFS)

	cmd.SetEmbeddedAssets(cmd.EmbeddedAssets{
		StaticFrontend: frontendDistFS,
		Modules:        moduleFS,
	})
	kernelmodules.SetEmbeddedFS(moduleFS)
}
