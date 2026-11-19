package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model inside the kernel model catalog.
// - Key symbols: `init`, `SysModelRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// SysModelRecord stores frontend-manageable model metadata rows in the shared
// system catalog table while keeping the runtime registry snapshot queryable.
type SysModelRecord struct {
	bun.BaseModel `bun:"table:sys_model,alias:sys_model"`
	orm.MetadataBaseModel
	BID            string         `bun:"bid" json:"bid" meta:"index,label:BID,help:Stores the universal global unique record identifier for this record."`
	Name           string         `bun:"name" json:"name" meta:"index,unique,label:Name,help:Stores the human readable name for this record."`
	Label          string         `bun:"label" json:"label" meta:"label:Label,help:Stores the human readable label shown to users."`
	Description    string         `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this record."`
	StorageBackend string         `bun:"storage_backend" json:"storage_backend" meta:"label:Storage Backend,help:Stores the storage backend value for this record."`
	StorageName    string         `bun:"storage_name" json:"storage_name" meta:"label:Storage Name,help:Stores the storage name value for this record."`
	HelperData     string         `bun:"helper_data" json:"helper_data" meta:"readonly,kind:html,label:Helper Data,help:Stores generated field helper HTML for this model."`
	JsonDto        map[string]any `bun:"json_dto,type:jsonb,nullzero" json:"json_dto" meta:"readonly,kind:json,label:JSON DTO,help:Stores generated runtime metadata for this model in JSON format."`
	Status         string         `bun:"status" json:"status" meta:"readonly,label:Status,help:Stores the publish lifecycle status of this model (INIT, PUBLISHING, PUBLISHED, UNPUBLISHING, UNPUBLISHED, DELETING, DELETED)."`
	HasUi          bool           `bun:"has_ui" json:"has_ui" default:"false" meta:"readonly,label:Has UI,help:True once this model has an admin screen. Go-coded models (no bid) are always true; custom models flip true only after base.model.create_ui / the Create UI button scaffolds their list/form views and menu entry."`
}

const SysModelModelName = "sys.model"

// sys.model / sys.model.field publish lifecycle statuses. INIT is the
// default for a freshly created custom model/field; PUBLISHING/UNPUBLISHING
// are transient (set synchronously before the async publish/unpublish job
// starts, cleared to a terminal status once the job finishes); DELETING is
// set synchronously right before a custom model's catalog row is removed
// (a DELETED status is never persisted: the row is gone).
const (
	SysModelStatusInit         = "INIT"
	SysModelStatusPublishing   = "PUBLISHING"
	SysModelStatusPublished    = "PUBLISHED"
	SysModelStatusUnpublishing = "UNPUBLISHING"
	SysModelStatusUnpublished  = "UNPUBLISHED"
	SysModelStatusDeleting     = "DELETING"
)

var SysModelModel = orm.Define[SysModelRecord](&Models, 20, orm.ModelSpec{
	Name:        SysModelModelName,
	Label:       "Models",
	Description: "System model metadata catalog exposed to the frontend; rows synced from a Go model definition (no bid) are read-only, custom models (with a bid) stay editable",
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createSysModel},
		{Name: "write", Public: true, UsesPathID: true, Call: writeSysModel},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkSysModel},
		{Name: "publish", Public: true, UsesPathID: true, Call: publishSysModel},
		{Name: "unpublish", Public: true, UsesPathID: true, Call: unpublishSysModel},
		{Name: "create_ui", Public: true, UsesPathID: true, Call: createUISysModel},
	},
})

func init() {
	registerFrontendBIDConstraint(SysModelModelName)
}

// createSysModel creates the sys model handled by this package. Only rows
// carrying a caller-supplied bid (a custom model, not one reflected from
// Go code) may be created here — registry sync never sets bid, so an
// empty bid at create time would produce a row indistinguishable from a
// Go-coded one and later collide with (or be silently kept alongside) a real
// sync row.
func createSysModel(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("sys.model.create requires values")
		return nil, fmt.Errorf("sys.model.create requires values")
	}
	for _, values := range valuesList {
		if strings.TrimSpace(stringValue(values["bid"])) == "" {
			return nil, fmt.Errorf("sys.model rows synced from Go code are read-only; only custom models (with a bid) can be created here")
		}
		if strings.TrimSpace(stringValue(values["status"])) == "" {
			values["status"] = SysModelStatusInit
		}
	}

	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(SysModelModelName, model), cloneRecordMap(values))
		if err != nil {
			return nil, err
		}
		id := metadataTargetRecordID(record)
		if id != "" {
			if err := makeHelperAndJSONForSysModelRecords(ctx, []string{id}); err != nil {
				return nil, err
			}
			rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, model), []string{id}, nil)
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				record = rows[0]
			}
		}
		if err := logMetadataChange(ctx, SysModelModelName, "create", "runtime_crud", nil, record); err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

