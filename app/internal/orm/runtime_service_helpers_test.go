package orm

// File overview:
// - Responsibility: verifies the shared runtime repository helper API — executor
//   dispatch, missing-executor failures, and the test stub seam used by model
//   business-method unit tests.
// - Key symbols: `TestRuntimeRepositoryHelpersDispatchThroughExecutor`,
//   `TestRuntimeRepositoryHelpersRequireExecutor`, `TestStubRuntimeRepositorySeam`.

import (
	"context"
	"strings"
	"testing"
)

// stubRuntimeHelperExecutor is a minimal in-memory runtimeExecutor so helper
// dispatch can be proven without a database or full handler bootstrap.
type stubRuntimeHelperExecutor struct {
	meta        *ModelMetadata
	lastAction  string
	lastValues  RecordMap
	lastIDs     []string
	lastFields  []string
	lastFilter  RecordMap
	unlinkCalls int
}

func (s *stubRuntimeHelperExecutor) Metadata() *ModelMetadata { return s.meta }

func (s *stubRuntimeHelperExecutor) ExecuteAction(_ context.Context, action string, _ RecordMap, _ any) (any, error) {
	s.lastAction = action
	return nil, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeSearch(context.Context, RecordMap) ([]any, error) {
	return nil, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeSearchCount(context.Context, RecordMap) (int64, error) {
	return 0, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeRead(_ context.Context, ids []string, fields []string) ([]RecordMap, error) {
	s.lastIDs = ids
	s.lastFields = fields
	return []RecordMap{{"id": int64(7), "name": "read"}}, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeSearchRead(_ context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	s.lastFilter = filter
	s.lastFields = fields
	return []RecordMap{{"id": int64(9), "name": "search_read"}}, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeCreate(_ context.Context, values RecordMap) (RecordMap, error) {
	s.lastValues = values
	return RecordMap{"id": int64(1)}, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeWrite(_ context.Context, ids []string, values RecordMap) (bool, error) {
	s.lastIDs = ids
	s.lastValues = values
	return true, nil
}

func (s *stubRuntimeHelperExecutor) RuntimeUnlink(_ context.Context, ids []string) (bool, error) {
	s.lastIDs = ids
	s.unlinkCalls++
	return true, nil
}

// registerHelperExecutor installs a fake executor for one model name and
// restores the previous registry entry on cleanup so parallel test state
// never leaks across cases.
func registerHelperExecutor(t *testing.T, modelName string) *stubRuntimeHelperExecutor {
	t.Helper()
	executor := &stubRuntimeHelperExecutor{meta: &ModelMetadata{Name: modelName}}

	runtimeExecutorRegistry.mu.Lock()
	previous, hadPrevious := runtimeExecutorRegistry.executors[modelName]
	runtimeExecutorRegistry.mu.Unlock()

	RegisterRuntimeExecutor(modelName, executor)
	t.Cleanup(func() {
		runtimeExecutorRegistry.mu.Lock()
		defer runtimeExecutorRegistry.mu.Unlock()
		if hadPrevious {
			runtimeExecutorRegistry.executors[modelName] = previous
			return
		}
		delete(runtimeExecutorRegistry.executors, modelName)
	})
	return executor
}

func TestRuntimeRepositoryHelpersDispatchThroughExecutor(t *testing.T) {
	const modelName = "helper.dispatch.model"
	executor := registerHelperExecutor(t, modelName)
	model := RuntimeModelRef(modelName, nil)
	ctx := context.Background()

	created, err := RuntimeRepositoryCreate(ctx, model, RecordMap{"name": "a"})
	if err != nil {
		t.Fatalf("create dispatch failed: %v", err)
	}
	if created["id"] != int64(1) {
		t.Fatalf("create did not reach the registered executor: %v", created)
	}
	if executor.lastValues["name"] != "a" {
		t.Fatalf("create values were not forwarded: %v", executor.lastValues)
	}

	ok, err := RuntimeRepositoryWrite(ctx, model, []string{"1"}, RecordMap{"name": "b"})
	if err != nil || !ok {
		t.Fatalf("write dispatch failed: ok=%v err=%v", ok, err)
	}

	rows, err := RuntimeRepositoryRead(ctx, model, []string{"7"}, []string{"name"})
	if err != nil || len(rows) != 1 || rows[0]["name"] != "read" {
		t.Fatalf("read dispatch failed: rows=%v err=%v", rows, err)
	}

	rows, err = RuntimeRepositorySearchRead(ctx, model, RecordMap{"name": "x"}, []string{"name"})
	if err != nil || len(rows) != 1 || rows[0]["name"] != "search_read" {
		t.Fatalf("search_read dispatch failed: rows=%v err=%v", rows, err)
	}

	ok, err = RuntimeRepositoryUnlink(ctx, model, []string{"7"})
	if err != nil || !ok || executor.unlinkCalls != 1 {
		t.Fatalf("unlink dispatch failed: ok=%v calls=%d err=%v", ok, executor.unlinkCalls, err)
	}
}

func TestRuntimeRepositoryHelpersRequireExecutor(t *testing.T) {
	ctx := context.Background()
	missing := RuntimeModelRef("helper.missing.model", nil)

	if _, err := RuntimeRepositoryCreate(ctx, missing, RecordMap{}); err == nil || !strings.Contains(err.Error(), "no runtime executor registered") {
		t.Fatalf("expected missing-executor error, got %v", err)
	}
	// Nil metadata must fail with the explicit metadata guard, not panic.
	if _, err := RuntimeRepositoryRead(ctx, nil, nil, nil); err == nil || !strings.Contains(err.Error(), "model metadata is required") {
		t.Fatalf("expected nil-metadata error, got %v", err)
	}
}

func TestStubRuntimeRepositorySeam(t *testing.T) {
	const modelName = "helper.stub.model"
	executor := registerHelperExecutor(t, modelName)
	model := RuntimeModelRef(modelName, nil)
	ctx := context.Background()

	restore := StubRuntimeRepository(modelName, RuntimeRepositoryStub{
		Read: func(_ context.Context, ids []string, _ []string) ([]RecordMap, error) {
			return []RecordMap{{"id": ids[0], "name": "stubbed"}}, nil
		},
	})

	rows, err := RuntimeRepositoryRead(ctx, model, []string{"42"}, nil)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "stubbed" {
		t.Fatalf("stubbed read was not used: rows=%v err=%v", rows, err)
	}
	if executor.lastIDs != nil {
		t.Fatalf("stubbed read leaked into the real executor: %v", executor.lastIDs)
	}

	// Operations the stub leaves nil must fall through to the registered executor.
	if _, err := RuntimeRepositoryCreate(ctx, model, RecordMap{"name": "real"}); err != nil {
		t.Fatalf("nil stub op should fall through to executor: %v", err)
	}
	if executor.lastValues["name"] != "real" {
		t.Fatalf("fall-through create did not reach executor: %v", executor.lastValues)
	}

	restore()
	rows, err = RuntimeRepositoryRead(ctx, model, []string{"7"}, nil)
	if err != nil || len(rows) != 1 || rows[0]["name"] != "read" {
		t.Fatalf("restore did not remove the stub: rows=%v err=%v", rows, err)
	}
}

func TestRuntimeModelRefFallsBackToNameOnlyHandle(t *testing.T) {
	ref := RuntimeModelRef(" helper.unregistered.model ", nil)
	if ref == nil || ref.Name != "helper.unregistered.model" {
		t.Fatalf("expected trimmed name-only handle, got %+v", ref)
	}

	provided := &ModelMetadata{Name: "helper.provided.model"}
	if got := RuntimeModelRef("ignored.name", provided); got != provided {
		t.Fatalf("dispatcher-provided model must win, got %+v", got)
	}
}
