package models

import (
	"reflect"
	"strings"
	"testing"

	ormpkg "git.thebase.vn/base_hub/internal/orm"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	basemodels "git.thebase.vn/base_hub/modules/base/models"
)

func TestBaseModelBackedRecordsUseSharedActiveField(t *testing.T) {
	cases := []struct {
		name string
		typ  reflect.Type
	}{
		{name: "sys.model.access", typ: reflect.TypeOf(SysModelAccessRecord{})},
		{name: "sys.rule", typ: reflect.TypeOf(SysRuleRecord{})},
		{name: "ui.actions.client", typ: reflect.TypeOf(UIActionClientRecord{})},
		{name: "ui.actions.window", typ: reflect.TypeOf(UIActionWindowRecord{})},
		{name: "ui.menu", typ: reflect.TypeOf(UIMenuRecord{})},
		{name: "ui.view", typ: reflect.TypeOf(UIViewRecord{})},
		{name: "ui.view.custom", typ: reflect.TypeOf(UIViewCustomRecord{})},
		{name: "base.service.bus", typ: reflect.TypeOf(busmodels.ServiceBusRecord{})},
		{name: "base.users", typ: reflect.TypeOf(basemodels.ResUserRecord{})},
		{name: "tbl.company", typ: reflect.TypeOf(basemodels.ResCompanyRecord{})},
		{name: "tbl.partner", typ: reflect.TypeOf(basemodels.ResPartnerRecord{})},
		{name: "tbl.partner.bank", typ: reflect.TypeOf(basemodels.BankDocument{})},
		{name: "tbl.lang", typ: reflect.TypeOf(basemodels.ResLangRecord{})},
		{name: "tbl.currency", typ: reflect.TypeOf(basemodels.ResCurrencyRecord{})},
		{name: "tbl.currency.rate", typ: reflect.TypeOf(basemodels.ResCurrencyRateRecord{})},
		{name: "tbl.groups", typ: reflect.TypeOf(basemodels.ResGroupRecord{})},
		{name: "base.groups.privilege", typ: reflect.TypeOf(basemodels.ResGroupPrivilegeRecord{})},
		{name: "tbl.country", typ: reflect.TypeOf(basemodels.ResCountryRecord{})},
		{name: "tbl.country.group", typ: reflect.TypeOf(basemodels.ResCountryGroupRecord{})},
		{name: "tbl.country.state", typ: reflect.TypeOf(basemodels.ResCountryStateRecord{})},
		{name: "tbl.country.ward", typ: reflect.TypeOf(basemodels.ResCountryWardRecord{})},
		{name: "base.workflow", typ: reflect.TypeOf(workflowmodels.FlowRecord{})},
		{name: "base.workflow.action", typ: reflect.TypeOf(workflowmodels.WorkflowActionRecord{})},
		{name: "base.workflow.action.catalog", typ: reflect.TypeOf(workflowmodels.WorkflowActionCatalogRecord{})},
		{name: "base.workflow.credential", typ: reflect.TypeOf(workflowmodels.WorkflowCredentialRecord{})},
		{name: "base.workflow.run", typ: reflect.TypeOf(workflowmodels.WorkflowRunRecord{})},
		{name: "base.workflow.trigger", typ: reflect.TypeOf(workflowmodels.WorkflowTriggerRecord{})},
		{name: "base.workflow.outbound", typ: reflect.TypeOf(workflowmodels.OutboundDeliveryRecord{})},
	}

	for _, tc := range cases {
		baseField, ok := tc.typ.FieldByName("BaseModel")
		if !ok {
			t.Fatalf("%s must embed orm.BaseModel", tc.name)
		}
		if baseField.Type != reflect.TypeOf(ormpkg.BaseModel{}) {
			t.Fatalf("%s BaseModel field = %v, want orm.BaseModel", tc.name, baseField.Type)
		}

		// Guard against shadow fields that remap the archive column away from the
		// shared BaseModel.Active contract.
		for i := 0; i < tc.typ.NumField(); i++ {
			field := tc.typ.Field(i)
			if field.Name == "BaseModel" {
				continue
			}
			tag := strings.TrimSpace(field.Tag.Get("bun"))
			if tag == "" {
				continue
			}
			column := strings.TrimSpace(strings.Split(tag, ",")[0])
			if column == "active" {
				t.Fatalf("%s must not declare its own active column on field %s", tc.name, field.Name)
			}
		}
	}
}
