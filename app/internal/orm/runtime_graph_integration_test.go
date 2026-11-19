package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for runtime graph integration.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These integration tests exercise multi-model runtime flows to prove that
// registry, relations, and CRUD dispatch compose correctly end-to-end.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	runtimeGraphParentModel = "test.runtime.graph.parent"
	runtimeGraphChildModel  = "test.runtime.graph.child"
	runtimeGraphTagModel    = "test.runtime.graph.tag"
)

type runtimeGraphParent struct {
	BaseModel
	Name     string  `bun:"name" json:"name"`
	Code     string  `bun:"code" json:"code" meta:"index"`
	Email    string  `bun:"email" json:"email" meta:"unique"`
	IsActive bool    `bun:"active" json:"active" default:"true"`
	ChildIDs []int64 `bun:"child_ids,type:jsonb,nullzero" json:"child_ids" meta:"kind:one2many,model:test.runtime.graph.child,inverse:parent_id"`
	TagIDs   []int64 `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:test.runtime.graph.tag,relation_table:runtime_graph_parent_tag_rel,relation_column:parent_id,relation_key:tag_id"`
}

type runtimeGraphChild struct {
	BaseModel
	Name       string `bun:"name" json:"name"`
	Kind       string `bun:"kind" json:"kind"`
	ParentID   int64  `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:test.runtime.graph.parent,ondelete:set null"`
	ParentName string `bun:"parent_name" json:"parent_name" meta:"related:parent_id.name,stored,depends:parent_id"`
}

type runtimeGraphTag struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type runtimeGraphParentTagRel struct {
	bun.BaseModel `bun:"table:runtime_graph_parent_tag_rel"`
	ParentID      int64 `bun:"parent_id"`
	TagID         int64 `bun:"tag_id"`
}

type runtimeGraphQueryCounter struct {
	mu            sync.Mutex
	childSelects  int
	parentSelects int
	lastErr       error
	lastQuery     string
	firstErr      error
	firstQuery    string
}

func runtimeGraphRelationTuples(t *testing.T, value any) [][2]any {
	t.Helper()
	tuples, ok := value.([][2]any)
	if !ok {
		t.Fatalf("expected relation tuples, got %#v", value)
	}
	return tuples
}

func (h *runtimeGraphQueryCounter) BeforeQuery(ctx context.Context, _ *bun.QueryEvent) context.Context {
	return ctx
}

func (h *runtimeGraphQueryCounter) AfterQuery(_ context.Context, event *bun.QueryEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if event.Err != nil {
		if h.firstErr == nil {
			h.firstErr = event.Err
			h.firstQuery = event.Query
		}
		h.lastErr = event.Err
		h.lastQuery = event.Query
	}
	query := strings.ToLower(event.Query)
	if event.Operation() != "SELECT" {
		return
	}
	if strings.Contains(query, "runtime_graph_child") {
		h.childSelects++
	}
	if strings.Contains(query, "runtime_graph_parent") {
		h.parentSelects++
	}
}

func (h *runtimeGraphQueryCounter) childCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.childSelects
}

func (h *runtimeGraphQueryCounter) lastFailure() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.firstErr != nil {
		return h.firstQuery, h.firstErr
	}
	return h.lastQuery, h.lastErr
}

var runtimeGraphRegistrationOnce sync.Once

