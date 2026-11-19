package masterdata

// File overview:
// - Responsibility: verifies masterdata import, alignment, and rendered view output for alignment.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down import and render contracts so bootstrap data and generated XML stay stable across refactors.

import (
	"encoding/xml"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
)

// alignmentBaseDatabaseRecord is a minimal stand-in for the modules/database
// base.database model: alignment only needs the model NAME to resolve for
// ui.view/ui.actions.window target checks, and the cross-import rule keeps the
// real module model out of internal tests.
type alignmentBaseDatabaseRecord struct {
	orm.BaseModel
	Name           string   `bun:"name" json:"name"`
	State          string   `bun:"state" json:"state"`
	IsTemplate     bool     `bun:"is_template" json:"is_template"`
	EnabledModules []string `bun:"enabled_modules" json:"enabled_modules" meta:"kind:json"`
	ErrorMsg       string   `bun:"error_msg" json:"error_msg"`
}

var alignmentBaseDatabaseModel = orm.NewHandlerWithModel[alignmentBaseDatabaseRecord](orm.ModelSpec{
	Name:       "base.database",
	Label:      "Databases (alignment stand-in)",
	Definition: orm.ModelDefinition("base.database", (*alignmentBaseDatabaseRecord)(nil)),
})

type alignmentAutomationActionRecord struct {
	orm.BaseModel
	Name        string         `bun:"name" json:"name"`
	ActionType  string         `bun:"action_type" json:"action_type"`
	Description string         `bun:"description" json:"description"`
	Active      bool           `bun:"active" json:"active"`
	Model       string         `bun:"model" json:"model"`
	Operation   string         `bun:"operation" json:"operation"`
	Domain      string         `bun:"domain" json:"domain"`
	Values      map[string]any `bun:"values" json:"values" meta:"kind:json"`
	HandlerKey  string         `bun:"handler_key" json:"handler_key"`
	LuaCode     string         `bun:"lua_code" json:"lua_code"`
}

var alignmentAutomationActionModel = orm.NewHandlerWithModel[alignmentAutomationActionRecord](orm.ModelSpec{
	Name:       "automation.action",
	Label:      "Automation Actions (alignment stand-in)",
	Definition: orm.ModelDefinition("automation.action", (*alignmentAutomationActionRecord)(nil)),
})

type alignmentAutomationCronRecord struct {
	orm.BaseModel
	Name            string `bun:"name" json:"name"`
	ActionID        *int64 `bun:"action_id" json:"action_id" meta:"kind:many2one,model:automation.action"`
	IntervalSeconds int64  `bun:"interval_seconds" json:"interval_seconds"`
	NextCall        string `bun:"next_call" json:"next_call"`
	MaxFailures     int64  `bun:"max_failures" json:"max_failures"`
	FailCount       int64  `bun:"fail_count" json:"fail_count"`
	LastRunStatus   string `bun:"last_run_status" json:"last_run_status"`
	Active          bool   `bun:"active" json:"active"`
}

var alignmentAutomationCronModel = orm.NewHandlerWithModel[alignmentAutomationCronRecord](orm.ModelSpec{
	Name:       "automation.cron",
	Label:      "Automation Crons (alignment stand-in)",
	Definition: orm.ModelDefinition("automation.cron", (*alignmentAutomationCronRecord)(nil)),
})

type alignmentSysCredentialRecord struct {
	orm.BaseModel
	Name                 string `bun:"name" json:"name"`
	CredentialType       string `bun:"credential_type" json:"credential_type"`
	Description          string `bun:"description" json:"description"`
	AuthURL              string `bun:"auth_url" json:"auth_url"`
	UserName             string `bun:"user_name" json:"user_name"`
	Password             string `bun:"password" json:"password"`
	ClientID             string `bun:"client_id" json:"client_id"`
	ClientSecret         string `bun:"client_secret" json:"client_secret"`
	Scope                string `bun:"scope" json:"scope"`
	AccessToken          string `bun:"access_token" json:"access_token"`
	RefreshToken         string `bun:"refresh_token" json:"refresh_token"`
	ExpiredTime          string `bun:"expired_time" json:"expired_time"`
	RefreshBeforeSeconds int64  `bun:"refresh_before_seconds" json:"refresh_before_seconds"`
	LastLoginStatus      string `bun:"last_login_status" json:"last_login_status"`
	LastLoginError       string `bun:"last_login_error" json:"last_login_error"`
	LastLoginAt          string `bun:"last_login_at" json:"last_login_at"`
	Active               bool   `bun:"active" json:"active"`
}

