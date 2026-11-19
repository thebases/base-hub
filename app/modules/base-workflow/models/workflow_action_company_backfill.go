package models

// Boot backfill of workflow child companies (plan T41), run on every migrated database.
//
// Actions recreated by the canvas save (replace_action_sequence) were stored with no company, and
// child writes could set any company. Every action, trigger, edge, run and workflow-scoped
// credential is set to its workflow's company; outbound rows (no workflow link) take the company
// of the run with the same request id ("<request id>" for notify, "<request id>-<uuid>" for
// post_output_object). Idempotent: only rows that differ are updated.
//
// File name keeps this before workflow_action_secret_migration.go (registration order = run
// order), so credentials moved out of actions take the corrected company.

import (
	"context"
	"fmt"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

func init() {
	orm.RegisterLegacyDataMigration("base-workflow: child rows follow their workflow's company", backfillWorkflowChildCompanies)
}

func backfillWorkflowChildCompanies(ctx context.Context, tx bun.IDB) error {
	flowTable := FlowModelSpec.ResolveStorageName()
	var flowInstalled bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", flowTable).Scan(ctx, &flowInstalled); err != nil {
		return fmt.Errorf("check workflow table: %w", err)
	}
	if !flowInstalled {
		return nil
	}
	for _, spec := range []orm.ModelSpec{WorkflowActionModelSpec, WorkflowTriggerModelSpec, WorkflowEdgeModelSpec, WorkflowRunModelSpec, WorkflowCredentialModelSpec} {
		table := spec.ResolveStorageName()
		if ok, err := workflowTableHasCompany(ctx, tx, table); err != nil || !ok {
			if err != nil {
				return err
			}
			continue
		}
		result, err := tx.NewRaw(`UPDATE ? AS c SET company_id = w.company_id FROM ? AS w
			WHERE c.workflow_id = w.id AND c.company_id IS DISTINCT FROM w.company_id`,
			bun.Ident(table), bun.Ident(flowTable)).Exec(ctx)
		if err != nil {
			return fmt.Errorf("backfill %s company: %w", table, err)
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			log.Infof("base-workflow: %s: %d row(s) moved to their workflow's company", table, affected)
		}
	}

	runTable := WorkflowRunModelSpec.ResolveStorageName()
	outboundTable := OutboundDeliveryModelSpec.ResolveStorageName()
	runOK, err := workflowTableHasCompany(ctx, tx, runTable)
	if err != nil {
		return err
	}
	outboundOK, err := workflowTableHasCompany(ctx, tx, outboundTable)
	if err != nil || !runOK || !outboundOK {
		return err
	}
	result, err := tx.NewRaw(`UPDATE ? AS o SET company_id = r.company_id FROM ? AS r
		WHERE o.company_id IS NULL AND r.company_id IS NOT NULL AND r.name <> ''
			AND (o.name = r.name OR o.name LIKE r.name || '-%')`,
		bun.Ident(outboundTable), bun.Ident(runTable)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("backfill %s company: %w", outboundTable, err)
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		log.Infof("base-workflow: %s: %d row(s) took their run's company", outboundTable, affected)
	}
	return nil
}

// workflowTableHasCompany reports whether table exists with a company_id column.
func workflowTableHasCompany(ctx context.Context, tx bun.IDB, table string) (bool, error) {
	var ok bool
	err := tx.NewRaw(`SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = ? AND column_name = 'company_id' AND table_schema = ANY (current_schemas(true)))`, table).Scan(ctx, &ok)
	if err != nil {
		return false, fmt.Errorf("check %s.company_id: %w", table, err)
	}
	return ok, nil
}
