package models

// File overview:
// - Responsibility: defines built-in runtime model metadata and server-side behavior for base aliases inside the kernel model catalog.
// - Key symbols: `Step`, `FlowRecord`, `ResUserModel`, `ResGroupModel`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Follows a state-machine style flow where persisted workflow state determines whether execution starts fresh, resumes, retries, or hands work to another step.

import (
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	basemodels "git.thebase.vn/base_hub/modules/base/models"
)

type Step = workflowmodels.Step
type FlowRecord = workflowmodels.FlowRecord
type ResUserRecord = basemodels.ResUserRecord
type ResLangRecord = basemodels.ResLangRecord
type ResGroupRecord = basemodels.ResGroupRecord
type BankDocument = basemodels.BankDocument
type ResCountryRecord = basemodels.ResCountryRecord
type ResCountryStateRecord = basemodels.ResCountryStateRecord
type ResCountryWardRecord = basemodels.ResCountryWardRecord

const (
	ResLangModelName         = basemodels.ResLangModelName
	ResCountryModelName      = basemodels.ResCountryModelName
	ResCountryGroupModelName = basemodels.ResCountryGroupModelName
	ResCountryStateModelName = basemodels.ResCountryStateModelName
	ResCountryWardModelName  = basemodels.ResCountryWardModelName
)

var (
	FlowModelName              = workflowmodels.FlowModelName
	FlowModel                  = workflowmodels.FlowModel
	WorkflowRunModel           = workflowmodels.WorkflowRunModel
	ResUserModelName           = basemodels.ResUserModelName
	ResUserModelSpec           = basemodels.ResUserModelSpec
	ResUserModel               = basemodels.ResUserModel
	ResLangModelSpec           = basemodels.ResLangModelSpec
	ResLangModel               = basemodels.ResLangModel
	ResGroupPrivilegeModelName = basemodels.ResGroupPrivilegeModelName
	ResGroupModelName          = basemodels.ResGroupModelName
	ResGroupPrivilegeModelSpec = basemodels.ResGroupPrivilegeModelSpec
	ResGroupModelSpec          = basemodels.ResGroupModelSpec
	ResGroupPrivilegeModel     = basemodels.ResGroupPrivilegeModel
	ResGroupModel              = basemodels.ResGroupModel
	ResCompanyModelName        = basemodels.ResCompanyModelName
	ResCompanyModel            = basemodels.ResCompanyModel
	BaseCompanyModel           = basemodels.ResCompanyModel
	BankModelName              = basemodels.BankModelName
	BankModel                  = basemodels.BankModel
	OutboundDeliveryModel      = workflowmodels.OutboundDeliveryModel
	ResCountryModelSpec        = basemodels.ResCountryModelSpec
	ResCountryWardModelSpec    = basemodels.ResCountryWardModelSpec
	ResCountryStateModelSpec   = basemodels.ResCountryStateModelSpec
	ResCountryModel            = basemodels.ResCountryModel
	ResCountryGroupModel       = basemodels.ResCountryGroupModel
	ResCountryStateModel       = basemodels.ResCountryStateModel
	ResCountryWardModel        = basemodels.ResCountryWardModel
	ResPartnerModelName        = basemodels.ResPartnerModelName
	ResPartnerModel            = basemodels.ResPartnerModel
	ETLActionModel             = workflowmodels.ETLActionModel
	WorkflowActionModel        = workflowmodels.WorkflowActionModel

	ResCurrencyModel     = basemodels.ResCurrencyModel
	ResCurrencyRateModel = basemodels.ResCurrencyRateModel
)
