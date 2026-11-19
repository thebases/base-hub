package i18n

import "testing"

func TestNormalizeLang(t *testing.T) {
	cases := map[string]string{
		"":        "",
		"vi":      "vi_vn",
		"VI_VN":   "vi_vn",
		"en-GB":   "en_gb",
		"en_US":   "en_us",
		"en":      "en_us",
		"jp":      "ja_jp",
		"jp_JP":   "ja_jp",
		"xx":      "xx",
		" fr-CA ": "fr_ca",
	}
	for in, want := range cases {
		if got := NormalizeLang(in); got != want {
			t.Fatalf("NormalizeLang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRegistryAcceptsAnyLangSpelling(t *testing.T) {
	r := NewRegistry()
	r.LoadEntries("vi_vn", []Entry{{MsgID: "Save", MsgStr: "Lưu"}})
	for _, lang := range []string{"vi", "vi_VN", "vi-vn"} {
		if got := r.Translate(lang, "Save"); got != "Lưu" {
			t.Fatalf("Translate(%q) = %q", lang, got)
		}
	}
}

func TestLangForCountry(t *testing.T) {
	cases := map[string]string{"VN": "vi_vn", "us": "en_us", " JP ": "ja_jp", "": "", "ZZ": ""}
	for in, want := range cases {
		if got := LangForCountry(in); got != want {
			t.Errorf("LangForCountry(%q) = %q, want %q", in, got, want)
		}
	}
}
