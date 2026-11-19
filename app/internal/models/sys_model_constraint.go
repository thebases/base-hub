package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys.model.constraint —
//   the admin-declared catalog of PostgreSQL-level (check/unique/unique_index) and business-rule constraints
//   applied against other models. Unlike sys.model.field/sys.model.relation/sys.model.operation, rows here are
//   never registry-synced from Go code; every row is created through the runtime API (or a future module seed).
//   bid is always server-generated (via the shared GenerateBID, see bid.go) on create, never typed by the caller — so
//   every row unconditionally satisfies the custom-row/editable convention from sys_model.go/sys_model_field.go
//   without the admin having to think about it. See .agents/memory/context/backend-context.md for the
//   SRS-F-003 polarity note.
// - Key symbols: `init`, `SysModelConstraintRecord`, `createSysModelConstraint`, `ReconcileModelConstraint`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay
//   centralized instead of being scattered across handlers.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/uptrace/bun"
)

// SysModelConstraintRecord stores one admin-declared constraint targeting
// another model: either a live PostgreSQL object (check/unique/unique_index,
// reconciled by ReconcileModelConstraint) or a data-driven business rule
// (evaluated in Go via orm.RegisterConstraint, no DDL involved).
type SysModelConstraintRecord struct {
	bun.BaseModel `bun:"table:sys_model_constraint,alias:sys_model_constraint"`
	orm.MetadataBaseModel
	BID            string    `bun:"bid" json:"bid" meta:"index,label:BID,help:Stores the universal global unique record identifier for this record."`
	Name           string    `bun:"name" json:"name" meta:"index,unique,label:Name,help:Stores the unique identifier used to derive the live PostgreSQL object name for this constraint."`
	ModelName      string    `bun:"model_name" json:"model_name" meta:"index,label:Model Name,help:Stores the model this constraint applies to."`
	ConstraintType string    `bun:"constraint_type" json:"constraint_type" meta:"index,label:Constraint Type,help:Selects whether this row reconciles a live PostgreSQL object or evaluates a business rule in Go." selection:"(check,Check),(unique,Unique),(unique_index,Unique Index),(business_rule,Business Rule)"`
	SQLDefinition  string    `bun:"sql_definition,nullzero" json:"sql_definition" meta:"kind:text,label:SQL Definition,help:Stores the CHECK expression, UNIQUE column list, or index column/expression spec for check/unique/unique_index rows."`
	InvalidDomain  []any     `bun:"invalid_domain,type:jsonb" json:"invalid_domain" meta:"kind:json,label:Invalid Domain,help:Stores the domain that identifies invalid records for business_rule rows."`
	TriggerFields  []string  `bun:"trigger_fields,type:jsonb" json:"trigger_fields" meta:"kind:json,label:Trigger Fields,help:Lists the fields that trigger business_rule evaluation on write."`
	Message        string    `bun:"message" json:"message" meta:"translate,label:Message,help:Stores the translated violation message shown when a business_rule row rejects a write; en is required."`
	Description    string    `bun:"description,nullzero" json:"description" meta:"kind:text,label:Description,help:Stores the descriptive notes for this record."`
	Module         string    `bun:"module,nullzero" json:"module" meta:"label:Module,help:Stores the module that seeded this constraint, if any."`
	State          string    `bun:"state,default:'draft'" json:"state" meta:"index,label:State,help:Tracks whether this constraint is unreconciled, live, disabled, or failed its last apply attempt." selection:"(draft,Draft),(active,Active),(error,Error),(disabled,Disabled)"`
	LastError      string    `bun:"last_error,nullzero" json:"last_error" meta:"label:Last Error,help:Stores the PostgreSQL diagnostic from the most recent failed apply attempt."`
	LastAppliedAt  time.Time `bun:"last_applied_at,nullzero" json:"last_applied_at" meta:"readonly,label:Last Applied At,help:Stores when this constraint was last successfully reconciled against PostgreSQL."`
}

const SysModelConstraintModelName = "sys.model.constraint"

// SysModelConstraintModel ranks 70: after sys.model.operation (65) since ReconcileModelConstraints
// (boot-time reconciliation) needs the full model registry populated first.
var SysModelConstraintModel = orm.Define[SysModelConstraintRecord](&Models, 70, orm.ModelSpec{
	Name:        SysModelConstraintModelName,
	Label:       "Model Constraints",
	Description: "Admin-declared catalog of check/unique/unique_index PostgreSQL constraints and data-driven business rules, base.group_system only",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModelConstraint},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModelConstraint},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModelConstraint},
		{Name: "action_apply", Public: true, UsesPathID: true, Call: actionApplySysModelConstraint},
		{Name: "action_enable", Public: true, UsesPathID: true, Call: actionEnableSysModelConstraint},
		{Name: "action_disable", Public: true, UsesPathID: true, Call: actionDisableSysModelConstraint},
	},
})

