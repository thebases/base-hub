package masterdata

// File overview:
// - Responsibility: reads import records from disk or fs.FS (JSON/CSV files), including path validation and raw map normalisation.
// - Key symbols: `loadImportRecords`, `loadImportRecordsFromFS`, `loadImportRecordsFromPath`, `loadImportRecordsFromPaths`.

import (
	"context"
	"encoding/json"
	"fmt"
	iofs "io/fs"
	"os"
	"path/filepath"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// loadImportRecords loads the import records handled by this package.
func loadImportRecords(path string) ([]ImportRecord, error) {
	if err := validateImportFilePath(path); err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return loadJSONImportRecords(path)
	case ".csv":
		return loadCSVImportRecords(path)
	case ".xml":
		return loadXMLImportRecords(path)
	default:
		log.Errorf("unsupported import format %q", filepath.Ext(path))
		return nil, fmt.Errorf("unsupported import format %q", filepath.Ext(path))
	}
}

// loadImportRecordsFromFS loads the import records from fs handled by this package.
func loadImportRecordsFromFS(fsys iofs.FS, root string) ([]ImportRecord, error) {
	files, err := collectImportFilesFromFS(fsys, root)
	if err != nil {
		return nil, err
	}

	records := make([]ImportRecord, 0)
	for _, filePath := range files {
		fileRecords, err := loadImportRecordsFSFile(fsys, filePath)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}
	return records, nil
}

// loadImportRecordsFromPath keeps the startup presence check aligned with the
// importer by reusing the same per-file parsing order for directories.
func loadImportRecordsFromPath(path string) ([]ImportRecord, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return loadImportRecords(path)
	}

	files, err := collectImportFilesFromPath(path)
	if err != nil {
		return nil, err
	}
	records := make([]ImportRecord, 0)
	for _, filePath := range files {
		fileRecords, err := loadImportRecords(filePath)
		if err != nil {
			return nil, err
		}
		records = append(records, fileRecords...)
	}
	return records, nil
}

// loadImportRecordsFromPaths loads the import records from paths handled by this package.
func loadImportRecordsFromPaths(paths []string) ([]ImportRecord, error) {
	records := make([]ImportRecord, 0)
	// Walk each sortimportpaths(paths) entry in order so validation and side effects stay scoped to the current element.
	for _, path := range sortImportPaths(paths) {
		pathRecords, err := loadImportRecordsFromPath(path)
		if err != nil {
			return nil, err
		}
		records = append(records, pathRecords...)
	}
	return records, nil
}

// loadImportRecordsFromFSRoots loads the import records from fs roots handled by this package.
func loadImportRecordsFromFSRoots(fsys iofs.FS, roots []string) ([]ImportRecord, error) {
	records := make([]ImportRecord, 0)
	// Walk each sortimportpaths(roots) entry in order so validation and side effects stay scoped to the current element.
	for _, root := range sortImportPaths(roots) {
		rootRecords, err := loadImportRecordsFromFS(fsys, root)
		if err != nil {
			return nil, err
		}
		records = append(records, rootRecords...)
	}
	return records, nil
}

// collectImportFilesFromPath collects the import files from path handled by this package.
func collectImportFilesFromPath(root string) ([]string, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if err := validateImportFilePath(root); err != nil {
			return nil, err
		}
		return []string{root}, nil
	}

	files := make([]string, 0)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if err := validateImportFilePath(path); err != nil {
			return err
		}
		if isSupportedImportExt(path) {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	files = sortImportPaths(files)
	files, err = reorderImportFilesByDependencies(files, loadImportRecords)
	if err != nil {
		return nil, err
	}
	return files, nil
}

