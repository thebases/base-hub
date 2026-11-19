package orm

// File overview:
// - Responsibility: verifies ORM runtime semantics, repository behavior, and registry coordination for model definition.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down runtime invariants so refactors preserve transaction, RBAC, registry, and storage behavior.

// These tests pin how model definitions are derived so the registry sees one
// stable metadata shape regardless of whether fields come from tags or schema.

import "testing"

type baseSimpleRecord struct {
	BaseModel
	Name  string `bun:"name,notnull,unique" json:"name" meta:"index"`
	Email string `bun:"email" json:"email"`
}

type baseRelationRecord struct {
	BaseModel
	PartnerID  int64          `bun:"partner_id,notnull" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:cascade"`
	TagIDs     []int64        `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:crm.tag"`
	ChildIDs   []int64        `bun:"child_ids,type:jsonb,nullzero" json:"child_ids" meta:"kind:one2many,model:x.child,inverse:parent_id"`
	Properties map[string]any `bun:"properties,type:jsonb,nullzero" json:"properties"`
}

type baseRelationCompanionRecord struct {
	BaseModel
	PartnerID int64     `bun:"partner_id,notnull" json:"partner_id" meta:"kind:one2one,model:tbl.partner,ondelete:cascade"`
	Partner   *struct{} `bun:"rel:belongs-to,join:partner_id=id" json:"-"`
}

type baseLabelRecord struct {
	BaseModel
	DisplayName string `bun:"display_name" json:"display_name" meta:"name:Display Name"`
	Email       string `bun:"email" json:"email" meta:"label:Email Address"`
}

type baseHelpRecord struct {
	BaseModel
	Description string `bun:"description" json:"description" meta:"help:Shown as helper text on the frontend."`
}

type baseSelectionRecord struct {
	BaseModel
	CompanyType string `bun:"company_type" json:"company_type" meta:"label:Company Type,index" selection:"(indi,Individual),(comp,Company)"`
}

type baseHTMLRecord struct {
	BaseModel
	Body string `bun:"body" json:"body" meta:"kind:html,label:Body"`
}

type legacyTypeOptionRecord struct {
	BaseModel
	Payload map[string]any `bun:"payload,type:jsonb,nullzero" json:"payload" meta:"type:json"`
}

type BaseComposedSharedRecord struct {
	BaseModel
	Name string `bun:"name,notnull" json:"name" meta:"index"`
	Type string `bun:"type,notnull" json:"type" meta:"index"`
}

type baseComposedRecord struct {
	BaseComposedSharedRecord
	URL string `bun:"url,notnull" json:"url"`
}

func TestModelDefinitionFromRecordMapsScalarFields(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.simple", (*baseSimpleRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	nameField, err := model.Field("name")
	if err != nil {
		t.Fatalf("expected reflected name field: %v", err)
	}
	if !nameField.Required {
		t.Fatal("expected name field to be required")
	}
	if !nameField.Unique {
		t.Fatal("expected name field to be unique")
	}
	if !nameField.Indexed {
		t.Fatal("expected name field to be indexed")
	}
}

func TestModelDefinitionFromRecordMapsRelations(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.relation", (*baseRelationRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	partnerField, err := model.Field("partner_id")
	if err != nil {
		t.Fatalf("expected partner_id field: %v", err)
	}
	if partnerField.Kind != FieldKindManyToOne {
		t.Fatalf("expected many2one kind, got %s", partnerField.Kind)
	}
	if partnerField.Relation != "tbl.partner" {
		t.Fatalf("expected relation tbl.partner, got %q", partnerField.Relation)
	}

	tagField, err := model.Field("tag_ids")
	if err != nil {
		t.Fatalf("expected tag_ids field: %v", err)
	}
	if tagField.Kind != FieldKindManyToMany {
		t.Fatalf("expected many2many kind, got %s", tagField.Kind)
	}
	if tagField.Stored {
		t.Fatal("expected many2many field to be metadata-only")
	}
	if tagField.RelationTable != "crm_tag_x_relation_rel" || tagField.RelationColumn != "x_relation_id" || tagField.RelationKey != "crm_tag_id" {
		t.Fatalf("unexpected many2many relation metadata: table=%q column=%q key=%q", tagField.RelationTable, tagField.RelationColumn, tagField.RelationKey)
	}

	propertiesField, err := model.Field("properties")
	if err != nil {
		t.Fatalf("expected properties field: %v", err)
	}
	if propertiesField.Kind != FieldKindJSON {
		t.Fatalf("expected json kind, got %s", propertiesField.Kind)
	}

	childField, err := model.Field("child_ids")
	if err != nil {
		t.Fatalf("expected child_ids field: %v", err)
	}
	if childField.Kind != FieldKindOneToMany {
		t.Fatalf("expected one2many kind, got %s", childField.Kind)
	}
	if childField.Stored {
		t.Fatal("expected one2many field to be explicitly non-stored")
	}
}