func setupRuntimeGraphFixture(t *testing.T) (*bun.DB, *runtimeGraphQueryCounter, func()) {
	t.Helper()

	bunDB, err := NewBunDB(runtimeGraphEnvPostgresConfig())
	if err != nil {
		t.Fatalf("open postgres runtime graph fixture: %v", err)
	}
	db := bunDB.DB
	counter := &runtimeGraphQueryCounter{}
	db.AddQueryHook(counter)

	previous := GetBunDB()
	SetBunDB(bunDB)
	t.Cleanup(func() {
		if previous == nil {
			SetBunDB(nil)
			return
		}
		SetBunDB(&BunDB{DB: previous})
	})

	runtimeGraphRegistrationOnce.Do(func() {
		RegisterOnchange(runtimeGraphParentModel, []string{"name"}, func(_ context.Context, _ RecordSet, values RecordMap, _ []string, _ map[string]any) (RecordMap, error) {
			return RecordMap{"name_echo": values["name"]}, nil
		})
	})

	NewHandlerWithModel[runtimeGraphParent](ModelSpec{
		Name:       runtimeGraphParentModel,
		Label:      "Runtime Graph Parent",
		Definition: ModelDefinition(runtimeGraphParentModel, (*runtimeGraphParent)(nil)),
	}.WithStorageName("runtime_graph_parent")).InitDefault()
	NewHandlerWithModel[runtimeGraphChild](ModelSpec{
		Name:       runtimeGraphChildModel,
		Label:      "Runtime Graph Child",
		Definition: ModelDefinition(runtimeGraphChildModel, (*runtimeGraphChild)(nil)),
	}.WithStorageName("runtime_graph_child")).InitDefault()
	NewHandlerWithModel[runtimeGraphTag](ModelSpec{
		Name:       runtimeGraphTagModel,
		Label:      "Runtime Graph Tag",
		Definition: ModelDefinition(runtimeGraphTagModel, (*runtimeGraphTag)(nil)),
	}.WithStorageName("runtime_graph_tag")).InitDefault()

	statements := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := db.NewDropTable().Model((*runtimeGraphParentTagRel)(nil)).IfExists().Cascade().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewDropTable().Model((*runtimeGraphChild)(nil)).ModelTableExpr("runtime_graph_child").IfExists().Cascade().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewDropTable().Model((*runtimeGraphParent)(nil)).ModelTableExpr("runtime_graph_parent").IfExists().Cascade().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewDropTable().Model((*runtimeGraphTag)(nil)).ModelTableExpr("runtime_graph_tag").IfExists().Cascade().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewCreateTable().Model((*runtimeGraphParent)(nil)).ModelTableExpr("runtime_graph_parent").IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewCreateTable().Model((*runtimeGraphChild)(nil)).ModelTableExpr("runtime_graph_child").IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewCreateTable().Model((*runtimeGraphTag)(nil)).ModelTableExpr("runtime_graph_tag").IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.NewCreateTable().Model((*runtimeGraphParentTagRel)(nil)).IfNotExists().Exec(ctx)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS runtime_graph_parent_code_idx ON runtime_graph_parent (code)`)
			return err
		},
		func(ctx context.Context) error {
			_, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS runtime_graph_parent_email_key ON runtime_graph_parent (email)`)
			return err
		},
	}
	for _, statement := range statements {
		if err := statement(context.Background()); err != nil {
			t.Fatalf("create fixture schema: %v", err)
		}
	}

	cleanup := func() {
		_ = bunDB.Close()
	}
	return db, counter, cleanup
}

func runtimeGraphEnvPostgresConfig() config.PostgresqlConfig {
	return config.PostgresqlConfig{
		Enable:   true,
		Host:     runtimeGraphGetenvDefault("RUNTIME_GRAPH_TEST_DB_HOST", "localhost"),
		Port:     runtimeGraphGetenvIntDefault("RUNTIME_GRAPH_TEST_DB_PORT", 5432),
		User:     runtimeGraphGetenvDefault("RUNTIME_GRAPH_TEST_DB_USER", "thebase"),
		Password: runtimeGraphGetenvDefault("RUNTIME_GRAPH_TEST_DB_PASSWORD", "theBase15112023@"),
		Name:     runtimeGraphGetenvDefault("RUNTIME_GRAPH_TEST_DB_NAME", "base_hub_test"),
		SSLMode:  runtimeGraphGetenvDefault("RUNTIME_GRAPH_TEST_DB_SSLMODE", "disable"),
	}
}

func runtimeGraphGetenvDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func runtimeGraphGetenvIntDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func runtimeGraphLookupID(t *testing.T, executor runtimeExecutor, filter RecordMap) string {
	t.Helper()
	rows, err := executor.RuntimeSearchRead(context.Background(), filter, []string{"id"})
	if err != nil {
		t.Fatalf("lookup runtime graph id: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected exactly one lookup row, got %#v", rows)
	}
	id, ok := runtimeStringID(rows[0]["id"])
	if !ok {
		t.Fatalf("expected lookup id in %#v", rows[0])
	}
	return id
}

