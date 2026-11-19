package models

// File overview:
// - Responsibility: owns the `base` module model set and boots it in declared order.
// - Key symbols: `Models`, `InitRuntimeModels`.
// - Pattern: each model file declares itself with `orm.Define` into `Models`; `orm.ModelSet` sorts by order then name.

import "git.thebase.vn/base_hub/internal/orm"

// Models collects every base model handler; filled by package-level `orm.Define` calls.
var Models orm.ModelSet

// InitRuntimeModels initializes every model of this module in declared order and returns the combined shutdown.
func InitRuntimeModels() func() { return Models.Init() }