// collectImportFilesFromFS collects the import files from fs handled by this package.
func collectImportFilesFromFS(fsys iofs.FS, root string) ([]string, error) {
	root = cleanFSPath(root)
	info, err := iofs.Stat(fsys, root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if err := validateImportFilePath(root); err != nil {
			return nil, err
		}
		return []string{root}, nil
	}

	files := make([]string, 0)
	if err := iofs.WalkDir(fsys, root, func(path string, entry iofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if err := validateImportFilePath(path); err != nil {
			return err
		}
		if isSupportedImportExt(path) {
			files = append(files, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	files = sortImportPaths(files)
	files, err = reorderImportFilesByDependencies(files, func(path string) ([]ImportRecord, error) {
		return loadImportRecordsFSFile(fsys, path)
	})
	if err != nil {
		return nil, err
	}
	return files, nil
}

// loadJSONImportRecords loads the json import records handled by this package.
func loadJSONImportRecords(path string) ([]ImportRecord, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	switch typed := payload.(type) {
	case []any:
		out := make([]ImportRecord, 0, len(typed))
		for i, item := range typed {
			row, ok := item.(map[string]any)
			if !ok {
				log.Errorf("%s:%d: JSON item must be an object", path, i+1)
				return nil, importErrorf(path, i+1, "JSON item must be an object")
			}
			record, err := normalizeRawImportMap(row, path, i+1)
			if err != nil {
				return nil, err
			}
			out = append(out, record)
		}
		return out, nil
	case map[string]any:
		record, err := normalizeRawImportMap(typed, path, 1)
		if err != nil {
			return nil, err
		}
		return []ImportRecord{record}, nil
	default:
		log.Errorf("%s: JSON root must be an object or array", path)
		return nil, importErrorf(path, 0, "JSON root must be an object or array")
	}
}

// loadImportRecordsFSFile loads the import records fs file handled by this package.
func loadImportRecordsFSFile(fsys iofs.FS, path string) ([]ImportRecord, error) {
	if err := validateImportFilePath(path); err != nil {
		return nil, err
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		return loadJSONImportRecordsFSFile(fsys, path)
	case ".csv":
		return loadCSVImportRecordsFSFile(fsys, path)
	case ".xml":
		return loadXMLImportRecordsFSFile(fsys, path)
	default:
		log.Errorf("unsupported import format %q", filepath.Ext(path))
		return nil, fmt.Errorf("unsupported import format %q", filepath.Ext(path))
	}
}

// loadJSONImportRecordsFSFile loads the json import records fs file handled by this package.
func loadJSONImportRecordsFSFile(fsys iofs.FS, path string) ([]ImportRecord, error) {
	raw, err := iofs.ReadFile(fsys, path)
	if err != nil {
		return nil, err
	}

	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, err
	}

	switch typed := payload.(type) {
	case []any:
		out := make([]ImportRecord, 0, len(typed))
		for i, item := range typed {
			row, ok := item.(map[string]any)
			if !ok {
				log.Errorf("%s:%d: JSON item must be an object", path, i+1)
				return nil, importErrorf(path, i+1, "JSON item must be an object")
			}
			record, err := normalizeRawImportMap(row, path, i+1)
			if err != nil {
				return nil, err
			}
			out = append(out, record)
		}
		return out, nil
	case map[string]any:
		record, err := normalizeRawImportMap(typed, path, 1)
		if err != nil {
			return nil, err
		}
		return []ImportRecord{record}, nil
	default:
		log.Errorf("%s: JSON root must be an object or array", path)
		return nil, importErrorf(path, 0, "JSON root must be an object or array")
	}
}

func (s *Service) importFSFile(ctx context.Context, fsys iofs.FS, path string) (FileReport, error) {
	records, err := loadImportRecordsFSFile(fsys, path)
	if err != nil {
		return FileReport{}, err
	}

	report := FileReport{Path: path}
	for _, record := range records {
		outcome, err := s.importRecord(ctx, record)
		if err != nil {
			return FileReport{}, err
		}
		switch outcome {
		case "created":
			report.Created++
		case "updated":
			report.Updated++
		case "skipped":
			report.Skipped++
		}
	}
	return report, nil
}

// cleanFSPath normalizes the fs path handled by this package.
func cleanFSPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || path == "." {
		return "."
	}
	return strings.TrimPrefix(filepath.Clean(path), "./")
}

func isSupportedImportExt(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json", ".csv", ".xml":
		return true
	default:
		return false
	}
}

// validateImportFilePath validates the import file path handled by this package.
func validateImportFilePath(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return nil
	}

	importDir := importDirName(path)
	if importDir == "" {
		return nil
	}

	switch importDir {
	case "data", "security":
		switch ext {
		case ".json", ".csv":
			return nil
		case ".xml":
			log.Errorf("%s: %s imports only support .json and .csv files", path, importDir)
			return importErrorf(path, 0, "%s imports only support .json and .csv files", importDir)
		}
	case "views":
		switch ext {
		case ".xml":
			return nil
		case ".json", ".csv":
			log.Errorf("%s: views imports only support .xml files", path)
			return importErrorf(path, 0, "views imports only support .xml files")
		}
	}
	return nil
}

