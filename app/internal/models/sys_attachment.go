package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for
//   sys.attachment (SRS-F-019 attachments) inside the kernel model catalog.
// - Key symbols: `init`, `createSysAttachment`, `writeSysAttachment`, `sysAttachmentAccessHandler`,
//   `SysAttachmentRecord`.
// - Pattern: content lives in the checksum-addressed filestore (internal/services/filestore);
//   the row stores only metadata, so dedup and blob GC stay independent of row lifecycle.
// - Access rule (SRS-F-019, merged with base.attachment's scope model 2026-08-01): a bound
//   attachment is visible if the caller can read the target (sys_model, model_id) record, OR if an
//   explicit user_id/group_id/company_id/is_public scope grants it (never a restriction on the
//   delegation rule, only an additional path). An unbound attachment is visible via the same scope
//   grant, or — if no scope is set at all — to every authenticated user of the database. Deletion
//   is stricter than read/write: creator or sudo only, regardless of scope or delegation. See
//   sysAttachmentAccessHandler/sysAttachmentScopeVisible/validateSysAttachmentScope.

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/filestore"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysAttachmentRecord stores one binary attachment's metadata. The bytes
// themselves live once per checksum in the per-database filestore, so two rows
// may reference the same blob (SRS-F-019 dedup contract).
type SysAttachmentRecord struct {
	orm.BaseModel
	Name string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the file name for this attachment."`
	// ResModel/ModelId bind the attachment to one record; both empty means an
	// unbound attachment (see UserID/GroupID/IsPublic for its visibility rule).
	ResModel string `bun:"sys_model" json:"sys_model" meta:"index,label:Resource Model,help:Stores the model name of the record this attachment belongs to."`
	ModelId  int64  `bun:"model_id" json:"model_id" meta:"index,label:Resource ID,help:Stores the record id this attachment belongs to."`
	// Mimetype is derived server-side on create; HTML-like client claims are
	// never trusted (SRS-F-019 serving rule).
	Mimetype string `bun:"mimetype" json:"mimetype" meta:"readonly,label:Mimetype,help:Stores the server-derived content type for this attachment."`
	// Checksum is the SHA-256 content address of the blob in the filestore.
	// Content is immutable per row: replacing bytes means a new attachment.
	Checksum string `bun:"checksum" json:"checksum" meta:"readonly,index,label:Checksum,help:Stores the SHA-256 content address of the stored blob."`
	FileSize int64  `bun:"file_size" json:"file_size" meta:"readonly,label:File Size,help:Stores the blob size in bytes."`
	// CreateUID records the uploader; always granted read/write/unlink access
	// to its own rows regardless of scope or binding.
	CreateUID int64  `bun:"create_uid" json:"create_uid" meta:"readonly,index,label:Created By,help:Stores the user id that uploaded this attachment."`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Links this attachment to one company. Empty means shared across companies."`
	// UserID/GroupID/IsPublic are the base.attachment-equivalent visibility
	// scope (merged 2026-08-01): at most one of company_id/user_id/group_id
	// may be set (validateSysAttachmentScope); is_public additionally allows
	// unauthenticated download via /web/content regardless of scope. See
	// sysAttachmentAccessHandler for how this composes with the bound-record
	// delegation rule.
	UserID   *int64 `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:User,help:Restricts visibility to this user."`
	GroupID  *int64 `bun:"group_id" json:"group_id" meta:"kind:many2one,model:res.group,ondelete:SET NULL,index,label:Group,help:Restricts visibility to members of this group."`
	IsPublic bool   `bun:"is_public" json:"is_public" default:"false" meta:"label:Public,help:Allows unauthenticated download via /web/content regardless of scope."`
}

const SysAttachmentModelName = "sys.attachment"

// Attachment size limit config (SRS-F-019): per-database overridable via
// sys.config_param `attachment.max_size_mb`, default 20 MB.
const (
	sysAttachmentMaxSizeParamKey  = "attachment.max_size_mb"
	sysAttachmentDefaultMaxSizeMB = 20
)

var SysAttachmentModel = orm.Define[SysAttachmentRecord](&Models, 320, orm.ModelSpec{
	Name:        SysAttachmentModelName,
	Label:       "Attachments",
	Description: "Binary files bound to records with checksum-addressed dedup storage (SRS-F-019)",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysAttachment},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysAttachment},
		// attachments_for_record replaces base.attachment's ListByRecord
		// (merged 2026-08-01): generic search/search_read has no per-row
		// scope filtering (see sysAttachmentAccessHandler's comment), so
		// "list the attachments bound to record X" needs its own method.
		{Name: "attachments_for_record", Public: true, ModelOnly: true, Call: listSysAttachmentsForRecord},
	},
})