func TestEnvironmentWithDBKeepsSharedCacheState(t *testing.T) {
	env := NewEnvironment(nil, 7, map[string]any{"active_test": true}, false)
	env.CacheField(runtimeGraphParentModel, "11", "child_ids", []string{"21"})

	next := env.WithDB(nil)
	cached, ok := next.CachedField(runtimeGraphParentModel, "11", "child_ids")
	if !ok {
		t.Fatalf("expected cloned environment to reuse request cache")
	}
	ids, _ := cached.([]string)
	if len(ids) != 1 || ids[0] != "21" {
		t.Fatalf("unexpected cached child ids %#v", cached)
	}
}

func TestRuntimeCreateRollsBackRelationalGraphMutation(t *testing.T) {
	_, _, cleanup := setupRuntimeGraphFixture(t)
	defer cleanup()

	RegisterConstraint(runtimeGraphParentModel, []string{"name"}, func(_ context.Context, _ RecordSet, changedValues RecordMap) error {
		if changedValues["name"] == "reject" {
			log.Errorf("reject create graph mutation")
			return fmt.Errorf("reject create graph mutation")
		}
		return nil
	})

	parentExecutor, ok := lookupRuntimeExecutor(runtimeGraphParentModel)
	if !ok {
		t.Fatalf("expected parent runtime executor")
	}
	if _, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{
		"name": "reject",
		"child_ids": []any{
			[]any{0, 0, RecordMap{"name": "child-rollback", "kind": "sale"}},
		},
	}); err == nil {
		t.Fatalf("expected create to fail so the transaction rolls back")
	}

	parents, err := parentExecutor.RuntimeSearchRead(context.Background(), nil, []string{"id"})
	if err != nil {
		t.Fatalf("search parents after rollback: %v", err)
	}
	if len(parents) != 0 {
		t.Fatalf("expected parent rollback, got %#v", parents)
	}

	childExecutor, ok := lookupRuntimeExecutor(runtimeGraphChildModel)
	if !ok {
		t.Fatalf("expected child runtime executor")
	}
	children, err := childExecutor.RuntimeSearchRead(context.Background(), nil, []string{"id", "name"})
	if err != nil {
		t.Fatalf("search children after rollback: %v", err)
	}
	if len(children) != 0 {
		t.Fatalf("expected child rollback, got %#v", children)
	}
}

func TestRuntimeWriteRollsBackRelationalGraphMutation(t *testing.T) {
	_, counter, cleanup := setupRuntimeGraphFixture(t)
	defer cleanup()

	parentExecutor, ok := lookupRuntimeExecutor(runtimeGraphParentModel)
	if !ok {
		t.Fatalf("expected parent runtime executor")
	}
	created, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{"name": "write-parent"})
	if err != nil {
		query, lastErr := counter.lastFailure()
		t.Fatalf("create write fixture: %v; last query=%q last err=%v", err, query, lastErr)
	}
	parentID, _ := runtimeStringID(created["id"])
	if parentID == "" {
		parentID = runtimeGraphLookupID(t, parentExecutor, RecordMap{"name": "write-parent"})
	}
	if parentID == "" {
		t.Fatalf("expected created parent id in %#v", created)
	}

	RegisterConstraint(runtimeGraphParentModel, []string{"name"}, func(_ context.Context, _ RecordSet, changedValues RecordMap) error {
		if changedValues["name"] == "reject-write" {
			log.Errorf("reject write graph mutation")
			return fmt.Errorf("reject write graph mutation")
		}
		return nil
	})

	if _, err := parentExecutor.RuntimeWrite(context.Background(), []string{parentID}, RecordMap{
		"name": "reject-write",
		"child_ids": []any{
			[]any{0, 0, RecordMap{"name": "child-rollback-write", "kind": "sale"}},
		},
	}); err == nil {
		t.Fatalf("expected write to fail so the transaction rolls back")
	}

	rows, err := parentExecutor.RuntimeSearchRead(context.Background(), RecordMap{"name": "write-parent"}, []string{"id", "name", "child_ids"})
	if err != nil {
		t.Fatalf("read parent after write rollback: %v", err)
	}
	if len(rows) != 1 || rows[0]["name"] != "write-parent" {
		t.Fatalf("expected parent name rollback, got %#v", rows)
	}
	if rows[0]["child_ids"] != false {
		t.Fatalf("expected child mutation rollback, got %#v", rows[0])
	}
}

