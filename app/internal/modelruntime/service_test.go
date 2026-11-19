package modelruntime

// File overview:
// - Responsibility: verifies runtime-service request dispatch and typed facade behavior for service.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down the service-to-ORM contract so generic callers keep the same method semantics and result typing.

import (
	"context"
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
)

type serviceTestDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" default:"Draft"`
}

type serviceTestAltDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" default:"Draft"`
}

type serviceTestReadDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" default:"Draft"`
}

type serviceTestFalseNormalizationDocument struct {
	orm.BaseModel
	Name   string `bun:"name" json:"name" default:"Draft"`
	Active bool   `bun:"active" json:"active" default:"true"`
}

type serviceTestRequestIsolationDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" default:"Draft"`
}

func TestExecuteRejectsMissingModelAndMethod(t *testing.T) {
	svc := NewService()

	if _, err := svc.Execute(context.Background(), Request{Method: "search"}); err == nil {
		t.Fatalf("expected model validation error")
	}
	if _, err := svc.Execute(context.Background(), Request{Model: "test.runtime_phase2"}); err == nil {
		t.Fatalf("expected method validation error")
	}
}

func TestServiceRuntimeWrappersDispatchToRegisteredModel(t *testing.T) {
	const modelName = "test.runtime_phase2_dispatch"

	runtest.RegisterModelType[serviceTestDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Runtime Phase 2 Dispatch",
		Definition: orm.ModelDefinition(modelName, (*serviceTestDocument)(nil)),
	}.WithStorageName("runtime_phase2_dispatch"))

	orm.RegisterRuntimeExecutor(modelName, &runtest.ExecutorStub{
		SearchResult:      []any{int64(11), int64(22)},
		SearchCountResult: 2,
		ReadResult:        []orm.RecordMap{{"id": int64(11), "name": "A"}},
		SearchReadResult:  []orm.RecordMap{{"id": int64(22), "name": "B"}},
		CreateResult:      orm.RecordMap{"id": int64(33), "name": "Created"},
		WriteResult:       true,
		UnlinkResult:      true,
	})

	svc := NewService()

	fields, err := svc.FieldsGet(context.Background(), modelName, []string{"name"})
	if err != nil {
		t.Fatalf("fields_get: %v", err)
	}
	if _, ok := fields["name"]; !ok {
		t.Fatalf("expected name field metadata, got %#v", fields)
	}

	defaults, err := svc.DefaultGet(context.Background(), modelName, []string{"name"})
	if err != nil {
		t.Fatalf("default_get: %v", err)
	}
	if defaults["name"] != "Draft" {
		t.Fatalf("expected Draft default, got %#v", defaults["name"])
	}

	ids, err := svc.Search(context.Background(), modelName, orm.RecordMap{"name": "A"})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %#v", ids)
	}

	count, err := svc.SearchCount(context.Background(), modelName, orm.RecordMap{"name": "A"})
	if err != nil {
		t.Fatalf("search_count: %v", err)
	}
	if count != 2 {
		t.Fatalf("expected search_count=2, got %d", count)
	}

	records, err := svc.Read(context.Background(), modelName, []string{"11"}, []string{"name"})
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(records) != 1 || records[0]["name"] != "A" {
		t.Fatalf("unexpected read result %#v", records)
	}

	searchRead, err := svc.SearchRead(context.Background(), modelName, orm.RecordMap{"name": "B"}, []string{"name"})
	if err != nil {
		t.Fatalf("search_read: %v", err)
	}
	if len(searchRead) != 1 || searchRead[0]["name"] != "B" {
		t.Fatalf("unexpected search_read result %#v", searchRead)
	}

	searchFetch, err := svc.SearchFetch(context.Background(), modelName, orm.RecordMap{"name": "B"}, []string{"name"})
	if err != nil {
		t.Fatalf("search_fetch: %v", err)
	}
	if len(searchFetch) != 1 || searchFetch[0]["name"] != "B" {
		t.Fatalf("unexpected search_fetch result %#v", searchFetch)
	}

	nameSearch, err := svc.NameSearch(context.Background(), modelName, "B", orm.RecordMap{"name": "B"}, 10)
	if err != nil {
		t.Fatalf("name_search: %v", err)
	}
	if len(nameSearch) != 1 || nameSearch[0][1] != "B" {
		t.Fatalf("unexpected name_search result %#v", nameSearch)
	}

	nameGet, err := svc.NameGet(context.Background(), modelName, []string{"11"})
	if err != nil {
		t.Fatalf("name_get: %v", err)
	}
	if len(nameGet) != 1 || nameGet[0][1] != "A" {
		t.Fatalf("unexpected name_get result %#v", nameGet)
	}

	readGroup, err := svc.ReadGroup(context.Background(), modelName, orm.RecordMap{"name": "B"}, []string{"name"}, []string{"name"})
	if err != nil {
		t.Fatalf("read_group: %v", err)
	}
	if len(readGroup) != 1 || readGroup[0]["name"] != "B" {
		t.Fatalf("unexpected read_group result %#v", readGroup)
	}

	existing, err := svc.Exists(context.Background(), modelName, []string{"11", "99"})
	if err != nil {
		t.Fatalf("exists: %v", err)
	}
	if len(existing) != 1 || existing[0] != int64(11) {
		t.Fatalf("unexpected exists result %#v", existing)
	}

	created, err := svc.Create(context.Background(), modelName, orm.RecordMap{"name": "Created"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created["id"] != int64(33) {
		t.Fatalf("unexpected create result %#v", created)
	}

	copyData, err := svc.CopyData(context.Background(), modelName, []string{"11"})
	if err != nil {
		t.Fatalf("copy_data: %v", err)
	}
	if len(copyData) != 1 || copyData[0]["name"] != "A" {
		t.Fatalf("unexpected copy_data result %#v", copyData)
	}

	copied, err := svc.Copy(context.Background(), modelName, []string{"11"})
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	copiedRecord, ok := copied.(orm.RecordMap)
	if !ok || copiedRecord["id"] != int64(33) {
		t.Fatalf("unexpected copy result %#v", copied)
	}

	written, err := svc.Write(context.Background(), modelName, []string{"33"}, orm.RecordMap{"name": "Updated"})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if !written {
		t.Fatalf("expected write to succeed")
	}

	deleted, err := svc.Unlink(context.Background(), modelName, []string{"33"})
	if err != nil {
		t.Fatalf("unlink: %v", err)
	}
	if !deleted {
		t.Fatalf("expected unlink to succeed")
	}

	orm.RegisterOnchange(modelName, []string{"name"}, func(_ context.Context, _ orm.RecordSet, _ orm.RecordMap, _ []string, _ map[string]any) (orm.RecordMap, error) {
		return orm.RecordMap{"display_name": "Changed"}, nil
	})
	onchange, err := svc.Onchange(context.Background(), modelName, orm.RecordMap{"name": "B"}, []string{"name"}, nil)
	if err != nil {
		t.Fatalf("onchange: %v", err)
	}
	valueMap, _ := onchange["value"].(orm.RecordMap)
	if valueMap["display_name"] != "Changed" {
		t.Fatalf("unexpected onchange result %#v", onchange)
	}
}

func TestReadRequiresIDs(t *testing.T) {
	const modelName = "test.runtime_phase3_read_requitbl_ids"

	runtest.RegisterModelType[serviceTestReadDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Runtime Phase 3 Read Requires IDs",
		Definition: orm.ModelDefinition(modelName, (*serviceTestReadDocument)(nil)),
	}.WithStorageName("runtime_phase3_read_requitbl_ids"))

	svc := NewService()
	if _, err := svc.Read(context.Background(), modelName, nil, []string{"name"}); err == nil {
		t.Fatalf("expected read without ids to fail")
	}
}