const (
	sysModelConstraintTypeCheck        = "check"
	sysModelConstraintTypeUnique       = "unique"
	sysModelConstraintTypeUniqueIndex  = "unique_index"
	sysModelConstraintTypeBusinessRule = "business_rule"
)

// createSysModelConstraint validates and creates one or more sys.model.constraint
// rows. Rows here are never registry-synced (unlike sys.model.field/relation),
// so — mirroring the custom-row create requirement in sys_model.go/sys_model_field.go —
// every row must carry a non-blank bid to be treated as editable; unlike those
// models, the caller never types it: it is always server-generated via the
// shared GenerateBID (see bid.go) so the admin form only has to fill in the
// actual constraint fields (model, type, SQL/domain, message).
func createSysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		return nil, fmt.Errorf("%s.create requires values", SysModelConstraintModelName)
	}
	requestedActiveByName := make(map[string]bool, len(valuesList))
	for _, values := range valuesList {
		if err := validateSysModelConstraintValues(values); err != nil {
			return nil, err
		}
		if err := ensureRawConstraintSQLAllowed(ctx, stringValue(values["constraint_type"])); err != nil {
			return nil, err
		}
		// Ignore any caller-supplied bid — always server-generated so the
		// row is unconditionally treated as custom/editable (see file header).
		values["bid"] = GenerateBID(stringValue(values["model_name"]))
		// A caller may explicitly request state="active" on create, but that only
		// takes effect if the synchronous apply below succeeds; otherwise force draft.
		requestedActiveByName[strings.TrimSpace(stringValue(values["name"]))] = strings.EqualFold(stringValue(values["state"]), "active")
		values["state"] = "draft"
	}

	created, err := createLoggedMetadataRecords(ctx, SysModelConstraintModelName, valuesListToArgs(valuesList), kwargs)
	if err != nil {
		return nil, err
	}

	for _, row := range createdConstraintRecordMaps(created) {
		if !requestedActiveByName[strings.TrimSpace(stringValue(row["name"]))] {
			continue
		}
		if err := applyConstraintRowSynchronously(ctx, row); err != nil {
			log.Errorf("%s.create: synchronous apply for %q failed, leaving draft: %v", SysModelConstraintModelName, row["name"], err)
		}
	}
	return created, nil
}

// applyConstraintRowSynchronously runs the apply/registration pipeline right
// after create when the caller explicitly requested state="active". On
// failure the row stays draft (create-time behavior differs from
// action_apply, which flips a normal apply failure to state=error).
func applyConstraintRowSynchronously(ctx context.Context, row orm.RecordMap) error {
	id := strings.TrimSpace(stringValue(row["id"]))
	if id == "" {
		return nil
	}
	// ReconcileModelConstraint refreshes the business-rule cache itself once the
	// row is active; refreshing here with the created (draft) row would evict it.
	return ReconcileModelConstraint(ctx, sysModelConstraintDB(ctx), sysModelConstraintDBName(ctx), row)
}

// valuesListToArgs re-wraps a normalized values list back into the args shape
// createLoggedMetadataRecords/recordMapListFromArgs expect.
func valuesListToArgs(valuesList []orm.RecordMap) []any {
	if len(valuesList) == 1 {
		return []any{valuesList[0]}
	}
	args := make([]any, len(valuesList))
	for i, v := range valuesList {
		args[i] = v
	}
	return []any{args}
}

// createdConstraintRecordMaps normalizes the return shape of
// createLoggedMetadataRecords (single record vs slice) into a flat slice.
func createdConstraintRecordMaps(created any) []orm.RecordMap {
	switch typed := created.(type) {
	case orm.RecordMap:
		return []orm.RecordMap{typed}
	case []orm.RecordMap:
		return typed
	default:
		return nil
	}
}

