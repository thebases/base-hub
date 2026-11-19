package models

// File overview:
// - Responsibility: defines the `base` module model contract for res company, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `init`, `defaultGetResCompany`, `ResCompanyRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// ResCompanyRecord provides the company catalog used to scope one
// database into multiple legal entities while keeping room for shared rows.
type ResCompanyRecord struct {
	orm.BaseModel
	Name            string  `bun:"name" json:"name" meta:"unique,index,label:Name,help:Stores the human readable company name."`
	Sequence        int64   `bun:"sequence" json:"sequence" meta:"index,label:Sequence,help:Orders companies in switchers and selectors."`
	ParentID        *int64  `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:tbl.company,ondelete:RESTRICT,index,label:Parent Company,help:Links this company to its parent company when operating as a branch."`
	ChildIDs        []int64 `bun:"child_ids,type:jsonb,nullzero" json:"child_ids" meta:"kind:one2many,model:tbl.company,inverse:parent_id,stored:false,label:Branches,help:Lists the branch companies linked through parent_id."`
	IsBranch        bool    `bun:"is_branch" json:"is_branch" meta:"index,label:Is Branch,help:Indicates whether this company behaves as a branch of another company."`
	PartnerID       *int64  `bun:"partner_id" json:"partner_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Partner ID,help:Links the company to its primary partner/contact record."`
	Email           string  `bun:"email" json:"email" meta:"index,label:Email,help:Stores the company email address."`
	Phone           string  `bun:"phone" json:"phone" meta:"index,label:Phone,help:Stores the company phone number."`
	Website         string  `bun:"website" json:"website" meta:"label:Website,help:Stores the company website."`
	Street          string  `bun:"street" json:"street" meta:"label:Street,help:Stores the first address line."`
	Street2         string  `bun:"street2" json:"street2" meta:"label:Street 2,help:Stores the second address line."`
	ZIP             string  `bun:"zip" json:"zip" meta:"index,label:ZIP,help:Stores the postal code."`
	City            string  `bun:"city" json:"city" meta:"index,label:City,help:Stores the city."`
	StateID         *int64  `bun:"state_id" json:"state_id" meta:"kind:many2one,model:tbl.country.state,ondelete:SET NULL,index,label:State,help:Links this company to its state or province."`
	CountryID       *int64  `bun:"country_id" json:"country_id" meta:"kind:many2one,model:tbl.country,ondelete:SET NULL,index,label:Country,help:Links this company to its country."`
	VAT             string  `bun:"vat" json:"vat" meta:"index,label:VAT,help:Stores the tax identifier."`
	CompanyRegistry string  `bun:"company_registry" json:"company_registry" meta:"index,label:Company Registry,help:Stores the company registration identifier."`
	CurrencyCode    string  `bun:"currency_code" json:"currency_code" meta:"index,label:Currency Code,help:Stores the functional currency code for this company or branch tree."`
}

var ResCompanyModelName = "tbl.company"

var ResCompanyDefinition = orm.ModelDefinition(ResCompanyModelName, (*ResCompanyRecord)(nil))

var ResCompanyModelSpec = orm.ModelSpec{
	Name:        ResCompanyModelName,
	Label:       "Companies",
	Description: "Company catalog used for multi-company scoping inside one database",
	Definition:  ResCompanyDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "default_get", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultGetResCompany},
		{Name: "create", Public: true, ModelOnly: true, Call: createResCompany},
		{Name: "write", Public: true, UsesPathID: true, Call: writeResCompany},
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: nameSearchResCompany},
	},
	// Physical storage uses the tbl_* prefix while the logical model name stays
	// dotted (tbl.company); the legacy tbl_company table is renamed in place
	// during migration so company rows and FK targets survive.
}

var ResCompanyModel = orm.Define[ResCompanyRecord](&Models, 190, ResCompanyModelSpec)

func init() {
	orm.RegisterConstraint(ResCompanyModelName, []string{"parent_id", "country_id", "currency_code"}, validateResCompanyBranchRules)
}

// defaultGetResCompany selects the get res company handled by this package.
func defaultGetResCompany(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	fields := stringSlice(firstPresent(argAt(args, 0), kwargs["fields"], kwargs["field_names"]))
	out := map[string]any{}
	if len(fields) == 0 || containsString(fields, "active") {
		out["active"] = true
	}
	if len(fields) == 0 || containsString(fields, "sequence") {
		out["sequence"] = 10
	}
	if len(fields) == 0 || containsString(fields, "is_branch") {
		out["is_branch"] = false
	}
	return out, nil
}

// createResCompany creates the res company handled by this package.
func createResCompany(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("tbl.company.create requires values")
		return nil, orm.InvalidParamsf("tbl.company.create requires values")
	}
	working := cloneRecordMap(values)
	normalizeResCompanyValues(working)
	record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(ResCompanyModelName, model), working)
	if err != nil {
		return nil, err
	}
	// Give the company its linked partner (or push its values onto the one supplied) so both rows start identical.
	if err := createCompanyPartner(ctx, record, working); err != nil {
		return nil, err
	}
	// Company 1's Technical Features users must always be able to switch into every company.
	if newID, _ := int64FromAny(record["id"]); newID > 0 {
		if err := grantRootSuperadminsCompany(ctx, newID); err != nil {
			return nil, err
		}
	}
	return record, nil
}