// Indirection breaks the SysAttachmentModelSpec -> createSysAttachment ->
// SysAttachmentModel initialization cycle, mirroring the sys_sequence.go pattern.

func init() {
	// The access handler REPLACES the default ACL handler for this model, so it
	// chains through orm.EnsureDefaultModelAccess first and only then adds the
	// target-record delegation that makes attachments exactly as visible as the
	// records they belong to.
	orm.RegisterAccessHandler(SysAttachmentModelName, sysAttachmentAccessHandler)
}

// sysAttachmentAccessHandler enforces the SRS-F-019 access rule, merged
// 2026-08-01 with the base.attachment scope rule, on top of the standard ACL
// semantics. Sudo never reaches this handler (ensureModelAccess bypasses
// hooks for sudo), so "admins" see everything by construction.
func sysAttachmentAccessHandler(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	// Standard ACL/group semantics first — this handler only ADDS checks.
	if err := orm.EnsureDefaultModelAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	// Create is validated in createSysAttachment where the full payload
	// (including the decoded content) is available; id-less operations such as
	// search stay on ACL only (no per-row filtering — see attachments_for_record
	// for the one listing path that does need it).
	if operation == "create" || len(ids) == 0 {
		return nil
	}
	rows, err := loadSysAttachmentRowsByID(ctx, ids)
	if err != nil {
		return err
	}
	// Walk each affected row in order so validation and side effects stay scoped to the current element.
	for _, row := range rows {
		// Deletion is intentionally stricter than read/write: only the creator or
		// sudo may unlink, regardless of scope or record-read delegation, so a
		// shared/company-visible file can never be deleted by any reader who
		// merely has access to view it (base.attachment's original delete rule).
		if operation == "unlink" {
			if env == nil || env.UserID() != row.CreateUID {
				log.Errorf("permission denied: attachment %d is not accessible", row.ID)
				return fmt.Errorf("permission denied: attachment %d is not accessible", row.ID)
			}
			continue
		}
		bound := strings.TrimSpace(row.ResModel) != "" && row.ModelId > 0
		// Write is stricter than read (plan T33): the creator, or a caller with
		// WRITE access on the bound record. Read delegation and scope grants
		// never allow edits, so a reader cannot rebind, rescope or publish.
		if operation == "write" {
			if sysAttachmentWriteAllowed(ctx, env, row) {
				continue
			}
			log.Errorf("permission denied: attachment %d is not writable", row.ID)
			return orm.Forbiddenf("permission denied: attachment %d is not accessible", row.ID)
		}
		// Bound attachments primarily delegate to the target record: read access
		// to the record grants access to its attachments (SRS-F-019). An explicit
		// scope grant (base.attachment merge) is an ADDITIONAL path, never a
		// restriction — it never revokes what the delegation rule already grants.
		if bound {
			if err := orm.EnsureRecordReadAccess(ctx, row.ResModel, []string{strconv.FormatInt(row.ModelId, 10)}); err == nil {
				continue
			}
		}
		if sysAttachmentScopeVisible(row, env, bound) {
			continue
		}
		// One denial message shape regardless of whether the target exists
		// or is blocked — existence must not leak through the error text.
		log.Errorf("permission denied: attachment %d is not accessible", row.ID)
		return fmt.Errorf("permission denied: attachment %d is not accessible", row.ID)
	}
	return nil
}

// sysAttachmentScopeVisible implements the base.attachment-equivalent
// visibility rule: the creator, a public row, a matching company/user/group
// scope, or — for UNBOUND rows only — no scope set at all (shared with every
// authenticated user of the database, matching base.attachment's original
// default). Bound rows with no scope set are NOT shared by this rule; they
// rely solely on the record-read delegation above, so SRS-F-019 stays intact
// for the common bound case.
func sysAttachmentScopeVisible(row sysAttachmentAccessRow, env *orm.Environment, bound bool) bool {
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	if row.CreateUID == userID {
		return true
	}
	if row.IsPublic {
		return true
	}
	if row.CompanyID != nil {
		if allowed, err := env.AllowedCompanyIDs(); err == nil && int64SliceContains(allowed, *row.CompanyID) {
			return true
		}
	}
	if row.UserID != nil && *row.UserID == userID {
		return true
	}
	if row.GroupID != nil && env != nil {
		if groupIDs, ok := env.ContextValue(orm.RBACContextGroupIDsKey); ok {
			if ids, ok := groupIDs.([]int64); ok && int64SliceContains(ids, *row.GroupID) {
				return true
			}
		}
	}
	if !bound && row.CompanyID == nil && row.UserID == nil && row.GroupID == nil {
		return true
	}
	return false
}

