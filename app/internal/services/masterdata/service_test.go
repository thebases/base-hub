package masterdata

// File overview:
// - Responsibility: verifies masterdata import, alignment, and rendered view output for service.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down import and render contracts so bootstrap data and generated XML stay stable across refactors.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	runtest "git.thebase.vn/base_hub/internal/test/runtime"
	"golang.org/x/crypto/bcrypt"
)

type fakeRuntime struct {
	created []runtimeCall
	writes  []runtimeCall
	bids    map[string]bidTarget
	exists  map[string]map[string]bool
	rows    map[string]map[string]orm.RecordMap
	nextID  int64
}

type runtimeCall struct {
	Model       string
	IDs         []string
	Values      orm.RecordMap
	InstallMode bool
}

type masterdataInstallModeDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

type masterdataInstallModeWriteDocument struct {
	orm.BaseModel
	Name string `bun:"name" json:"name"`
}

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{
		bids:   make(map[string]bidTarget),
		exists: make(map[string]map[string]bool),
		rows:   make(map[string]map[string]orm.RecordMap),
		nextID: 100,
	}
}

func (f *fakeRuntime) LookupModel(name string) (*orm.ModelMetadata, bool) {
	return orm.LookupModel(name)
}

func (f *fakeRuntime) Create(ctx context.Context, model string, values orm.RecordMap) (orm.RecordMap, error) {
	f.created = append(f.created, runtimeCall{
		Model:       model,
		Values:      cloneRecordMap(values),
		InstallMode: contextInstallMode(ctx),
	})
	if model == "sys.model.data" {
		f.nextID++
		target := bidTarget{
			RowID:    toStringID(f.nextID),
			Model:    toString(values["model"]),
			ModelId:  toString(values["model_id"]),
			NoUpdate: boolValue(values["noupdate"]),
		}
		f.bids[toString(values["complete_name"])] = target
		return orm.RecordMap{"id": f.nextID}, nil
	}
	f.nextID++
	f.markExists(model, toStringID(f.nextID))
	f.setRow(model, toStringID(f.nextID), orm.RecordMap{
		"id": f.nextID,
	})
	return orm.RecordMap{"id": f.nextID}, nil
}

func (f *fakeRuntime) Write(ctx context.Context, model string, ids []string, values orm.RecordMap) (bool, error) {
	f.writes = append(f.writes, runtimeCall{
		Model:       model,
		IDs:         append([]string(nil), ids...),
		Values:      cloneRecordMap(values),
		InstallMode: contextInstallMode(ctx),
	})
	if model == "sys.model.data" && len(ids) > 0 {
		f.bids[toString(values["complete_name"])] = bidTarget{
			RowID:    ids[0],
			Model:    toString(values["model"]),
			ModelId:  toString(values["model_id"]),
			NoUpdate: boolValue(values["noupdate"]),
		}
	}
	if len(ids) > 0 {
		row := cloneRecordMap(values)
		row["id"] = ids[0]
		f.setRow(model, ids[0], row)
	}
	return true, nil
}

func (f *fakeRuntime) SearchRead(_ context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
	_ = fields
	if model != "sys.model.data" {
		if id := toString(filter["id"]); id != "" {
			if row, ok := f.rows[model][id]; ok {
				return []orm.RecordMap{cloneRecordMap(row)}, nil
			}
			if f.exists[model][id] {
				return []orm.RecordMap{{"id": id}}, nil
			}
			return nil, nil
		}
		for _, row := range f.rows[model] {
			if fakeRowMatchesFilter(row, filter) {
				return []orm.RecordMap{cloneRecordMap(row)}, nil
			}
		}
		return nil, nil
	}
	completeName := toString(filter["complete_name"])
	if target, ok := f.bids[completeName]; ok {
		return []orm.RecordMap{{
			"id":        target.RowID,
			"model":     target.Model,
			"model_id":  target.ModelId,
			"noupdate":  target.NoUpdate,
			"reference": target.Reference,
		}}, nil
	}
	return nil, nil
}

func (f *fakeRuntime) Unlink(ctx context.Context, model string, ids []string) (bool, error) {
	f.writes = append(f.writes, runtimeCall{
		Model:       model,
		IDs:         append([]string(nil), ids...),
		InstallMode: contextInstallMode(ctx),
	})
	for _, id := range ids {
		delete(f.rows[model], id)
		delete(f.exists[model], id)
	}
	return true, nil
}

func fakeRowMatchesFilter(row orm.RecordMap, filter orm.RecordMap) bool {
	for key, want := range filter {
		if !strings.EqualFold(toString(row[key]), toString(want)) {
			return false
		}
	}
	return true
}

func contextInstallMode(ctx context.Context) bool {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return false
	}
	value, ok := env.ContextValue("install_mode")
	return ok && boolValue(value)
}

func (f *fakeRuntime) markExists(model, id string) {
	if f.exists[model] == nil {
		f.exists[model] = make(map[string]bool)
	}
	f.exists[model][id] = true
}

