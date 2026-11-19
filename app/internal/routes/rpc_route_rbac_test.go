package routes

// File overview:
// - Responsibility: verifies HTTP and RPC transport behavior for rpc route rbac.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down request parsing and auth/response contracts so frontend and integration clients keep seeing stable transport behavior.

import (
	"net/http"
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/uptrace/bun"
)

const rpcRBACModelName = "test.rpc_rbac_model"

type rpcRBACDocument struct {
	bun.BaseModel `bun:"table:test_rpc_rbac_model,alias:rpc_rbac_document"`
	ID            int64  `bun:"id,pk,autoincrement" json:"id" meta:"readonly,primary"`
	Name          string `bun:"name" json:"name"`
}

type rpcRBACRoleRow struct {
	bun.BaseModel
	ID          int64  `bun:"id,pk"`
	Name        string `bun:"name"`
	Description string `bun:"description"`
	Superadmin  bool   `bun:"superadmin"`
}

type rpcRBACUserRow struct {
	bun.BaseModel       `bun:"table:_base_users"`
	ID                  int64  `bun:"id,pk"`
	Login               string `bun:"login"`
	Email               string `bun:"email"`
	Name                string `bun:"name"`
	Password            string `bun:"password"`
	GroupID             int64  `bun:"group_id"`
	AllDevicesAvailable bool   `bun:"alldevicesavailable"`
	AllConfigAvailable  bool   `bun:"allconfigavailable"`
	PasswordReset       bool   `bun:"passwordreset"`
}

// setBearerAuth attaches an OAuth access-token Bearer credential, the
// RBAC-test replacement for the removed Authorization: Basic header on /rpc
// and the removed stored-authtoken bearer path (RBAC/OAuth backend plan
// §1/§6/§7.4) — the token itself now comes from loginAndGetAccessToken below.
func setBearerAuth(req *http.Request, token string) {
	req.Header.Set("Authorization", "Bearer "+token)
}

// loginAndGetAccessToken mints an OAuth access token via
// baseauth.CurrentProvider().IssuePassword directly, standing in for the
// removed stored-authtoken bearer fixtures. This calls the same password-grant
// path POST /oauth/token uses (modules/base-auth/routes) without a round trip
// through that HTTP route — the login endpoint itself now lives in a
// different package (moved off /web/session/authenticate, then /auth/token,
// consolidated onto /oauth/token), so these RBAC tests (which only care about
// /rpc enforcement) get their token directly instead of importing that
// package, which would create an import cycle (it imports this one for
// CurrentRPCIdentity/RequestRPCDBName).
func loginAndGetAccessToken(t *testing.T, db, login, password string) string {
	t.Helper()
	pair, err := baseauth.CurrentProvider().IssuePassword(t.Context(), db, login, password)
	if err != nil {
		t.Fatalf("issue password grant: %v", err)
	}
	if pair.AccessToken == "" {
		t.Fatalf("expected non-empty access_token from IssuePassword")
	}
	return pair.AccessToken
}

