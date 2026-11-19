package models

// File overview:
// - Responsibility: starter site header and footer markup, offered by the builder when a site has
//   none. `ul#site-nav-menu` is the slot the publish step fills from tbl.website_menu.
// - Key symbols: `defaultWebsiteChrome`, `defaultWebsiteChromeHTML`.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

const defaultWebsiteHeaderHTML = `<header id="site-header"><nav class="navbar navbar-expand-lg border-bottom bg-body"><div class="container">` +
	`<a class="navbar-brand fw-bold" href="/">Website</a>` +
	`<div class="navbar-collapse"><ul id="site-nav-menu" class="navbar-nav me-auto mb-2 mb-lg-0"></ul>` +
	`<a class="btn btn-primary btn-sm" href="/contact">Get started</a></div></div></nav></header>`

const defaultWebsiteFooterHTML = `<footer id="site-footer" class="py-4 border-top bg-body-tertiary"><div class="container d-flex flex-column flex-md-row justify-content-between align-items-center gap-2">` +
	`<span class="text-body-secondary small">© Website. All rights reserved.</span>` +
	`<ul class="nav"><li class="nav-item"><a class="nav-link small" href="/">Home</a></li><li class="nav-item"><a class="nav-link small" href="/contact">Contact</a></li></ul></div></footer>`

// defaultWebsiteChrome is the model-level `default_chrome` RuntimeMethod. The markup passes the same
// sanitizer as everything the builder saves, so what is offered is what would be stored.
func defaultWebsiteChrome(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	header, err := defaultWebsiteChromeHTML(ctx, "header", defaultWebsiteHeaderHTML)
	if err != nil {
		return nil, err
	}
	footer, err := defaultWebsiteChromeHTML(ctx, "footer", defaultWebsiteFooterHTML)
	if err != nil {
		return nil, err
	}
	return map[string]any{"header_html": header, "footer_html": footer}, nil
}

// defaultWebsiteChromeHTML starts a site from the "website/default" template of `kind`, so a new
// header/footer already carries the markers the option schemas and behaviors need. The constant is the
// fallback when the template files are unreadable.
func defaultWebsiteChromeHTML(ctx context.Context, kind, fallback string) (string, error) {
	if templates, err := loadWebsiteChromeTemplates(ctx, kind); err == nil {
		for _, tpl := range templates {
			if tpl.Key == "website/default" {
				return tpl.HTML, nil
			}
		}
	}
	return sanitizeWebsiteHTML(fallback)
}
