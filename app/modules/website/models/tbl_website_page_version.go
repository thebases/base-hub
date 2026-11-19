package models

import (
	"context"
	"fmt"
	"strconv"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const WebsitePageVersionModelName = "tbl.website_page_version"

// websitePageVersionKeep bounds snapshot history per page so publish stays cheap.
const websitePageVersionKeep = 30

// WebsitePageVersionRecord is an immutable snapshot of a page taken on publish.
// It is the rollback safety net for the builder: restore copies the snapshot
// back into the page draft; the live site only changes on the next publish.
type WebsitePageVersionRecord struct {
	orm.BaseModel
	PageID   *int64         `bun:"page_id" json:"page_id" meta:"kind:many2one,model:tbl.website_page,ondelete:CASCADE,index,required,label:Page,help:Page this snapshot belongs to."`
	SiteID   *int64         `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,label:Site,help:Owning site kept for scoping."`
	Revision int64          `bun:"revision" json:"revision" meta:"readonly,label:Page Revision,help:Page revision counter at the time of the snapshot."`
	BodyHTML string         `bun:"body_html" json:"body_html" meta:"readonly,label:Body HTML,help:Snapshot of the page body HTML."`
	CSS      string         `bun:"custom_css" json:"custom_css" meta:"kind:text,readonly,label:Custom CSS,help:Snapshot of the page custom CSS."`
	Seo      map[string]any `bun:"seo,type:jsonb,nullzero" json:"seo" meta:"kind:json,readonly,label:SEO,help:Snapshot of the page SEO."`
	Label    string         `bun:"label" json:"label" meta:"label:Label,help:Short note describing the snapshot."`
}

var WebsitePageVersionModel = orm.Define[WebsitePageVersionRecord](&Models, 430, orm.ModelSpec{
	Name:        WebsitePageVersionModelName,
	Label:       "Website Page Versions",
	Description: "Publish-time snapshots of website pages for restore",
})

// snapshotWebsitePage stores the page's current draft as a version row and
// prunes history beyond websitePageVersionKeep. Runs inside the publish flow,
// so a snapshot failure aborts the publish rather than losing the safety net.
func snapshotWebsitePage(ctx context.Context, pageRow orm.RecordMap, label string) error {
	pageID := anyToInt64(pageRow["id"])
	if pageID <= 0 {
		return nil
	}
	values := orm.RecordMap{
		"page_id":   pageID,
		"site_id":   pageRow["site_id"],
		"revision":  anyToInt64(pageRow["revision"]),
		"body_html": stringValue(pageRow["body_html"]),
		"label":     label,
	}
	if css := stringValue(pageRow["custom_css"]); css != "" {
		values["custom_css"] = css
	}
	if seo, ok := pageRow["seo"].(map[string]any); ok && len(seo) > 0 {
		values["seo"] = seo
	}
	ref := orm.RuntimeModelRef(WebsitePageVersionModelName, nil)
	if _, err := orm.RuntimeRepositoryCreate(ctx, ref, values); err != nil {
		return fmt.Errorf("snapshot website page %d: %w", pageID, err)
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, ref, orm.RecordMap{
		"page_id": pageID, "__order": "id desc", "__limit": 500,
	}, []string{"id"})
	if err != nil || len(rows) <= websitePageVersionKeep {
		return err
	}
	stale := make([]string, 0, len(rows)-websitePageVersionKeep)
	for _, row := range rows[websitePageVersionKeep:] {
		stale = append(stale, strconv.FormatInt(anyToInt64(row["id"]), 10))
	}
	_, err = orm.RuntimeRepositoryUnlink(ctx, ref, stale)
	return err
}

func listWebsitePageVersions(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.website_page.list_versions requires ids")
	}
	pageID, err := strconv.ParseInt(ids[0], 10, 64)
	if err != nil || pageID <= 0 {
		return nil, fmt.Errorf("tbl.website_page.list_versions: invalid page id %q", ids[0])
	}
	return orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageVersionModelName, nil), orm.RecordMap{
		"page_id": pageID, "__order": "id desc", "__limit": websitePageVersionKeep,
	}, []string{"id", "revision", "label", "create_date"})
}

// actionRestoreWebsitePageVersion copies a snapshot back into the page draft.
// The snapshot must belong to the page (checked server-side so an id from
// another page or site can never be applied).
func actionRestoreWebsitePageVersion(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.action_restore_version requires ids")
	}
	versionID, _ := int64FromAny(kwargs["version_id"])
	if versionID <= 0 && len(args) > 0 {
		versionID, _ = int64FromAny(args[0])
	}
	if versionID <= 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.action_restore_version requires version_id")
	}
	pageID, err := strconv.ParseInt(ids[0], 10, 64)
	if err != nil || pageID <= 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.action_restore_version: invalid page id %q", ids[0])
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageVersionModelName, nil), orm.RecordMap{
		"id": versionID, "page_id": pageID, "__limit": 1,
	}, nil)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, orm.NotFoundf("tbl.website_page_version %d not found for page %d", versionID, pageID)
	}
	snap := rows[0]
	// Snapshot rows are not trusted: sanitize on the way back in, and bump the revision through the
	// same CAS used by normal writes so a builder tab opened before the restore cannot overwrite it.
	values := orm.RecordMap{"body_html": stringValue(snap["body_html"])}
	if err := sanitizeWebsiteHTMLFields(values, "body_html"); err != nil {
		return nil, err
	}
	values["custom_css"] = stringValue(snap["custom_css"])
	if err := sanitizeWebsiteCustomCSSField(values); err != nil {
		return nil, err
	}
	if seo, ok := snap["seo"].(map[string]any); ok {
		values["seo"] = seo
	}
	// The revision bump and the write share one transaction (see applyWebsiteRevisionCAS); only the
	// page the snapshot was checked against is written.
	if err := runInWebsiteTx(ctx, func(txCtx context.Context, _ bun.IDB) error {
		if err := applyWebsiteRevisionCAS(txCtx, WebsitePageModelName, ids[0], values); err != nil {
			return err
		}
		_, err := orm.RuntimeRepositoryWrite(txCtx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids[:1], values)
		return err
	}); err != nil {
		return nil, err
	}
	return map[string]any{"page_id": pageID, "version_id": versionID}, nil
}
