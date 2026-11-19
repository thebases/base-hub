package orm

import (
	"context"
	"reflect"
	"testing"
)

func TestBuildDynamicStructTypeMapsSupportedDataTypes(t *testing.T) {
	rows := []systemModelFieldRow{
		{FieldName: "balance", Label: "Balance", DataType: "float", IsRequired: true},
		{FieldName: "owner_name", Label: "Owner Name", DataType: "text"},
		{FieldName: "opened_at", DataType: "datetime"},
		{FieldName: "closed_on", DataType: "date"},
		{FieldName: "is_frozen", DataType: "boolean"},
		{FieldName: "metadata", DataType: "json"},
		// Reserved names already provided by the embedded BaseModel must be
		// skipped rather than producing a duplicate struct field.
		{FieldName: "id", DataType: "integer"},
		{FieldName: "created_at", DataType: "datetime"},
	}

	typ, err := buildDynamicStructType(rows)
	if err != nil {
		t.Fatalf("buildDynamicStructType returned error: %v", err)
	}

	def, err := ModelDefinitionFromRecord("tbl.account", reflect.New(typ).Interface())
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	wantKinds := map[string]FieldKind{
		"id":         FieldKindID,
		"active":     FieldKindBoolean,
		"created_at": FieldKindDateTime,
		"updated_at": FieldKindDateTime,
		"balance":    FieldKindFloat,
		"owner_name": FieldKindText,
		"opened_at":  FieldKindDateTime,
		"closed_on":  FieldKindDate,
		"is_frozen":  FieldKindBoolean,
		"metadata":   FieldKindJSON,
	}
	if len(def.Fields) != len(wantKinds) {
		t.Fatalf("field count = %d, want %d (%v)", len(def.Fields), len(wantKinds), def.FieldNames())
	}
	for name, wantKind := range wantKinds {
		field, ok := def.Fields[name]
		if !ok {
			t.Fatalf("missing field %q", name)
		}
		if field.Kind != wantKind {
			t.Errorf("field %q kind = %q, want %q", name, field.Kind, wantKind)
		}
	}
	if !def.Fields["balance"].Required {
		t.Errorf("balance should be required")
	}
	if def.Fields["owner_name"].Required {
		t.Errorf("owner_name should not be required")
	}
}

func TestBuildDynamicStructTypeMapsRelationFields(t *testing.T) {
	rows := []systemModelFieldRow{
		{FieldName: "owner_id", Label: "Owner", DataType: "many2one", RelationModel: "tbl.partner"},
		{FieldName: "site_id", Label: "Site", DataType: "one2one", RelationModel: "tbl.site"},
	}

	typ, err := buildDynamicStructType(rows)
	if err != nil {
		t.Fatalf("buildDynamicStructType returned error: %v", err)
	}
	def, err := ModelDefinitionFromRecord("tbl.relation_test", reflect.New(typ).Interface())
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	owner, ok := def.Fields["owner_id"]
	if !ok {
		t.Fatal("missing owner_id field")
	}
	if owner.Kind != FieldKindManyToOne {
		t.Errorf("owner_id kind = %q, want many2one", owner.Kind)
	}
	if owner.Relation != "tbl.partner" {
		t.Errorf("owner_id relation = %q, want tbl.partner", owner.Relation)
	}

	site, ok := def.Fields["site_id"]
	if !ok {
		t.Fatal("missing site_id field")
	}
	if site.Kind != FieldKindOneToOne {
		t.Errorf("site_id kind = %q, want one2one", site.Kind)
	}
	if site.Relation != "tbl.site" {
		t.Errorf("site_id relation = %q, want tbl.site", site.Relation)
	}
}

func TestBuildDynamicStructTypeRejectsRelationWithoutModel(t *testing.T) {
	_, err := buildDynamicStructType([]systemModelFieldRow{
		{FieldName: "owner_id", DataType: "many2one"},
	})
	if err == nil {
		t.Fatal("expected an error when a relation field has no relation_model")
	}
}

