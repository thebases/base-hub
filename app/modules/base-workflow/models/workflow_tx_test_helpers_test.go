package models

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"sync"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// fakeWorkflowTx replaces the database half of runInWorkflowTx (lock + revision bump) for tests
// that run on in-memory repository stubs. It keeps a revision per workflow, records which rows were
// locked, and reports whether the last transaction body failed (= would have rolled back).
type fakeWorkflowTx struct {
	mu        sync.Mutex
	revisions map[int64]int64
	locked    []int64
	failed    int
	committed int
}

func stubWorkflowTx(t *testing.T, revisions map[int64]int64) *fakeWorkflowTx {
	t.Helper()
	fake := &fakeWorkflowTx{revisions: map[int64]int64{}}
	for id, revision := range revisions {
		fake.revisions[id] = revision
	}
	previous := workflowTx
	workflowTx = workflowTxOps{
		run: func(ctx context.Context, workflowID int64, fn func(context.Context, int64) error) error {
			fake.mu.Lock()
			revision := fake.revisions[workflowID]
			if workflowID > 0 {
				fake.locked = append(fake.locked, workflowID)
			}
			fake.mu.Unlock()
			if err := fn(ctx, revision); err != nil {
				fake.mu.Lock()
				fake.failed++
				fake.mu.Unlock()
				return err
			}
			fake.mu.Lock()
			fake.committed++
			fake.mu.Unlock()
			return nil
		},
		bump: func(_ context.Context, workflowID int64) (int64, error) {
			fake.mu.Lock()
			defer fake.mu.Unlock()
			fake.revisions[workflowID]++
			return fake.revisions[workflowID], nil
		},
	}
	t.Cleanup(func() { workflowTx = previous })
	return fake
}

// memRepo is a tiny in-memory runtime repository for one model: equality filters on the keys of a
// SearchRead filter ("__" keys ignored, many2one values compared by id), ids assigned from next.
type memRepo struct {
	mu   sync.Mutex
	rows map[int64]orm.RecordMap
	next int64
}

func newMemRepo(t *testing.T, modelName string, firstID int64, rows ...orm.RecordMap) *memRepo {
	t.Helper()
	repo := &memRepo{rows: map[int64]orm.RecordMap{}, next: firstID}
	for _, row := range rows {
		repo.rows[int64Value(row["id"])] = cloneRecordMap(row)
	}
	restore := orm.StubRuntimeRepository(modelName, orm.RuntimeRepositoryStub{
		Create:     repo.create,
		Read:       repo.read,
		SearchRead: repo.searchRead,
		Write:      repo.write,
		Unlink:     repo.unlink,
	})
	t.Cleanup(restore)
	return repo
}

func memComparable(value any) string {
	if ptr, ok := int64PointerValue(value); ok && ptr != nil {
		return strconv.FormatInt(*ptr, 10)
	}
	return fmt.Sprint(value)
}

func (r *memRepo) create(_ context.Context, values orm.RecordMap) (orm.RecordMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	row := cloneRecordMap(values)
	row["id"] = r.next
	r.rows[r.next] = row
	r.next++
	return cloneRecordMap(row), nil
}

func (r *memRepo) read(_ context.Context, ids []string, _ []string) ([]orm.RecordMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []orm.RecordMap{}
	for _, id := range ids {
		if row, ok := r.rows[int64Value(id)]; ok {
			out = append(out, cloneRecordMap(row))
		}
	}
	return out, nil
}

func (r *memRepo) searchRead(_ context.Context, filter orm.RecordMap, _ []string) ([]orm.RecordMap, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]int64, 0, len(r.rows))
	for id := range r.rows {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := []orm.RecordMap{}
	for _, id := range ids {
		row := r.rows[id]
		match := true
		for key, want := range filter {
			if len(key) > 1 && key[:2] == "__" {
				continue
			}
			if memComparable(row[key]) != memComparable(want) {
				match = false
				break
			}
		}
		if match {
			out = append(out, cloneRecordMap(row))
		}
	}
	return out, nil
}

func (r *memRepo) write(_ context.Context, ids []string, values orm.RecordMap) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		row, ok := r.rows[int64Value(id)]
		if !ok {
			return false, nil
		}
		for key, value := range values {
			row[key] = value
		}
	}
	return true, nil
}

func (r *memRepo) unlink(_ context.Context, ids []string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range ids {
		delete(r.rows, int64Value(id))
	}
	return true, nil
}

func (r *memRepo) all() []orm.RecordMap {
	rows, _ := r.searchRead(context.Background(), orm.RecordMap{}, nil)
	return rows
}

func (r *memRepo) where(key string, want any) []orm.RecordMap {
	rows, _ := r.searchRead(context.Background(), orm.RecordMap{key: want}, nil)
	return rows
}
