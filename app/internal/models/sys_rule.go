package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys rule inside the kernel model catalog.
// - Key symbols: `init`, `createSysRule`, `SysRuleRecord`, `sysRuleState`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// SysRuleRecord stores record-level security metadata that can be audited and
// edited from the admin UI while remaining compatible with the shared runtime
// rule hooks.
type SysRuleRecord struct {
	// bun.BaseModel `bun:"table:sys_rule,alias:sys_rule"`
	orm.BaseModel
	BID         string   `bun:"bid" json:"bid" meta:"index,unique,label:BID,help:Stores the universal global unique record identifier for this record."`
	Name        string   `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	ModelName   string   `bun:"model_name" json:"model_name" meta:"index,label:Model Name,help:Stores the target model name enforced by this record rule."`
	DomainForce string   `bun:"domain_force" json:"domain_force" meta:"label:Domain Force,help:Stores the domain string applied by this record rule."`
	GlobalRule  bool     `bun:"global_rule" json:"global_rule" meta:"index,label:Global Rule,help:Controls whether the rule applies to everyone when no group BIDs are assigned."`
	PermRead    bool     `bun:"perm_read" json:"perm_read" meta:"label:Perm Read,help:Controls whether perm read is enabled for this record."`
	PermWrite   bool     `bun:"perm_write" json:"perm_write" meta:"label:Perm Write,help:Controls whether perm write is enabled for this record."`
	PermCreate  bool     `bun:"perm_create" json:"perm_create" meta:"label:Perm Create,help:Controls whether perm create is enabled for this record."`
	PermUnlink  bool     `bun:"perm_unlink" json:"perm_unlink" meta:"label:Perm Unlink,help:Controls whether perm unlink is enabled for this record."`
	GroupBIDs   []string `bun:"group_bids,type:jsonb,nullzero" json:"group_bids" meta:"kind:json,label:Group BIDs,help:Stores the BIDs of groups covered by this rule."`
}

const SysRuleModelName = "sys.rule"

var SysRuleModel = orm.Define[SysRuleRecord](&Models, 295, orm.ModelSpec{
	Name:        SysRuleModelName,
	Label:       "Record Rules",
	Description: "Record-level runtime access rules with per-model, per-group allow/deny semantics",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysRule},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysRule},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysRule},
	},
})

func init() {
	registerFrontendBIDConstraint(SysRuleModelName)
	orm.RegisterDefaultRecordRule(defaultSysRuleDomain)
}

// createSysRule creates the sys rule handled by this package.
func createSysRule(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("sys.rule.create requires values")
		return nil, fmt.Errorf("sys.rule.create requires values")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		normalized, err := normalizeSysRuleValues(ctx, values, nil, true)
		if err != nil {
			return nil, err
		}
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(SysRuleModelName, model), normalized)
		if err != nil {
			return nil, err
		}
		if err := logMetadataChange(ctx, SysRuleModelName, "create", "runtime_crud", nil, record); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeSysRule(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.rule.write requires ids")
		return nil, fmt.Errorf("sys.rule.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("sys.rule.write requires values")
		return nil, fmt.Errorf("sys.rule.write requires values")
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysRuleModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	normalized := cloneRecordMap(values)
	for _, before := range beforeRows {
		current := sysRuleStateFromRecord(before)
		normalized, err = normalizeSysRuleValues(ctx, normalized, &current, false)
		if err != nil {
			return nil, err
		}
	}

	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(SysRuleModelName, model), ids, normalized)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}

	afterRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysRuleModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	beforeByID := recordMapsByID(beforeRows)
	afterByID := recordMapsByID(afterRows)
	for _, id := range ids {
		after := afterByID[id]
		if len(after) == 0 {
			continue
		}
		if err := logMetadataChange(ctx, SysRuleModelName, "write", "runtime_crud", beforeByID[id], after); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

func unlinkSysRule(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return unlinkLoggedMetadataRecords(ctx, SysRuleModelName, ids)
}

type sysRuleState struct {
	ID          int64
	ModelName   string
	DomainForce string
	GlobalRule  bool
	PermRead    bool
	PermWrite   bool
	PermCreate  bool
	PermUnlink  bool
	GroupBIDs   []string
}

func normalizeSysRuleValues(ctx context.Context, values orm.RecordMap, current *sysRuleState, creating bool) (orm.RecordMap, error) {
	working := cloneRecordMap(values)

	if _, ok := working["active"]; !ok && creating {
		working["active"] = true
	}
	// Walk each string{"perm read", "perm write", "perm create", "perm unlink" entry in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"perm_read", "perm_write", "perm_create", "perm_unlink"} {
		if _, ok := working[key]; !ok && creating {
			working[key] = true
		}
	}

	if rawModelID, changed, err := optionalInt64FromRecord(working, "model_id"); err != nil {
		return nil, err
	} else if changed {
		if rawModelID == nil || *rawModelID <= 0 {
			delete(working, "model_id")
		} else {
			modelName, err := lookupSysModelNameByID(ctx, *rawModelID)
			if err != nil {
				return nil, err
			}
			working["model_name"] = modelName
		}
	}

	if modelName, ok := stringFromAny(working["model_name"]); ok {
		working["model_name"] = strings.TrimSpace(modelName)
	}
	if domainForce, ok := stringFromAny(working["domain_force"]); ok {
		working["domain_force"] = strings.TrimSpace(domainForce)
	}
	if name, ok := stringFromAny(working["name"]); ok {
		working["name"] = strings.TrimSpace(name)
	}

	if groups, changed, err := normalizeSysRuleGroups(working); err != nil {
		return nil, err
	} else if changed {
		working["group_bids"] = groups
		working["global_rule"] = len(groups) == 0
	} else if creating {
		working["group_bids"] = []string{}
		working["global_rule"] = true
	}

	if err := validateSysRuleValues(working, current); err != nil {
		return nil, err
	}
	return working, nil
}

// validateSysRuleValues validates the sys rule values handled by this package.
func validateSysRuleValues(values orm.RecordMap, current *sysRuleState) error {
	modelName := strings.TrimSpace(ruleStringValue(values["model_name"], currentFieldString(current, "model_name")))
	if modelName == "" {
		log.Errorf("sys.rule model_name is required")
		return fmt.Errorf("sys.rule model_name is required")
	}
	if _, exists := orm.LookupModel(modelName); !exists {
		log.Errorf("sys.rule unknown target model %q", modelName)
		return fmt.Errorf("sys.rule unknown target model %q", modelName)
	}
	if modelName == SysRuleModelName {
		log.Errorf("sys.rule cannot target itself")
		return fmt.Errorf("sys.rule cannot target itself")
	}

	name := strings.TrimSpace(ruleStringValue(values["name"], ""))
	if name == "" && current == nil {
		log.Errorf("sys.rule name is required")
		return fmt.Errorf("sys.rule name is required")
	}

	perms := sysRulePermissions(values, current)
	if !perms.PermRead && !perms.PermWrite && !perms.PermCreate && !perms.PermUnlink {
		log.Errorf("sys.rule requires at least one enabled permission")
		return fmt.Errorf("sys.rule requires at least one enabled permission")
	}

	domainForce := strings.TrimSpace(ruleStringValue(values["domain_force"], currentFieldString(current, "domain_force")))
	if _, err := parseSysRuleDomain(domainForce); err != nil {
		return err
	}
	return nil
}

func normalizeSysRuleGroups(values orm.RecordMap) ([]string, bool, error) {
	// Walk each string{"group bids", "group ids", "groups" entry in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"group_bids", "group_ids", "groups"} {
		raw, ok := values[key]
		if !ok {
			continue
		}
		groups, err := sysRuleGroupBIDs(raw)
		if err != nil {
			return nil, false, err
		}
		delete(values, "group_ids")
		delete(values, "groups")
		return groups, true, nil
	}
	return nil, false, nil
}

func sysRuleGroupBIDs(raw any) ([]string, error) {
	switch typed := raw.(type) {
	case nil:
		return []string{}, nil
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return []string{}, nil
		}
		var decoded []string
		if json.Unmarshal([]byte(trimmed), &decoded) == nil {
			return normalizeSysRuleGroupBIDs(decoded), nil
		}
		return normalizeSysRuleGroupBIDs([]string{trimmed}), nil
	case []string:
		return normalizeSysRuleGroupBIDs(typed), nil
	case []any:
		groups := make([]string, 0, len(typed))
		for _, item := range typed {
			if group, ok := stringFromAny(item); ok {
				groups = append(groups, group)
			}
		}
		return normalizeSysRuleGroupBIDs(groups), nil
	default:
		log.Errorf("sys.rule unsupported group_bids payload %T", raw)
		return nil, fmt.Errorf("sys.rule unsupported group_bids payload %T", raw)
	}
}

func normalizeSysRuleGroupBIDs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

// lookupSysModelNameByID looks up the sys model name by id handled by this package.
func lookupSysModelNameByID(ctx context.Context, id int64) (string, error) {
	db := metadataChangeDB(ctx)
	if db == nil || id <= 0 {
		log.Errorf("sys.rule invalid model_id %d", id)
		return "", fmt.Errorf("sys.rule invalid model_id %d", id)
	}
	var name string
	if err := db.NewSelect().
		Model((*SysModelRecord)(nil)).
		Column("name").
		Where("id = ?", id).
		Limit(1).
		Scan(ctx, &name); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			log.Errorf("sys.rule unknown model_id %d", id)
			return "", fmt.Errorf("sys.rule unknown model_id %d", id)
		}
		return "", err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		log.Errorf("sys.rule unknown model_id %d", id)
		return "", fmt.Errorf("sys.rule unknown model_id %d", id)
	}
	return name, nil
}

func sysRuleStateFromRecord(record orm.RecordMap) sysRuleState {
	id, _ := int64FromAny(record["id"])
	return sysRuleState{
		ID:          id,
		ModelName:   strings.TrimSpace(stringValue(record["model_name"])),
		DomainForce: strings.TrimSpace(stringValue(record["domain_force"])),
		GlobalRule:  boolValue(record["global_rule"]),
		PermRead:    boolValue(record["perm_read"]),
		PermWrite:   boolValue(record["perm_write"]),
		PermCreate:  boolValue(record["perm_create"]),
		PermUnlink:  boolValue(record["perm_unlink"]),
		GroupBIDs:   normalizeSysRuleGroupBIDs(ruleStringSlice(record["group_bids"])),
	}
}

func currentFieldString(current *sysRuleState, field string) string {
	if current == nil {
		return ""
	}
	switch field {
	case "model_name":
		return current.ModelName
	case "domain_force":
		return current.DomainForce
	default:
		return ""
	}
}

func ruleStringValue(raw any, fallback string) string {
	if raw == nil {
		return fallback
	}
	return stringValue(raw)
}

func ruleStringSlice(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if value, ok := stringFromAny(item); ok {
				out = append(out, value)
			}
		}
		return out
	default:
		return nil
	}
}

func sysRulePermissions(values orm.RecordMap, current *sysRuleState) SysRuleRecord {
	perms := SysRuleRecord{
		PermRead:   true,
		PermWrite:  true,
		PermCreate: true,
		PermUnlink: true,
	}
	if current != nil {
		perms.PermRead = current.PermRead
		perms.PermWrite = current.PermWrite
		perms.PermCreate = current.PermCreate
		perms.PermUnlink = current.PermUnlink
	}
	for key, ptr := range map[string]*bool{
		"perm_read":   &perms.PermRead,
		"perm_write":  &perms.PermWrite,
		"perm_create": &perms.PermCreate,
		"perm_unlink": &perms.PermUnlink,
	} {
		if _, ok := values[key]; ok {
			*ptr = boolValue(values[key])
		}
	}
	return perms
}

type sysRuleDBRow struct {
	ID          int64        `bun:"id"`
	ModelName   string       `bun:"model_name"`
	DomainForce string       `bun:"domain_force"`
	GlobalRule  bool         `bun:"global_rule"`
	PermRead    bool         `bun:"perm_read"`
	PermWrite   bool         `bun:"perm_write"`
	PermCreate  bool         `bun:"perm_create"`
	PermUnlink  bool         `bun:"perm_unlink"`
	GroupBIDs   []string     `bun:"group_bids"`
	Active      sql.NullBool `bun:"active"`
}

// defaultSysRuleDomain selects the sys rule domain handled by this package.
func defaultSysRuleDomain(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string) (orm.DomainNode, error) {
	if env == nil || model == nil {
		return orm.TrueDomain(), nil
	}
	db := metadataChangeDB(ctx)
	if db == nil {
		return orm.TrueDomain(), nil
	}

	rows := make([]sysRuleDBRow, 0)
	if err := db.NewSelect().
		Model((*SysRuleRecord)(nil)).
		// Bind the explicit sys_rule storage table: SysRuleRecord carries no bun
		// table tag, so raw Bun inference drifts to "sys_rule_records" and every
		// record rule would silently stop loading (same failure class as the
		// tbl_lang storage-binding rule in backend context).
		ModelTableExpr("sys_rule AS sys_rule_record").
		Column("id", "model_name", "domain_force", "global_rule", "perm_read", "perm_write", "perm_create", "perm_unlink", "group_bids", "active").
		Where("model_name = ?", model.Name).
		Where("active = ?", true).
		OrderExpr("id ASC").
		Scan(ctx, &rows); err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "does not exist") {
			return orm.TrueDomain(), nil
		}
		return orm.DomainNode{}, err
	}
	if len(rows) == 0 {
		return orm.TrueDomain(), nil
	}

	// Match against every group the caller holds, implied groups included, so a role
	// that inherits base.group_user is restricted by base.group_user rules.
	callerBIDs, err := orm.CallerGroupBIDs(ctx)
	if err != nil {
		return orm.DomainNode{}, err
	}
	return sysRuleDomainFor(rows, callerBIDs, operation)
}

// sysRuleDomainFor combines the active rules of one model for one operation:
// global rules (and group rules without groups) are ANDed; group rules matching
// the caller are ORed and ANDed with the globals. When group rules exist for the
// operation but none matches the caller, access is denied (false domain): the
// prefix baseline grants every internal user CRUD on tbl./base. models, so
// treating "no matching group rule" as unrestricted would let any user past a
// rule written only for, say, base.group_system.
func sysRuleDomainFor(rows []sysRuleDBRow, callerBIDs []string, operation string) (orm.DomainNode, error) {
	globalDomains := make([]orm.DomainNode, 0)
	groupDomains := make([]orm.DomainNode, 0)
	hasGroupRules := false
	for _, row := range rows {
		if !sysRuleRowAllows(row, operation) {
			continue
		}
		domain, err := parseSysRuleDomain(row.DomainForce)
		if err != nil {
			log.Errorf("sys.rule %d invalid domain_force: %v", row.ID, err)
			return orm.DomainNode{}, fmt.Errorf("sys.rule %d invalid domain_force: %w", row.ID, err)
		}
		groups := normalizeSysRuleGroupBIDs(row.GroupBIDs)
		if row.GlobalRule || len(groups) == 0 {
			globalDomains = append(globalDomains, domain)
			continue
		}
		hasGroupRules = true
		if sysRuleMatchesAnyGroup(groups, callerBIDs) {
			groupDomains = append(groupDomains, domain)
		}
	}

	if hasGroupRules && len(groupDomains) == 0 {
		return orm.FalseDomain(), nil
	}
	result := orm.TrueDomain()
	if len(globalDomains) > 0 {
		result = andSysRuleDomains(globalDomains)
	}
	if len(groupDomains) == 0 {
		return result, nil
	}
	groupDomain := orSysRuleDomains(groupDomains)
	if result.Kind == "" || result.Kind == orm.DomainKindTrue {
		return groupDomain, nil
	}
	return orm.And(result, groupDomain), nil
}

func sysRuleMatchesAnyGroup(ruleGroups, callerBIDs []string) bool {
	for _, group := range ruleGroups {
		for _, bid := range callerBIDs {
			if strings.EqualFold(strings.TrimSpace(group), strings.TrimSpace(bid)) {
				return true
			}
		}
	}
	return false
}

func andSysRuleDomains(domains []orm.DomainNode) orm.DomainNode {
	switch len(domains) {
	case 0:
		return orm.TrueDomain()
	case 1:
		return domains[0]
	default:
		return orm.And(domains...)
	}
}

func orSysRuleDomains(domains []orm.DomainNode) orm.DomainNode {
	switch len(domains) {
	case 0:
		return orm.TrueDomain()
	case 1:
		return domains[0]
	default:
		return orm.Or(domains...)
	}
}

func sysRuleRowAllows(row sysRuleDBRow, operation string) bool {
	switch strings.TrimSpace(strings.ToLower(operation)) {
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

// parseSysRuleDomain parses the sys rule domain handled by this package.
func parseSysRuleDomain(raw string) (orm.DomainNode, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return orm.TrueDomain(), nil
	}
	switch compact := strings.ReplaceAll(strings.ReplaceAll(trimmed, " ", ""), "'", "\""); compact {
	case "[(1,\"=\",1)]", "[[1,\"=\",1]]":
		return orm.TrueDomain(), nil
	case "[(0,\"=\",1)]", "[[0,\"=\",1]]":
		return orm.FalseDomain(), nil
	}

	normalized := strings.NewReplacer(
		"(", "[",
		")", "]",
		"'", "\"",
		" True", " true",
		":True", ":true",
		"[True", "[true",
		",True", ",true",
		" False", " false",
		":False", ":false",
		"[False", "[false",
		",False", ",false",
		" None", " null",
		":None", ":null",
		"[None", "[null",
		",None", ",null",
	).Replace(trimmed)

	var payload any
	if err := json.Unmarshal([]byte(normalized), &payload); err != nil {
		log.Errorf("unsupported domain syntax %q", raw)
		return orm.DomainNode{}, fmt.Errorf("unsupported domain syntax %q", raw)
	}
	return orm.ParseDomain(payload)
}
