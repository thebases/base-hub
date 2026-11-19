//go:build conformance

package conformance

// File overview:
// - Responsibility: CONF-02 — the composed access matrix: model ACL
//   (sys.model.access) × record rule (sys.rule, SRS-F-014) × implicit company
//   scope × auth mode (basic / bearer / api-key / anonymous), including the
//   sudo bypass and the shared-row (company_id = NULL) mutation rule.
// - Pattern: one fixture set, one table of matrix rows; each row states who
//   calls what and whether the composed policy must allow or deny it.

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
)

// Fixture identifiers sit in a reserved high range so they never collide with
// seeded data on shared development databases.
const (
	confViewerGroupID  = int64(920101)
	confEditorGroupID  = int64(920102)
	confViewerUserID   = int64(920111)
	confEditorUserID   = int64(920112)
	confMainEditorID   = int64(920113)
	confSecondCompany  = int64(920120)
	confViewerGroupXML = "conf.group_viewer"
	confEditorGroupXML = "conf.group_editor"
	confViewerLogin    = "conf_viewer"
	confEditorLogin    = "conf_editor"
	confMainEditor     = "conf_editor_main"
	confPassword       = "conf-secret"
)

// confBearerToken/confEditorBearerToken/confMainEditorBearerToken hold OAuth
// access tokens minted per-run by installConf02Fixture (real password-grant
// login, not a stored column value) — the removed authtoken/api_key columns
// no longer exist (RBAC/OAuth backend plan §1/§7.4). Package-level vars, not
// consts, since the value is only known once the fixture's users are
// inserted; safe because this package runs its tests sequentially.
var (
	confBearerToken           string
	confEditorBearerToken     string
	confMainEditorBearerToken string
)

type conf02Fixture struct {
	suite     *confSuite
	docC1     int64 // company 1 (main) document
	docC2     int64 // second-company document
	docShared int64 // shared row: company_id NULL
	docSecret int64 // company 1 document hidden from viewers by record rule
}