// validateSysModelConstraintValues runs the create-time validation gate
// (SRS-F-002): resolves model_name against the live registry and enforces
// the per-constraint_type shape before any row is persisted.
func validateSysModelConstraintValues(values orm.RecordMap) error {
	modelName := strings.TrimSpace(stringValue(values["model_name"]))
	if modelName == "" {
		return fmt.Errorf("%s.model_name is required", SysModelConstraintModelName)
	}
	meta, ok := orm.LookupModel(modelName)
	if !ok || meta == nil {
		return fmt.Errorf("unknown_model: %q is not a registered model", modelName)
	}

	constraintType := strings.TrimSpace(stringValue(values["constraint_type"]))
	switch constraintType {
	case sysModelConstraintTypeCheck, sysModelConstraintTypeUnique, sysModelConstraintTypeUniqueIndex:
		sqlDefinition := strings.TrimSpace(stringValue(values["sql_definition"]))
		if sqlDefinition == "" {
			return fmt.Errorf("sql_definition is required for constraint_type %q", constraintType)
		}
		if strings.Contains(sqlDefinition, ";") {
			return fmt.Errorf("sql_definition must not contain statement separators (\";\")")
		}
		if constraintType == sysModelConstraintTypeUnique {
			columns, err := parseUniqueConstraintColumns(sqlDefinition)
			if err != nil {
				return err
			}
			for _, column := range columns {
				if field, err := meta.Field(column); err != nil || !field.Stored {
					return orm.InvalidParamsf("sql_definition: %q is not a stored field of %s", column, modelName)
				}
			}
		}
	case sysModelConstraintTypeBusinessRule:
		domainRaw := values["invalid_domain"]
		if isEmptyDomainValue(domainRaw) {
			return fmt.Errorf("invalid_domain is required for constraint_type %q", constraintType)
		}
		if _, err := orm.ParseDomain(domainRaw); err != nil {
			return fmt.Errorf("invalid_domain: %w", err)
		}
		triggerFields := stringSliceValue(values["trigger_fields"])
		if len(triggerFields) == 0 {
			return fmt.Errorf("trigger_fields is required for constraint_type %q", constraintType)
		}
		for _, field := range triggerFields {
			if !modelHasField(meta, field) {
				return fmt.Errorf("trigger_fields: %q is not a field on model %q", field, modelName)
			}
		}
	default:
		return fmt.Errorf("constraint_type must be one of check, unique, unique_index, business_rule, got %q", constraintType)
	}

	message := values["message"]
	if !translatedMessageHasEnglish(message) {
		return fmt.Errorf("message.en is required")
	}
	return nil
}

// isEmptyDomainValue reports whether a raw incoming domain payload is absent
// or an empty container ([]/{} / "").
func isEmptyDomainValue(raw any) bool {
	switch typed := raw.(type) {
	case nil:
		return true
	case []any:
		return len(typed) == 0
	case string:
		return strings.TrimSpace(typed) == "" || strings.TrimSpace(typed) == "[]"
	default:
		return false
	}
}

// stringSliceValue coerces a jsonb []any (the common shape for RPC-decoded
// arrays) or []string into a plain []string.
func stringSliceValue(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

// modelHasField reports whether name is a real field on the resolved model.
func modelHasField(meta *orm.ModelMetadata, name string) bool {
	if meta == nil {
		return false
	}
	for _, field := range meta.Fields {
		if field.Name == name {
			return true
		}
	}
	return false
}

// translatedMessageHasEnglish validates the Go-side "en required" rule for
// the translated message field (SRS 5.2) — there is no DB-level enforcement
// for translated jsonb columns, so this must run before persistence.
func translatedMessageHasEnglish(raw any) bool {
	switch typed := raw.(type) {
	case map[string]any:
		return strings.TrimSpace(stringValue(typed["en"])) != ""
	case string:
		return strings.TrimSpace(typed) != ""
	default:
		return false
	}
}

// writeSysModelConstraint handles write on sys.model.constraint. Guarded the
// same way as sys.model.field custom rows (SRS-F-003, corrected polarity).
func writeSysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if err := guardSysModelConstraintRowsCustom(ctx, ids, "edited"); err != nil {
		return nil, err
	}
	if err := validateSysModelConstraintWrite(ctx, ids, args, kwargs); err != nil {
		return nil, err
	}
	return writeLoggedMetadataRecords(ctx, SysModelConstraintModelName, ids, args, kwargs)
}

// sysModelConstraintShapeFields are the columns that decide what DDL a row produces.
var sysModelConstraintShapeFields = []string{"model_name", "constraint_type", "sql_definition", "invalid_domain"}

