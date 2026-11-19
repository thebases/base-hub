package orm

// File overview:
// - Responsibility: T36 / SEC-D-21 regression: a client transport call is capped at
//   server.maxReadLimit on list methods, while aggregates, server-side callers and calls nested
//   inside the client's method still read every row.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
)

const clientReadCapModel = "test.client_read_cap"

type clientReadCapDocument struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

// clientReadCapExecutor returns as many rows as the filter's limit allows (matching = 5 rows),
// and records the limit each search received.
type clientReadCapExecutor struct {
	stubRuntimeHelperExecutor
	limits []int
}

func (e *clientReadCapExecutor) RuntimeSearchRead(_ context.Context, filter RecordMap, _ []string) ([]RecordMap, error) {
	limit := int(toInt64(filter["__limit"]))
	e.limits = append(e.limits, limit)
	const matching = 5
	n := matching
	if limit > 0 && limit < n {
		n = limit
	}
	rows := make([]RecordMap, n)
	for i := range rows {
		rows[i] = RecordMap{"id": int64(i + 1), "name": "x"}
	}
	return rows, nil
}

func setupClientReadCap(t *testing.T, maxRows int) *clientReadCapExecutor {
	t.Helper()
	previous := config.Get().Server.MaxReadLimit
	config.Update(func(c *config.AppConfig) { c.Server.MaxReadLimit = maxRows })
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.Server.MaxReadLimit = previous }) })

	_, err := RegisterModelType[clientReadCapDocument](ModelSpec{
		Name:       clientReadCapModel,
		Label:      "Client Read Cap",
		Definition: ModelDefinition(clientReadCapModel, (*clientReadCapDocument)(nil)),
		RuntimeMethods: []RuntimeMethod{{
			// A server-side method that lists rows on the client's behalf (like a record-rule
			// id list or an export): its nested search must not be capped.
			Name: "nested_list", Public: true, ModelOnly: true, ReadOnly: true,
			Call: func(ctx context.Context, model *ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
				return ExecuteRuntimeMethod(ctx, model.Name, "search_read", nil, nil, nil)
			},
		}},
	}.WithStorageName("test_client_read_cap"))
	if err != nil {
		t.Fatalf("register model: %v", err)
	}
	executor := &clientReadCapExecutor{stubRuntimeHelperExecutor: stubRuntimeHelperExecutor{meta: &ModelMetadata{Name: clientReadCapModel}}}
	RegisterRuntimeExecutor(clientReadCapModel, executor)
	t.Cleanup(func() {
		runtimeExecutorRegistry.mu.Lock()
		defer runtimeExecutorRegistry.mu.Unlock()
		delete(runtimeExecutorRegistry.executors, clientReadCapModel)
	})
	return executor
}

func clientReadCapUserContext(sudo bool) context.Context {
	return ContextWithEnvironment(context.Background(), EnvironmentForIdentity(&RBACIdentity{
		UserID: 7, Login: "u", RoleID: 2, AuthMode: "bearer", IsSuperadmin: sudo,
	}))
}

func TestClientReadCapClampsListMethods(t *testing.T) {
	executor := setupClientReadCap(t, 3)
	client := ContextWithClientReadCap(clientReadCapUserContext(false))

	// No limit and a larger limit both become the cap; a smaller limit is kept.
	for _, tc := range []struct {
		kwargs map[string]any
		want   int
	}{
		{nil, 3},
		{map[string]any{"limit": 50}, 3},
		{map[string]any{"limit": 2}, 2},
	} {
		executor.limits = nil
		if _, err := ExecuteRuntimeMethod(client, clientReadCapModel, "search_read", nil, nil, tc.kwargs); err != nil {
			t.Fatal(err)
		}
		if len(executor.limits) != 1 || executor.limits[0] != tc.want {
			t.Fatalf("kwargs %v: limit %v, want %d", tc.kwargs, executor.limits, tc.want)
		}
	}

	// Server-side caller (no transport mark): uncapped.
	executor.limits = nil
	if _, err := ExecuteRuntimeMethod(clientReadCapUserContext(false), clientReadCapModel, "search_read", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if executor.limits[0] != 0 {
		t.Fatalf("server-side search capped: %v", executor.limits)
	}

	// Sudo client: uncapped.
	executor.limits = nil
	if _, err := ExecuteRuntimeMethod(ContextWithClientReadCap(clientReadCapUserContext(true)), clientReadCapModel, "search_read", nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if executor.limits[0] != 0 {
		t.Fatalf("sudo search capped: %v", executor.limits)
	}
}

func TestClientReadCapDoesNotReachNestedCalls(t *testing.T) {
	executor := setupClientReadCap(t, 3)
	client := ContextWithClientReadCap(clientReadCapUserContext(false))
	result, err := ExecuteRuntimeMethod(client, clientReadCapModel, "nested_list", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows, _ := result.([]RecordMap); len(rows) != 5 || executor.limits[0] != 0 {
		t.Fatalf("nested search was capped: rows %d limits %v", len(rows), executor.limits)
	}
}

// Aggregates return grouped results, not rows: pivot/grouped views on large tables keep working.
func TestClientReadCapLeavesAggregatesUncapped(t *testing.T) {
	executor := setupClientReadCap(t, 3)
	client := ContextWithClientReadCap(clientReadCapUserContext(false))
	if _, err := ExecuteRuntimeMethod(client, clientReadCapModel, "read_group", nil, nil, map[string]any{"groupby": []any{"name"}}); err != nil {
		t.Fatalf("read_group refused: %v", err)
	}
	if executor.limits[0] != 0 {
		t.Fatalf("aggregate read was capped: %v", executor.limits)
	}
}

func TestClampClientReadLimit(t *testing.T) {
	setupClientReadCap(t, 3)
	meta, _ := LookupModel(clientReadCapModel)
	var seen []int
	probe := func(ctx context.Context, requested int) {
		seen = append(seen, ClampClientReadLimit(ctx, meta, requested))
	}
	// Server-side: unchanged.
	probe(clientReadCapUserContext(false), 0)
	probe(clientReadCapUserContext(false), 100)
	// Top-level client call (as an override sees it inside ExecuteRuntimeMethod).
	active := scopeClientReadCap(ContextWithClientReadCap(clientReadCapUserContext(false)), meta)
	probe(active, 0)
	probe(active, 100)
	probe(active, 2)
	want := []int{0, 100, 3, 3, 2}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("got %v want %v", seen, want)
		}
	}
}
