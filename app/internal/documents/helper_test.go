package documents

import (
	"testing"
	"testing/fstest"
)

func TestHelperListAndReadGatedByInstall(t *testing.T) {
	kernel := fstest.MapFS{"base/en/README.md": {Data: []byte("# Kernel\n")}}
	modules := fstest.MapFS{
		"m/documents/helper/en/README.md": {Data: []byte("# M guide\n")},
		"m/documents/helper/vn/README.md": {Data: []byte("# M vn\n")},
		"x/documents/helper/en/a.md":      {Data: []byte("x")},
	}
	installed := func(name string) (bool, error) { return name == "m", nil }

	list, err := ListHelper(kernel, modules, installed, "vn")
	if err != nil || len(list) != 2 || list[0].Module != "base" || list[1].Module != "m" || list[1].Items[0].Title != "M vn" {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if body, ok, _ := ReadHelper(kernel, modules, installed, "m", "fr", "README.md"); !ok || body != "# M guide\n" {
		t.Fatalf("english fallback read = %q %v", body, ok)
	}
	if _, ok, _ := ReadHelper(kernel, modules, installed, "x", "en", "a.md"); ok {
		t.Fatal("uninstalled module must not serve docs")
	}
	if _, ok, _ := ReadHelper(kernel, modules, installed, "m", "en", "../../x/documents/helper/en/a.md"); ok {
		t.Fatal("path escape must not resolve")
	}
}

// Helper folders are named "vi" while the normalized language code is "vn"; both must resolve.
func TestHelperViFolderServedForVn(t *testing.T) {
	modules := fstest.MapFS{
		"m/documents/helper/en/README.md": {Data: []byte("# M en\n")},
		"m/documents/helper/vi/README.md": {Data: []byte("# M vi\n")},
	}
	installed := func(string) (bool, error) { return true, nil }
	if body, ok, _ := ReadHelper(nil, modules, installed, "m", "vn", "README.md"); !ok || body != "# M vi\n" {
		t.Fatalf("vn read = %q %v", body, ok)
	}
	list, _ := ListHelper(nil, modules, installed, "vn")
	if len(list) != 1 || list[0].Items[0].Title != "M vi" {
		t.Fatalf("vn list = %+v", list)
	}
}
