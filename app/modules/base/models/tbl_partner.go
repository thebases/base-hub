package models

// File overview:
// - Responsibility: defines the `base` module model contract for res partner, including field metadata, runtime methods, and module-specific invariants.
// - Key symbols: `ResPartnerRecord`, `createResPartner`, `writeResPartner`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"net/mail"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// ResPartnerRecord keeps the legacy customer table exposed through one richer
// partner contract aligned with the tbl.partner reference fields and
// lightweight helper methods.
type ResPartnerRecord struct {
	orm.BaseModel
	Name         string  `bun:"name" json:"name" meta:"index,label:Name,help:Stores the human readable name for this record."`
	CompleteName string  `bun:"complete_name" json:"complete_name" meta:"index,label:Complete Name,help:Stores the complete name value for this record."`
	IsCompany    bool    `bun:"is_company" default:"false" json:"is_company" meta:"label:Is Company,help:Controls whether is company is enabled for this record."`
	CompanyType  string  `bun:"company_type" json:"company_type" meta:"index,label:Company Type,help:Stores the company type value for this record."`
	Type         string  `bun:"type" json:"type" meta:"index,label:Type,help:Stores the type value for this record."`
	ParentID     *int64  `bun:"parent_id" json:"parent_id" meta:"kind:many2one,model:tbl.partner,ondelete:SET NULL,index,label:Parent ID,help:Links this record to the related tbl.partner record for parent id."`
	ParentName   string  `bun:"parent_name" json:"parent_name" meta:"label:Parent Name,help:Stores the parent name value for this record."`
	ChildIDs     []int64 `bun:"child_ids,type:jsonb,nullzero" json:"child_ids" meta:"kind:one2many,model:tbl.partner,inverse:parent_id,stored:false,label:Child IDs,help:Lists the related tbl.partner records linked back through parent_id."`
	CompanyID    *int64  `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Links this record to the related tbl.company record for company id."`
	UserIDs      []int64 `bun:"user_ids,type:jsonb,nullzero" json:"user_ids" meta:"kind:one2many,model:base.users,inverse:partner_id,stored:false,label:User IDs,help:Lists the related base.users records linked back through partner_id."`
	// CategoryIDs      []int64 `bun:"category_ids,type:jsonb,nullzero" json:"category_ids" meta:"kind:many2many,model:tbl.partner.category,label:Category IDs,help:Lists the related tbl.partner.category records associated with this record."`
	Ref             string `bun:"ref" json:"ref" meta:"index,label:Ref,help:Stores the ref value for this record."`
	Lang            string `bun:"lang" json:"lang" meta:"index,label:Lang,help:Stores the lang value for this record."`
	TZ              string `bun:"tz" json:"tz" meta:"index,label:TZ,help:Stores the tz value for this record."`
	UserID          *int64 `bun:"user_id" json:"user_id" meta:"kind:many2one,model:base.users,ondelete:SET NULL,index,label:User ID,help:Links this record to the related base.users record for user id."`
	Email           string `bun:"email" json:"email" meta:"index,label:Email,help:Stores the email value for this record."`
	Phone           string `bun:"phone" json:"phone" meta:"index,label:Phone,help:Stores the phone value for this record."`
	Mobile          string `bun:"mobile" json:"mobile" meta:"index,label:Mobile,help:Stores the mobile value for this record."`
	VAT             string `bun:"vat" json:"vat" meta:"index,label:VAT,help:Stores the vat value for this record."`
	CompanyRegistry string `bun:"company_registry" json:"company_registry" meta:"index,label:Company Registry,help:Stores the company registry value for this record."`
	Function        string `bun:"function" json:"function" meta:"label:Function,help:Stores the function value for this record."`
	Website         string `bun:"website" json:"website" meta:"label:Website,help:Stores the website value for this record."`
	Street          string `bun:"street" json:"street" meta:"label:Street,help:Stores the street value for this record."`
	Street2         string `bun:"street2" json:"street2" meta:"label:Street 2,help:Stores the street 2 value for this record."`
	CountryID       *int64 `bun:"country_id" json:"country_id" meta:"kind:many2one,model:tbl.country,ondelete:SET NULL,index,label:Country ID,help:Links this record to the related tbl.country record for country id."`
	StateID         *int64 `bun:"state_id" json:"state_id" meta:"kind:many2one,model:tbl.country.state,ondelete:SET NULL,index,label:State ID,help:Links this record to the related tbl.country.state record for state id."`
	WardID          *int64 `bun:"ward_id" json:"ward_id" meta:"kind:many2one,model:tbl.country.ward,ondelete:SET NULL,index,label:Ward ID,help:Links this record to the related tbl.country.ward record for ward id."`
	ZIP             string `bun:"zip" json:"zip" meta:"index,label:ZIP,help:Stores the zip value for this record."`
	City            string `bun:"city" json:"city" meta:"index,label:City,help:Stores the city value for this record."`
	Comment         string `bun:"comment" json:"comment" meta:"label:Comment,help:Stores the comment value for this record."`
	Image1920       string `bun:"image_1920" json:"image_1920" meta:"label:Image 1920,help:Stores the image 1920 value for this record."`
	Description     string `bun:"description" json:"description" meta:"label:Description,help:Stores the descriptive notes for this record."`
	FilesDir        string `bun:"filesdir" json:"filesdir" meta:"label:Files Dir,help:Stores the files dir value for this record."`
	// Master           bool    `bun:"master" json:"master" meta:"label:Master,help:Controls whether master is enabled for this record."`
	Prefix           string  `bun:"prefix" json:"prefix" meta:"index,label:Prefix,help:Stores the prefix value for this record."`
	RegistrationTime *int64  `bun:"registrationtime" json:"registrationtime" meta:"label:Registration Time,help:Stores the numeric value for registration time."`
	LastLoginTime    *int64  `bun:"lastlogintime" json:"lastlogintime" meta:"label:Last Login Time,help:Stores the numeric value for last login time."`
	AccountType      int64   `bun:"accounttype" json:"accounttype" meta:"label:Account Type,help:Stores the numeric value for account type."`
	ExpiryTime       *int64  `bun:"expirytime" json:"expirytime" meta:"label:Expiry Time,help:Stores the numeric value for expiry time."`
	DeviceLimit      int64   `bun:"devicelimit" json:"devicelimit" meta:"label:Device Limit,help:Stores the numeric value for device limit."`
	CustomerStatus   string  `bun:"customerstatus" json:"customerstatus" meta:"index,label:Customer Status,help:Stores the customer status value for this record."`
	FirstName        string  `bun:"firstname" json:"firstname" meta:"label:First Name,help:Stores the first name value for this record."`
	LastName         string  `bun:"lastname" json:"lastname" meta:"label:Last Name,help:Stores the last name value for this record."`
	Language         string  `bun:"language" json:"language" meta:"index,label:Language,help:Stores the language value for this record."`
	InactiveState    int64   `bun:"inactivestate" json:"inactivestate" meta:"label:Inactive State,help:Stores the numeric value for inactive state."`
	PauseState       int64   `bun:"pausestate" json:"pausestate" meta:"label:Pause State,help:Stores the numeric value for pause state."`
	AbandonState     int64   `bun:"abandonstate" json:"abandonstate" meta:"label:Abandon State,help:Stores the numeric value for abandon state."`
	SizeLimit        int64   `bun:"sizelimit" json:"sizelimit" meta:"label:Size Limit,help:Stores the numeric value for size limit."`
	SignupStatus     string  `bun:"signupstatus" json:"signupstatus" meta:"index,label:Signup Status,help:Stores the signup status value for this record."`
	SignupToken      string  `bun:"signuptoken" json:"signuptoken" meta:"label:Signup Token,help:Stores the signup token value for this record."`
	BankAccountIDs   []int64 `bun:"bank_account_ids,type:jsonb,nullzero" json:"bank_account_ids" meta:"kind:one2many,model:tbl.partner.bank,inverse:partner_id,stored:false,label:Bank Account IDs,help:Lists the related tbl.partner.bank records linked back through partner_id."`
}

