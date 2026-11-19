//go:build conformance

package conformance

// File overview:
// - Responsibility: shared fixture bootstrap for the runtime conformance suite
//   (SRS §4.6 CONF-01..03). Owns database connection, kernel-model bootstrap,
//   conformance model registration, and the HTTP app the tests drive.
// - Pattern: mirrors internal/routes RPC tests — env-var DSN with skip-on-
//   unreachable, real runtime executors (no stubs), and per-test cleanup so
//   the suite can rerun against the same database without manual resets.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/routes"
	testutil "git.thebase.vn/base_hub/internal/test"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/uptrace/bun"
)

// confOAuthClientID/confOAuthClientSecret register one sys.oauth_client row
// per suite so the "sudo bypass" matrix rows have a real client_credentials
// caller to authenticate as — the direct successor to the removed static
// X-API-Key service credential (RBAC/OAuth backend plan §1/§3/§7.3): each
// automation caller gets its own revocable secret instead of one shared key.
const (
	confOAuthClientID     = "conformance-suite-client"
	confOAuthClientSecret = "conformance-suite-client-secret"
)

// Conformance models cover one registered model kind each (CONF-01):
// plain (no BaseModel), archive-aware (orm.BaseModel), company-scoped
// (company_id field), and metadata (orm.MetadataBaseModel).

// confPartnerRecord is the plain kind and the many2one target for tickets.
// It declares its own columns instead of embedding a shared fragment; the
// timestamps stay because the generic write path stamps updated_at on every
// registered model regardless of kind.
type confPartnerRecord struct {
	bun.BaseModel `bun:"table:conf_partner"`
	ID            int64     `bun:"id,pk,autoincrement" json:"id" meta:"readonly,primary"`
	Name          string    `bun:"name" json:"name" meta:"label:Name"`
	TicketIDs     []int64   `bun:"ticket_ids" json:"ticket_ids" meta:"kind:one2many,model:conf.ticket,inverse:partner_id,stored:false,label:Tickets"`
	CreatedAt     time.Time `bun:"created_at,nullzero" json:"createdAt" meta:"readonly"`
	UpdatedAt     time.Time `bun:"updated_at,nullzero" json:"updatedAt" meta:"readonly"`
}

// confTicketRecord is the archive-aware kind; it carries the serialization
// contract surface (many2one tuple, boolean false, empty relation false).
type confTicketRecord struct {
	orm.BaseModel
	Name      string `bun:"name" json:"name" meta:"label:Name"`
	Note      string `bun:"note" json:"note" meta:"label:Note"`
	Urgent    bool   `bun:"urgent" json:"urgent" meta:"label:Urgent"`
	PartnerID *int64 `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:conf.partner,ondelete:SET NULL,index,label:Partner"`
}

// confScopedRecord is the company-scoped kind (implicit company_id filtering).
type confScopedRecord struct {
	orm.BaseModel
	Name      string `bun:"name" json:"name" meta:"label:Name"`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company"`
}

// confMetaRecord is the metadata-catalog kind (orm.MetadataBaseModel).
type confMetaRecord struct {
	orm.MetadataBaseModel
	Name string `bun:"name" json:"name" meta:"label:Name"`
}

// confDocRecord is the CONF-02 access-matrix target: company-scoped plus
// ACL/record-rule fixtures attach to it.
type confDocRecord struct {
	orm.BaseModel
	Name      string `bun:"name" json:"name" meta:"label:Name"`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company"`
}

const (
	confPartnerModelName = "conf.partner"
	confTicketModelName  = "conf.ticket"
	confScopedModelName  = "conf.scoped"
	confMetaModelName    = "conf.meta"
	confDocModelName     = "conf.doc"
)

