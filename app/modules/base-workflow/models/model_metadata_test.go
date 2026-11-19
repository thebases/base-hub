package models

// File overview:
// - Responsibility: registers this package's model metadata (no executor) once for the unit tests, so
//   the guarded create/write path (orm.CreateRecord/WriteRecords, plan T87) canonicalises payloads
//   against the real fields instead of a name-only handle that drops every key. The repository
//   itself stays the per-test stub seam.

import "git.thebase.vn/base_hub/internal/orm"

func init() {
	register := func(name string, err error) {
		if err != nil {
			panic("register " + name + " metadata for tests: " + err.Error())
		}
	}
	_, err := orm.RegisterModelType[FlowRecord](FlowModelSpec)
	register(FlowModelName, err)
	_, err = orm.RegisterModelType[WorkflowActionRecord](WorkflowActionModelSpec)
	register(WorkflowActionModelName, err)
	_, err = orm.RegisterModelType[WorkflowTriggerRecord](WorkflowTriggerModelSpec)
	register(WorkflowTriggerModelName, err)
	_, err = orm.RegisterModelType[WorkflowEdgeRecord](WorkflowEdgeModelSpec)
	register(WorkflowEdgeModelName, err)
	_, err = orm.RegisterModelType[WorkflowCredentialRecord](WorkflowCredentialModelSpec)
	register(WorkflowCredentialModelName, err)
}
