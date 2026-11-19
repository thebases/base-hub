package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime rbac, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `init`, `AuthenticateRPCBasic`, `RBACIdentity`, `rbacUserRow`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

const (
	// RBAC context keys keep the authenticated caller metadata available to
	// downstream runtime methods without coupling them to Fiber request state.
	RBACContextLoginKey    = "rpc_login"
	RBACContextRoleIDKey   = "rpc_role_id"
	RBACContextRoleKey     = "rpc_role_name"
	RBACContextAuthModeKey = "rpc_auth_mode"
	// RBACContextGroupIDsKey carries the user's primary group plus all groups
	// transitively implied by it, expanded at auth time so every RBAC check can
	// match against the full membership set without extra DB round-trips.
	RBACContextGroupIDsKey = "rpc_group_ids"
)

// The four baseline tbl.group roles and the model-name-prefix / record-scope
// hierarchy they grant. base.group_no_one is not listed as a BID constant
// used in ACL checks: its `superadmin` flag makes it bypass ensureRBACModelAccess
// and record rules entirely via env.IsSudo(), so no group-BID branch names it.
const (
	groupBIDUser         = "base.group_user"
	groupBIDMultiCompany = "base.group_multi_company"
	groupBIDSystem       = "base.group_system"
)

// RBACIdentity captures the authenticated RPC caller and the authorization
// attributes needed to enforce model-level access.
type RBACIdentity struct {
	UserID       int64
	DBName       string
	Login        string
	RoleID       int64
	RoleName     string
	GroupIDs     []int64
	CompanyID    int64
	CompanyIDs   []int64
	AuthMode     string
	IsSuperadmin bool
	// Lang is the user's preferred language code (e.g. "en_US", "vi").
	// Populated from base_users.lang at authentication time so the session
	// response can carry the locale without a second round-trip.
	Lang string
}

type rbacUserRow struct {
	ID             int64          `bun:"id"`
	Login          string         `bun:"login"`
	Password       string         `bun:"password"`
	GroupIDRaw     string         `bun:"group_id_raw"`
	RoleName       sql.NullString `bun:"role_name"`
	RoleSuperadmin sql.NullBool   `bun:"role_superadmin"`
	// GroupIDs is NOT a DB column. enrichRBACUserRole populates it in-memory with
	// the primary role plus all transitively implied groups so identityFromRBACUser
	// can embed the full set without an extra query.
	GroupIDs []int64 `bun:"-"`
	// GroupBIDs is NOT a DB column either. enrichRBACUserRole resolves the BID of
	// every id in GroupIDs so identityFromRBACUser can test membership in the
	// baseline tbl.group roles (base.group_user/multi_company/system) by name.
	GroupBIDs  []string      `bun:"-"`
	CompanyID  sql.NullInt64 `bun:"company_id"`
	CompanyIDs []int64       `bun:"company_ids"`
	// Lang stores the user's locale preference; nullable because the column may
	// be empty in older rows before the default was applied.
	Lang sql.NullString `bun:"lang"`
}

// rbacGroupBIDRow resolves a group id to its BID and superadmin flag. Shared
// by enrichRBACUserRole (superadmin + multi-company scope at auth time) and
// ensureRBACModelAccess (tbl./base./ui./sys. prefix grants) so both checks
// resolve group membership the same way and never drift.
type rbacGroupBIDRow struct {
	ID         int64  `bun:"id"`
	BID        string `bun:"bid"`
	Name       string `bun:"name"`
	Superadmin bool   `bun:"superadmin"`
}

type rbacAccessRow struct {
	ID         int64         `bun:"id"`
	Active     bool          `bun:"active"`
	ModelName  string        `bun:"model_name"`
	GroupID    sql.NullInt64 `bun:"group_id"`
	PermRead   bool          `bun:"perm_read"`
	PermWrite  bool          `bun:"perm_write"`
	PermCreate bool          `bun:"perm_create"`
	PermUnlink bool          `bun:"perm_unlink"`
}

func init() {
	// Install RBAC as the shared fallback so every registered model can opt into
	// the same policy without per-model wiring.
	RegisterDefaultAccessHandler(ensureRBACModelAccess)
}

