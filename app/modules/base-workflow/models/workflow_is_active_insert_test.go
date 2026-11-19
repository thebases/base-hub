package models

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// A bun `default:` tag makes Bun write DEFAULT for a zero value, so an explicit is_active=false
// (template seeds 14-16, copies, instantiations) was stored as the column default true.
// The create normalizers already default an absent is_active to true.
func TestWorkflowIsActiveHasNoBunDefault(t *testing.T) {
	db := bun.NewDB(nil, pgdialect.New())
	for name, model := range map[string]any{"base.workflow": FlowRecord{}, "base.workflow.action": WorkflowActionRecord{}} {
		field := db.Table(reflect.TypeOf(model)).LookupField("is_active")
		if field == nil || field.SQLDefault != "" || field.NullZero {
			t.Fatalf("%s is_active field = %+v, want no bun default (a false insert would become DEFAULT)", name, field)
		}
	}
}

// Round trip through the same bun INSERT the repository uses (bunInsertWithDB), on a table built
// from the model's own DDL. Skips without Postgres.
func TestWorkflowIsActiveFalseStoredAgainstPostgres(t *testing.T) {
	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	ctx := context.Background()
	conn, err := bunDB.DB.Conn(ctx)
	if err != nil {
		t.Skipf("conn: %v", err)
	}
	schema := fmt.Sprintf("wf_is_active_test_%d", time.Now().UnixNano())
	t.Cleanup(func() {
		_, _ = conn.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE")
		_ = conn.Close()
	})
	for _, stmt := range []string{"CREATE SCHEMA " + schema, "SET search_path TO " + schema} {
		if _, err := conn.ExecContext(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := conn.NewCreateTable().Model((*FlowRecord)(nil)).ModelTableExpr("base_workflow").Exec(ctx); err != nil {
		t.Fatal(err)
	}
	for _, want := range []bool{false, true} {
		row := &FlowRecord{Name: fmt.Sprintf("w_%t", want), Topic: fmt.Sprintf("t_%t", want), IsActive: want}
		var id int64
		if err := conn.NewInsert().Model(row).ModelTableExpr("base_workflow").ExcludeColumn("id").Returning("id").Scan(ctx, &id); err != nil {
			t.Fatal(err)
		}
		var got bool
		if err := conn.NewRaw("SELECT is_active FROM base_workflow WHERE id = ?", id).Scan(ctx, &got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("is_active stored %t, want %t", got, want)
		}
	}
}