// int64SliceContains reports whether target is present in values.
func int64SliceContains(values []int64, target int64) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// createSysAttachment decodes the base64 `datas` payload, enforces the size
// and mimetype rules, stores the blob content-addressed, and persists the
// metadata row. `datas` itself is never a column.
func createSysAttachment(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("sys.attachment.create requires values")
		return nil, fmt.Errorf("sys.attachment.create requires values")
	}

	env := orm.EnvironmentFromContext(ctx)
	// The blob must land in the request database's filestore subtree, so an
	// environment-bound database name is a hard requirement for uploads.
	if env == nil || strings.TrimSpace(env.DBName()) == "" {
		log.Errorf("sys.attachment.create requires a database-bound environment")
		return nil, fmt.Errorf("sys.attachment.create requires a database-bound environment")
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		working := cloneRecordMap(values)
		name := strings.TrimSpace(stringValue(working["name"]))
		if name == "" {
			log.Errorf("sys.attachment name is required")
			return nil, fmt.Errorf("sys.attachment name is required")
		}
		data, err := decodeSysAttachmentDatas(working)
		if err != nil {
			return nil, err
		}
		// Oversize uploads are rejected BEFORE any blob write so the filestore
		// never accumulates content the row layer refused (SRS-F-019).
		maxBytes := sysAttachmentMaxSizeBytes(ctx)
		if int64(len(data)) > maxBytes {
			log.Errorf("sys.attachment %q exceeds the maximum size of %d bytes", name, maxBytes)
			return nil, fmt.Errorf("sys.attachment %q exceeds the maximum size of %d bytes", name, maxBytes)
		}
		if err := validateSysAttachmentBinding(ctx, env, working); err != nil {
			return nil, err
		}
		if err := validateSysAttachmentScope(env, working); err != nil {
			return nil, err
		}

		checksum, size, err := filestore.Store(ctx, env.DBName(), data)
		if err != nil {
			return nil, err
		}
		// Server-derived facts overwrite whatever the client sent: the checksum,
		// size, mimetype, and uploader are trust anchors for serving and GC.
		working["checksum"] = checksum
		working["file_size"] = size
		working["mimetype"] = resolveSysAttachmentMimetype(stringValue(working["mimetype"]), data)
		working["create_uid"] = env.UserID()
		// datas is transport payload, not a column; it must never reach the ORM.
		delete(working, "datas")
		if _, ok := working["active"]; !ok {
			working["active"] = true
		}

		// Raw insert on purpose: company_id/user_id/group_id are mutually exclusive scopes
		// (validateSysAttachmentScope), so CreateRecord's implicit company pinning would turn a
		// user- or group-scoped attachment into an invalid two-scope row. create_uid is also set
		// server-side here. The T15 dispatcher guard validates an explicit company_id.
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(SysAttachmentModelName, model), working)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

// writeSysAttachment allows metadata edits (name, binding, company) while
// keeping the content immutable: bytes, checksum, size, mimetype, and uploader
// can never change on an existing row — replacing content means a new
// attachment, which keeps the checksum-addressed dedup story consistent.
func writeSysAttachment(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.attachment.write requires ids")
		return nil, fmt.Errorf("sys.attachment.write requires ids")
	}
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("sys.attachment.write requires values")
		return nil, fmt.Errorf("sys.attachment.write requires values")
	}
	// Walk each immutable key in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"datas", "checksum", "file_size", "mimetype", "create_uid"} {
		if _, ok := values[key]; ok {
			log.Errorf("sys.attachment %s is immutable; create a new attachment instead", key)
			return nil, fmt.Errorf("sys.attachment %s is immutable; create a new attachment instead", key)
		}
	}
	// Rebinding to another record must pass the same validation as create so a
	// write can never attach a row to an unregistered model or an inaccessible
	// record.
	if hasRecordMapKey(values, "sys_model") || hasRecordMapKey(values, "model_id") {
		if err := validateSysAttachmentBinding(ctx, orm.EnvironmentFromContext(ctx), values); err != nil {
			return nil, err
		}
	}
	if err := ensureSysAttachmentPublishAllowed(ctx, ids, values); err != nil {
		return nil, err
	}
	// Rescoping (company_id/user_id/group_id/is_public) must pass the same
	// mutual-exclusivity and self-scope validation as create.
	if hasRecordMapKey(values, "company_id") || hasRecordMapKey(values, "user_id") || hasRecordMapKey(values, "group_id") || hasRecordMapKey(values, "is_public") {
		if err := validateSysAttachmentScope(orm.EnvironmentFromContext(ctx), values); err != nil {
			return nil, err
		}
	}
	return orm.WriteRecords(ctx, orm.RuntimeModelRef(SysAttachmentModelName, model), ids, values)
}

