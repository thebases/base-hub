package routes

// File overview:
// - Responsibility: locks the GET /wf/node-specs JSON shape (plan T93). The node specs moved from a
//   switch in this package to the node definitions registered by service/nodes; the golden file
//   testdata/node_specs_golden.json was captured from the switch-based code before that move.
// - Key symbols: `TestWorkflowNodeSpecsMatchGolden`.

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// nodeSpecAdditiveKeys are the param keys added by T93 (storage target, secret picker, value
// type). The SPA ignores unknown keys, so they are stripped before comparing with the golden.
var nodeSpecAdditiveKeys = []string{"column", "extra_key", "secret", "value_type"}

type nodeSpecGolden struct {
	Triggers  []workflowmodels.TriggerNodeSpec         `json:"triggers"`
	Fallback  []workflowmodels.ActionNodeSpec          `json:"fallback_actions"`
	ByHandler map[string]workflowmodels.ActionNodeSpec `json:"catalog_by_handler_key"`
}

func TestWorkflowNodeSpecsMatchGolden(t *testing.T) {
	// No runtime environment: the catalog and automation.action lookups fail, which exercises the
	// static fallback and the empty Custom Action select, exactly as when the golden was captured.
	ctx := context.Background()
	got := nodeSpecGolden{
		Triggers:  buildWorkflowNodeSpecsResponse(ctx).Triggers,
		Fallback:  withDynamicActionParams(ctx, workflowBuiltinActionSpecs()),
		ByHandler: map[string]workflowmodels.ActionNodeSpec{},
	}
	for _, def := range workflowmodels.NodeDefs() {
		spec := workflowCatalogActionSpec(def.Code, 7, def.Label, def.Code, def.Version)
		got.ByHandler[def.Code] = withDynamicActionParams(ctx, []workflowmodels.ActionNodeSpec{spec})[0]
	}

	want, err := os.ReadFile("testdata/node_specs_golden.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal node specs: %v", err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(gotJSON, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	stripNodeSpecAdditiveKeys(gotValue)
	if !reflect.DeepEqual(gotValue, wantValue) {
		pretty, _ := json.MarshalIndent(gotValue, "", "  ")
		t.Fatalf("node specs drifted from testdata/node_specs_golden.json; got:\n%s", pretty)
	}
}

func TestWorkflowNodeSpecActionParamsCarryStorage(t *testing.T) {
	for _, spec := range workflowBuiltinActionSpecs() {
		for _, param := range spec.Params {
			if (param.Column == "") == (param.ExtraKey == "") {
				t.Fatalf("%s.%s: want exactly one of column/extra_key, got %q/%q", spec.Code, param.Name, param.Column, param.ExtraKey)
			}
		}
	}
}

// TestWorkflowCatalogActionSpecUnknownHandler keeps the old switch's default case: a catalog row
// whose handler has no definition renders as a param-less "Action".
func TestWorkflowCatalogActionSpecUnknownHandler(t *testing.T) {
	spec := workflowCatalogActionSpec("custom", 9, "Custom", "no_such_handler", 1)
	if spec.Category != "Action" || spec.Params != nil || spec.Code != "custom" || spec.CatalogID != 9 || spec.Label != "Custom" {
		t.Fatalf("unexpected spec %+v", spec)
	}
}

func stripNodeSpecAdditiveKeys(value any) {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range nodeSpecAdditiveKeys {
			delete(typed, key)
		}
		for _, item := range typed {
			stripNodeSpecAdditiveKeys(item)
		}
	case []any:
		for _, item := range typed {
			stripNodeSpecAdditiveKeys(item)
		}
	}
}
