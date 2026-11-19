package models

// base.workflow.version: one row per published version of a workflow (plan T97, see
// workflow_publish.go). The engine resumes a run on the version it was started with, so every
// version a run may still point at is kept; retention (T96) may later prune versions no live run
// references. Rows are written only by publish and the boot backfill below: every RPC mutation is
// Internal.
//
// Boot backfill (every migrated database, idempotent): workflows that existed before T97 got
// state = published from the column default but no snapshot. Each one is frozen from its current
// rows as version max(version, 1), the number runs started before T97 stored as flow_version, so
// those runs resume on the same definition. Only rows still without a snapshot are touched, and the
// version row is inserted ON CONFLICT DO NOTHING, so a rerun changes nothing. A workflow whose rows
// cannot be decoded is skipped with a warning and keeps running from its rows (engine fallback).

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type WorkflowVersionRecord struct {
	orm.BaseModel
	WorkflowID *int64         `bun:"workflow_id" json:"workflow_id" meta:"kind:many2one,model:base.workflow,ondelete:CASCADE,index,label:Workflow,help:Workflow this published version belongs to."`
	CompanyID  *int64         `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent workflow."`
	Version    int            `bun:"version" json:"version" meta:"index,readonly,label:Version,help:Published version number. Runs store it as flow_version."`
	Revision   int64          `bun:"revision,default:0" json:"revision" meta:"readonly,label:Revision,help:Workflow revision that was published."`
	Snapshot   map[string]any `bun:"snapshot,type:jsonb,nullzero" json:"snapshot" meta:"kind:json,readonly,label:Snapshot,help:Frozen steps and edges and execution settings of this version."`
}

const WorkflowVersionModelName = "base.workflow.version"

var WorkflowVersionModelSpec = orm.ModelSpec{
	Name:        WorkflowVersionModelName,
	Label:       "Workflow Version",
	Description: "Published, immutable snapshot of a workflow definition",
	RuntimeMethods: []orm.RuntimeMethod{
		// Versions are immutable history written by publish only (plan T97).
		{Name: "create", Public: true, Internal: true, ModelOnly: true, Call: refuseWorkflowVersionMutation},
		{Name: "write", Public: true, Internal: true, UsesPathID: true, Call: refuseWorkflowVersionMutation},
		{Name: "unlink", Public: true, Internal: true, UsesPathID: true, Call: refuseWorkflowVersionMutation},
	},
}

var WorkflowVersionModel = orm.Define[WorkflowVersionRecord](&Models, 115, WorkflowVersionModelSpec)

func refuseWorkflowVersionMutation(context.Context, *orm.ModelMetadata, []string, []any, map[string]any) (any, error) {
	return nil, orm.Forbiddenf("base.workflow.version rows are written by base.workflow.publish only")
}

const workflowVersionUniqueIndex = "base_workflow_version_workflow_version_uniq"

// workflowNoSnapshotSQL matches a workflow row with no snapshot ('{}' too, in case a jsonb default
// or an ORM write stored an empty object).
const workflowNoSnapshotSQL = "(published_snapshot IS NULL OR published_snapshot = '{}'::jsonb)"

func init() {
	// Index first: the backfill's ON CONFLICT needs it (registration order = run order).
	orm.RegisterLegacyDataMigration("base-workflow: unique version per workflow", ensureWorkflowVersionIndex)
	orm.RegisterLegacyDataMigration("base-workflow: snapshot workflows published before versioning", backfillWorkflowPublishedSnapshots)
}

func ensureWorkflowVersionIndex(ctx context.Context, tx bun.IDB) error {
	table := WorkflowVersionModelSpec.ResolveStorageName()
	installed, err := workflowTableInstalled(ctx, tx, table)
	if err != nil || !installed {
		return err
	}
	if _, err := tx.NewRaw("CREATE UNIQUE INDEX IF NOT EXISTS ? ON ? (workflow_id, version)",
		bun.Ident(workflowVersionUniqueIndex), bun.Ident(table)).Exec(ctx); err != nil {
		return fmt.Errorf("create %s: %w", workflowVersionUniqueIndex, err)
	}
	return nil
}

func workflowTableInstalled(ctx context.Context, tx bun.IDB, table string) (bool, error) {
	var installed bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", table).Scan(ctx, &installed); err != nil {
		return false, fmt.Errorf("check %s: %w", table, err)
	}
	return installed, nil
}