func TestExecuteReturnsTypedErrorWhenMethodShapeDoesNotMatchWrapper(t *testing.T) {
	const modelName = "test.runtime_phase2_type_error"

	runtest.RegisterModelType[serviceTestAltDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Runtime Phase 2 Type Error",
		Definition: orm.ModelDefinition(modelName, (*serviceTestAltDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:      "search_count",
				Public:    true,
				ModelOnly: true,
				ReadOnly:  true,
				Call: func(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
					return "wrong-shape", nil
				},
			},
		},
	}.WithStorageName("runtime_phase2_type_error"))

	svc := NewService()
	if _, err := svc.SearchCount(context.Background(), modelName, nil); err == nil {
		t.Fatalf("expected typed wrapper error")
	}
}

func TestExecuteNormalizesNamesAndProtectsCallerOwnedPayloads(t *testing.T) {
	const modelName = "test.runtime_request_isolation"

	runtest.RegisterModelType[serviceTestRequestIsolationDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Runtime Request Isolation",
		Definition: orm.ModelDefinition(modelName, (*serviceTestRequestIsolationDocument)(nil)),
		RuntimeMethods: []orm.RuntimeMethod{
			{
				Name:      "inspect_payload",
				Public:    true,
				ModelOnly: true,
				ReadOnly:  true,
				Call: func(_ context.Context, meta *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
					// Mutate the received payload to prove the service boundary cloned it
					// before runtime dispatch handed ownership to the method implementation.
					ids[0] = "mutated-id"
					args[0] = "mutated-arg"
					kwargs["flag"] = "mutated-kwarg"
					return orm.RecordMap{
						"model":  meta.Name,
						"ids":    append([]string(nil), ids...),
						"args":   append([]any(nil), args...),
						"kwargs": kwargs,
					}, nil
				},
			},
		},
	}.WithStorageName("runtime_request_isolation"))

	req := Request{
		Model:  " " + modelName + " ",
		Method: " inspect_payload ",
		IDs:    []string{"1"},
		Args:   []any{"original-arg"},
		Kwargs: map[string]any{"flag": "original-kwarg"},
	}

	svc := NewService()
	result, err := svc.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	payload, ok := result.(orm.RecordMap)
	if !ok {
		t.Fatalf("expected orm.RecordMap, got %T", result)
	}
	if payload["model"] != modelName {
		t.Fatalf("expected trimmed model name, got %#v", payload["model"])
	}

	if got := req.IDs[0]; got != "1" {
		t.Fatalf("expected request ids to remain caller-owned, got %#v", got)
	}
	if got := req.Args[0]; got != "original-arg" {
		t.Fatalf("expected request args to remain caller-owned, got %#v", got)
	}
	if got := req.Kwargs["flag"]; got != "original-kwarg" {
		t.Fatalf("expected request kwargs to remain caller-owned, got %#v", got)
	}
}

