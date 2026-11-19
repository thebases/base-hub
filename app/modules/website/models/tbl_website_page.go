package models

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const WebsitePageModelName = "tbl.website_page"

type WebsitePageRecord struct {
	orm.BaseModel
	SiteID          *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,required,label:Site,help:Links this page to its owning site."`
	CompanyID       *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Inherits the company scope from the selected website site. Empty means the page belongs to the shared main site."`
	Name            string `bun:"name" json:"name" meta:"index,required,label:Name,help:Stores the editor-facing page name."`
	Slug            string `bun:"slug" json:"slug" meta:"index,required,label:Slug,help:Stores the page slug for editorial reference."`
	RoutePath       string `bun:"route_path" json:"route_path" meta:"index,required,label:Route Path,help:Stores the public route path for the page."`
	PageTemplate    string `bun:"page_template" json:"page_template" meta:"label:Template,help:Key of the page template (module/file under static/templates) the page was created from. Applied once on create when the page has no content."`
	IsHomepage      bool   `bun:"is_homepage,notnull,default:false" json:"is_homepage" meta:"label:Homepage,help:Controls whether this page is the site homepage."`
	LastPublishedAt string `bun:"last_published_at" json:"last_published_at" meta:"readonly,label:Last Published At,help:Stores when this page was last published."`
	DeployPath      string `bun:"deploy_path" json:"deploy_path" meta:"readonly,label:Deploy Path,help:Stores the absolute path of this page's currently deployed Owl scaffold folder (index.html + <slug>.js/.xml/.css). Empty when the page is unpublished."`

	Seo      map[string]any `bun:"seo,type:jsonb,nullzero" json:"seo" meta:"kind:json,label:SEO,help:Stores nested page SEO fields (title/description/canonicalUrl/robots/openGraph/structuredData/includeInSitemap/changeFrequency/priority)."`
	Revision int64          `bun:"revision,notnull,default:1" json:"revision" meta:"readonly,label:Revision,help:Stores the optimistic-concurrency counter bumped on every write."`

	// Builder content model: sanitized HTML is the canonical page content.
	BodyHTML       string `bun:"body_html" json:"body_html" meta:"label:Body HTML,help:Sanitized HTML fragment authored by the website builder. Sanitized server-side on every write."`
	CustomCSS      string `bun:"custom_css" json:"custom_css" meta:"kind:text,label:Custom CSS,help:Page-specific stylesheet published next to the page. Validated on write: no @import, no script-capable constructs, only same-site or https urls."`
	IsIndexed      bool   `bun:"is_indexed,notnull,default:true" json:"is_indexed" meta:"label:Indexed,help:When off the page is left out of the sitemap."`
	PublishAt      string `bun:"publish_at" json:"publish_at" meta:"label:Publish At,help:RFC3339 time at which the scheduler publishes this page. Empty means no schedule."`
	PublishPending bool   `bun:"publish_pending,notnull,default:false" json:"publish_pending" meta:"readonly,index,label:Publish Pending,help:True while a scheduled publish is armed; cleared once it has run."`
	Visibility     string `bun:"visibility,notnull,default:'public'" json:"visibility" meta:"label:Visibility,help:Who can read the published page. Logged-in pages are served only to signed-in users: their content is withheld from anonymous requests and left out of the sitemap." selection:"(public,Public),(logged_in,Logged-in users only)"`
	IsInMenu       bool   `bun:"is_in_menu,notnull,default:false" json:"is_in_menu" meta:"label:In Menu,help:Marks the page as a candidate entry for the site navigation menu."`
}

var WebsitePageModel = orm.Define[WebsitePageRecord](&Models, 420, orm.ModelSpec{
	Name:        WebsitePageModelName,
	Label:       "Website Pages",
	Description: "Stable page identities, routes, and current draft pointers for website sites",
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createWebsitePage,
		Write:  writeWebsitePage,
		Unlink: unlinkWebsitePage,
	},
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "list_page_templates", Public: true, ModelOnly: true, ReadOnly: true, Call: listWebsitePageTemplates, Description: "Lists the page templates shipped by installed modules (static/templates/*.xml)."},
		{Name: "action_publish", Public: true, UsesPathID: true, Call: actionPublishWebsitePage},
		{Name: "action_unpublish", Public: true, UsesPathID: true, Call: actionUnpublishWebsitePage},
		{Name: "action_set_homepage", Public: true, UsesPathID: true, Call: actionSetHomepageWebsitePage},
		{Name: "list_versions", Public: true, UsesPathID: true, ReadOnly: true, Call: listWebsitePageVersions, Description: "Lists the publish snapshots of a page (newest first)."},
		{Name: "action_restore_version", Public: true, UsesPathID: true, Call: actionRestoreWebsitePageVersion, Description: "Restores the page draft from a snapshot (kwargs version_id); the live site is unchanged until republish."},
	},
})