func (f *fakeRuntime) setRow(model, id string, row orm.RecordMap) {
	if f.rows[model] == nil {
		f.rows[model] = make(map[string]orm.RecordMap)
	}
	f.rows[model][id] = cloneRecordMap(row)
	f.markExists(model, id)
}

func TestLoadCSVImportRecordsInfersModelAndParsesArrays(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "24_sys_model_access.csv")
	content := "id,name,group_id,model_name,perm_read,perm_write,perm_create,perm_unlink\n" +
		"base.access_users_admin,Users admin,base.group_system,base.users,true,true,true,true\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	records, err := loadCSVImportRecords(path)
	if err != nil {
		t.Fatalf("loadCSVImportRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}
	if records[0].Model != "sys.model.access" {
		t.Fatalf("model = %q, want sys.model.access", records[0].Model)
	}
	if records[0].ExternalID != "base.access_users_admin" {
		t.Fatalf("external id = %q", records[0].ExternalID)
	}
	if !boolValue(records[0].Data["perm_create"]) {
		t.Fatalf("perm_create = %#v, want true", records[0].Data["perm_create"])
	}
}

func TestImportRecordMarksCreateAsInstallMode(t *testing.T) {
	const modelName = "test.masterdata_install_mode_create"
	runtest.RegisterModelType[masterdataInstallModeDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Masterdata Install Mode Create",
		Definition: orm.ModelDefinition(modelName, (*masterdataInstallModeDocument)(nil)),
	}.WithStorageName("test_masterdata_install_mode_create"))

	runtime := newFakeRuntime()
	service := newServiceWithRuntime(runtime)
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 0, nil, true))

	status, err := service.importRecord(ctx, ImportRecord{
		Action:     "c",
		Model:      modelName,
		ExternalID: "base.install_mode_create",
		Data: orm.RecordMap{
			"name": "Created in seed",
		},
		SourcePath: "test.json",
		SourceLine: 1,
	})
	if err != nil {
		t.Fatalf("importRecord() error = %v", err)
	}
	if status != "created" {
		t.Fatalf("importRecord() status = %q, want created", status)
	}
	if len(runtime.created) == 0 {
		t.Fatalf("expected at least one create call")
	}
	if runtime.created[0].Model != modelName {
		t.Fatalf("first create model = %q, want %q", runtime.created[0].Model, modelName)
	}
	if !runtime.created[0].InstallMode {
		t.Fatalf("expected target create to run with install_mode context")
	}
}

func TestImportRecordMarksWriteAsInstallMode(t *testing.T) {
	const modelName = "test.masterdata_install_mode_write"
	runtest.RegisterModelType[masterdataInstallModeWriteDocument](t, orm.ModelSpec{
		Name:       modelName,
		Label:      "Masterdata Install Mode Write",
		Definition: orm.ModelDefinition(modelName, (*masterdataInstallModeWriteDocument)(nil)),
	}.WithStorageName("test_masterdata_install_mode_write"))

	runtime := newFakeRuntime()
	runtime.bids["base.install_mode_write"] = bidTarget{
		RowID:   "201",
		Model:   modelName,
		ModelId: "301",
	}
	runtime.setRow(modelName, "301", orm.RecordMap{
		"id":   "301",
		"name": "Before",
	})
	service := newServiceWithRuntime(runtime)
	ctx := orm.ContextWithEnvironment(context.Background(), orm.NewEnvironment(nil, 0, nil, true))

	status, err := service.importRecord(ctx, ImportRecord{
		Action:      "c",
		Model:       modelName,
		ExternalID:  "base.install_mode_write",
		ForceUpdate: true,
		Data: orm.RecordMap{
			"name": "After",
		},
		SourcePath: "test.json",
		SourceLine: 2,
	})
	if err != nil {
		t.Fatalf("importRecord() error = %v", err)
	}
	if status != "updated" {
		t.Fatalf("importRecord() status = %q, want updated", status)
	}
	if len(runtime.writes) == 0 {
		t.Fatalf("expected at least one write call")
	}
	if runtime.writes[0].Model != modelName {
		t.Fatalf("first write model = %q, want %q", runtime.writes[0].Model, modelName)
	}
	if !runtime.writes[0].InstallMode {
		t.Fatalf("expected target write to run with install_mode context")
	}
}

func TestLoadCSVImportRecordsKeepsNumericStringFieldsAsStrings(t *testing.T) {
	registerRuntimeImportModels(t)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "tbl.country.ward.csv")
	content := "id,state_id,country_id,code,name\n" +
		"state_vn_VN-HN_10105001,base.state_vn_VN-HN,vn,\"10105001\",Phường Hoàn Kiếm\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	records, err := loadCSVImportRecords(path)
	if err != nil {
		t.Fatalf("loadCSVImportRecords() error = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("record count = %d, want 1", len(records))
	}

	got, ok := records[0].Data["code"].(string)
	if !ok {
		t.Fatalf("code type = %T, want string", records[0].Data["code"])
	}
	if got != "10105001" {
		t.Fatalf("code = %q, want 10105001", got)
	}
}

