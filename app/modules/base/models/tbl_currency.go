package models

// File overview:
// - Responsibility: defines the `base` module model contract for res currency, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `init`, `createResCurrency`, `prepareResCurrency`, `ResCurrencyRecord`, `ResCurrencyRateRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

// ResCurrencyRecord ports ERP currency metadata and conversion helpers into
// the metadata-driven runtime used by the base module.
type ResCurrencyRecord struct {
	orm.BaseModel
	Name                     string  `bun:"name" json:"name" meta:"index,unique,label:Name,help:Stores the ISO currency code for this record."`
	ISONumeric               string  `bun:"iso_numeric" json:"iso_numeric" meta:"label:ISO Numeric,help:Stores the numeric ISO code value for this record."`
	FullName                 string  `bun:"full_name" json:"full_name" meta:"index,label:Full Name,help:Stores the full currency name for this record."`
	Symbol                   string  `bun:"symbol" json:"symbol" meta:"label:Symbol,help:Stores the currency symbol value for this record."`
	Rate                     float64 `bun:"rate" json:"rate" meta:"compute:_compute_current_rate,stored:false,label:Rate,help:Stores the computed conversion rate for this record."`
	InverseRate              float64 `bun:"inverse_rate" json:"inverse_rate" meta:"compute:_compute_current_rate,stored:false,label:Inverse Rate,help:Stores the computed inverse conversion rate for this record."`
	RateString               string  `bun:"rate_string" json:"rate_string" meta:"compute:_compute_current_rate,stored:false,label:Rate String,help:Stores the computed human-readable rate string for this record."`
	RateIDs                  []int64 `bun:"rate_ids,type:jsonb,nullzero" json:"rate_ids" meta:"kind:one2many,model:tbl.currency.rate,inverse:currency_id,stored:false,label:Rate IDs,help:Lists the related tbl.currency.rate records linked back through currency_id."`
	Rounding                 float64 `bun:"rounding" json:"rounding" meta:"label:Rounding,help:Stores the rounding factor used for this currency."`
	DecimalPlaces            int64   `bun:"decimal_places" json:"decimal_places" meta:"compute:_compute_decimal_places,stored:false,label:Decimal Places,help:Stores the computed decimal precision for this record."`
	Position                 string  `bun:"position" json:"position" meta:"label:Position,help:Stores whether the symbol renders before or after the amount."`
	Date                     string  `bun:"date" json:"date" meta:"compute:_compute_date,stored:false,label:Date,help:Stores the newest technical rate date for this record."`
	CurrencyUnitLabel        string  `bun:"currency_unit_label" json:"currency_unit_label" meta:"label:Currency Unit Label,help:Stores the unit label for amount-to-text formatting."`
	CurrencySubunitLabel     string  `bun:"currency_subunit_label" json:"currency_subunit_label" meta:"label:Currency Subunit Label,help:Stores the subunit label for amount-to-text formatting."`
	IsCurrentCompanyCurrency bool    `bun:"is_current_company_currency" json:"is_current_company_currency" meta:"compute:_compute_is_current_company_currency,stored:false,label:Is Current Company Currency,help:Controls whether this record matches the current company currency in context."`
}

