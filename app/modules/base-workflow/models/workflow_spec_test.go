package models

// File overview:
// - Responsibility: verifies workflow model state, metadata, and persistence behavior for workflow spec.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down workflow model semantics so request IDs, run state, and action definitions remain replay-safe and observable.

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestAsyncAuditModelSpecsResolveFixedStorageNames(t *testing.T) {
	if got := OutboundDeliveryModelSpec.ResolveStorageName(); got != "base_outbound_delivery" {
		t.Fatalf("expected outbound delivery storage name to be fixed in code, got %q", got)
	}
	if got := WorkflowRunModelSpec.ResolveStorageName(); got != "base_workflow_run" {
		t.Fatalf("expected workflow run storage name to be fixed in code, got %q", got)
	}
}

// TestWorkflowActionCatalogSeedMatchesNodeDefs keeps data/11_etl_action_catalog.json 1:1 with the
// registered node definitions (plan T93): every definition has a seed row carrying the same
// derived catalog entry, and every seed row has a definition.
func TestWorkflowActionCatalogSeedMatchesNodeDefs(t *testing.T) {
	type seedRow struct {
		Model string `json:"model"`
		Data  struct {
			Name           string         `json:"name"`
			Code           string         `json:"code"`
			Version        int            `json:"version"`
			Description    string         `json:"description"`
			HandlerKey     string         `json:"handler_key"`
			HandlerVersion int            `json:"handler_version"`
			DefaultParams  map[string]any `json:"default_params"`
		} `json:"data"`
	}

	raw, err := os.ReadFile("../data/11_etl_action_catalog.json")
	if err != nil {
		t.Fatalf("read workflow action catalog seed: %v", err)
	}
	var rows []seedRow
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatalf("decode workflow action catalog seed: %v", err)
	}

	actions := BuiltinETLActions()
	if len(actions) == 0 {
		t.Fatal("no node definitions registered: service/nodes is not linked into this test binary")
	}
	var seeded []ETLActionCatalogEntry
	for _, row := range rows {
		if row.Model != WorkflowActionCatalogModelName {
			continue
		}
		seeded = append(seeded, ETLActionCatalogEntry{
			Label:          row.Data.Name,
			Name:           row.Data.Code,
			Version:        row.Data.Version,
			Description:    row.Data.Description,
			HandlerKey:     row.Data.HandlerKey,
			HandlerVersion: row.Data.HandlerVersion,
			DefaultParams:  row.Data.DefaultParams,
		})
	}
	// Compare through JSON so seed numbers (float64) and Go defaults (int, bool) meet as JSON.
	// Order matters too: it is NodeDef.Sequence, the canvas fallback order.
	wantJSON, _ := json.Marshal(seeded)
	gotJSON, _ := json.Marshal(actions)
	var want, got any
	_ = json.Unmarshal(wantJSON, &want)
	_ = json.Unmarshal(gotJSON, &got)
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("catalog seed and node definitions differ\nseed:     %s\nregistry: %s", wantJSON, gotJSON)
	}
}
