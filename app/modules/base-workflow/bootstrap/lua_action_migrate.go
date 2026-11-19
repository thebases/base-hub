package bootstrap

// File overview:
// - Responsibility: one-time, idempotent data migration for the "no inline Lua in workflows" rule.
//   lua_script@v1 steps used to carry their source in extra_params (code / lua_code / script); they
//   must now select a saved base-automation `lua` action. This converts every such legacy step into
//   an automation.action row (name "<workflow>: <step_key>") and rewrites the step to
//   `automation_action_id`, and refreshes the lua_script catalog row (description, default params)
//   that master-data import would only update on a forced reimport.
// - Key symbols: `ReconcileLuaScriptSteps`, `legacyLuaSource`, `rewriteLuaStepParams`.
// - Scope: every registered database (default and tenants), each under its own env (plan T57).
// - Safety: runs under a sudo root-company env like ReconcileWorkflowUIShell; a step is rewritten
//   only after its action row exists, so a crash between the two just re-converts on next boot
//   (an orphan action is reused by name+code rather than duplicated).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	automationActionModelName = "automation.action"
	luaScriptCatalogCode      = "lua_script"
	luaScriptCatalogDesc      = "Run a Lua custom action saved in Automation > Actions. It receives the previous node's output as payload and this node's custom JSON as data; base.call(model, method, kwargs) reaches model runtime methods in the run's database."
)

var legacyLuaCodeKeys = []string{"code", "lua_code", "script"}

type legacyLuaStep struct {
	ID           int64          `bun:"id"`
	StepKey      string         `bun:"step_key"`
	WorkflowName string         `bun:"workflow_name"`
	ExtraParams  map[string]any `bun:"extra_params,type:jsonb"`
}

// legacyLuaSource returns the inline script of a legacy step ("" when it has none).
func legacyLuaSource(params map[string]any) string {
	for _, key := range legacyLuaCodeKeys {
		if code, ok := params[key].(string); ok && strings.TrimSpace(code) != "" {
			return code
		}
	}
	return ""
}

// rewriteLuaStepParams drops the inline source keys and points the step at actionID, keeping every
// other key (e.g. a `data` object) untouched.
func rewriteLuaStepParams(params map[string]any, actionID int64) map[string]any {
	next := make(map[string]any, len(params)+1)
	for key, value := range params {
		next[key] = value
	}
	for _, key := range legacyLuaCodeKeys {
		delete(next, key)
	}
	next["automation_action_id"] = actionID
	return next
}

// luaMigrationDatabases lists the databases to migrate and luaMigrationOpenDB opens one by name
// (never creating it); both are swappable in tests.
var (
	luaMigrationDatabases = orm.RegisteredDBNames
	luaMigrationOpenDB    = func(name string) (*bun.DB, error) {
		registered, err := orm.EnsureNamedBunDBNoCreate(name)
		if err != nil {
			return nil, err
		}
		if registered == nil || registered.DB == nil {
			return nil, fmt.Errorf("database %q is not available", name)
		}
		return registered.DB, nil
	}
	reconcileLuaScriptStepsInDB = reconcileLuaScriptStepsInDatabase
)

// ReconcileLuaScriptSteps converts legacy inline-Lua workflow steps into saved automation actions
// and refreshes the lua_script catalog row in every registered database (plan T57: tenant
// databases hold their own workflows; this used to touch only the default database). One
// database's failure is reported and does not stop the others. Safe to run on every boot.
func ReconcileLuaScriptSteps(ctx context.Context) error {
	names := luaMigrationDatabases()
	if len(names) == 0 {
		names = []string{orm.ResolveLogicalDBName("")}
	}
	sort.Strings(names)
	var failures []error
	for _, name := range names {
		db, err := luaMigrationOpenDB(name)
		if err != nil {
			log.Warnf("workflow: lua step migration skipped db=%s: %v", name, err)
			continue
		}
		if err := reconcileLuaScriptStepsInDB(ctx, name, db); err != nil {
			failures = append(failures, fmt.Errorf("db %s: %w", name, err))
		}
	}
	return errors.Join(failures...)
}