var alignmentSysCredentialModel = orm.NewHandlerWithModel[alignmentSysCredentialRecord](orm.ModelSpec{
	Name:       "sys.credential",
	Label:      "System Credentials (alignment stand-in)",
	Definition: orm.ModelDefinition("sys.credential", (*alignmentSysCredentialRecord)(nil)),
})

// alignmentServiceBusRecord is a minimal stand-in for base.service.bus, which
// now lives in modules/base-bus and is out of reach for internal tests under
// the same cross-import rule as base.database and sys.credential above.
type alignmentServiceBusRecord struct {
	orm.BaseModel
	Channel   string         `bun:"channel" json:"channel"`
	EventType string         `bun:"event_type" json:"event_type"`
	Payload   map[string]any `bun:"payload,type:jsonb,nullzero" json:"payload"`
	CreatedBy int64          `bun:"created_by" json:"created_by"`
}

var alignmentServiceBusModel = orm.NewHandlerWithModel[alignmentServiceBusRecord](orm.ModelSpec{
	Name:       "base.service.bus",
	Label:      "Service Bus (alignment stand-in)",
	Definition: orm.ModelDefinition("base.service.bus", (*alignmentServiceBusRecord)(nil)),
})

// initAlignmentTestModels registers the runtime-backed models that module seed
// files are expected to target during import.
func initAlignmentTestModels() func() {
	shutdowns := make([]func(), 0, 25)

	register := func(initFn func() any, shutdownFn func()) {
		_ = initFn()
		shutdowns = append(shutdowns, shutdownFn)
	}

	register(func() any { return models.SysConfigModel.InitDefault() }, models.SysConfigModel.Shutdown)
	register(func() any { return models.SysModuleCategoryModel.InitDefault() }, models.SysModuleCategoryModel.Shutdown)
	register(func() any { return models.SysModuleModel.InitDefault() }, models.SysModuleModel.Shutdown)
	register(func() any { return models.SysModelModel.InitDefault() }, models.SysModelModel.Shutdown)
	register(func() any { return models.SysModelLogModel.InitDefault() }, models.SysModelLogModel.Shutdown)
	register(func() any { return models.SysModelFieldModel.InitDefault() }, models.SysModelFieldModel.Shutdown)
	register(func() any { return models.SysModelDataModel.InitDefault() }, models.SysModelDataModel.Shutdown)
	register(func() any { return models.SysModelRelationModel.InitDefault() }, models.SysModelRelationModel.Shutdown)
	register(func() any { return models.BankModel.InitDefault() }, models.BankModel.Shutdown)
	register(func() any { return models.ETLActionModel.InitDefault() }, models.ETLActionModel.Shutdown)
	register(func() any { return models.WorkflowActionModel.InitDefault() }, models.WorkflowActionModel.Shutdown)
	register(func() any { return models.FlowModel.InitDefault() }, models.FlowModel.Shutdown)
	register(func() any { return models.OutboundDeliveryModel.InitDefault() }, models.OutboundDeliveryModel.Shutdown)
	register(func() any { return models.WorkflowRunModel.InitDefault() }, models.WorkflowRunModel.Shutdown)
	register(func() any { return models.UIViewModel.InitDefault() }, models.UIViewModel.Shutdown)
	register(func() any { return models.UIViewCustomModel.InitDefault() }, models.UIViewCustomModel.Shutdown)
	register(func() any { return models.UIMenuModel.InitDefault() }, models.UIMenuModel.Shutdown)
	register(func() any { return models.UIActionWindowModel.InitDefault() }, models.UIActionWindowModel.Shutdown)
	register(func() any { return models.UIActionClientModel.InitDefault() }, models.UIActionClientModel.Shutdown)
	register(func() any { return models.ResGroupPrivilegeModel.InitDefault() }, models.ResGroupPrivilegeModel.Shutdown)
	register(func() any { return models.ResGroupModel.InitDefault() }, models.ResGroupModel.Shutdown)
	register(func() any { return models.BaseCompanyModel.InitDefault() }, models.BaseCompanyModel.Shutdown)
	register(func() any { return models.ResLangModel.InitDefault() }, models.ResLangModel.Shutdown)
	register(func() any { return models.ResCountryModel.InitDefault() }, models.ResCountryModel.Shutdown)
	register(func() any { return models.ResCountryGroupModel.InitDefault() }, models.ResCountryGroupModel.Shutdown)
	register(func() any { return models.ResCountryStateModel.InitDefault() }, models.ResCountryStateModel.Shutdown)
	register(func() any { return models.ResCountryWardModel.InitDefault() }, models.ResCountryWardModel.Shutdown)
	register(func() any { return models.ResPartnerModel.InitDefault() }, models.ResPartnerModel.Shutdown)
	register(func() any { return models.ResUserModel.InitDefault() }, models.ResUserModel.Shutdown)
	register(func() any { return models.SysModelAccessModel.InitDefault() }, models.SysModelAccessModel.Shutdown)
	register(func() any { return models.SysRuleModel.InitDefault() }, models.SysRuleModel.Shutdown)
	register(func() any { return alignmentServiceBusModel.InitDefault() }, alignmentServiceBusModel.Shutdown)
	register(func() any { return models.ResCurrencyModel.InitDefault() }, models.ResCurrencyModel.Shutdown)
	register(func() any { return models.ResCurrencyRateModel.InitDefault() }, models.ResCurrencyRateModel.Shutdown)
	register(func() any { return models.SysCronModel.InitDefault() }, models.SysCronModel.Shutdown)
	register(func() any { return models.SysSequenceModel.InitDefault() }, models.SysSequenceModel.Shutdown)
	register(func() any { return models.SysAttachmentModel.InitDefault() }, models.SysAttachmentModel.Shutdown)
	register(func() any { return models.SysReportModel.InitDefault() }, models.SysReportModel.Shutdown)
	register(func() any { return alignmentAutomationActionModel.InitDefault() }, alignmentAutomationActionModel.Shutdown)
	register(func() any { return alignmentAutomationCronModel.InitDefault() }, alignmentAutomationCronModel.Shutdown)
	// base.database lives in modules/database, which internal tests must not
	// import (cross-import rule). Register a minimal stand-in so seed and view
	// references to the model name resolve the same way module bootstrap
	// resolves them at runtime.
	register(func() any { return alignmentBaseDatabaseModel.InitDefault() }, alignmentBaseDatabaseModel.Shutdown)
	register(func() any { return alignmentSysCredentialModel.InitDefault() }, alignmentSysCredentialModel.Shutdown)

	return func() {
		for i := len(shutdowns) - 1; i >= 0; i-- {
			shutdowns[i]()
		}
	}
}

