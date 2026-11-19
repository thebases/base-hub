package models

// File overview:
// - Responsibility: scheduled publish (`publish_at`) and redirect-on-rename for pages.
// - Key symbols: `normalizeWebsitePublishAt`, `publishDueWebsitePages`, `InitPublishCron`,
//   `createRenameRedirect`.
// - Pattern: `publish_pending` is the only column the scheduler filters on (equality search), so a
//   due scan never reads unscheduled pages; the due comparison itself happens in Go.

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/cron"
	"git.thebase.vn/base_hub/internal/services/log"
)

// websitePublishCronKey is the sys.cron `handler` value seeded by data/06_sys_cron_publish_due.json.
const websitePublishCronKey = "website.publish_due_pages"

// websiteRedirectOnRenameKey is a write-only flag in page values: when true and the write changes
// the page route, a 301 from the old route to the new one is created. It is never persisted.
const websiteRedirectOnRenameKey = "redirect_old_url"

// normalizeWebsitePublishAt validates `publish_at` and keeps `publish_pending` consistent with it:
// a non-empty future-or-past instant arms the scheduler, an empty value disarms it. Absent keys are
// left alone so content-only writes never touch the schedule.
func normalizeWebsitePublishAt(values orm.RecordMap) error {
	raw, ok := values["publish_at"]
	if !ok {
		return nil
	}
	text := strings.TrimSpace(stringValue(raw))
	if raw == nil || text == "" {
		values["publish_at"] = ""
		values["publish_pending"] = false
		return nil
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return fmt.Errorf("tbl.website_page publish_at must be an RFC3339 timestamp: %w", err)
	}
	values["publish_at"] = parsed.UTC().Format(time.RFC3339)
	values["publish_pending"] = true
	return nil
}

// publishDueWebsitePages publishes every armed page whose time has come and disarms it. A page that
// fails to publish stays armed (retried next tick) and does not block the others.
func publishDueWebsitePages(ctx context.Context) (int, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"publish_pending": true, "__limit": 200,
	}, []string{"id", "publish_at"})
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	published := 0
	for _, row := range rows {
		due, err := time.Parse(time.RFC3339, stringValue(row["publish_at"]))
		if err != nil || due.After(now) {
			continue
		}
		id := strconv.FormatInt(anyToInt64(row["id"]), 10)
		if _, err := orm.ExecuteRuntimeMethod(ctx, WebsitePageModelName, "action_publish", []string{id}, nil, nil); err != nil {
			log.Warnf("website: scheduled publish of page %s failed: %v", id, err)
			continue
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), []string{id}, orm.RecordMap{"publish_pending": false}); err != nil {
			log.Warnf("website: could not disarm scheduled page %s: %v", id, err)
		}
		published++
	}
	return published, nil
}

// InitPublishCron registers the scheduled-publish handler with the kernel scheduler.
func InitPublishCron() func() {
	if err := cron.RegisterHandler(websitePublishCronKey, func(ctx context.Context, _ map[string]any) error {
		_, err := publishDueWebsitePages(ctx)
		return err
	}); err != nil {
		log.Fatalf("website: register cron handler: %v", err)
	}
	return func() { cron.UnregisterHandler(websitePublishCronKey) }
}

// popRedirectOnRename removes the write-only flag from values and reports whether it was set.
func popRedirectOnRename(values orm.RecordMap) bool {
	on := boolFromRow(values[websiteRedirectOnRenameKey])
	delete(values, websiteRedirectOnRenameKey)
	return on
}

// createRenameRedirect records a 301 from the old route to the new one. A failure (for example an
// existing redirect for that path) is returned so the caller can surface it, but the rename itself
// has already been written.
func createRenameRedirect(ctx context.Context, siteID int64, oldRoute, newRoute string) error {
	if oldRoute == "" || newRoute == "" || oldRoute == newRoute || oldRoute == "/" {
		return nil
	}
	_, err := orm.ExecuteRuntimeMethod(ctx, WebsiteRedirectModelName, "create", nil, nil, map[string]any{
		"values": map[string]any{
			"site_id": siteID, "url_from": oldRoute, "url_to": newRoute, "redirect_type": "301", "is_active": true,
		},
	})
	return err
}