func TestRPCModelRoutesEnforceRBAC(t *testing.T) {
	setRPCTestConfig(t)

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()

	testutil.SwapBunDB(t, bunDB)

	// sys.model.log and sys.rule must be registered before migration: catalog
	// sync writes change logs into sys_model_log, and the record-rule check
	// reads sys_rule — on a fresh scratch database both tables have to be
	// created by this same migrate pass or the RPC flow fails before RBAC is
	// even evaluated (the old always-provisioned test DB masked this).
	models.SysModelLogModel.InitDefault()
	defer models.SysModelLogModel.Shutdown()
	models.SysRuleModel.InitDefault()
	defer models.SysRuleModel.Shutdown()
	models.ResGroupModel.InitDefault()
	defer models.ResGroupModel.Shutdown()
	models.ResUserModel.InitDefault()
	defer models.ResUserModel.Shutdown()
	models.SysModelAccessModel.InitDefault()
	defer models.SysModelAccessModel.Shutdown()

	runtest.RegisterModelType[rpcRBACDocument](t, orm.ModelSpec{
		Name:       rpcRBACModelName,
		Label:      "RPC RBAC Test Model",
		Definition: orm.ModelDefinition(rpcRBACModelName, (*rpcRBACDocument)(nil)),
	})
	orm.RegisterRuntimeExecutor(rpcRBACModelName, &runtest.ExecutorStub{
		ReadResult: []orm.RecordMap{{"id": int64(5), "name": "Visible"}},
		// The record-rule guard search-counts the requested ids through this
		// stub; it must report the one visible document or the read is blocked
		// before RBAC semantics are ever asserted.
		SearchCountResult: 1,
	})

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}
	ensureACLActiveColumnForRPCTest(t, db)

	roleID := int64(910001)
	userID := int64(910002)
	documentID := int64(5)

	if _, err := db.NewDelete().Model((*models.SysModelAccessRecord)(nil)).ModelTableExpr("sys_model_access").Where("group_id = ?", roleID).Exec(t.Context()); err != nil {
		t.Fatalf("cleanup acl rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACDocument)(nil)).ModelTableExpr("test_rpc_rbac_model").Where("id = ?", documentID).Exec(t.Context()); err != nil {
		t.Fatalf("cleanup document rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context()); err != nil {
		t.Fatalf("cleanup user rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACRoleRow)(nil)).ModelTableExpr("tbl_groups").Where("id = ?", roleID).Exec(t.Context()); err != nil {
		t.Fatalf("cleanup role rows: %v", err)
	}
	defer func() {
		_, _ = db.NewDelete().Model((*models.SysModelAccessRecord)(nil)).ModelTableExpr("sys_model_access").Where("group_id = ?", roleID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACDocument)(nil)).ModelTableExpr("test_rpc_rbac_model").Where("id = ?", documentID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACRoleRow)(nil)).ModelTableExpr("tbl_groups").Where("id = ?", roleID).Exec(t.Context())
	}()

	if _, err := db.NewInsert().Model(&rpcRBACRoleRow{
		ID:          roleID,
		Name:        "RPC Viewer",
		Description: "read-only rpc role",
		Superadmin:  false,
	}).ModelTableExpr("tbl_groups").Exec(t.Context()); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := db.NewInsert().Model(&rpcRBACUserRow{
		ID:                  userID,
		Login:               "rpc_rbac_viewer",
		Email:               "rpc_rbac_viewer@example.test",
		Name:                "RPC Viewer",
		Password:            "secret",
		GroupID:             roleID,
		AllDevicesAvailable: true,
		AllConfigAvailable:  true,
		PasswordReset:       false,
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.NewInsert().Model(&rpcRBACDocument{
		ID:   documentID,
		Name: "Visible",
	}).ModelTableExpr("test_rpc_rbac_model").Exec(t.Context()); err != nil {
		t.Fatalf("insert document: %v", err)
	}
	if _, err := db.NewInsert().Model(&models.SysModelAccessRecord{
		// The viewer grant must be active: ensureRBACModelAccess only matches
		// rows with active = true (the inactive-ACL case is covered by
		// TestRPCBasicAuthInactiveACLIsIgnored below).
		BaseModel:  orm.BaseModel{ID: 910004, Active: true},
		Name:       "viewer read access",
		ModelName:  rpcRBACModelName,
		GroupID:    &roleID,
		PermRead:   true,
		PermWrite:  false,
		PermCreate: false,
		PermUnlink: false,
	}).ModelTableExpr("sys_model_access").Exec(t.Context()); err != nil {
		t.Fatalf("insert acl: %v", err)
	}
	app := newRPCTestApp()

	// api_key/authtoken are gone; login now issues a real OAuth 2.0 token pair
	// (RBAC/OAuth backend plan §1/§7.4/§12.1 Option A), minted directly here
	// rather than via HTTP — see loginAndGetAccessToken's doc comment.
	accessToken := loginAndGetAccessToken(t, "bean_ipn", "rpc_rbac_viewer", "secret")

	readReq := testutil.NewJSONRequest(t, http.MethodPost, "/rpc/"+rpcRBACModelName+"/read/5", `{"args":[],"kwargs":{"fields":["id","name"]}}`)
	setBearerAuth(readReq, accessToken)
	readResp, err := app.Test(readReq)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected read status: %d body=%s", readResp.StatusCode, testutil.ReadBody(t, readResp))
	}

	createReq := testutil.NewJSONRequest(t, http.MethodPost, "/rpc/"+rpcRBACModelName+"/create", `{"args":[[],{"name":"Blocked"}],"kwargs":{}}`)
	setBearerAuth(createReq, accessToken)
	createResp, err := app.Test(createReq)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if createResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected create to be forbidden, got %d body=%s", createResp.StatusCode, testutil.ReadBody(t, createResp))
	}
}