var ResPartnerModelName = "tbl.partner"

var ResPartnerDefinition = orm.ModelDefinition(ResPartnerModelName, (*ResPartnerRecord)(nil))

var ResPartnerModelSpec = orm.ModelSpec{
	Name:        ResPartnerModelName,
	Label:       "Partners",
	Description: "Compatibility partner model backed by the legacy customers table",
	Definition:  ResPartnerDefinition,
	StandardHooks: orm.StandardRuntimeHooks{
		DefaultGet:    defaultGetResPartner,
		Create:        createResPartner,
		Write:         writeResPartner,
		NameCreate:    nameCreateResPartner,
		CopyData:      copyDataResPartner,
		ViewHeaderGet: viewHeaderGetResPartner,
	},
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "onchange_parent_id", Public: true, ReadOnly: true, Call: onchangeParentIDResPartner},
		{Name: "onchange_company_type", Public: true, ReadOnly: true, Call: onchangeCompanyTypeResPartner},
		{Name: "find_or_create", Public: true, ModelOnly: true, Call: findOrCreateResPartner},
		{Name: "address_get", Public: true, ReadOnly: true, Call: addressGetResPartner},
		{Name: "get_import_templates", Public: true, ModelOnly: true, ReadOnly: true, Call: getImportTemplatesResPartner},
	},
	// Physical storage uses the tbl_* prefix while the logical model name stays
	// dotted (tbl.partner); the legacy tbl_partner table is renamed in place
	// during migration so partner rows survive.
}