func TestRuntimeSearchCountHonorsTransactionalReadYourOwnWrite(t *testing.T) {
	_, _, cleanup := setupRuntimeGraphFixture(t)
	defer cleanup()

	parentExecutor, ok := lookupRuntimeExecutor(runtimeGraphParentModel)
	if !ok {
		t.Fatalf("expected parent runtime executor")
	}
	parentHandler, ok := parentExecutor.(*Handler[runtimeGraphParent])
	if !ok {
		t.Fatalf("expected concrete parent handler")
	}

	rollbackErr := errors.New("rollback runtime search_count fixture")
	err := withRuntimeTx(context.Background(), parentHandler, func(txCtx context.Context) error {
		if _, err := parentHandler.RuntimeCreate(txCtx, RecordMap{"name": "tx-count-parent"}); err != nil {
			return err
		}

		count, err := parentHandler.RuntimeSearchCount(txCtx, RecordMap{"name": "tx-count-parent"})
		if err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("expected transactional search_count to see uncommitted row, got %d", count)
		}
		return rollbackErr
	})
	if !errors.Is(err, rollbackErr) {
		t.Fatalf("expected rollback sentinel, got %v", err)
	}

	count, err := parentExecutor.RuntimeSearchCount(context.Background(), RecordMap{"name": "tx-count-parent"})
	if err != nil {
		t.Fatalf("search_count after rollback: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected rolled back row to stay invisible, got %d", count)
	}

	if _, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{"name": "committed-count-parent"}); err != nil {
		t.Fatalf("create committed count fixture: %v", err)
	}
	count, err = parentExecutor.RuntimeSearchCount(context.Background(), RecordMap{"name": "committed-count-parent"})
	if err != nil {
		t.Fatalf("search_count after commit: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected committed row to be counted, got %d", count)
	}
}

func TestRuntimeRelationCacheReuseAndInvalidation(t *testing.T) {
	_, counter, cleanup := setupRuntimeGraphFixture(t)
	defer cleanup()

	parentExecutor, _ := lookupRuntimeExecutor(runtimeGraphParentModel)
	created, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{
		"name": "cache-parent",
		"child_ids": []any{
			[]any{0, 0, RecordMap{"name": "child-a", "kind": "sale"}},
		},
	})
	if err != nil {
		t.Fatalf("create cache fixture: %v", err)
	}
	parentID, _ := runtimeStringID(created["id"])
	if parentID == "" {
		parentID = runtimeGraphLookupID(t, parentExecutor, RecordMap{"name": "cache-parent"})
	}

	env := NewEnvironment(GetBunDB(), 0, nil, false)
	ctx := ContextWithEnvironment(context.Background(), env)

	first, err := parentExecutor.RuntimeSearchRead(ctx, RecordMap{"name": "cache-parent"}, []string{"id", "child_ids"})
	if err != nil {
		t.Fatalf("first read: %v", err)
	}
	if got := len(runtimeGraphRelationTuples(t, first[0]["child_ids"])); got != 1 {
		t.Fatalf("expected one child on first read, got %d", got)
	}
	firstCount := counter.childCount()
	if firstCount == 0 {
		t.Fatalf("expected grouped child select on first read")
	}

	second, err := parentExecutor.RuntimeSearchRead(ctx, RecordMap{"name": "cache-parent"}, []string{"id", "child_ids"})
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if got := len(runtimeGraphRelationTuples(t, second[0]["child_ids"])); got != 1 {
		t.Fatalf("expected one child on second read, got %d", got)
	}
	if counter.childCount() != firstCount {
		t.Fatalf("expected second read to reuse cache, counts %d -> %d", firstCount, counter.childCount())
	}

	if _, err := parentExecutor.RuntimeWrite(ctx, []string{parentID}, RecordMap{
		"child_ids": []any{
			[]any{0, 0, RecordMap{"name": "child-b", "kind": "refund"}},
		},
	}); err != nil {
		t.Fatalf("write with child mutation: %v", err)
	}

	third, err := parentExecutor.RuntimeSearchRead(ctx, RecordMap{"name": "cache-parent"}, []string{"id", "child_ids"})
	if err != nil {
		t.Fatalf("third read: %v", err)
	}
	if got := len(runtimeGraphRelationTuples(t, third[0]["child_ids"])); got != 2 {
		t.Fatalf("expected cache invalidation to expose two children, got %d", got)
	}
	if counter.childCount() <= firstCount {
		t.Fatalf("expected invalidation to trigger a fresh grouped child select")
	}
}