// sysModelConstraintValidatedFields are all the columns validateSysModelConstraintValues reads.
var sysModelConstraintValidatedFields = append([]string{"message", "trigger_fields"}, sysModelConstraintShapeFields...)

// validateSysModelConstraintWrite applies the create-time gate to a write that changes a row's
// shape (plan T21): each target row merged with the new values must still validate, and raw SQL
// (check/unique_index) may only be set by a sudo caller. Writes to message/name/state pass as before.
func validateSysModelConstraintWrite(ctx context.Context, ids []string, args []any, kwargs map[string]any) error {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil || values == nil {
		return err
	}
	touchesShape := false
	for _, key := range sysModelConstraintShapeFields {
		if _, ok := values[key]; ok {
			touchesShape = true
			break
		}
	}
	if !touchesShape {
		return nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, nil)
	if err != nil {
		return err
	}
	for _, row := range rows {
		// Every field the validator reads, current row value overridden by the write.
		merged := orm.RecordMap{}
		for _, key := range sysModelConstraintValidatedFields {
			merged[key] = row[key]
			if value, ok := values[key]; ok {
				merged[key] = value
			}
		}
		if err := validateSysModelConstraintValues(merged); err != nil {
			return err
		}
		if err := ensureRawConstraintSQLAllowed(ctx, stringValue(merged["constraint_type"])); err != nil {
			return err
		}
	}
	return nil
}

// ensureRawConstraintSQLAllowed restricts authoring of raw-SQL constraint types to sudo (plan
// T21). A CHECK or unique-index expression is pasted into DDL and runs inside every insert/update
// on the table, where it can call any database function the server role may call, so
// base.group_system must not be able to set it. unique (column list) and business_rule (domain)
// are compiled safely and stay open to the model ACL. Internal callers without an RPC identity
// (seeds, boot) are not restricted.
func ensureRawConstraintSQLAllowed(ctx context.Context, constraintType string) error {
	switch strings.TrimSpace(constraintType) {
	case sysModelConstraintTypeCheck, sysModelConstraintTypeUniqueIndex:
	default:
		return nil
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.IsSudo() {
		return nil
	}
	if _, rpc := env.ContextValue(orm.RBACContextAuthModeKey); !rpc {
		return nil
	}
	return orm.Forbiddenf("permission denied: only a superadmin can set raw SQL for %s constraints", strings.TrimSpace(constraintType))
}

// uniqueConstraintColumnPattern bounds a unique constraint's column names to plain identifiers.
var uniqueConstraintColumnPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseUniqueConstraintColumns reads a unique constraint's sql_definition as a column list
// ("a, b" or "(a, b)"). Anything else (expressions, quotes, keywords) is refused, so the DDL can be
// built from bun.Ident values instead of the raw text (plan T21).
func parseUniqueConstraintColumns(definition string) ([]string, error) {
	text := strings.TrimSpace(definition)
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		text = strings.TrimSpace(text[1 : len(text)-1])
	}
	parts := strings.Split(text, ",")
	columns := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		column := strings.TrimSpace(part)
		if !uniqueConstraintColumnPattern.MatchString(column) {
			return nil, orm.InvalidParamsf("sql_definition for a unique constraint must be a comma-separated list of field names, got %q", definition)
		}
		if _, dup := seen[column]; dup {
			return nil, orm.InvalidParamsf("sql_definition repeats field %q", column)
		}
		seen[column] = struct{}{}
		columns = append(columns, column)
	}
	return columns, nil
}

// unlinkSysModelConstraint handles unlink (SRS-F-008): guard, then best-effort
// drop the live PostgreSQL object (if any), remove from the business-rule
// cache (if applicable), then delete the row via the shared logging helper.
func unlinkSysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if err := guardSysModelConstraintRowsCustom(ctx, ids, "deleted"); err != nil {
		return nil, err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	db := sysModelConstraintDB(ctx)
	dbName := sysModelConstraintDBName(ctx)
	for _, row := range rows {
		if err := dropLiveConstraintObjectBestEffort(ctx, db, dbName, row); err != nil {
			log.Errorf("%s.unlink: best-effort drop of live object for row %v failed: %v", SysModelConstraintModelName, row["id"], err)
		}
	}
	result, err := unlinkLoggedMetadataRecords(ctx, SysModelConstraintModelName, ids)
	if err != nil {
		return result, err
	}
	// Drop cached rules only once the delete commits (a failed or rolled-back
	// unlink must keep enforcing them).
	orm.AfterCommit(ctx, func(context.Context) {
		for _, row := range rows {
			removeBusinessRuleCacheRow(dbName, row)
		}
		invalidateBusinessRuleSync(dbName)
	})
	return result, nil
}

