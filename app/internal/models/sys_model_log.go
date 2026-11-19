package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model log inside the kernel model catalog.
// - Key symbols: `init`, `createLoggedMetadataRecords`, `SysModelLogRecord`, `orm.CreateRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysModelLogRecord stores append-only metadata audit rows so operators can
// inspect how field, relation, and access definitions changed over time.
type SysModelLogRecord struct {
	bun.BaseModel `bun:"table:sys_model_log,alias:sys_model_log"`
	orm.MetadataBaseModel
	Name          string         `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	ChangeType    string         `bun:"change_type" json:"change_type" meta:"index,label:Change Type,help:Stores the change type value for this record."`
	Action        string         `bun:"action" json:"action" meta:"index,label:Action,help:Stores the action value for this record."`
	TargetModel   string         `bun:"target_model" json:"target_model" meta:"index,label:Target Model,help:Stores the target model value for this record."`
	TargetRecord  string         `bun:"target_record" json:"target_record" meta:"index,label:Target Record,help:Stores the target record value for this record."`
	SubjectModel  string         `bun:"subject_model" json:"subject_model" meta:"index,label:Subject Model,help:Stores the subject model value for this record."`
	SubjectName   string         `bun:"subject_name" json:"subject_name" meta:"index,label:Subject Name,help:Stores the subject name value for this record."`
	Source        string         `bun:"source" json:"source" meta:"index,label:Source,help:Stores the source value for this record."`
	UserID        int64          `bun:"user_id" json:"user_id" meta:"index,label:User ID,help:Stores the numeric value for user id."`
	ChangedFields []string       `bun:"changed_fields,type:jsonb,nullzero" json:"changed_fields" meta:"kind:json,label:Changed Fields,help:Stores the changed fields payload in JSON format."`
	BeforeData    map[string]any `bun:"before_data,type:jsonb,nullzero" json:"before_data" meta:"kind:json,label:Before Data,help:Stores the before data payload in JSON format."`
	AfterData     map[string]any `bun:"after_data,type:jsonb,nullzero" json:"after_data" meta:"kind:json,label:After Data,help:Stores the after data payload in JSON format."`
}

const SysModelLogModelName = "sys.model.log"

var SysModelLogModel = orm.Define[SysModelLogRecord](&Models, 30, orm.ModelSpec{
	Name:        SysModelLogModelName,
	Label:       "Model Change Log",
	Description: "Append-only audit log for sys.model field, relation, and access metadata changes",
})

func init() {
	orm.RegisterModelChangeLogger(insertSysModelChangeLog)
}

// createLoggedMetadataRecords creates the logged metadata records handled by this package.
// Runtime CRUD dispatch resolves through the shared ORM repository helpers by
// target model name, so metadata models no longer wire per-model delegates.
func createLoggedMetadataRecords(ctx context.Context, targetModel string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("%s.create requires values", targetModel)
		return nil, fmt.Errorf("%s.create requires values", targetModel)
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		// Guarded create (T87): RPC callers get the same normalisation, computed-field and company
		// guards as the default create; the change log below is the override's only extra.
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(targetModel, nil), cloneRecordMap(values))
		if err != nil {
			return nil, err
		}
		if err := logMetadataChange(ctx, targetModel, "create", "runtime_crud", nil, record); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeLoggedMetadataRecords(ctx context.Context, targetModel string, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("%s.write requires values", targetModel)
		return nil, fmt.Errorf("%s.write requires values", targetModel)
	}
	if len(ids) == 0 {
		log.Errorf("%s.write requires ids", targetModel)
		return nil, fmt.Errorf("%s.write requires ids", targetModel)
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(targetModel, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(targetModel, nil), ids, cloneRecordMap(values))
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}

	afterRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(targetModel, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	beforeByID := recordMapsByID(beforeRows)
	afterByID := recordMapsByID(afterRows)
	for _, id := range ids {
		before := beforeByID[id]
		after := afterByID[id]
		if len(after) == 0 {
			continue
		}
		if err := logMetadataChange(ctx, targetModel, "write", "runtime_crud", before, after); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

func unlinkLoggedMetadataRecords(ctx context.Context, targetModel string, ids []string) (any, error) {
	if len(ids) == 0 {
		log.Errorf("%s.unlink requires ids", targetModel)
		return nil, fmt.Errorf("%s.unlink requires ids", targetModel)
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(targetModel, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	ok, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(targetModel, nil), ids)
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}
	for _, row := range beforeRows {
		if err := logMetadataChange(ctx, targetModel, "unlink", "runtime_crud", row, nil); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

// logMetadataChange logs the metadata change handled by this package.
func logMetadataChange(ctx context.Context, targetModel, action, source string, before, after orm.RecordMap) error {
	entry := buildMetadataChangeLogEntry(ctx, targetModel, action, source, before, after)
	if action == "write" && len(entry.ChangedFields) == 0 {
		return nil
	}
	return orm.LogModelChange(ctx, metadataChangeDB(ctx), entry)
}

func metadataChangeDB(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	return orm.GetBunDB()
}

func insertSysModelChangeLog(ctx context.Context, db bun.IDB, entry orm.ModelChangeLogEntry) error {
	if db == nil {
		db = orm.GetBunDB()
	}
	if db == nil {
		log.Errorf("sys.model.log database is not initialized")
		return fmt.Errorf("sys.model.log database is not initialized")
	}

	record := SysModelLogRecord{
		Name:          buildSysModelLogName(entry),
		ChangeType:    strings.TrimSpace(entry.ChangeType),
		Action:        strings.TrimSpace(entry.Action),
		TargetModel:   strings.TrimSpace(entry.TargetModel),
		TargetRecord:  strings.TrimSpace(entry.TargetRecord),
		SubjectModel:  strings.TrimSpace(entry.SubjectModel),
		SubjectName:   strings.TrimSpace(entry.SubjectName),
		Source:        strings.TrimSpace(entry.Source),
		UserID:        entry.UserID,
		ChangedFields: append([]string(nil), entry.ChangedFields...),
		BeforeData:    cloneJSONMap(entry.BeforeData),
		AfterData:     cloneJSONMap(entry.AfterData),
	}
	_, err := db.NewInsert().Model(&record).Exec(ctx)
	return err
}

// buildMetadataChangeLogEntry builds the metadata change log entry handled by this package.
func buildMetadataChangeLogEntry(ctx context.Context, targetModel, action, source string, before, after orm.RecordMap) orm.ModelChangeLogEntry {
	subject := preferredMetadataSnapshot(after, before)
	return orm.ModelChangeLogEntry{
		ChangeType:    metadataChangeType(targetModel),
		Action:        strings.TrimSpace(action),
		TargetModel:   strings.TrimSpace(targetModel),
		TargetRecord:  metadataTargetRecordID(subject),
		SubjectModel:  strings.TrimSpace(stringValue(subject["model_name"])),
		SubjectName:   metadataSubjectName(targetModel, subject),
		Source:        strings.TrimSpace(source),
		UserID:        metadataChangeUserID(ctx),
		ChangedFields: metadataChangedFields(before, after),
		BeforeData:    cloneJSONMap(before),
		AfterData:     cloneJSONMap(after),
	}
}

// buildSysModelLogName builds the sys model log name handled by this package.
func buildSysModelLogName(entry orm.ModelChangeLogEntry) string {
	parts := []string{
		strings.TrimSpace(entry.ChangeType),
		strings.TrimSpace(entry.Action),
		strings.TrimSpace(entry.SubjectModel),
		strings.TrimSpace(entry.SubjectName),
	}
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		filtered = append(filtered, part)
	}
	if len(filtered) == 0 {
		return strings.TrimSpace(entry.TargetModel)
	}
	return strings.Join(filtered, ":")
}

func metadataChangeType(targetModel string) string {
	switch strings.TrimSpace(targetModel) {
	case SysModelFieldModelName:
		return "field"
	case SysModelRelationModelName:
		return "relation"
	case SysModelOperationModelName:
		return "operation"
	case SysModelAccessModelName:
		return "access"
	case SysModelConstraintModelName:
		return "constraint"
	default:
		return "metadata"
	}
}

func metadataSubjectName(targetModel string, values orm.RecordMap) string {
	switch strings.TrimSpace(targetModel) {
	case SysModelFieldModelName:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["field_name"])), strings.TrimSpace(stringValue(values["name"])))
	case SysModelRelationModelName:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["field_name"])), strings.TrimSpace(stringValue(values["name"])))
	case SysModelOperationModelName:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["method_name"])), strings.TrimSpace(stringValue(values["name"])))
	case SysModelAccessModelName:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["name"])), strings.TrimSpace(stringValue(values["group_id"])))
	case SysModelConstraintModelName:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["name"])), strings.TrimSpace(stringValue(values["model_name"])))
	default:
		return firstNonEmpty(strings.TrimSpace(stringValue(values["name"])), strings.TrimSpace(stringValue(values["id"])))
	}
}

func metadataTargetRecordID(values orm.RecordMap) string {
	if text, ok := stringFromAny(values["id"]); ok {
		return strings.TrimSpace(text)
	}
	if number, ok := int64FromAny(values["id"]); ok {
		return fmt.Sprintf("%d", number)
	}
	return ""
}

func preferredMetadataSnapshot(primary, fallback orm.RecordMap) orm.RecordMap {
	if len(primary) > 0 {
		return primary
	}
	return fallback
}

func metadataChangeUserID(ctx context.Context) int64 {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return env.UserID()
	}
	return 0
}

func metadataChangedFields(before, after orm.RecordMap) []string {
	keys := make(map[string]struct{})
	for key := range before {
		if shouldIgnoreMetadataChangeField(key) {
			continue
		}
		keys[key] = struct{}{}
	}
	for key := range after {
		if shouldIgnoreMetadataChangeField(key) {
			continue
		}
		keys[key] = struct{}{}
	}

	changed := make([]string, 0, len(keys))
	for key := range keys {
		if reflect.DeepEqual(before[key], after[key]) {
			continue
		}
		changed = append(changed, key)
	}
	sort.Strings(changed)
	return changed
}

func shouldIgnoreMetadataChangeField(name string) bool {
	switch strings.TrimSpace(name) {
	case "", "id", "createdAt", "updatedAt", "created_at", "updated_at":
		return true
	default:
		return false
	}
}

func recordMapsByID(rows []orm.RecordMap) map[string]orm.RecordMap {
	indexed := make(map[string]orm.RecordMap, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(stringValue(row["id"]))
		if id == "" {
			continue
		}
		indexed[id] = row
	}
	return indexed
}

// cloneJSONMap clones the json map handled by this package.
func cloneJSONMap(values orm.RecordMap) map[string]any {
	if values == nil {
		return nil
	}
	cloned := make(map[string]any, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
