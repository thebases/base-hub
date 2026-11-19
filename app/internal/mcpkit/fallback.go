package mcpkit

// File overview:
// - Responsibility: the fallback pipeline for a model/method/tool lookup miss --
//   (1) a best-effort knowledge-search hint attached to the *_not_found error, and
//   (2) a debounced module-catalog resync triggered by the miss, then one lookup retry.
// - Key symbols: ResolveModel, ResolveRuntimeMethod, NotFoundWithHint, KnowledgeHint,
//   TriggerCatalogReload, ResetCatalogReloadDebounce.
// - Moved here (originally modules/base-mcp/routes/mcp_route_fallback.go) so every
//   tool-contributing module's mcp/dispatch.go shares the exact same fallback
//   behavior without importing modules/base-mcp -- kernelmodules.KnowledgeDocumentProviderHooks
//   is reachable directly from this internal/* package, same as base-mcp reached it.
// - IMPORTANT, read before extending this: orm.LookupModel reads a pure in-memory map
//   (defaultModelRegistry) populated only by each module's compile-time
//   orm.RegisterModelType call at process init, or orm.PublishModel for a custom/dynamic
//   model (which already registers it live at publish time). orm.SyncModuleCatalog only
//   writes manifest data into sys_module/sys_model DB rows -- it never touches
//   defaultModelRegistry, so the retry after a reload here will NOT turn a genuine
//   model_not_found into a hit; it is kept only as legitimate, harmless housekeeping tied
//   to the miss (keeps catalog rows fresh for anything that reads them).

import (
	"context"
	"fmt"
	"sync"
	"time"

	kernelmodules "git.thebase.vn/base_hub/internal/modules"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

var (
	catalogReloadMu     sync.Mutex
	catalogLastReloadAt time.Time
)

// CatalogReloadDebounce bounds how often a lookup miss can trigger a real
// manifest rescan -- a burst of calls with a bad/misspelled model name must
// not turn into a repeated full catalog sync on a hot request path.
const CatalogReloadDebounce = 30 * time.Second

// ResetCatalogReloadDebounce clears the debounce timestamp so the next
// lookup-miss reload (or an explicit cache-invalidation call) isn't
// throttled by a resync that happened before the state change this is
// reacting to. Called by modules/base-mcp/routes.InvalidateMCPCache.
func ResetCatalogReloadDebounce() {
	catalogReloadMu.Lock()
	catalogLastReloadAt = time.Time{}
	catalogReloadMu.Unlock()
}

// TriggerCatalogReload re-syncs the module manifest catalog via
// orm.SyncModuleCatalog. See the file header: this refreshes sys_module/
// sys_model DB rows, it does NOT and cannot make a previously-unregistered
// model appear in orm.LookupModel's in-memory registry -- it's housekeeping
// tied to the miss, not a fix for the miss itself. Debounced; errors are
// logged, never surfaced to the MCP caller.
func TriggerCatalogReload(ctx context.Context) {
	catalogReloadMu.Lock()
	if time.Since(catalogLastReloadAt) < CatalogReloadDebounce {
		catalogReloadMu.Unlock()
		return
	}
	catalogLastReloadAt = time.Now()
	catalogReloadMu.Unlock()

	if _, _, err := orm.SyncModuleCatalog(ctx); err != nil {
		log.Warnf("mcp: module catalog reload after lookup miss failed: %v", err)
	}
}

// KnowledgeHint best-effort looks up a knowledge document related to term so
// a *_not_found error can point the caller somewhere useful instead of a bare
// 404. Never blocks or fails the caller's original error on a knowledge
// lookup problem -- returns "" on any failure, empty result, or when
// knowledge is disabled/not installed for this tenant. Mirrors the exact gate
// modules/base-mcp/routes's knowledgeModuleEnabled applied before this moved
// here: fails closed on both "not installed for this tenant DB" and "no
// provider registered" (module not bootstrapped/compiled in).
func KnowledgeHint(ctx context.Context, identity *orm.RBACIdentity, term string) string {
	dbName := ""
	if identity != nil {
		dbName = identity.DBName
	}
	if installed, err := queries.ModuleInstalled(ctx, orm.ResolvePGXPool(dbName), "knowledge"); err != nil || !installed {
		return ""
	}
	provider, ok := kernelmodules.KnowledgeDocumentProviderHooks()
	if !ok {
		return ""
	}
	items, err := provider.SearchDocuments(ctx, dbName, term)
	if err != nil || len(items) == 0 {
		return ""
	}
	return fmt.Sprintf("see also %q", items[0].RelativePath)
}

// NotFoundWithHint builds a *_not_found ToolError, appending a
// knowledge-search hint for term when one is available.
func NotFoundWithHint(ctx context.Context, identity *orm.RBACIdentity, code, message, term string) error {
	if hint := KnowledgeHint(ctx, identity, term); hint != "" {
		message = fmt.Sprintf("%s (%s)", message, hint)
	}
	return NewToolError(fiber.StatusNotFound, code, message)
}

// ResolveModel is the fallback-aware replacement for a bare
// orm.LookupModel(modelName) call at every caller-supplied-model-name entry
// point: a miss triggers a debounced catalog resync (housekeeping only, see
// TriggerCatalogReload) and one retry -- which will only ever succeed for a
// genuinely racy registration, not a truly nonexistent model -- before
// finally returning model_not_found with a knowledge-search hint attached.
func ResolveModel(ctx context.Context, identity *orm.RBACIdentity, modelName string) (*orm.ModelMetadata, error) {
	if meta, ok := orm.LookupModel(modelName); ok {
		return meta, nil
	}
	TriggerCatalogReload(ctx)
	if meta, ok := orm.LookupModel(modelName); ok {
		return meta, nil
	}
	return nil, NotFoundWithHint(ctx, identity, "model_not_found", fmt.Sprintf("model %q is not registered", modelName), modelName)
}

// ResolveRuntimeMethod layers a method lookup on top of ResolveModel, for the
// caller-supplied-method-name entry points (base.record.call_method and every
// <group>.record.* generic dispatcher).
func ResolveRuntimeMethod(ctx context.Context, identity *orm.RBACIdentity, modelName, methodName string) (*orm.ModelMetadata, orm.RuntimeMethod, error) {
	meta, err := ResolveModel(ctx, identity, modelName)
	if err != nil {
		return nil, orm.RuntimeMethod{}, err
	}
	method, ok := meta.LookupRuntimeMethod(methodName)
	if !ok {
		return nil, orm.RuntimeMethod{}, NotFoundWithHint(ctx, identity, "method_not_found",
			fmt.Sprintf("runtime method %q is not registered on model %q", methodName, modelName), methodName)
	}
	return meta, method, nil
}
