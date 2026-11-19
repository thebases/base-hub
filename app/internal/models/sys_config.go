package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys config inside the kernel model catalog.
// - Key symbols: `init`, `SysConfigRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

type SysConfigRecord struct {
	bun.BaseModel `bun:"table:sys_config_param,alias:sys_config_param"`
	orm.MetadataBaseModel
	// Name stores the flattened dot-path config key such as kafka.enable.
	Name string `bun:"name,unique" json:"name" meta:"index,unique,label:Name,help:Stores the human readable name for this record."`
	// Value stores the raw config value and preserves bool, number, string, or array types.
	Value any `bun:"value,type:jsonb,nullzero" json:"value" meta:"kind:json,label:Value,help:Stores the value payload in JSON format."`
}

var SysConfigModelName = "sys.config_param"

// SysConfigModel keeps customization in composition style so configuration
// validation and defaults can be added as focused helpers around the spec.
var SysConfigModel = orm.Define[SysConfigRecord](&Models, 10, orm.ModelSpec{
	Name:        SysConfigModelName,
	Label:       "System Config Parameter",
	Description: "Dynamic runtime configuration persisted as flattened dot-path keys",
})
