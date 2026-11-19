package cmd

import "testing"

func TestInjectThemeColor(t *testing.T) {
	in := []byte(`<head><meta name="theme-color" content="#000000" /></head>`)
	got := string(injectThemeColor(in, `#12"34$1`))
	want := `<head><meta name="theme-color" content="#12&#34;34$1" /></head>`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}
