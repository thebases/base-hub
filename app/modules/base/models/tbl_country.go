package models

// File overview:
// - Responsibility: defines the `base` module model contract for res country, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `init`, `createResCountry`, `prepareResCountry`, `ResCountryRecord`, `ResCountryGroupRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ResCountryRecord ports the tbl.country reference metadata and the small
// helper surface used by address rendering, search widgets, and phone-country
// lookup logic.
type ResCountryRecord struct {
	orm.BaseModel
	Name              string   `bun:"name" json:"name" meta:"translate,index,unique,label:Name,help:Stores the human readable name for this record."`
	Code              string   `bun:"code" json:"code" meta:"index,unique,label:Code,help:Stores the code value for this record."`
	AddressFormat     string   `bun:"address_format" json:"address_format" meta:"label:Address Format,help:Stores the address format value for this record."`
	AddressViewID     *int64   `bun:"address_view_id" json:"address_view_id" meta:"kind:many2one,model:ui.view,ondelete:SET NULL,index,label:Address View ID,help:Links this record to the related ui.view record for address view id."`
	CurrencyID        *int64   `bun:"currency_id" json:"currency_id" meta:"kind:many2one,model:tbl.currency,ondelete:SET NULL,index,label:Currency ID,help:Links this record to the related tbl.currency record for currency id."`
	ImageURL          string   `bun:"image_url" json:"image_url" meta:"compute:_compute_image_url,stored:false,label:Image URL,help:Stores the computed value for image url."`
	PhoneCode         int64    `bun:"phone_code" json:"phone_code" meta:"index,label:Phone Code,help:Stores the numeric value for phone code."`
	CountryGroupIDs   []int64  `bun:"country_group_ids,type:jsonb,nullzero" json:"country_group_ids" meta:"kind:many2many,model:tbl.country.group,relation_table:tbl_country_tbl_country_group_rel,relation_column:tbl_country_id,relation_key:tbl_country_group_id,label:Country Group IDs,help:Lists the related tbl.country.group records associated with this record."`
	TZ                string   `bun:"tz" json:"tz" meta:"label:Timezone,help:Main IANA timezone name (for example Asia/Ho_Chi_Minh) used to display datetimes to users in this country; a state timezone overrides it."`
	CountryGroupCodes []string `bun:"country_group_codes,type:jsonb,nullzero" json:"country_group_codes" meta:"kind:json,compute:_compute_country_group_codes,stored:false,label:Country Group Codes,help:Stores the computed JSON payload for country group codes."`
	StateIDs          []int64  `bun:"state_ids,type:jsonb,nullzero" json:"state_ids" meta:"kind:one2many,model:tbl.country.state,inverse:country_id,stored:false,label:State IDs,help:Lists the related tbl.country.state records linked back through country_id."`
	NamePosition      string   `bun:"name_position" json:"name_position" meta:"label:Name Position,help:Stores the name position value for this record."`
	VATLabel          string   `bun:"vat_label" json:"vat_label" meta:"label:VAT Label,help:Stores the vat label value for this record."`
	StateRequired     bool     `bun:"state_required" json:"state_required" meta:"label:State Required,help:Controls whether state required is enabled for this record."`
	ZIPRequired       bool     `bun:"zip_required" json:"zip_required" meta:"label:ZIP Required,help:Controls whether zip required is enabled for this record."`
}

const ResCountryModelName = "tbl.country"

var ResCountryDefinition = orm.ModelDefinition(ResCountryModelName, (*ResCountryRecord)(nil))

var ResCountryModelSpec = orm.ModelSpec{
	Name:        ResCountryModelName,
	Label:       "Country",
	Description: "Country reference data and address-format helpers",
	Definition:  ResCountryDefinition,
	Prepare:     prepareResCountry,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createResCountry},
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: nameSearchResCountry},
		{Name: "_phone_code_for", Public: true, ModelOnly: true, ReadOnly: true, Call: phoneCodeForResCountry},
		{Name: "get_address_fields", Public: true, ReadOnly: true, Call: getAddressFieldsResCountry},
	},
	// Physical storage for all tbl.country* models uses the tbl_* prefix while
	// the logical model names stay dotted; legacy tbl_country* tables are
	// renamed in place during migration so reference data survives.
}

