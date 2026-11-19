package modules

// File overview:
// - Responsibility: bumps the end-user frontend's version.json and pushes a live bus event after a module install/uninstall, so an open tab's service worker purges its offline cache instead of waiting for its next full page load.
// - Key symbols: `BumpFrontendVersion`.
// - Pattern: Mirrors scripts/write-frontend-version.mjs's build-time stamp format so both the build pipeline and this runtime path produce version strings the frontend's identical comparison logic already understands.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// frontendVersionPath mirrors resolveUserFEStaticAssets' disk fallback
// ("./frontend", i.e. app/frontend when the process runs from
// app). internal/modules cannot import internal/cmd (which imports
// this package), so the path is duplicated here rather than shared — same
// self-contained approach reloadModuleTranslations already takes with
// EmbeddedFS(). Writing this file only matters for disk-served deployments;
// an embedded production binary serves version.json from its compiled-in
// filesystem snapshot, which this write cannot change.
const frontendVersionPath = "frontend/version.json"

// frontendVersionUpdatedEvent is the bus notification type the end-user
// frontend's bus-root.js subscribes to (handleFrontendVersionUpdated), which
// calls the same applyServerVersion() purge+reload path as its normal
// on-load version.json check.
const frontendVersionUpdatedEvent = "app.frontend_version_updated"

// BumpFrontendVersion stamps a fresh build id into the end-user frontend's
// version.json and publishes it over the global service-bus channel, so a
// module install/uninstall (which can change seeded data, menus, and views
// the frontend renders) is reflected without requiring a manual hard
// refresh. Both steps are best-effort: a write or publish failure is logged
// and swallowed rather than failing the install/uninstall it was triggered
// from, matching reloadModuleTranslations's error-handling stance.
func BumpFrontendVersion(ctx context.Context) {
	version := fmt.Sprintf("base-%s", time.Now().UTC().Format("20060102-150405"))

	if err := writeFrontendVersionFile(version); err != nil {
		log.Warnf("modules: write %s: %v", frontendVersionPath, err)
	}

	channels := []string{"servicebus:global"}
	payload := map[string]any{"version": version}
	if _, err := orm.ExecuteRuntimeMethod(ctx, "base.service.bus", "publish", nil, []any{channels, frontendVersionUpdatedEvent, payload}, nil); err != nil {
		log.Warnf("modules: publish %s: %v", frontendVersionUpdatedEvent, err)
	}
}

// writeFrontendVersionFile writes the same {"version": "..."} shape
// scripts/write-frontend-version.mjs produces at build time.
func writeFrontendVersionFile(version string) error {
	payload := fmt.Sprintf("{\"version\": %q}\n", version)
	if err := os.MkdirAll(filepath.Dir(frontendVersionPath), 0o755); err != nil {
		return err
	}
	return os.WriteFile(frontendVersionPath, []byte(payload), 0o644)
}
