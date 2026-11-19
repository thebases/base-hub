package models

import (
	"git.thebase.vn/base_hub/internal/orm"
)

const WebsiteDomainModelName = "tbl.website_domain"

type WebsiteDomainRecord struct {
	orm.BaseModel
	SiteID    *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,required,label:Site,help:Links this domain binding to its owning site."`
	Host      string `bun:"host" json:"host" meta:"index,required,label:Host,help:Stores the bound hostname for the site."`
	BasePath  string `bun:"base_path" json:"base_path" meta:"label:Base Path,help:Stores the optional base path bound to this host."`
	IsPrimary bool   `bun:"is_primary,notnull,default:false" json:"is_primary" meta:"label:Primary,help:Controls whether this binding is the primary public hostname for the site."`
	IsActive  bool   `bun:"is_active,notnull,default:true" json:"is_active" meta:"label:Active,help:Controls whether the domain binding is active for runtime resolution."`
}

var WebsiteDomainModel = orm.Define[WebsiteDomainRecord](&Models, 450, orm.ModelSpec{
	Name:        WebsiteDomainModelName,
	Label:       "Website Domains",
	Description: "Optional domain and path bindings for websites",
})