// AuthenticateRPCBasic validates login/password against the legacy users table.
func AuthenticateRPCBasic(ctx context.Context, login, password string) (*RBACIdentity, error) {
	user, err := lookupRPCUserByLogin(ctx, strings.TrimSpace(login))
	if err != nil {
		return nil, err
	}
	if user == nil {
		// Same bcrypt cost as a real check so the response time does not reveal unknown logins.
		EqualizePasswordCheckTiming(password)
		log.Errorf("invalid login or password")
		return nil, fmt.Errorf("invalid login or password")
	}
	matched, legacy := matchStoredPasswordDetail(user.Password, password)
	if !matched {
		log.Errorf("invalid login or password")
		return nil, fmt.Errorf("invalid login or password")
	}
	// SEC-02: a successful login through a legacy (plaintext/SHA-1) branch is the
	// only moment the cleartext password is in hand, so upgrade the stored value
	// to bcrypt immediately; otherwise legacy credentials would linger until a
	// manual password change.
	if legacy {
		rehashLegacyRPCPassword(ctx, user, password)
	}
	return identityFromRBACUser(user, "basic"), nil
}

// EnvironmentForIdentity binds the authenticated caller into one ORM runtime
// environment so every downstream model method sees the same identity.
func EnvironmentForIdentity(identity *RBACIdentity) *Environment {
	if identity == nil {
		return nil
	}
	values := map[string]any{
		RBACContextLoginKey:    identity.Login,
		RBACContextRoleIDKey:   identity.RoleID,
		RBACContextRoleKey:     identity.RoleName,
		RBACContextAuthModeKey: identity.AuthMode,
		// Store all group memberships so ensureRBACModelAccess can use them
		// when the primary role alone does not resolve to a valid ACL entry.
		RBACContextGroupIDsKey:         append([]int64(nil), identity.GroupIDs...),
		ContextKeyCurrentCompanyID:     identity.CompanyID,
		ContextKeyAllowedCompanyIDs:    append([]int64(nil), identity.CompanyIDs...),
		ContextKeyAuthorizedCompanyIDs: append([]int64(nil), identity.CompanyIDs...),
		ContextKeyDatabaseName:         identity.DBName,
	}
	// The caller's own language is the default for translated fields: a request that names no
	// lang reads (and writes) in the user's language. A request lang (context.lang / lang kwarg)
	// is merged over this later and wins.
	if lang := strings.TrimSpace(identity.Lang); lang != "" {
		values["lang"] = lang
	}
	return NewEnvironmentWithDBName(runtimeDBForName(identity.DBName), identity.DBName, identity.UserID, values, identity.IsSuperadmin)
}

// ensureRBACModelAccess applies the fallback RBAC policy to RPC-authenticated
// callers: caller groups, then the base-group prefix grant, then sys_model_access
// rows. It is deliberately serial and deterministic (audit-sensitive).
func ensureRBACModelAccess(ctx context.Context, env *Environment, model *ModelMetadata, operation string, _ []string, _ RecordMap) error {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return fmt.Errorf("orm: model metadata is required")
	}
	if !rbacPolicyApplies(env) {
		return nil
	}
	allGroupIDs, err := rbacCallerGroupIDs(env, model)
	if err != nil {
		return err
	}

	normalizedOperation := normalizeRBACOperation(operation)
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("rbac database is not initialized")
		return fmt.Errorf("rbac database is not initialized")
	}

	granted, err := rbacBaselineGrant(ctx, db, allGroupIDs, model.Name, normalizedOperation)
	if err != nil {
		return err
	}
	if granted {
		return nil
	}

	rows, err := loadRBACAccessRows(ctx, db, model.Name, allGroupIDs)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if accessRowAllows(row, normalizedOperation) {
			return nil
		}
	}
	log.Errorf("permission denied: user %d cannot %s model %q", env.UserID(), normalizedOperation, model.Name)
	return fmt.Errorf("permission denied: user %d cannot %s model %q", env.UserID(), normalizedOperation, model.Name)
}

