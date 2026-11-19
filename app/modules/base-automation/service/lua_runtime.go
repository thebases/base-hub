package service

// File overview:
// - Responsibility: runs a user-authored Lua script inside a sandboxed gopher-lua VM so
//   automation.action rows of action_type "lua" (and base-workflow's lua_script@v1 node) can
//   execute arbitrary logic without touching the filesystem, network, or host process — this is a
//   financial backend, untrusted script execution must stay bounded and auditable.
// - Key symbols: `RunLuaAction`, `RunLuaScript`, `LuaScript`, `LuaHost`, `newSandboxedLuaState`,
//   `goValueToLua`, `luaValueToGo`.
// - Host bridge: when a caller passes a LuaHost, the script gets a `base` table whose `call`
//   reaches model runtime methods through the host. The VM itself still has no I/O; every side
//   effect goes through the host, so the host decides the database and permission scope.

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	lua "github.com/yuin/gopher-lua"
)

// luaActionTimeout bounds one script execution so a runaway or malicious
// script (e.g. an infinite loop) cannot occupy a scheduler or RPC worker
// indefinitely. gopher-lua checks context cancellation at VM instruction
// boundaries once SetContext is used, so this timeout is enforced even
// without I/O calls inside the script. It is a var (not const) so tests can
// shrink it instead of spending real wall-clock time on a runaway script.
var luaActionTimeout = 30 * time.Second

// Bounds on what a script can make the host build (plan T24). gopher-lua has no allocator hook, so
// these cap the amplifiers the host controls: Lua→Go conversion (results, base.call kwargs,
// json_encode) and string.rep. A script can still grow memory with plain concatenation until the
// timeout; a hard VM memory cap needs a process-level limit.
const (
	// luaMaxConvertDepth bounds table nesting; deeper tables (or a cycle) would otherwise recurse
	// until the Go stack overflows, which is fatal for the whole process, not a recoverable panic.
	luaMaxConvertDepth = 64
	// luaMaxConvertValues bounds how many values one conversion emits; shared sub-tables count each
	// time they appear, so a small DAG cannot expand into a huge result.
	luaMaxConvertValues = 100_000
	// luaMaxConvertBytes bounds the total string bytes one conversion emits.
	luaMaxConvertBytes = 16 << 20
	// luaMaxStringRepBytes bounds one string.rep result.
	luaMaxStringRepBytes = 1 << 20
	// luaCallStackSize bounds Lua call depth (recursion) per VM.
	luaCallStackSize = 200
	// luaRegistryMaxSize bounds the VM value stack.
	luaRegistryMaxSize = 256 * 1024
)

// LuaHost is the side-effect bridge exposed to a script as `base.call`. Implementations run the
// call in the caller's context (database, environment, transaction), never a fresh one.
type LuaHost interface {
	CallModelMethod(ctx context.Context, model, method string, ids []string, kwargs map[string]any) (any, error)
}

// ORMLuaHost is the default LuaHost: it dispatches base.call through orm.ExecuteRuntimeMethod, the
// same chokepoint RPC and MCP use, in the caller's ctx (so ACL/env follow whoever started the run:
// sudo for cron/trigger runs, the user for a manual Run).
type ORMLuaHost struct{}

// CallModelMethod runs model.method in ctx.
func (ORMLuaHost) CallModelMethod(ctx context.Context, model, method string, ids []string, kwargs map[string]any) (any, error) {
	return orm.ExecuteRuntimeMethod(ctx, model, method, ids, nil, kwargs)
}

// LuaScript is one script execution request.
type LuaScript struct {
	Code    string
	Payload map[string]any
	// Globals are extra read-only inputs bound next to `payload` (e.g. the raw step input).
	Globals map[string]any
	// Host, when set, installs the `base` table (call/log/json_encode/json_decode).
	Host LuaHost
	// KeepPayload makes a script that returns no table yield the (possibly mutated) `payload`
	// global instead of an empty result, so pipeline steps can edit payload in place.
	KeepPayload bool
}