// installConf02Fixture seeds groups, users, ACLs, one record rule, and four
// documents. Cleanup deletes in reverse dependency order via t.Cleanup.
func installConf02Fixture(t *testing.T, suite *confSuite) *conf02Fixture {
	t.Helper()
	ctx := t.Context()

	cleanup := func(ctx context.Context) {
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM conf_doc WHERE name LIKE 'conf02 %'`)
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM sys_rule WHERE model_name = ?`, confDocModelName)
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM sys_model_access WHERE model_name = ?`, confDocModelName)
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM base_users WHERE id IN (?, ?, ?)`, confViewerUserID, confEditorUserID, confMainEditorID)
		_, _ = suite.db.ExecContext(ctx, `DELETE FROM tbl_groups WHERE id IN (?, ?)`, confViewerGroupID, confEditorGroupID)
	}
	cleanup(ctx)
	t.Cleanup(func() { cleanup(context.Background()) })

	// Two groups; the viewer group carries the bid the record rule targets.
	for _, group := range []models.ResGroupRecord{
		{BaseModel: orm.BaseModel{ID: confViewerGroupID, Active: true}, BID: confViewerGroupXML, Name: "Conf Viewer"},
		{BaseModel: orm.BaseModel{ID: confEditorGroupID, Active: true}, BID: confEditorGroupXML, Name: "Conf Editor"},
	} {
		group := group
		if _, err := suite.db.NewInsert().Model(&group).ModelTableExpr("tbl_groups").Exec(ctx); err != nil {
			t.Fatalf("insert group %s: %v", group.BID, err)
		}
	}

	// Three users across two companies. /rpc no longer accepts a raw
	// Authorization: Basic header, nor a stored authtoken/api_key column
	// (RBAC/OAuth backend plan §1/§6/§7.4) — every ACL-matrix row below
	// authenticates via a real OAuth access token minted just below instead.
	// SEC-02's legacy-password-rehash path is covered directly by
	// internal/orm/runtime_rbac_test.go, not by this fixture.
	companyMain := int64(1)
	companySecond := confSecondCompany
	for _, user := range []models.ResUserRecord{
		{
			BaseModel: orm.BaseModel{ID: confViewerUserID, Active: true},
			Login:     confViewerLogin, Email: "conf_viewer@example.test", Name: "Conf Viewer",
			Password: confPassword, GroupID: confViewerGroupID,
			CompanyID: &companyMain, CompanyIDs: []int64{companyMain},
		},
		{
			BaseModel: orm.BaseModel{ID: confEditorUserID, Active: true},
			Login:     confEditorLogin, Email: "conf_editor@example.test", Name: "Conf Editor",
			Password: confPassword, GroupID: confEditorGroupID,
			CompanyID: &companySecond, CompanyIDs: []int64{companySecond},
		},
		{
			BaseModel: orm.BaseModel{ID: confMainEditorID, Active: true},
			Login:     confMainEditor, Email: "conf_editor_main@example.test", Name: "Conf Editor Main",
			Password: confPassword, GroupID: confEditorGroupID,
			CompanyID: &companyMain, CompanyIDs: []int64{companyMain},
		},
	} {
		user := user
		if _, err := suite.db.NewInsert().Model(&user).ModelTableExpr("base_users").Exec(ctx); err != nil {
			t.Fatalf("insert user %s: %v", user.Login, err)
		}
	}

	// Mint one real OAuth access token per user via the password grant
	// (same provider POST /oauth/token uses) — direct successor to the
	// removed stored-authtoken bearer fixtures.
	var err error
	confBearerToken, err = issueConfPasswordBearer(t, suite, confViewerLogin, confPassword)
	if err != nil {
		t.Fatalf("issue viewer bearer: %v", err)
	}
	confEditorBearerToken, err = issueConfPasswordBearer(t, suite, confEditorLogin, confPassword)
	if err != nil {
		t.Fatalf("issue editor bearer: %v", err)
	}
	confMainEditorBearerToken, err = issueConfPasswordBearer(t, suite, confMainEditor, confPassword)
	if err != nil {
		t.Fatalf("issue main-editor bearer: %v", err)
	}

	// ACL matrix: viewers read-only, editors full CRUD on conf.doc.
	viewerGroup := confViewerGroupID
	editorGroup := confEditorGroupID
	for _, acl := range []models.SysModelAccessRecord{
		{
			BaseModel: orm.BaseModel{ID: 920131, Active: true},
			Name:      "conf02 viewer read", ModelName: confDocModelName, GroupID: &viewerGroup,
			PermRead: true,
		},
		{
			BaseModel: orm.BaseModel{ID: 920132, Active: true},
			Name:      "conf02 editor all", ModelName: confDocModelName, GroupID: &editorGroup,
			PermRead: true, PermWrite: true, PermCreate: true, PermUnlink: true,
		},
	} {
		acl := acl
		if _, err := suite.db.NewInsert().Model(&acl).ModelTableExpr("sys_model_access").Exec(ctx); err != nil {
			t.Fatalf("insert acl %s: %v", acl.Name, err)
		}
	}

	// One record rule scoped to the viewer group: classified documents stay
	// invisible on read no matter what the ACL says.
	rule := models.SysRuleRecord{
		BaseModel:   orm.BaseModel{ID: 920141, Active: true},
		BID:         "conf.rule_viewer_unclassified",
		Name:        "conf02 viewers see unclassified only",
		ModelName:   confDocModelName,
		DomainForce: `[["name", "!=", "conf02 classified"]]`,
		PermRead:    true,
		GroupBIDs:   []string{confViewerGroupXML},
	}
	if _, err := suite.db.NewInsert().Model(&rule).ModelTableExpr("sys_rule").Exec(ctx); err != nil {
		t.Fatalf("insert record rule: %v", err)
	}

	// Documents: one per company, one shared (NULL), one classified.
	fixture := &conf02Fixture{suite: suite}
	for _, doc := range []struct {
		target  *int64
		name    string
		company *int64
	}{
		{&fixture.docC1, "conf02 main-company doc", &companyMain},
		{&fixture.docC2, "conf02 second-company doc", &companySecond},
		{&fixture.docShared, "conf02 shared doc", nil},
		{&fixture.docSecret, "conf02 classified", &companyMain},
	} {
		row := confDocRecord{BaseModel: orm.BaseModel{Active: true}, Name: doc.name, CompanyID: doc.company}
		if _, err := suite.db.NewInsert().Model(&row).ModelTableExpr("conf_doc").Returning("id").Exec(ctx); err != nil {
			t.Fatalf("insert doc %q: %v", doc.name, err)
		}
		*doc.target = row.ID
	}
	return fixture
}

