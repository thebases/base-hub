package main

// File overview:
// - Responsibility: contains build-time generation logic for main, keeping codegen reproducible and versioned with the backend.
// - Key symbols: `main`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"flag"
	"log"

	"git.thebase.vn/base_hub/internal/modulesgen"
)

// main is the process entrypoint and hands control to the configured application bootstrap path.
func main() {
	modulesRoot := flag.String("modules-root", "../", "Path to the backend modules root")
	importBase := flag.String("import-base", "git.thebase.vn/base_hub/modules", "Go import prefix for backend modules")
	outputPath := flag.String("output", "./modules_gen.go", "Path to the generated import file")
	// Parse the incoming payload into the shape the rest of the function expects.
	flag.Parse()

	if err := modulesgen.GenerateModuleImports(modulesgen.GenerateOptions{
		ModulesRoot: *modulesRoot,
		ImportBase:  *importBase,
		OutputPath:  *outputPath,
	}); err != nil {
		log.Fatal(err)
	}
}