func init() {
	orm.RegisterOnchange(WebsitePageModelName, []string{"name", "is_homepage", "site_id"}, websitePageRouteOnchange)
}

func createWebsitePage(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("tbl.website_page.create requires values")
	}
	working := cloneRecordMap(values)
	// Server-managed: publish sets deploy_path, normalizeWebsitePublishAt below derives publish_pending
	// from publish_at, and a new page starts at the default revision.
	stripWebsiteServerManagedFields(working, "deploy_path", "revision", "publish_pending")
	if err := sanitizeWebsiteHTMLFields(working, "body_html"); err != nil {
		return nil, err
	}
	if err := sanitizeWebsiteCustomCSSField(working); err != nil {
		return nil, err
	}
	popRedirectOnRename(working)
	if err := normalizeWebsitePublishAt(working); err != nil {
		return nil, err
	}
	if err := normalizeWebsitePageValues(ctx, working, true); err != nil {
		return nil, err
	}
	if err := validateWebsitePageValues(ctx, "", working, true); err != nil {
		return nil, err
	}
	if err := applyWebsitePageTemplate(ctx, working); err != nil {
		return nil, err
	}
	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(WebsitePageModelName, model), working)
	if err != nil {
		return nil, websitePageRouteConflict(err, working)
	}
	return created, nil
}

func writeWebsitePage(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("tbl.website_page.write requires values")
	}
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.write requires ids")
	}
	// Every id is normalized, validated and revision-checked on its own (deriving the route once from
	// ids[0] gave every page of a multi-id rename the same route), and all of them commit together with
	// their revision bumps.
	err = runInWebsiteTx(ctx, func(txCtx context.Context, _ bun.IDB) error {
		for _, id := range ids {
			if err := writeOneWebsitePage(txCtx, model, strings.TrimSpace(id), values); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return true, nil
}

// writeOneWebsitePage applies the write hook rules to one page. Must run inside runInWebsiteTx.
func writeOneWebsitePage(ctx context.Context, model *orm.ModelMetadata, id string, values orm.RecordMap) error {
	working := cloneRecordMap(values)
	// revision stays as the client's CAS token (applyWebsiteRevisionCAS overwrites it); publish_pending
	// is re-derived from publish_at by normalizeWebsitePublishAt.
	stripWebsiteServerManagedFields(working, "deploy_path", "publish_pending")
	if err := sanitizeWebsiteHTMLFields(working, "body_html"); err != nil {
		return err
	}
	if err := sanitizeWebsiteCustomCSSField(working); err != nil {
		return err
	}
	redirectOnRename := popRedirectOnRename(working)
	if err := normalizeWebsitePublishAt(working); err != nil {
		return err
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, model), []string{id}, []string{"site_id", "name", "is_homepage", "route_path"})
	if err != nil {
		return err
	}
	stored, err := firstRow(rows, "tbl.website_page "+id)
	if err != nil {
		return err
	}
	if !hasRecordMapKey(working, "site_id") {
		working["site_id"] = stored["site_id"]
	}
	if hasRecordMapKey(working, "name") || hasRecordMapKey(working, "is_homepage") {
		// The route is derived from name AND is_homepage: a payload carrying only one of them takes the
		// other from the stored row (a rename of the homepage must keep "/", and toggling is_homepage
		// alone must not derive a route from an empty name).
		for _, key := range []string{"name", "is_homepage"} {
			if !hasRecordMapKey(working, key) {
				working[key] = stored[key]
			}
		}
	}
	if err := normalizeWebsitePageValues(ctx, working, false); err != nil {
		return err
	}
	if err := validateWebsitePageValues(ctx, id, working, false); err != nil {
		return err
	}
	if err := applyWebsiteRevisionCAS(ctx, WebsitePageModelName, id, working); err != nil {
		return err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, model), []string{id}, working); err != nil {
		return websitePageRouteConflict(err, working)
	}
	if redirectOnRename {
		siteID, _ := int64FromAny(working["site_id"])
		// Same transaction: when the redirect cannot be created the rename rolls back with it.
		if err := createRenameRedirect(ctx, siteID, stringValue(stored["route_path"]), stringValue(working["route_path"])); err != nil {
			return fmt.Errorf("page not renamed: the redirect from %s could not be created: %w", stringValue(stored["route_path"]), err)
		}
	}
	return nil
}

