package routes

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// SEC-T-12 / BUG-M-10: a cancel notification reaches only the caller's own
// in-flight call, and a finished call never removes a newer one's entry.
func TestMCPCancelRegistryScopedToCaller(t *testing.T) {
	alice := &orm.RBACIdentity{DBName: "db1", UserID: 1, Login: "alice"}
	bob := &orm.RBACIdentity{DBName: "db1", UserID: 2, Login: "bob"}
	aliceOtherDB := &orm.RBACIdentity{DBName: "db2", UserID: 1, Login: "alice"}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	token := mcpRegisterCancelableCall(alice, "sess-a", float64(1), cancel)
	defer mcpUnregisterCancelableCall(token)

	for name, try := range map[string]func() bool{
		"other user":    func() bool { return mcpCancelRequestedCall(bob, "sess-a", float64(1)) },
		"other db":      func() bool { return mcpCancelRequestedCall(aliceOtherDB, "sess-a", float64(1)) },
		"other session": func() bool { return mcpCancelRequestedCall(alice, "sess-b", float64(1)) },
		"no request id": func() bool { return mcpCancelRequestedCall(alice, "sess-a", nil) },
	} {
		if try() || ctx.Err() != nil {
			t.Fatalf("%s cancelled alice's call", name)
		}
	}
	// String and numeric ids are the same JSON-RPC id once formatted.
	if !mcpCancelRequestedCall(alice, "sess-a", float64(1)) || ctx.Err() == nil {
		t.Fatal("owner could not cancel its own call")
	}
}

func TestMCPCancelUnregisterKeepsNewerEntry(t *testing.T) {
	alice := &orm.RBACIdentity{DBName: "db1", UserID: 1, Login: "alice"}

	_, cancelOld := context.WithCancel(context.Background())
	defer cancelOld()
	oldToken := mcpRegisterCancelableCall(alice, "", "req-7", cancelOld)

	newCtx, cancelNew := context.WithCancel(context.Background())
	defer cancelNew()
	newToken := mcpRegisterCancelableCall(alice, "", "req-7", cancelNew)
	defer mcpUnregisterCancelableCall(newToken)

	mcpUnregisterCancelableCall(oldToken)
	if !mcpCancelRequestedCall(alice, "", "req-7") || newCtx.Err() == nil {
		t.Fatal("finishing the older call removed the newer call's cancel entry")
	}
}
