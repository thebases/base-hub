package models

import "git.thebase.vn/base_hub/internal/orm"

// Models collects this module's runtime models (declared with orm.Define in the tbl_*.go files).
var Models orm.ModelSet

// InitRuntimeModels initializes every model of this module in declared order and returns the combined shutdown.
func InitRuntimeModels() func() { return Models.Init() }