// TestConf02AccessMatrix drives the composed policy over HTTP for every
// auth-mode × operation combination the SRS calls out.
func TestConf02AccessMatrix(t *testing.T) {
	suite := newConfSuite(t)
	fixture := installConf02Fixture(t, suite)

	readKwargs := map[string]any{"fields": []string{"id", "name"}}
	writeKwargs := map[string]any{"values": map[string]any{"name": "conf02 shared doc"}}

	matrix := []struct {
		name       string
		method     string
		docID      int64
		kwargs     map[string]any
		auth       rpcAuth
		wantAllow  bool
		wantStatus int // 0 = only assert allow/deny
	}{
		// ---- ACL layer ----
		{name: "viewer basic read own-company doc", method: "read", docID: 0, auth: authBearer(confBearerToken), wantAllow: true},
		{name: "viewer basic create denied by ACL", method: "create", auth: authBearer(confBearerToken), wantAllow: false, wantStatus: http.StatusForbidden,
			kwargs: map[string]any{"values": map[string]any{"name": "conf02 blocked create"}}},
		{name: "editor basic create allowed by ACL", method: "create", auth: authBearer(confEditorBearerToken), wantAllow: true,
			kwargs: map[string]any{"values": map[string]any{"name": "conf02 editor-created doc"}}},

		// ---- record rule layer (viewer group only) ----
		{name: "viewer blocked from classified doc by record rule", method: "read", docID: -4, auth: authBearer(confBearerToken), wantAllow: false},
		{name: "editor unaffected by viewer-group rule but company-scoped", method: "read", docID: -4, auth: authBearer(confEditorBearerToken), wantAllow: false},
		{name: "main-company editor reads classified doc", method: "read", docID: -4, auth: authBearer(confMainEditorBearerToken), wantAllow: true},

		// ---- company scope layer ----
		{name: "second-company editor cannot read main-company doc", method: "read", docID: -1, auth: authBearer(confEditorBearerToken), wantAllow: false},
		{name: "second-company editor reads own doc", method: "read", docID: -2, auth: authBearer(confEditorBearerToken), wantAllow: true},

		// ---- shared-row (company_id = NULL) mutation rule ----
		{name: "any company reads shared row", method: "read", docID: -3, auth: authBearer(confEditorBearerToken), wantAllow: true},
		{name: "second-company editor cannot write shared row", method: "write", docID: -3, kwargs: writeKwargs, auth: authBearer(confEditorBearerToken), wantAllow: false},
		{name: "main-company editor writes shared row", method: "write", docID: -3, kwargs: writeKwargs, auth: authBearer(confMainEditorBearerToken), wantAllow: true},

		// ---- auth modes ----
		{name: "bearer token authenticates as its user", method: "read", docID: -1, auth: authBearer(confBearerToken), wantAllow: true},
		{name: "anonymous is rejected", method: "read", docID: -1, auth: authNone(), wantAllow: false, wantStatus: http.StatusUnauthorized},

		// ---- sudo bypass (service API key) ----
		{name: "api key bypasses record rule", method: "read", docID: -4, auth: authAPIKey(), wantAllow: true},
		{name: "api key bypasses shared-row mutation rule", method: "write", docID: -3, kwargs: writeKwargs, auth: authAPIKey(), wantAllow: true},
	}

	resolveDoc := func(id int64) int64 {
		switch id {
		case -1:
			return fixture.docC1
		case -2:
			return fixture.docC2
		case -3:
			return fixture.docShared
		case -4:
			return fixture.docSecret
		default:
			return id
		}
	}

	for _, row := range matrix {
		row := row
		t.Run(row.name, func(t *testing.T) {
			kwargs := row.kwargs
			if kwargs == nil {
				kwargs = readKwargs
			}
			path := rpcPath(confDocModelName, row.method)
			if row.method != "create" {
				docID := resolveDoc(row.docID)
				if docID == 0 {
					docID = fixture.docC1
				}
				path = rpcPath(confDocModelName, row.method, docID)
			}
			status, payload := suite.rpcCall(t, path, kwargs, row.auth, nil)
			allowed := status == http.StatusOK
			if allowed != row.wantAllow {
				t.Fatalf("%s %s: status = %d (payload %v), want allow=%v", row.method, path, status, payload, row.wantAllow)
			}
			if row.wantStatus != 0 && status != row.wantStatus {
				t.Fatalf("%s %s: status = %d, want %d", row.method, path, status, row.wantStatus)
			}
			if !row.wantAllow && status == http.StatusBadRequest {
				// Denials surfaced from the runtime carry the permission-denied
				// reason; anything else is a fixture bug, not a policy denial.
				if raw, _ := payload["_raw"].(string); raw != "" && !strings.Contains(raw, "permission denied") {
					t.Fatalf("%s %s: expected permission denial, got %q", row.method, path, raw)
				}
			}
		})
	}
}

// TestConf02ViewerSearchHidesClassified asserts the record rule also filters
// list queries, not just direct id access.
func TestConf02ViewerSearchHidesClassified(t *testing.T) {
	suite := newConfSuite(t)
	fixture := installConf02Fixture(t, suite)

	rows := asList(t, suite.rpcResult(t, rpcPath(confDocModelName, "search_read"), map[string]any{
		"domain": []any{[]any{"name", "like", "conf02 %"}},
		"fields": []string{"id", "name"},
	}, authBearer(confBearerToken)))

	for _, raw := range rows {
		row := asMap(t, raw)
		if recordID(t, row) == fixture.docSecret {
			t.Fatalf("record rule failed: classified doc visible to viewer: %v", row)
		}
		if row["name"] == "conf02 classified" {
			t.Fatalf("record rule failed: classified name visible to viewer: %v", row)
		}
	}
}

// issueConfPasswordBearer mints an OAuth access token for one fixture user
// via the same grant_type=password path POST /oauth/token uses, standing in
// for the removed stored-authtoken bearer fixtures (RBAC/OAuth backend plan
// §1/§7.4).
func issueConfPasswordBearer(t *testing.T, suite *confSuite, login, password string) (string, error) {
	t.Helper()
	pair, err := baseauth.CurrentProvider().IssuePassword(t.Context(), suite.dbName, login, password)
	if err != nil {
		return "", err
	}
	return pair.AccessToken, nil
}
