package orm

import (
	"testing"
	"testing/fstest"
)

func TestReadDescriptionFile(t *testing.T) {
	SetDescriptionFS(fstest.MapFS{"a/doc.md": {Data: []byte("# Hi\n")}})
	t.Cleanup(func() { SetDescriptionFS(nil) })

	cases := map[string]struct {
		want string
		ok   bool
	}{
		"a/doc.md":          {"# Hi", true},
		"/../a/../a/doc.md": {"# Hi", true},
		"missing.md":        {"", false},
		"":                  {"", false},
	}
	for in, c := range cases {
		got, ok := ReadDescriptionFile(in)
		if got != c.want || ok != c.ok {
			t.Errorf("ReadDescriptionFile(%q) = %q,%v want %q,%v", in, got, ok, c.want, c.ok)
		}
	}
}
