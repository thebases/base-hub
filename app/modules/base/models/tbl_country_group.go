package models

// File overview:
// - Responsibility: defines the `base` module model contract for country group, including field metadata and runtime methods.
// - Key symbols: `ResCountryGroupRecord`, `createResCountryGroup`, `prepareResCountryGroup`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
)

type ResCountryGroupRecord struct {
	orm.BaseModel
	// translate:true (SRS-F-021): one name per language, resolved by context.lang with en fallback.
	Name       string  `bun:"name" json:"name" meta:"translate,index,label:Name,help:Stores the human readable name for this record."`
	Code       string  `bun:"code" json:"code" meta:"index,unique,label:Code,help:Stores the code value for this record."`
	CountryIDs []int64 `bun:"country_ids,type:jsonb,nullzero" json:"country_ids" meta:"kind:many2many,model:tbl.country,relation_table:tbl_country_tbl_country_group_rel,relation_column:tbl_country_group_id,relation_key:tbl_country_id,label:Country IDs,help:Lists the related tbl.country records associated with this record."`
}

const ResCountryGroupModelName = "tbl.country.group"

var ResCountryGroupDefinition = orm.ModelDefinition(ResCountryGroupModelName, (*ResCountryGroupRecord)(nil))

var ResCountryGroupModelSpec = orm.ModelSpec{
	Name:        ResCountryGroupModelName,
	Label:       "Country Group",
	Description: "Country grouping metadata",
	Definition:  ResCountryGroupDefinition,
	Prepare:     prepareResCountryGroup,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createResCountryGroup},
	},
}

var ResCountryGroupModel = orm.Define[ResCountryGroupRecord](&Models, 240, ResCountryGroupModelSpec)

// createResCountryGroup keeps the list-payload create; prepareResCountryGroup normalises the code.
func createResCountryGroup(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createRecordList(ctx, orm.RuntimeModelRef(ResCountryGroupModelName, model), args, kwargs)
}

// prepareResCountryGroup upper-cases the group code on the guarded create and write.
func prepareResCountryGroup(_ context.Context, op orm.WriteOp) error {
	uppercaseValue(op.Values, "code")
	return nil
}