// TestCreateAndWritePassCanonicalizedPayloadToExecutor verifies that the service
// layer canonicalizes field names before dispatching to the executor but does NOT
// normalize false values. False-value coercion (false→nil, bool-false skip) is
// applied inside RuntimeCreate/RuntimeWrite so every mutation path shares one
// rule set. Stub executors that bypass the ORM handler therefore receive the
// raw canonicalized values.
func TestCreateAndWritePassCanonicalizedPayloadToExecutor(t *testing.T) {
	const modelName = "test.runtime_false_normalization"

	runtest.RegisterModelType[serviceTestFalseNormalizationDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Runtime False Normalization",
		Definition: orm.ModelDefinition(modelName, (*serviceTestFalseNormalizationDocument)(nil)),
	}.WithStorageName("runtime_false_normalization"))

	executor := &runtest.ExecutorStub{
		CreateResult: orm.RecordMap{"id": int64(1), "name": "Created", "active": true},
		WriteResult:  true,
	}
	orm.RegisterRuntimeExecutor(modelName, executor)

	svc := NewService()

	if _, err := svc.Create(context.Background(), modelName, orm.RecordMap{
		"name":   false,
		"active": false,
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	// normalizePayload canonicalizes keys only; false values reach the executor
	// as-is and are converted inside RuntimeCreate (which the stub bypasses).
	if got, exists := executor.LastCreateValues["name"]; !exists || got != false {
		t.Fatalf("expected create name to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}
	if got, exists := executor.LastCreateValues["active"]; !exists || got != false {
		t.Fatalf("expected create active to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}

	if _, err := svc.Write(context.Background(), modelName, []string{"1"}, orm.RecordMap{
		"name":   false,
		"active": false,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got, exists := executor.LastWriteValues["name"]; !exists || got != false {
		t.Fatalf("expected write name to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}
	if got, exists := executor.LastWriteValues["active"]; !exists || got != false {
		t.Fatalf("expected write active to remain false after canonicalization, got %#v (exists=%v)", got, exists)
	}
}
