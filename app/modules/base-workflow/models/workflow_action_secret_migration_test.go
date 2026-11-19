package models

// Postgres-backed check of the T40 boot migrations. Skips unless RUNTIME_GRAPH_TEST_DB_PASSWORD
// is set (same env vars as the orm and secretbox integration tests). Tables are TEMP tables with
// the real storage names, created inside a rolled-back transaction; pg_temp is searched first, so
// they shadow any real table of the same name.

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/modules/base-credential/secretbox"
)

func TestWorkflowSecretMigrationsAgainstPostgres(t *testing.T) {
	password := os.Getenv("RUNTIME_GRAPH_TEST_DB_PASSWORD")
	if password == "" {
		t.Skip("RUNTIME_GRAPH_TEST_DB_PASSWORD not set: skipping Postgres-backed test")
	}
	env := func(key, fallback string) string {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
		return fallback
	}
	port, _ := strconv.Atoi(env("RUNTIME_GRAPH_TEST_DB_PORT", "5432"))
	db, err := orm.NewBunDB(config.PostgresqlConfig{
		Enable: true, Host: env("RUNTIME_GRAPH_TEST_DB_HOST", "localhost"), Port: port,
		User: env("RUNTIME_GRAPH_TEST_DB_USER", "thebase"), Password: password,
		Name: env("RUNTIME_GRAPH_TEST_DB_NAME", "base_hub_test"), SSLMode: env("RUNTIME_GRAPH_TEST_DB_SSLMODE", "disable"),
	})
	if err != nil {
		t.Skipf("integration database unavailable: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	const appKey = "t40-migration-test-key-0123456789"
	prevKey := workflowSecretMigrationAppKey
	workflowSecretMigrationAppKey = func() string { return appKey }
	t.Cleanup(func() { workflowSecretMigrationAppKey = prevKey })

	ctx := context.Background()
	tx, err := db.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	actionTable := WorkflowActionModelSpec.ResolveStorageName()
	credentialTable := WorkflowCredentialModelSpec.ResolveStorageName()
	outboundTable := OutboundDeliveryModelSpec.ResolveStorageName()
	for _, ddl := range []string{
		`CREATE TEMP TABLE ` + actionTable + ` (id bigserial PRIMARY KEY, workflow_id bigint, company_id bigint,
			hmac_key text, aes_key text, "authorization" text, extra_params jsonb)`,
		`CREATE TEMP TABLE ` + credentialTable + ` (id bigserial PRIMARY KEY, active boolean, created_at timestamptz,
			updated_at timestamptz, workflow_id bigint, company_id bigint, name text, credential_type text,
			description text, encrypted_value text)`,
		`CREATE TEMP TABLE ` + outboundTable + ` (id bigserial PRIMARY KEY, headers jsonb)`,
	} {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+actionTable+` (workflow_id, company_id, hmac_key, aes_key, "authorization", extra_params) VALUES
		(5, 2, 'plain-hmac', '@cred:kept', 'Bearer abc', '{"key":"plain-extra","key_field":"_k"}'),
		(5, 2, '', NULL, NULL, '"not an object"'),
		(6, NULL, '@syscred:vault', '', '', NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+outboundTable+` (headers) VALUES
		('{"Authorization":"Bearer abc","Content-Type":"application/json"}'), ('{"authorization":"x"}'), ('{"X":"1"}'), ('[]'), (NULL)`); err != nil {
		t.Fatal(err)
	}

	for run := 0; run < 2; run++ { // second run must change nothing
		if err := migrateWorkflowActionLiteralSecrets(ctx, tx); err != nil {
			t.Fatalf("run %d: %v", run, err)
		}
		if err := scrubOutboundAuthorizationHeaders(ctx, tx); err != nil {
			t.Fatalf("run %d scrub: %v", run, err)
		}
	}

	var hmacKey, aesKey, authorization, extraRaw string
	if err := tx.NewRaw(`SELECT hmac_key, aes_key, "authorization", extra_params::text FROM `+actionTable+` WHERE id = 1`).
		Scan(ctx, &hmacKey, &aesKey, &authorization, &extraRaw); err != nil {
		t.Fatal(err)
	}
	if hmacKey != "@cred:action_1_hmac_key" || aesKey != "@cred:kept" || authorization != "@cred:action_1_authorization" {
		t.Fatalf("action 1 = %q %q %q", hmacKey, aesKey, authorization)
	}
	var extra map[string]any
	_ = json.Unmarshal([]byte(extraRaw), &extra)
	if extra["key"] != "@cred:action_1_extra_key" || extra["key_field"] != "_k" {
		t.Fatalf("extra_params = %s", extraRaw)
	}

	var count int
	if err := tx.NewRaw(`SELECT count(*) FROM `+credentialTable).Scan(ctx, &count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("credentials = %d, want 3 (idempotent second run)", count)
	}
	var sealed string
	var workflowID, companyID int64
	if err := tx.NewRaw(`SELECT encrypted_value, workflow_id, company_id FROM `+credentialTable+` WHERE name = 'action_1_authorization'`).
		Scan(ctx, &sealed, &workflowID, &companyID); err != nil {
		t.Fatal(err)
	}
	if plain, err := secretbox.Open(appKey, workflowCredentialSecretLabel, sealed); err != nil || plain != "Bearer abc" || workflowID != 5 || companyID != 2 {
		t.Fatalf("credential = %q (%v), workflow %d company %d", plain, err, workflowID, companyID)
	}

	var withAuth int
	if err := tx.NewRaw(`SELECT count(*) FROM `+outboundTable+` WHERE headers::text ILIKE '%authorization%'`).Scan(ctx, &withAuth); err != nil {
		t.Fatal(err)
	}
	var contentType string
	if err := tx.NewRaw(`SELECT headers->>'Content-Type' FROM `+outboundTable+` WHERE id = 1`).Scan(ctx, &contentType); err != nil {
		t.Fatal(err)
	}
	if withAuth != 0 || contentType != "application/json" {
		t.Fatalf("outbound headers: %d rows keep authorization, content-type %q", withAuth, contentType)
	}
}