// rbacPolicyApplies reports whether the fallback RBAC policy covers env. Only
// RPC-authenticated requests flow through it; internal runtime callers (nil
// env, sudo, or no auth mode) keep their existing default semantics.
func rbacPolicyApplies(env *Environment) bool {
	if env == nil || env.IsSudo() {
		return false
	}
	_, ok := env.ContextValue(RBACContextAuthModeKey)
	return ok
}

// rbacCallerGroupIDs collects every group the caller carries: the primary role
// plus any extra group_ids stored at auth time, so a user can access a model
// when any of their groups grants the permission. It fails for anonymous callers
// and for callers with no role or group at all (nothing to match in sys_model_access).
func rbacCallerGroupIDs(env *Environment, model *ModelMetadata) ([]int64, error) {
	if env.UserID() <= 0 {
		log.Errorf("permission denied: anonymous callers cannot access model %q", model.Name)
		return nil, fmt.Errorf("permission denied: anonymous callers cannot access model %q", model.Name)
	}
	roleID, _ := env.ContextValue(RBACContextRoleIDKey)
	groupIDsRaw, _ := env.ContextValue(RBACContextGroupIDsKey)
	allGroupIDs := normalizeRBACGroupIDs(toInt64(roleID), int64SliceFromAny(groupIDsRaw))
	if len(allGroupIDs) == 0 {
		log.Errorf("permission denied: user %d has no assigned role for model %q", env.UserID(), model.Name)
		return nil, fmt.Errorf("permission denied: user %d has no assigned role for model %q", env.UserID(), model.Name)
	}
	return allGroupIDs, nil
}

// rbacBaselineGrant is the tbl.group baseline: base.group_user/multi_company/
// system grant blanket CRUD/read by model-name prefix regardless of any
// sys_model_access row, so a fresh install works before an admin has seeded
// per-model ACL rows. base.group_no_one is not checked here: its superadmin
// flag already bypasses the whole policy via env.IsSudo().
func rbacBaselineGrant(ctx context.Context, db *bun.DB, groupIDs []int64, modelName, operation string) (bool, error) {
	groupBIDRows, err := lookupRBACGroupBIDs(ctx, db, groupIDs)
	if err != nil {
		return false, err
	}
	groupBIDs := make([]string, 0, len(groupBIDRows))
	for _, row := range groupBIDRows {
		if row.BID != "" {
			groupBIDs = append(groupBIDs, row.BID)
		}
	}
	return prefixGrantsAccess(groupBIDs, modelName, operation), nil
}

// loadRBACAccessRows matches any ACL row that applies to one of the caller's
// groups OR is public (group_id IS NULL). The OR-NULL rule lets administrators
// define model-wide access without assigning a specific group. No rows at all
// is a denial.
func loadRBACAccessRows(ctx context.Context, db *bun.DB, modelName string, groupIDs []int64) ([]rbacAccessRow, error) {
	rows := make([]rbacAccessRow, 0, 4)
	if err := db.NewSelect().
		TableExpr("sys_model_access AS access").
		ColumnExpr("access.id").
		ColumnExpr("access.active").
		ColumnExpr("access.model_name").
		ColumnExpr("access.group_id").
		ColumnExpr("access.perm_read").
		ColumnExpr("access.perm_write").
		ColumnExpr("access.perm_create").
		ColumnExpr("access.perm_unlink").
		Where("access.active = ?", true).
		Where("access.model_name = ?", modelName).
		Where("(access.group_id IN (?) OR access.group_id IS NULL)", bun.In(groupIDs)).
		Scan(ctx, &rows); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			log.Errorf("permission denied: model ACL storage is not ready")
			return nil, fmt.Errorf("permission denied: model ACL storage is not ready")
		}
		return nil, err
	}
	return rows, nil
}

