package masterdata

// File overview:
// - Responsibility: encapsulates domain/service orchestration for view xml, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `loadXMLImportRecords`, `loadXMLImportRecordsFSFile`, `xmlRoot`, `xmlData`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"encoding/xml"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

var xmlRefPattern = regexp.MustCompile(`ref\(\s*['"]([^'"]+)['"]\s*\)`)

type xmlRoot struct {
	XMLName   xml.Name      `xml:""`
	NoUpdate  string        `xml:"noupdate,attr"`
	Data      []xmlData     `xml:"data"`
	MenuItems []xmlMenuItem `xml:"menuitem"`
	Records   []xmlRecord   `xml:"record"`
}

type xmlData struct {
	NoUpdate  string        `xml:"noupdate,attr"`
	MenuItems []xmlMenuItem `xml:"menuitem"`
	Records   []xmlRecord   `xml:"record"`
}

type xmlRecord struct {
	ID     string     `xml:"id,attr"`
	Model  string     `xml:"model,attr"`
	Fields []xmlField `xml:"field"`
}

type xmlField struct {
	Name  string `xml:"name,attr"`
	Ref   string `xml:"ref,attr"`
	Type  string `xml:"type,attr"`
	Eval  string `xml:"eval,attr"`
	Inner string `xml:",innerxml"`
}

type xmlMenuItem struct {
	ID        string        `xml:"id,attr"`
	Name      string        `xml:"name,attr"`
	Parent    string        `xml:"parent,attr"`
	Action    string        `xml:"action,attr"`
	Sequence  string        `xml:"sequence,attr"`
	Icon      string        `xml:"icon,attr"`
	WebIcon   string        `xml:"web_icon,attr"`
	RoutePath string        `xml:"route_path,attr"`
	Groups    string        `xml:"groups,attr"`
	Active    string        `xml:"active,attr"`
	MenuItems []xmlMenuItem `xml:"menuitem"`
}

// loadXMLImportRecords loads the xml import records handled by this package.
func loadXMLImportRecords(path string) ([]ImportRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseXMLImportRecords(path, raw)
}

// loadXMLImportRecordsFSFile loads the xml import records fs file handled by this package.
func loadXMLImportRecordsFSFile(fsys iofs.FS, path string) ([]ImportRecord, error) {
	raw, err := iofs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}
	return parseXMLImportRecords(path, raw)
}

// parseXMLImportRecords parses the xml import records handled by this package.
func parseXMLImportRecords(path string, raw []byte) ([]ImportRecord, error) {
	var root xmlRoot
	if err := xml.Unmarshal(raw, &root); err != nil {
		return nil, err
	}

	moduleName := inferModuleFromPath(path)
	if moduleName == "" {
		moduleName = defaultImportModule
	}

	records := make([]ImportRecord, 0)
	actionSpecs := make(map[string]xmlActionSpec)
	recordIndex := 0
	appendRecord := func(entry xmlRecord, noupdate bool) error {
		recordIndex++
		record, err := xmlRecordToImportRecord(path, recordIndex, moduleName, entry, noupdate)
		if err != nil {
			return err
		}
		if spec, ok := xmlActionSpecFromRecord(record); ok {
			actionSpecs[record.ExternalID] = spec
		}
		records = append(records, record)
		return nil
	}
	appendMenuItem := func(entry xmlMenuItem, parentExternalID string, noupdate bool) error {
		menuRecords, err := xmlMenuItemToImportRecords(path, &recordIndex, moduleName, entry, parentExternalID, noupdate, actionSpecs)
		if err != nil {
			return err
		}
		records = append(records, menuRecords...)
		return nil
	}

	switch strings.ToLower(strings.TrimSpace(root.XMLName.Local)) {
	case "odoo":
		for _, record := range root.Records {
			if err := appendRecord(record, false); err != nil {
				return nil, err
			}
		}
		for _, menuItem := range root.MenuItems {
			if err := appendMenuItem(menuItem, "", false); err != nil {
				return nil, err
			}
		}
		for _, data := range root.Data {
			noupdate := xmlTruthy(data.NoUpdate)
			for _, record := range data.Records {
				if err := appendRecord(record, noupdate); err != nil {
					return nil, err
				}
			}
			for _, menuItem := range data.MenuItems {
				if err := appendMenuItem(menuItem, "", noupdate); err != nil {
					return nil, err
				}
			}
		}
	case "data":
		noupdate := xmlTruthy(root.NoUpdate)
		for _, record := range root.Records {
			if err := appendRecord(record, noupdate); err != nil {
				return nil, err
			}
		}
		for _, menuItem := range root.MenuItems {
			if err := appendMenuItem(menuItem, "", noupdate); err != nil {
				return nil, err
			}
		}
	default:
		log.Errorf("%s: unsupported XML root <%s>", path, root.XMLName.Local)
		return nil, fmt.Errorf("%s: unsupported XML root <%s>", path, root.XMLName.Local)
	}
	return records, nil
}