// websitePageRouteConflict maps a hit on the (site_id, route_path) unique index to a Conflict.
func websitePageRouteConflict(err error, values orm.RecordMap) error {
	return websiteUniqueViolation(err, websitePageRouteIndex,
		fmt.Sprintf("tbl.website_page route_path %q already exists in this site", stringValue(values["route_path"])))
}

// normalizeWebsitePageValues only recomputes slug/route_path when the payload
// actually touches page identity (name/is_homepage) or on create. This keeps
// content-only writes -- the the legacy editor adapter's autosave patches, which
// carry only body_html/seo/custom_css/custom_js -- from blanking
// out an existing page's slug/route_path.
func normalizeWebsitePageValues(ctx context.Context, values orm.RecordMap, isCreate bool) error {
	if isCreate || hasRecordMapKey(values, "name") || hasRecordMapKey(values, "is_homepage") {
		isHomepage := boolFromRow(values["is_homepage"])
		name := stringValue(values["name"])
		values["slug"] = derivedWebsitePageSlug(name)
		values["route_path"] = derivedWebsitePageRoute(name, isHomepage)
	}
	if err := inheritWebsitePageCompanyID(ctx, values); err != nil {
		return err
	}
	return nil
}

func validateWebsitePageValues(ctx context.Context, currentID string, values orm.RecordMap, isCreate bool) error {
	siteID, ok := int64FromAny(values["site_id"])
	if !ok || siteID <= 0 {
		return orm.InvalidParamsf("tbl.website_page site_id is required")
	}
	if !isCreate && !hasRecordMapKey(values, "name") && !hasRecordMapKey(values, "is_homepage") {
		// Content-only write: identity fields (name/slug/route_path) were not
		// touched by normalizeWebsitePageValues, so there's nothing to validate.
		return nil
	}
	if stringValue(values["name"]) == "" {
		return orm.InvalidParamsf("tbl.website_page name is required")
	}
	routePath := stringValue(values["route_path"])
	if routePath == "" {
		return orm.InvalidParamsf("tbl.website_page route_path is required")
	}

	// Route uniqueness is enforced at the model layer so publish does not have
	// to discover collisions only after editors already saved conflicting drafts.
	// Checked without record rules (a page the caller cannot see still owns its
	// route); websitePageRouteIndex catches writers racing past this read.
	rows, err := orm.RuntimeRepositorySearchRead(websiteSudoContext(ctx), orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id":    siteID,
		"route_path": routePath,
		"__limit":    5,
	}, []string{"id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		rowID := strconv.FormatInt(anyToInt64(row["id"]), 10)
		if currentID != "" && rowID == currentID {
			continue
		}
		return orm.Conflictf("tbl.website_page route_path %q already exists in this site", routePath)
	}
	return nil
}

// websitePageRouteOnchange previews the derived slug and route path so the form stays
// aligned with the backend write behavior before the record is saved.
func websitePageRouteOnchange(ctx context.Context, _ orm.RecordSet, values orm.RecordMap, _ []string, _ map[string]any) (orm.RecordMap, error) {
	name := stringValue(values["name"])
	isHomepage := boolFromRow(values["is_homepage"])
	updates := orm.RecordMap{
		"slug":       derivedWebsitePageSlug(name),
		"route_path": derivedWebsitePageRoute(name, isHomepage),
		"company_id": nil,
	}
	siteID, ok := int64FromAny(values["site_id"])
	if !ok || siteID <= 0 {
		return updates, nil
	}
	siteRow, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return nil, err
	}
	updates["company_id"] = siteRow["company_id"]
	return updates, nil
}

func inheritWebsitePageCompanyID(ctx context.Context, values orm.RecordMap) error {
	siteID, ok := int64FromAny(values["site_id"])
	if !ok || siteID <= 0 {
		return nil
	}
	siteRow, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return err
	}
	values["company_id"] = siteRow["company_id"]
	return nil
}

