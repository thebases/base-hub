package runtime

// File overview:
// - Responsibility: provides backend test-support runtime helpers for runtime even though this file itself is part of the non-`_test.go` support surface.
// - Key symbols: `Metadata`, `ExecuteAction`, `ExecutorStub`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

type ExecutorStub struct {
	ActionResult      any
	ActionErr         error
	SearchResult      []any
	SearchCountResult int64
	ReadResult        []orm.RecordMap
	SearchReadResult  []orm.RecordMap
	CreateResult      orm.RecordMap
	WriteResult       bool
	UnlinkResult      bool
	LastWriteIDs      []string
	LastCreateValues  orm.RecordMap
	LastWriteValues   orm.RecordMap
}

func (s *ExecutorStub) Metadata() *orm.ModelMetadata { return nil }

// ExecuteAction executes the action handled by this package.
func (s *ExecutorStub) ExecuteAction(_ context.Context, _ string, _ orm.RecordMap, _ any) (any, error) {
	return s.ActionResult, s.ActionErr
}

func (s *ExecutorStub) RuntimeSearch(_ context.Context, _ orm.RecordMap) ([]any, error) {
	return s.SearchResult, nil
}

func (s *ExecutorStub) RuntimeSearchCount(_ context.Context, _ orm.RecordMap) (int64, error) {
	return s.SearchCountResult, nil
}

func (s *ExecutorStub) RuntimeRead(_ context.Context, _ []string, _ []string) ([]orm.RecordMap, error) {
	return s.ReadResult, nil
}

func (s *ExecutorStub) RuntimeSearchRead(_ context.Context, _ orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
	return s.SearchReadResult, nil
}

func (s *ExecutorStub) RuntimeCreate(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
	s.LastCreateValues = values
	return s.CreateResult, nil
}

func (s *ExecutorStub) RuntimeWrite(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
	s.LastWriteIDs = append([]string(nil), ids...)
	s.LastWriteValues = values
	return s.WriteResult, nil
}

func (s *ExecutorStub) RuntimeUnlink(_ context.Context, _ []string) (bool, error) {
	return s.UnlinkResult, nil
}

func RegisterModelType[T any](t *testing.T, spec orm.ModelSpec) *orm.ModelMetadata {
	t.Helper()

	meta, err := orm.RegisterModelType[T](spec)
	if err != nil {
		t.Fatalf("register model %q: %v", spec.Name, err)
	}
	// The default model registry is process-global, so a test that registers a
	// model without unregistering it leaks a byType entry keyed on T's Go
	// type. A later test in the same package that reuses T under a different
	// model name then fails registration with "go type ... already
	// registered as ..." — order-dependent and only visible when tests run
	// together, not in isolation. Cleanup here (rather than requiring every
	// call site to remember it) makes registrations always test-scoped.
	t.Cleanup(func() {
		orm.DefaultModelRegistry().Unregister(spec.Name)
	})
	return meta
}