func TestRuntimeHighLevelMethodsUseRealRelationalGraphs(t *testing.T) {
	_, _, cleanup := setupRuntimeGraphFixture(t)
	defer cleanup()

	tagExecutor, _ := lookupRuntimeExecutor(runtimeGraphTagModel)
	tagA, err := tagExecutor.RuntimeCreate(context.Background(), RecordMap{"name": "Tag A"})
	if err != nil {
		t.Fatalf("create tag A: %v", err)
	}
	tagB, err := tagExecutor.RuntimeCreate(context.Background(), RecordMap{"name": "Tag B"})
	if err != nil {
		t.Fatalf("create tag B: %v", err)
	}
	tagAID, _ := runtimeStringID(tagA["id"])
	tagBID, _ := runtimeStringID(tagB["id"])
	if tagAID == "" {
		tagAID = runtimeGraphLookupID(t, tagExecutor, RecordMap{"name": "Tag A"})
	}
	if tagBID == "" {
		tagBID = runtimeGraphLookupID(t, tagExecutor, RecordMap{"name": "Tag B"})
	}

	parentExecutor, _ := lookupRuntimeExecutor(runtimeGraphParentModel)
	parentMeta, _ := LookupModel(runtimeGraphParentModel)
	childMeta, _ := LookupModel(runtimeGraphChildModel)
	parentA, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{
		"name":    "Parent Alpha",
		"code":    "alpha",
		"email":   "alpha@example.com",
		"tag_ids": []string{tagAID, tagBID},
	})
	if err != nil {
		t.Fatalf("create parent A: %v", err)
	}
	parentAID, _ := runtimeStringID(parentA["id"])
	if parentAID == "" {
		parentAID = runtimeGraphLookupID(t, parentExecutor, RecordMap{"name": "Parent Alpha"})
	}

	parentB, err := parentExecutor.RuntimeCreate(context.Background(), RecordMap{
		"name":  "Parent Beta",
		"code":  "beta",
		"email": "beta@example.com",
		"child_ids": []any{
			[]any{0, 0, RecordMap{"name": "Child One", "kind": "sale"}},
		},
	})
	if err != nil {
		t.Fatalf("create parent B: %v", err)
	}
	parentBID, _ := runtimeStringID(parentB["id"])
	if parentBID == "" {
		parentBID = runtimeGraphLookupID(t, parentExecutor, RecordMap{"name": "Parent Beta"})
	}

	childExecutor, _ := lookupRuntimeExecutor(runtimeGraphChildModel)
	children, err := childExecutor.RuntimeSearchRead(context.Background(), RecordMap{"parent_id": toInt64(parentBID)}, []string{"id", "parent_name", "kind"})
	if err != nil {
		t.Fatalf("search child for parent B: %v", err)
	}
	if len(children) != 1 || children[0]["parent_name"] != "Parent Beta" {
		t.Fatalf("expected inverse-edge related recompute, got %#v", children)
	}
	childID, _ := runtimeStringID(children[0]["id"])

	if _, err := parentExecutor.RuntimeWrite(context.Background(), []string{parentAID}, RecordMap{
		"child_ids": []any{
			[]any{4, childID, []any{}},
		},
	}); err != nil {
		t.Fatalf("link child onto parent A: %v", err)
	}
	moved, err := childExecutor.RuntimeSearchRead(context.Background(), RecordMap{"id": toInt64(childID)}, []string{"id", "parent_id", "parent_name"})
	if err != nil {
		t.Fatalf("read moved child: %v", err)
	}
	parentTuple, _ := moved[0]["parent_id"].([2]any)
	if parentTuple[1] != "Parent Alpha" || moved[0]["parent_name"] != "Parent Alpha" {
		t.Fatalf("expected moved child to recompute inverse related fields, got %#v", moved[0])
	}
	if _, err := parentExecutor.RuntimeWrite(context.Background(), []string{parentAID}, RecordMap{"name": "Parent Alpha Renamed"}); err != nil {
		t.Fatalf("rename parent A: %v", err)
	}
	renamed, err := childExecutor.RuntimeSearchRead(context.Background(), RecordMap{"id": toInt64(childID)}, []string{"id", "parent_name"})
	if err != nil {
		t.Fatalf("read renamed child relation: %v", err)
	}
	if renamed[0]["parent_name"] != "Parent Alpha Renamed" {
		t.Fatalf("expected source-field rename to recompute inverse related fields, got %#v", renamed[0])
	}

	searchFetch, err := runtimeSearchFetch(context.Background(), parentMeta, nil, nil, map[string]any{
		"filter": RecordMap{"id": toInt64(parentAID)},
		"fields": []string{"id", "name", "tag_ids", "child_ids"},
	})
	if err != nil {
		t.Fatalf("search_fetch: %v", err)
	}
	searchFetchRows := searchFetch.([]RecordMap)
	if len(searchFetchRows) != 1 || len(runtimeGraphRelationTuples(t, searchFetchRows[0]["tag_ids"])) != 2 {
		t.Fatalf("expected search_fetch to return relational tuples, got %#v", searchFetchRows)
	}

	nameSearch, err := runtimeNameSearch(context.Background(), parentMeta, nil, []any{"Parent"}, map[string]any{"limit": 5})
	if err != nil {
		t.Fatalf("name_search: %v", err)
	}
	if got := len(nameSearch.([][2]any)); got < 2 {
		t.Fatalf("expected name_search to find both parents, got %d", got)
	}

	readGroup, err := runtimeReadGroup(context.Background(), childMeta, nil, nil, map[string]any{
		"groupby": []string{"kind"},
		"fields":  []string{"kind"},
	})
	if err != nil {
		t.Fatalf("read_group: %v", err)
	}
	if len(readGroup.([]RecordMap)) == 0 {
		t.Fatalf("expected read_group rows")
	}

	copyData, err := runtimeCopyData(context.Background(), parentMeta, []string{parentAID}, nil, nil)
	if err != nil {
		t.Fatalf("copy_data: %v", err)
	}
	copyRows := copyData.([]RecordMap)
	if len(copyRows) != 1 || len(runtimeGraphRelationTuples(t, copyRows[0]["tag_ids"])) != 2 {
		t.Fatalf("expected copy_data to include many2many tuples, got %#v", copyRows)
	}

	copied, err := runtimeCopy(context.Background(), parentMeta, []string{parentAID}, nil, nil)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	copiedRow := copied.(RecordMap)
	copiedID, _ := runtimeStringID(copiedRow["id"])
	if copiedID == parentAID {
		t.Fatalf("expected copy to create a new record, got %#v", copiedRow)
	}
	copiedCode := strings.TrimSpace(fmt.Sprint(copiedRow["code"]))
	if !strings.HasPrefix(copiedCode, "alpha-") || len(copiedCode) != len("alpha-")+16 {
		t.Fatalf("expected indexed code to gain 16-char hex suffix, got %#v", copiedRow["code"])
	}
	copiedEmail := strings.TrimSpace(fmt.Sprint(copiedRow["email"]))
	if !strings.HasPrefix(copiedEmail, "alpha+") || !strings.HasSuffix(copiedEmail, "@example.com") {
		t.Fatalf("expected unique email to preserve email shape with suffix, got %#v", copiedRow["email"])
	}
	emailLocalPart := strings.TrimSuffix(strings.TrimPrefix(copiedEmail, "alpha+"), "@example.com")
	if len(emailLocalPart) != 16 {
		t.Fatalf("expected copied email local-part hex suffix length 16, got %q", emailLocalPart)
	}

	onchange, err := runtimeOnchange(context.Background(), parentMeta, nil, []any{RecordMap{"name": "Echo"}, []any{"name"}}, nil)
	if err != nil {
		t.Fatalf("onchange: %v", err)
	}
	values, _ := onchange.(map[string]any)["value"].(RecordMap)
	if values["name_echo"] != "Echo" {
		t.Fatalf("expected onchange to run against the registered runtime hook, got %#v", onchange)
	}
}