var ResPartnerModel = orm.Define[ResPartnerRecord](&Models, 270, ResPartnerModelSpec)

var partnerAddressFields = []string{"street", "street2", "zip", "city"}

// createResPartner creates the res partner handled by this package.
func createResPartner(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	valuesList, err := normalizePartnerCreatePayload(firstPresent(argAt(args, 0), kwargs["values"]))
	if err != nil {
		return nil, err
	}
	created := make([]orm.RecordMap, 0, len(valuesList))
	for _, values := range valuesList {
		working := cloneRecordMap(values)
		applyResPartnerDefaults(working)
		if name := strings.TrimSpace(stringValue(working["name"])); name != "" {
			working["complete_name"] = partnerCompleteName(ctx, model, name, working["parent_id"])
		}
		// Guarded create: like the default create it pins company_id to the caller's active company
		// and checks it, so an RPC partner is no longer stored shared (company_id NULL) by default.
		record, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), working)
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

func writeResPartner(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		log.Errorf("tbl.partner.write requires values")
		return nil, orm.InvalidParamsf("tbl.partner.write requires values")
	}
	working := cloneRecordMap(values)
	applyResPartnerDefaults(working)
	if _, changedName := working["name"]; changedName {
		working["complete_name"] = partnerCompleteName(ctx, model, strings.TrimSpace(stringValue(working["name"])), working["parent_id"])
	}
	result, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), ids, working)
	if err != nil {
		return nil, err
	}
	// Mirror same-named field changes onto companies linked through partner_id; no-op when the write came from the company side.
	if err := syncPartnerValuesToCompanies(ctx, ids, pickCompanyPartnerSharedValues(working)); err != nil {
		return nil, err
	}
	// Mirror a rename onto users linked through partner_id (user.name duplicates partner.name).
	if err := syncPartnerNameToUsers(ctx, ids, working); err != nil {
		return nil, err
	}
	return result, nil
}