// ResCurrencyRateRecord stores dated technical rates for one currency and
// optional company-specific overrides.
type ResCurrencyRateRecord struct {
	orm.BaseModel
	Name               string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the rate date in YYYY-MM-DD form for this record."`
	Rate               float64 `bun:"rate" json:"rate" meta:"label:Rate,help:Stores the technical conversion rate for this record."`
	CompanyRate        float64 `bun:"company_rate" json:"company_rate" meta:"compute:_compute_company_rate,stored:false,label:Company Rate,help:Stores the computed company-normalized rate for this record."`
	InverseCompanyRate float64 `bun:"inverse_company_rate" json:"inverse_company_rate" meta:"compute:_compute_inverse_company_rate,stored:false,label:Inverse Company Rate,help:Stores the computed inverse company-normalized rate for this record."`
	CurrencyID         int64   `bun:"currency_id" json:"currency_id" meta:"kind:many2one,model:tbl.currency,ondelete:CASCADE,index,label:Currency ID,help:Links this record to the related tbl.currency record for currency id."`
	CompanyID          *int64  `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
}

type resCurrencyRow struct {
	bun.BaseModel `bun:"table:tbl_currency,alias:tbl_currency"`
	ID            int64   `bun:"id"`
	Name          string  `bun:"name"`
	Symbol        string  `bun:"symbol"`
	Position      string  `bun:"position"`
	Rounding      float64 `bun:"rounding"`
	DecimalPlaces int64   `bun:"decimal_places"`
	Active        bool    `bun:"active"`
}

type resCurrencyRateRow struct {
	bun.BaseModel `bun:"table:tbl_currency_rate,alias:tbl_currency_rate"`
	ID            int64   `bun:"id"`
	Name          string  `bun:"name"`
	Rate          float64 `bun:"rate"`
	CurrencyID    int64   `bun:"currency_id"`
	CompanyID     *int64  `bun:"company_id"`
}

const ResCurrencyModelName = "tbl.currency"
const ResCurrencyRateModelName = "tbl.currency.rate"

var ResCurrencyDefinition = orm.ModelDefinition(ResCurrencyModelName, (*ResCurrencyRecord)(nil))
var ResCurrencyRateDefinition = orm.ModelDefinition(ResCurrencyRateModelName, (*ResCurrencyRateRecord)(nil))

var ResCurrencyModelSpec = orm.ModelSpec{
	Name:        ResCurrencyModelName,
	Label:       "Currency",
	Description: "ISO currency metadata, rounding rules, and conversion helpers",
	Definition:  ResCurrencyDefinition,
	Prepare:     prepareResCurrency,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createResCurrency},
		{Name: "write", Public: true, UsesPathID: true, Call: writeResCurrency},
		{Name: "unlink", Public: true, UsesPathID: true, Call: unlinkResCurrency},
		{Name: "_toggle_group_multi_currency", Public: false, ModelOnly: true, Call: toggleGroupMultiCurrencyResCurrency},
		{Name: "_activate_group_multi_currency", Public: false, ModelOnly: true, Call: activateGroupMultiCurrencyResCurrency},
		{Name: "_deactivate_group_multi_currency", Public: false, ModelOnly: true, Call: deactivateGroupMultiCurrencyResCurrency},
		{Name: "_get_rates", Public: false, ReadOnly: true, Call: getRatesResCurrency},
		{Name: "amount_to_text", Public: true, ReadOnly: true, Call: amountToTextResCurrency},
		{Name: "format", Public: true, ReadOnly: true, Call: formatResCurrency},
		{Name: "round", Public: true, ReadOnly: true, Call: roundResCurrency},
		{Name: "compare_amounts", Public: true, ReadOnly: true, Call: compareAmountsResCurrency},
		{Name: "is_zero", Public: true, ReadOnly: true, Call: isZeroResCurrency},
		{Name: "get_all_currencies", Public: true, ModelOnly: true, ReadOnly: true, Call: getAllCurrenciesResCurrency},
		{Name: "_get_conversion_rate", Public: false, ModelOnly: true, ReadOnly: true, Call: getConversionRateResCurrency},
		{Name: "_convert", Public: true, ReadOnly: true, Call: convertResCurrency},
		{Name: "_select_companies_rates", Public: false, ModelOnly: true, ReadOnly: true, Call: selectCompaniesRatesResCurrency},
	},
	// Physical storage uses the tbl_* prefix while the logical model name stays
	// dotted (tbl.currency); the legacy tbl_currency table is renamed in place
	// during migration so rate and rounding data survive.
}

var ResCurrencyRateModelSpec = orm.ModelSpec{
	Name:        ResCurrencyRateModelName,
	Label:       "Currency Rate",
	Description: "Dated technical currency rates and company-specific overrides",
	Definition:  ResCurrencyRateDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "create", Public: true, ModelOnly: true, Call: createResCurrencyRate},
		{Name: "write", Public: true, UsesPathID: true, Call: writeResCurrencyRate},
		{Name: "_sanitize_vals", Public: false, ModelOnly: true, ReadOnly: true, Call: sanitizeValsResCurrencyRate},
		{Name: "_get_latest_rate", Public: false, ReadOnly: true, Call: getLatestRateResCurrencyRate},
		{Name: "_get_last_rates_for_companies", Public: false, ModelOnly: true, ReadOnly: true, Call: getLastRatesForCompaniesResCurrencyRate},
		{Name: "_onchange_rate_warning", Public: true, ReadOnly: true, Call: onchangeRateWarningResCurrencyRate},
		{Name: "_search_display_name", Public: false, ModelOnly: true, ReadOnly: true, Call: searchDisplayNameResCurrencyRate},
	},
}

var ResCurrencyModel = orm.Define[ResCurrencyRecord](&Models, 225, ResCurrencyModelSpec)
var ResCurrencyRateModel = orm.Define[ResCurrencyRateRecord](&Models, 226, ResCurrencyRateModelSpec)

func init() {
	orm.RegisterCompute(ResCurrencyModelName, "rate", computeCurrentRateResCurrency)
	orm.RegisterCompute(ResCurrencyModelName, "inverse_rate", computeCurrentRateResCurrency)
	orm.RegisterCompute(ResCurrencyModelName, "rate_string", computeCurrentRateResCurrency)
	orm.RegisterCompute(ResCurrencyModelName, "decimal_places", computeDecimalPlacesResCurrency)
	orm.RegisterCompute(ResCurrencyModelName, "date", computeDateResCurrency)
	orm.RegisterCompute(ResCurrencyModelName, "is_current_company_currency", computeIsCurrentCompanyCurrencyResCurrency)
	orm.RegisterCompute(ResCurrencyRateModelName, "company_rate", computeCompanyRateResCurrencyRate)
	orm.RegisterCompute(ResCurrencyRateModelName, "inverse_company_rate", computeInverseCompanyRateResCurrencyRate)
	orm.RegisterConstraint(ResCurrencyModelName, []string{"active"}, validateCompanyCurrencyActiveResCurrency)
	orm.RegisterConstraint(ResCurrencyRateModelName, []string{"company_id"}, validateMainCompanyResCurrencyRate)
}

// createResCurrency keeps the list-payload create (defaults and validation run in prepareResCurrency)
// and refreshes the multi-currency group once every row is stored.
func createResCurrency(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}

	ref := orm.RuntimeModelRef(ResCurrencyModelName, model)
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		record, err := orm.CreateRecord(ctx, ref, values)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}

	if _, err := toggleGroupMultiCurrencyResCurrency(ctx, nil, nil, nil, nil); err != nil {
		return nil, err
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeResCurrency(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("tbl.currency.write requires values")
		return nil, orm.InvalidParamsf("tbl.currency.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("tbl.currency.write requires ids")
		return nil, orm.InvalidParamsf("tbl.currency.write requires ids")
	}
	updated, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(ResCurrencyModelName, model), ids, values)
	if err != nil {
		return nil, err
	}
	if _, changed := values["active"]; changed {
		if _, err := toggleGroupMultiCurrencyResCurrency(ctx, nil, nil, nil, nil); err != nil {
			return nil, err
		}
	}
	return updated, nil
}

// prepareResCurrency runs inside the guarded create/write. It runs after the computed-field check,
// so the decimal_places default (a computed field, dropped by the repository) does not trip it.
func prepareResCurrency(_ context.Context, op orm.WriteOp) error {
	if op.Create {
		applyResCurrencyDefaults(op.Values)
	}
	uppercaseValue(op.Values, "name")
	return validateResCurrencyValues(op.Values)
}

func unlinkResCurrency(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) == 0 {
		log.Errorf("tbl.currency.unlink requires ids")
		return nil, orm.InvalidParamsf("tbl.currency.unlink requires ids")
	}
	deleted, err := orm.RuntimeRepositoryUnlink(ctx, orm.RuntimeModelRef(ResCurrencyModelName, model), ids)
	if err != nil {
		return nil, err
	}
	if _, err := toggleGroupMultiCurrencyResCurrency(ctx, nil, nil, nil, nil); err != nil {
		return nil, err
	}
	return deleted, nil
}

func toggleGroupMultiCurrencyResCurrency(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	count, err := activeCurrencyCount(ctx)
	if err != nil {
		return nil, err
	}
	if count > 1 {
		return activateGroupMultiCurrencyResCurrency(ctx, nil, nil, nil, nil)
	}
	return deactivateGroupMultiCurrencyResCurrency(ctx, nil, nil, nil, nil)
}

func activateGroupMultiCurrencyResCurrency(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return map[string]any{"group_multi_currency": true}, nil
}

func deactivateGroupMultiCurrencyResCurrency(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return map[string]any{"group_multi_currency": false}, nil
}

// getRatesResCurrency retrieves the rates res currency handled by this package.
func getRatesResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	date := currencyDateArg("date", 1, args, kwargs)
	companyID := currencyOptionalID("company_id", 0, args, kwargs)
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	return latestRatesForCurrencies(ctx, idsToInt64(ids), companyID, date)
}

func amountToTextResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.amount_to_text expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.amount_to_text expected singleton")
	}
	amount := floatValueOrDefault(argValue(args, 0), 0)
	rows, err := loadCurrencyRows(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return "", nil
	}
	unit := strings.TrimSpace(rows[0].Name)
	if unit == "" {
		unit = "Unit"
	}
	return fmt.Sprintf("%.2f %s", amount, unit), nil
}

func formatResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.format expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.format expected singleton")
	}
	amount := floatValueOrDefault(argValue(args, 0), 0)
	rows, err := loadCurrencyRows(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return fmt.Sprintf("%.2f", amount), nil
	}
	return formatCurrencyAmount(rows[0], amount), nil
}

func roundResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.round expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.round expected singleton")
	}
	row, err := singletonCurrencyRow(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	return roundToFactor(floatValueOrDefault(argValue(args, 0), 0), row.Rounding), nil
}

// compareAmountsResCurrency compares the amounts res currency handled by this package.
func compareAmountsResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.compare_amounts expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.compare_amounts expected singleton")
	}
	row, err := singletonCurrencyRow(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	left := roundToFactor(floatValueOrDefault(argValue(args, 0), 0), row.Rounding)
	right := roundToFactor(floatValueOrDefault(argValue(args, 1), 0), row.Rounding)
	switch {
	case left < right:
		return int64(-1), nil
	case left > right:
		return int64(1), nil
	default:
		return int64(0), nil
	}
}

func isZeroResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.is_zero expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.is_zero expected singleton")
	}
	row, err := singletonCurrencyRow(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	return roundToFactor(floatValueOrDefault(argValue(args, 0), 0), row.Rounding) == 0, nil
}

// getAllCurrenciesResCurrency retrieves the all currencies res currency handled by this package.
func getAllCurrenciesResCurrency(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	rows := []resCurrencyRow{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		Model(&rows).
		Where("active IS TRUE").
		OrderExpr("name ASC").
		Scan(ctx); err != nil {
		return nil, err
	}
	result := make(map[int64]map[string]any, len(rows))
	for _, row := range rows {
		result[row.ID] = map[string]any{
			"name":     row.Name,
			"symbol":   row.Symbol,
			"position": row.Position,
			"digits":   []int64{69, row.DecimalPlaces},
		}
	}
	return result, nil
}

// getConversionRateResCurrency retrieves the conversion rate res currency handled by this package.
func getConversionRateResCurrency(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	fromID := currencyRequiredID("from_currency", 0, args, kwargs)
	toID := currencyRequiredID("to_currency", 1, args, kwargs)
	if fromID == 0 || toID == 0 || fromID == toID {
		return float64(1), nil
	}
	date := currencyDateArg("date", 3, args, kwargs)
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	companyID := currencyOptionalID("company_id", 2, args, kwargs)
	// Money conversion must never guess: a missing rate is ErrNoRate, not 1.
	rates, err := requiredRatesForCurrencies(ctx, []int64{fromID, toID}, companyID, date)
	if err != nil {
		return nil, err
	}
	return rates[fromID] / rates[toID], nil
}

func convertResCurrency(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency._convert expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency._convert expected singleton")
	}
	amount := floatValueOrDefault(argValue(args, 0), 0)
	toID := currencyRequiredID("to_currency", 1, args, kwargs)
	if toID == 0 {
		log.Errorf("tbl.currency._convert requires to_currency")
		return nil, orm.InvalidParamsf("tbl.currency._convert requires to_currency")
	}
	rateRaw, err := getConversionRateResCurrency(ctx, nil, nil, []any{ids[0], toID, argValue(args, 2), argValue(args, 3)}, kwargs)
	if err != nil {
		return nil, err
	}
	converted := amount * floatValueOrDefault(rateRaw, 1)
	roundResult := true
	if raw := currencyValueFromArgs("round", 4, args, kwargs); raw != nil {
		roundResult = boolValueOrDefault(raw, true)
	}
	if !roundResult {
		return converted, nil
	}
	target, err := singletonCurrencyRow(ctx, strconv.FormatInt(toID, 10))
	if err != nil {
		return nil, err
	}
	return roundToFactor(converted, target.Rounding), nil
}

func selectCompaniesRatesResCurrency(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return strings.TrimSpace(`
SELECT
    r.currency_id AS currency_id,
    COALESCE(r.company_id, c.id) AS company_id,
    r.rate,
    r.name AS date_start,
    (SELECT name FROM tbl_currency_rate r2
     WHERE r2.name > r.name
       AND r2.currency_id = r.currency_id
       AND (r2.company_id IS NULL OR r2.company_id = c.id)
     ORDER BY r2.name ASC
     LIMIT 1) AS date_end
FROM tbl_currency_rate r
JOIN tbl_company c ON (r.company_id IS NULL OR r.company_id = c.id)`), nil
}

// createResCurrencyRate creates the res currency rate handled by this package.
func createResCurrencyRate(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := recordMapListFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if len(valuesList) == 0 {
		log.Errorf("tbl.currency.rate.create requires values")
		return nil, orm.InvalidParamsf("tbl.currency.rate.create requires values")
	}
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		working := sanitizeResCurrencyRateVals(cloneRecordMap(values))
		applyResCurrencyRateDefaults(working)
		if err := validateResCurrencyRateValues(working); err != nil {
			return nil, err
		}
		// Raw repository create on purpose: company_rate/inverse_company_rate are computed fields
		// accepted here as inverse inputs for rate (CreateRecord refuses them), and CreateRecord would
		// pin company_id, turning a technical (shared) rate into a company override.
		record, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(ResCurrencyRateModelName, model), working)
		if err != nil {
			return nil, err
		}
		created = append(created, record)
	}
	if len(created) == 1 {
		return created[0], nil
	}
	return created, nil
}

func writeResCurrencyRate(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("tbl.currency.rate.write requires values")
		return nil, orm.InvalidParamsf("tbl.currency.rate.write requires values")
	}
	if len(ids) == 0 {
		log.Errorf("tbl.currency.rate.write requires ids")
		return nil, orm.InvalidParamsf("tbl.currency.rate.write requires ids")
	}
	working := sanitizeResCurrencyRateVals(cloneRecordMap(values))
	if err := validateResCurrencyRateValues(working); err != nil {
		return nil, err
	}
	// Raw repository write for the same computed inverse inputs as create; the T15 override guard
	// already checks record rules and an explicit company_id before this override runs.
	return orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(ResCurrencyRateModelName, model), ids, working)
}

func sanitizeValsResCurrencyRate(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return map[string]any{}, nil
	}
	return sanitizeResCurrencyRateVals(cloneRecordMap(values)), nil
}

// getLatestRateResCurrencyRate retrieves the latest rate res currency rate handled by this package.
func getLatestRateResCurrencyRate(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	if len(ids) != 1 {
		log.Errorf("tbl.currency.rate._get_latest_rate expected singleton")
		return nil, orm.InvalidParamsf("tbl.currency.rate._get_latest_rate expected singleton")
	}
	row, err := loadCurrencyRateRow(ctx, ids[0])
	if err != nil {
		return nil, err
	}
	return latestRateBefore(ctx, row.CurrencyID, row.CompanyID, row.Name), nil
}

// getLastRatesForCompaniesResCurrencyRate retrieves the last rates for companies res currency rate handled by this package.
func getLastRatesForCompaniesResCurrencyRate(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	companyIDs := int64Slice(argValue(args, 0))
	result := make(map[int64]float64, len(companyIDs))
	for _, companyID := range companyIDs {
		rate, err := latestCompanyCurrencyRate(ctx, companyID)
		if err != nil {
			return nil, err
		}
		result[companyID] = rate
	}
	return result, nil
}

func onchangeRateWarningResCurrencyRate(ctx context.Context, _ *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	currentRate := floatValueOrDefault(values["rate"], 0)
	if currentRate == 0 && len(ids) == 1 {
		row, err := loadCurrencyRateRow(ctx, ids[0])
		if err != nil {
			return nil, err
		}
		currentRate = row.Rate
	}
	latestRaw, err := getLatestRateResCurrencyRate(ctx, nil, ids, nil, nil)
	if err != nil {
		return nil, err
	}
	latest := floatValueOrDefault(latestRaw, 0)
	if latest == 0 || currentRate == 0 || math.Abs((latest-currentRate)/latest) <= 0.2 {
		return map[string]any{}, nil
	}
	return map[string]any{
		"warning": map[string]any{
			"title":   "Warning for currency rate",
			"message": "The new rate is quite far from the previous rate. Incorrect currency rates may cause critical problems, make sure the rate is correct!",
		},
	}, nil
}

func searchDisplayNameResCurrencyRate(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	operator := strings.TrimSpace(stringArg(args, 0))
	if operator == "" {
		operator = "="
	}
	value := strings.TrimSpace(stringArg(args, 1))
	return []any{[]any{"name", operator, value}}, nil
}

// computeCurrentRateResCurrency computes the current rate res currency handled by this package.
func computeCurrentRateResCurrency(ctx context.Context, records orm.RecordSet, field orm.FieldMetadata) (map[string]any, error) {
	ids := idsToInt64(records.IDs())
	if len(ids) == 0 {
		return map[string]any{}, nil
	}
	contextValues := records.Environment().ContextMap()
	toID := int64Value(currencyContextValue(contextValues, "to_currency"))
	if toID == 0 {
		var err error
		toID, err = currentCompanyCurrencyID(ctx, contextValues)
		if err != nil {
			return nil, err
		}
		if toID == 0 {
			toID = ids[0]
		}
	}
	date := strings.TrimSpace(stringValue(currencyContextValue(contextValues, "date")))
	if date == "" {
		date = time.Now().UTC().Format("2006-01-02")
	}
	rateIDs := append(append([]int64{}, ids...), toID)
	rates, err := latestRatesForCurrencies(ctx, rateIDs, nil, date)
	if err != nil {
		return nil, err
	}
	toRate := rates[toID]
	if toRate == 0 {
		toRate = 1
	}
	names, err := currencyNamesByID(ctx, rateIDs)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(ids))
	for _, id := range ids {
		rate := rates[id] / toRate
		if rate == 0 {
			rate = 1
		}
		key := strconv.FormatInt(id, 10)
		switch field.Name {
		case "inverse_rate":
			result[key] = 1 / rate
		case "rate_string":
			if id == toID {
				result[key] = ""
			} else {
				result[key] = fmt.Sprintf("1 %s = %.6f %s", names[toID], rate, names[id])
			}
		default:
			result[key] = rate
		}
	}
	return result, nil
}

// computeDecimalPlacesResCurrency computes the decimal places res currency handled by this package.
func computeDecimalPlacesResCurrency(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	rows, err := loadCurrencyRows(ctx, records.IDs())
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(rows))
	for _, row := range rows {
		result[strconv.FormatInt(row.ID, 10)] = decimalPlacesForRounding(row.Rounding)
	}
	return result, nil
}

// computeDateResCurrency computes the date res currency handled by this package.
func computeDateResCurrency(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	result := make(map[string]any, len(records.IDs()))
	// Walk each ids() entry in order so validation and side effects stay scoped to the current element.
	for _, id := range records.IDs() {
		var date string
		_ = orm.BunDBFromContext(ctx).NewSelect().
			TableExpr("tbl_currency_rate").
			ColumnExpr("name").
			Where("currency_id = ?", int64Value(id)).
			OrderExpr("name DESC").
			Limit(1).
			Scan(ctx, &date)
		result[id] = date
	}
	return result, nil
}

// computeIsCurrentCompanyCurrencyResCurrency computes the is current company currency res currency handled by this package.
func computeIsCurrentCompanyCurrencyResCurrency(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	companyCurrencyID, err := currentCompanyCurrencyID(ctx, records.Environment().ContextMap())
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(records.IDs()))
	// Walk each ids() entry in order so validation and side effects stay scoped to the current element.
	for _, id := range records.IDs() {
		result[id] = companyCurrencyID != 0 && int64Value(id) == companyCurrencyID
	}
	return result, nil
}

// computeCompanyRateResCurrencyRate computes the company rate res currency rate handled by this package.
func computeCompanyRateResCurrencyRate(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	rows, err := loadCurrencyRateRows(ctx, records.IDs())
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(rows))
	for _, row := range rows {
		baseRate := float64(1)
		if row.CompanyID != nil {
			if value, err := latestCompanyCurrencyRate(ctx, *row.CompanyID); err == nil && value != 0 {
				baseRate = value
			}
		}
		result[strconv.FormatInt(row.ID, 10)] = row.Rate / baseRate
	}
	return result, nil
}

// computeInverseCompanyRateResCurrencyRate computes the inverse company rate res currency rate handled by this package.
func computeInverseCompanyRateResCurrencyRate(ctx context.Context, records orm.RecordSet, field orm.FieldMetadata) (map[string]any, error) {
	companyRates, err := computeCompanyRateResCurrencyRate(ctx, records, field)
	if err != nil {
		return nil, err
	}
	result := make(map[string]any, len(companyRates))
	for id, raw := range companyRates {
		value := floatValueOrDefault(raw, 1)
		if value == 0 {
			value = 1
		}
		result[id] = 1 / value
	}
	return result, nil
}

func applyResCurrencyDefaults(values map[string]any) {
	if _, exists := values["rounding"]; !exists {
		values["rounding"] = float64(0.01)
	}
	if _, exists := values["active"]; !exists {
		values["active"] = true
	}
	if _, exists := values["position"]; !exists {
		values["position"] = "after"
	}
	if _, exists := values["decimal_places"]; !exists {
		values["decimal_places"] = decimalPlacesForRounding(floatValueOrDefault(values["rounding"], 0.01))
	}
}

func applyResCurrencyRateDefaults(values map[string]any) {
	if _, exists := values["name"]; !exists {
		values["name"] = time.Now().UTC().Format("2006-01-02")
	}
	if _, exists := values["rate"]; !exists {
		if companyRate, ok := values["company_rate"]; ok {
			values["rate"] = floatValueOrDefault(companyRate, 1)
		} else if inverse, ok := values["inverse_company_rate"]; ok {
			inverseValue := floatValueOrDefault(inverse, 1)
			if inverseValue == 0 {
				inverseValue = 1
			}
			values["rate"] = 1 / inverseValue
		} else {
			values["rate"] = float64(1)
		}
	}
}

// validateResCurrencyValues validates the res currency values handled by this package.
func validateResCurrencyValues(values map[string]any) error {
	if rounding, ok := values["rounding"]; ok && floatValueOrDefault(rounding, 0) <= 0 {
		log.Errorf("tbl.currency: rounding factor must be greater than 0")
		return orm.InvalidParamsf("tbl.currency: rounding factor must be greater than 0")
	}
	return nil
}

// validateResCurrencyRateValues validates the res currency rate values handled by this package.
func validateResCurrencyRateValues(values map[string]any) error {
	if rate, ok := values["rate"]; ok && floatValueOrDefault(rate, 0) <= 0 {
		log.Errorf("tbl.currency.rate: rate must be strictly positive")
		return orm.InvalidParamsf("tbl.currency.rate: rate must be strictly positive")
	}
	return nil
}

func sanitizeResCurrencyRateVals(values map[string]any) map[string]any {
	if _, hasInverse := values["inverse_company_rate"]; hasInverse {
		if _, hasCompanyRate := values["company_rate"]; hasCompanyRate {
			delete(values, "inverse_company_rate")
		}
		if _, hasRate := values["rate"]; hasRate {
			delete(values, "inverse_company_rate")
		}
	}
	if _, hasCompanyRate := values["company_rate"]; hasCompanyRate {
		if _, hasRate := values["rate"]; hasRate {
			delete(values, "company_rate")
		}
	}
	return values
}

func activeCurrencyCount(ctx context.Context) (int, error) {
	return orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_currency").
		Where("active IS TRUE").
		Count(ctx)
}

// ErrNoRate means no tbl.currency.rate row exists for a currency, so a conversion
// would have to guess. Money conversion fails with it instead of assuming rate 1.
var ErrNoRate = errors.New("currency: no conversion rate")

func init() {
	// Surface a missing rate as a 422 business-rule error rather than a generic 400.
	orm.RegisterErrorClassifier(func(err error) *orm.Error {
		if errors.Is(err, ErrNoRate) {
			return orm.NewError(orm.ErrBusinessRule, 0, err.Error())
		}
		return nil
	})
}

// currencyRateRow is one picked rate per currency.
type currencyRateRow struct {
	CurrencyID int64   `bun:"currency_id"`
	Rate       float64 `bun:"rate"`
}

// pickCurrencyRates returns one rate row per currency in a single query. datedUpTo
// (YYYY-MM-DD) restricts to rows at or before that date and picks the newest;
// empty picks the earliest row on record. Company scoping and the
// "company_id NULLS FIRST" tie order match the per-currency lookup this replaced.
func pickCurrencyRates(ctx context.Context, ids []int64, rootCompanyID *int64, datedUpTo string) (map[int64]float64, error) {
	out := make(map[int64]float64, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	query := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_currency_rate").
		DistinctOn("currency_id").
		ColumnExpr("currency_id, rate").
		Where("currency_id IN (?)", bun.In(ids))
	order := "currency_id, company_id NULLS FIRST, name ASC"
	if datedUpTo != "" {
		query = query.Where("name <= ?", datedUpTo)
		order = "currency_id, company_id NULLS FIRST, name DESC"
	}
	if rootCompanyID != nil {
		query = query.Where("(company_id IS NULL OR company_id = ?)", *rootCompanyID)
	}
	var rows []currencyRateRow
	if err := query.OrderExpr(order).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.CurrencyID] = row.Rate
	}
	return out, nil
}

// lookupCurrencyRates returns, per currency, the newest rate at or before date
// (company-scoped), or the earliest rate on record when none is that old. A
// currency absent from the result has no usable rate row at all; a database
// failure is returned as err and is never mistaken for "no rate". It costs two
// queries for any number of currencies, not two per currency.
func lookupCurrencyRates(ctx context.Context, ids []int64, rootCompanyID *int64, date string) (map[int64]float64, error) {
	dated, err := pickCurrencyRates(ctx, ids, rootCompanyID, date)
	if err != nil {
		return nil, err
	}
	rates := make(map[int64]float64, len(ids))
	var needFallback []int64
	for _, id := range ids {
		if rate, ok := dated[id]; ok && rate > 0 {
			rates[id] = rate
		} else {
			needFallback = append(needFallback, id)
		}
	}
	if len(needFallback) == 0 {
		return rates, nil
	}
	earliest, err := pickCurrencyRates(ctx, needFallback, rootCompanyID, "")
	if err != nil {
		return nil, err
	}
	for _, id := range needFallback {
		if rate, ok := earliest[id]; ok && rate > 0 {
			rates[id] = rate
		}
	}
	return rates, nil
}

// latestRatesForCurrencies returns each currency's rate for display callers, which
// treat "no rate yet" as 1. It does not use that default for money: conversion goes
// through requiredRatesForCurrencies. Real database errors are returned.
func latestRatesForCurrencies(ctx context.Context, ids []int64, companyID *int64, date string) (map[int64]float64, error) {
	rates, _, err := collectCurrencyRates(ctx, ids, companyID, date)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := rates[id]; !ok {
			rates[id] = 1
		}
	}
	return rates, nil
}

// requiredRatesForCurrencies is the strict variant for money conversion: a currency
// with no rate row yields an error wrapping ErrNoRate instead of a silent 1.
func requiredRatesForCurrencies(ctx context.Context, ids []int64, companyID *int64, date string) (map[int64]float64, error) {
	rates, missing, err := collectCurrencyRates(ctx, ids, companyID, date)
	if err != nil {
		return nil, err
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("%w: currency id(s) %v on %s", ErrNoRate, missing, date)
	}
	return rates, nil
}

// collectCurrencyRates looks up every non-zero id; missing lists ids without a rate row.
func collectCurrencyRates(ctx context.Context, ids []int64, companyID *int64, date string) (rates map[int64]float64, missing []int64, err error) {
	rootCompanyID, err := resolvedRootCompanyID(ctx, companyID)
	if err != nil {
		return nil, nil, err
	}
	wanted := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := seen[id]; id == 0 || dup {
			continue
		}
		seen[id] = struct{}{}
		wanted = append(wanted, id)
	}
	rates, err = lookupCurrencyRates(ctx, wanted, rootCompanyID, date)
	if err != nil {
		return nil, nil, err
	}
	for _, id := range wanted {
		if _, ok := rates[id]; !ok {
			missing = append(missing, id)
		}
	}
	return rates, missing, nil
}

// loadCurrencyRows loads the currency rows handled by this package.
func loadCurrencyRows(ctx context.Context, ids []string) ([]resCurrencyRow, error) {
	rows := []resCurrencyRow{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := orm.BunDBFromContext(ctx).NewSelect().
		Model(&rows).
		Where("id IN (?)", bun.In(idsToAny(ids))).
		Scan(ctx)
	return rows, err
}

func singletonCurrencyRow(ctx context.Context, id string) (resCurrencyRow, error) {
	rows, err := loadCurrencyRows(ctx, []string{id})
	if err != nil {
		return resCurrencyRow{}, err
	}
	if len(rows) == 0 {
		log.Errorf("tbl.currency %s not found", id)
		return resCurrencyRow{}, orm.NotFoundf("tbl.currency %s not found", id)
	}
	return rows[0], nil
}

// loadCurrencyRateRows loads the currency rate rows handled by this package.
func loadCurrencyRateRows(ctx context.Context, ids []string) ([]resCurrencyRateRow, error) {
	rows := []resCurrencyRateRow{}
	if len(ids) == 0 {
		return rows, nil
	}
	err := orm.BunDBFromContext(ctx).NewSelect().
		Model(&rows).
		Where("id IN (?)", bun.In(idsToAny(ids))).
		Scan(ctx)
	return rows, err
}

// loadCurrencyRateRow loads the currency rate row handled by this package.
func loadCurrencyRateRow(ctx context.Context, id string) (resCurrencyRateRow, error) {
	rows, err := loadCurrencyRateRows(ctx, []string{id})
	if err != nil {
		return resCurrencyRateRow{}, err
	}
	if len(rows) == 0 {
		log.Errorf("tbl.currency.rate %s not found", id)
		return resCurrencyRateRow{}, orm.NotFoundf("tbl.currency.rate %s not found", id)
	}
	return rows[0], nil
}

func latestRateBefore(ctx context.Context, currencyID int64, companyID *int64, date string) float64 {
	query := orm.BunDBFromContext(ctx).NewSelect().
		ColumnExpr("rate").
		Where("currency_id = ?", currencyID).
		Where("name < ?", date)
	rootCompanyID, err := resolvedRootCompanyID(ctx, companyID)
	if err == nil && rootCompanyID != nil {
		query = query.Where("(company_id IS NULL OR company_id = ?)", *rootCompanyID)
	}
	var rate float64
	if err := query.OrderExpr("name DESC").Limit(1).Scan(ctx, &rate); err != nil || rate <= 0 {
		return 1
	}
	return rate
}

func latestCompanyCurrencyRate(ctx context.Context, companyID int64) (float64, error) {
	rootCompanyID, err := resolvedRootCompanyID(ctx, &companyID)
	if err != nil {
		return 0, err
	}
	currencyID, err := currentCompanyCurrencyID(ctx, map[string]any{"company_id": derefInt64(rootCompanyID)})
	if err != nil || currencyID == 0 {
		return 1, nil
	}
	rates, err := latestRatesForCurrencies(ctx, []int64{currencyID}, rootCompanyID, time.Now().UTC().Format("2006-01-02"))
	if err != nil {
		return 0, err
	}
	return rates[currencyID], nil
}

func currencyNamesByID(ctx context.Context, ids []int64) (map[int64]string, error) {
	rows := []resCurrencyRow{}
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	err := orm.BunDBFromContext(ctx).NewSelect().
		Model(&rows).
		Where("id IN (?)", bun.In(ids)).
		Scan(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]string, len(rows))
	for _, row := range rows {
		result[row.ID] = row.Name
	}
	return result, nil
}

func decimalPlacesForRounding(rounding float64) int64 {
	if rounding > 0 && rounding < 1 {
		return int64(math.Ceil(math.Log10(1 / rounding)))
	}
	return 0
}

// roundToFactor rounds amount to the nearest multiple of factor, half away from
// zero, using exact rational arithmetic over the shortest decimal form of each
// float. Plain float division mis-rounds ties (1.005/0.01 = 100.49999...), which
// is audit-visible on money. Non-finite inputs are returned unchanged.
func roundToFactor(amount, factor float64) float64 {
	if factor <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) || math.IsNaN(factor) || math.IsInf(factor, 0) {
		return amount
	}
	amountRat, okAmount := new(big.Rat).SetString(strconv.FormatFloat(amount, 'f', -1, 64))
	factorRat, okFactor := new(big.Rat).SetString(strconv.FormatFloat(factor, 'f', -1, 64))
	if !okAmount || !okFactor || factorRat.Sign() == 0 {
		return amount
	}
	quotient := new(big.Rat).Quo(amountRat, factorRat)
	// Round half away from zero: add 1/2 toward the sign, then truncate the integer part.
	half := big.NewRat(1, 2)
	if quotient.Sign() < 0 {
		quotient.Sub(quotient, half)
	} else {
		quotient.Add(quotient, half)
	}
	units := new(big.Int).Quo(quotient.Num(), quotient.Denom())
	value, _ := new(big.Rat).Mul(new(big.Rat).SetInt(units), factorRat).Float64()
	if value == 0 {
		return 0
	}
	return value
}

func formatCurrencyAmount(row resCurrencyRow, amount float64) string {
	rounded := roundToFactor(amount, row.Rounding)
	places := int(row.DecimalPlaces)
	if places < 0 {
		places = 0
	}
	text := strconv.FormatFloat(rounded, 'f', places, 64)
	if row.Symbol == "" {
		return text
	}
	if row.Position == "before" {
		return row.Symbol + text
	}
	return text + row.Symbol
}

func currencyValueFromArgs(name string, index int, args []any, kwargs map[string]any) any {
	if kwargs != nil {
		if value, ok := kwargs[name]; ok {
			return value
		}
	}
	return argValue(args, index)
}

func currencyRequiredID(name string, index int, args []any, kwargs map[string]any) int64 {
	return int64Value(currencyValueFromArgs(name, index, args, kwargs))
}

func currencyOptionalID(name string, index int, args []any, kwargs map[string]any) *int64 {
	id := int64Value(currencyValueFromArgs(name, index, args, kwargs))
	if id == 0 {
		return nil
	}
	return &id
}

func currencyDateArg(name string, index int, args []any, kwargs map[string]any) string {
	raw := currencyValueFromArgs(name, index, args, kwargs)
	switch value := raw.(type) {
	case time.Time:
		return value.Format("2006-01-02")
	case string:
		return strings.TrimSpace(value)
	default:
		return ""
	}
}

func int64Slice(raw any) []int64 {
	switch values := raw.(type) {
	case []int64:
		return values
	case []any:
		out := make([]int64, 0, len(values))
		for _, value := range values {
			if id := int64Value(value); id != 0 {
				out = append(out, id)
			}
		}
		return out
	default:
		if id := int64Value(raw); id != 0 {
			return []int64{id}
		}
		return nil
	}
}

func idsToInt64(ids []string) []int64 {
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if value, ok := int64FromAny(id); ok {
			out = append(out, value)
		}
	}
	return out
}

func currencyContextValue(values map[string]any, key string) any {
	if values == nil {
		return nil
	}
	return values[key]
}

// validateCompanyCurrencyActiveResCurrency validates the company currency active res currency handled by this package.
func validateCompanyCurrencyActiveResCurrency(ctx context.Context, records orm.RecordSet, changedValues orm.RecordMap) error {
	// install_mode is honored only from the server-set environment context below, never from
	// write values a client controls (plan T36).
	if boolValue(changedValues["force_deactivate"]) {
		return nil
	}
	// Honor install-mode from the request environment too because bootstrap
	// imports flow through generic runtime write/create paths, not direct field
	// payloads, and should not trip operator-facing deactivation guards.
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		if installMode, ok := env.ContextValue("install_mode"); ok && boolValue(installMode) {
			return nil
		}
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResCurrencyModelName, nil), records.IDs(), []string{"id", "active", "name"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if boolValue(row["active"]) {
			continue
		}
		currencyName := strings.TrimSpace(stringValue(row["name"]))
		if currencyName == "" {
			continue
		}
		used, err := companyUsesCurrencyCode(ctx, currencyName)
		if err != nil {
			return err
		}
		if used {
			log.Errorf("this currency is set on a company and therefore cannot be deactivated")
			return orm.Conflictf("this currency is set on a company and therefore cannot be deactivated")
		}
	}
	return nil
}

// validateMainCompanyResCurrencyRate validates the main company res currency rate handled by this package.
func validateMainCompanyResCurrencyRate(ctx context.Context, records orm.RecordSet, _ orm.RecordMap) error {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResCurrencyRateModelName, nil), records.IDs(), []string{"id", "company_id"})
	if err != nil {
		return err
	}
	for _, row := range rows {
		companyID, _ := many2oneID(row["company_id"])
		if companyID <= 0 {
			continue
		}
		var parentID int64
		if err := orm.BunDBFromContext(ctx).NewSelect().
			TableExpr("tbl_company").
			ColumnExpr("COALESCE(parent_id, 0)").
			Where("id = ?", companyID).
			Limit(1).
			Scan(ctx, &parentID); err != nil {
			return err
		}
		if parentID > 0 {
			log.Errorf("currency rates should only be created for main companies")
			return orm.InvalidParamsf("currency rates should only be created for main companies")
		}
	}
	return nil
}

func currentCompanyCurrencyID(ctx context.Context, contextValues map[string]any) (int64, error) {
	if contextValues == nil {
		return 0, nil
	}
	if currencyID := int64Value(currencyContextValue(contextValues, "company_currency_id")); currencyID != 0 {
		return currencyID, nil
	}
	companyID := int64Value(currencyContextValue(contextValues, "company_id"))
	if companyID == 0 {
		return 0, nil
	}
	rootCompanyID, err := resolvedRootCompanyID(ctx, &companyID)
	if err != nil {
		return 0, err
	}
	var currencyCode string
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_company").
		ColumnExpr("currency_code").
		Where("id = ?", derefInt64(rootCompanyID)).
		Limit(1).
		Scan(ctx, &currencyCode); err != nil {
		return 0, nil
	}
	currencyCode = strings.ToUpper(strings.TrimSpace(currencyCode))
	if currencyCode == "" {
		return 0, nil
	}
	var currencyID int64
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_currency").
		ColumnExpr("id").
		Where("UPPER(name) = ?", currencyCode).
		OrderExpr("id ASC").
		Limit(1).
		Scan(ctx, &currencyID); err != nil {
		return 0, nil
	}
	return currencyID, nil
}

func resolvedRootCompanyID(ctx context.Context, companyID *int64) (*int64, error) {
	if companyID == nil || *companyID == 0 {
		return nil, nil
	}
	current := *companyID
	for current > 0 {
		var parentID int64
		if err := orm.BunDBFromContext(ctx).NewSelect().
			TableExpr("tbl_company").
			ColumnExpr("COALESCE(parent_id, 0)").
			Where("id = ?", current).
			Limit(1).
			Scan(ctx, &parentID); err != nil {
			return nil, err
		}
		if parentID == 0 {
			return &current, nil
		}
		current = parentID
	}
	return nil, nil
}

func companyUsesCurrencyCode(ctx context.Context, currencyCode string) (bool, error) {
	currencyCode = strings.ToUpper(strings.TrimSpace(currencyCode))
	if currencyCode == "" {
		return false, nil
	}
	count, err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_company").
		Where("UPPER(currency_code) = ?", currencyCode).
		Limit(1).
		Count(ctx)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func derefInt64(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