// validateSysAttachmentScope enforces the base.attachment-equivalent scope
// contract (merged 2026-08-01): at most one of company_id/user_id/group_id
// may be set, and a non-sudo caller may only scope a row to a company/group
// they belong to, or to themselves.
func validateSysAttachmentScope(env *orm.Environment, values orm.RecordMap) error {
	companyID, hasCompany := sysAttachmentScopeInt64(values, "company_id")
	userID, hasUser := sysAttachmentScopeInt64(values, "user_id")
	groupID, hasGroup := sysAttachmentScopeInt64(values, "group_id")
	setCount := 0
	// Walk each has-flag in order so validation and side effects stay scoped to the current element.
	for _, has := range []bool{hasCompany, hasUser, hasGroup} {
		if has {
			setCount++
		}
	}
	if setCount > 1 {
		log.Errorf("sys.attachment: at most one of company_id, user_id, group_id may be set")
		return fmt.Errorf("sys.attachment: at most one of company_id, user_id, group_id may be set")
	}
	// Sudo may scope an attachment to any company/user/group (mirrors
	// validateSysAttachmentBinding's sudo bypass).
	if env == nil || env.IsSudo() {
		return nil
	}
	if hasCompany {
		allowed, err := env.AllowedCompanyIDs()
		if err != nil {
			return err
		}
		if !int64SliceContains(allowed, companyID) {
			log.Errorf("permission denied: cannot scope attachment to company %d", companyID)
			return fmt.Errorf("permission denied: cannot scope attachment to company %d", companyID)
		}
	}
	if hasUser && userID != env.UserID() {
		log.Errorf("permission denied: cannot scope attachment to another user")
		return fmt.Errorf("permission denied: cannot scope attachment to another user")
	}
	if hasGroup {
		groupIDs, _ := env.ContextValue(orm.RBACContextGroupIDsKey)
		ids, _ := groupIDs.([]int64)
		if !int64SliceContains(ids, groupID) {
			log.Errorf("permission denied: cannot scope attachment to a group you do not belong to")
			return fmt.Errorf("permission denied: cannot scope attachment to a group you do not belong to")
		}
	}
	return nil
}

// sysAttachmentScopeInt64 reads a positive-id scope field, treating an
// absent key, nil, or non-positive value alike as "not set" (mirrors
// base.attachment's original form-parsing rule of only counting a
// non-empty raw value).
func sysAttachmentScopeInt64(values orm.RecordMap, key string) (int64, bool) {
	if !hasRecordMapKey(values, key) {
		return 0, false
	}
	value, ok := int64FromAny(values[key])
	if !ok || value <= 0 {
		return 0, false
	}
	return value, true
}

// validateSysAttachmentBinding enforces the (sys_model, model_id) contract: a
// bound attachment needs a registered model and a positive record id, and a
// non-sudo caller must be able to read the target record before binding to it.
func validateSysAttachmentBinding(ctx context.Context, env *orm.Environment, values orm.RecordMap) error {
	resModel := strings.TrimSpace(stringValue(values["sys_model"]))
	modelId, _ := int64FromAny(values["model_id"])
	if resModel == "" {
		// Unbound attachments are allowed; clear a stray model_id so visibility
		// semantics (creator-only) stay unambiguous.
		if modelId != 0 {
			log.Errorf("sys.attachment model_id requires sys_model")
			return fmt.Errorf("sys.attachment model_id requires sys_model")
		}
		return nil
	}
	// Unknown sys_model is a validation error per SRS-F-019.
	if _, ok := orm.LookupModel(resModel); !ok {
		log.Errorf("sys.attachment sys_model %q is not registered", resModel)
		return fmt.Errorf("sys.attachment sys_model %q is not registered", resModel)
	}
	if modelId <= 0 {
		log.Errorf("sys.attachment sys_model requires a positive model_id")
		return fmt.Errorf("sys.attachment sys_model requires a positive model_id")
	}
	// Binding requires read access to the target so a caller can never attach
	// files to records they cannot see (sudo bypasses by design).
	if env != nil && !env.IsSudo() {
		if err := orm.EnsureRecordReadAccess(ctx, resModel, []string{strconv.FormatInt(modelId, 10)}); err != nil {
			log.Errorf("permission denied: cannot attach to %s(%d)", resModel, modelId)
			return fmt.Errorf("permission denied: cannot attach to %s(%d)", resModel, modelId)
		}
	}
	return nil
}

