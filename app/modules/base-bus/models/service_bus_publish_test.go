package models

import (
	"context"
	"errors"
	"slices"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// publish inserts one row per normalized channel and returns their ids; a failed insert fails the
// whole publish (rows and NOTIFY share one transaction, so nothing half-published is announced).
func TestPublishServiceBusCreatesRowPerChannel(t *testing.T) {
	var created []string
	next := int64(100)
	failOn := ""
	restore := orm.StubRuntimeRepository(ServiceBusModelName, orm.RuntimeRepositoryStub{
		Create: func(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
			channel, _ := values["channel"].(string)
			if channel == failOn {
				return nil, errors.New("insert failed")
			}
			created = append(created, channel)
			next++
			return orm.RecordMap{"id": next}, nil
		},
	})
	t.Cleanup(restore)

	model := orm.RuntimeModelRef(ServiceBusModelName, nil)
	got, err := publishServiceBus(t.Context(), model, nil, []any{[]string{"b:chan", "a:chan"}, "ping", map[string]any{"k": 1}}, nil)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if ids, _ := got.([]int64); !slices.Equal(ids, []int64{101, 102}) {
		t.Fatalf("ids = %v, want [101 102]", got)
	}
	if !slices.Equal(created, []string{"a:chan", "b:chan"}) {
		t.Fatalf("created channels = %v", created)
	}

	failOn = "b:chan"
	if _, err := publishServiceBus(t.Context(), model, nil, []any{[]string{"a:chan", "b:chan"}, "ping", nil}, nil); err == nil {
		t.Fatal("a failed insert must fail the publish")
	}
}

// Joining a caller's transaction, the MQTT push is queued on orm.AfterCommit: nothing reaches the
// broker before commit, and a rolled-back transaction publishes nothing.
func TestPublishServiceBusMQTTWaitsForCallerCommit(t *testing.T) {
	prevCfg := *config.Get()
	cfg := prevCfg
	cfg.MqttClient.Enable = true
	cfg.MqttClient.PublicWSURL = "wss://broker.invalid/mqtt"
	config.Set(cfg)
	t.Cleanup(func() { config.Set(prevCfg) })

	var published []string
	prevPublish := publishServiceBusMQTT
	publishServiceBusMQTT = func(channel string, _ map[string]any) { published = append(published, channel) }
	t.Cleanup(func() { publishServiceBusMQTT = prevPublish })

	next := int64(0)
	t.Cleanup(orm.StubRuntimeRepository(ServiceBusModelName, orm.RuntimeRepositoryStub{
		Create: func(context.Context, orm.RecordMap) (orm.RecordMap, error) {
			next++
			return orm.RecordMap{"id": next}, nil
		},
	}))

	model := orm.RuntimeModelRef(ServiceBusModelName, nil)
	outer := orm.ContextWithEnvironment(t.Context(), orm.NewEnvironment(nil, 1, nil, false))
	// callerTx stands in for a caller that opened its own transaction (only the handle's type
	// matters to AfterCommit; with MQTT active publish issues no SQL on it).
	callerTx := func(ctx context.Context) context.Context {
		return orm.ContextWithEnvironment(ctx, orm.EnvironmentFromContext(ctx).WithDB(bun.Tx{}))
	}

	err := orm.WithAfterCommitScope(outer, func(scopeCtx context.Context) error {
		if _, err := publishServiceBus(callerTx(scopeCtx), model, nil, []any{"a:chan", "ping", nil}, nil); err != nil {
			return err
		}
		if len(published) != 0 {
			t.Fatalf("published before commit: %v", published)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scope: %v", err)
	}
	if !slices.Equal(published, []string{"a:chan"}) {
		t.Fatalf("published after commit = %v, want [a:chan]", published)
	}

	published = nil
	_ = orm.WithAfterCommitScope(outer, func(scopeCtx context.Context) error {
		if _, err := publishServiceBus(callerTx(scopeCtx), model, nil, []any{"a:chan", "ping", nil}, nil); err != nil {
			return err
		}
		return errors.New("caller rolls back")
	})
	if len(published) != 0 {
		t.Fatalf("rolled-back publish reached the broker: %v", published)
	}
}