var ResCountryModel = orm.Define[ResCountryRecord](&Models, 230, ResCountryModelSpec)

var countryFlagMapping = map[string]string{
	"GF": "fr", "BV": "no", "BQ": "nl", "GP": "fr", "HM": "au",
	"YT": "fr", "RE": "fr", "MF": "fr", "UM": "us", "XI": "uk",
}

var countryWithoutFlags = map[string]struct{}{
	"AQ": {},
	"SJ": {},
}

var countryAddressFormatKeyPattern = regexp.MustCompile(`\((.+?)\)`)

func init() {
	orm.RegisterCompute(ResCountryModelName, "image_url", computeImageURLResCountry)
	orm.RegisterCompute(ResCountryModelName, "country_group_codes", computeCountryGroupCodesResCountry)
}

// createResCountry keeps the list-payload create; defaults and validation run in prepareResCountry.
func createResCountry(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	return createRecordList(ctx, orm.RuntimeModelRef(ResCountryModelName, model), args, kwargs)
}

// prepareResCountry runs inside the guarded create/write: create fills the defaults, both upper-case
// the ISO code, and a sent address_format must only use known placeholders.
func prepareResCountry(_ context.Context, op orm.WriteOp) error {
	if !op.Create && len(op.IDs) == 0 {
		log.Errorf("tbl.country.write requires ids")
		return orm.InvalidParamsf("tbl.country.write requires ids")
	}
	if op.Create {
		applyResCountryDefaults(op.Values)
	}
	uppercaseValue(op.Values, "code")
	if raw, exists := op.Values["address_format"]; exists || op.Create {
		return validateCountryAddressFormat(strings.TrimSpace(stringValue(raw)))
	}
	return nil
}

func nameSearchResCountry(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	operator := firstNonEmpty(strings.TrimSpace(stringValue(kwargs["operator"])), "ilike")
	limit := int64Value(kwargs["limit"])
	if limit <= 0 {
		limit = 100
	}
	// A client call cannot list past server.maxReadLimit (plan T36).
	limit = int64(orm.ClampClientReadLimit(ctx, model, int(limit)))
	rows := []struct {
		ID   int64  `bun:"id"`
		Name string `bun:"name"`
		Code string `bun:"code"`
	}{}
	// Pin helper reads to the runtime table so Bun does not fall back to the
	// struct-derived plural table name during bootstrap imports.
	// name is a translate:true jsonb column: raw SQL must read the request-language text.
	nameExpr := orm.TranslatedColumnExpr(ctx, "c", "name")
	query := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country AS c").
		ColumnExpr("c.id").
		ColumnExpr(nameExpr + " AS name").
		ColumnExpr("c.code")
	if name != "" {
		if len(name) == 2 {
			query = query.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.Where("c.code = ?", strings.ToUpper(name)).WhereOr(nameExpr+" ILIKE ?", "%"+name+"%")
			})
		} else {
			query = query.Where(nameExpr+" ILIKE ?", "%"+name+"%")
		}
	}
	if strings.EqualFold(operator, "=") || strings.EqualFold(operator, "==") {
		query = orm.BunDBFromContext(ctx).NewSelect().
			TableExpr("tbl_country AS c").
			ColumnExpr("c.id").
			ColumnExpr(nameExpr+" AS name").
			ColumnExpr("c.code").
			Where(nameExpr+" = ? OR c.code = ?", name, strings.ToUpper(name))
	}
	if err := query.OrderExpr(nameExpr+" ASC").Limit(int(limit)).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{row.ID, row.Name})
	}
	return out, nil
}

func phoneCodeForResCountry(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	code := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["code"])))
	if code == "" {
		return int64(0), nil
	}
	rows := []struct {
		PhoneCode int64 `bun:"phone_code"`
	}{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country AS c").
		Column("phone_code").
		Where("code = ?", strings.ToUpper(code)).
		Limit(1).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return int64(0), nil
	}
	return rows[0].PhoneCode, nil
}

