package models

import (
	"git.thebase.vn/base_hub/internal/orm"
)

const WebsiteSnippetModelName = "tbl.website_snippet"

// WebsiteSnippetRecord backs the legacy editor's tbl.website_snippet/list|install|
// uninstall capabilities. A nil SiteID row is a global catalog snippet
// available to every project; a set SiteID row is a project-saved snippet
// captured from the canvas (see the library's "How to Use Snippets" flow).
type WebsiteSnippetRecord struct {
	orm.BaseModel
	Name       string         `bun:"name" json:"name" meta:"index,required,label:Name,help:Stores the snippet's display name."`
	Category   string         `bun:"category" json:"category" meta:"index,label:Category,help:Stores the snippet's block-library category."`
	Definition map[string]any `bun:"definition,type:jsonb,nullzero" json:"definition" meta:"kind:json,required,label:Definition,help:Stores the snippet's component/style payload."`
	SiteID     *int64         `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,label:Site,help:Empty means a global catalog snippet available to every project; set means a project-saved snippet."`
}

var WebsiteSnippetModel = orm.Define[WebsiteSnippetRecord](&Models, 470, orm.ModelSpec{
	Name:        WebsiteSnippetModelName,
	Label:       "Website Snippets",
	Description: "Reusable component/style snippets, global or per-project, for website projects",
	Prepare:     prepareWebsiteSnippet,
	RuntimeMethods: []orm.RuntimeMethod{
		{
			Name:           "data_source",
			Public:         true,
			ModelOnly:      true,
			ReadOnly:       true,
			AliasRoute:     "GET /website/runtime/data/:source/:limit",
			AliasAnonymous: true,
			Call:           websiteDataSource,
			Description:    "Anonymous read of a module-registered public data source. Path: source key and item limit.",
		},
		{Name: "list_options", Public: true, ModelOnly: true, ReadOnly: true, Call: listWebsiteOptionSchemas, Description: "Lists the builder option schemas shipped by installed modules (static/options/*.json)."},
		{Name: "list_catalog", Public: true, ModelOnly: true, ReadOnly: true, Call: listWebsiteSnippetCatalog, Description: "Lists builder blocks shipped by installed modules (static/snippets/*.xml); kwargs group, search."},
	},
})