func TestModuleDataRecordsAlignWithRegisteredModels(t *testing.T) {
	shutdown := initAlignmentTestModels()
	defer shutdown()

	records, err := loadImportRecordsFromPath("../../../modules")
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	problems := make([]string, 0)
	for _, record := range records {
		modelName := strings.TrimSpace(record.Model)
		meta, ok := orm.LookupModel(modelName)
		if !ok {
			problems = append(problems, record.SourcePath+":"+strconv.Itoa(record.SourceLine)+": unknown model "+modelName)
			continue
		}
		for key := range record.Data {
			if strings.TrimSpace(key) == "id" {
				continue
			}
			if _, ok := lookupField(meta, key); !ok {
				problems = append(problems, record.SourcePath+":"+strconv.Itoa(record.SourceLine)+": "+modelName+" unknown field "+key)
			}
		}
		switch modelName {
		case models.UIViewModelName:
			targetModel := strings.TrimSpace(stringValue(record.Data["model"]))
			if targetModel != "" {
				targetMeta, ok := orm.LookupModel(targetModel)
				if !ok {
					problems = append(problems, record.SourcePath+":"+strconv.Itoa(record.SourceLine)+": ui.view target model "+targetModel+" is not registered")
					break
				}
				for _, problem := range validateViewArchFieldRefs(targetMeta, stringValue(record.Data["arch"])) {
					problems = append(problems, record.SourcePath+":"+strconv.Itoa(record.SourceLine)+": "+problem)
				}
			}
		case models.UIActionWindowModelName:
			targetModel := strings.TrimSpace(stringValue(record.Data["sys_model"]))
			if targetModel != "" {
				if _, ok := orm.LookupModel(targetModel); !ok {
					problems = append(problems, record.SourcePath+":"+strconv.Itoa(record.SourceLine)+": ui.actions.window target model "+targetModel+" is not registered")
				}
			}
		}
	}

	if len(problems) > 0 {
		t.Fatalf("module data alignment problems:\n%s", strings.Join(problems, "\n"))
	}
}