// getAddressFieldsResCountry retrieves the address fields res country handled by this package.
func getAddressFieldsResCountry(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.country.get_address_fields expected singleton")
		return nil, orm.InvalidParamsf("tbl.country.get_address_fields expected singleton")
	}
	rows := []struct {
		AddressFormat string `bun:"address_format"`
	}{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country AS c").
		Column("address_format").
		Where("id = ?", ids[0]).
		Limit(1).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return []string{}, nil
	}
	return countryAddressFormatFields(rows[0].AddressFormat), nil
}

// computeImageURLResCountry computes the image url res country handled by this package.
func computeImageURLResCountry(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	rows := []struct {
		ID   int64  `bun:"id"`
		Code string `bun:"code"`
	}{}
	// Computed-field readback runs during create/import verification, so this
	// query must target the physical runtime table explicitly.
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country AS c").
		Column("id", "code").
		Where("id IN (?)", bun.In(idsToAny(records.IDs()))).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		out[fmt.Sprintf("%d", row.ID)] = countryFlagURL(row.Code)
	}
	return out, nil
}

// computeCountryGroupCodesResCountry computes the country group codes res country handled by this package.
func computeCountryGroupCodesResCountry(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	rows := []struct {
		CountryID int64  `bun:"country_id"`
		GroupCode string `bun:"group_code"`
	}{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country_tbl_country_group_rel AS rel").
		ColumnExpr("rel.tbl_country_id AS country_id").
		ColumnExpr("grp.code AS group_code").
		Join("JOIN tbl_country_group AS grp ON grp.id = rel.tbl_country_group_id").
		Where("rel.tbl_country_id IN (?)", bun.In(idsToAny(records.IDs()))).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, records.Len())
	grouped := make(map[int64][]string)
	for _, row := range rows {
		if strings.TrimSpace(row.GroupCode) == "" {
			continue
		}
		grouped[row.CountryID] = append(grouped[row.CountryID], row.GroupCode)
	}
	// Walk each ids() entry in order so validation and side effects stay scoped to the current element.
	for _, id := range records.IDs() {
		key, ok := int64FromAny(id)
		if !ok {
			out[id] = []string{}
			continue
		}
		out[id] = grouped[key]
	}
	return out, nil
}

func applyResCountryDefaults(values map[string]any) {
	if _, exists := values["name_position"]; !exists {
		values["name_position"] = "before"
	}
	if _, exists := values["state_required"]; !exists {
		values["state_required"] = false
	}
	if _, exists := values["zip_required"]; !exists {
		values["zip_required"] = true
	}
}

func uppercaseValue(values map[string]any, key string) {
	text := strings.TrimSpace(stringValue(values[key]))
	if text == "" {
		return
	}
	values[key] = strings.ToUpper(text)
}

func countryFlagURL(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	if _, hidden := countryWithoutFlags[code]; hidden {
		return ""
	}
	if mapped, ok := countryFlagMapping[code]; ok {
		return "/base/static/img/country_flags/" + mapped + ".png"
	}
	return "/base/static/img/country_flags/" + strings.ToLower(code) + ".png"
}

func countryAddressFormatFields(format string) []string {
	matches := countryAddressFormatKeyPattern.FindAllStringSubmatch(format, -1)
	seen := make(map[string]struct{}, len(matches))
	fields := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		key := strings.TrimSuffix(strings.TrimSpace(match[1]), "s")
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		fields = append(fields, key)
	}
	return fields
}

// validateCountryAddressFormat validates the country address format handled by this package.
func validateCountryAddressFormat(format string) error {
	if strings.TrimSpace(format) == "" {
		return nil
	}
	allowed := map[string]struct{}{
		"street": {}, "street2": {}, "zip": {}, "city": {}, "state_code": {},
		"state_name": {}, "country_name": {}, "country_code": {}, "company_name": {},
	}
	// Walk each countryaddressformatfields(format) entry in order so validation and side effects stay scoped to the current element.
	for _, key := range countryAddressFormatFields(format) {
		if _, ok := allowed[key]; !ok {
			log.Errorf("tbl.country unknown address format key %q", key)
			return orm.InvalidParamsf("tbl.country unknown address format key %q", key)
		}
	}
	return nil
}