// LookupRBACIdentityByUserID re-reads a user's current role/company scope by
// primary key. Used by the OAuth refresh grant (modules/base-auth) to embed
// live RBAC state in the reissued access token instead of replaying whatever
// the prior token's claims happened to say (RBAC/OAuth backend plan §3).
func LookupRBACIdentityByUserID(ctx context.Context, userID int64) (*RBACIdentity, error) {
	if userID <= 0 {
		return nil, fmt.Errorf("user id is required")
	}
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("rbac database is not initialized")
		return nil, fmt.Errorf("rbac database is not initialized")
	}
	var row rbacUserRow
	err := db.NewSelect().
		TableExpr("base_users AS u").
		ColumnExpr("u.id").
		ColumnExpr("u.login").
		ColumnExpr("u.password").
		ColumnExpr("u.company_id").
		ColumnExpr(rbacUserCompanyIDsColumn()).
		ColumnExpr("u.lang").
		ColumnExpr("COALESCE(NULLIF(BTRIM(u.group_id::text), ''), '') AS group_id_raw").
		Where("u.id = ?", userID).
		// Inactive (archived) users must not authenticate by any method; NULL (legacy rows) counts as active.
		Where("u.active IS NOT FALSE").
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := enrichRBACUserRole(ctx, &row); err != nil {
		return nil, err
	}
	return identityFromRBACUser(&row, "bearer"), nil
}

// HashAPIKey returns the hex SHA-256 digest under which base_users.api_key is stored. A fast hash
// is acceptable because API keys are high-entropy machine secrets, and it keeps lookup indexable.
func HashAPIKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// LookupRBACIdentityByAPIKey resolves the user whose stored api_key digest matches the presented
// X-API-Key, with the same role/company enrichment as password login so both methods share one
// role model. Returns nil (no error) for an unknown/blank key; users with a NULL api_key never match.
func LookupRBACIdentityByAPIKey(ctx context.Context, apiKey string) (*RBACIdentity, error) {
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, nil
	}
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("rbac database is not initialized")
		return nil, fmt.Errorf("rbac database is not initialized")
	}
	var row rbacUserRow
	err := db.NewSelect().
		TableExpr("base_users AS u").
		ColumnExpr("u.id").
		ColumnExpr("u.login").
		ColumnExpr("u.password").
		ColumnExpr("u.company_id").
		ColumnExpr(rbacUserCompanyIDsColumn()).
		ColumnExpr("u.lang").
		ColumnExpr("COALESCE(NULLIF(BTRIM(u.group_id::text), ''), '') AS group_id_raw").
		Where("u.api_key IS NOT NULL").
		// Inactive (archived) users must not authenticate by any method; NULL (legacy rows) counts as active.
		Where("u.active IS NOT FALSE").
		Where("u.api_key = ?", HashAPIKey(apiKey)).
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := enrichRBACUserRole(ctx, &row); err != nil {
		return nil, err
	}
	return identityFromRBACUser(&row, "apikey"), nil
}

// LookupRBACIdentityByLogin re-reads a user's current role/company scope by
// login. Used by the external-OIDC provider (modules/base-auth) to map a
// verified IdP identity claim onto a local RBAC identity.
func LookupRBACIdentityByLogin(ctx context.Context, login string) (*RBACIdentity, error) {
	user, err := lookupRPCUserByLogin(ctx, strings.TrimSpace(login))
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, nil
	}
	return identityFromRBACUser(user, "oidc"), nil
}

// lookupRPCUserByLogin looks up the rpc user by login handled by this package.
func lookupRPCUserByLogin(ctx context.Context, login string) (*rbacUserRow, error) {
	if login == "" {
		log.Errorf("login is required")
		return nil, fmt.Errorf("login is required")
	}
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("rbac database is not initialized")
		return nil, fmt.Errorf("rbac database is not initialized")
	}

	var row rbacUserRow
	err := db.NewSelect().
		TableExpr("base_users AS u").
		ColumnExpr("u.id").
		ColumnExpr("u.login").
		ColumnExpr("u.password").
		ColumnExpr("u.company_id").
		ColumnExpr(rbacUserCompanyIDsColumn()).
		ColumnExpr("u.lang").
		// Read the primary group identifier through text so empty-string values in
		// older rows do not force PostgreSQL to cast "" into a bigint.
		ColumnExpr("COALESCE(NULLIF(BTRIM(u.group_id::text), ''), '') AS group_id_raw").
		Where("u.login = ?", login).
		// Inactive (archived) users must not authenticate by any method; NULL (legacy rows) counts as active.
		Where("u.active IS NOT FALSE").
		Limit(1).
		Scan(ctx, &row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if err := enrichRBACUserRole(ctx, &row); err != nil {
		return nil, err
	}
	return &row, nil
}