func xmlRecordToImportRecord(path string, line int, moduleName string, entry xmlRecord, noupdate bool) (ImportRecord, error) {
	modelName := strings.TrimSpace(entry.Model)
	if modelName == "" {
		log.Errorf("%s:%d: XML record model is required", path, line)
		return ImportRecord{}, fmt.Errorf("%s:%d: XML record model is required", path, line)
	}
	if !isSupportedXMLImportModel(modelName) {
		log.Errorf("%s:%d: unsupported XML record model %q in views import", path, line, modelName)
		return ImportRecord{}, fmt.Errorf("%s:%d: unsupported XML record model %q in views import", path, line, modelName)
	}

	externalID := qualifyModuleExternalID(moduleName, strings.TrimSpace(entry.ID))
	if externalID == "" {
		log.Errorf("%s:%d: XML record id is required", path, line)
		return ImportRecord{}, fmt.Errorf("%s:%d: XML record id is required", path, line)
	}

	values := make(orm.RecordMap, len(entry.Fields))
	for _, field := range entry.Fields {
		name := strings.TrimSpace(field.Name)
		if name == "" {
			log.Errorf("%s:%d: XML field name is required", path, line)
			return ImportRecord{}, fmt.Errorf("%s:%d: XML field name is required", path, line)
		}
		value, err := xmlFieldValue(moduleName, name, field)
		if err != nil {
			log.Errorf("%s:%d: %v", path, line, err)
			return ImportRecord{}, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		values[name] = value
	}
	return ImportRecord{
		Action:      "c",
		Model:       modelName,
		ExternalID:  externalID,
		ForceUpdate: true,
		NoUpdate:    noupdate,
		Module:      moduleName,
		Data:        values,
		SourcePath:  path,
		SourceLine:  line,
	}, nil
}

func xmlFieldValue(moduleName string, fieldName string, field xmlField) (any, error) {
	if ref := strings.TrimSpace(field.Ref); ref != "" {
		return qualifyModuleExternalID(moduleName, ref), nil
	}
	if eval := strings.TrimSpace(field.Eval); eval != "" {
		return parseXMLEval(moduleName, fieldName, eval)
	}

	if strings.EqualFold(strings.TrimSpace(field.Type), "xml") || fieldName == "arch" || fieldName == "arch_base" {
		return strings.TrimSpace(field.Inner), nil
	}
	return parseCSVCell(strings.TrimSpace(field.Inner)), nil
}

// parseXMLEval parses the xml eval handled by this package.
func parseXMLEval(moduleName string, fieldName string, raw string) (any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", nil
	}

	switch strings.ToLower(trimmed) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return parsed, nil
	}

	refs := xmlRefPattern.FindAllStringSubmatch(trimmed, -1)
	if len(refs) > 0 {
		if fieldName == "group_ids" {
			values := make([]string, 0, len(refs))
			for _, ref := range refs {
				values = append(values, qualifyModuleExternalID(moduleName, ref[1]))
			}
			return values, nil
		}
		values := make([]string, 0, len(refs))
		for _, ref := range refs {
			values = append(values, qualifyModuleExternalID(moduleName, ref[1]))
		}
		if fieldName == "inherit_id" && len(values) == 1 {
			return values[0], nil
		}
	}

	parser := xmlEvalParser{
		moduleName: moduleName,
		input:      trimmed,
	}
	value, err := parser.parseValue()
	if err != nil {
		log.Errorf("unsupported eval expression %q: %v", raw, err)
		return nil, fmt.Errorf("unsupported eval expression %q: %w", raw, err)
	}
	parser.skipSpace()
	if !parser.eof() {
		log.Errorf("unsupported eval expression %q", raw)
		return nil, fmt.Errorf("unsupported eval expression %q", raw)
	}
	return value, nil
}

