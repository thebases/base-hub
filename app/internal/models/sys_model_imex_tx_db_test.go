package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestSysModelImexTxDefersEventsToCommit: a batch import that rolls back must
// fire none of the after-commit callbacks (record events / workflow triggers)
// its rows queued; a committed batch fires them after the commit.
func TestSysModelImexTxDefersEventsToCommit(t *testing.T) {
	setupSysSequenceFixture(t)
	ctx := sysSequenceTestContext(nil)

	fired := 0
	queue := func(txCtx context.Context) {
		orm.AfterCommit(txCtx, func(context.Context) { fired++ })
	}
	rollback := errors.New("row 50 failed")
	err := withSysModelImexTx(ctx, func(txCtx context.Context) error {
		queue(txCtx)
		queue(txCtx)
		if fired != 0 {
			t.Fatalf("callbacks ran inside the transaction")
		}
		return rollback
	})
	if !errors.Is(err, rollback) || fired != 0 {
		t.Fatalf("rolled-back import: err=%v fired=%d, want rollback error and 0", err, fired)
	}

	if err := withSysModelImexTx(ctx, func(txCtx context.Context) error {
		queue(txCtx)
		return nil
	}); err != nil || fired != 1 {
		t.Fatalf("committed import: err=%v fired=%d, want nil and 1", err, fired)
	}
}