type alignmentXMLNode struct {
	XMLName  xml.Name
	Attrs    []xml.Attr         `xml:",any,attr"`
	Children []alignmentXMLNode `xml:",any"`
}

func validateViewArchFieldRefs(targetMeta *orm.ModelMetadata, arch string) []string {
	arch = strings.TrimSpace(arch)
	if targetMeta == nil || arch == "" {
		return nil
	}

	var root alignmentXMLNode
	if err := xml.Unmarshal([]byte(arch), &root); err != nil {
		return []string{fmt.Sprintf("ui.view arch XML parse failed for %s: %v", targetMeta.Name, err)}
	}
	return validateViewArchNode(targetMeta, root)
}

func validateViewArchNode(currentMeta *orm.ModelMetadata, node alignmentXMLNode) []string {
	if currentMeta == nil {
		return nil
	}

	problems := make([]string, 0)
	childMeta := currentMeta
	if node.XMLName.Local == "field" {
		fieldName := strings.TrimSpace(xmlAttr(node.Attrs, "name"))
		if fieldName != "" {
			field, ok := lookupField(currentMeta, fieldName)
			if !ok {
				return []string{currentMeta.Name + " unknown view field " + fieldName}
			}
			if relationMeta, ok := resolveViewFieldRelationMeta(field); ok {
				// Nested subviews under relational fields must validate against the related model.
				childMeta = relationMeta
			}
		}
	}

	for _, child := range node.Children {
		problems = append(problems, validateViewArchNode(childMeta, child)...)
	}
	return problems
}

func resolveViewFieldRelationMeta(field orm.FieldMetadata) (*orm.ModelMetadata, bool) {
	switch field.Kind {
	case orm.FieldKindManyToOne, orm.FieldKindOneToMany, orm.FieldKindManyToMany, orm.FieldKindOneToOne:
	default:
		return nil, false
	}

	relationModel := strings.TrimSpace(field.Relation)
	if relationModel == "" {
		return nil, false
	}
	relationMeta, ok := orm.LookupModel(relationModel)
	return relationMeta, ok
}

func xmlAttr(attrs []xml.Attr, name string) string {
	for _, attr := range attrs {
		if strings.EqualFold(strings.TrimSpace(attr.Name.Local), name) {
			return attr.Value
		}
	}
	return ""
}

func TestModuleDataSeedsAdminRoleAndMenuAccess(t *testing.T) {
	records, err := loadImportRecordsFromPath("../../../modules")
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	hasSystemGroup := false
	adminAssignedSystemGroup := false
	hasSystemMenuAccess := false

	for _, record := range records {
		switch {
		case record.Model == models.ResGroupModelName && strings.TrimSpace(record.ExternalID) == "base.group_system":
			hasSystemGroup = true
		case record.Model == models.ResUserModelName && strings.TrimSpace(record.ExternalID) == "user.admin":
			adminAssignedSystemGroup = strings.TrimSpace(stringValue(record.Data["group_id"])) == "base.group_system"
		case record.Model == models.SysModelAccessModelName:
			groupID := strings.TrimSpace(stringValue(record.Data["group_id"]))
			modelName := strings.TrimSpace(stringValue(record.Data["model_name"]))
			if groupID == "base.group_system" && modelName == models.UIMenuModelName {
				hasSystemMenuAccess = boolValue(record.Data["perm_read"])
			}
		}
	}

	if !hasSystemGroup {
		t.Fatalf("expected modules to seed base.group_system")
	}
	if !adminAssignedSystemGroup {
		t.Fatalf("expected user.admin to be assigned to base.group_system")
	}
	if !hasSystemMenuAccess {
		t.Fatalf("expected modules to seed readable sys.model.access for base.group_system on ui.menu")
	}
}