func qualifyModuleExternalID(moduleName string, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if strings.Contains(trimmed, ".") {
		return trimmed
	}
	if strings.TrimSpace(moduleName) == "" {
		return trimmed
	}
	return moduleName + "." + trimmed
}

func xmlTruthy(raw string) bool {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes":
		return true
	default:
		return false
	}
}

type xmlActionSpec struct {
	Model    string
	Name     string
	ResModel string
	Tag      string
	URL      string
}

func isSupportedXMLImportModel(modelName string) bool {
	switch strings.TrimSpace(modelName) {
	case models.UIViewModelName, models.UIMenuModelName, models.UIActionWindowModelName, models.UIActionClientModelName:
		return true
	default:
		return false
	}
}

func xmlActionSpecFromRecord(record ImportRecord) (xmlActionSpec, bool) {
	switch strings.TrimSpace(record.Model) {
	case models.UIActionWindowModelName:
		resModel := strings.TrimSpace(stringValue(record.Data["sys_model"]))
		return xmlActionSpec{
			Model:    models.UIActionWindowModelName,
			Name:     strings.TrimSpace(stringValue(record.Data["name"])),
			ResModel: resModel,
			URL:      xmlWindowActionRoute(resModel),
		}, true
	case models.UIActionClientModelName:
		return xmlActionSpec{
			Model: models.UIActionClientModelName,
			Name:  strings.TrimSpace(stringValue(record.Data["name"])),
			Tag:   strings.TrimSpace(stringValue(record.Data["tag"])),
		}, true
	default:
		return xmlActionSpec{}, false
	}
}

func xmlMenuItemToImportRecords(path string, recordIndex *int, moduleName string, entry xmlMenuItem, parentExternalID string, noupdate bool, actionSpecs map[string]xmlActionSpec) ([]ImportRecord, error) {
	menuID := qualifyModuleExternalID(moduleName, strings.TrimSpace(entry.ID))
	if menuID == "" {
		log.Errorf("%s:%d: menuitem id is required", path, *recordIndex+1)
		return nil, fmt.Errorf("%s:%d: menuitem id is required", path, *recordIndex+1)
	}
	name := strings.TrimSpace(entry.Name)
	if name == "" {
		log.Errorf("%s:%d: menuitem name is required", path, *recordIndex+1)
		return nil, fmt.Errorf("%s:%d: menuitem name is required", path, *recordIndex+1)
	}

	values := orm.RecordMap{
		"name": name,
		// menuitem has no persisted "inactive" concept in legacy XML, so default to
		// active unless the entry explicitly opts out with active="0"/"false".
		"active": true,
	}
	if active := strings.TrimSpace(entry.Active); active != "" {
		values["active"] = xmlTruthy(active)
	}
	if sequence := parseCSVCell(strings.TrimSpace(entry.Sequence)); strings.TrimSpace(entry.Sequence) != "" {
		values["sequence"] = sequence
	}
	if icon := strings.TrimSpace(firstNonEmpty(entry.Icon, entry.WebIcon)); icon != "" {
		values["icon"] = icon
	}
	// groups="base.group_multi_company,base.group_no_one" restricts the menu to callers holding any listed group BID.
	if groups := splitMenuGroupBIDs(entry.Groups); len(groups) > 0 {
		values["group_bids"] = groups
	}
	if parent := qualifyModuleExternalID(moduleName, strings.TrimSpace(entry.Parent)); parent != "" {
		values["parent_id"] = parent
	} else if parentExternalID != "" {
		values["parent_id"] = parentExternalID
	}
	if routePath := strings.TrimSpace(entry.RoutePath); routePath != "" {
		values["route_path"] = routePath
	}
	if actionRef := qualifyModuleExternalID(moduleName, strings.TrimSpace(entry.Action)); actionRef != "" {
		actionValue, routePath, err := xmlMenuActionValue(actionRef, actionSpecs[actionRef])
		if err != nil {
			log.Errorf("%s:%d: %v", path, *recordIndex+1, err)
			return nil, fmt.Errorf("%s:%d: %w", path, *recordIndex+1, err)
		}
		values["action"] = actionValue
		if strings.TrimSpace(stringValue(values["route_path"])) == "" && routePath != "" {
			values["route_path"] = routePath
		}
	}

	*recordIndex = *recordIndex + 1
	out := []ImportRecord{{
		Action:      "c",
		Model:       models.UIMenuModelName,
		ExternalID:  menuID,
		ForceUpdate: true,
		NoUpdate:    noupdate,
		Module:      moduleName,
		Data:        values,
		SourcePath:  path,
		SourceLine:  *recordIndex,
	}}
	for _, child := range entry.MenuItems {
		childRecords, err := xmlMenuItemToImportRecords(path, recordIndex, moduleName, child, menuID, noupdate, actionSpecs)
		if err != nil {
			return nil, err
		}
		out = append(out, childRecords...)
	}
	return out, nil
}

