package models

import (
	"context"
	"errors"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

func TestHealthWorkflowTriggerRequiresViewer(t *testing.T) {
	stubWorkflowAccess(t, workflowTestPlainUser...)
	ctx := orm.ContextWithEnvironment(context.Background(), workflowRPCEnv(7))
	_, err := healthWorkflowTrigger(ctx, nil, nil, nil, nil)
	var ormErr *orm.Error
	if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrForbidden {
		t.Fatalf("plain user: err = %v, want forbidden", err)
	}
}

func TestHealthMethodIsReadOnlyAndPublic(t *testing.T) {
	for _, method := range WorkflowTriggerModelSpec.RuntimeMethods {
		if method.Name == "health" {
			if !method.Public || !method.ReadOnly || !method.ModelOnly {
				t.Fatalf("health flags = %#v, want public read-only model method", method)
			}
			return
		}
	}
	t.Fatal("health method not declared")
}

// TestBuildTriggerHealthRowsScopesByDB: the provider is asked for the caller's database only, and
// webhook rows are reported as not applicable.
func TestBuildTriggerHealthRowsScopesByDB(t *testing.T) {
	previous := workflowTriggerHealthProvider.Load()
	t.Cleanup(func() { workflowTriggerHealthProvider.Store(previous) })
	var askedDB string
	RegisterWorkflowTriggerHealthProvider(func(dbName string, ids []int64) map[int64]WorkflowTriggerHealth {
		askedDB = dbName
		return map[int64]WorkflowTriggerHealth{5: {Registered: true, Events: 3, LastEventAt: 100}}
	})
	rows := []orm.RecordMap{
		{"id": int64(5), "trigger_type": "mqtt", "topic": "a/b"},
		{"id": int64(6), "trigger_type": "webhook", "topic": "wh"},
	}
	got := buildTriggerHealthRows("tenant_a", rows)
	if askedDB != "tenant_a" {
		t.Fatalf("provider db = %q, want tenant_a", askedDB)
	}
	if len(got) != 2 || !got[0].Registered || got[0].Events != 3 || got[0].TriggerType != "mqtt" || !got[0].Applicable {
		t.Fatalf("mqtt row = %#v", got[0])
	}
	if got[1].Applicable || got[1].Registered || got[1].Instance == "" {
		t.Fatalf("webhook row = %#v, want not applicable with instance", got[1])
	}
}
