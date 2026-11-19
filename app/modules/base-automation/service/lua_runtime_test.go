package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunLuaActionReturnsTableResult(t *testing.T) {
	result, err := RunLuaAction(context.Background(), `
		local total = payload.a + payload.b
		return {sum = total}
	`, map[string]any{"a": float64(2), "b": float64(3)})
	if err != nil {
		t.Fatalf("RunLuaAction returned error: %v", err)
	}
	sum, ok := result["sum"].(float64)
	if !ok || sum != 5 {
		t.Fatalf("RunLuaAction result = %#v, want sum=5", result)
	}
}

func TestRunLuaActionWithoutReturnYieldsEmptyResult(t *testing.T) {
	result, err := RunLuaAction(context.Background(), `local x = 1`, nil)
	if err != nil {
		t.Fatalf("RunLuaAction returned error: %v", err)
	}
	if len(result) != 0 {
		t.Fatalf("RunLuaAction result = %#v, want empty map", result)
	}
}

func TestRunLuaActionSandboxHasNoFileOrOsAccess(t *testing.T) {
	for _, script := range []string{
		`return {ok = (os == nil)}`,
		`return {ok = (io == nil)}`,
		`return {ok = (dofile == nil)}`,
		`return {ok = (loadfile == nil)}`,
		`return {ok = (require == nil)}`,
	} {
		result, err := RunLuaAction(context.Background(), script, nil)
		if err != nil {
			t.Fatalf("script %q returned error: %v", script, err)
		}
		if ok, _ := result["ok"].(bool); !ok {
			t.Fatalf("script %q result = %#v, want ok=true (global must be nil)", script, result)
		}
	}
}

func TestRunLuaActionTimeoutCancelsRunawayScript(t *testing.T) {
	previous := luaActionTimeout
	luaActionTimeout = 200 * time.Millisecond
	t.Cleanup(func() { luaActionTimeout = previous })

	start := time.Now()
	_, err := RunLuaAction(context.Background(), `while true do end`, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatalf("RunLuaAction did not return an error for a runaway script")
	}
	if !strings.Contains(err.Error(), "context") {
		t.Fatalf("RunLuaAction error = %v, want a context-cancellation error", err)
	}
	if elapsed > luaActionTimeout+2*time.Second {
		t.Fatalf("RunLuaAction took %v, want it bounded near luaActionTimeout=%v", elapsed, luaActionTimeout)
	}
}

type fakeLuaHost struct{ got []string }

func (f *fakeLuaHost) CallModelMethod(_ context.Context, model, method string, ids []string, kwargs map[string]any) (any, error) {
	f.got = append(f.got, model+"."+method)
	return map[string]any{"ok": true, "n": kwargs["n"]}, nil
}

func TestRunLuaActionExposesBaseCall(t *testing.T) {
	host := &fakeLuaHost{}
	prev := luaActionHost
	luaActionHost = host
	defer func() { luaActionHost = prev }()

	result, err := RunLuaAction(context.Background(), `local r = base.call("x.y", "m", {n = 3}); return {ok = r.ok, n = r.n}`, map[string]any{})
	if err != nil {
		t.Fatalf("RunLuaAction() error = %v", err)
	}
	if result["ok"] != true || result["n"] != float64(3) || len(host.got) != 1 || host.got[0] != "x.y.m" {
		t.Fatalf("result=%#v calls=%#v", result, host.got)
	}
}

func TestRunLuaActionRefusesSelfReferencingResult(t *testing.T) {
	_, err := RunLuaAction(context.Background(), `local t = {} t.self = t return t`, nil)
	if err == nil || !strings.Contains(err.Error(), "contains itself") {
		t.Fatalf("error = %v, want cycle error", err)
	}
}

func TestRunLuaActionAllowsSharedSubtable(t *testing.T) {
	result, err := RunLuaAction(context.Background(), `local s = {v = 1} return {a = s, b = s}`, nil)
	if err != nil {
		t.Fatalf("error = %v, want nil", err)
	}
	if _, ok := result["b"].(map[string]any); !ok {
		t.Fatalf("result = %#v, want b converted", result)
	}
}

func TestRunLuaActionRefusesDeepResult(t *testing.T) {
	_, err := RunLuaAction(context.Background(), `
		local root = {}
		local cur = root
		for i = 1, 200 do cur.next = {} cur = cur.next end
		return root`, nil)
	if err == nil || !strings.Contains(err.Error(), "nested deeper") {
		t.Fatalf("error = %v, want depth error", err)
	}
}

func TestRunLuaActionCycleInHostCallsIsLuaError(t *testing.T) {
	_, err := RunLuaAction(context.Background(), `local t = {} t[1] = t return {s = base.json_encode(t)}`, nil)
	if err == nil || !strings.Contains(err.Error(), "json_encode") {
		t.Fatalf("error = %v, want json_encode error", err)
	}
}

func TestRunLuaActionCapsStringRep(t *testing.T) {
	_, err := RunLuaAction(context.Background(), `return {s = string.rep("x", 1e12)}`, nil)
	if err == nil || !strings.Contains(err.Error(), "string.rep") {
		t.Fatalf("error = %v, want string.rep cap error", err)
	}
	result, err := RunLuaAction(context.Background(), `return {s = string.rep("ab", 3)}`, nil)
	if err != nil || result["s"] != "ababab" {
		t.Fatalf("result = %#v, err = %v, want ababab", result, err)
	}
}

func TestRunLuaScriptRefusesReservedGlobals(t *testing.T) {
	for _, name := range []string{"payload", "base", "string", "pcall"} {
		_, err := RunLuaScript(context.Background(), LuaScript{Code: `return {}`, Globals: map[string]any{name: "x"}})
		if err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Fatalf("global %q error = %v, want reserved", name, err)
		}
	}
	result, err := RunLuaScript(context.Background(), LuaScript{Code: `return {t = topic}`, Globals: map[string]any{"topic": "a/b"}})
	if err != nil || result["t"] != "a/b" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
}
