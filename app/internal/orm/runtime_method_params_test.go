package orm

import (
	"context"
	"strings"
	"testing"
)

func TestNormalizeRuntimeMethodParamsDefaultsAndRejects(t *testing.T) {
	params, err := normalizeRuntimeMethodParams("m", []RuntimeMethodParam{{Name: " amount ", Type: "Float"}, {Name: "note"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params[0].Name != "amount" || params[0].Type != RuntimeMethodParamFloat || params[0].Label != "amount" {
		t.Fatalf("param 0 not normalized: %+v", params[0])
	}
	if params[1].Type != RuntimeMethodParamChar {
		t.Fatalf("empty type should default to char, got %q", params[1].Type)
	}

	bad := map[string][]RuntimeMethodParam{
		"empty name":       {{Name: " "}},
		"duplicate":        {{Name: "a"}, {Name: "a"}},
		"unknown type":     {{Name: "a", Type: "blob"}},
		"selection no opt": {{Name: "a", Type: "selection"}},
	}
	for name, input := range bad {
		if _, err := normalizeRuntimeMethodParams("m", input); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestValidateRuntimeMethodKwargs(t *testing.T) {
	method := RuntimeMethod{Name: "do", Params: []RuntimeMethodParam{
		{Name: "qty", Label: "Quantity", Type: RuntimeMethodParamInteger, Required: true},
		{Name: "rate", Label: "Rate", Type: RuntimeMethodParamFloat},
		{Name: "force", Label: "Force", Type: RuntimeMethodParamBoolean, Required: true},
		{Name: "mode", Label: "Mode", Type: RuntimeMethodParamSelection, Selection: []SelectionOption{{Value: "a", Label: "A"}}},
	}}
	cases := []struct {
		name    string
		kwargs  map[string]any
		wantErr string
	}{
		{"ok minimal (false satisfies required bool)", map[string]any{"qty": float64(3), "force": false}, ""},
		{"ok full + undeclared passthrough", map[string]any{"qty": 3, "rate": 1.5, "force": true, "mode": "a", "context": map[string]any{}}, ""},
		{"missing required", map[string]any{"force": true}, `"Quantity" is required`},
		{"blank required string", map[string]any{"qty": "  ", "force": true}, `"Quantity" is required`},
		{"non-integer", map[string]any{"qty": 1.5, "force": true}, `"Quantity" must be a valid integer`},
		{"wrong bool type", map[string]any{"qty": 1, "force": "yes"}, `"Force" must be a valid boolean`},
		{"unknown option", map[string]any{"qty": 1, "force": true, "mode": "z"}, `"Mode" must be a valid selection`},
	}
	for _, tc := range cases {
		err := ValidateRuntimeMethodKwargs(method, nil, tc.kwargs)
		if tc.wantErr == "" && err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
		}
		if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.wantErr, err)
		}
	}
}

func TestValidateRuntimeMethodKwargsPositionalSkipsRequired(t *testing.T) {
	method := RuntimeMethod{Name: "change_password", Params: []RuntimeMethodParam{{Name: "new_password", Label: "New Password", Type: RuntimeMethodParamPassword, Required: true}}}
	if err := ValidateRuntimeMethodKwargs(method, []any{"secret"}, map[string]any{}); err != nil {
		t.Fatalf("positional call must not fail the required check: %v", err)
	}
	if err := ValidateRuntimeMethodKwargs(method, nil, map[string]any{}); err == nil {
		t.Fatalf("kwargs-only call without the required param must fail")
	}
	if err := ValidateRuntimeMethodKwargs(method, []any{"x"}, map[string]any{"new_password": 5}); err == nil {
		t.Fatalf("a present kwarg is still type-checked on a positional call")
	}
}

func TestMergeRuntimeMethodsCarriesParams(t *testing.T) {
	call := func(context.Context, *ModelMetadata, []string, []any, map[string]any) (any, error) { return nil, nil }
	methods, metadata, err := mergeRuntimeMethods([]RuntimeMethod{{Name: "zz_with_params", Public: true, ModelOnly: true, Call: call,
		Params: []RuntimeMethodParam{{Name: "x", Required: true}}}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := methods["zz_with_params"].Params; len(got) != 1 || got[0].Type != RuntimeMethodParamChar {
		t.Fatalf("params not normalized onto method: %+v", got)
	}
	for _, item := range metadata {
		if item.Name == "zz_with_params" && len(item.Params) == 1 {
			return
		}
	}
	t.Fatalf("params missing from metadata view")
}

// TestMergeRuntimeMethodsAliasOnlyEntry covers an alias-only declaration (nil Call): it keeps the
// inherited default body and flags, gains the routes, and an unknown name without a body still
// fails boot. Route normalization canonicalizes both alias forms.
func TestMergeRuntimeMethodsAliasOnlyEntry(t *testing.T) {
	methods, metadata, err := mergeRuntimeMethods([]RuntimeMethod{
		{Name: "read", AliasRoute: "get /base/things/:id/", Aliases: []MethodAlias{{Route: "/legacy/read/"}}},
	})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	read := methods["read"]
	if read.Call == nil || !read.ReadOnly || !read.UsesPathID {
		t.Fatalf("alias-only entry lost the default body/flags: %#v", read)
	}
	if read.AliasRoute != "GET /base/things/:id" || read.Aliases[0].Route != "legacy/read" {
		t.Fatalf("routes not normalized: %q %q", read.AliasRoute, read.Aliases[0].Route)
	}
	for _, meta := range metadata {
		if meta.Name == "read" && (len(meta.AliasRoutes) != 1 || meta.AliasRoutes[0] != "legacy/read") {
			t.Fatalf("metadata AliasRoutes = %#v", meta.AliasRoutes)
		}
	}
	if _, _, err := mergeRuntimeMethods([]RuntimeMethod{{Name: "no_such_method", AliasRoute: "GET /x"}}); err == nil {
		t.Fatal("alias-only entry for an unknown method must fail")
	}
	if verb, path, rest := ParseAliasRoute("orders/:id/cancel"); verb != "POST" || path != "/base/orders/:id/cancel" || rest {
		t.Fatalf("legacy parse = %s %s %v", verb, path, rest)
	}
}