var (
	confPartnerModel = orm.NewHandlerWithModel[confPartnerRecord](orm.ModelSpec{
		Name:       confPartnerModelName,
		Label:      "Conformance Partner",
		Definition: orm.ModelDefinition(confPartnerModelName, (*confPartnerRecord)(nil)),
	})
	confTicketModel = orm.NewHandlerWithModel[confTicketRecord](orm.ModelSpec{
		Name:       confTicketModelName,
		Label:      "Conformance Ticket",
		Definition: orm.ModelDefinition(confTicketModelName, (*confTicketRecord)(nil)),
	})
	confScopedModel = orm.NewHandlerWithModel[confScopedRecord](orm.ModelSpec{
		Name:       confScopedModelName,
		Label:      "Conformance Scoped",
		Definition: orm.ModelDefinition(confScopedModelName, (*confScopedRecord)(nil)),
	})
	confMetaModel = orm.NewHandlerWithModel[confMetaRecord](orm.ModelSpec{
		Name:       confMetaModelName,
		Label:      "Conformance Meta",
		Definition: orm.ModelDefinition(confMetaModelName, (*confMetaRecord)(nil)),
	})
	confDocModel = orm.NewHandlerWithModel[confDocRecord](orm.ModelSpec{
		Name:       confDocModelName,
		Label:      "Conformance Doc",
		Definition: orm.ModelDefinition(confDocModelName, (*confDocRecord)(nil)),
	})
)

type confSuite struct {
	app    *fiber.App
	db     *bun.DB
	dbName string
	// sudoToken is a client_credentials-issued OAuth bearer bound to a
	// superadmin sys.oauth_client row, the direct successor to the removed
	// X-API-Key sudo-bypass credential (plan §1/§3/§7.3). authAPIKey() uses it.
	sudoToken string
}

// newConfSuite provisions the shared conformance environment: PostgreSQL from
// the env-var DSN (skips when unreachable so unit CI stays green), the kernel
// identity/ACL/rule models with their real executors, the conf.* models, and
// a Fiber app carrying the production RPC routes.
func newConfSuite(t *testing.T) *confSuite {
	t.Helper()

	cfg := testutil.EnvPostgresConfig()
	testutil.RestoreConfig(t, func(app *config.AppConfig) {
		app.PostgresqlDb = cfg
	})

	bunDB, err := orm.NewBunDB(cfg)
	if err != nil {
		// Locally an unreachable database skips the suite so unit-test runs
		// stay green; in CI the skip would silently pass a broken pipeline, so
		// CONF_REQUIRE_DB=1 turns it into a hard failure (CONF-04).
		if os.Getenv("CONF_REQUIRE_DB") == "1" {
			t.Fatalf("CONF_REQUIRE_DB=1 but postgres fixture is unreachable: %v", err)
		}
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = bunDB.Close() })
	testutil.SwapBunDB(t, bunDB)
	// Register the pool under its logical name too: the RPC layer rebuilds the
	// ORM environment from the resolved DB name, and record-rule/count checks
	// need that name to resolve back to a live handle.
	orm.SetNamedBunDB(cfg.Name, bunDB)
	orm.SetDefaultBunDBName(cfg.Name)
	t.Cleanup(func() { orm.SetNamedBunDB(cfg.Name, nil) })

	// Kernel models needed for auth (users/groups), ACLs, record rules, and
	// the company scope; init/shutdown stay symmetric per test so reruns and
	// other suites never see stale registry entries.
	for _, boot := range []struct {
		init     func()
		shutdown func()
	}{
		{func() { models.ResGroupModel.InitDefault() }, models.ResGroupModel.Shutdown},
		{func() { models.ResUserModel.InitDefault() }, models.ResUserModel.Shutdown},
		{func() { models.ResCompanyModel.InitDefault() }, models.ResCompanyModel.Shutdown},
		{func() { models.SysModelAccessModel.InitDefault() }, models.SysModelAccessModel.Shutdown},
		{func() { models.SysRuleModel.InitDefault() }, models.SysRuleModel.Shutdown},
		{func() { authmodels.SysOauthRefreshTokenModel.InitDefault() }, authmodels.SysOauthRefreshTokenModel.Shutdown},
		{func() { authmodels.SysOauthClientModel.InitDefault() }, authmodels.SysOauthClientModel.Shutdown},
		{func() { confPartnerModel.InitDefault() }, confPartnerModel.Shutdown},
		{func() { confTicketModel.InitDefault() }, confTicketModel.Shutdown},
		{func() { confScopedModel.InitDefault() }, confScopedModel.Shutdown},
		{func() { confMetaModel.InitDefault() }, confMetaModel.Shutdown},
		{func() { confDocModel.InitDefault() }, confDocModel.Shutdown},
	} {
		boot.init()
		t.Cleanup(boot.shutdown)
	}

	// Drop conformance tables first so schema drift in the test models never
	// leaves stale columns behind; auto-migrate recreates them fresh.
	for _, table := range []string{"conf_partner", "conf_ticket", "conf_scoped", "conf_meta", "conf_doc"} {
		if _, err := bunDB.DB.ExecContext(t.Context(), "DROP TABLE IF EXISTS "+table); err != nil {
			t.Fatalf("drop conformance table %s: %v", table, err)
		}
	}
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), bunDB.DB, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}
	// Legacy databases may miss the ACL active column the RBAC query filters
	// on; mirror the guard used by internal/routes RPC tests.
	if _, err := bunDB.DB.ExecContext(t.Context(), `ALTER TABLE sys_model_access ADD COLUMN IF NOT EXISTS active boolean DEFAULT true`); err != nil {
		t.Fatalf("ensure sys_model_access.active column: %v", err)
	}

	app := fiber.New(fiber.Config{JSONEncoder: base.JSONMarshal})
	routes.RegisterRPCRoutes(app)

	sudoToken := issueConfSudoBearer(t, bunDB.DB, strings.TrimSpace(cfg.Name))
	// authAPIKey() is a free function (call sites in conf01/conf03 predate the
	// suite-scoped token), so stash it package-wide; suites never run
	// concurrently in this package (no t.Parallel usage here).
	currentSudoBearer = sudoToken

	return &confSuite{app: app, db: bunDB.DB, dbName: strings.TrimSpace(cfg.Name), sudoToken: sudoToken}
}

