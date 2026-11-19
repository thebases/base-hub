package models

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const WebsiteSiteModelName = "tbl.website_site"

type WebsiteSiteRecord struct {
	orm.BaseModel
	Name                 string  `bun:"name" json:"name" meta:"index,required,label:Name,help:Stores the admin-facing website name."`
	Slug                 string  `bun:"slug" json:"slug" meta:"index,required,label:Slug,help:Stores the stable site slug used for publish and runtime metadata."`
	CompanyID            *int64  `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company,help:Links this site to one company. Empty means the shared main site for all companies."`
	Description          string  `bun:"description" json:"description" meta:"label:Description,help:Stores the editorial description for the site."`
	IsActive             bool    `bun:"is_active,notnull,default:true" json:"is_active" meta:"label:Active,help:Controls whether the site is eligible for publish and public runtime selection."`
	HomePageID           *int64  `bun:"home_page_id" json:"home_page_id" meta:"kind:many2one,model:tbl.website_page,ondelete:SET NULL,index,label:Home Page,help:Links the site to its current home page."`
	DeployPath           string  `bun:"deploy_path" json:"deploy_path" meta:"readonly,label:Deploy Path,help:Stores the folder used for this site's most recent publish; individual pages deploy their static files under this folder."`
	DefaultSEOName       string  `bun:"default_seo_name" json:"default_seo_name" meta:"label:Default SEO Title,help:Stores the default SEO title pattern for pages that do not override it."`
	DefaultSEODesc       string  `bun:"default_seo_desc" json:"default_seo_desc" meta:"label:Default SEO Description,help:Stores the default SEO description for pages that do not override it."`
	DefaultCanonicalBase string  `bun:"default_canonical_base" json:"default_canonical_base" meta:"label:Canonical Base URL,help:Stores the base URL used to resolve page canonical links when pages do not override them."`
	DefaultRobots        string  `bun:"default_robots" json:"default_robots" meta:"label:Default Robots,help:Stores default robots directives for pages that do not override them."`
	RobotsTXT            string  `bun:"robots_txt" json:"robots_txt" meta:"label:Robots TXT,help:Stores the site-level robots.txt policy content edited in admin."`
	SitemapEnabled       bool    `bun:"sitemap_enabled,notnull,default:true" json:"sitemap_enabled" meta:"label:Sitemap Enabled,help:Controls whether publish includes the site in sitemap generation."`
	PublishedAt          *string `bun:"published_at" json:"published_at" meta:"readonly,label:Published At,help:Stores when the latest deployment became active."`

	// The fields below hold the legacy editor's richer "project" shape natively
	// (see .agents/memory/context/website-module-context.md, "the legacy editor
	// migration" entry). Revision backs the editor's optimistic-concurrency
	// writes; the flat DefaultSEO* fields above remain the source for
	// robots.txt/sitemap generation, DefaultSEO is the canonical editor-facing
	// nested shape going forward.
	Revision            int64          `bun:"revision,notnull,default:1" json:"revision" meta:"readonly,label:Revision,help:Stores the optimistic-concurrency counter bumped on every write."`
	Navigation          map[string]any `bun:"navigation,type:jsonb,nullzero" json:"navigation" meta:"kind:json,label:Navigation,help:Stores the site's navigation structure edited in the editor."`
	Theme               map[string]any `bun:"theme,type:jsonb,nullzero" json:"theme" meta:"kind:json,label:Theme,help:Stores the site's currently applied theme definition (colors, typography, buttons, inputs, links, layout, advanced CSS variables)."`
	SupportedLanguages  []string       `bun:"supported_languages,type:jsonb,nullzero" json:"supported_languages" meta:"kind:json,label:Supported Languages,help:Stores the list of locale codes the site supports."`
	FaviconAttachmentID *int64         `bun:"favicon_attachment_id" json:"favicon_attachment_id" meta:"kind:many2one,model:sys.attachment,ondelete:SET NULL,index,label:Favicon,help:Links the site to its favicon attachment."`
	DefaultSEO          map[string]any `bun:"default_seo,type:jsonb,nullzero" json:"default_seo" meta:"kind:json,label:Default SEO,help:Stores the nested site-level SEO defaults matching the editor's SEO schema (title/description/canonicalUrl/robots/openGraph/structuredData)."`
	HeaderHTML          string         `bun:"header_html" json:"header_html" meta:"label:Header HTML,help:Sanitized site-level header markup, composed above every page's body at publish."`
	FooterHTML          string         `bun:"footer_html" json:"footer_html" meta:"label:Footer HTML,help:Sanitized site-level footer markup, composed below every page's body at publish."`
	DefaultLanguage     string         `bun:"default_language,notnull,default:'en'" json:"default_language" meta:"label:Default Language,help:Language code of the pages' own content. Translations exist for the other supported languages and are served under /<code>/."`
	CustomHeadHTML      string         `bun:"custom_head_html" json:"custom_head_html" meta:"kind:text,label:Custom Head Code,help:Raw HTML added to the head of every published page (analytics, verification tags). Not sanitized: only trusted administrators may edit it."`
	CustomFooterHTML    string         `bun:"custom_footer_html" json:"custom_footer_html" meta:"kind:text,label:Custom Footer Code,help:Raw HTML added at the end of the body of every published page. Not sanitized: only trusted administrators may edit it."`
	SpaManifest         map[string]any `bun:"spa_manifest,type:jsonb,nullzero" json:"spa_manifest" meta:"kind:json,label:SPA Manifest,help:Stores the editor's SPA/PWA manifest settings (name, shortName, description, themeColor, backgroundColor, display, orientation, iconAssetId, offlineSupport) used to generate manifest.json and the service worker in the static website scaffold export."`
}

