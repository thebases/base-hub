package all

import (
	"testing"

	"git.thebase.vn/base_hub/internal/orm"
)

// TestUserCompanyIDsRelationTable pins the storage of base.users.company_ids: the kernel RBAC
// lookups (orm.rbacUserCompanyIDsColumn) read a user's authorized companies from this relation
// table, because ORM writes never fill the legacy jsonb column. If the field moves, login would
// silently fall back to that NULL column and cut every user to their primary company again.
func TestUserCompanyIDsRelationTable(t *testing.T) {
	bootstrapAllModules(t)
	model, ok := orm.LookupModel("base.users")
	if !ok {
		t.Fatal("base.users is not registered")
	}
	for _, field := range model.Fields {
		if field.Name != "company_ids" {
			continue
		}
		if field.Kind != orm.FieldKindManyToMany || field.RelationTable != "base_users_tbl_company_rel" ||
			field.RelationColumn != "base_users_id" || field.RelationKey != "tbl_company_id" {
			t.Fatalf("company_ids storage = kind %q table %q column %q key %q, want many2many base_users_tbl_company_rel(base_users_id, tbl_company_id)",
				field.Kind, field.RelationTable, field.RelationColumn, field.RelationKey)
		}
		return
	}
	t.Fatal("base.users has no company_ids field")
}
