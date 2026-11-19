package models

// File overview:
// - Responsibility: translated content of a page, one row per (page, language). The default
//   language lives on the page itself; a translation is published as its own component and served at
//   `/<lang><route>` with hreflang alternates.
// - Key symbols: `WebsitePageTranslationModel`, `publishWebsitePageTranslations`,
//   `unpublishWebsitePageTranslations`.

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const WebsitePageTranslationModelName = "tbl.website_page_translation"

var websiteLangRe = regexp.MustCompile(`^[a-z]{2,3}(-[a-z0-9]{2,8})?$`)

type WebsitePageTranslationRecord struct {
	orm.BaseModel
	PageID     *int64         `bun:"page_id" json:"page_id" meta:"kind:many2one,model:tbl.website_page,ondelete:CASCADE,index,required,label:Page,help:Page this translation belongs to."`
	SiteID     *int64         `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,readonly,label:Site,help:Owning site, taken from the page."`
	Lang       string         `bun:"lang" json:"lang" meta:"index,required,label:Language,help:Language code (for example vi). Must be one of the site's supported languages and not its default language."`
	BodyHTML   string         `bun:"body_html" json:"body_html" meta:"label:Body HTML,help:Sanitized translated page body."`
	Seo        map[string]any `bun:"seo,type:jsonb,nullzero" json:"seo" meta:"kind:json,label:SEO,help:Translated SEO fields (title, description)."`
	IsActive   bool           `bun:"is_active,notnull,default:true" json:"is_active" meta:"label:Published With Page,help:Inactive translations are not published."`
	DeployPath string         `bun:"deploy_path" json:"deploy_path" meta:"readonly,label:Deploy Path,help:Folder of the currently deployed translated component. Empty when not published."`
	Revision   int64          `bun:"revision,notnull,default:1" json:"revision" meta:"readonly,label:Revision,help:Optimistic-concurrency counter bumped on every write."`
}

var WebsitePageTranslationModel = orm.Define[WebsitePageTranslationRecord](&Models, 440, orm.ModelSpec{
	Name:        WebsitePageTranslationModelName,
	Label:       "Website Page Translations",
	Description: "Per-language content of website pages",
	StandardHooks: orm.StandardRuntimeHooks{
		Write: writeWebsitePageTranslation,
	},
	Prepare: prepareWebsitePageTranslation,
})

// validateWebsiteTranslation checks the language against the owning site and enforces one row per
// (page, language). `values` must carry page_id and lang (merged with the stored row on write).
func validateWebsiteTranslation(ctx context.Context, currentID string, values orm.RecordMap) error {
	pageID, ok := int64FromAny(values["page_id"])
	if !ok || pageID <= 0 {
		return orm.InvalidParamsf("tbl.website_page_translation page_id is required")
	}
	lang := strings.ToLower(strings.TrimSpace(stringValue(values["lang"])))
	if !websiteLangRe.MatchString(lang) {
		return orm.InvalidParamsf("tbl.website_page_translation lang %q is not a valid language code", lang)
	}
	values["lang"] = lang
	pages, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageModelName, nil), []string{strconv.FormatInt(pageID, 10)}, []string{"site_id"})
	if err != nil {
		return err
	}
	page, err := firstRow(pages, WebsitePageModelName)
	if err != nil {
		return err
	}
	siteID := anyToInt64(page["site_id"])
	site, err := loadWebsiteSiteRow(ctx, siteID)
	if err != nil {
		return err
	}
	if def := websiteSiteDefaultLanguage(site); lang == def {
		return orm.InvalidParamsf("tbl.website_page_translation: %q is the site's default language; edit the page itself", lang)
	}
	supported := false
	for _, l := range websiteSiteLanguages(site) {
		if l == lang {
			supported = true
		}
	}
	if !supported {
		return orm.InvalidParamsf("tbl.website_page_translation: language %q is not in the site's supported languages", lang)
	}
	values["site_id"] = siteID
	// Without record rules: a translation the caller cannot see still owns its (page, lang).
	rows, err := orm.RuntimeRepositorySearchRead(websiteSudoContext(ctx), orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), orm.RecordMap{"page_id": pageID, "lang": lang, "__limit": 5}, []string{"id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strconv.FormatInt(anyToInt64(row["id"]), 10) != currentID {
			return orm.Conflictf("tbl.website_page_translation: page %d already has a %s translation", pageID, lang)
		}
	}
	return nil
}

func websiteSiteDefaultLanguage(site orm.RecordMap) string {
	if l := strings.ToLower(strings.TrimSpace(stringValue(site["default_language"]))); l != "" {
		return l
	}
	return "en"
}

// websiteSiteLanguages returns the supported language codes, lower-cased.
func websiteSiteLanguages(site orm.RecordMap) []string {
	var out []string
	switch v := site["supported_languages"].(type) {
	case []string:
		for _, l := range v {
			out = append(out, strings.ToLower(strings.TrimSpace(l)))
		}
	case []any:
		for _, l := range v {
			out = append(out, strings.ToLower(strings.TrimSpace(stringValue(l))))
		}
	}
	return out
}