func TestModuleInitDataSeedsDeterministicMainCompanyScope(t *testing.T) {
	records, err := loadImportRecordsFromPath("../../../modules")
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	hasMainCompany := false
	mainCompanyPartnerLinked := false
	scopedUsers := map[string]bool{
		"user.admin":    false,
		"user.template": false,
		"user.base_bot": false,
	}

	for _, record := range records {
		switch {
		case record.Model == models.ResCompanyModelName && strings.TrimSpace(record.ExternalID) == "company.main":
			hasMainCompany = true
			mainCompanyPartnerLinked = strings.TrimSpace(stringValue(record.Data["partner_id"])) == "partner.main"
		case record.Model == models.ResUserModelName:
			expected, ok := scopedUsers[strings.TrimSpace(record.ExternalID)]
			if !ok || expected {
				continue
			}
			companyID := strings.TrimSpace(stringValue(record.Data["company_id"]))
			companyIDs := stringListValue(record.Data["company_ids"])
			scopedUsers[record.ExternalID] = companyID == "company.main" && len(companyIDs) == 1 && companyIDs[0] == "company.main"
		}
	}

	if !hasMainCompany {
		t.Fatalf("expected init data to seed company.main")
	}
	if !mainCompanyPartnerLinked {
		t.Fatalf("expected company.main to link to partner.main")
	}
	for externalID, ok := range scopedUsers {
		if !ok {
			t.Fatalf("expected %s to seed explicit company_id/company_ids for company.main", externalID)
		}
	}
}

func TestModuleInitDataSeedsSampleCompaniesAndUsers(t *testing.T) {
	records, err := loadImportRecordsFromPath("../../../modules")
	if err != nil {
		t.Fatalf("load modules: %v", err)
	}

	type sampleCompany struct {
		partnerExternalID string
		userExternalID    string
		userPartnerID     string
	}
	samples := map[string]sampleCompany{
		"company.base1": {
			partnerExternalID: "partner.base1_company",
			userExternalID:    "user.base1",
			userPartnerID:     "partner.base1_user",
		},
	}
	seenPartners := map[string]bool{}
	seenUserPartners := map[string]bool{}
	seenCompanies := map[string]bool{}
	seenUsers := map[string]bool{}

	for _, record := range records {
		externalID := strings.TrimSpace(record.ExternalID)
		switch record.Model {
		case models.ResPartnerModelName:
			if _, ok := seenPartners[externalID]; ok {
				seenPartners[externalID] = true
			}
			if _, ok := seenUserPartners[externalID]; ok {
				seenUserPartners[externalID] = true
			}
			for _, sample := range samples {
				if externalID == sample.partnerExternalID {
					seenPartners[externalID] = true
				}
				if externalID == sample.userPartnerID {
					seenUserPartners[externalID] = true
				}
			}
		case models.ResCompanyModelName:
			sample, ok := samples[externalID]
			if !ok {
				continue
			}
			seenCompanies[externalID] = strings.TrimSpace(stringValue(record.Data["partner_id"])) == sample.partnerExternalID
		case models.ResUserModelName:
			for companyExternalID, sample := range samples {
				if externalID != sample.userExternalID {
					continue
				}
				companyID := strings.TrimSpace(stringValue(record.Data["company_id"]))
				companyIDs := stringListValue(record.Data["company_ids"])
				partnerID := strings.TrimSpace(stringValue(record.Data["partner_id"]))
				seenUsers[externalID] = companyID == companyExternalID &&
					len(companyIDs) == 1 &&
					companyIDs[0] == companyExternalID &&
					partnerID == sample.userPartnerID
			}
		}
	}

	for companyExternalID, sample := range samples {
		if !seenPartners[sample.partnerExternalID] {
			t.Fatalf("expected init data to seed %s", sample.partnerExternalID)
		}
		if !seenUserPartners[sample.userPartnerID] {
			t.Fatalf("expected init data to seed %s", sample.userPartnerID)
		}
		if !seenCompanies[companyExternalID] {
			t.Fatalf("expected %s to link to %s", companyExternalID, sample.partnerExternalID)
		}
		if !seenUsers[sample.userExternalID] {
			t.Fatalf("expected %s to seed a user scoped only to %s", sample.userExternalID, companyExternalID)
		}
	}
}

func stringListValue(value any) []string {
	typed, ok := value.([]any)
	if !ok {
		return nil
	}

	out := make([]string, 0, len(typed))
	for _, item := range typed {
		text := strings.TrimSpace(stringValue(item))
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}