// writeSysModel handles the sys model handled by this package. Only rows
// already carrying a non-empty bid (custom models) may be edited;
// registry-synced rows (no bid) are read-only.
func writeSysModel(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("sys.model.write requires values")
		return nil, fmt.Errorf("sys.model.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("sys.model.write requires ids")
		return nil, fmt.Errorf("sys.model.write requires ids")
	}
	if _, err := guardSysModelRowsCustom(ctx, ids, "edited"); err != nil {
		return nil, err
	}

	beforeRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(beforeRows) == 0 {
		return false, nil
	}

	ok, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(SysModelModelName, model), ids, cloneRecordMap(values))
	if err != nil {
		return nil, err
	}
	if !ok {
		return ok, nil
	}
	if err := makeHelperAndJSONForSysModelRecords(ctx, ids); err != nil {
		return nil, err
	}

	afterRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, model), ids, nil)
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
		if err := logMetadataChange(ctx, SysModelModelName, "write", "runtime_crud", beforeByID[id], after); err != nil {
			return nil, err
		}
	}
	return ok, nil
}

// unlinkSysModel handles the sys model handled by this package. Only rows
// already carrying a non-empty bid (custom models) may be removed;
// registry-synced rows (no bid) are read-only. A model still published
// (registered in the live ModelRegistry) must be taken offline via
// base.model.unpublish first — unlink never implicitly unpublishes a model
// that might still be serving base.record.* traffic. Passing
// kwargs["confirm_drop_table"] = true additionally drops the physical table
// once the catalog row is gone; omitting it preserves this codebase's default
// of never destroying data on unlink (see orm.PublishModel/UnpublishModel).
func unlinkSysModel(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.model.unlink requires ids")
		return nil, fmt.Errorf("sys.model.unlink requires ids")
	}

	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, nil), ids, []string{"id", "name", "bid", "storage_name"})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	storageNames := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return nil, fmt.Errorf("sys.model rows synced from Go code are read-only; only custom models (with a bid) can be deleted")
		}
		name := strings.TrimSpace(stringValue(row["name"]))
		if _, published := orm.LookupModel(name); published {
			return nil, fmt.Errorf("sys.model %q is still published; call base.model.unpublish before unlink", name)
		}
		names = append(names, name)
		storageNames = append(storageNames, strings.TrimSpace(stringValue(row["storage_name"])))
	}

	// Mark DELETING before the row is actually removed so a concurrent reader
	// (or the requesting form, mid-refresh) can see the in-flight state; the
	// row disappears immediately after, so DELETED itself is never persisted
	// — it only ever appears in the completion event payload below.
	if err := setSysModelStatus(ctx, ids, SysModelStatusDeleting); err != nil {
		return nil, err
	}

	result, err := unlinkLoggedMetadataRecords(ctx, SysModelModelName, ids)
	if err != nil {
		// The row still exists (delete failed) — revert the transient status
		// rather than leaving it stuck at DELETING.
		if revertErr := setSysModelStatus(ctx, ids, SysModelStatusInit); revertErr != nil {
			log.Errorf("sys.model.unlink: revert status after failed delete: %v", revertErr)
		}
		return nil, err
	}
	// A deleted custom model's catalog row must never leave it queryable via
	// base.record.*/the runtime registry with no sys.model row backing it.
	// The guard above already proved every model is unpublished, so this is a
	// defensive no-op kept for idempotency rather than a required step.
	for _, name := range names {
		orm.DefaultModelRegistry().Unregister(name)
		orm.UnregisterRuntimeExecutor(name)
	}
	for i, name := range names {
		notifySysModelJobResult(ctx, ids[i], name, "unlink", true, nil)
	}

	// Dropping the physical table is opt-in and irreversible: the caller
	// (base.record.unlink / RPC unlink on sys.model) must explicitly pass
	// confirm_drop_table=true, otherwise the table/data is left untouched as
	// with every other unlink/unpublish path in this codebase.
	if confirmDropTable, _ := kwargs["confirm_drop_table"].(bool); confirmDropTable {
		for _, storageName := range storageNames {
			if storageName == "" {
				continue
			}
			if err := orm.DropCustomModelTable(ctx, storageName); err != nil {
				return nil, fmt.Errorf("sys.model.unlink: %w", err)
			}
		}
	}
	return result, nil
}

