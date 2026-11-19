package bootstrap

import "testing"

func TestLegacyLuaSourceAndRewrite(t *testing.T) {
	params := map[string]any{"lua_code": "return payload", "data": map[string]any{"a": 1}, "code": "  "}
	if got := legacyLuaSource(params); got != "return payload" {
		t.Fatalf("legacyLuaSource = %q", got)
	}
	if legacyLuaSource(map[string]any{"automation_action_id": 3}) != "" {
		t.Fatal("a step with no inline source must report none")
	}
	next := rewriteLuaStepParams(params, 9)
	if next["automation_action_id"] != int64(9) || next["data"] == nil {
		t.Fatalf("rewrite lost keys: %#v", next)
	}
	for _, key := range legacyLuaCodeKeys {
		if _, ok := next[key]; ok {
			t.Fatalf("inline key %q must be removed", key)
		}
	}
	if _, ok := params["lua_code"]; !ok {
		t.Fatal("rewrite must not mutate its input")
	}
}