// luaActionHost is the host automation.action Lua scripts run with; a var so tests can stub it.
var luaActionHost LuaHost = ORMLuaHost{}

// RunLuaAction executes code in a sandboxed Lua VM with payload bound as the
// global table `payload`. If the script returns a table, it is converted
// back into a Go map as the action result; any other return shape (or none)
// yields an empty result map.
func RunLuaAction(ctx context.Context, code string, payload map[string]any) (map[string]any, error) {
	return RunLuaScript(ctx, LuaScript{Code: code, Payload: payload, Host: luaActionHost})
}

// RunLuaScript executes one LuaScript under luaActionTimeout (or the caller's tighter deadline).
func RunLuaScript(ctx context.Context, script LuaScript) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, luaActionTimeout)
	defer cancel()

	state := newSandboxedLuaState()
	defer state.Close()
	state.SetContext(ctx)

	// Caller globals must not replace `payload`, `base` or a sandbox library/function: a script
	// reading `string`, `pcall` or `base` has to get the real thing.
	for name := range script.Globals {
		if name == "payload" || name == "base" || state.GetGlobal(name) != lua.LNil {
			return nil, fmt.Errorf("automation lua action: global %q is reserved", name)
		}
	}
	state.SetGlobal("payload", goValueToLua(state, script.Payload))
	for name, value := range script.Globals {
		state.SetGlobal(name, goValueToLua(state, value))
	}
	if script.Host != nil {
		state.SetGlobal("base", newLuaHostTable(state, script.Host))
	}

	if err := state.DoString(script.Code); err != nil {
		return nil, fmt.Errorf("automation lua action: %w", err)
	}

	// The script's single return value, if any, sits on top of the stack after
	// DoString runs a top-level chunk that ends with `return ...`.
	var result lua.LValue = lua.LNil
	if state.GetTop() > 0 {
		result = state.Get(-1)
		state.Pop(1)
	}
	if _, isTable := result.(*lua.LTable); !isTable && script.KeepPayload {
		result = state.GetGlobal("payload")
	}

	converted, err := luaValueToGo(result)
	if err != nil {
		return nil, fmt.Errorf("automation lua action: result: %w", err)
	}
	resultMap, ok := converted.(map[string]any)
	if !ok {
		return map[string]any{}, nil
	}
	return resultMap, nil
}

// newLuaHostTable builds the `base` table. base.call raises a Lua error on failure so an
// unguarded call fails the whole script; scripts that expect failures wrap it in pcall.
func newLuaHostTable(state *lua.LState, host LuaHost) *lua.LTable {
	table := state.NewTable()
	state.SetField(table, "call", state.NewFunction(func(L *lua.LState) int {
		model, method := L.CheckString(1), L.CheckString(2)
		rawKwargs, err := luaValueToGo(L.Get(3))
		if err != nil {
			L.RaiseError("%s.%s: kwargs: %s", model, method, err.Error())
			return 0
		}
		kwargs, _ := rawKwargs.(map[string]any)
		if kwargs == nil {
			kwargs = map[string]any{}
		}
		ids, err := luaIDs(L.Get(4))
		if err != nil {
			L.RaiseError("%s.%s: ids: %s", model, method, err.Error())
			return 0
		}
		result, err := host.CallModelMethod(L.Context(), model, method, ids, kwargs)
		if err != nil {
			L.RaiseError("%s.%s: %s", model, method, err.Error())
			return 0
		}
		L.Push(goValueToLua(L, plainJSONValue(result)))
		return 1
	}))
	state.SetField(table, "log", state.NewFunction(func(L *lua.LState) int {
		log.Infof("lua: %s", L.CheckString(1))
		return 0
	}))
	state.SetField(table, "json_encode", state.NewFunction(func(L *lua.LState) int {
		value, err := luaValueToGo(L.Get(1))
		if err != nil {
			L.RaiseError("json_encode: %s", err.Error())
			return 0
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			L.RaiseError("json_encode: %s", err.Error())
			return 0
		}
		L.Push(lua.LString(encoded))
		return 1
	}))
	state.SetField(table, "json_decode", state.NewFunction(func(L *lua.LState) int {
		var decoded any
		if err := json.Unmarshal([]byte(L.CheckString(1)), &decoded); err != nil {
			L.RaiseError("json_decode: %s", err.Error())
			return 0
		}
		L.Push(goValueToLua(L, decoded))
		return 1
	}))
	return table
}

