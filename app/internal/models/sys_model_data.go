package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for sys model data inside the kernel model catalog.
// - Key symbols: `init`, `SysModelDataRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// SysModelDataRecord stores stable external identifiers for imported records so
// later imports can resolve references and perform deterministic updates.
type SysModelDataRecord struct {
	bun.BaseModel `bun:"table:sys_model_data,alias:sys_model_data"`
	orm.MetadataBaseModel
	Name         string `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	CompleteName string `bun:"complete_name" json:"complete_name" meta:"index,label:Complete Name,help:Stores the complete name value for this record."`
	Model        string `bun:"model" json:"model" meta:"index,label:Model,help:Stores the model value for this record."`
	Module       string `bun:"module" json:"module" meta:"index,label:Module,help:Stores the module value for this record."`
	ModelId      string `bun:"model_id" json:"model_id" meta:"index,label:Model Id,help:Stores the target record id value for this record."`
	Noupdate     bool   `bun:"noupdate" json:"noupdate" meta:"label:Noupdate,help:Controls whether noupdate is enabled for this record."`
	Reference    string `bun:"reference" json:"reference" meta:"label:Reference,help:Stores the reference value for this record."`
}

var SysModelDataModelName = "sys.model.data"

var SysModelDataModelSpec = orm.ModelSpec{
	Name:        SysModelDataModelName,
	Label:       "External Identifiers",
	Description: "Stable external identifiers for master-data import and cross-record reference resolution",
	Definition:  orm.ModelDefinition(SysModelDataModelName, (*SysModelDataRecord)(nil)),
}

// SysModelDataModel exposes imported external identifiers through the same
// runtime CRUD surface as the rest of the metadata-driven models.
// SysModelDataModel should stay composition-oriented so metadata lookup rules,
// defaults, and validation helpers remain easy to reason about.
var SysModelDataModel = orm.Define[SysModelDataRecord](&Models, 50, SysModelDataModelSpec)