// decodeSysAttachmentDatas decodes the required base64 `datas` payload.
func decodeSysAttachmentDatas(values orm.RecordMap) ([]byte, error) {
	raw := strings.TrimSpace(stringValue(values["datas"]))
	if raw == "" {
		log.Errorf("sys.attachment.create requires base64 datas")
		return nil, fmt.Errorf("sys.attachment.create requires base64 datas")
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		log.Errorf("sys.attachment datas is not valid base64: %v", err)
		return nil, fmt.Errorf("sys.attachment datas is not valid base64: %w", err)
	}
	if len(data) == 0 {
		log.Errorf("sys.attachment datas must not be empty")
		return nil, fmt.Errorf("sys.attachment datas must not be empty")
	}
	return data, nil
}

// resolveSysAttachmentMimetype derives the served content type. The client
// value is kept only when it is present and neither side looks HTML-like:
// HTML-ish content claims are never trusted because they enable stored-XSS
// through inline rendering (SRS-F-019 serving rule).
func resolveSysAttachmentMimetype(clientMimetype string, data []byte) string {
	detected := http.DetectContentType(data)
	client := strings.TrimSpace(clientMimetype)
	if client == "" || IsHTMLishMimetype(client) || IsHTMLishMimetype(detected) {
		return detected
	}
	return client
}

// IsHTMLishMimetype reports whether a content type can execute in the browser
// when rendered inline; such types are always forced to download on serve.
func IsHTMLishMimetype(mimetype string) bool {
	lowered := strings.ToLower(strings.TrimSpace(mimetype))
	// Walk each dangerous marker in order so validation and side effects stay scoped to the current element.
	for _, marker := range []string{"html", "xml", "svg", "javascript", "ecmascript"} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

// sysAttachmentMaxSizeBytes resolves the upload size limit from the request
// database's sys.config_param, falling back to the 20 MB SRS default when the
// param is absent, unparsable, or non-positive.
func sysAttachmentMaxSizeBytes(ctx context.Context) int64 {
	fallback := int64(sysAttachmentDefaultMaxSizeMB) * 1024 * 1024
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		return fallback
	}
	var raw string
	if err := db.NewRaw(
		`SELECT value::text FROM sys_config_param WHERE name = ? LIMIT 1`,
		sysAttachmentMaxSizeParamKey,
	).Scan(ctx, &raw); err != nil {
		return fallback
	}
	// jsonb text form quotes string values; strip before parsing.
	parsed, err := strconv.ParseInt(strings.Trim(strings.TrimSpace(raw), `"`), 10, 64)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed * 1024 * 1024
}

// sysAttachmentAccessRow is the raw projection used by the access handler.
type sysAttachmentAccessRow struct {
	ID        int64  `bun:"id"`
	ResModel  string `bun:"sys_model"`
	ModelId   int64  `bun:"model_id"`
	CreateUID int64  `bun:"create_uid"`
	CompanyID *int64 `bun:"company_id"`
	UserID    *int64 `bun:"user_id"`
	GroupID   *int64 `bun:"group_id"`
	IsPublic  bool   `bun:"is_public"`
}

