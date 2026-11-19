package models

// File overview:
// - Responsibility: the `base-calendar` module's model set; each model file declares itself into
//   `Models` with `orm.Define` and its bootstrap order.
// - Key symbols: `Models`, `InitRuntimeModels`.

import "git.thebase.vn/base_hub/internal/orm"

// Models collects this module's model handlers in declared order.
var Models orm.ModelSet

// InitRuntimeModels initializes every model of this module in declared order and returns the combined shutdown.
func InitRuntimeModels() func() { return Models.Init() }
