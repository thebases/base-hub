// Package all blank-imports every generated backend module package.
//
//go:generate go run ../../internal/tools/generate_module_imports
package all

// File overview:
// - Responsibility: provides bootstrap or aggregation glue for the `all` module in doc.
// - Key symbols: package init hooks and file-local helpers.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.
