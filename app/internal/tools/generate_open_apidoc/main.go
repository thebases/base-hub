package main

// File overview:
// - Responsibility: build-time generator that writes open_apidoc*.json (OpenAPI 3.1 of every module alias route) into the base_apidoc viewer folder, which is then embedded into the binary with the rest of ./frontend.
// - Key symbols: `main`.
// - Pattern: blank-imports modules/all so every module registers its models, then reads the registry; run by `make build` (and `go generate ./modules/all`'s sibling step) before `go build`.

import (
	"context"
	"flag"
	"io/fs"
	"log"
	"os"

	"git.thebase.vn/base_hub/internal/apidoc"
	"git.thebase.vn/base_hub/internal/documents"
	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	_ "git.thebase.vn/base_hub/modules/all"
)

func main() {
	outputDir := flag.String("output-dir", "frontend/vendor/base_apidoc", "Directory that receives open_apidoc*.json")
	title := flag.String("title", "Base HUB", "API title")
	version := flag.String("version", "1.0.0", "API version")
	flag.Parse()

	// DescriptionFile markdown is read from the embedded documents, same as the server.
	apiDocs, err := fs.Sub(documents.FS, "apidoc")
	if err != nil {
		log.Fatal(err)
	}
	orm.SetDescriptionFS(apiDocs)
	// Module documents are read from modules/<m>/apidoc on disk (cwd is app).
	orm.SetModuleDescriptionFS(os.DirFS("modules"))

	// Models register inside each module's Bootstrap, exactly as the server does at startup.
	shutdown, _, err := kernelmodules.BootstrapAll(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	defer shutdown()

	if err := apidoc.WriteAliasOpenAPIFiles(*outputDir, apidoc.Config{Title: *title, Version: *version}); err != nil {
		log.Fatal(err)
	}
}