// derivedWebsitePageRoute centralizes the name -> slug -> route_path rule so onchange,
// create, and write all keep website pages on the same public URL pattern.
func derivedWebsitePageRoute(name string, isHomepage bool) string {
	if isHomepage {
		return "/"
	}
	slug := derivedWebsitePageSlug(name)
	if slug == "" {
		return ""
	}
	return normalizeRoutePath("/"+slug, false)
}

// derivedWebsitePageSlug keeps live form state blank until a real page name exists,
// while still reusing the repo's slug normalization once authors type a value.
func derivedWebsitePageSlug(name string) string {
	name = stringValue(name)
	if name == "" {
		return ""
	}
	return slugify(name)
}

// actionSetHomepageWebsitePage makes this page the site's homepage: it clears
// is_homepage on any other page of the same site currently holding it (and
// recomputes their route_path back to /<slug>, mirroring normalizeWebsitePageValues'
// create/write rule since this bypasses the standard write hook), sets
// is_homepage/route_path("/") on the target page, and points
// tbl.website_site.home_page_id at it so runtime_route.go's "/" lookup resolves here.
func actionSetHomepageWebsitePage(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("tbl.website_page.action_set_homepage requires ids")
	}
	pageRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids, nil)
	if err != nil {
		return nil, err
	}
	pageRow, err := firstRow(pageRows, "tbl.website_page")
	if err != nil {
		return nil, err
	}
	siteID := anyToInt64(pageRow["site_id"])
	if siteID <= 0 {
		return nil, fmt.Errorf("tbl.website_page %v has no site_id", pageRow["id"])
	}
	pageID := anyToInt64(pageRow["id"])

	if boolFromRow(pageRow["is_homepage"]) {
		return pageRow, nil
	}

	siblingRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id":     siteID,
		"is_homepage": true,
		"__limit":     1000,
	}, []string{"id", "name"})
	if err != nil {
		return nil, err
	}
	for _, sibling := range siblingRows {
		siblingID := anyToInt64(sibling["id"])
		if siblingID <= 0 || siblingID == pageID {
			continue
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), []string{strconv.FormatInt(siblingID, 10)}, orm.RecordMap{
			"is_homepage": false,
			"route_path":  derivedWebsitePageRoute(stringValue(sibling["name"]), false),
		}); err != nil {
			return nil, err
		}
	}

	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids, orm.RecordMap{
		"is_homepage": true,
		"route_path":  "/",
	}); err != nil {
		return nil, err
	}
	pageRow["is_homepage"] = true
	pageRow["route_path"] = "/"

	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), []string{strconv.FormatInt(siteID, 10)}, orm.RecordMap{
		"home_page_id": pageID,
	}); err != nil {
		return nil, err
	}

	return pageRow, nil
}

func actionPublishWebsitePage(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.action_publish requires ids")
	}
	_, siteID, err := readWebsitePageForPublish(ctx, ids)
	if err != nil {
		return nil, err
	}
	// Same site-row lock as site publish/unpublish: a page publish rewrites the site's root scaffold
	// (route table, sitemap) and may create its deploy folder, so it must not interleave with them.
	var published orm.RecordMap
	var staleFolder string
	if err := runInWebsiteSiteTx(ctx, siteID, func(txCtx context.Context) error {
		// Re-read under the lock so the publish renders the row as it is now, not as before the wait.
		locked, _, err := readWebsitePageForPublish(txCtx, ids)
		if err != nil {
			return err
		}
		published, staleFolder, err = publishWebsitePage(txCtx, ids, locked, true)
		return err
	}); err != nil {
		return nil, err
	}
	removeStaleWebsiteFolders(ctx, staleFolder)
	return published, nil
}

func actionUnpublishWebsitePage(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_page.action_unpublish requires ids")
	}
	_, siteID, err := readWebsitePageForPublish(ctx, ids)
	if err != nil {
		return nil, err
	}
	var unpublished orm.RecordMap
	if err := runInWebsiteSiteTx(ctx, siteID, func(txCtx context.Context) error {
		locked, _, err := readWebsitePageForPublish(txCtx, ids)
		if err != nil {
			return err
		}
		unpublished, err = unpublishWebsitePage(txCtx, ids, locked)
		return err
	}); err != nil {
		return nil, err
	}
	return unpublished, nil
}