func TestParseCSVCellTreatsExplicitQuotedValuesAsStrings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "single quoted", input: "'10105001'", want: "10105001"},
		{name: "double quoted", input: "\"10105001\"", want: "10105001"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseCSVCell(tt.input)
			text, ok := got.(string)
			if !ok {
				t.Fatalf("parseCSVCell(%q) type = %T, want string", tt.input, got)
			}
			if text != tt.want {
				t.Fatalf("parseCSVCell(%q) = %q, want %q", tt.input, text, tt.want)
			}
		})
	}
}

func TestImportPathCreatesAndUpdatesRecordsWithExternalIDs(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.setRow("tbl.groups", "10", orm.RecordMap{"id": "10", "bid": "base.group_system"})
	runtime.setRow("tbl.groups", "11", orm.RecordMap{"id": "11", "bid": "base.group_user"})
	runtime.bids["base.user_admin"] = bidTarget{
		RowID:   "3",
		Model:   "base.users",
		ModelId: "12",
	}
	runtime.markExists("base.users", "12")

	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	groupPath := filepath.Join(tempDir, "10_tbl_groups.json")
	groupPayload := `[
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_system","name":"System","description":"System admins","superadmin":true}},
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_user","name":"User","description":"Standard users","superadmin":false}}
	]`
	if err := os.WriteFile(groupPath, []byte(groupPayload), 0o644); err != nil {
		t.Fatalf("write group fixture: %v", err)
	}

	userPath := filepath.Join(tempDir, "11_base_users.json")
	userPayload := `[
		{"action":"c","model":"base.users","id":"base.user_admin","forceUpdate":true,"data":{"login":"admin","email":"admin@example.com","name":"Administrator","password":"admin","group_id":"base.group_system"}},
		{"action":"c","model":"base.users","id":"base.user_operator","forceUpdate":true,"data":{"login":"operator","email":"operator@example.com","name":"Operator","password":"operator","group_id":"base.group_user"}}
	]`
	if err := os.WriteFile(userPath, []byte(userPayload), 0o644); err != nil {
		t.Fatalf("write user fixture: %v", err)
	}

	report, err := service.ImportPath(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("ImportPath() error = %v", err)
	}
	if report.Created != 1 {
		t.Fatalf("created = %d, want 1", report.Created)
	}
	if report.Updated != 3 {
		t.Fatalf("updated = %d, want 3", report.Updated)
	}

	var userWrite runtimeCall
	foundUserWrite := false
	for _, writeCall := range runtime.writes {
		if writeCall.Model != "base.users" {
			continue
		}
		userWrite = writeCall
		foundUserWrite = true
	}
	if !foundUserWrite {
		t.Fatalf("expected at least one base.users write, got %#v", runtime.writes)
	}
	resolvedGroup := userWrite.Values["group_id"]
	switch typed := resolvedGroup.(type) {
	case int64:
		if typed != 10 {
			t.Fatalf("resolved group relation = %#v, want 10", resolvedGroup)
		}
	case int:
		if typed != 10 {
			t.Fatalf("resolved group relation = %#v, want 10", resolvedGroup)
		}
	case string:
		if typed != "10" {
			t.Fatalf("resolved group relation = %#v, want 10", resolvedGroup)
		}
	default:
		t.Fatalf("resolved group relation = %#v, want 10", resolvedGroup)
	}

	if _, ok := runtime.bids["base.user_operator"]; !ok {
		t.Fatalf("expected base.user_operator bid to be created")
	}
}