func backfillWorkflowPublishedSnapshots(ctx context.Context, tx bun.IDB) error {
	flowTable := FlowModelSpec.ResolveStorageName()
	versionTable := WorkflowVersionModelSpec.ResolveStorageName()
	for _, table := range []string{flowTable, versionTable} {
		if installed, err := workflowTableInstalled(ctx, tx, table); err != nil || !installed {
			return err
		}
	}
	var ids []int64
	if err := tx.NewRaw("SELECT id FROM ? WHERE state = ? AND "+workflowNoSnapshotSQL+" ORDER BY id",
		bun.Ident(flowTable), WorkflowStatePublished).Scan(ctx, &ids); err != nil {
		return fmt.Errorf("list workflows to snapshot: %w", err)
	}
	done := 0
	for _, id := range ids {
		ok, err := backfillWorkflowSnapshot(ctx, tx, id)
		if err != nil {
			return err
		}
		if ok {
			done++
		}
	}
	if done > 0 {
		log.Infof("base-workflow: %d workflow(s) published as their current definition", done)
	}
	return nil
}

// backfillWorkflowSnapshot freezes one workflow; false when it was skipped.
func backfillWorkflowSnapshot(ctx context.Context, tx bun.IDB, workflowID int64) (bool, error) {
	flows, err := workflowRowsAsJSON(ctx, tx, "SELECT to_jsonb(w) FROM ? AS w WHERE w.id = ?", FlowModelSpec.ResolveStorageName(), workflowID)
	if err != nil || len(flows) == 0 {
		return false, err
	}
	actions, err := workflowRowsAsJSON(ctx, tx, "SELECT to_jsonb(a) FROM ? AS a WHERE a.workflow_id = ? ORDER BY a.sequence ASC, a.id ASC", WorkflowActionModelSpec.ResolveStorageName(), workflowID)
	if err != nil {
		return false, err
	}
	edges, err := workflowRowsAsJSON(ctx, tx, "SELECT to_jsonb(e) FROM ? AS e WHERE e.workflow_id = ? AND e.active IS NOT FALSE ORDER BY e.id ASC", WorkflowEdgeModelSpec.ResolveStorageName(), workflowID)
	if err != nil {
		return false, err
	}
	def := workflowDefinition{}
	for _, row := range actions {
		def.Steps = append(def.Steps, workflowDefStep{StepKey: stringValue(row["step_key"]), Columns: row})
	}
	for _, row := range edges {
		def.Edges = append(def.Edges, workflowDefEdge{
			ID: int64Value(row["id"]), From: stringValue(row["from_step_key"]), To: stringValue(row["to_step_key"]),
			EdgeType: stringValue(row["edge_type"]), ConditionExpr: stringValue(row["condition_expr"]),
		})
	}
	flow := flows[0]
	version := int(max(int64Value(flow["version"]), 1))
	snapshot, err := buildWorkflowSnapshot(flow, def, version, int64Value(flow["revision"]))
	if err != nil {
		log.Warnf("base-workflow: workflow %d not snapshotted (it keeps running from its rows): %v", workflowID, err)
		return false, nil
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return false, fmt.Errorf("encode workflow %d snapshot: %w", workflowID, err)
	}
	if _, err := tx.NewRaw(`INSERT INTO ? (workflow_id, company_id, version, revision, snapshot, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?::jsonb, TRUE, NOW(), NOW()) ON CONFLICT (workflow_id, version) DO NOTHING`,
		bun.Ident(WorkflowVersionModelSpec.ResolveStorageName()), workflowID, int64PointerFromAny(flow["company_id"]),
		version, snapshot.Revision, string(encoded)).Exec(ctx); err != nil {
		return false, fmt.Errorf("store workflow %d version %d: %w", workflowID, version, err)
	}
	if _, err := tx.NewRaw(`UPDATE ? SET published_snapshot = ?::jsonb, published_version = ?, published_at = NOW()
		WHERE id = ? AND `+workflowNoSnapshotSQL,
		bun.Ident(FlowModelSpec.ResolveStorageName()), string(encoded), version, workflowID).Exec(ctx); err != nil {
		return false, fmt.Errorf("publish workflow %d: %w", workflowID, err)
	}
	return true, nil
}

// workflowRowsAsJSON runs query (one to_jsonb column; ? = table, then id) and decodes each row, so
// column names come out as the model's json keys (json tag == bun column).
func workflowRowsAsJSON(ctx context.Context, tx bun.IDB, query, table string, id int64) ([]orm.RecordMap, error) {
	var raws []string
	if err := tx.NewRaw(query, bun.Ident(table), id).Scan(ctx, &raws); err != nil {
		return nil, fmt.Errorf("read %s rows of workflow %d: %w", table, id, err)
	}
	rows := make([]orm.RecordMap, 0, len(raws))
	for _, raw := range raws {
		var row orm.RecordMap
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			return nil, fmt.Errorf("decode %s row of workflow %d: %w", table, id, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}