// publishSysModel backs the sys.model "Publish" object button (RuntimeMethod
// publish, UsesPathID). orm.PublishModel's AutoMigrateRegisteredModels step
// can run a full registry migration sweep, so this handler only sets the
// transient PUBLISHING status synchronously and returns immediately; the
// actual orm.PublishModel call runs in a detached goroutine
// (runSysModelPublishJob), which flips the row to PUBLISHED/back to INIT and
// fires the base.service.bus completion events the frontend listens for.
func publishSysModel(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.model.publish requires ids")
		return nil, fmt.Errorf("sys.model.publish requires ids")
	}
	names, err := guardSysModelRowsCustom(ctx, ids, "published")
	if err != nil {
		return nil, err
	}
	if err := setSysModelStatus(ctx, ids, SysModelStatusPublishing); err != nil {
		return nil, err
	}
	jobCtx, err := detachedSysModelJobContext(ctx)
	if err != nil {
		if revertErr := setSysModelStatus(ctx, ids, SysModelStatusInit); revertErr != nil {
			log.Errorf("sys.model.publish: revert status after failed job context: %v", revertErr)
		}
		return nil, err
	}
	go runSysModelPublishJob(jobCtx, ids, names)
	return readSysModelRowsAfterAction(ctx, model, ids)
}

// unpublishSysModel backs the sys.model "Unpublish" object button
// (RuntimeMethod unpublish, UsesPathID), mirroring publishSysModel's async
// shape (see runSysModelUnpublishJob).
func unpublishSysModel(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.model.unpublish requires ids")
		return nil, fmt.Errorf("sys.model.unpublish requires ids")
	}
	names, err := guardSysModelRowsCustom(ctx, ids, "unpublished")
	if err != nil {
		return nil, err
	}
	if err := setSysModelStatus(ctx, ids, SysModelStatusUnpublishing); err != nil {
		return nil, err
	}
	jobCtx, err := detachedSysModelJobContext(ctx)
	if err != nil {
		if revertErr := setSysModelStatus(ctx, ids, SysModelStatusPublished); revertErr != nil {
			log.Errorf("sys.model.unpublish: revert status after failed job context: %v", revertErr)
		}
		return nil, err
	}
	go runSysModelUnpublishJob(jobCtx, ids, names)
	return readSysModelRowsAfterAction(ctx, model, ids)
}

// createUISysModel backs the sys.model "Create UI" object button (RuntimeMethod
// create_ui, UsesPathID). It scaffolds a default list ui.view, form ui.view,
// and ui.menu entry for a published custom model (see CreateUIForModel),
// which always nests the new menu under menu.main, then stamps has_ui=true.
// Synchronous (unlike publish/unpublish): view/menu inserts are cheap
// metadata writes, not DDL, so there is no need for the async job pattern.
// Mirrors base.model.create_ui (routes/mcp_route.go), the MCP-facing
// equivalent that goes through RBAC-gated dispatch instead of this direct
// runtime-method call path.
func createUISysModel(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("sys.model.create_ui requires ids")
		return nil, fmt.Errorf("sys.model.create_ui requires ids")
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, nil), ids, []string{"id", "name", "label", "bid", "status"})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return nil, fmt.Errorf("sys.model rows synced from Go code already have a UI; create_ui only applies to custom models (with a bid)")
		}
		if strings.TrimSpace(stringValue(row["status"])) != SysModelStatusPublished {
			return nil, fmt.Errorf("sys.model %q must be published before create_ui can run", strings.TrimSpace(stringValue(row["name"])))
		}
		name := strings.TrimSpace(stringValue(row["name"]))
		label := strings.TrimSpace(stringValue(row["label"]))
		if _, _, _, err := CreateUIForModel(ctx, name, label); err != nil {
			return nil, err
		}
	}
	return readSysModelRowsAfterAction(ctx, model, ids)
}

// runSysModelPublishJob runs orm.PublishModel for each (id, name) pair on a
// detached, tenant-bound context after the RPC handler has already returned
// the "job started" response. On success it does not need to write
// status=PUBLISHED itself: orm.PublishModel's own AutoMigrateRegisteredModels
// call ends in SyncModelRegistryToDB, whose upsert stamps every currently
// registered model (and its fields) PUBLISHED as a side effect (see
// orm.newSystemModelRow/newSystemModelFieldRows). On failure the row is
// reverted to INIT so it doesn't get stuck showing PUBLISHING forever.
func runSysModelPublishJob(ctx context.Context, ids, names []string) {
	for i, name := range names {
		id := ids[i]
		if _, err := orm.PublishModel(ctx, name); err != nil {
			log.Errorf("sys.model.publish: model %q: %v", name, err)
			if revertErr := setSysModelStatus(ctx, []string{id}, SysModelStatusInit); revertErr != nil {
				log.Errorf("sys.model.publish: revert status for %q: %v", name, revertErr)
			}
			notifySysModelJobResult(ctx, id, name, "publish", false, err)
			continue
		}
		notifySysModelJobResult(ctx, id, name, "publish", true, nil)
	}
}