func identityFromRBACUser(user *rbacUserRow, authMode string) *RBACIdentity {
	if user == nil {
		return nil
	}
	groupID := parseRBACGroupID(user.GroupIDRaw)
	roleName := ""
	if user.RoleName.Valid {
		roleName = strings.TrimSpace(user.RoleName.String)
	}
	superadmin := false
	if user.RoleSuperadmin.Valid {
		superadmin = user.RoleSuperadmin.Bool
	}
	// Default to "en_US" when the stored lang is empty so the frontend always
	// receives a well-formed locale code without a conditional on its side.
	lang := strings.TrimSpace(user.Lang.String)
	if lang == "" {
		lang = "en_us"
	}
	companyIDs := normalizeRBACCompanyIDs(user.CompanyID, user.CompanyIDs)
	// base.group_user on its own is scoped to its own primary company only; the
	// base_users.company_ids many2many only applies once base.group_multi_company
	// is in the (implied-expanded) group set. Superadmins bypass record rules
	// entirely via env.IsSudo() and never consult CompanyIDs for that decision.
	if !superadmin && !slices.Contains(user.GroupBIDs, groupBIDMultiCompany) {
		if user.CompanyID.Valid && user.CompanyID.Int64 > 0 {
			companyIDs = []int64{user.CompanyID.Int64}
		} else {
			companyIDs = nil
		}
	}
	return &RBACIdentity{
		UserID: user.ID,
		Login:  strings.TrimSpace(user.Login),
		// Keep RoleID as the public identity field for compatibility with the
		// broader RBAC API, but source it from base_users.group_id.
		RoleID:       groupID,
		RoleName:     roleName,
		GroupIDs:     normalizeRBACGroupIDs(groupID, user.GroupIDs),
		CompanyID:    user.CompanyID.Int64,
		CompanyIDs:   companyIDs,
		AuthMode:     authMode,
		IsSuperadmin: superadmin,
		Lang:         lang,
	}
}

func enrichRBACUserRole(ctx context.Context, user *rbacUserRow) error {
	if user == nil {
		return nil
	}
	groupID := parseRBACGroupID(user.GroupIDRaw)
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("rbac database is not initialized")
		return fmt.Errorf("rbac database is not initialized")
	}

	// Expand implied groups transitively so a user whose primary role inherits
	// from another group automatically passes ACL checks for the implied group's
	// models. Expansion is done before the role name lookup so the full set is
	// available even when groupID is 0 (user has only explicit group_ids).
	seed := normalizeRBACGroupIDs(groupID, user.GroupIDs)
	expanded, err := expandImpliedGroupIDs(ctx, db, seed)
	if err != nil {
		return err
	}
	user.GroupIDs = expanded

	// Resolve BID/name/superadmin across the FULL implied-group set, not just the
	// primary group: a superadmin or base.group_multi_company grant reached only
	// through implication (e.g. base.group_system -> base.group_multi_company)
	// must still count. Skip the query when there is nothing to resolve.
	if len(expanded) == 0 {
		return nil
	}
	groupRows, err := lookupRBACGroupBIDs(ctx, db, expanded)
	if err != nil {
		return err
	}
	bids := make([]string, 0, len(groupRows))
	superadmin := false
	for _, row := range groupRows {
		if row.BID != "" {
			bids = append(bids, row.BID)
		}
		if row.Superadmin {
			superadmin = true
		}
		// The primary group's own name is the caller-facing role name.
		if row.ID == groupID {
			user.RoleName = sql.NullString{String: row.Name, Valid: row.Name != ""}
		}
	}
	user.GroupBIDs = bids
	user.RoleSuperadmin = sql.NullBool{Bool: superadmin, Valid: true}
	return nil
}

