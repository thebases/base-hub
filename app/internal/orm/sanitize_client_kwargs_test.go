package orm

import "testing"

func TestSanitizeClientKwargsDropsEnvironmentOverrides(t *testing.T) {
	in := map[string]any{
		"sudo":  true,
		"uid":   float64(1),
		"limit": 5,
		"context": map[string]any{
			"lang":                   "vi_vn",
			"allowed_company_ids":    []any{float64(2)},
			"authorized_company_ids": []any{float64(1), float64(2)},
			RBACContextRoleIDKey:     float64(1),
			RBACContextGroupIDsKey:   []any{float64(1)},
			RBACContextAuthModeKey:   "x",
			ContextKeyDatabaseName:   "other",
			ContextKeyInstallMode:    true,
			ReadOnlyEnvContextKey:    false,
		},
	}
	out := SanitizeClientKwargs(in)
	if _, ok := out["sudo"]; ok {
		t.Fatal("sudo must be dropped")
	}
	if _, ok := out["uid"]; ok {
		t.Fatal("uid must be dropped")
	}
	if out["limit"] != 5 {
		t.Fatal("ordinary kwargs must survive")
	}
	ctx := out["context"].(map[string]any)
	for _, k := range clientReservedContextKeys {
		if _, ok := ctx[k]; ok {
			t.Fatalf("reserved context key %q must be dropped", k)
		}
	}
	if ctx["lang"] != "vi_vn" || ctx["allowed_company_ids"] == nil {
		t.Fatal("UI context hints must survive")
	}
	// input must not be mutated
	if _, ok := in["sudo"]; !ok {
		t.Fatal("input map mutated")
	}
	if _, ok := in["context"].(map[string]any)[RBACContextRoleIDKey]; !ok {
		t.Fatal("input context mutated")
	}
}

func TestSanitizeClientKwargsNonMapContextAndNil(t *testing.T) {
	if SanitizeClientKwargs(nil) != nil {
		t.Fatal("nil in, nil out")
	}
	out := SanitizeClientKwargs(map[string]any{"context": "bad"})
	if _, ok := out["context"]; ok {
		t.Fatal("non-map context must be dropped")
	}
}
