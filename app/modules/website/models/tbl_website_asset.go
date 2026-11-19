package models

import (
	"git.thebase.vn/base_hub/internal/orm"
)

const WebsiteAssetModelName = "tbl.website_asset"

// WebsiteAssetRecord backs the legacy editor's tbl.website_asset capability group.
// Bytes are never stored here: AttachmentID points at the sys.attachment row
// created via the existing /web/content upload path; this model only holds the asset-library metadata the editor's Asset Manager needs.
type WebsiteAssetRecord struct {
	orm.BaseModel
	SiteID       *int64         `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:CASCADE,index,required,label:Site,help:Links this asset to its owning site/project."`
	AttachmentID *int64         `bun:"attachment_id" json:"attachment_id" meta:"kind:many2one,model:sys.attachment,ondelete:CASCADE,index,required,label:Attachment,help:Links to the sys.attachment row holding the actual file bytes."`
	Filename     string         `bun:"filename" json:"filename" meta:"index,required,label:Filename,help:Stores the original uploaded filename."`
	MimeType     string         `bun:"mime_type" json:"mime_type" meta:"index,label:MIME Type,help:Stores the asset's content type."`
	SizeBytes    int64          `bun:"size_bytes,notnull,default:0" json:"size_bytes" meta:"label:Size (Bytes),help:Stores the asset's byte size."`
	Hash         string         `bun:"hash" json:"hash" meta:"index,label:Hash,help:Stores a content hash used for dedupe."`
	AltText      string         `bun:"alt_text" json:"alt_text" meta:"label:Alt Text,help:Stores accessibility alt text for image assets."`
	TitleText    string         `bun:"title_text" json:"title_text" meta:"label:Title Text,help:Stores an editorial title/caption for the asset."`
	FocalPoint   map[string]any `bun:"focal_point,type:jsonb,nullzero" json:"focal_point" meta:"kind:json,label:Focal Point,help:Stores the {x,y} focal point used for responsive image cropping."`
	Kind         string         `bun:"kind" json:"kind" meta:"index,label:Kind,selection:image|Image;video|Video;font|Font;pdf|PDF;document|Document,help:Stores the asset library category."`
}

var WebsiteAssetModel = orm.Define[WebsiteAssetRecord](&Models, 440, orm.ModelSpec{
	Name:        WebsiteAssetModelName,
	Label:       "Website Assets",
	Description: "Asset library metadata for website projects; file bytes live in sys.attachment",
})