// lookupRBACGroupBIDs resolves the BID, name, and superadmin flag for a set of
// group ids in one query. Returns nil, nil when the relation table does not
// exist yet (pre-migration) so callers degrade gracefully like
// expandImpliedGroupIDs does.
func lookupRBACGroupBIDs(ctx context.Context, db *bun.DB, groupIDs []int64) ([]rbacGroupBIDRow, error) {
	// Nothing to resolve; skip the round-trip entirely.
	if len(groupIDs) == 0 || db == nil {
		return nil, nil
	}
	var rows []rbacGroupBIDRow
	err := db.NewSelect().
		TableExpr("tbl_groups AS g").
		ColumnExpr("g.id").
		ColumnExpr("g.bid").
		ColumnExpr("g.name").
		ColumnExpr("g.superadmin").
		Where("g.id IN (?)", bun.In(groupIDs)).
		Scan(ctx, &rows)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return nil, nil
		}
		return nil, err
	}
	return rows, nil
}

// prefixGrantsAccess implements the tbl.group baseline access model: group
// membership grants blanket CRUD/read by model-name prefix, independent of any
// sys_model_access row, so a fresh install works before an admin has seeded
// per-model ACL rows. base.group_no_one is not handled here: it bypasses
// ensureRBACModelAccess entirely via its superadmin flag (env.IsSudo()).
func prefixGrantsAccess(groupBIDs []string, modelName, operation string) bool {
	prefix := modelNamespacePrefix(modelName)
	// base.group_user: full CRUD on tbl./base., read-only on ui. A non-read ui.
	// operation must NOT return false here: it has to fall through so the
	// base.group_system branch below still gets a chance to grant it.
	if slices.Contains(groupBIDs, groupBIDUser) {
		switch prefix {
		case "tbl.", "base.":
			return true
		case "ui.":
			if operation == "read" {
				return true
			}
		}
	}
	// base.group_system: full CRUD on ui./sys., on top of whatever
	// base.group_user above already granted (group_system implies group_user
	// transitively through group_multi_company, so groupBIDs already carries it).
	if slices.Contains(groupBIDs, groupBIDSystem) {
		switch prefix {
		case "ui.", "sys.":
			return true
		}
	}
	return false
}

// modelNamespacePrefix returns the dotted namespace prefix ("tbl.", "base.",
// "ui.", "sys.") a model's logical name is registered under, or "" when the
// name has no dot segment to classify.
func modelNamespacePrefix(modelName string) string {
	idx := strings.Index(modelName, ".")
	if idx <= 0 {
		return ""
	}
	return modelName[:idx+1]
}

// expandImpliedGroupIDs walks the tbl_group_implied_rel join table with a
// recursive CTE and returns seed plus every transitively implied group ID.
// The relation table columns are: gid = the group that carries the implied set,
// hid = the group that is implied (child). Cycles are handled by UNION
// (not UNION ALL) which deduplicates automatically in PostgreSQL.
// Returns seed unchanged when the table does not exist yet (pre-migration).
func expandImpliedGroupIDs(ctx context.Context, db *bun.DB, seed []int64) ([]int64, error) {
	// Nothing to expand; skip the round-trip entirely.
	if len(seed) == 0 || db == nil {
		return seed, nil
	}
	type impliedRow struct {
		GroupID int64 `bun:"group_id"`
	}
	var rows []impliedRow
	err := db.NewRaw(
		// Walk the full implied chain in one query: start from all seed group IDs,
		// then recursively follow gid→hid edges until no new rows are added.
		`WITH RECURSIVE implied_tree AS (
			SELECT hid AS group_id FROM tbl_group_implied_rel WHERE gid IN (?)
			UNION
			SELECT r.hid FROM tbl_group_implied_rel r
			JOIN implied_tree t ON r.gid = t.group_id
		)
		SELECT DISTINCT group_id FROM implied_tree`,
		bun.In(seed),
	).Scan(ctx, &rows)
	if err != nil {
		// Gracefully degrade when the relation table has not been migrated yet;
		// ACL checks still work via the seed groups the caller already holds.
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return seed, nil
		}
		return nil, err
	}
	result := append([]int64(nil), seed...)
	for _, r := range rows {
		result = append(result, r.GroupID)
	}
	return uniqueInt64Slice(result), nil
}

