package i18n

// File overview:
// - Responsibility: thread-safe in-memory translation registry and process-global T() helper.
// - Key symbols: Registry, T, DefaultRegistry.
// - Why: a keyed map is the minimal safe structure for concurrent reads from many
//   request goroutines while a single loader goroutine populates it at boot time.
//   Storing (lang, msgid) pairs avoids any language-prefix overhead in hot paths.

import "sync"

// translationKey uniquely addresses one translated message across all languages.
type translationKey struct {
	lang  string // canonical code from NormalizeLang, e.g. "vi_vn", "en_us"
	msgid string // source string exactly as it appears in the .pot / .po file
}

// Registry is a thread-safe in-memory store for translated strings.
// Keys are (language_code, msgid) pairs; values are translated strings (msgstr).
type Registry struct {
	mu           sync.RWMutex
	translations map[translationKey]string
}

// NewRegistry returns an empty, ready-to-use registry.
func NewRegistry() *Registry {
	return &Registry{
		translations: make(map[translationKey]string),
	}
}

// LoadEntries bulk-stores all entries for a given language code.
// Entries with an empty MsgID are silently skipped (they are file header blocks
// that the parser did not filter out).
// This method acquires the write lock once for the entire batch, keeping the
// critical section short regardless of how many strings are loaded.
func (r *Registry) LoadEntries(lang string, entries []Entry) {
	lang = NormalizeLang(lang)
	if lang == "" || len(entries) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range entries {
		if e.MsgID == "" {
			continue
		}
		r.translations[translationKey{lang: lang, msgid: e.MsgID}] = e.MsgStr
	}
}

// Translate returns the translated string for (lang, msgid).
// It falls back to msgid itself when:
//   - no translation exists for the language, or
//   - the stored msgstr is empty (an untranslated entry in a .po file).
//
// This fallback is intentional: untranslated strings are shown in the
// source language rather than as blank values.
func (r *Registry) Translate(lang, msgid string) string {
	lang = NormalizeLang(lang)
	if lang == "" || msgid == "" {
		return msgid
	}
	r.mu.RLock()
	msgstr, ok := r.translations[translationKey{lang: lang, msgid: msgid}]
	r.mu.RUnlock()
	if ok && msgstr != "" {
		return msgstr
	}
	return msgid
}

// TranslateMany returns translated strings for each msgid in order.
// It holds the read lock for the entire slice to avoid per-call lock overhead
// in hot paths (e.g. rendering a large form with many labels).
// Missing or empty translations fall back to the original msgid.
func (r *Registry) TranslateMany(lang string, msgids []string) []string {
	out := make([]string, len(msgids))
	lang = NormalizeLang(lang)
	if lang == "" {
		copy(out, msgids)
		return out
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for i, msgid := range msgids {
		if msgid == "" {
			out[i] = msgid
			continue
		}
		if msgstr, ok := r.translations[translationKey{lang: lang, msgid: msgid}]; ok && msgstr != "" {
			out[i] = msgstr
		} else {
			out[i] = msgid
		}
	}
	return out
}

// defaultRegistry is the process-global translation registry populated by the
// base module's bootstrap path (LoadFromFS) and queried by every T() call.
var defaultRegistry = NewRegistry()

// DefaultRegistry exposes the process-global registry so external packages can
// call LoadEntries directly (e.g. for database-backed translations).
func DefaultRegistry() *Registry {
	return defaultRegistry
}

// T translates msgid into lang using the global default registry.
// Returns msgid unchanged when no translation exists, which keeps the
// application functional even when a language file is incomplete.
func T(lang, msgid string) string {
	return defaultRegistry.Translate(lang, msgid)
}