// guardSysModelConstraintRowsCustom mirrors guardSysModelFieldRowsCustom: a
// row with a blank bid is treated as protected/read-only.
func guardSysModelConstraintRowsCustom(ctx context.Context, ids []string, verb string) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, []string{"id", "bid"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return fmt.Errorf("%s rows without a bid are read-only; only rows with a bid can be %s", SysModelConstraintModelName, verb)
		}
	}
	return nil
}

func sysModelConstraintDB(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	return orm.GetBunDB()
}

// sysModelConstraintDBName resolves the connected database name for the
// constraint-message cache key (see constraintMessageCache below). Boot-time
// reconciliation has no request Environment and passes its own dbName
// explicitly instead of calling this.
func sysModelConstraintDBName(ctx context.Context) string {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return env.DBName()
	}
	return ""
}

// ---- Constraint-violation message translation (runtime substitution) ----
//
// Keyed by "<database_name>|<live object name>" — objectName alone is not
// safe across tenant databases (each data-plane DB reconciles its own
// sys.model.constraint rows and PostgreSQL constraint namespaces are
// per-database, so the same object name in two DBs can carry different
// messages).

var constraintMessageCache = struct {
	mu    sync.RWMutex
	byKey map[string]string
}{byKey: map[string]string{}}

func constraintMessageCacheKey(dbName, objectName string) string {
	return dbName + "|" + objectName
}

// setConstraintMessage registers the friendly message shown in place of the
// raw PostgreSQL error whenever objectName rejects a write. Called after a
// successful apply (ReconcileModelConstraint) so the cache always reflects
// what is actually live.
func setConstraintMessage(dbName, objectName, message string) {
	if objectName == "" || message == "" {
		return
	}
	constraintMessageCache.mu.Lock()
	defer constraintMessageCache.mu.Unlock()
	constraintMessageCache.byKey[constraintMessageCacheKey(dbName, objectName)] = message
}

// clearConstraintMessage removes a mapping once the live object is dropped
// (unlink/disable) so a stale message never survives its constraint.
func clearConstraintMessage(dbName, objectName string) {
	if objectName == "" {
		return
	}
	constraintMessageCache.mu.Lock()
	defer constraintMessageCache.mu.Unlock()
	delete(constraintMessageCache.byKey, constraintMessageCacheKey(dbName, objectName))
}

func init() {
	orm.RegisterConstraintViolationTranslator(translateSysModelConstraintViolation)
}

// translateSysModelConstraintViolation is the one translator registered for
// the whole process (orm.RuntimeRepositoryCreate/Write calls it on every
// write error). It only replaces the error when PostgreSQL's structured
// diagnostic names a constraint/index this model reconciled itself.
func translateSysModelConstraintViolation(ctx context.Context, err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.ConstraintName == "" {
		return "", false
	}
	dbName := ""
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		dbName = env.DBName()
	}
	constraintMessageCache.mu.RLock()
	message, ok := constraintMessageCache.byKey[constraintMessageCacheKey(dbName, pgErr.ConstraintName)]
	constraintMessageCache.mu.RUnlock()
	return message, ok
}

// ---- Business-rule cache (SRS-F-006) ----

// businessRuleCacheKey scopes rules per tenant DB, like constraintMessageCacheKey:
// each data-plane DB holds its own sys.model.constraint rows for the same model.
func businessRuleCacheKey(dbName, modelName string) string {
	return dbName + "|" + modelName
}

var businessRuleCache = struct {
	mu    sync.RWMutex
	byRow map[string][]SysModelConstraintRecord // keyed by businessRuleCacheKey(dbName, model_name)
	// registeredModels tracks which target models already have the generic
	// business-rule ConstraintHandler registered; orm.RegisterConstraint has
	// no Unregister, so this only ever grows.
	registeredModels map[string]bool
}{
	byRow:            map[string][]SysModelConstraintRecord{},
	registeredModels: map[string]bool{},
}