func TestRPCBasicAuthInactiveACLIsIgnored(t *testing.T) {
	setRPCTestConfig(t)

	bunDB, err := orm.NewBunDB(testutil.EnvPostgresConfig())
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	defer bunDB.Close()

	testutil.SwapBunDB(t, bunDB)

	// sys.model.log and sys.rule must be registered before migration: catalog
	// sync writes change logs into sys_model_log, and the record-rule check
	// reads sys_rule — on a fresh scratch database both tables have to be
	// created by this same migrate pass or the RPC flow fails before RBAC is
	// even evaluated (the old always-provisioned test DB masked this).
	models.SysModelLogModel.InitDefault()
	defer models.SysModelLogModel.Shutdown()
	models.SysRuleModel.InitDefault()
	defer models.SysRuleModel.Shutdown()
	models.ResGroupModel.InitDefault()
	defer models.ResGroupModel.Shutdown()
	models.ResUserModel.InitDefault()
	defer models.ResUserModel.Shutdown()
	models.SysModelAccessModel.InitDefault()
	defer models.SysModelAccessModel.Shutdown()
	runtest.RegisterModelType[rpcRBACDocument](t, orm.ModelSpec{
		Name:       rpcRBACModelName,
		Label:      "RPC RBAC Test",
		Definition: orm.ModelDefinition(rpcRBACModelName, (*rpcRBACDocument)(nil)),
	})
	orm.RegisterRuntimeExecutor(rpcRBACModelName, &runtest.ExecutorStub{
		ReadResult: []orm.RecordMap{{"id": int64(5), "name": "Hidden"}},
	})

	db := bunDB.DB
	if err := orm.AutoMigrateRegisteredModelsToDB(t.Context(), db, orm.DefaultModelRegistry()); err != nil {
		t.Fatalf("auto-migrate registered models: %v", err)
	}
	ensureACLActiveColumnForRPCTest(t, db)

	roleID := int64(910102)
	userID := int64(910202)
	documentID := int64(5)
	if _, err := db.NewDelete().Model((*models.SysModelAccessRecord)(nil)).ModelTableExpr("sys_model_access").Where("group_id = ?", roleID).Exec(t.Context()); err != nil {
		t.Fatalf("delete old acl rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACDocument)(nil)).ModelTableExpr("test_rpc_rbac_model").Where("id = ?", documentID).Exec(t.Context()); err != nil {
		t.Fatalf("delete old document rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context()); err != nil {
		t.Fatalf("delete old user rows: %v", err)
	}
	if _, err := db.NewDelete().Model((*rpcRBACRoleRow)(nil)).ModelTableExpr("tbl_groups").Where("id = ?", roleID).Exec(t.Context()); err != nil {
		t.Fatalf("delete old role rows: %v", err)
	}
	defer func() {
		_, _ = db.NewDelete().Model((*models.SysModelAccessRecord)(nil)).ModelTableExpr("sys_model_access").Where("group_id = ?", roleID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACDocument)(nil)).ModelTableExpr("test_rpc_rbac_model").Where("id = ?", documentID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACUserRow)(nil)).ModelTableExpr("base_users").Where("id = ?", userID).Exec(t.Context())
		_, _ = db.NewDelete().Model((*rpcRBACRoleRow)(nil)).ModelTableExpr("tbl_groups").Where("id = ?", roleID).Exec(t.Context())
	}()

	if _, err := db.NewInsert().Model(&rpcRBACRoleRow{
		ID:          roleID,
		Name:        "RPC Disabled ACL",
		Description: "Role with inactive ACL",
		Superadmin:  false,
	}).ModelTableExpr("tbl_groups").Exec(t.Context()); err != nil {
		t.Fatalf("insert role: %v", err)
	}
	if _, err := db.NewInsert().Model(&rpcRBACUserRow{
		ID:                  userID,
		Login:               "rpc_rbac_disabled",
		Email:               "rpc_rbac_disabled@example.test",
		Name:                "RPC Disabled ACL",
		Password:            "secret",
		GroupID:             roleID,
		AllDevicesAvailable: true,
		AllConfigAvailable:  true,
		PasswordReset:       false,
	}).ModelTableExpr("base_users").Exec(t.Context()); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := db.NewInsert().Model(&rpcRBACDocument{
		ID:   documentID,
		Name: "Hidden",
	}).ModelTableExpr("test_rpc_rbac_model").Exec(t.Context()); err != nil {
		t.Fatalf("insert document: %v", err)
	}
	if _, err := db.NewInsert().Model(&models.SysModelAccessRecord{
		BaseModel:  orm.BaseModel{ID: 910304, Active: false},
		Name:       "inactive read access",
		ModelName:  rpcRBACModelName,
		GroupID:    &roleID,
		PermRead:   true,
		PermWrite:  false,
		PermCreate: false,
		PermUnlink: false,
	}).ModelTableExpr("sys_model_access").Exec(t.Context()); err != nil {
		t.Fatalf("insert acl: %v", err)
	}

	app := newRPCTestApp()
	accessToken := loginAndGetAccessToken(t, "bean_ipn", "rpc_rbac_disabled", "secret")
	// A denied read-only method now degrades to its natural empty result (200
	// + zero value) instead of 403 — see zeroReadResultForMethod in
	// rpc_route_handlers.go — so the caller with an inactive ACL sees "no
	// records" rather than a permission error, and the hidden document never
	// appears in the response.
	readReq := testutil.NewJSONRequest(t, http.MethodPost, "/rpc/"+rpcRBACModelName+"/read/5", `{"args":[],"kwargs":{"fields":["id","name"]}}`)
	setBearerAuth(readReq, accessToken)
	readResp, err := app.Test(readReq)
	if err != nil {
		t.Fatalf("read request: %v", err)
	}
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("expected read with inactive ACL to degrade to 200, got %d body=%s", readResp.StatusCode, testutil.ReadBody(t, readResp))
	}
	payload := testutil.DecodeJSON[map[string]any](t, readResp.Body)
	records, ok := payload["result"].([]any)
	if !ok {
		t.Fatalf("result type = %T, want []any", payload["result"])
	}
	if len(records) != 0 {
		t.Fatalf("expected no records for inactive ACL, got %#v", records)
	}
}

func ensureACLActiveColumnForRPCTest(t *testing.T, db *bun.DB) {
	t.Helper()
	if _, err := db.ExecContext(t.Context(), `ALTER TABLE sys_model_access ADD COLUMN IF NOT EXISTS active boolean DEFAULT true`); err != nil {
		t.Fatalf("ensure sys_model_access.active column: %v", err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE sys_model_access SET active = true WHERE active IS NULL`); err != nil {
		t.Fatalf("backfill sys_model_access.active column: %v", err)
	}
}