// loadSysAttachmentRowsByID reads the binding facts for access decisions on
// the environment-bound handle, binding the explicit storage table per the
// package-level query rule (tbl_lang rule).
func loadSysAttachmentRowsByID(ctx context.Context, ids []string) ([]sysAttachmentAccessRow, error) {
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.attachment: no database handle available")
		return nil, fmt.Errorf("sys.attachment: no database handle available")
	}
	numericIDs := make([]int64, 0, len(ids))
	for _, raw := range ids {
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		if err != nil {
			log.Errorf("sys.attachment: invalid record id %q", raw)
			return nil, fmt.Errorf("sys.attachment: invalid record id %q", raw)
		}
		numericIDs = append(numericIDs, parsed)
	}
	if len(numericIDs) == 0 {
		return nil, nil
	}
	rows := make([]sysAttachmentAccessRow, 0, len(numericIDs))
	err := db.NewRaw(
		// COALESCE mirrors the sys.sequence raw-scan rule: untagged columns
		// created by bun CreateTable are nullable, so non-pointer scans need it.
		// company_id/user_id/group_id stay pointers (COALESCE would collapse the
		// "unset" meaning the scope rule depends on) and is_public defaults false.
		`SELECT id, COALESCE(sys_model, '') AS sys_model, COALESCE(model_id, 0) AS model_id, COALESCE(create_uid, 0) AS create_uid,
		        company_id, user_id, group_id, COALESCE(is_public, false) AS is_public
		 FROM sys_attachment WHERE id IN (?)`,
		bun.In(numericIDs),
	).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// listSysAttachmentsForRecord returns the attachments bound to one
// (sys_model, model_id), replacing base.attachment's ListByRecord (merged
// 2026-08-01). Unlike generic search/search_read, this enforces per-row
// visibility itself: if the caller can read the target record, every bound
// row is returned (SRS-F-019 delegation, matching what a single read-by-id
// would already grant); otherwise rows are filtered to the scope-visible
// subset (creator/public/company/user/group match — the same OR rule
// sysAttachmentScopeVisible applies for bound rows).
func listSysAttachmentsForRecord(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	resModel := strings.TrimSpace(stringValue(kwargs["sys_model"]))
	modelId, _ := int64FromAny(kwargs["model_id"])
	if resModel == "" || modelId <= 0 {
		log.Errorf("sys.attachment.attachments_for_record requires sys_model and model_id")
		return nil, fmt.Errorf("sys.attachment.attachments_for_record requires sys_model and model_id")
	}
	db := sysSequenceDBFromContext(ctx)
	if db == nil {
		log.Errorf("sys.attachment: no database handle available")
		return nil, fmt.Errorf("sys.attachment: no database handle available")
	}
	env := orm.EnvironmentFromContext(ctx)

	delegated := env == nil || env.IsSudo()
	// A single check against the shared target record governs every candidate
	// row: they all bind to the same (sys_model, model_id).
	if !delegated {
		delegated = orm.EnsureRecordReadAccess(ctx, resModel, []string{strconv.FormatInt(modelId, 10)}) == nil
	}

	var rows []SysAttachmentRecord
	// ModelTableExpr overrides bun's default (pluralized-struct-name) table
	// guess: SysAttachmentRecord has no bun.BaseModel tag — one can't be added
	// without colliding with the embedded orm.BaseModel field name — so raw
	// bun queries must name the real table explicitly (see sys_rule.go /
	// sys_model_access.go for the same constraint). The alias must be kept as
	// "sys_attachment_record" (bun's struct-derived default) because the
	// generated SELECT column list is still qualified with that alias; a bare
	// "sys_attachment" table expr drops the alias and the query fails with
	// "missing FROM-clause entry for table sys_attachment_record".
	query := db.NewSelect().Model(&rows).ModelTableExpr("sys_attachment AS sys_attachment_record").
		Where("sys_model = ?", resModel).
		Where("model_id = ?", modelId).
		OrderExpr("id ASC")
	if !delegated {
		userID := int64(0)
		if env != nil {
			userID = env.UserID()
		}
		companyIDs, _ := env.AllowedCompanyIDs()
		groupIDsRaw, _ := env.ContextValue(orm.RBACContextGroupIDsKey)
		groupIDs, _ := groupIDsRaw.([]int64)
		query.Where(
			`(create_uid = ? OR is_public = true OR company_id IN (?) OR user_id = ? OR group_id IN (?))`,
			userID, bun.In(nonEmptySysAttachmentInt64s(companyIDs)), userID, bun.In(nonEmptySysAttachmentInt64s(groupIDs)),
		)
	}

	if err := query.Scan(ctx, &rows); err != nil {
		log.Errorf("sys.attachment.attachments_for_record: %v", err)
		return nil, fmt.Errorf("sys.attachment.attachments_for_record: %w", err)
	}
	return rows, nil
}

// nonEmptySysAttachmentInt64s guards against bun.In(nil) producing invalid
// SQL ("= ANY()") by substituting an impossible id so the ANY() clause stays
// valid and simply matches nothing.
func nonEmptySysAttachmentInt64s(values []int64) []int64 {
	if len(values) == 0 {
		return []int64{-1}
	}
	return values
}