// runSysModelUnpublishJob mirrors runSysModelPublishJob for orm.UnpublishModel.
// Unlike publish, UnpublishModel never runs a registry sync, so this job must
// explicitly stamp the model row and its field rows UNPUBLISHED itself.
func runSysModelUnpublishJob(ctx context.Context, ids, names []string) {
	for i, name := range names {
		id := ids[i]
		if err := orm.UnpublishModel(ctx, name); err != nil {
			log.Errorf("sys.model.unpublish: model %q: %v", name, err)
			if revertErr := setSysModelStatus(ctx, []string{id}, SysModelStatusPublished); revertErr != nil {
				log.Errorf("sys.model.unpublish: revert status for %q: %v", name, revertErr)
			}
			notifySysModelJobResult(ctx, id, name, "unpublish", false, err)
			continue
		}
		if err := setSysModelStatus(ctx, []string{id}, SysModelStatusUnpublished); err != nil {
			log.Errorf("sys.model.unpublish: set status for %q: %v", name, err)
		}
		if err := setSysModelFieldStatusByModelName(ctx, name, SysModelStatusUnpublished); err != nil {
			log.Errorf("sys.model.unpublish: set field status for %q: %v", name, err)
		}
		notifySysModelJobResult(ctx, id, name, "unpublish", true, nil)
	}
}

// detachedSysModelJobContext builds a background context bound to the same
// tenant database and caller identity as ctx, for use inside a goroutine
// that outlives the originating RPC request — the request's own ctx is
// cancelled once the handler returns the "job started" response. Mirrors
// internal/services/cron.Service.jobContext, this codebase's only other
// request-triggered background-job pattern.
func detachedSysModelJobContext(ctx context.Context) (context.Context, error) {
	env := orm.EnvironmentFromContext(ctx)
	var dbName string
	var userID int64
	if env != nil {
		dbName = env.DBName()
		userID = env.UserID()
	}
	if dbName == "" {
		db := orm.GetBunDB()
		if db == nil {
			return nil, fmt.Errorf("sys.model: default database is not initialized")
		}
		return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(db, userID, nil, true)), nil
	}
	registered, err := orm.EnsureNamedBunDB(dbName)
	if err != nil {
		return nil, fmt.Errorf("sys.model: connect database %q: %w", dbName, err)
	}
	if registered == nil || registered.DB == nil {
		return nil, fmt.Errorf("sys.model: database %q is not initialized", dbName)
	}
	return orm.ContextWithEnvironment(context.Background(), orm.NewEnvironmentWithDBName(registered.DB, dbName, userID, nil, true)), nil
}

// setSysModelStatus writes the sys.model status column for ids through the
// normal runtime write path (keeps updated_at/serialization consistent with
// every other sys.model mutation instead of a bespoke raw SQL update).
func setSysModelStatus(ctx context.Context, ids []string, status string) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(SysModelModelName, nil), ids, map[string]any{"status": status})
	return err
}

// setSysModelFieldStatusByModelName bulk-updates every sys.model.field row
// for one model by model_name rather than by id — there is no per-id write
// path suited to "all fields of this model" the way setSysModelStatus's
// RuntimeRepositoryWrite is id-scoped, so this mirrors
// makeHelperAndJSONForSysModelRecords's direct db.NewUpdate() pattern
// elsewhere in this file.
func setSysModelFieldStatusByModelName(ctx context.Context, modelName, status string) error {
	db := sysModelMetadataDB(ctx)
	if db == nil {
		return fmt.Errorf("sys.model.field database is not initialized")
	}
	_, err := db.NewUpdate().
		Model((*SysModelFieldRecord)(nil)).
		Set("status = ?", status).
		Set("updated_at = ?", time.Now().UTC()).
		Where("model_name = ?", modelName).
		Exec(ctx)
	return err
}

