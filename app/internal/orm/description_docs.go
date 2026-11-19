package orm

// File overview:
// - Responsibility: reads the bundled markdown/text documents referenced by ModelSpec.DescriptionFile and RuntimeMethod.DescriptionFile (layout `<module>/<lang>/<file>`; root: internal/documents/apidoc, then each module's `apidoc/`).
// - Key symbols: `SetDescriptionFS`, `SetModuleDescriptionFS`, `ReadDescriptionFile`.
// - Pattern: the filesystem is injected by the entrypoint (embedded at build time) so orm never imports data packages; reads are lazy because specs register before the entrypoint wires the FS.

import (
	"io/fs"
	"os"
	"path"
	"strings"
	"sync/atomic"
)

var descriptionFS atomic.Pointer[fs.FS]

// devDescriptionFS reads the on-disk api documents folder relative to the working directory.
var devDescriptionFS = os.DirFS("internal/documents/apidoc")

var moduleFS atomic.Pointer[fs.FS]

// devModuleFS reads app/modules from disk for the `go run` flow.
var devModuleFS = os.DirFS("modules")

// SetModuleDescriptionFS registers the filesystem rooted at the modules folder (the embedded
// `modules` bundle): "<module>/<rest>" resolves to "<module>/apidoc/<rest>".
func SetModuleDescriptionFS(fsys fs.FS) {
	if fsys == nil {
		moduleFS.Store(nil)
		return
	}
	moduleFS.Store(&fsys)
}

// SetDescriptionFS registers the filesystem (rooted at the api documents folder) used to
// read description files. Passing nil disables file reads.
func SetDescriptionFS(fsys fs.FS) {
	if fsys == nil {
		descriptionFS.Store(nil)
		return
	}
	descriptionFS.Store(&fsys)
}

// ReadDescriptionFile returns the trimmed contents of the document at ref (relative to the
// api documents root). ok is false when ref is empty, escapes the root, or cannot be read.
func ReadDescriptionFile(ref string) (string, bool) {
	// path.Clean on a rooted path drops any "../" so a ref can never leave the root.
	ref = strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(ref)), "/")
	fsys := descriptionFS.Load()
	if ref == "" {
		return "", false
	}
	// `go run ./main.go` compiles without main_embed.go, so no FS is injected in that dev flow;
	// fall back to the source folder (cwd is app there) instead of hiding every document.
	var reader fs.FS = devDescriptionFS
	if fsys != nil {
		reader = *fsys
	}
	data, err := fs.ReadFile(reader, ref)
	if err != nil {
		// Not in the root: look in the owning module's apidoc folder.
		module, rest, found := strings.Cut(ref, "/")
		if !found {
			return "", false
		}
		var modules fs.FS = devModuleFS
		if mf := moduleFS.Load(); mf != nil {
			modules = *mf
		}
		data, err = fs.ReadFile(modules, module+"/apidoc/"+rest)
		if err != nil {
			return "", false
		}
	}
	return strings.TrimSpace(string(data)), true
}