func TestModelDefinitionFromRecordSupportsOneToOneAndSkipsBunRelationCompanions(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.relation.companion", (*baseRelationCompanionRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	partnerField, err := model.Field("partner_id")
	if err != nil {
		t.Fatalf("expected partner_id field: %v", err)
	}
	if partnerField.Kind != FieldKindOneToOne {
		t.Fatalf("expected one2one kind, got %s", partnerField.Kind)
	}
	if !partnerField.Unique {
		t.Fatalf("expected one2one field to be unique")
	}
	if _, err := model.Field("partner"); err == nil {
		t.Fatalf("expected bun relation companion field to be skipped")
	}
}

func TestModelDefinitionFromRecordRejectsImplicitScalarRelationKinds(t *testing.T) {
	type invalidImplicitRelationRecord struct {
		BaseModel
		PartnerID int64 `bun:"partner_id" json:"partner_id" meta:"model:tbl.partner"`
	}

	_, err := ModelDefinitionFromRecord("x.invalid.relation", (*invalidImplicitRelationRecord)(nil))
	if err == nil {
		t.Fatalf("expected implicit scalar relation kind to fail")
	}
}

func TestModelDefinitionFromRecordMapsFieldLabels(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.label", (*baseLabelRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	displayNameField, err := model.Field("display_name")
	if err != nil {
		t.Fatalf("expected display_name field: %v", err)
	}
	if displayNameField.Label != "Display Name" {
		t.Fatalf("expected display_name label override, got %q", displayNameField.Label)
	}

	emailField, err := model.Field("email")
	if err != nil {
		t.Fatalf("expected email field: %v", err)
	}
	if emailField.Label != "Email Address" {
		t.Fatalf("expected email label override, got %q", emailField.Label)
	}
}

func TestModelDefinitionFromRecordMapsFieldHelp(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.help", (*baseHelpRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	descriptionField, err := model.Field("description")
	if err != nil {
		t.Fatalf("expected description field: %v", err)
	}
	if descriptionField.Help != "Shown as helper text on the frontend." {
		t.Fatalf("expected description help override, got %q", descriptionField.Help)
	}
}

func TestModelDefinitionFromRecordMapsSelections(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.selection", (*baseSelectionRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	field, err := model.Field("company_type")
	if err != nil {
		t.Fatalf("expected company_type field: %v", err)
	}
	if field.Kind != FieldKindSelection {
		t.Fatalf("expected selection kind, got %s", field.Kind)
	}
	if len(field.Selection) != 2 {
		t.Fatalf("expected two selection options, got %#v", field.Selection)
	}
	if field.Selection[0].Value != "indi" || field.Selection[0].Label != "Individual" {
		t.Fatalf("unexpected first selection option: %#v", field.Selection[0])
	}
}

func TestModelDefinitionFromRecordSupportsHTMLKindOverride(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.html", (*baseHTMLRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	field, err := model.Field("body")
	if err != nil {
		t.Fatalf("expected body field: %v", err)
	}
	if field.Kind != FieldKindHTML {
		t.Fatalf("expected html kind, got %s", field.Kind)
	}
}

func TestModelDefinitionFromRecordSupportsLegacyTypeOverride(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.legacy.type", (*legacyTypeOptionRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	field, err := model.Field("payload")
	if err != nil {
		t.Fatalf("expected payload field: %v", err)
	}
	if field.Kind != FieldKindJSON {
		t.Fatalf("expected legacy type override to map to json kind, got %s", field.Kind)
	}
}

func TestModelDefinitionFromRecordFlattensEmbeddedStructs(t *testing.T) {
	model, err := ModelDefinitionFromRecord("x.composed.action", (*baseComposedRecord)(nil))
	if err != nil {
		t.Fatalf("ModelDefinitionFromRecord returned error: %v", err)
	}

	for _, fieldName := range []string{"id", "created_at", "updated_at", "name", "type", "url"} {
		if _, err := model.Field(fieldName); err != nil {
			t.Fatalf("expected reflected %s field: %v", fieldName, err)
		}
	}
}