func TestCollectImportFilesFromPathPrioritizesCSVBeforeJSON(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "11_init.users.json")
	csvPath := filepath.Join(tempDir, "tbl.lang.csv")
	if err := os.WriteFile(jsonPath, []byte("[]"), 0o644); err != nil {
		t.Fatalf("write json fixture: %v", err)
	}
	if err := os.WriteFile(csvPath, []byte("id,name,code,iso_code,direction,grouping,decimal_point,thousands_sep,date_format,time_format,week_start\n"), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	files, err := collectImportFilesFromPath(tempDir)
	if err != nil {
		t.Fatalf("collectImportFilesFromPath() error = %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("file count = %d, want 2", len(files))
	}
	if files[0] != csvPath || files[1] != jsonPath {
		t.Fatalf("files = %#v, want [%q %q]", files, csvPath, jsonPath)
	}
}

func TestCollectImportFilesFromPathKeepsDependencyProvidersAheadOfCSVConsumers(t *testing.T) {
	registerRuntimeImportModels(t)

	tempDir := t.TempDir()
	countryPath := filepath.Join(tempDir, "13_tbl_country.json")
	countryPayload := `[
		{"action":"c","model":"tbl.country","id":"vn","forceUpdate":true,"data":{"name":"Vietnam","code":"VN"}}
	]`
	if err := os.WriteFile(countryPath, []byte(countryPayload), 0o644); err != nil {
		t.Fatalf("write country fixture: %v", err)
	}

	statePath := filepath.Join(tempDir, "14_tbl_country_state.json")
	statePayload := `[
		{"action":"c","model":"tbl.country.state","id":"state_vn_VN-HN","forceUpdate":true,"data":{"name":"Ha Noi","code":"VN-HN","country_id":"vn"}}
	]`
	if err := os.WriteFile(statePath, []byte(statePayload), 0o644); err != nil {
		t.Fatalf("write state fixture: %v", err)
	}

	wardPath := filepath.Join(tempDir, "tbl.country.ward.csv")
	wardPayload := "id,state_id,country_id,code,name\n" +
		"state_vn_VN-HN_10105001,base.state_vn_VN-HN,vn,\"10105001\",Phường Hoàn Kiếm\n"
	if err := os.WriteFile(wardPath, []byte(wardPayload), 0o644); err != nil {
		t.Fatalf("write ward fixture: %v", err)
	}

	files, err := collectImportFilesFromPath(tempDir)
	if err != nil {
		t.Fatalf("collectImportFilesFromPath() error = %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("file count = %d, want 3", len(files))
	}
	if files[0] != countryPath || files[1] != statePath || files[2] != wardPath {
		t.Fatalf("files = %#v, want [%q %q %q]", files, countryPath, statePath, wardPath)
	}
}

func TestImportPathResolvesResLangExternalIDFromCSVLoadedBeforeJSON(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	csvPath := filepath.Join(tempDir, "tbl.lang.csv")
	csvPayload := "id,name,code,iso_code,direction,grouping,decimal_point,thousands_sep,date_format,time_format,week_start\n" +
		"base_lang_en,English (US),en_US,en,ltr,\"3,0\",.,,,%m/%d/%Y,%H:%M:%S,sunday\n"
	if err := os.WriteFile(csvPath, []byte(csvPayload), 0o644); err != nil {
		t.Fatalf("write csv fixture: %v", err)
	}

	jsonPath := filepath.Join(tempDir, "11_init.users.json")
	jsonPayload := `[
		{"action":"c","model":"base.users","id":"base.user_admin","forceUpdate":true,"data":{"login":"admin","name":"Administrator","password":"admin","lang_id":"base_lang_en"}}
	]`
	if err := os.WriteFile(jsonPath, []byte(jsonPayload), 0o644); err != nil {
		t.Fatalf("write json fixture: %v", err)
	}

	report, err := service.ImportPath(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("ImportPath() error = %v", err)
	}
	if report.Created != 2 {
		t.Fatalf("created = %d, want 2", report.Created)
	}
	if len(runtime.created) < 2 {
		t.Fatalf("created calls = %#v, want at least 2", runtime.created)
	}

	var userCreate runtimeCall
	foundUserCreate := false
	for _, createCall := range runtime.created {
		if createCall.Model != models.ResUserModelName {
			continue
		}
		userCreate = createCall
		foundUserCreate = true
	}
	if !foundUserCreate {
		t.Fatalf("expected base.users create, got %#v", runtime.created)
	}

	switch typed := userCreate.Values["lang_id"].(type) {
	case int64:
		if typed != 101 {
			t.Fatalf("resolved lang_id relation = %#v, want 101", userCreate.Values["lang_id"])
		}
	case int:
		if typed != 101 {
			t.Fatalf("resolved lang_id relation = %#v, want 101", userCreate.Values["lang_id"])
		}
	case string:
		if typed != "101" {
			t.Fatalf("resolved lang_id relation = %#v, want 101", userCreate.Values["lang_id"])
		}
	default:
		t.Fatalf("resolved lang_id relation = %#v, want 101", userCreate.Values["lang_id"])
	}
}

func TestImportPathResetForceOverridesNoUpdateRecordsByBID(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.setRow("tbl.groups", "10", orm.RecordMap{
		"id":          "10",
		"bid":         "base.group_system",
		"name":        "Legacy",
		"description": "Legacy description",
	})

	service := newServiceWithRuntime(runtime)
	service.SetForceOverrideByBID(true)

	tempDir := t.TempDir()
	groupPath := filepath.Join(tempDir, "10_tbl_groups.json")
	groupPayload := `[
		{"action":"c","model":"tbl.groups","data":{"bid":"base.group_system","name":"System","description":"System admins"},"noupdate":true}
	]`
	if err := os.WriteFile(groupPath, []byte(groupPayload), 0o644); err != nil {
		t.Fatalf("write group fixture: %v", err)
	}

	report, err := service.ImportPath(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("ImportPath() error = %v", err)
	}
	if report.Updated != 1 {
		t.Fatalf("updated = %d, want 1", report.Updated)
	}
	if report.Skipped != 0 {
		t.Fatalf("skipped = %d, want 0", report.Skipped)
	}
	if len(runtime.writes) != 1 {
		t.Fatalf("expected exactly one write call for reset import (no sys.model.data catalog row for a bidDirectModels model), got %#v", runtime.writes)
	}
	targetWrite := runtime.writes[0]
	if targetWrite.Model != "tbl.groups" {
		t.Fatalf("write model = %q, want tbl.groups", targetWrite.Model)
	}
	if got := toString(targetWrite.Values["name"]); got != "System" {
		t.Fatalf("write name = %q, want System", got)
	}
}

func TestLookupExternalIDFallsBackToBareSuffix(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.bids["no"] = bidTarget{
		RowID:     "10",
		Model:     "tbl.country",
		ModelId:   "251",
		Reference: "tbl.country,251",
	}

	service := newServiceWithRuntime(runtime)

	target, found, err := service.lookupExternalID(context.Background(), "base.no")
	if err != nil {
		t.Fatalf("lookupExternalID() error = %v", err)
	}
	if !found {
		t.Fatal("lookupExternalID() did not resolve qualified alias")
	}
	if target.Model != "tbl.country" || target.ModelId != "251" {
		t.Fatalf("lookupExternalID() = %#v, want tbl.country/251", target)
	}
	if _, ok := service.bids["base.no"]; !ok {
		t.Fatal("expected resolved alias to be cached")
	}
}

func TestCollectImportFilesFromPathRejectsWrongFolderExtension(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "modules", "base")
	viewPath := filepath.Join(root, "views", "bad.json")
	if err := os.MkdirAll(filepath.Dir(viewPath), 0o755); err != nil {
		t.Fatalf("mkdir views: %v", err)
	}
	if err := os.WriteFile(viewPath, []byte(`[]`), 0o644); err != nil {
		t.Fatalf("write invalid views fixture: %v", err)
	}

	_, err := collectImportFilesFromPath(filepath.Join(root, "views"))
	if err == nil {
		t.Fatalf("expected invalid views json to fail")
	}
	if !strings.Contains(err.Error(), "views imports only support .xml files") {
		t.Fatalf("error = %v", err)
	}
}

func TestCollectImportFilesFromFSRejectsWrongFolderExtension(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"base/security/bad.xml": &fstest.MapFile{Data: []byte("<data/>")},
	}

	_, err := collectImportFilesFromFS(fs.FS(fsys), "base/security")
	if err == nil {
		t.Fatalf("expected invalid security xml to fail")
	}
	if !strings.Contains(err.Error(), "security imports only support .json and .csv files") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadImportRecordsRejectsWrongDirectModuleFileExtension(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "modules", "base", "data", "bad.xml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir data: %v", err)
	}
	if err := os.WriteFile(path, []byte("<data/>"), 0o644); err != nil {
		t.Fatalf("write invalid data fixture: %v", err)
	}

	_, err := loadImportRecords(path)
	if err == nil {
		t.Fatalf("expected invalid data xml to fail")
	}
	if !strings.Contains(err.Error(), "data imports only support .json and .csv files") {
		t.Fatalf("error = %v", err)
	}
}

