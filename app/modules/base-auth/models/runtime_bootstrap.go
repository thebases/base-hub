package models

// File overview:
// - Responsibility: declares the `auth` module's model set; every model file adds itself with
//   orm.Define (or Models.Add for non-handler entries) and the module boots them in declared order.
// - Key symbols: `Models`, `InitRuntimeModels`.

import "git.thebase.vn/base_hub/internal/orm"

// Models collects the module's runtime bootstrap entries (plan T88).
var Models orm.ModelSet

// InitRuntimeModels initializes every model of this module in declared order and returns the combined shutdown.
func InitRuntimeModels() func() { return Models.Init() }