// issueConfSudoBearer registers one sys.oauth_client row and mints a
// client_credentials access token bound to it, the direct successor to the
// removed static X-API-Key sudo-bypass credential (RBAC/OAuth backend plan
// §1/§3/§7.3): the "sudo bypass" CONF-02 matrix rows exercise the same
// full-RBAC-bypass trust level, just via a revocable per-caller credential.
func issueConfSudoBearer(t *testing.T, db *bun.DB, dbName string) string {
	t.Helper()
	hash, err := authmodels.HashClientSecret(confOAuthClientSecret)
	if err != nil {
		t.Fatalf("hash conformance sudo client secret: %v", err)
	}
	if _, err := db.NewDelete().ModelTableExpr("sys_oauth_client").Where("client_id = ?", confOAuthClientID).Exec(t.Context()); err != nil {
		t.Fatalf("cleanup conformance sudo client: %v", err)
	}
	if _, err := db.NewInsert().
		Model(&struct {
			bun.BaseModel    `bun:"table:sys_oauth_client"`
			ID               int64  `bun:"id,pk,autoincrement"`
			ClientID         string `bun:"client_id"`
			ClientSecretHash string `bun:"client_secret_hash"`
			ServiceName      string `bun:"service_name"`
			IsSuperadmin     bool   `bun:"is_superadmin"`
			Active           bool   `bun:"active"`
		}{
			ClientID: confOAuthClientID, ClientSecretHash: hash,
			ServiceName: "conformance suite", IsSuperadmin: true, Active: true,
		}).
		Exec(t.Context()); err != nil {
		t.Fatalf("insert conformance sudo client: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.NewDelete().ModelTableExpr("sys_oauth_client").Where("client_id = ?", confOAuthClientID).Exec(context.Background())
	})
	pair, err := baseauth.CurrentProvider().IssueClientCredentials(t.Context(), dbName, confOAuthClientID, confOAuthClientSecret)
	if err != nil {
		t.Fatalf("issue conformance sudo bearer: %v", err)
	}
	return pair.AccessToken
}

// ---- HTTP helpers ----

type rpcAuth func(*http.Request)

func authNone() rpcAuth { return func(*http.Request) {} }

func authBearer(token string) rpcAuth {
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+token) }
}

// currentSudoBearer holds the active suite's sudo OAuth token for authAPIKey()
// (kept as a package-level slot, see issueConfSudoBearer's doc comment).
var currentSudoBearer string

// authAPIKey exercises the sudo/superadmin bypass row in the CONF-02 access
// matrix. Despite the name (kept for zero call-site churn across
// conf01/conf03), it no longer sends an X-API-Key header — that credential
// was fully removed (RBAC/OAuth backend plan §1/§7.4). It now sends the
// OAuth bearer minted for the suite's sys.oauth_client row.
func authAPIKey() rpcAuth {
	return func(req *http.Request) { req.Header.Set("Authorization", "Bearer "+currentSudoBearer) }
}

