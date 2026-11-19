package models

// Attachment write and listing rules (plan T33, SEC-D-14/SEC-D-15).
//
//   - Write on an existing row: creator, or write access on the bound record (enforced per id in
//     sysAttachmentAccessHandler). Changing is_public: creator or sudo only.
//   - Read record rule: id-less searches (search, search_read, read_group, many2one lookups) only
//     return rows sysAttachmentAccessHandler would let the caller read: creator, public, matching
//     company/user/group scope, unbound rows with no scope, or rows bound to a record the caller
//     can read. It is ANDed with the configured sys.rule rows, which a model-specific rule
//     handler would otherwise replace.
//
// Only non-sudo RPC callers are restricted by the record rule; sudo, seeds and internal Go
// callers keep the configured sys.rule behavior only (same population as the discuss rule).

import (
	"context"
	"strconv"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// Test seams over helpers that need a database.
var (
	sysAttachmentTargetAccess    = orm.EnsureRecordAccess
	sysAttachmentBoundModels     = distinctSysAttachmentBoundModels
	sysAttachmentTargetReadScope = sysAttachmentTargetReadDomain
	sysAttachmentBaseRule        = defaultSysRuleDomain
	sysAttachmentRowsByID        = loadSysAttachmentRowsByID
)

func init() {
	orm.RegisterRecordRule(SysAttachmentModelName, sysAttachmentRecordRule)
}

// sysAttachmentRestrictedEnv reports whether env is a non-sudo RPC caller.
func sysAttachmentRestrictedEnv(env *orm.Environment) bool {
	if env == nil || env.IsSudo() {
		return false
	}
	_, ok := env.ContextValue(orm.RBACContextAuthModeKey)
	return ok
}

// sysAttachmentWriteAllowed: the creator, or a caller with write access on the bound record.
func sysAttachmentWriteAllowed(ctx context.Context, env *orm.Environment, row sysAttachmentAccessRow) bool {
	if env != nil && env.UserID() == row.CreateUID {
		return true
	}
	if row.ResModel == "" || row.ModelId <= 0 {
		return false
	}
	return sysAttachmentTargetAccess(ctx, row.ResModel, "write", []string{strconv.FormatInt(row.ModelId, 10)}) == nil
}

// ensureSysAttachmentPublishAllowed refuses an is_public change by anyone but the creator (or
// sudo). A value equal to the stored one passes, so whole-record form saves keep working.
func ensureSysAttachmentPublishAllowed(ctx context.Context, ids []string, values orm.RecordMap) error {
	if !hasRecordMapKey(values, "is_public") {
		return nil
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return nil
	}
	// Anything but a bool equal to the stored value counts as a change, so a
	// loosely typed payload ("true", 1) cannot slip past as "unchanged".
	want, isBool := values["is_public"].(bool)
	rows, err := sysAttachmentRowsByID(ctx, ids)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if (!isBool || row.IsPublic != want) && row.CreateUID != env.UserID() {
			return orm.Forbiddenf("permission denied: only the uploader can change is_public on attachment %d", row.ID)
		}
	}
	return nil
}