// ensureBusinessRuleHandlerRegistered lazily registers the one generic
// ConstraintHandler for modelName the first time a business_rule row targets
// it. Registered with fields=nil (always invoked) because orm.RegisterConstraint
// has no Unregister and the actual trigger-field set changes as rows are
// added/disabled over time — a static registration-time field list would go
// stale in either direction. The short-circuit required by SRS-F-006
// ("no evaluation cost on unrelated writes") instead happens inside the
// handler body via a cheap map/slice intersection before any domain eval.
func ensureBusinessRuleHandlerRegistered(modelName string) {
	businessRuleCache.mu.Lock()
	defer businessRuleCache.mu.Unlock()
	if businessRuleCache.registeredModels[modelName] {
		return
	}
	businessRuleCache.registeredModels[modelName] = true
	orm.RegisterConstraint(modelName, nil, businessRuleConstraintHandler)
}

// businessRuleConstraintHandler is the one generic handler shared by every
// model that has at least one active business_rule constraint. Read-only: no
// writes/commits happen here (SRS 4.3/11.3).
func businessRuleConstraintHandler(ctx context.Context, records orm.RecordSet, changedValues orm.RecordMap) error {
	dbName := sysModelConstraintDBName(ctx)
	syncBusinessRuleCache(ctx, dbName)
	businessRuleCache.mu.RLock()
	rows := append([]SysModelConstraintRecord(nil), businessRuleCache.byRow[businessRuleCacheKey(dbName, records.ModelName())]...)
	businessRuleCache.mu.RUnlock()
	if len(rows) == 0 {
		return nil
	}

	changedFields := make(map[string]struct{}, len(changedValues))
	for key := range changedValues {
		changedFields[key] = struct{}{}
	}

	for _, row := range rows {
		if !anyTriggerFieldChanged(row.TriggerFields, changedFields) {
			continue
		}
		domain, err := orm.ParseDomain(row.InvalidDomain)
		if err != nil {
			log.Errorf("%s %q: invalid_domain failed to parse: %v", SysModelConstraintModelName, row.Name, err)
			continue
		}
		affected, err := recordsMatchingInvalidDomain(ctx, records, domain)
		if err != nil {
			return err
		}
		if len(affected) > 0 {
			return fmt.Errorf("business_rule_violation: %s", resolvedConstraintMessage(row))
		}
	}
	return nil
}

// businessRuleSyncInterval bounds how often one process re-checks the shared
// sys_model_constraint table for business-rule changes made by another
// instance (or raw SQL): a rule changed elsewhere is enforced here within this
// interval, while the write hot path pays one fingerprint query per DB at most
// this often instead of on every write.
const businessRuleSyncInterval = 5 * time.Second

type businessRuleSyncState struct {
	checkedAt   time.Time
	fingerprint string
}

// businessRuleSync holds the last fingerprint per tenant DB. The zero
// fingerprint never matches a real one, so the first check after boot always
// reloads once.
var businessRuleSync = struct {
	mu   sync.Mutex
	byDB map[string]businessRuleSyncState
}{byDB: map[string]businessRuleSyncState{}}

// syncBusinessRuleCache revalidates the per-process cache for dbName against a
// fingerprint (count + max(updated_at) of business_rule rows, any state: a
// disable bumps updated_at, a delete drops the count), the same approach as
// ui_menu_cache.go. On a mismatch the DB's whole rule set is reloaded. Errors
// keep the current cache (fail open to the last known rules) and are logged.
func syncBusinessRuleCache(ctx context.Context, dbName string) {
	// Query on a non-transactional handle: this runs from inside the caller's
	// write tx, where a failed SELECT would abort that tx (25P02) and a reload
	// would read the tx's uncommitted rule rows.
	db := businessRuleSyncDB(ctx, dbName)
	if db == nil {
		return
	}
	now := time.Now()
	businessRuleSync.mu.Lock()
	state := businessRuleSync.byDB[dbName]
	if now.Sub(state.checkedAt) < businessRuleSyncInterval {
		businessRuleSync.mu.Unlock()
		return
	}
	// Claim this interval before the query so concurrent writers skip the
	// check instead of all querying at once; the lock is not held during I/O.
	state.checkedAt = now
	businessRuleSync.byDB[dbName] = state
	businessRuleSync.mu.Unlock()

	var count int64
	var maxUpdated string
	if err := db.NewSelect().
		Model((*SysModelConstraintRecord)(nil)).
		ColumnExpr("count(*)").
		ColumnExpr("coalesce(max(updated_at)::text, '')").
		Where("constraint_type = ?", sysModelConstraintTypeBusinessRule).
		Scan(ctx, &count, &maxUpdated); err != nil {
		log.Errorf("%s: business-rule cache fingerprint for %q: %v", SysModelConstraintModelName, dbName, err)
		return
	}
	fingerprint := fmt.Sprintf("%d@%s", count, maxUpdated)
	if fingerprint == state.fingerprint {
		return
	}
	// The fingerprint was taken before the reload, so a change committed
	// mid-reload leaves it stale and the next check reloads again.
	if err := reloadBusinessRuleCache(ctx, db, dbName); err != nil {
		log.Errorf("%s: business-rule cache reload for %q: %v", SysModelConstraintModelName, dbName, err)
		return
	}
	businessRuleSync.mu.Lock()
	businessRuleSync.byDB[dbName] = businessRuleSyncState{checkedAt: now, fingerprint: fingerprint}
	businessRuleSync.mu.Unlock()
}