func TestLookupExternalIDNormalizesLegacyCountryModelAlias(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.bids["base.state_no_1"] = bidTarget{
		RowID:     "20",
		Model:     "base.country.state",
		ModelId:   "301",
		Reference: "base.country.state,301",
	}

	service := newServiceWithRuntime(runtime)

	target, found, err := service.lookupExternalID(context.Background(), "base.state_no_1")
	if err != nil {
		t.Fatalf("lookupExternalID() error = %v", err)
	}
	if !found {
		t.Fatal("lookupExternalID() did not resolve legacy model alias")
	}
	if target.Model != models.ResCountryStateModelName {
		t.Fatalf("lookupExternalID() model = %q, want %q", target.Model, models.ResCountryStateModelName)
	}
}

func TestResolveReferenceValueMapsLegacyResLangCodeToRelationID(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.setRow(models.ResLangModelName, "9", orm.RecordMap{
		"id":     int64(9),
		"code":   "en_US",
		"active": true,
	})

	service := newServiceWithRuntime(runtime)

	got, err := service.resolveReferenceValue(context.Background(), models.ResLangModelName, "en_US")
	if err != nil {
		t.Fatalf("resolveReferenceValue() error = %v", err)
	}
	if got != int64(9) {
		t.Fatalf("resolveReferenceValue() = %#v, want int64(9)", got)
	}
}