// reconcileLuaScriptStepsInDatabase runs the migration on one database under a sudo root-company
// env bound to it, so the automation.action rows it creates land in that same database.
func reconcileLuaScriptStepsInDatabase(ctx context.Context, dbName string, db *bun.DB) error {
	if db == nil {
		return nil
	}
	ctx = orm.ContextWithEnvironment(ctx, orm.NewEnvironmentWithDBName(db, dbName, 0, map[string]any{
		orm.ContextKeyCurrentCompanyID:  rootCompanyID,
		orm.ContextKeyAllowedCompanyIDs: []int64{rootCompanyID},
	}, true))

	// Fresh or partially migrated databases (or ones without the workflow module) may not have
	// the tables yet; that is not a failure.
	if _, err := db.NewRaw(`SELECT 1 FROM base_workflow_action LIMIT 1`).Exec(ctx); err != nil {
		return nil
	}
	if _, err := db.NewRaw(`SELECT 1 FROM automation_action LIMIT 1`).Exec(ctx); err != nil {
		return nil
	}
	if err := refreshLuaCatalogRow(ctx, db); err != nil {
		return err
	}
	return convertLegacyLuaSteps(ctx, db)
}

// refreshLuaCatalogRow drops the obsolete inline `code` default from the seeded catalog row.
func refreshLuaCatalogRow(ctx context.Context, db bun.IDB) error {
	_, err := db.NewRaw(
		`UPDATE base_workflow_action_catalog
		    SET description = ?, default_params = '{"data": {}}'::jsonb
		  WHERE code = ? AND (jsonb_exists(default_params, 'code') OR description <> ?)`,
		luaScriptCatalogDesc, luaScriptCatalogCode, luaScriptCatalogDesc,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("refresh lua_script catalog row: %w", err)
	}
	return nil
}

func convertLegacyLuaSteps(ctx context.Context, db bun.IDB) error {
	var steps []legacyLuaStep
	if err := db.NewRaw(
		`SELECT a.id, a.step_key, COALESCE(w.name, '') AS workflow_name, a.extra_params
		   FROM base_workflow_action a
		   LEFT JOIN base_workflow w ON w.id = a.workflow_id
		  WHERE a.action_code = ?
		    AND a.extra_params IS NOT NULL
		    AND NOT jsonb_exists(a.extra_params, 'automation_action_id')
		    AND (jsonb_exists(a.extra_params, 'code') OR jsonb_exists(a.extra_params, 'lua_code') OR jsonb_exists(a.extra_params, 'script'))`,
		luaScriptCatalogCode,
	).Scan(ctx, &steps); err != nil {
		return fmt.Errorf("list legacy lua_script steps: %w", err)
	}
	for _, step := range steps {
		code := legacyLuaSource(step.ExtraParams)
		if code == "" {
			continue
		}
		actionID, err := ensureLuaAction(ctx, db, strings.TrimSpace(step.WorkflowName+": "+step.StepKey), code)
		if err != nil {
			return fmt.Errorf("convert lua step %d: %w", step.ID, err)
		}
		encoded, err := json.Marshal(rewriteLuaStepParams(step.ExtraParams, actionID))
		if err != nil {
			return err
		}
		if _, err := db.NewRaw(`UPDATE base_workflow_action SET extra_params = ?::jsonb WHERE id = ?`, string(encoded), step.ID).Exec(ctx); err != nil {
			return fmt.Errorf("rewrite lua step %d: %w", step.ID, err)
		}
		log.Infof("workflow: converted inline Lua of step %d (%s) to automation.action %d", step.ID, step.StepKey, actionID)
	}
	return nil
}

// ensureLuaAction returns the id of the lua automation.action with this name and code, creating
// it when absent so a re-run after a crash does not duplicate it.
func ensureLuaAction(ctx context.Context, db bun.IDB, name, code string) (int64, error) {
	var existing int64
	if err := db.NewRaw(
		`SELECT id FROM automation_action WHERE action_type = 'lua' AND name = ? AND lua_code = ? ORDER BY id LIMIT 1`,
		name, code,
	).Scan(ctx, &existing); err == nil && existing > 0 {
		return existing, nil
	}
	created, err := executeRuntimeMethod(ctx, automationActionModelName, "create", nil, nil, map[string]any{
		"values": map[string]any{
			"name":        name,
			"action_type": "lua",
			"lua_code":    code,
			"description": "Migrated from an inline lua_script workflow step.",
		},
	})
	if err != nil {
		return 0, err
	}
	record := orm.RecordMap{}
	if encoded, marshalErr := json.Marshal(created); marshalErr == nil {
		_ = json.Unmarshal(encoded, &record)
	}
	id, ok := record["id"].(float64)
	if !ok || id <= 0 {
		return 0, fmt.Errorf("automation.action create returned no id")
	}
	return int64(id), nil
}
