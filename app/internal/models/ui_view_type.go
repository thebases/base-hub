package models

// File overview:
// - Responsibility: defines the `ui.view.type` kernel catalog — the view types the backend knows
//   (seeded from modules/base/data/12_ui_view_type.json). Rows with `is_view_mode` mirror the
//   frontend's VIEW_TYPES (view-registry.js): the modes ModelView can render and that window
//   actions / sys.model.action.view_type may list. Arch-only types (search, qweb, ...) are kept
//   so ui.view.type can pick from the same catalog.
// - Key symbols: `UIViewTypeRecord`, `UIViewTypeModelName`.
// - Pattern: fields referencing a view type stay plain char columns (one name, or a comma-separated
//   list) and use the `ui_view_type` / `ui_view_types` form widgets — no FK, so existing rows and
//   arch XML keep working unchanged.

import "git.thebase.vn/base_hub/internal/orm"

// UIViewTypeRecord is one known view type, keyed by its technical name (e.g. "list").
type UIViewTypeRecord struct {
	orm.BaseModel
	Name     string `bun:"name,unique,notnull" json:"name" meta:"index,unique,label:Technical Name,help:Stores the view type key used in arch roots, view_mode and view_type lists (e.g. list, form, kanban)."`
	Label    string `bun:"label" json:"label" meta:"label:Label,help:Stores the human readable view type name shown in pickers."`
	Icon     string `bun:"icon" json:"icon" meta:"label:Icon,help:Stores the optional Lucide icon name shown for this view type."`
	Sequence int64  `bun:"sequence,default:10" json:"sequence" default:"10" meta:"label:Sequence,help:Orders view types inside pickers (lowest first)."`
	// IsViewMode separates the modes the SPA renders (ModelView VIEW_TYPES) from arch-only types;
	// the view-type widgets offer only view modes unless the arch asks for all types.
	IsViewMode bool `bun:"is_view_mode,default:false" json:"is_view_mode" default:"false" meta:"label:View Mode,help:Marks a view type the frontend renders as a view mode (list, form, kanban, ...); arch-only types such as search stay unchecked."`
}

const UIViewTypeModelName = "ui.view.type"

// UIViewTypeModel registers the catalog before ui.view (140) so view forms can read it from the first boot.
var UIViewTypeModel = orm.Define[UIViewTypeRecord](&Models, 135, orm.ModelSpec{
	Name:        UIViewTypeModelName,
	Label:       "View Types",
	Description: "Catalog of view types known to the backend; view-mode rows mirror the frontend VIEW_TYPES registry",
})