func TestBuildDynamicStructTypeRejectsUnpublishableRelationKinds(t *testing.T) {
	for _, dataType := range []string{"one2many", "many2many"} {
		_, err := buildDynamicStructType([]systemModelFieldRow{
			{FieldName: "tags", DataType: dataType, RelationModel: "tbl.tag"},
		})
		if err == nil {
			t.Fatalf("expected an error for unpublishable relation data_type %q", dataType)
		}
	}
}

func TestBuildDynamicStructTypeRejectsUnsupportedDataType(t *testing.T) {
	_, err := buildDynamicStructType([]systemModelFieldRow{
		{FieldName: "weird", DataType: "point"},
	})
	if err == nil {
		t.Fatal("expected an error for unsupported data_type")
	}
}

func TestBuildDynamicStructTypeRejectsEmptyFieldSet(t *testing.T) {
	_, err := buildDynamicStructType([]systemModelFieldRow{
		{FieldName: "id", DataType: "integer"},
		{FieldName: "created_at", DataType: "datetime"},
	})
	if err == nil {
		t.Fatal("expected an error when every field row is a reserved base column")
	}
}

func TestPascalCaseFieldNameRejectsCollisions(t *testing.T) {
	used := map[string]struct{}{}
	first, err := pascalCaseFieldName("owner_name", used)
	if err != nil || first != "OwnerName" {
		t.Fatalf("first call = %q, %v", first, err)
	}
	// "owner-name" normalizes to the same Go identifier as "owner_name".
	if _, err := pascalCaseFieldName("owner-name", used); err == nil {
		t.Fatal("expected a collision error for a second field normalizing to the same Go name")
	}
}

func TestSanitizeTagValueStripsTagBreakingCharacters(t *testing.T) {
	got := sanitizeTagValue(`Owner, "VIP": account`)
	if got != "Owner  VIP  account" {
		t.Fatalf("sanitizeTagValue = %q", got)
	}
}

func TestUnpublishModelNoOpsWhenNotRegistered(t *testing.T) {
	// UnpublishModel must not touch the database at all for a model name
	// that isn't currently registered — it's an idempotent no-op, so this
	// must pass even with no BunDB configured in this test binary.
	if err := UnpublishModel(context.Background(), "tbl.never_published_anywhere"); err != nil {
		t.Fatalf("UnpublishModel on an unregistered model = %v, want nil", err)
	}
}

func TestUnpublishModelRejectsEmptyName(t *testing.T) {
	if err := UnpublishModel(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for a blank model name")
	}
}

func TestDropCustomModelTableRejectsEmptyStorageName(t *testing.T) {
	if err := DropCustomModelTable(context.Background(), "   "); err == nil {
		t.Fatal("expected an error for a blank storage name")
	}
}

func TestDynamicModelRegisterAndUnregisterRoundTrip(t *testing.T) {
	rows := []systemModelFieldRow{
		{FieldName: "balance", Label: "Balance", DataType: "float", IsRequired: true},
	}
	typ, err := buildDynamicStructType(rows)
	if err != nil {
		t.Fatalf("buildDynamicStructType: %v", err)
	}
	def, err := ModelDefinitionFromRecord("tbl.publish_test_account", reflect.New(typ).Interface())
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord: %v", err)
	}

	registry := NewModelRegistry()
	meta, err := registry.Register(typ, ModelSpec{Name: "tbl.publish_test_account", Label: "Test Account", Definition: def})
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	if meta.StorageName != "tbl_publish_test_account" {
		t.Errorf("StorageName = %q, want tbl_publish_test_account", meta.StorageName)
	}
	if _, ok := registry.Lookup("tbl.publish_test_account"); !ok {
		t.Fatal("expected model to be registered")
	}

	registry.Unregister("tbl.publish_test_account")
	if _, ok := registry.Lookup("tbl.publish_test_account"); ok {
		t.Fatal("expected model to be unregistered")
	}
}