var WebsiteSiteModel = orm.Define[WebsiteSiteRecord](&Models, 410, orm.ModelSpec{
	Name:        WebsiteSiteModelName,
	Label:       "Website Sites",
	Description: "Top-level website configuration, SEO defaults, and active deployment ownership",
	StandardHooks: orm.StandardRuntimeHooks{
		Write: writeWebsiteSite,
	},
	Prepare: prepareWebsiteSite,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "preview_theme_css", Public: true, ModelOnly: true, ReadOnly: true, Call: previewWebsiteThemeCSS, Description: "Renders the stylesheet a draft theme (kwargs theme) would publish."},
		{Name: "list_chrome_templates", Public: true, ModelOnly: true, ReadOnly: true, Call: listWebsiteChromeTemplates, Description: "Lists the header or footer templates (kwargs kind=header|footer) shipped by installed modules (static/chrome/<kind>/*.xml)."},
		{Name: "default_chrome", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultWebsiteChrome, Description: "Returns the starter header_html and footer_html for a site that has none."},
		{Name: "action_publish", Public: true, UsesPathID: true, Call: actionPublishWebsiteSite},
		{Name: "action_unpublish", Public: true, UsesPathID: true, Call: actionUnpublishWebsiteSite},
	},
})

// prepareWebsiteSite sanitizes and validates a new site before the guarded create persists it
// (writes keep writeWebsiteSite: they run per id inside one transaction with the revision CAS).
func prepareWebsiteSite(ctx context.Context, op orm.WriteOp) error {
	if !op.Create {
		return nil
	}
	working := op.Values
	// deploy_path is assigned by publish only; a fresh site starts at the column default revision.
	stripWebsiteServerManagedFields(working, "deploy_path", "revision")
	if err := sanitizeWebsiteHTMLFields(working, "header_html", "footer_html"); err != nil {
		return err
	}
	if err := validateWebsiteTheme(working["theme"], ""); err != nil {
		return err
	}
	normalizeWebsiteSiteValues(working)
	return validateWebsiteSiteValues(ctx, "", working, true)
}

