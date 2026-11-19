package models

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

const WebsiteRedirectModelName = "tbl.website_redirect"

// WebsiteRedirectRecord maps an old public path to a new target (301/302) or to
// a gone/not-found status (410/404). The runtime applies it before the site
// shell is served, so a moved page keeps its inbound links.
type WebsiteRedirectRecord struct {
	orm.BaseModel
	SiteID       *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,required,label:Site,help:Site this redirect applies to."`
	URLFrom      string `bun:"url_from" json:"url_from" meta:"index,required,label:From,help:Exact request path to match (for example /old-page)."`
	URLTo        string `bun:"url_to" json:"url_to" meta:"label:To,help:Target path or http(s) URL. Ignored for 404/410."`
	RedirectType string `bun:"redirect_type,notnull,default:301" json:"redirect_type" meta:"label:Type,help:301 permanent; 302 temporary; 404 not found; 410 gone." selection:"(301,301 Permanent),(302,302 Temporary),(404,404 Not Found),(410,410 Gone)"`
	IsActive     bool   `bun:"is_active,notnull,default:true" json:"is_active" meta:"label:Active,help:Inactive redirects are ignored."`
}

var WebsiteRedirectModel = orm.Define[WebsiteRedirectRecord](&Models, 460, orm.ModelSpec{
	Name:        WebsiteRedirectModelName,
	Label:       "Website Redirects",
	Description: "Path redirects and gone/not-found rules applied before page resolution",
	Prepare:     prepareWebsiteRedirect,
})

// websiteRedirectTargetOK rejects control/space characters and backslashes anywhere in a target:
// browsers strip tabs and newlines inside URLs, so "/\t/evil.com" would otherwise become "//evil.com".
func websiteRedirectTargetOK(to string) bool {
	for _, r := range to {
		if r <= 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	l := strings.ToLower(to)
	if strings.HasPrefix(to, "/") {
		return !strings.HasPrefix(to, "//")
	}
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

// normalizeWebsiteRedirectValues validates a complete redirect payload (on write the caller merges the
// stored row first, so a type change cannot leave 301 without a target). Only relative paths and
// http(s) URLs are allowed as targets, so a rule can never emit a javascript:/data: Location or a
// protocol-relative open redirect.
func normalizeWebsiteRedirectValues(values orm.RecordMap, creating bool) error {
	from := strings.TrimSpace(stringValue(values["url_from"]))
	if strings.ContainsAny(from, "?#\\") || strings.IndexFunc(from, func(r rune) bool { return r <= 0x20 }) >= 0 {
		return fmt.Errorf("tbl.website_redirect url_from must be a plain path without query, fragment or spaces")
	}
	from = "/" + strings.Trim(from, "/")
	if from == "/" {
		return fmt.Errorf("tbl.website_redirect url_from is required and cannot be the root path")
	}
	values["url_from"] = from
	kind := stringValue(values["redirect_type"])
	if kind == "" {
		kind = "301"
		values["redirect_type"] = kind
	}
	switch kind {
	case "301", "302":
		to := strings.TrimSpace(stringValue(values["url_to"]))
		if to == "" {
			return fmt.Errorf("tbl.website_redirect url_to is required for 301/302")
		}
		if !websiteRedirectTargetOK(to) {
			return fmt.Errorf("tbl.website_redirect url_to must be a path or an http(s) URL without spaces or backslashes")
		}
		if to == from {
			return fmt.Errorf("tbl.website_redirect url_to must differ from url_from")
		}
		values["url_to"] = to
	case "404", "410":
	default:
		return fmt.Errorf("tbl.website_redirect redirect_type %q is invalid", kind)
	}
	return nil
}

// prepareWebsiteRedirect validates the payload; on write it validates the merged result so a type
// change cannot leave 301 without a target.
func prepareWebsiteRedirect(ctx context.Context, op orm.WriteOp) error {
	working := op.Values
	if op.Create {
		return normalizeWebsiteRedirectValues(working, true)
	}
	// Validate the merged result: stored fields fill whatever the payload omits.
	merged := cloneRecordMap(working)
	if len(op.IDs) > 0 {
		rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(WebsiteRedirectModelName, op.Model), op.IDs[:1], []string{"url_from", "url_to", "redirect_type"})
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			for _, k := range []string{"url_from", "url_to", "redirect_type"} {
				if _, ok := merged[k]; !ok {
					merged[k] = rows[0][k]
				}
			}
		}
	}
	if err := normalizeWebsiteRedirectValues(merged, false); err != nil {
		return err
	}
	for _, k := range []string{"url_from", "url_to", "redirect_type"} {
		if _, ok := working[k]; ok {
			working[k] = merged[k]
		}
	}
	return nil
}

// LookupWebsiteRedirect finds the active redirect for path on the site.
// Returns nil when none matches.
func LookupWebsiteRedirect(ctx context.Context, siteID int64, path string) (orm.RecordMap, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteRedirectModelName, nil), orm.RecordMap{
		"site_id": siteID, "url_from": path, "is_active": true, "__order": "id desc", "__limit": 1,
	}, []string{"id", "url_to", "redirect_type"})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	return rows[0], nil
}
