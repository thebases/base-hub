package masterdata

// File overview:
// - Responsibility: CSV import parsing: row normalisation and per-field cell coercion.
// - Key symbols: `loadCSVImportRecords`, `loadCSVImportRecordsFSFile`, `normalizeCSVRows`, `csvRowModelMetadata`.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	iofs "io/fs"
	"os"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

// loadCSVImportRecords loads the csv import records handled by this package.
func loadCSVImportRecords(path string) ([]ImportRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return normalizeCSVRows(path, rows)
}

// loadCSVImportRecordsFSFile loads the csv import records fs file handled by this package.
func loadCSVImportRecordsFSFile(fsys iofs.FS, path string) ([]ImportRecord, error) {
	file, err := fsys.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.FieldsPerRecord = -1
	rows, err := reader.ReadAll()
	if err != nil {
		return nil, err
	}
	return normalizeCSVRows(path, rows)
}

func normalizeCSVRows(path string, rows [][]string) ([]ImportRecord, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	header := make([]string, 0, len(rows[0]))
	// Walk each rows[0 entry in order so validation and side effects stay scoped to the current element.
	for _, value := range rows[0] {
		header = append(header, strings.TrimSpace(value))
	}

	inferredModel := inferModelFromPath(path)
	out := make([]ImportRecord, 0, max(0, len(rows)-1))
	for i := 1; i < len(rows); i++ {
		line := rows[i]
		if isBlankCSVLine(line) {
			continue
		}

		row := make(map[string]any, len(header))
		modelMeta := csvRowModelMetadata(inferredModel, line, header)
		for columnIndex, name := range header {
			value := ""
			if columnIndex < len(line) {
				value = line[columnIndex]
			}
			row[name] = parseCSVCellForField(modelMeta, name, value)
		}
		if strings.TrimSpace(fmt.Sprint(row["model"])) == "" && inferredModel != "" {
			row["model"] = inferredModel
		}

		record, err := normalizeRawImportMap(row, path, i+1)
		if err != nil {
			return nil, err
		}
		out = append(out, record)
	}
	return out, nil
}

func csvRowModelMetadata(inferredModel string, line, header []string) *orm.ModelMetadata {
	modelName := strings.TrimSpace(inferredModel)
	if modelName == "" {
		for columnIndex, name := range header {
			if strings.TrimSpace(name) != "model" {
				continue
			}
			if columnIndex >= len(line) {
				break
			}
			modelName = strings.TrimSpace(line[columnIndex])
			break
		}
	}
	if modelName == "" {
		return nil
	}

	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return nil
	}
	return meta
}

// parseCSVCell parses the csv cell handled by this package.
func parseCSVCell(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if unquoted, ok := unwrapExplicitCSVString(trimmed); ok {
		return unquoted
	}

	switch strings.ToLower(trimmed) {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}

	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var decoded any
		if err := json.Unmarshal([]byte(trimmed), &decoded); err == nil {
			return decoded
		}
	}

	if intValue, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
		return intValue
	}
	if floatValue, err := strconv.ParseFloat(trimmed, 64); err == nil {
		return floatValue
	}
	return value
}

func unwrapExplicitCSVString(value string) (string, bool) {
	if len(value) < 2 {
		return "", false
	}
	if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
		return value[1 : len(value)-1], true
	}
	if strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") {
		return value[1 : len(value)-1], true
	}
	return "", false
}

// parseCSVCellForField parses the csv cell for field handled by this package.
func parseCSVCellForField(model *orm.ModelMetadata, fieldName, value string) any {
	if shouldPreserveCSVStringField(model, fieldName) {
		return value
	}
	return parseCSVCell(value)
}

func shouldPreserveCSVStringField(model *orm.ModelMetadata, fieldName string) bool {
	if model == nil {
		return false
	}
	field, ok := lookupField(model, fieldName)
	if !ok {
		return false
	}
	return strings.TrimSpace(field.Type) == "string"
}

func isBlankCSVLine(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return false
		}
	}
	return true
}