// readWebsitePageForPublish reads the first page of ids and its site id.
func readWebsitePageForPublish(ctx context.Context, ids []string) (orm.RecordMap, int64, error) {
	pageRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids[:1], nil)
	if err != nil {
		return nil, 0, err
	}
	pageRow, err := firstRow(pageRows, "tbl.website_page")
	if err != nil {
		return nil, 0, err
	}
	siteID := anyToInt64(pageRow["site_id"])
	if siteID <= 0 {
		return nil, 0, orm.InvalidParamsf("tbl.website_page %v has no site_id", pageRow["id"])
	}
	return pageRow, siteID, nil
}

// removeStaleWebsiteFolders deletes deploy folders a publish replaced. Callers run it only after
// the publish transaction committed: the old folder stays live (and is still what the DB points
// at) until then, so a failed publish leaves the site serving the previous version. Best-effort:
// a leftover folder is unreachable garbage, not an error.
func removeStaleWebsiteFolders(ctx context.Context, folders ...string) {
	for _, folder := range folders {
		if folder == "" {
			continue
		}
		if err := deleteWebsiteStaticFile(ctx, folder); err != nil {
			log.Warnf("website: could not remove previous deployed folder %q: %v", folder, err)
		}
	}
}

// publishWebsitePage builds this page's current draft into an Owl component
// scaffold (<Name>.js/.xml/.css, no per-page index.html) and deploys it under
// pages/<Name>/ inside the owning site's DeployPath folder, using the same
// flat structure the legacy editor's export function produces. Shared by the
// page's own action_publish and tbl.website_site's action_publish loop.
// When regenerateRoot is true, the site's root scaffold (index.html/main.js/
// manifest.json/sitemap.xml/styles.css/sw.js) is rebuilt afterward so its
// route table picks up this page; site-wide publish passes false and does a
// single regen after publishing every page instead.
//
// The caller holds the site lock (runInWebsiteSiteTx). The returned staleFolder is the page's
// previous deploy folder when it moved; the caller removes it with removeStaleWebsiteFolders after
// the transaction commits.
func publishWebsitePage(ctx context.Context, ids []string, pageRow orm.RecordMap, regenerateRoot bool) (published orm.RecordMap, staleFolder string, err error) {
	// The page IS the current draft now (BodyHTML/Seo live
	// directly on tbl.website_page) -- publish never accepts a design payload of
	// its own, it just renders whatever the editor's autosave already wrote.
	siteID := anyToInt64(pageRow["site_id"])
	if siteID <= 0 {
		return nil, "", orm.InvalidParamsf("tbl.website_page %v has no site_id", pageRow["id"])
	}
	deployFolder, err := ensureWebsiteSiteDeployFolder(ctx, siteID)
	if err != nil {
		return nil, "", err
	}

	componentName, err := websitePageComponentName(ctx, siteID, pageRow)
	if err != nil {
		return nil, "", err
	}

	siteRow, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return nil, "", err
	}
	menuItems, err := loadWebsiteMenuItems(ctx, siteID)
	if err != nil {
		return nil, "", err
	}
	composed, err := composeWebsitePublishHTML(siteRow, pageRow, renderWebsiteMenuHTML(menuItems))
	if err != nil {
		return nil, "", fmt.Errorf("tbl.website_page %v: %w", pageRow["id"], err)
	}
	if composed != "" {
		pageRow[websitePublishHTMLKey] = composed
	}
	// Snapshot BEFORE touching live files: the component folder is usually overwritten in place, so a
	// snapshot failure after the write would report an error for content that is already live.
	if err := snapshotWebsitePage(ctx, pageRow, "publish"); err != nil {
		return nil, "", err
	}
	previousDeployPath := stringValue(pageRow["deploy_path"])
	newPath, err := writeWebsitePageComponentFiles(deployFolder, pageRow, componentName)
	delete(pageRow, websitePublishHTMLKey)
	if err != nil {
		return nil, "", err
	}

	updated, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids, orm.RecordMap{
		"deploy_path":       newPath,
		"last_published_at": nowRFC3339(),
	})
	if err != nil {
		return nil, "", err
	}
	_ = updated
	pageRow["deploy_path"] = newPath
	// The previous folder is removed only after the DB points at the new one and the transaction
	// committed (caller); deleting it first left a page with no files whenever this write failed.
	// This also self-heals pre-flat-scaffold sites, whose random-suffixed folder differs from newPath.
	if previousDeployPath != "" && previousDeployPath != newPath {
		staleFolder = previousDeployPath
	}
	if err := publishWebsitePageTranslations(ctx, siteID, pageRow, deployFolder, componentName, siteRow, renderWebsiteMenuHTML(menuItems)); err != nil {
		return nil, "", err
	}

	if regenerateRoot {
		if err := regenerateWebsiteSiteRootScaffold(ctx, siteID); err != nil {
			return nil, "", err
		}
	}
	return pageRow, staleFolder, nil
}