func TestEnsurePathImportedDoesNotClobberAlreadySetPasswordOnReimport(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	passwordHash, err := bcrypt.GenerateFromPassword([]byte("wrong-password"), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("GenerateFromPassword() error = %v", err)
	}
	runtime.bids["user.admin"] = bidTarget{
		RowID:     "41",
		Model:     "base.users",
		ModelId:   "7",
		Reference: "base.users,7",
	}
	runtime.setRow("base.users", "7", orm.RecordMap{
		"id":       "7",
		"login":    "admin",
		"password": string(passwordHash),
	})

	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	userPath := filepath.Join(tempDir, "11_init.users.json")
	userPayload := `{
		"action":"c",
		"model":"base.users",
		"id":"user.admin",
		"forceUpdate":true,
		"data":{
			"login":"admin",
			"password":"admin",
			"active":true
		}
	}`
	if err := os.WriteFile(userPath, []byte(userPayload), 0o644); err != nil {
		t.Fatalf("write user fixture: %v", err)
	}

	report, err := service.EnsurePathImported(context.Background(), tempDir)
	if err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}
	if !report.Imported {
		t.Fatalf("expected EnsurePathImported to trigger import, got %#v", report)
	}
	if len(report.Missing) == 0 || report.Missing[0] != "user.admin" {
		t.Fatalf("missing = %#v, want user.admin", report.Missing)
	}
	foundUserWrite := false
	for _, call := range runtime.writes {
		if call.Model != "base.users" || len(call.IDs) != 1 || call.IDs[0] != "7" {
			continue
		}
		foundUserWrite = true
		if toString(call.Values["login"]) != "admin" {
			t.Fatalf("expected admin login to be replayed on update, got %#v", call.Values["login"])
		}
		if _, ok := call.Values["password"]; ok {
			t.Fatalf("expected seed password to be dropped from update because a real password is already stored, got %#v", call.Values["password"])
		}
	}
	if !foundUserWrite {
		t.Fatalf("expected base.users write during refresh, got %#v", runtime.writes)
	}
}

func TestEnsurePathImportedSetsSeedPasswordWhenCurrentlyEmpty(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.bids["user.admin"] = bidTarget{
		RowID:     "41",
		Model:     "base.users",
		ModelId:   "7",
		Reference: "base.users,7",
	}
	runtime.setRow("base.users", "7", orm.RecordMap{
		"id":       "7",
		"login":    "admin",
		"password": "",
	})

	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	userPath := filepath.Join(tempDir, "11_init.users.json")
	userPayload := `{
		"action":"c",
		"model":"base.users",
		"id":"user.admin",
		"forceUpdate":true,
		"data":{
			"login":"admin",
			"password":"admin",
			"active":true
		}
	}`
	if err := os.WriteFile(userPath, []byte(userPayload), 0o644); err != nil {
		t.Fatalf("write user fixture: %v", err)
	}

	if _, err := service.EnsurePathImported(context.Background(), tempDir); err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}

	foundUserWrite := false
	for _, call := range runtime.writes {
		if call.Model != "base.users" || len(call.IDs) != 1 || call.IDs[0] != "7" {
			continue
		}
		foundUserWrite = true
		if toString(call.Values["password"]) != "admin" {
			t.Fatalf("expected seed password to be set because the stored password was empty, got %#v", call.Values["password"])
		}
	}
	if !foundUserWrite {
		t.Fatalf("expected base.users write during refresh, got %#v", runtime.writes)
	}
}

func TestEnsurePathImportedSkipsImportWhenAllMetadataExists(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.setRow("tbl.groups", "10", orm.RecordMap{"id": "10", "bid": "base.group_system"})
	runtime.setRow("tbl.groups", "11", orm.RecordMap{"id": "11", "bid": "base.group_user"})

	service := newServiceWithRuntime(runtime)
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_groups.json")
	payload := `[
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_system","name":"System"}},
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_user","name":"User"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.EnsurePathImported(context.Background(), path)
	if err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}
	if report.Imported {
		t.Fatalf("EnsurePathImported() imported = true, want false")
	}
	if report.Checked != 2 {
		t.Fatalf("checked = %d, want 2", report.Checked)
	}
	if len(runtime.created) != 0 || len(runtime.writes) != 0 {
		t.Fatalf("expected no import calls, got created=%d writes=%d", len(runtime.created), len(runtime.writes))
	}
}

func TestEnsurePathImportedRunsImportWhenMetadataMissing(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	service := newServiceWithRuntime(runtime)
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_groups.json")
	payload := `[
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_system","name":"System"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.EnsurePathImported(context.Background(), path)
	if err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}
	if !report.Imported {
		t.Fatalf("EnsurePathImported() imported = false, want true")
	}
	if len(report.Missing) != 1 || report.Missing[0] != "base.group_system" {
		t.Fatalf("missing = %#v, want [base.group_system]", report.Missing)
	}
	if report.ImportReport == nil || report.ImportReport.Created != 1 {
		t.Fatalf("import report = %#v, want created=1", report.ImportReport)
	}
}