// businessRuleSyncDB is the pool handle for dbName: the environment's DB when it
// is a plain *bun.DB, else the registered pool of dbName (the default pool for
// an unnamed environment); nil when none is available. Never a tenant fallback.
func businessRuleSyncDB(ctx context.Context, dbName string) *bun.DB {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		if db, ok := env.DB().(*bun.DB); ok && db != nil {
			return db
		}
	}
	if strings.TrimSpace(dbName) == "" {
		// An environment without a DB name runs on the default database.
		return orm.GetBunDB()
	}
	if registered := orm.ResolveRegisteredDatabase(dbName); registered != nil {
		return registered.DB
	}
	return nil
}

// invalidateBusinessRuleSync forgets dbName's stored fingerprint so the next
// rule-handler call re-checks and reloads. Used after an in-process cache edit,
// which a later rollback could otherwise leave in place behind a matching
// fingerprint.
func invalidateBusinessRuleSync(dbName string) {
	businessRuleSync.mu.Lock()
	delete(businessRuleSync.byDB, dbName)
	businessRuleSync.mu.Unlock()
}

// reloadBusinessRuleCache replaces every cached rule of dbName with the active
// business_rule rows in the DB, and registers the handler for any target model
// that has none yet in this process.
func reloadBusinessRuleCache(ctx context.Context, db bun.IDB, dbName string) error {
	var records []SysModelConstraintRecord
	if err := db.NewSelect().
		Model(&records).
		// message is a translated jsonb column; a shared cache cannot resolve
		// per-caller languages, so it keeps the required en text instead of the
		// raw JSON envelope.
		ExcludeColumn("message").
		ColumnExpr("sys_model_constraint.message->>'en' AS message").
		Where("constraint_type = ?", sysModelConstraintTypeBusinessRule).
		Where("state = ?", "active").
		OrderExpr("model_name ASC, name ASC").
		Scan(ctx); err != nil {
		return err
	}
	byModel := make(map[string][]SysModelConstraintRecord)
	for _, record := range records {
		byModel[record.ModelName] = append(byModel[record.ModelName], record)
	}
	prefix := businessRuleCacheKey(dbName, "")
	businessRuleCache.mu.Lock()
	for key := range businessRuleCache.byRow {
		if strings.HasPrefix(key, prefix) {
			delete(businessRuleCache.byRow, key)
		}
	}
	for modelName, rows := range byModel {
		businessRuleCache.byRow[businessRuleCacheKey(dbName, modelName)] = rows
	}
	businessRuleCache.mu.Unlock()
	for modelName := range byModel {
		ensureBusinessRuleHandlerRegistered(modelName)
	}
	return nil
}

// anyTriggerFieldChanged is the cheap short-circuit: no domain evaluation
// runs unless at least one of the row's declared trigger fields changed.
func anyTriggerFieldChanged(triggerFields []string, changedFields map[string]struct{}) bool {
	for _, field := range triggerFields {
		if _, ok := changedFields[field]; ok {
			return true
		}
	}
	return false
}

// recordsMatchingInvalidDomain evaluates the parsed invalid_domain against
// every record in the affected batch (never assumes a singleton).
func recordsMatchingInvalidDomain(ctx context.Context, records orm.RecordSet, domain orm.DomainNode) ([]string, error) {
	ids := records.IDs()
	if len(ids) == 0 {
		return nil, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(records.ModelName(), nil), ids, nil)
	if err != nil {
		return nil, err
	}
	matched := make([]string, 0)
	for _, row := range rows {
		ok, err := orm.EvaluateDomain(row, domain)
		if err != nil {
			return nil, err
		}
		if ok {
			matched = append(matched, metadataTargetRecordID(row))
		}
	}
	return matched, nil
}