// websitePageComponentName resolves this page's deterministic component name
// via assignWebsiteComponentNames over the site's full already-published page
// set plus this page (so the folder written below and the root scaffold's
// route table -- built from the same published-page set -- always agree).
func websitePageComponentName(ctx context.Context, siteID int64, pageRow orm.RecordMap) (string, error) {
	pageRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
		"site_id": siteID,
		"__order": "id asc",
		"__limit": 1000,
	}, nil)
	if err != nil {
		return "", err
	}
	pageID := anyToInt64(pageRow["id"])
	included := make([]orm.RecordMap, 0, len(pageRows))
	found := false
	for _, row := range pageRows {
		if anyToInt64(row["id"]) == pageID {
			included = append(included, pageRow)
			found = true
			continue
		}
		if stringValue(row["deploy_path"]) != "" {
			included = append(included, row)
		}
	}
	if !found {
		included = append(included, pageRow)
	}
	names := assignWebsiteComponentNames(included)
	name := names[pageID]
	if name == "" {
		return "", fmt.Errorf("tbl.website_page %v: could not resolve a component name", pageRow["id"])
	}
	return name, nil
}

// unpublishWebsitePage removes the page's deployed component folder and
// clears its DeployPath pointer, then regenerates the site's root scaffold so
// main.js/sitemap.xml stop referencing it. Shared by the page's own
// action_unpublish and tbl.website_site's action_unpublish loop.
func unpublishWebsitePage(ctx context.Context, ids []string, pageRow orm.RecordMap) (orm.RecordMap, error) {
	deployPath := stringValue(pageRow["deploy_path"])
	if deployPath != "" {
		if err := deleteWebsiteStaticFile(ctx, deployPath); err != nil {
			return nil, err
		}
	}
	if err := unpublishWebsitePageTranslations(ctx, anyToInt64(pageRow["id"])); err != nil {
		return nil, err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), ids, orm.RecordMap{
		"deploy_path":       nil,
		"last_published_at": pageRow["last_published_at"],
	}); err != nil {
		return nil, err
	}
	pageRow["deploy_path"] = nil

	if siteID := anyToInt64(pageRow["site_id"]); siteID > 0 {
		if err := regenerateWebsiteSiteRootScaffold(ctx, siteID); err != nil {
			return nil, err
		}
	}
	return pageRow, nil
}

// unlinkWebsitePage deletes pages together with what hangs off them: a deleted page must leave the
// live site (its deployed component, translations and route-table entry go with the next regeneration)
// and must not leave orphaned translations or snapshots behind.
func unlinkWebsitePage(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	for _, id := range ids {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, model), []string{id}, nil)
		if err != nil {
			return nil, err
		}
		if len(rows) == 0 {
			continue
		}
		row := rows[0]
		if stringValue(row["deploy_path"]) != "" {
			if _, err := unpublishWebsitePage(ctx, []string{id}, row); err != nil {
				return nil, err
			}
		} else if err := unpublishWebsitePageTranslations(ctx, anyToInt64(row["id"])); err != nil {
			return nil, err
		}
		for _, child := range []string{WebsitePageTranslationModelName, WebsitePageVersionModelName} {
			kids, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(child, nil), orm.RecordMap{"page_id": anyToInt64(row["id"]), "__limit": 1000}, []string{"id"})
			if err != nil {
				return nil, err
			}
			if len(kids) == 0 {
				continue
			}
			kidIDs := make([]string, 0, len(kids))
			for _, k := range kids {
				kidIDs = append(kidIDs, strconv.FormatInt(anyToInt64(k["id"]), 10))
			}
			if _, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(child, nil), kidIDs); err != nil {
				return nil, err
			}
		}
	}
	return orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(WebsitePageModelName, model), ids)
}