// copyDataResPartner copies the data res partner handled by this package.
func copyDataResPartner(ctx context.Context, model *orm.ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), ids, nil)
	if err != nil {
		return nil, err
	}
	defaults, err := recordMapFromArgs(args, kwargs, "default", "defaults")
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, row := range rows {
		cloned := cloneRecordMap(row)
		delete(cloned, "id")
		delete(cloned, "created_at")
		delete(cloned, "updated_at")
		for key, value := range defaults {
			cloned[key] = value
		}
		if _, exists := defaults["name"]; !exists {
			cloned["name"] = strings.TrimSpace(stringValue(row["name"])) + " (copy)"
		}
		out = append(out, cloned)
	}
	return out, nil
}

// defaultGetResPartner selects the get res partner handled by this package.
func defaultGetResPartner(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	fields := stringSlice(firstPresent(argAt(args, 0), kwargs["fields"], kwargs["field_names"]))
	out := map[string]any{}
	if containsString(fields, "active") {
		out["active"] = true
	}
	if containsString(fields, "company_type") {
		out["company_type"] = "person"
	}
	if containsString(fields, "type") {
		out["type"] = "contact"
	}
	if containsString(fields, "is_company") {
		out["is_company"] = false
	}
	return out, nil
}

func onchangeParentIDResPartner(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	parentID, ok := int64FromAny(values["parent_id"])
	if !ok || parentID <= 0 {
		return map[string]any{"value": map[string]any{}}, nil
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), []string{fmt.Sprintf("%d", parentID)}, append([]string{"name"}, partnerAddressFields...))
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]any{"value": map[string]any{}}, nil
	}
	out := map[string]any{"parent_name": strings.TrimSpace(stringValue(rows[0]["name"]))}
	for _, field := range partnerAddressFields {
		out[field] = rows[0][field]
	}
	return map[string]any{"value": out}, nil
}

func onchangeCompanyTypeResPartner(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	companyType := strings.TrimSpace(stringValue(values["company_type"]))
	return map[string]any{"value": map[string]any{"is_company": companyType == "company"}}, nil
}

func nameCreateResPartner(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	display := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	if display == "" {
		log.Errorf("tbl.partner.name_create requires name")
		return nil, orm.InvalidParamsf("tbl.partner.name_create requires name")
	}
	name, email := parsePartnerContact(display)
	values := orm.RecordMap{"name": firstNonEmpty(name, display)}
	if email != "" {
		values["email"] = email
	}
	created, err := createResPartner(ctx, model, nil, []any{values}, nil)
	if err != nil {
		return nil, err
	}
	record, ok := created.(orm.RecordMap)
	if !ok {
		return created, nil
	}
	return [2]any{int64Value(record["id"]), stringValue(record["name"])}, nil
}

// findOrCreateResPartner finds the or create res partner handled by this package.
func findOrCreateResPartner(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	display := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	name, email := parsePartnerContact(display)
	filter := orm.RecordMap{}
	if email != "" {
		filter["email"] = email
	} else if name != "" {
		filter["name"] = name
	}
	if len(filter) > 0 {
		rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), filter, []string{"id", "name"})
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			return int64Value(rows[0]["id"]), nil
		}
	}
	created, err := createResPartner(ctx, model, nil, []any{orm.RecordMap{"name": firstNonEmpty(name, display), "email": email}}, nil)
	if err != nil {
		return nil, err
	}
	record, ok := created.(orm.RecordMap)
	if !ok {
		return created, nil
	}
	return int64Value(record["id"]), nil
}

func addressGetResPartner(ctx context.Context, model *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), ids, append([]string{"id", "parent_id", "type"}, partnerAddressFields...))
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, row := range rows {
		rowID := int64Value(row["id"])
		rowType := strings.TrimSpace(stringValue(row["type"]))
		if rowType == "" {
			rowType = "contact"
		}
		out[rowType] = rowID
		if _, exists := out["contact"]; !exists {
			out["contact"] = rowID
		}
	}
	return out, nil
}

func viewHeaderGetResPartner(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	if name == "" {
		name = "Partner"
	}
	return name, nil
}