func xmlMenuActionValue(actionExternalID string, spec xmlActionSpec) (map[string]any, string, error) {
	switch spec.Model {
	case models.UIActionWindowModelName:
		out := map[string]any{
			"type": models.UIActionWindowModelName,
			"bid":  actionExternalID,
		}
		if spec.Name != "" {
			out["name"] = spec.Name
		}
		if spec.ResModel != "" {
			out["sys_model"] = spec.ResModel
		}
		if spec.URL != "" {
			out["url"] = spec.URL
		}
		return out, spec.URL, nil
	case models.UIActionClientModelName:
		out := map[string]any{
			"type": models.UIActionClientModelName,
			"bid":  actionExternalID,
		}
		if spec.Name != "" {
			out["name"] = spec.Name
		}
		if spec.Tag != "" {
			out["tag"] = spec.Tag
		}
		return out, "", nil
	case "":
		log.Errorf("menu action %q was not found in XML records", actionExternalID)
		return nil, "", fmt.Errorf("menu action %q was not found in XML records", actionExternalID)
	default:
		log.Errorf("menu action %q points to unsupported model %q", actionExternalID, spec.Model)
		return nil, "", fmt.Errorf("menu action %q points to unsupported model %q", actionExternalID, spec.Model)
	}
}

func xmlWindowActionRoute(resModel string) string {
	resModel = strings.TrimSpace(resModel)
	if resModel == "" {
		return ""
	}
	return "/web/" + resModel
}

type xmlEvalParser struct {
	moduleName string
	input      string
	pos        int
}

func (p *xmlEvalParser) eof() bool {
	return p.pos >= len(p.input)
}

func (p *xmlEvalParser) skipSpace() {
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.input[p.pos:])
		if !unicode.IsSpace(r) {
			return
		}
		p.pos += size
	}
}

// parseValue parses the value handled by this package.
func (p *xmlEvalParser) parseValue() (any, error) {
	p.skipSpace()
	if p.eof() {
		log.Errorf("unexpected end of expression")
		return nil, fmt.Errorf("unexpected end of expression")
	}
	switch p.input[p.pos] {
	case '\'', '"':
		return p.parseString()
	case '[':
		return p.parseList(']')
	case '(':
		return p.parseList(')')
	case '{':
		return p.parseDict()
	default:
		return p.parseSymbol()
	}
}

// parseString parses the string handled by this package.
func (p *xmlEvalParser) parseString() (string, error) {
	quote := p.input[p.pos]
	p.pos++
	var out strings.Builder
	for !p.eof() {
		ch := p.input[p.pos]
		p.pos++
		if ch == '\\' {
			if p.eof() {
				log.Errorf("unterminated escape")
				return "", fmt.Errorf("unterminated escape")
			}
			out.WriteByte(p.input[p.pos])
			p.pos++
			continue
		}
		if ch == quote {
			return out.String(), nil
		}
		out.WriteByte(ch)
	}
	log.Errorf("unterminated string")
	return "", fmt.Errorf("unterminated string")
}

