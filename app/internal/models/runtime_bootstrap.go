package models

// File overview:
// - Responsibility: owns the kernel model catalog's bootstrap set; each model file declares itself into
//   Models with orm.Define and the server boots them in one explicit order.
// - Key symbols: `Models`, `InitRuntimeModels`.
// - Pattern: one shared orm.ModelSet instead of a per-package registry copy (plan T88).

import "git.thebase.vn/base_hub/internal/orm"

// Models collects every kernel runtime model in declared order.
var Models orm.ModelSet

// InitRuntimeModels initializes every model of this package in declared order and returns the combined shutdown.
func InitRuntimeModels() func() { return Models.Init() }
