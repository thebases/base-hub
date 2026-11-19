package i18n

// File overview:
// - Responsibility: walk a module fs.FS and load all */i18n/*.po and */i18n/*.pot
//   files into the global translation registry.
// - Key symbols: LoadFromFS, langFromFilename.
// - Why: at startup the main binary embeds the entire modules/ tree via
//   //go:embed. Walking it here keeps the loading logic decoupled from which
//   specific modules exist — new modules with i18n/ directories are picked up
//   automatically without any code changes.
//
// Naming convention (<lang>.po per module):
//   <module>/i18n/<module>.pot   → language "en_us"  (source-language template)
//   <module>/i18n/vi_vn.po       → language "vi_vn"
//   <module>/i18n/en_gb.po       → language "en_gb"
//
// A malformed file is silently skipped so one bad translation file does not
// prevent the application from starting; only filesystem I/O errors bubble up.

import (
	"bytes"
	"io/fs"
	"path"
	"strings"
)

// LoadFromFS walks fsys looking for files whose parent directory is named "i18n"
// and whose extension is ".po" or ".pot". It parses each file and loads the
// resulting entries into the global default registry.
//
// fsys is the embedded modules filesystem (rooted at modules/), so paths inside
// look like "base/i18n/vi.po" or "workflow/i18n/workflow.pot".
func LoadFromFS(fsys fs.FS) error {
	if fsys == nil {
		return nil
	}
	return fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}

		// Restrict to files that live directly inside an "i18n" directory.
		// path.Dir("base/i18n/vi.po") == "base/i18n", path.Base(...) == "i18n".
		if path.Base(path.Dir(p)) != "i18n" {
			return nil
		}

		ext := strings.ToLower(path.Ext(d.Name()))
		if ext != ".po" && ext != ".pot" {
			return nil
		}

		lang := langFromFilename(d.Name(), ext)
		if lang == "" {
			return nil
		}

		data, readErr := fs.ReadFile(fsys, p)
		if readErr != nil {
			// I/O failure is a real error; surface it.
			return readErr
		}

		entries, parseErr := ParsePO(bytes.NewReader(data))
		if parseErr != nil {
			// Parse failure is non-fatal: the file may be empty or still being
			// edited. Logging is intentionally omitted to avoid importing a logger
			// into this low-level package; callers can wrap LoadFromFS if needed.
			return nil
		}

		defaultRegistry.LoadEntries(lang, entries)
		return nil
	})
}

// langFromFilename derives the language code from a translation filename.
//
// For .pot files:
//   - The stem (e.g. "base" from "base.pot") is the module name; the file
//     contains the English source strings with empty msgstr fields.
//   - We load them as language "en" so that T("en", msgid) still returns the
//     msgid via the fallback, consistent with how source-language strings work.
//
// For .po files:
//   - The stem is the IETF / XPG language code: "vi_vn", "en_gb", "zh_tw", etc. (normalized by NormalizeLang)
//   - We use the stem directly as the registry language key.
func langFromFilename(name, ext string) string {
	stem := strings.TrimSuffix(name, ext)
	stem = strings.TrimSpace(stem)
	if stem == "" {
		return ""
	}
	if ext == ".pot" {
		// .pot files are source-language templates; map to "en".
		return "en_us"
	}
	return NormalizeLang(stem)
}