func importDirName(path string) string {
	normalized := filepath.ToSlash(strings.TrimSpace(path))
	if normalized == "" {
		return ""
	}
	parts := strings.Split(normalized, "/")
	for i := len(parts) - 2; i >= 0; i-- {
		switch parts[i] {
		case "security", "data", "views":
			return parts[i]
		}
	}
	return ""
}

func normalizeRawImportMap(row map[string]any, sourcePath string, line int) (ImportRecord, error) {
	working := cloneMap(row)
	modelName := firstNonEmpty(stringValue(working["model"]), inferModelFromPath(sourcePath))
	externalID := firstNonEmpty(
		stringValue(working["external_id"]),
		stringValue(working["bid"]),
		stringValue(working["id"]),
	)

	moduleName := strings.TrimSpace(stringValue(working["module"]))
	if moduleName == "" {
		moduleName = inferModuleFromPath(sourcePath)
	}
	if moduleName == "" && externalID != "" {
		moduleName, _ = splitExternalID(externalID)
	}
	if moduleName == "" {
		moduleName = defaultImportModule
	}

	// Walk each string{"action", "model", "external id", "bid", "id", "forceupdate", "force update", "noupdate", "module" entry in order so validation and side effects stay scoped to the current element.
	for _, key := range []string{"action", "model", "external_id", "bid", "id", "forceUpdate", "force_update", "noupdate", "module"} {
		delete(working, key)
	}

	data := orm.RecordMap{}
	if rawData, ok := row["data"]; ok {
		typed, err := anyToRecordMap(rawData)
		if err != nil {
			log.Errorf("%s:%d: invalid data payload: %v", sourcePath, line, err)
			return ImportRecord{}, importErrorf(sourcePath, line, "invalid data payload: %w", err)
		}
		data = typed
	} else {
		for key, value := range working {
			data[key] = value
		}
	}

	record := ImportRecord{
		Action:      strings.ToLower(strings.TrimSpace(firstNonEmpty(stringValue(row["action"]), "c"))),
		Model:       modelName,
		ExternalID:  externalID,
		ForceUpdate: boolValue(firstNonNil(row["forceUpdate"], row["force_update"])),
		NoUpdate:    boolValue(row["noupdate"]),
		Module:      moduleName,
		Data:        data,
		SourcePath:  sourcePath,
		SourceLine:  line,
	}

	if record.Action != "c" && record.Action != "u" && record.Action != "" {
		log.Errorf("%s:%d: unsupported action %q", sourcePath, line, record.Action)
		return ImportRecord{}, importErrorf(sourcePath, line, "unsupported action %q", record.Action)
	}
	if strings.TrimSpace(record.Model) == "" {
		log.Errorf("%s:%d: model is required", sourcePath, line)
		return ImportRecord{}, importErrorf(sourcePath, line, "model is required")
	}
	// bidDirectModels (tbl.groups, sys.rule) carry their own data.bid instead
	// of a top-level external_id — checked later in importRecordByBID.
	if strings.TrimSpace(record.ExternalID) == "" && !bidDirectModels[modelName] {
		log.Errorf("%s:%d: external id is required", sourcePath, line)
		return ImportRecord{}, importErrorf(sourcePath, line, "external id is required")
	}
	if record.Data == nil {
		record.Data = orm.RecordMap{}
	}
	return record, nil
}
