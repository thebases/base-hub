package models

// Boot migration for workflow secrets at rest (plan T40), run on every migrated database.
//
// 1. Each literal hmac_key / aes_key / authorization on base.workflow.action (and the same keys in
//    extra_params) is sealed into a base.workflow.credential row scoped to the action's workflow
//    and company, named "action_<id>_<field>", and the field is rewritten to "@cred:<name>". The
//    step resolves that reference to the same plaintext, so running workflows keep working.
// 2. Authorization headers stored on base.workflow.outbound rows before the reference column
//    existed are removed: those rows hold the resolved token. A manual retry of such a row is sent
//    without it.
//
// Idempotent: converted fields are references and are skipped; a credential with the generated
// name is reused. Without app.encryptKey (CLI paths only) step 1 is skipped and retried later.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

// Test seams.
var (
	workflowSecretMigrationAppKey = secretbox.AppKey
	workflowSecretMigrationNow    = func() time.Time { return time.Now().UTC() }
)

func init() {
	// Runs before the credential zero-key re-encryption registered in workflow_credential_crypto.go
	// (file order); that one skips the values sealed here because they already use the current key.
	orm.RegisterLegacyDataMigration("base-workflow: move literal action secrets to credentials", migrateWorkflowActionLiteralSecrets)
	orm.RegisterLegacyDataMigration("base-workflow: drop stored outbound authorization headers", scrubOutboundAuthorizationHeaders)
}

type workflowActionSecretRow struct {
	ID            int64
	WorkflowID    sql.NullInt64
	CompanyID     sql.NullInt64
	HmacKey       string
	AesKey        string
	Authorization string
	ExtraParams   sql.NullString
}

func workflowActionSecretRowFromMap(item map[string]any) workflowActionSecretRow {
	row := workflowActionSecretRow{
		ID:            migrationInt64(item["id"]).Int64,
		WorkflowID:    migrationInt64(item["workflow_id"]),
		CompanyID:     migrationInt64(item["company_id"]),
		HmacKey:       migrationString(item["hmac_key"]),
		AesKey:        migrationString(item["aes_key"]),
		Authorization: migrationString(item["authorization"]),
	}
	if item["extra_params"] != nil {
		row.ExtraParams = sql.NullString{String: migrationString(item["extra_params"]), Valid: true}
	}
	return row
}

func migrationInt64(value any) sql.NullInt64 {
	switch typed := value.(type) {
	case int64:
		return sql.NullInt64{Int64: typed, Valid: true}
	case int32:
		return sql.NullInt64{Int64: int64(typed), Valid: true}
	case int:
		return sql.NullInt64{Int64: int64(typed), Valid: true}
	}
	return sql.NullInt64{}
}

func migrationString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	}
	return ""
}

// workflowActionLiteralSecret is one literal found on an action row.
type workflowActionLiteralSecret struct {
	column         string // action column, or "" for an extra_params key
	extraKey       string
	value          string
	credentialName string
	credentialType string
}

// workflowActionLiteralSecrets lists the literal secrets of one action row.
func workflowActionLiteralSecrets(row workflowActionSecretRow, extra map[string]any) []workflowActionLiteralSecret {
	var out []workflowActionLiteralSecret
	columns := map[string]string{"hmac_key": row.HmacKey, "aes_key": row.AesKey, "authorization": row.Authorization}
	types := map[string]string{"hmac_key": "hmac_key", "aes_key": "aes_key", "authorization": "token", "key": "other"}
	for _, column := range workflowActionSecretColumns {
		value := strings.TrimSpace(columns[column])
		if value == "" || IsWorkflowCredentialRef(value) {
			continue
		}
		out = append(out, workflowActionLiteralSecret{
			column:         column,
			value:          value,
			credentialName: fmt.Sprintf("action_%d_%s", row.ID, column),
			credentialType: types[column],
		})
	}
	for _, key := range workflowActionSecretExtraParamKeys {
		raw, exists := extra[key]
		if !exists || isEmptyOrCredentialRef(raw) {
			continue
		}
		value := strings.TrimSpace(fmt.Sprint(raw))
		out = append(out, workflowActionLiteralSecret{
			extraKey:       key,
			value:          value,
			credentialName: fmt.Sprintf("action_%d_extra_%s", row.ID, key),
			credentialType: types[key],
		})
	}
	return out
}