// resolvedConstraintMessage picks the caller-facing violation message. The
// jsonb translated value normally resolves through the runtime read path
// (COALESCE(lang, en)); Message on the Go struct is read post-read so this is
// already the environment-resolved string.
func resolvedConstraintMessage(row SysModelConstraintRecord) string {
	if strings.TrimSpace(row.Message) != "" {
		return row.Message
	}
	return fmt.Sprintf("constraint %q violated", row.Name)
}

// refreshBusinessRuleCacheRow rebuilds the cached entry for one row's target
// model whenever a business_rule row is created/written/enabled/disabled.
func refreshBusinessRuleCacheRow(dbName string, row orm.RecordMap) {
	if strings.TrimSpace(stringValue(row["constraint_type"])) != sysModelConstraintTypeBusinessRule {
		return
	}
	modelName := strings.TrimSpace(stringValue(row["model_name"]))
	if modelName == "" {
		return
	}
	record := businessRuleRecordFromMap(row)
	businessRuleCache.mu.Lock()
	defer businessRuleCache.mu.Unlock()
	cacheKey := businessRuleCacheKey(dbName, modelName)
	existing := businessRuleCache.byRow[cacheKey]
	replaced := false
	for i, r := range existing {
		if r.ID == record.ID {
			if strings.EqualFold(record.State, "active") {
				existing[i] = record
			} else {
				existing = append(existing[:i], existing[i+1:]...)
			}
			replaced = true
			break
		}
	}
	if !replaced && strings.EqualFold(record.State, "active") {
		existing = append(existing, record)
	}
	businessRuleCache.byRow[cacheKey] = existing
}

// removeBusinessRuleCacheRow drops a row from the cache on unlink.
func removeBusinessRuleCacheRow(dbName string, row orm.RecordMap) {
	modelName := strings.TrimSpace(stringValue(row["model_name"]))
	id := strings.TrimSpace(stringValue(row["id"]))
	if modelName == "" || id == "" {
		return
	}
	businessRuleCache.mu.Lock()
	defer businessRuleCache.mu.Unlock()
	cacheKey := businessRuleCacheKey(dbName, modelName)
	existing := businessRuleCache.byRow[cacheKey]
	for i, r := range existing {
		if fmt.Sprintf("%d", r.ID) == id {
			businessRuleCache.byRow[cacheKey] = append(existing[:i], existing[i+1:]...)
			return
		}
	}
}

func businessRuleRecordFromMap(row orm.RecordMap) SysModelConstraintRecord {
	id, _ := int64FromAny(row["id"])
	return SysModelConstraintRecord{
		MetadataBaseModel: orm.MetadataBaseModel{ID: id},
		Name:              stringValue(row["name"]),
		ModelName:         stringValue(row["model_name"]),
		ConstraintType:    stringValue(row["constraint_type"]),
		InvalidDomain:     domainSliceValue(row["invalid_domain"]),
		TriggerFields:     stringSliceValue(row["trigger_fields"]),
		Message:           stringValue(row["message"]),
		State:             stringValue(row["state"]),
	}
}

func domainSliceValue(raw any) []any {
	if typed, ok := raw.([]any); ok {
		return typed
	}
	return nil
}

// actionEnableSysModelConstraint re-runs the apply/registration logic as if
// resuming from draft (SRS-F-007).
func actionEnableSysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	return applyOrEnableConstraintRows(ctx, ids, "enable")
}

// actionDisableSysModelConstraint drops the live object (check/unique/unique_index)
// or removes the row from the business-rule cache, then marks state=disabled.
func actionDisableSysModelConstraint(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	db := sysModelConstraintDB(ctx)
	dbName := sysModelConstraintDBName(ctx)
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if err := dropLiveConstraintObjectBestEffort(ctx, db, dbName, row); err != nil {
			return nil, err
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(SysModelConstraintModelName, nil), []string{stringValue(row["id"])}, orm.RecordMap{"state": "disabled", "last_error": ""}); err != nil {
			return nil, err
		}
		// Stop enforcing only once the disable commits.
		disabled := row
		orm.AfterCommit(ctx, func(context.Context) {
			removeBusinessRuleCacheRow(dbName, disabled)
			invalidateBusinessRuleSync(dbName)
		})
		if err := logMetadataChange(ctx, SysModelConstraintModelName, "action_disable", "runtime_action", row, orm.RecordMap{"state": "disabled"}); err != nil {
			return nil, err
		}
	}
	return true, nil
}