// getImportTemplatesResPartner retrieves the import templates res partner handled by this package.
func getImportTemplatesResPartner(_ context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return []map[string]any{
		{"label": "Customers", "template": "/base/static/xls/tbl_partner.xlsx"},
	}, nil
}

func applyResPartnerDefaults(values map[string]any) {
	if _, exists := values["active"]; !exists {
		values["active"] = true
	}
	if _, exists := values["type"]; !exists {
		values["type"] = "contact"
	}
	if _, exists := values["company_type"]; !exists {
		values["company_type"] = "person"
	}
	if _, exists := values["is_company"]; !exists {
		values["is_company"] = strings.TrimSpace(stringValue(values["company_type"])) == "company"
	}
	if website := cleanPartnerWebsite(strings.TrimSpace(stringValue(values["website"]))); website != "" {
		values["website"] = website
	}
}

func normalizePartnerCreatePayload(raw any) ([]orm.RecordMap, error) {
	if raw == nil {
		log.Errorf("tbl.partner.create requires values")
		return nil, orm.InvalidParamsf("tbl.partner.create requires values")
	}
	if record, err := ormAnyToRecordMap(raw); err == nil && record != nil {
		return []orm.RecordMap{record}, nil
	}
	switch typed := raw.(type) {
	case []any:
		out := make([]orm.RecordMap, 0, len(typed))
		for _, item := range typed {
			record, err := ormAnyToRecordMap(item)
			if err != nil {
				return nil, err
			}
			out = append(out, record)
		}
		return out, nil
	default:
		log.Errorf("tbl.partner.create values must be an object or list of objects")
		return nil, orm.InvalidParamsf("tbl.partner.create values must be an object or list of objects")
	}
}

// cleanPartnerWebsite normalizes the partner website handled by this package.
func cleanPartnerWebsite(input string) string {
	input = strings.TrimSpace(input)
	if input == "" {
		return ""
	}
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		return input
	}
	return "http://" + input
}

// parsePartnerContact parses the partner contact handled by this package.
func parsePartnerContact(display string) (string, string) {
	display = strings.TrimSpace(display)
	if display == "" {
		return "", ""
	}
	address, err := mail.ParseAddress(display)
	if err == nil {
		return strings.TrimSpace(address.Name), strings.TrimSpace(address.Address)
	}
	if strings.Contains(display, "@") && !strings.Contains(display, " ") {
		return "", display
	}
	return display, ""
}

func partnerCompleteName(ctx context.Context, model *orm.ModelMetadata, name string, parentRaw any) string {
	name = strings.TrimSpace(name)
	parentID, ok := int64FromAny(parentRaw)
	if !ok || parentID <= 0 {
		return name
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(ResPartnerModelName, model), []string{fmt.Sprintf("%d", parentID)}, []string{"complete_name", "name"})
	if err != nil || len(rows) == 0 {
		return name
	}
	parentName := firstNonEmpty(strings.TrimSpace(stringValue(rows[0]["complete_name"])), strings.TrimSpace(stringValue(rows[0]["name"])))
	if parentName == "" {
		return name
	}
	return parentName + ", " + name
}

func ormAnyToRecordMap(value any) (orm.RecordMap, error) {
	switch typed := value.(type) {
	case orm.RecordMap:
		return cloneRecordMap(typed), nil
	case map[string]any:
		return cloneRecordMap(orm.RecordMap(typed)), nil
	default:
		log.Errorf("record payload must be an object")
		return nil, orm.InvalidParamsf("record payload must be an object")
	}
}

func firstPresent(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func containsString(items []string, want string) bool {
	for _, item := range items {
		if strings.TrimSpace(item) == want {
			return true
		}
	}
	return false
}

func (r ResPartnerRecord) PrimaryUserID() *int64 {
	if len(r.UserIDs) != 1 {
		return nil
	}
	userID := r.UserIDs[0]
	return &userID
}