func migrateWorkflowActionLiteralSecrets(ctx context.Context, tx bun.IDB) error {
	actionTable := WorkflowActionModelSpec.ResolveStorageName()
	credentialTable := WorkflowCredentialModelSpec.ResolveStorageName()
	var installed bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL AND to_regclass(?) IS NOT NULL", actionTable, credentialTable).Scan(ctx, &installed); err != nil {
		return fmt.Errorf("check workflow tables: %w", err)
	}
	if !installed {
		return nil
	}

	// Scanned into maps (as secretbox.ResealColumns does): there is no bun model for this table here.
	var raw []map[string]any
	err := tx.NewSelect().
		TableExpr("?", bun.Ident(actionTable)).
		ColumnExpr("id, workflow_id, company_id").
		ColumnExpr("COALESCE(hmac_key, '') AS hmac_key, COALESCE(aes_key, '') AS aes_key").
		ColumnExpr(`COALESCE("authorization", '') AS "authorization"`).
		ColumnExpr("extra_params::text AS extra_params").
		// Candidates only; workflowActionLiteralSecrets decides what is a literal.
		Where(`COALESCE(hmac_key, '') <> '' OR COALESCE(aes_key, '') <> '' OR COALESCE("authorization", '') <> ''
			OR (jsonb_typeof(extra_params) = 'object' AND jsonb_exists_any(extra_params, array['key', 'hmac_key', 'aes_key', 'authorization']))`).
		OrderExpr("id").
		For("UPDATE").
		Scan(ctx, &raw)
	if err != nil {
		return fmt.Errorf("read workflow action secrets: %w", err)
	}
	rows := make([]workflowActionSecretRow, 0, len(raw))
	for _, item := range raw {
		rows = append(rows, workflowActionSecretRowFromMap(item))
	}
	if len(rows) == 0 {
		return nil
	}
	appKey := workflowSecretMigrationAppKey()
	if appKey == "" {
		log.Warnf("base-workflow: %d action(s) with literal secrets left unchanged: app.encryptKey is not configured", len(rows))
		return nil
	}

	moved := 0
	for _, row := range rows {
		extra := map[string]any{}
		if row.ExtraParams.Valid && strings.TrimSpace(row.ExtraParams.String) != "" {
			if err := json.Unmarshal([]byte(row.ExtraParams.String), &extra); err != nil {
				extra = map[string]any{} // not an object: no extra_params secret to move
			}
		}
		secrets := workflowActionLiteralSecrets(row, extra)
		if len(secrets) == 0 {
			continue
		}
		update := tx.NewUpdate().TableExpr("?", bun.Ident(actionTable)).Where("id = ?", row.ID)
		extraChanged := false
		for _, secret := range secrets {
			if err := ensureMigratedWorkflowCredential(ctx, tx, credentialTable, appKey, row, secret); err != nil {
				return err
			}
			ref := workflowCredentialRefPrefix + secret.credentialName
			if secret.column != "" {
				update = update.Set("? = ?", bun.Ident(secret.column), ref)
			} else {
				extra[secret.extraKey] = ref
				extraChanged = true
			}
			moved++
		}
		if extraChanged {
			encoded, err := json.Marshal(extra)
			if err != nil {
				return fmt.Errorf("encode workflow action %d extra_params: %w", row.ID, err)
			}
			update = update.Set("extra_params = ?::jsonb", string(encoded))
		}
		if _, err := update.Exec(ctx); err != nil {
			return fmt.Errorf("rewrite workflow action %d secrets: %w", row.ID, err)
		}
	}
	if moved > 0 {
		log.Infof("base-workflow: moved %d literal action secret(s) into base.workflow.credential", moved)
	}
	return nil
}

// ensureMigratedWorkflowCredential inserts the sealed credential unless a row with that name
// already exists for the same workflow (a previous partial run is rolled back, so this only
// matters for a credential an operator created by hand with the generated name).
func ensureMigratedWorkflowCredential(ctx context.Context, tx bun.IDB, table, appKey string, row workflowActionSecretRow, secret workflowActionLiteralSecret) error {
	var existing int
	if err := tx.NewSelect().TableExpr("?", bun.Ident(table)).ColumnExpr("count(*)").
		Where("name = ?", secret.credentialName).
		Where("workflow_id IS NOT DISTINCT FROM ?", row.WorkflowID).
		Scan(ctx, &existing); err != nil {
		return fmt.Errorf("look up credential %s: %w", secret.credentialName, err)
	}
	if existing > 0 {
		return nil
	}
	sealed, err := secretbox.Seal(appKey, workflowCredentialSecretLabel, secret.value)
	if err != nil {
		return fmt.Errorf("seal credential %s: %w", secret.credentialName, err)
	}
	field := secret.column
	if field == "" {
		field = "extra_params." + secret.extraKey
	}
	now := workflowSecretMigrationNow()
	_, err = tx.NewRaw(
		"INSERT INTO ? (active, created_at, updated_at, workflow_id, company_id, name, credential_type, description, encrypted_value) VALUES (true, ?, ?, ?, ?, ?, ?, ?, ?)",
		bun.Ident(table), now, now, row.WorkflowID, row.CompanyID, secret.credentialName, secret.credentialType,
		fmt.Sprintf("Moved from workflow action %d %s (literal secrets are no longer stored on actions)", row.ID, field),
		sealed,
	).Exec(ctx)
	if err != nil {
		return fmt.Errorf("insert credential %s: %w", secret.credentialName, err)
	}
	return nil
}

// scrubOutboundAuthorizationHeaders removes any Authorization header (any casing) stored on
// outbound delivery rows.
func scrubOutboundAuthorizationHeaders(ctx context.Context, tx bun.IDB) error {
	table := OutboundDeliveryModelSpec.ResolveStorageName()
	var installed bool
	if err := tx.NewRaw("SELECT to_regclass(?) IS NOT NULL", table).Scan(ctx, &installed); err != nil {
		return fmt.Errorf("check outbound table: %w", err)
	}
	if !installed {
		return nil
	}
	result, err := tx.NewRaw(`UPDATE ? SET headers = (
			SELECT COALESCE(jsonb_object_agg(e.key, e.value), '{}'::jsonb)
			FROM jsonb_each(headers) AS e
			WHERE lower(e.key) <> 'authorization')
		WHERE EXISTS (
			SELECT 1 FROM jsonb_object_keys(CASE WHEN jsonb_typeof(headers) = 'object' THEN headers ELSE '{}'::jsonb END) AS k
			WHERE lower(k) = 'authorization')`, bun.Ident(table)).Exec(ctx)
	if err != nil {
		return fmt.Errorf("scrub outbound authorization headers: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected > 0 {
		log.Infof("base-workflow: removed stored Authorization headers from %d outbound delivery row(s)", affected)
	}
	return nil
}
