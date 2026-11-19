package models

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
)

func rpcEnv(userID int64) *orm.Environment {
	return orm.NewEnvironment(nil, userID, map[string]any{orm.RBACContextAuthModeKey: "bearer"}, false)
}

func stubAliasCallerGroups(t *testing.T, groups ...string) {
	t.Helper()
	prevGroups, prevDefault := aliasCallerGroupBIDs, aliasDefaultAccess
	aliasCallerGroupBIDs = func(context.Context) ([]string, error) { return groups, nil }
	// The base.group_user blanket grant already passes the default ACL for
	// tbl.*; model that here so the test isolates the group_system gate.
	aliasDefaultAccess = func(context.Context, *orm.Environment, *orm.ModelMetadata, string, []string, orm.RecordMap) error {
		return nil
	}
	t.Cleanup(func() { aliasCallerGroupBIDs, aliasDefaultAccess = prevGroups, prevDefault })
}

func wantOrmCode(t *testing.T, err error, code int) {
	t.Helper()
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != code {
		t.Fatalf("err = %v, want orm code %d", err, code)
	}
}

func TestDiscussAliasAccessNonSystemUserCannotMutate(t *testing.T) {
	stubAliasCallerGroups(t, "base.group_user", "base.group_multi_company")
	meta := &orm.ModelMetadata{Name: DiscussAliasModelName}
	ctx := context.Background()
	for _, op := range []string{"create", "write", "unlink", "name_create"} {
		wantOrmCode(t, discussAliasAccess(ctx, rpcEnv(7), meta, op, nil, nil), orm.ErrForbidden)
	}
	if err := discussAliasAccess(ctx, rpcEnv(7), meta, "read", nil, nil); err != nil {
		t.Fatalf("read must stay on standard ACL: %v", err)
	}
}

func TestDiscussAliasAccessSystemSudoAndInternalAllowed(t *testing.T) {
	meta := &orm.ModelMetadata{Name: DiscussAliasModelName}
	ctx := context.Background()

	stubAliasCallerGroups(t, "base.group_user", "base.group_system")
	if err := discussAliasAccess(ctx, rpcEnv(7), meta, "create", nil, nil); err != nil {
		t.Fatalf("group_system create: %v", err)
	}

	stubAliasCallerGroups(t, "base.group_user")
	if err := discussAliasAccess(ctx, orm.NewEnvironment(nil, 7, nil, true), meta, "create", nil, nil); err != nil {
		t.Fatalf("sudo create: %v", err)
	}
	// No auth mode = internal caller (seed import, boot): kernel default semantics.
	if err := discussAliasAccess(ctx, orm.NewEnvironment(nil, 0, nil, false), meta, "create", nil, nil); err != nil {
		t.Fatalf("internal create: %v", err)
	}
	if err := discussAliasAccess(ctx, nil, meta, "create", nil, nil); err != nil {
		t.Fatalf("nil env create: %v", err)
	}
}

func TestValidateAliasTargetModelRejectsSensitiveModels(t *testing.T) {
	ctx := context.Background()
	for _, name := range []string{"base.users", "tbl.groups", "sys.config", "ui.view", " base.users ", ""} {
		wantOrmCode(t, validateAliasModelConstraint(ctx, orm.RecordSet{}, orm.RecordMap{"alias_model_id": name}), orm.ErrInvalidParams)
	}
	wantOrmCode(t, validateAliasTargetModel(ctx, "tbl.no_such_model_xyz"), orm.ErrInvalidParams)
}

// TestModelDeclaresChatter runs the chatter lookup against a throwaway
// ui_view table (same pattern as mail_claim_db_test.go).
func TestModelDeclaresChatter(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	db := bunDB.DB
	ctx := context.Background()
	schema := fmt.Sprintf("alias_guard_test_%d", time.Now().UnixNano())
	for _, stmt := range []string{
		"CREATE SCHEMA " + schema,
		"CREATE TABLE " + schema + `.ui_view (id BIGSERIAL PRIMARY KEY, active BOOLEAN, model TEXT,
			use_chatter BOOLEAN NOT NULL DEFAULT FALSE, arch_db TEXT, arch TEXT)`,
		"INSERT INTO " + schema + `.ui_view (active, model, use_chatter, arch_db) VALUES
			(TRUE, 'tbl.partner', TRUE, '<form/>'),
			(TRUE, 'tbl.ticket', FALSE, '<form><sheet/><chatter/></form>'),
			(FALSE, 'tbl.archived', TRUE, '<form/>'),
			(TRUE, 'base.users', FALSE, '<form><sheet/></form>')`,
	} {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") })
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := conn.ExecContext(ctx, "SET search_path TO "+schema); err != nil {
		t.Fatal(err)
	}

	for model, want := range map[string]bool{
		"tbl.partner": true, "tbl.ticket": true, "tbl.archived": false, "base.users": false, "tbl.none": false,
	} {
		got, err := modelDeclaresChatter(ctx, conn, model)
		if err != nil {
			t.Fatalf("%s: %v", model, err)
		}
		if got != want {
			t.Fatalf("modelDeclaresChatter(%q) = %v, want %v", model, got, want)
		}
	}
}