func writeWebsiteSite(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("tbl.website_site.write requires values")
	}
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_site.write requires ids")
	}
	working := cloneRecordMap(values)
	// revision stays: it is the client's CAS token, and applyWebsiteRevisionCAS below replaces it with
	// the server-computed next value before the write.
	stripWebsiteServerManagedFields(working, "deploy_path")
	if err := sanitizeWebsiteHTMLFields(working, "header_html", "footer_html"); err != nil {
		return nil, err
	}
	if err := validateWebsiteTheme(working["theme"], ""); err != nil {
		return nil, err
	}
	normalizeWebsiteSiteValues(working)
	// Each id is validated and revision-checked on its own (only ids[0] was before), and every row
	// commits together with its revision bump.
	err = runInWebsiteTx(ctx, func(txCtx context.Context, _ bun.IDB) error {
		for _, id := range ids {
			if err := writeOneWebsiteSite(txCtx, model, strings.TrimSpace(id), cloneRecordMap(working)); err != nil {
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

// writeOneWebsiteSite validates and writes one site row. Must run inside runInWebsiteTx.
func writeOneWebsiteSite(ctx context.Context, model *orm.ModelMetadata, id string, working orm.RecordMap) error {
	if err := validateWebsiteSiteValues(ctx, id, working, false); err != nil {
		return err
	}
	if err := applyWebsiteRevisionCAS(ctx, WebsiteSiteModelName, id, working); err != nil {
		return err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, model), []string{id}, working); err != nil {
		return err
	}
	if hasRecordMapKey(working, "company_id") {
		return syncWebsitePageCompanyScopeForSites(ctx, []string{id}, working["company_id"])
	}
	return nil
}

func normalizeWebsiteSiteValues(values orm.RecordMap) {
	if name := stringValue(values["name"]); name != "" && stringValue(values["slug"]) == "" {
		values["slug"] = slugify(name)
	}
	if _, ok := values["is_active"]; !ok {
		values["is_active"] = true
	}
	if _, ok := values["sitemap_enabled"]; !ok {
		values["sitemap_enabled"] = true
	}
	if stringValue(values["default_robots"]) == "" {
		values["default_robots"] = "index,follow"
	}
}

func validateWebsiteSiteValues(ctx context.Context, currentID string, values orm.RecordMap, creating bool) error {
	// A partial write (the builder saving only header_html or theme) leaves identity fields alone,
	// so they are required only when creating or when the payload touches them.
	if (creating || hasRecordMapKey(values, "name")) && stringValue(values["name"]) == "" {
		return orm.InvalidParamsf("tbl.website_site name is required")
	}
	if (creating || hasRecordMapKey(values, "slug")) && stringValue(values["slug"]) == "" {
		return orm.InvalidParamsf("tbl.website_site slug is required")
	}
	if err := ensureSharedWebsiteSiteAvailable(ctx, currentID, values, creating); err != nil {
		return err
	}
	return nil
}

func ensureSharedWebsiteSiteAvailable(ctx context.Context, currentID string, values orm.RecordMap, creating bool) error {
	if !creating && !hasRecordMapKey(values, "company_id") {
		return nil
	}
	targetCompanyID, explicitCompany := int64FromAny(values["company_id"])
	if explicitCompany && targetCompanyID > 0 {
		return nil
	}
	if !explicitCompany {
		env := orm.EnvironmentFromContext(ctx)
		if env != nil {
			resolvedCompanyID, err := env.CompanyID()
			if err != nil {
				return err
			}
			if resolvedCompanyID > 0 {
				return nil
			}
		}
	}
	// Without record rules: a shared site the caller cannot see still counts.
	rows, err := orm.RuntimeRepositorySearchRead(websiteSudoContext(ctx), orm.RuntimeModelRef(WebsiteSiteModelName, nil), orm.RecordMap{
		"company_id": nil,
		"__limit":    5,
	}, []string{"id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		rowID, _ := int64FromAny(row["id"])
		if currentID != "" && strconv.FormatInt(rowID, 10) == currentID {
			continue
		}
		return orm.Conflictf("tbl.website_site allows only one shared site with empty company_id")
	}
	return nil
}

func syncWebsitePageCompanyScopeForSites(ctx context.Context, siteIDs []string, companyValue any) error {
	for _, siteID := range siteIDs {
		numericSiteID, err := strconv.ParseInt(strings.TrimSpace(siteID), 10, 64)
		if err != nil || numericSiteID <= 0 {
			return fmt.Errorf("tbl.website_site ids contain invalid value %q", siteID)
		}
		pageRows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), orm.RecordMap{
			"site_id": numericSiteID,
			"__limit": 1000,
			"__order": "id asc",
		}, []string{"id"})
		if err != nil {
			return err
		}
		for _, row := range pageRows {
			pageID, ok := int64FromAny(row["id"])
			if !ok || pageID <= 0 {
				continue
			}
			if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), []string{strconv.FormatInt(pageID, 10)}, orm.RecordMap{
				"company_id": companyValue,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func actionPublishWebsiteSite(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	siteID, err := websiteSiteIDFromArgs(ids, "action_publish")
	if err != nil {
		return nil, err
	}
	var result orm.RecordMap
	var staleFolders []string
	// The whole publish (deploy folder, every page, root scaffold, published_at) runs under the site
	// row lock, so a concurrent site or page publish/unpublish cannot interleave file writes or
	// regenerate the route table from a half-published page set.
	if err := runInWebsiteSiteTx(ctx, siteID, func(txCtx context.Context) error {
		var publishErr error
		result, staleFolders, publishErr = publishWebsiteSiteLocked(txCtx, siteID)
		return publishErr
	}); err != nil {
		return nil, err
	}
	removeStaleWebsiteFolders(ctx, staleFolders...)
	return result, nil
}

// publishWebsiteSiteLocked publishes every page of the site; the caller holds the site lock. It
// returns the replaced page folders, to be removed once the transaction committed.
func publishWebsiteSiteLocked(ctx context.Context, siteID int64) (orm.RecordMap, []string, error) {
	_, pageRows, err := readSiteAndPageRows(ctx, siteID)
	if err != nil {
		return nil, nil, err
	}
	if len(pageRows) == 0 {
		return nil, nil, orm.InvalidParamsf("tbl.website_site.action_publish requires at least one page")
	}
	// Reserve (or reuse) the site's stable deploy folder before touching any
	// page, so every page below deploys its static file into the same site.deploy_path.
	if _, err := ensureWebsiteSiteDeployFolder(ctx, siteID); err != nil {
		return nil, nil, err
	}
	var staleFolders []string
	for _, pageRow := range pageRows {
		pageID := anyToInt64(pageRow["id"])
		if pageID <= 0 {
			return nil, nil, fmt.Errorf("tbl.website_site.action_publish found a page without a valid id")
		}
		_, stale, err := publishWebsitePage(ctx, []string{strconv.FormatInt(pageID, 10)}, pageRow, false)
		if err != nil {
			return nil, nil, fmt.Errorf("tbl.website_page %v: %w", pageRow["id"], err)
		}
		staleFolders = append(staleFolders, stale)
	}
	if err := regenerateWebsiteSiteRootScaffold(ctx, siteID); err != nil {
		return nil, nil, err
	}
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), []string{strconv.FormatInt(siteID, 10)}, orm.RecordMap{
		"published_at": time.Now().UTC().Format(time.RFC3339),
	}); err != nil {
		return nil, nil, err
	}
	return orm.RecordMap{"site_id": siteID, "published_pages": int64(len(pageRows))}, staleFolders, nil
}

func actionUnpublishWebsiteSite(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	siteID, err := websiteSiteIDFromArgs(ids, "action_unpublish")
	if err != nil {
		return nil, err
	}
	var result orm.RecordMap
	if err := runInWebsiteSiteTx(ctx, siteID, func(txCtx context.Context) error {
		_, pageRows, err := readSiteAndPageRows(txCtx, siteID)
		if err != nil {
			return err
		}
		unpublishedCount := 0
		for _, pageRow := range pageRows {
			pageID := anyToInt64(pageRow["id"])
			if pageID <= 0 {
				continue
			}
			if _, err := unpublishWebsitePage(txCtx, []string{strconv.FormatInt(pageID, 10)}, pageRow); err != nil {
				return fmt.Errorf("tbl.website_page %v: %w", pageRow["id"], err)
			}
			unpublishedCount++
		}
		result = orm.RecordMap{"site_id": siteID, "unpublished_pages": int64(unpublishedCount)}
		return nil
	}); err != nil {
		return nil, err
	}
	return result, nil
}

// websiteSiteIDFromArgs parses the single site id a site action runs on.
func websiteSiteIDFromArgs(ids []string, method string) (int64, error) {
	if len(ids) == 0 {
		return 0, orm.InvalidParamsf("tbl.website_site.%s requires ids", method)
	}
	siteID, err := strconv.ParseInt(strings.TrimSpace(ids[0]), 10, 64)
	if err != nil || siteID <= 0 {
		return 0, orm.InvalidParamsf("tbl.website_site.%s requires a positive site id", method)
	}
	return siteID, nil
}

func boolFromRow(raw any) bool {
	value, ok := boolValue(raw)
	return ok && value
}