// CallerGroupBIDs returns the BIDs of every group the caller holds, including
// groups implied through tbl_group_implied_rel (e.g. base.group_system implies
// base.group_multi_company). Callers that gate UI visibility by group, such as
// the sidebar menu, use it instead of re-reading the group tables themselves.
// A nil environment yields no groups; sudo/superadmin bypass is the caller's call.
func CallerGroupBIDs(ctx context.Context) ([]string, error) {
	env := EnvironmentFromContext(ctx)
	if env == nil {
		return nil, nil
	}
	roleID, _ := env.ContextValue(RBACContextRoleIDKey)
	groupIDsRaw, _ := env.ContextValue(RBACContextGroupIDsKey)
	seed := normalizeRBACGroupIDs(toInt64(roleID), int64SliceFromAny(groupIDsRaw))
	db := runtimeAuthDB(ctx)
	if len(seed) == 0 || db == nil {
		return nil, nil
	}
	expanded, err := expandImpliedGroupIDs(ctx, db, seed)
	if err != nil {
		return nil, err
	}
	rows, err := lookupRBACGroupBIDs(ctx, db, expanded)
	if err != nil {
		return nil, err
	}
	bids := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.BID != "" {
			bids = append(bids, row.BID)
		}
	}
	return bids, nil
}

// parseRBACGroupID parses the primary RBAC group id handled by this package.
func parseRBACGroupID(raw string) int64 {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

// matchStoredPasswordDetail reports whether the candidate matches the stored
// credential and whether a legacy (non-bcrypt) branch produced the match, so
// the caller can trigger a bcrypt re-hash while the cleartext credential is
// still in hand (SEC-02).
func matchStoredPasswordDetail(stored, candidate string) (matched bool, legacy bool) {
	stored = strings.TrimSpace(stored)
	if stored == "" || candidate == "" {
		// A passwordless account answers as slowly as a wrong password would.
		EqualizePasswordCheckTiming(candidate)
		return false, false
	}
	if strings.HasPrefix(stored, "$2") {
		return bcrypt.CompareHashAndPassword([]byte(stored), []byte(candidate)) == nil, false
	}
	// TODO(SEC-02): remove the plaintext and SHA-1 fallback branches below once
	// telemetry shows zero `legacy_password_rehash` log hits; removal is then a
	// one-line PR. Do not remove earlier — seeded legacy rows would be locked out.
	// Both compares are constant-time so a wrong guess learns nothing from timing (plan T36).
	if subtle.ConstantTimeCompare([]byte(stored), []byte(candidate)) == 1 {
		return true, true
	}
	sum := sha1.Sum([]byte(candidate))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(stored)), []byte(hex.EncodeToString(sum[:]))) == 1 {
		return true, true
	}
	return false, false
}