// parseList parses the list handled by this package.
func (p *xmlEvalParser) parseList(closing byte) ([]any, error) {
	p.pos++
	out := make([]any, 0)
	for {
		p.skipSpace()
		if p.eof() {
			log.Errorf("unterminated list")
			return nil, fmt.Errorf("unterminated list")
		}
		if p.input[p.pos] == closing {
			p.pos++
			return out, nil
		}
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out = append(out, value)
		p.skipSpace()
		if p.eof() {
			log.Errorf("unterminated list")
			return nil, fmt.Errorf("unterminated list")
		}
		if p.input[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.input[p.pos] == closing {
			p.pos++
			return out, nil
		}
		log.Errorf("unexpected token %q", p.input[p.pos])
		return nil, fmt.Errorf("unexpected token %q", p.input[p.pos])
	}
}

// parseDict parses the dict handled by this package.
func (p *xmlEvalParser) parseDict() (map[string]any, error) {
	p.pos++
	out := make(map[string]any)
	for {
		p.skipSpace()
		if p.eof() {
			log.Errorf("unterminated dict")
			return nil, fmt.Errorf("unterminated dict")
		}
		if p.input[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		keyValue, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.eof() || p.input[p.pos] != ':' {
			log.Errorf("dict key is missing ':'")
			return nil, fmt.Errorf("dict key is missing ':'")
		}
		p.pos++
		value, err := p.parseValue()
		if err != nil {
			return nil, err
		}
		out[fmt.Sprint(keyValue)] = value
		p.skipSpace()
		if p.eof() {
			log.Errorf("unterminated dict")
			return nil, fmt.Errorf("unterminated dict")
		}
		if p.input[p.pos] == ',' {
			p.pos++
			continue
		}
		if p.input[p.pos] == '}' {
			p.pos++
			return out, nil
		}
		log.Errorf("unexpected token %q", p.input[p.pos])
		return nil, fmt.Errorf("unexpected token %q", p.input[p.pos])
	}
}

// parseSymbol parses the symbol handled by this package.
func (p *xmlEvalParser) parseSymbol() (any, error) {
	start := p.pos
	for !p.eof() {
		r, size := utf8.DecodeRuneInString(p.input[p.pos:])
		if unicode.IsSpace(r) || strings.ContainsRune(",:()[]{}", r) {
			break
		}
		p.pos += size
	}
	token := strings.TrimSpace(p.input[start:p.pos])
	switch token {
	case "True", "true":
		return true, nil
	case "False", "false":
		return false, nil
	case "None", "null":
		return nil, nil
	case "ref":
		return p.parseRefCall()
	case "Command.set":
		values, err := p.parseCallArguments()
		if err != nil {
			return nil, err
		}
		if len(values) == 0 {
			return []any{}, nil
		}
		return values[0], nil
	}
	if intValue, err := strconv.ParseInt(token, 10, 64); err == nil {
		return intValue, nil
	}
	return token, nil
}

// parseRefCall parses the ref call handled by this package.
func (p *xmlEvalParser) parseRefCall() (string, error) {
	values, err := p.parseCallArguments()
	if err != nil {
		return "", err
	}
	if len(values) != 1 {
		log.Errorf("ref() expects one argument")
		return "", fmt.Errorf("ref() expects one argument")
	}
	return qualifyModuleExternalID(p.moduleName, fmt.Sprint(values[0])), nil
}

// parseCallArguments parses the call arguments handled by this package.
func (p *xmlEvalParser) parseCallArguments() ([]any, error) {
	p.skipSpace()
	if p.eof() || p.input[p.pos] != '(' {
		log.Errorf("expected '('")
		return nil, fmt.Errorf("expected '('")
	}
	return p.parseList(')')
}

func importStagePriority(path string) int {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	parts := strings.Split(normalized, "/")
	for i := len(parts) - 2; i >= 0; i-- {
		switch parts[i] {
		case "security":
			return 0
		case "data":
			return 1
		case "views":
			return 2
		case "i18n":
			return 3
		case "static":
			return 4
		}
	}
	return 5
}

// importFileExtensionPriority keeps relation providers such as CSV-backed
// reference data ahead of JSON consumers during deterministic import walks.
func importFileExtensionPriority(path string) int {
	switch strings.ToLower(filepath.Ext(strings.TrimSpace(path))) {
	case ".csv":
		return 0
	case ".json":
		return 1
	case ".xml":
		return 2
	default:
		return 3
	}
}

func sortImportPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, value := range paths {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	sort.SliceStable(out, func(i, j int) bool {
		leftModule := inferModuleFromPath(out[i])
		rightModule := inferModuleFromPath(out[j])
		if leftModule != rightModule {
			return leftModule < rightModule
		}
		leftStage := importStagePriority(out[i])
		rightStage := importStagePriority(out[j])
		if leftStage != rightStage {
			return leftStage < rightStage
		}
		leftExtPriority := importFileExtensionPriority(out[i])
		rightExtPriority := importFileExtensionPriority(out[j])
		if leftExtPriority != rightExtPriority {
			return leftExtPriority < rightExtPriority
		}
		return out[i] < out[j]
	})
	return out
}

// splitMenuGroupBIDs turns the comma-separated groups attribute into trimmed, non-empty group BIDs.
func splitMenuGroupBIDs(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if bid := strings.TrimSpace(part); bid != "" {
			out = append(out, bid)
		}
	}
	return out
}