// sysAttachmentRecordRule is the record rule for sys.attachment (file header).
func sysAttachmentRecordRule(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string) (orm.DomainNode, error) {
	base, err := sysAttachmentBaseRule(ctx, env, model, operation)
	if err != nil {
		return orm.DomainNode{}, err
	}
	// Writes and unlinks are decided per id by the access handler; the rule only scopes reads.
	if operation != "read" || !sysAttachmentRestrictedEnv(env) {
		return base, nil
	}
	ctx = orm.ContextWithEnvironment(ctx, env)
	userID := env.UserID()
	visible := []orm.DomainNode{
		orm.Where("create_uid", "=", userID),
		orm.Where("is_public", "=", true),
		orm.Where("user_id", "=", userID),
		// Unbound and unscoped: shared with every authenticated user of the database.
		orm.And(
			orm.Or(orm.Where("sys_model", "=", nil), orm.Where("sys_model", "=", ""), orm.Where("model_id", "=", nil), orm.Where("model_id", "<=", 0)),
			orm.Where("company_id", "=", nil), orm.Where("user_id", "=", nil), orm.Where("group_id", "=", nil),
		),
	}
	if companies, err := env.AllowedCompanyIDs(); err == nil && len(companies) > 0 {
		visible = append(visible, orm.Where("company_id", "in", companies))
	}
	if raw, ok := env.ContextValue(orm.RBACContextGroupIDsKey); ok {
		if groups, ok := raw.([]int64); ok && len(groups) > 0 {
			visible = append(visible, orm.Where("group_id", "in", groups))
		}
	}
	targetModels, err := sysAttachmentBoundModels(ctx)
	if err != nil {
		return orm.DomainNode{}, err
	}
	for _, targetModel := range targetModels {
		if targetModel == SysAttachmentModelName {
			continue
		}
		scope, ok, err := sysAttachmentTargetReadScope(ctx, targetModel)
		if err != nil {
			return orm.DomainNode{}, err
		}
		if ok {
			visible = append(visible, scope)
		}
	}
	rule := orm.Or(visible...)
	if base.Kind == "" || base.Kind == orm.DomainKindTrue {
		return rule, nil
	}
	return orm.And(base, rule), nil
}

// sysAttachmentTargetReadDomain returns the attachment domain for rows bound to targetModel
// records the caller may read, and false when none are readable. Restricted targets (read
// record rules) are listed by id through a caller-scoped search.
func sysAttachmentTargetReadDomain(ctx context.Context, targetModel string) (orm.DomainNode, bool, error) {
	meta, ok := orm.LookupModel(targetModel)
	if !ok {
		return orm.DomainNode{}, false, nil
	}
	if err := orm.EnsureRuntimeModelAccess(ctx, meta, orm.RuntimeMethod{Name: "search", ReadOnly: true}, nil); err != nil {
		return orm.DomainNode{}, false, nil
	}
	rule, err := orm.RecordRuleDomainFor(ctx, targetModel, "read")
	if err != nil {
		return orm.DomainNode{}, false, err
	}
	if rule.Kind == orm.DomainKindTrue {
		return orm.Where("sys_model", "=", targetModel), true, nil
	}
	result, err := orm.ExecuteRuntimeMethod(ctx, targetModel, "search", nil, []any{[]any{}}, nil)
	if err != nil {
		log.Warnf("sys.attachment: read rule could not scope %s: %v", targetModel, err)
		return orm.DomainNode{}, false, nil
	}
	ids := sysAttachmentAnyIDs(result)
	if len(ids) == 0 {
		return orm.DomainNode{}, false, nil
	}
	return orm.And(orm.Where("sys_model", "=", targetModel), orm.Where("model_id", "in", ids)), true, nil
}

func distinctSysAttachmentBoundModels(ctx context.Context) ([]string, error) {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		return nil, nil
	}
	var names []string
	err := db.NewSelect().
		TableExpr("sys_attachment").
		ColumnExpr("DISTINCT sys_model").
		Where("COALESCE(sys_model, '') <> ''").
		Where("model_id > 0").
		Scan(ctx, &names)
	return names, err
}

// sysAttachmentAnyIDs flattens a search result into domain "in" values.
func sysAttachmentAnyIDs(result any) []any {
	switch typed := result.(type) {
	case []any:
		return typed
	case []int64:
		out := make([]any, len(typed))
		for i, id := range typed {
			out[i] = id
		}
		return out
	case []string:
		out := make([]any, 0, len(typed))
		for _, id := range typed {
			if parsed, err := strconv.ParseInt(id, 10, 64); err == nil {
				out = append(out, parsed)
			}
		}
		return out
	default:
		return nil
	}
}
