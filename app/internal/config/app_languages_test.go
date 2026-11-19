package config

import (
	"reflect"
	"testing"
)

func TestAppLanguages(t *testing.T) {
	cases := map[string][]string{
		"":              {"en_us", "vi_vn"},
		" , ":           {"en_us", "vi_vn"},
		"en,vi,jp":      {"en_us", "vi_vn", "ja_jp"},
		"en_GB,vi-VN":   {"en_gb", "vi_vn"},
		" fr , fr, de ": {"fr_fr", "de_de"},
	}
	for raw, want := range cases {
		cfg := AppConfig{App: AppSettings{Language: raw}}
		if got := cfg.AppLanguages(); !reflect.DeepEqual(got, want) {
			t.Fatalf("AppLanguages(%q) = %v, want %v", raw, got, want)
		}
	}
}
