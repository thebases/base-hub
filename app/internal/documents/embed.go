package documents

// File overview:
// - Responsibility: bundles the kernel-level documents (`apidoc/<module>/<lang>/...`, `helper/<module>/<lang>/...`) into the binary so DescriptionFile references and the Instruction pages resolve without a runtime file layout.
// - Key symbols: `FS`, `ListHelper`, `ReadHelper`.

import "embed"

// FS holds the kernel documents. `all:` keeps dot- and underscore-prefixed files; sub-folders are
// imported recursively. The entrypoint passes fs.Sub(FS, "apidoc") to orm.SetDescriptionFS; module
// api documents (modules/<m>/apidoc) travel with the embedded modules bundle instead.
//
//go:embed all:apidoc all:helper
var FS embed.FS