// notifySysModelJobResult publishes two base.service.bus events once a
// publish/unpublish/unlink job settles:
//  1. "simple_notification" — consumed automatically by the frontend's
//     existing bus-provider bridge (no bespoke frontend code needed, see
//     frontend-context.md) to toast the outcome.
//  2. "sys_model.status_changed" — a machine-readable event carrying the
//     record id/status so an open sys.model form can refresh itself instead
//     of polling.
//
// Both publishes run on ctx, which for the async jobs above is already bound
// to the correct tenant database — service-bus channels are scoped per
// tenant DB by that binding, not by the channel string, so this multi-tenant
// setup needs no per-tenant channel naming (see backend-context.md's
// base.service.bus section). Publish failures are logged only: the row's own
// status column (already durable) is the source of truth, this is best-effort
// UX.
func notifySysModelJobResult(ctx context.Context, id, name, action string, success bool, cause error) {
	notifType := "success"
	var title, message string
	switch {
	case success && action == "publish":
		title, message = "Model published", fmt.Sprintf("%q is now published and live.", name)
	case success && action == "unpublish":
		title, message = "Model unpublished", fmt.Sprintf("%q has been unpublished.", name)
	case success && action == "unlink":
		title, message = "Model deleted", fmt.Sprintf("%q has been deleted.", name)
	case !success && action == "publish":
		notifType, title, message = "error", "Publish failed", fmt.Sprintf("Publishing %q failed: %v", name, cause)
	case !success && action == "unpublish":
		notifType, title, message = "error", "Unpublish failed", fmt.Sprintf("Unpublishing %q failed: %v", name, cause)
	}

	simplePayload := map[string]any{"type": notifType, "title": title, "message": message}
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{[]string{"servicebus:global"}, "simple_notification", simplePayload}, nil); err != nil {
		log.Warnf("sys.model.%s: publish toast notification for %q: %v", action, name, err)
	}

	statusPayload := map[string]any{
		"model":   SysModelModelName,
		"id":      id,
		"name":    name,
		"action":  action,
		"success": success,
	}
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{[]string{"servicebus:global"}, "sys_model.status_changed", statusPayload}, nil); err != nil {
		log.Warnf("sys.model.%s: publish status event for %q: %v", action, name, err)
	}
}

// readSysModelRowsAfterAction re-reads the target rows so the object-button
// caller (the admin form) sees the fresh storage_backend/storage_name the
// publish/unpublish pipeline just changed, without a separate client-side
// reload. Shared by publishSysModel/unpublishSysModel only.
func readSysModelRowsAfterAction(ctx context.Context, model *orm.ModelMetadata, ids []string) (any, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 1 {
		return rows[0], nil
	}
	return rows, nil
}

// guardSysModelRowsCustom rejects the call when any target row has no
// bid, i.e. it is a registry-synced snapshot of a Go-coded model rather
// than a custom model. Returns each row's model name so callers that also
// need it (unlinkSysModel, to unregister a deleted published model) don't
// have to issue a second read.
func guardSysModelRowsCustom(ctx context.Context, ids []string, verb string) ([]string, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, nil), ids, []string{"id", "name", "bid"})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if strings.TrimSpace(stringValue(row["bid"])) == "" {
			return nil, fmt.Errorf("sys.model rows synced from Go code are read-only; only custom models (with a bid) can be %s", verb)
		}
		names = append(names, strings.TrimSpace(stringValue(row["name"])))
	}
	return names, nil
}

func makeHelperAndJSONForSysModelRecords(ctx context.Context, ids []string) error {
	if len(ids) == 0 {
		return nil
	}

	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(SysModelModelName, nil), ids, []string{"id", "name"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}

	db := sysModelMetadataDB(ctx)
	if db == nil {
		log.Errorf("sys.model database is not initialized")
		return fmt.Errorf("sys.model database is not initialized")
	}

	now := time.Now().UTC()
	for _, row := range rows {
		id := metadataTargetRecordID(row)
		if id == "" {
			continue
		}
		name := strings.TrimSpace(stringValue(row["name"]))
		helperData, jsonDTO := buildSysModelArtifacts(name)
		if _, err := db.NewUpdate().
			Model((*SysModelRecord)(nil)).
			Set("helper_data = ?", helperData).
			Set("json_dto = ?", jsonDTO).
			Set("updated_at = ?", now).
			Where("id = ?", row["id"]).
			Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

func buildSysModelArtifacts(modelName string) (string, map[string]any) {
	meta, ok := orm.LookupModel(modelName)
	if !ok || meta == nil {
		return "", nil
	}
	return orm.BuildModelHelperHTML(*meta), orm.BuildModelJSONDTO(*meta)
}

func sysModelMetadataDB(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	db := orm.GetBunDB()
	if db == nil {
		return nil
	}
	return db
}