func writeResCompany(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("tbl.company.write requires values")
		return nil, orm.InvalidParamsf("tbl.company.write requires values")
	}
	working := cloneRecordMap(values)
	normalizeResCompanyValues(working)
	result, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(ResCompanyModelName, model), ids, working)
	if err != nil {
		return nil, err
	}
	// Mirror same-named field changes onto the linked partner; no-op when the write came from the partner side.
	if err := syncCompanyValuesToPartners(ctx, ids, pickCompanyPartnerSharedValues(working)); err != nil {
		return nil, err
	}
	return result, nil
}

func nameSearchResCompany(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	filter := orm.RecordMap{"__limit": 100, "__order": "sequence asc, name asc"}
	if name != "" {
		filter["__domain"] = orm.Or(
			orm.Where("name", "ilike", name),
			orm.Where("email", "ilike", name),
			orm.Where("company_registry", "ilike", name),
		)
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ResCompanyModelName, model), filter, []string{"id", "name"})
	if err != nil {
		return nil, err
	}
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{int64Value(row["id"]), strings.TrimSpace(stringValue(row["name"]))})
	}
	return out, nil
}

func normalizeResCompanyValues(values orm.RecordMap) {
	if values == nil {
		return
	}
	if _, exists := values["active"]; !exists {
		values["active"] = true
	}
	if _, exists := values["sequence"]; !exists {
		values["sequence"] = 10
	}
	if _, exists := values["is_branch"]; !exists {
		parentID, _ := int64FromAny(values["parent_id"])
		values["is_branch"] = parentID > 0
	}
	if parentID, ok := int64FromAny(values["parent_id"]); ok && parentID > 0 {
		values["is_branch"] = true
	}
}

// validateResCompanyBranchRules validates the res company branch rules handled by this package.
func validateResCompanyBranchRules(ctx context.Context, records orm.RecordSet, changedValues orm.RecordMap) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResCompanyModelName, nil), records.IDs(), []string{"id", "name", "parent_id", "country_id", "currency_code"})
	if err != nil {
		return err
	}
	parentRowsByID, err := loadResCompanyParentRows(ctx, rows)
	if err != nil {
		return err
	}
	for _, row := range rows {
		parentID, _ := many2oneID(row["parent_id"])
		if parentID <= 0 {
			continue
		}
		parentRow, ok := parentRowsByID[parentID]
		if !ok {
			log.Errorf("tbl.company parent %d does not exist", parentID)
			return orm.NotFoundf("tbl.company parent %d does not exist", parentID)
		}
		parentCountryID, _ := many2oneID(parentRow["country_id"])
		recordCountryID, _ := many2oneID(row["country_id"])
		if parentCountryID > 0 && recordCountryID > 0 && parentCountryID != recordCountryID {
			log.Errorf("tbl.company branches must stay in the same country as their parent")
			return orm.InvalidParamsf("tbl.company branches must stay in the same country as their parent")
		}
		parentCurrency := strings.TrimSpace(stringValue(parentRow["currency_code"]))
		recordCurrency := strings.TrimSpace(stringValue(row["currency_code"]))
		if parentCurrency != "" && recordCurrency != "" && !strings.EqualFold(parentCurrency, recordCurrency) {
			log.Errorf("tbl.company branches must use the same currency_code as their parent")
			return orm.InvalidParamsf("tbl.company branches must use the same currency_code as their parent")
		}
	}
	return nil
}

func loadResCompanyParentRows(ctx context.Context, rows []orm.RecordMap) (map[int64]orm.RecordMap, error) {
	parentIDs := make([]string, 0, len(rows))
	seen := make(map[int64]struct{}, len(rows))
	// Batch parent lookups once so branch validation stays linear even when many rows share the same parent.
	for _, row := range rows {
		parentID, _ := many2oneID(row["parent_id"])
		if parentID <= 0 {
			continue
		}
		if _, exists := seen[parentID]; exists {
			continue
		}
		seen[parentID] = struct{}{}
		parentIDs = append(parentIDs, fmt.Sprintf("%d", parentID))
	}
	if len(parentIDs) == 0 {
		return map[int64]orm.RecordMap{}, nil
	}
	parentRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResCompanyModelName, nil), parentIDs, []string{"id", "country_id", "currency_code"})
	if err != nil {
		return nil, err
	}
	parentRowsByID := make(map[int64]orm.RecordMap, len(parentRows))
	// Key parent rows by id so each branch comparison can reuse the same loaded data without more runtime reads.
	for _, row := range parentRows {
		parentID, _ := int64FromAny(row["id"])
		if parentID <= 0 {
			continue
		}
		parentRowsByID[parentID] = row
	}
	return parentRowsByID, nil
}
