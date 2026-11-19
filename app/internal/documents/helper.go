package documents

// File overview:
// - Responsibility: lists and reads the "Instruction" documents: kernel docs (`helper/<module>/<lang>/...`) plus each installed module's `<module>/documents/helper/<lang>/...`.
// - Key symbols: `HelperItem`, `HelperModule`, `ListHelper`, `ReadHelper`.
// - Pattern: module files ride in the embedded modules bundle at build time; "un-embedding" on uninstall is the install gate (`installed`), same rule as module static files. The package stays a leaf: callers inject the module tree and the install check.

import (
	"io/fs"
	"path"
	"sort"
	"strings"
)

// HelperItem is one markdown instruction document, addressed by Path relative to its language folder.
type HelperItem struct {
	Path  string `json:"path"`
	Title string `json:"title"`
}

// HelperModule groups the instruction documents of one module.
type HelperModule struct {
	Module string       `json:"module"`
	Items  []HelperItem `json:"items"`
}

// Installed reports whether a module is installed for the current request.
type Installed func(module string) (bool, error)

// helperSources returns the language folders that may hold module's docs, kernel first.
func helperSources(kernel, modules fs.FS, module, lang string) []fs.FS {
	var out []fs.FS
	add := func(fsys fs.FS, dir string) {
		if fsys == nil {
			return
		}
		// fs.Sub succeeds for missing folders, so confirm the folder exists first.
		if info, err := fs.Stat(fsys, dir); err != nil || !info.IsDir() {
			return
		}
		if sub, err := fs.Sub(fsys, dir); err == nil {
			out = append(out, sub)
		}
	}
	// API docs use the "vn" language code but helper folders are named "vi", so accept both spellings.
	candidates := []string{lang, "en"}
	if lang == "vn" {
		candidates = []string{"vn", "vi", "en"}
	}
	for _, l := range candidates {
		if l == "" || strings.ContainsAny(l, `/\.`) {
			continue
		}
		add(kernel, module+"/"+l)
		add(modules, module+"/documents/helper/"+l)
		if len(out) > 0 {
			break // the requested language exists; fall back to English only when it has no docs at all
		}
	}
	return out
}

func kernelHas(kernel fs.FS, module string) bool {
	if kernel == nil {
		return false
	}
	_, err := fs.Stat(kernel, module)
	return err == nil
}

// ListHelper returns the instruction documents per module: the kernel's own plus those of installed modules, sorted by module name.
func ListHelper(kernel, modules fs.FS, installed Installed, lang string) ([]HelperModule, error) {
	names := map[string]bool{}
	for _, fsys := range []fs.FS{kernel, modules} {
		if fsys == nil {
			continue
		}
		entries, err := fs.ReadDir(fsys, ".")
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() {
				names[e.Name()] = true
			}
		}
	}
	var result []HelperModule
	for name := range names {
		if !kernelHas(kernel, name) {
			ok, err := installed(name)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
		}
		seen := map[string]bool{}
		var items []HelperItem
		for _, src := range helperSources(kernel, modules, name, lang) {
			_ = fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(p), ".md") || seen[p] {
					return nil
				}
				seen[p] = true
				body, _ := fs.ReadFile(src, p)
				items = append(items, HelperItem{Path: p, Title: markdownTitle(p, string(body))})
				return nil
			})
		}
		if len(items) == 0 {
			continue
		}
		sort.Slice(items, func(i, j int) bool { return items[i].Path < items[j].Path })
		result = append(result, HelperModule{Module: name, Items: items})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Module < result[j].Module })
	return result, nil
}

// ReadHelper returns the document at rel for module in lang (English fallback). ok is false when the
// module is neither kernel-owned nor installed, the path escapes the folder, or the file is missing.
func ReadHelper(kernel, modules fs.FS, installed Installed, module, lang, rel string) (string, bool, error) {
	module = strings.TrimSpace(module)
	if module == "" || strings.ContainsAny(module, `/\.`) {
		return "", false, nil
	}
	if !kernelHas(kernel, module) {
		ok, err := installed(module)
		if err != nil || !ok {
			return "", false, err
		}
	}
	// path.Clean on a rooted path drops "../" so rel cannot leave the language folder.
	rel = strings.TrimPrefix(path.Clean("/"+rel), "/")
	if rel == "" {
		return "", false, nil
	}
	for _, src := range helperSources(kernel, modules, module, lang) {
		if raw, err := fs.ReadFile(src, rel); err == nil {
			return string(raw), true, nil
		}
	}
	return "", false, nil
}

// markdownTitle uses the first "# " heading, else the file name without extension.
func markdownTitle(p, body string) string {
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return strings.TrimSuffix(path.Base(p), path.Ext(p))
}