// prepareWebsitePageTranslation sanitizes and validates a new translation before the guarded create
// persists it (writes keep writeWebsitePageTranslation: per-id revision CAS in one transaction).
func prepareWebsitePageTranslation(ctx context.Context, op orm.WriteOp) error {
	if !op.Create {
		return nil
	}
	working := op.Values
	stripWebsiteServerManagedFields(working, "deploy_path", "revision")
	if err := sanitizeWebsiteHTMLFields(working, "body_html"); err != nil {
		return err
	}
	return validateWebsiteTranslation(ctx, "", working)
}

func writeWebsitePageTranslation(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, orm.InvalidParamsf("tbl.website_page_translation.write requires values")
	}
	if len(ids) == 0 {
		return nil, orm.InvalidParamsf("tbl.website_page_translation.write requires ids")
	}
	working := cloneRecordMap(values)
	// Same contract as the page: deploy_path is publish-owned, revision is the CAS token.
	stripWebsiteServerManagedFields(working, "deploy_path")
	if err := sanitizeWebsiteHTMLFields(working, "body_html"); err != nil {
		return nil, err
	}
	// Each id gets its own page/lang validation and revision check (only ids[0] was checked before);
	// all rows commit together.
	err = runInWebsiteTx(ctx, func(txCtx context.Context, _ bun.IDB) error {
		for _, id := range ids {
			if err := writeOneWebsitePageTranslation(txCtx, model, strings.TrimSpace(id), cloneRecordMap(working)); err != nil {
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

// writeOneWebsitePageTranslation validates and writes one translation. Must run inside runInWebsiteTx.
func writeOneWebsitePageTranslation(ctx context.Context, model *orm.ModelMetadata, id string, working orm.RecordMap) error {
	if hasRecordMapKey(working, "page_id") || hasRecordMapKey(working, "lang") {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, model), []string{id}, []string{"page_id", "lang"})
		if err != nil {
			return err
		}
		stored, err := firstRow(rows, WebsitePageTranslationModelName)
		if err != nil {
			return err
		}
		merged := orm.RecordMap{"page_id": stored["page_id"], "lang": stored["lang"]}
		for _, k := range []string{"page_id", "lang"} {
			if hasRecordMapKey(working, k) {
				merged[k] = working[k]
			}
		}
		if err := validateWebsiteTranslation(ctx, id, merged); err != nil {
			return err
		}
		working["lang"], working["site_id"] = merged["lang"], merged["site_id"]
	}
	if err := applyWebsiteRevisionCAS(ctx, WebsitePageTranslationModelName, id, working); err != nil {
		return err
	}
	_, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, model), []string{id}, working)
	return err
}

// websiteTranslationComponentName is the component (and folder) name of a translation: the page's
// component name plus the language, with the underscore keeping it apart from any page name.
func websiteTranslationComponentName(pageComponent, lang string) string {
	return pageComponent + "_" + strings.NewReplacer("-", "_").Replace(lang)
}

// publishWebsitePageTranslations writes one component folder per active translation of the page and
// records its deploy_path. Translations that were published before but are now inactive are removed.
func publishWebsitePageTranslations(ctx context.Context, siteID int64, pageRow orm.RecordMap, deployFolder, pageComponent string, siteRow orm.RecordMap, menuHTML string) error {
	pageID := anyToInt64(pageRow["id"])
	all, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), orm.RecordMap{"page_id": pageID, "__limit": 200}, nil)
	if err != nil {
		return err
	}
	for _, t := range all {
		id := strconv.FormatInt(anyToInt64(t["id"]), 10)
		active := boolFromAny(t["is_active"]) && strings.TrimSpace(stringValue(t["body_html"])) != ""
		if !active {
			if prev := stringValue(t["deploy_path"]); prev != "" {
				if err := deleteWebsiteStaticFile(ctx, prev); err != nil {
					return err
				}
				if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), []string{id}, orm.RecordMap{"deploy_path": nil}); err != nil {
					return err
				}
			}
			continue
		}
		overlay := orm.RecordMap{"id": t["id"], "body_html": t["body_html"]}
		composed, err := composeWebsitePublishHTML(siteRow, overlay, menuHTML)
		if err != nil {
			return fmt.Errorf("translation %s: %w", stringValue(t["lang"]), err)
		}
		overlay[websitePublishHTMLKey] = composed
		overlay["custom_css"] = pageRow["custom_css"]
		overlay["visibility"] = pageRow["visibility"]
		path, err := writeWebsitePageComponentFiles(deployFolder, overlay, websiteTranslationComponentName(pageComponent, stringValue(t["lang"])))
		if err != nil {
			return err
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), []string{id}, orm.RecordMap{"deploy_path": path}); err != nil {
			return err
		}
	}
	return nil
}

// unpublishWebsitePageTranslations removes every deployed translation folder of the page.
func unpublishWebsitePageTranslations(ctx context.Context, pageID int64) error {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), orm.RecordMap{"page_id": pageID, "__limit": 200}, []string{"id", "deploy_path"})
	if err != nil {
		return err
	}
	for _, t := range rows {
		prev := stringValue(t["deploy_path"])
		if prev == "" {
			continue
		}
		if err := deleteWebsiteStaticFile(ctx, prev); err != nil {
			return err
		}
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(WebsitePageTranslationModelName, nil), []string{strconv.FormatInt(anyToInt64(t["id"]), 10)}, orm.RecordMap{"deploy_path": nil}); err != nil {
			return err
		}
	}
	return nil
}