// TestEnsurePathImportedTreatsStaleMetadataAsMissing exercises the
// sys.model.data-catalog "stale" case (catalog row present, target row
// gone) for a non-bidDirectModels-style scenario: bidDirectModels resolve
// directly against their own table, so there is no separate catalog entry
// that can go stale independently — "no matching bid row" and "stale
// metadata" collapse into the same outcome for tbl.groups/sys.rule.
func TestEnsurePathImportedTreatsStaleMetadataAsMissing(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_groups.json")
	payload := `[
		{"action":"c","model":"tbl.groups","forceUpdate":true,"data":{"bid":"base.group_system","name":"System"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.EnsurePathImported(context.Background(), path)
	if err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}
	if !report.Imported {
		t.Fatalf("EnsurePathImported() imported = false, want true")
	}
	if len(report.Missing) != 1 || report.Missing[0] != "base.group_system" {
		t.Fatalf("missing = %#v, want [base.group_system]", report.Missing)
	}
}

func TestEnsurePathImportedTreatsLegacyModelAliasMetadataAsMissing(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.bids["base.no"] = bidTarget{
		RowID:   "29",
		Model:   "tbl.country",
		ModelId: "251",
	}
	runtime.markExists("tbl.country", "251")
	runtime.bids["base.state_no_1"] = bidTarget{
		RowID:   "30",
		Model:   "base.country.state",
		ModelId: "301",
	}
	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_country_state.json")
	payload := `[
		{"action":"c","model":"tbl.country.state","id":"base.state_no_1","forceUpdate":true,"data":{"name":"Oslo","code":"OSL","country_id":"base.no"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.EnsurePathImported(context.Background(), path)
	if err != nil {
		t.Fatalf("EnsurePathImported() error = %v", err)
	}
	if !report.Imported {
		t.Fatalf("EnsurePathImported() imported = false, want true")
	}
	if len(report.Missing) != 1 || report.Missing[0] != "base.state_no_1" {
		t.Fatalf("missing = %#v, want [base.state_no_1]", report.Missing)
	}
}

// TestImportPathRecreatesStaleBIDTarget exercises the sys.model.data catalog
// recreate-on-stale-target path. Deliberately uses tbl.country, not
// tbl.groups: bidDirectModels (tbl.groups, sys.rule) resolve directly
// against their own bid column with no separate catalog row, so they have
// no "catalog says it exists but the target row is gone" failure mode to
// exercise — see TestEnsurePathImportedTreatsStaleMetadataAsMissing.
func TestImportPathRecreatesStaleBIDTarget(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	runtime.bids["base.no"] = bidTarget{
		RowID:   "1",
		Model:   "tbl.country",
		ModelId: "10",
	}
	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_country.json")
	payload := `[
		{"action":"c","model":"tbl.country","external_id":"base.no","forceUpdate":true,"data":{"name":"Norway","code":"NO"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.ImportPath(context.Background(), path)
	if err != nil {
		t.Fatalf("ImportPath() error = %v", err)
	}
	if report.Created != 1 {
		t.Fatalf("created = %d, want 1", report.Created)
	}
	if len(runtime.writes) == 0 || runtime.writes[len(runtime.writes)-1].Model != "sys.model.data" {
		t.Fatalf("expected stale bid metadata row to be updated, got writes=%#v", runtime.writes)
	}
}

// TestImportPathAdoptsRowByMatchFieldsWhenExternalIDStale: the external id's row is gone, but a row
// with the same natural key exists (recreated by normal use). The import writes that row and
// re-points the external id instead of creating a duplicate.
func TestImportPathAdoptsRowByMatchFieldsWhenExternalIDStale(t *testing.T) {
	registerRuntimeImportModels(t)
	orm.RegisterImportMatchFields("tbl.country", "code")
	t.Cleanup(func() { orm.RegisterImportMatchFields("tbl.country", " ") })

	runtime := newFakeRuntime()
	runtime.bids["base.no"] = bidTarget{RowID: "1", Model: "tbl.country", ModelId: "10"}
	runtime.rows["tbl.country"] = map[string]orm.RecordMap{
		"20": {"id": "20", "name": "Norway (old)", "code": "NO"},
	}
	service := newServiceWithRuntime(runtime)

	path := filepath.Join(t.TempDir(), "10_tbl_country.json")
	payload := `[
		{"action":"c","model":"tbl.country","external_id":"base.no","forceUpdate":true,"data":{"name":"Norway","code":"NO"}}
	]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	report, err := service.ImportPath(context.Background(), path)
	if err != nil {
		t.Fatalf("ImportPath() error = %v", err)
	}
	if report.Created != 0 || report.Updated != 1 {
		t.Fatalf("created=%d updated=%d, want 0/1", report.Created, report.Updated)
	}
	var wroteCountry, wroteExternalID bool
	for _, call := range runtime.writes {
		if call.Model == "tbl.country" && len(call.IDs) == 1 && call.IDs[0] == "20" {
			wroteCountry = true
		}
		if call.Model == "sys.model.data" && toString(call.Values["model_id"]) == "20" {
			wroteExternalID = true
		}
	}
	if !wroteCountry || !wroteExternalID {
		t.Fatalf("want write of tbl.country[20] and external id re-pointed to 20, got writes=%#v", runtime.writes)
	}
}

func TestLoadImportRecordsRejectsMissingExternalID(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_country.json")
	payload := `[{"action":"c","model":"tbl.country","data":{"name":"Norway"}}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := loadImportRecords(path); err == nil {
		t.Fatal("loadImportRecords() error = nil, want missing external id error")
	}
}

// TestImportRecordByBIDRejectsMissingBID is the bidDirectModels analogue of
// TestLoadImportRecordsRejectsMissingExternalID: tbl.groups/sys.rule skip
// the load-time external_id check (they carry their own bid instead), but
// importRecordByBID still rejects a record whose data.bid is blank.
func TestImportRecordByBIDRejectsMissingBID(t *testing.T) {
	registerRuntimeImportModels(t)

	runtime := newFakeRuntime()
	service := newServiceWithRuntime(runtime)

	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "10_tbl_groups.json")
	payload := `[{"action":"c","model":"tbl.groups","data":{"name":"System"}}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	if _, err := service.ImportPath(context.Background(), tempDir); err == nil {
		t.Fatal("ImportPath() error = nil, want bid required error")
	}
}

func TestModuleDataJSONFilesUseCanonicalImportEnvelopeKeys(t *testing.T) {
	root := filepath.Join("../../../modules")
	problems := make([]string, 0)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		normalized := filepath.ToSlash(path)
		if !strings.Contains(normalized, "/data/") || !strings.HasSuffix(strings.ToLower(normalized), ".json") {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		var rows []map[string]any
		if err := json.Unmarshal(raw, &rows); err != nil {
			problems = append(problems, normalized+": expected JSON import array")
			return nil
		}

		for index, row := range rows {
			lineRef := normalized + ":" + strconv.Itoa(index+1)
			if _, hasLegacyID := row["id"]; hasLegacyID {
				problems = append(problems, lineRef+": use external_id instead of id")
			}
			if _, hasLegacyForce := row["forceUpdate"]; hasLegacyForce {
				problems = append(problems, lineRef+": use force_update instead of forceUpdate")
			}
			// bidDirectModels (tbl.groups, sys.rule) match on their own data.bid
			// instead of external_id — see service.go's bidDirectModels.
			if bidDirectModels[strings.TrimSpace(stringValue(row["model"]))] {
				data, _ := row["data"].(map[string]any)
				if strings.TrimSpace(stringValue(data["bid"])) == "" {
					problems = append(problems, lineRef+": data.bid is required")
				}
				continue
			}
			if strings.TrimSpace(stringValue(row["external_id"])) == "" {
				problems = append(problems, lineRef+": external_id is required")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module data json: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("module data JSON envelope problems:\n%s", strings.Join(problems, "\n"))
	}
}

func TestModuleDataCSVFilesUseCanonicalExternalIDHeader(t *testing.T) {
	root := filepath.Join("../../../modules")
	problems := make([]string, 0)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		normalized := filepath.ToSlash(path)
		if !strings.Contains(normalized, "/data/") || !strings.HasSuffix(strings.ToLower(normalized), ".csv") {
			return nil
		}

		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		firstLine := strings.TrimSpace(strings.SplitN(string(raw), "\n", 2)[0])
		if strings.HasPrefix(firstLine, "\"id\",") || strings.HasPrefix(firstLine, "id,") {
			problems = append(problems, normalized+": use external_id header instead of id")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk module data csv: %v", err)
	}
	if len(problems) > 0 {
		t.Fatalf("module data CSV header problems:\n%s", strings.Join(problems, "\n"))
	}
}

func registerRuntimeImportModels(t *testing.T) {
	t.Helper()

	for _, item := range []struct {
		name string
		init func() error
	}{
		{
			name: models.SysModelDataModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.SysModelDataRecord](models.SysModelDataModelSpec)
				return err
			},
		},
		{
			name: models.ResGroupModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResGroupRecord](models.ResGroupModelSpec)
				return err
			},
		},
		{
			name: models.ResUserModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResUserRecord](models.ResUserModelSpec)
				return err
			},
		},
		{
			name: models.ResLangModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResLangRecord](models.ResLangModelSpec)
				return err
			},
		},
		{
			name: models.ResCountryModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResCountryRecord](models.ResCountryModelSpec)
				return err
			},
		},
		{
			name: models.ResCountryStateModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResCountryStateRecord](models.ResCountryStateModelSpec)
				return err
			},
		},
		{
			name: models.ResCountryWardModelName,
			init: func() error {
				_, err := orm.RegisterModelType[models.ResCountryWardRecord](models.ResCountryWardModelSpec)
				return err
			},
		},
	} {
		if _, ok := orm.LookupModel(item.name); ok {
			continue
		}
		if err := item.init(); err != nil {
			t.Fatalf("register model %q: %v", item.name, err)
		}
	}
}

func cloneRecordMap(in orm.RecordMap) orm.RecordMap {
	out := make(orm.RecordMap, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func toString(value any) string {
	return stringValue(value)
}

func toStringID(value int64) string {
	return toString(value)
}