var dummyPasswordHash = sync.OnceValue(func() []byte {
	hash, err := bcrypt.GenerateFromPassword([]byte("base-hub-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		log.Errorf("orm: dummy password hash: %v", err)
	}
	return hash
})

// EqualizePasswordCheckTiming runs one bcrypt compare at the default cost against a fixed hash
// and discards the result. Call it on the paths that refuse a login without comparing a real
// hash (unknown login, no stored password) so their latency matches a wrong password.
func EqualizePasswordCheckTiming(candidate string) {
	_ = bcrypt.CompareHashAndPassword(dummyPasswordHash(), []byte(candidate))
}

// rehashLegacyRPCPassword persists a bcrypt upgrade for a credential that just
// authenticated through a legacy branch. Failures are logged, never fatal: the
// login itself already succeeded and the legacy stored value keeps working, so
// the next successful login simply retries the upgrade.
func rehashLegacyRPCPassword(ctx context.Context, user *rbacUserRow, candidate string) {
	// Reuse the same request-bound DB handle the auth read used so the upgrade
	// lands on the database that actually authenticated the user.
	db := runtimeAuthDB(ctx)
	if db == nil {
		log.Errorf("legacy_password_rehash skipped for user %d: rbac database is not initialized", user.ID)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(candidate), bcrypt.DefaultCost)
	if err != nil {
		log.Errorf("legacy_password_rehash hashing failed for user %d: %v", user.ID, err)
		return
	}
	// Guard the update with the value read during authentication so a concurrent
	// password change between the auth read and this write is never overwritten.
	res, err := db.NewUpdate().
		Table("base_users").
		Set("password = ?", string(hash)).
		Where("id = ?", user.ID).
		Where("password = ?", user.Password).
		Exec(ctx)
	if err != nil {
		log.Errorf("legacy_password_rehash update failed for user %d: %v", user.ID, err)
		return
	}
	if rows, rowsErr := res.RowsAffected(); rowsErr == nil && rows == 0 {
		log.Warnf("legacy_password_rehash skipped for user %d: stored password changed concurrently", user.ID)
		return
	}
	// Ops-visible migration signal: count these lines to decide when the legacy
	// fallback branches can be deleted (see TODO(SEC-02) above).
	log.Infof("legacy_password_rehash user_id=%d login=%s", user.ID, user.Login)
}

func normalizeRBACOperation(operation string) string {
	switch strings.TrimSpace(strings.ToLower(operation)) {
	case "create", "new", "name_create":
		return "create"
	case "write", "update", "toggle_active":
		return "write"
	case "unlink", "delete", "remove":
		return "unlink"
	default:
		return "read"
	}
}

func runtimeAuthDB(ctx context.Context) *bun.DB {
	if env := EnvironmentFromContext(ctx); env != nil {
		if envName := strings.TrimSpace(env.DBName()); envName != "" {
			if db, err := EnsureNamedBunDB(envName); err == nil && db != nil {
				return db.DB
			}
		}
		if db, ok := env.DB().(*bun.DB); ok && db != nil {
			return db
		}
		if db := runtimeDBForName(env.DBName()); db != nil {
			return db
		}
	}
	return GetBunDB()
}

func runtimeDBForName(name string) *bun.DB {
	if db := ResolveBunDB(name); db != nil {
		return db
	}
	return GetBunDB()
}

// rbacUserCompanyIDsColumn selects base.users.company_ids for the RBAC identity lookups.
// The field is a many2many the ORM stores in its relation table (base_users_tbl_company_rel by
// default); the jsonb column of the same name is not written by ORM create/write and stays NULL,
// so reading only the column collapsed every user's authorized companies to the primary one.
// Join rows win; the column is the fallback for legacy rows and for a registry without
// base.users (kernel-only tests), where the plain column is selected as before.
func rbacUserCompanyIDsColumn() string {
	model, ok := LookupModel("base.users")
	if !ok || model == nil {
		return "u.company_ids"
	}
	for _, field := range model.Fields {
		if field.Name != "company_ids" || field.Kind != FieldKindManyToMany {
			continue
		}
		if field.RelationTable == "" || field.RelationColumn == "" || field.RelationKey == "" {
			break
		}
		return fmt.Sprintf(
			"COALESCE((SELECT jsonb_agg(rel.%[3]s ORDER BY rel.%[3]s) FROM %[1]s AS rel WHERE rel.%[2]s = u.id), u.company_ids) AS company_ids",
			quoteIdent(field.RelationTable), quoteIdent(field.RelationColumn), quoteIdent(field.RelationKey),
		)
	}
	return "u.company_ids"
}

func normalizeRBACCompanyIDs(primary sql.NullInt64, allowed []int64) []int64 {
	normalized := uniqueInt64Slice(allowed)
	if primary.Valid && primary.Int64 > 0 {
		normalized = append([]int64{primary.Int64}, removeInt64Value(normalized, primary.Int64)...)
	}
	return uniqueInt64Slice(normalized)
}

func normalizeRBACGroupIDs(primary int64, groups []int64) []int64 {
	normalized := uniqueInt64Slice(groups)
	if primary > 0 {
		normalized = append([]int64{primary}, removeInt64Value(normalized, primary)...)
	}
	return uniqueInt64Slice(normalized)
}

func accessRowAllows(row rbacAccessRow, operation string) bool {
	switch operation {
	case "create":
		return row.PermCreate
	case "write":
		return row.PermWrite
	case "unlink":
		return row.PermUnlink
	default:
		return row.PermRead
	}
}