// issueConfSudoBearerForDB mints a client_credentials token for the suite's
// already-registered sys.oauth_client row, scoped to a different logical DB
// name than the one newConfSuite used — needed by CONF-03's multi-DB-header
// tests, since an access token is only valid for the DB it was issued
// against (plan §12.5); the row itself lives in the same physical Postgres
// pool the alternate name resolves to, so no new client registration is
// required, just a fresh grant with a different db form value.
func issueConfSudoBearerForDB(t *testing.T, dbName string) string {
	t.Helper()
	pair, err := baseauth.CurrentProvider().IssueClientCredentials(t.Context(), dbName, confOAuthClientID, confOAuthClientSecret)
	if err != nil {
		t.Fatalf("issue sudo bearer for db %q: %v", dbName, err)
	}
	return pair.AccessToken
}

// rpcCall posts an RPC body and returns status plus decoded JSON payload.
func (s *confSuite) rpcCall(t *testing.T, path string, kwargs map[string]any, auth rpcAuth, headers map[string]string) (int, map[string]any) {
	t.Helper()

	body, err := json.Marshal(map[string]any{"args": []any{}, "kwargs": kwargs})
	if err != nil {
		t.Fatalf("marshal rpc body: %v", err)
	}
	req := testutil.NewJSONRequest(t, http.MethodPost, path, string(body))
	auth(req)
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	resp, err := s.app.Test(req, fiber.TestConfig{Timeout: 0, FailOnTimeout: false})
	if err != nil {
		t.Fatalf("rpc %s: %v", path, err)
	}
	defer resp.Body.Close()

	raw := testutil.ReadBody(t, resp)
	payload := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		// Non-JSON bodies (fiber plain-text errors) still matter in failure
		// messages, so surface them under a reserved key.
		return resp.StatusCode, map[string]any{"_raw": raw}
	}
	return resp.StatusCode, payload
}

// rpcResult asserts a 200 response and returns the `result` value.
func (s *confSuite) rpcResult(t *testing.T, path string, kwargs map[string]any, auth rpcAuth) any {
	t.Helper()
	status, payload := s.rpcCall(t, path, kwargs, auth, nil)
	if status != http.StatusOK {
		t.Fatalf("rpc %s: status = %d, payload = %v", path, status, payload)
	}
	return payload["result"]
}

// ---- fixture helpers ----

// mustExec runs fixture SQL and fails the test on error.
func (s *confSuite) mustExec(t *testing.T, query string, args ...any) {
	t.Helper()
	if _, err := s.db.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatalf("fixture sql %q: %v", query, err)
	}
}

// asMap normalizes a decoded RPC record for assertions.
func asMap(t *testing.T, value any) map[string]any {
	t.Helper()
	record, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("expected record map, got %T (%v)", value, value)
	}
	return record
}

// asList normalizes a decoded RPC list result for assertions.
func asList(t *testing.T, value any) []any {
	t.Helper()
	list, ok := value.([]any)
	if !ok {
		t.Fatalf("expected list, got %T (%v)", value, value)
	}
	return list
}

// recordID extracts the numeric id a create call returned.
func recordID(t *testing.T, record map[string]any) int64 {
	t.Helper()
	raw, ok := record["id"]
	if !ok {
		t.Fatalf("record has no id: %v", record)
	}
	switch typed := raw.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			t.Fatalf("record id %v: %v", raw, err)
		}
		return parsed
	default:
		t.Fatalf("unexpected id type %T (%v)", raw, raw)
		return 0
	}
}

// requireTuple asserts the many2one serialization contract: [id, name].
func requireTuple(t *testing.T, value any, wantID int64, context string) {
	t.Helper()
	tuple, ok := value.([]any)
	if !ok || len(tuple) != 2 {
		t.Fatalf("%s: expected [id, name] tuple, got %T (%v)", context, value, value)
	}
	if got := int64(tuple[0].(float64)); got != wantID {
		t.Fatalf("%s: tuple id = %d, want %d", context, got, wantID)
	}
	if _, ok := tuple[1].(string); !ok {
		t.Fatalf("%s: tuple name = %T (%v), want string", context, tuple[1], tuple[1])
	}
}

func rpcPath(model, method string, id ...int64) string {
	if len(id) > 0 {
		return fmt.Sprintf("/rpc/%s/%s/%d", model, method, id[0])
	}
	return fmt.Sprintf("/rpc/%s/%s", model, method)
}