// luaIDs accepts a single id or an array of ids (numbers or strings) for base.call's 4th arg.
func luaIDs(value lua.LValue) ([]string, error) {
	converted, err := luaValueToGo(value)
	if err != nil {
		return nil, err
	}
	switch typed := converted.(type) {
	case nil:
		return nil, nil
	case []any:
		ids := make([]string, 0, len(typed))
		for _, item := range typed {
			ids = append(ids, luaIDString(item))
		}
		return ids, nil
	default:
		return []string{luaIDString(typed)}, nil
	}
}

// luaIDString renders Lua's float64 numbers as integer ids ("7", not "7.0").
func luaIDString(value any) string {
	if number, ok := value.(float64); ok {
		return fmt.Sprintf("%d", int64(number))
	}
	return fmt.Sprint(value)
}

// plainJSONValue normalizes a host result (records, structs, typed maps/slices) into the
// map[string]any / []any / scalar shapes goValueToLua understands, via its JSON form.
func plainJSONValue(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

// newSandboxedLuaState opens only the libraries a data-transform script
// needs (base, table, string, math) and then strips the filesystem/bytecode-
// loading globals that OpenBase bundles unconditionally (dofile, loadfile,
// load, loadstring, require, module, newproxy) — those are the actual
// sandbox escape surface, not the os/io libraries, which are simply never
// opened in the first place.
func newSandboxedLuaState() *lua.LState {
	state := lua.NewState(lua.Options{
		SkipOpenLibs:    true,
		CallStackSize:   luaCallStackSize,
		RegistryMaxSize: luaRegistryMaxSize,
	})
	for _, open := range []lua.LGFunction{lua.OpenBase, lua.OpenTable, lua.OpenString, lua.OpenMath} {
		// Mirrors (*LState).OpenLibs' own call convention: push the loader,
		// push its module name, invoke with 1 arg. The individual OpenXxx
		// functions ignore the arg themselves but this keeps the call shape
		// identical to the library's own bootstrap path.
		state.Push(state.NewFunction(open))
		state.Push(lua.LString(""))
		state.Call(1, 0)
	}
	for _, name := range []string{"dofile", "loadfile", "load", "loadstring", "require", "module", "newproxy", "_printregs"} {
		state.SetGlobal(name, lua.LNil)
	}
	// string.rep allocates len(s)*n bytes in one go; a huge n is an out-of-memory crash for the
	// whole process, not a Lua error, so cap the result size.
	if stringLib, ok := state.GetGlobal("string").(*lua.LTable); ok {
		stringLib.RawSetString("rep", state.NewFunction(cappedStringRep))
	}
	return state
}

// cappedStringRep is string.rep(s, n) with the result bounded by luaMaxStringRepBytes.
func cappedStringRep(L *lua.LState) int {
	str := L.CheckString(1)
	n := L.CheckInt(2)
	if n <= 0 || str == "" {
		L.Push(lua.LString(""))
		return 1
	}
	if n > luaMaxStringRepBytes/len(str) {
		L.RaiseError("string.rep: result larger than %d bytes", luaMaxStringRepBytes)
		return 0
	}
	L.Push(lua.LString(strings.Repeat(str, n)))
	return 1
}

// goValueToLua converts a Go value produced by JSONB/map decoding into the
// matching Lua value. Unsupported Go types convert to nil rather than
// panicking, since payloads always originate from JSON-decoded data.
func goValueToLua(state *lua.LState, value any) lua.LValue {
	switch typed := value.(type) {
	case nil:
		return lua.LNil
	case bool:
		return lua.LBool(typed)
	case string:
		return lua.LString(typed)
	case int64:
		return lua.LNumber(typed)
	case int:
		return lua.LNumber(typed)
	case float64:
		return lua.LNumber(typed)
	case map[string]any:
		table := state.NewTable()
		for key, item := range typed {
			table.RawSetString(key, goValueToLua(state, item))
		}
		return table
	case []any:
		table := state.NewTable()
		for i, item := range typed {
			table.RawSetInt(i+1, goValueToLua(state, item))
		}
		return table
	default:
		return lua.LNil
	}
}

// luaValueToGo converts a Lua value back into plain Go data
// (map[string]any / []any / string / float64 / bool / nil) so the caller can
// treat every action_type's result the same way. Tables with contiguous
// integer keys starting at 1 convert to a slice; any other table converts to
// a map. Cyclic, too deep or too large tables are an error (see the luaMaxConvert* bounds);
// functions and userdata convert to nil.
func luaValueToGo(value lua.LValue) (any, error) {
	converter := luaConverter{onPath: map[*lua.LTable]struct{}{}}
	return converter.convert(value, 0)
}

// luaConverter carries one conversion's budget and the tables on the current path (cycle check).
// A table reached twice through different keys is fine; reaching it again from inside itself is not.
type luaConverter struct {
	onPath map[*lua.LTable]struct{}
	values int
	bytes  int
}

func (c *luaConverter) convert(value lua.LValue, depth int) (any, error) {
	c.values++
	if c.values > luaMaxConvertValues {
		return nil, fmt.Errorf("lua value has more than %d elements", luaMaxConvertValues)
	}
	switch typed := value.(type) {
	case *lua.LNilType:
		return nil, nil
	case lua.LBool:
		return bool(typed), nil
	case lua.LNumber:
		return float64(typed), nil
	case lua.LString:
		if err := c.addBytes(len(typed)); err != nil {
			return nil, err
		}
		return string(typed), nil
	case *lua.LTable:
		if depth >= luaMaxConvertDepth {
			return nil, fmt.Errorf("lua table nested deeper than %d levels", luaMaxConvertDepth)
		}
		if _, cyclic := c.onPath[typed]; cyclic {
			return nil, fmt.Errorf("lua table contains itself")
		}
		c.onPath[typed] = struct{}{}
		defer delete(c.onPath, typed)
		return c.convertTable(typed, depth+1)
	default:
		return nil, nil
	}
}

func (c *luaConverter) addBytes(n int) error {
	c.bytes += n
	if c.bytes > luaMaxConvertBytes {
		return fmt.Errorf("lua value is larger than %d bytes", luaMaxConvertBytes)
	}
	return nil
}

func (c *luaConverter) convertTable(table *lua.LTable, depth int) (any, error) {
	length := table.Len()
	if length > 0 && isLuaArray(table, length) {
		out := make([]any, length)
		for i := 1; i <= length; i++ {
			item, err := c.convert(table.RawGetInt(i), depth)
			if err != nil {
				return nil, err
			}
			out[i-1] = item
		}
		return out, nil
	}

	out := map[string]any{}
	var err error
	table.ForEach(func(key, val lua.LValue) {
		if err != nil {
			return
		}
		name := key.String()
		if err = c.addBytes(len(name)); err != nil {
			return
		}
		var item any
		if item, err = c.convert(val, depth); err == nil {
			out[name] = item
		}
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// isLuaArray reports whether table only has the contiguous integer keys
// 1..length, so it round-trips as a Go slice instead of a map.
func isLuaArray(table *lua.LTable, length int) bool {
	count := 0
	table.ForEach(func(lua.LValue, lua.LValue) {
		count++
	})
	return count == length
}
