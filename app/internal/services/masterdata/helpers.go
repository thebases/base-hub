package masterdata

// File overview:
// - Responsibility: small shared helpers: external id parsing, model/module inference from paths, value coercion.
// - Key symbols: `uniqueExpectedRecordIdentities`, `lookupField`, `splitExternalID`, `externalIDParts`.

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
)

// uniqueExpectedExternalIDs collects the expected external i ds handled by this package.
func uniqueExpectedRecordIdentities(records []ImportRecord) []expectedRecordIdentity {
	seen := make(map[expectedRecordIdentity]struct{}, len(records))
	out := make([]expectedRecordIdentity, 0, len(records))
	for _, record := range records {
		identity := recordIdentity(record)
		if identity.ID == "" {
			continue
		}
		if _, exists := seen[identity]; exists {
			continue
		}
		seen[identity] = struct{}{}
		out = append(out, identity)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Model != out[j].Model {
			return out[i].Model < out[j].Model
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// lookupField looks up the field handled by this package.
func lookupField(model *orm.ModelMetadata, requested string) (orm.FieldMetadata, bool) {
	if model == nil {
		return orm.FieldMetadata{}, false
	}
	needle := strings.TrimSpace(requested)
	if needle == "" {
		return orm.FieldMetadata{}, false
	}
	for _, field := range model.Fields {
		switch needle {
		case field.Name, field.GoName, field.JSONName, field.BunColumn:
			return field, true
		}
	}
	return orm.FieldMetadata{}, false
}

// splitExternalID splits the external id handled by this package.
func splitExternalID(value string) (string, string) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultImportModule, ""
	}
	parts := strings.SplitN(trimmed, ".", 2)
	if len(parts) == 2 && strings.TrimSpace(parts[0]) != "" && strings.TrimSpace(parts[1]) != "" {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return defaultImportModule, trimmed
}

func externalIDParts(record ImportRecord) (string, string) {
	moduleName := strings.TrimSpace(record.Module)
	externalID := strings.TrimSpace(record.ExternalID)
	if moduleName == "" {
		moduleName, externalID = splitExternalID(externalID)
		return moduleName, externalID
	}

	prefix := moduleName + "."
	if strings.HasPrefix(externalID, prefix) {
		externalID = strings.TrimPrefix(externalID, prefix)
	}
	if externalID == "" {
		externalID = moduleName
	}
	return moduleName, externalID
}

func externalIDLookupCandidates(value string) []string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}

	candidates := []string{trimmed}
	if dot := strings.Index(trimmed, "."); dot > 0 && dot < len(trimmed)-1 {
		// Master-data payloads sometimes author records with bare ids like
		// "no" but reference them later through qualified aliases like
		// "base.no". Fall back to the authored suffix after exact lookup fails.
		suffix := strings.TrimSpace(trimmed[dot+1:])
		if suffix != "" && suffix != trimmed {
			candidates = append(candidates, suffix)
		}
	}
	return candidates
}

func normalizeMasterDataModelName(value string) string {
	modelName := strings.TrimSpace(value)
	switch modelName {
	case "base.country":
		return models.ResCountryModelName
	case "base.country.group":
		return models.ResCountryGroupModelName
	case "base.country.state":
		return models.ResCountryStateModelName
	default:
		return modelName
	}
}

// inferModelFromPath infers the model from path handled by this package.
func inferModelFromPath(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	base = strings.TrimSpace(base)
	if base == "" {
		return ""
	}

	parts := strings.SplitN(base, "_", 2)
	if len(parts) == 2 {
		if _, err := strconv.Atoi(parts[0]); err == nil {
			base = parts[1]
		}
	}
	base = strings.ReplaceAll(base, "_", ".")
	return strings.TrimSpace(base)
}

// inferModuleFromPath infers the module from path handled by this package.
func inferModuleFromPath(path string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return ""
	}
	parts := strings.Split(normalized, "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "modules" && i+1 < len(parts) {
			return strings.TrimSpace(parts[i+1])
		}
	}
	if len(parts) >= 2 {
		switch parts[1] {
		case "data", "security", "views", "i18n", "static":
			return strings.TrimSpace(parts[0])
		}
	}
	return ""
}

func anyToRecordMap(value any) (orm.RecordMap, error) {
	switch typed := value.(type) {
	case nil:
		return orm.RecordMap{}, nil
	case orm.RecordMap:
		return typed, nil
	case map[string]any:
		return orm.RecordMap(typed), nil
	default:
		raw, err := json.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var out orm.RecordMap
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, err
		}
		return out, nil
	}
}

// cloneMap clones the map handled by this package.
func cloneMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	default:
		return fmt.Sprint(value)
	}
}

func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	case int:
		return typed != 0
	case int64:
		return typed != 0
	case float64:
		return typed != 0
	default:
		return false
	}
}

func runtimeInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int8:
		return int64(typed), true
	case int16:
		return int64(typed), true
	case int32:
		return int64(typed), true
	case int64:
		return typed, true
	case uint:
		return int64(typed), true
	case uint8:
		return int64(typed), true
	case uint16:
		return int64(typed), true
	case uint32:
		return int64(typed), true
	case uint64:
		return int64(typed), true
	case float64:
		return int64(typed), true
	default:
		return 0, false
	}
}

func runtimeStringID(value any) (string, bool) {
	if id, ok := runtimeInt64(value); ok {
		return strconv.FormatInt(id, 10), true
	}
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	default:
		return "", false
	}
}

func idFilterValue(value string) any {
	trimmed := strings.TrimSpace(value)
	if parsed, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return parsed
	}
	return trimmed
}

// firstNonEmpty returns the non empty handled by this package.
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// firstNonNil returns the non nil handled by this package.
func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
