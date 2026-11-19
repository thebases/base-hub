package masterdata

// File overview:
// - Responsibility: encapsulates domain/service orchestration for service, keeping integrations and multi-step business flows out of transport handlers.
// - Key symbols: `defaultImportModule`, `Report`, `EnsureReport`, `FileReport`.

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	iofs "io/fs"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"golang.org/x/crypto/bcrypt"
)

const defaultImportModule = "base"

// Report summarizes one import run so operators can review what changed.
type Report struct {
	Files   []FileReport `json:"files"`
	Created int          `json:"created"`
	Updated int          `json:"updated"`
	Skipped int          `json:"skipped"`
}

// EnsureReport summarizes the startup metadata check that decides whether one
// import run is required for the current master-data payload.
type EnsureReport struct {
	Checked      int      `json:"checked"`
	Missing      []string `json:"missing"`
	Imported     bool     `json:"imported"`
	ImportReport *Report  `json:"import_report,omitempty"`
}

// FileReport summarizes one imported file inside one directory run.
type FileReport struct {
	Path    string `json:"path"`
	Created int    `json:"created"`
	Updated int    `json:"updated"`
	Skipped int    `json:"skipped"`
}

// ImportRecord is the normalized in-memory shape shared by JSON and CSV files.
type ImportRecord struct {
	Action      string
	Model       string
	ExternalID  string
	ForceUpdate bool
	NoUpdate    bool
	Module      string
	Data        orm.RecordMap
	SourcePath  string
	SourceLine  int
}

type runtimeClient interface {
	LookupModel(name string) (*orm.ModelMetadata, bool)
	Create(ctx context.Context, model string, values orm.RecordMap) (orm.RecordMap, error)
	Write(ctx context.Context, model string, ids []string, values orm.RecordMap) (bool, error)
	SearchRead(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error)
	Unlink(ctx context.Context, model string, ids []string) (bool, error)
}

type defaultRuntimeClient struct {
	service *modelruntime.Service
}

// LookupModel looks up the model handled by this package.
func (c *defaultRuntimeClient) LookupModel(name string) (*orm.ModelMetadata, bool) {
	return orm.LookupModel(strings.TrimSpace(name))
}

// Create creates the create handled by this package.
func (c *defaultRuntimeClient) Create(ctx context.Context, model string, values orm.RecordMap) (orm.RecordMap, error) {
	return c.service.Create(ctx, model, values)
}

func (c *defaultRuntimeClient) Write(ctx context.Context, model string, ids []string, values orm.RecordMap) (bool, error) {
	return c.service.Write(ctx, model, ids, values)
}

func (c *defaultRuntimeClient) SearchRead(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
	return c.service.SearchRead(ctx, model, filter, fields)
}

// Unlink deletes the given rows of model, used by module uninstall to remove
// the master data a module previously seeded.
func (c *defaultRuntimeClient) Unlink(ctx context.Context, model string, ids []string) (bool, error) {
	return c.service.Unlink(ctx, model, ids)
}

type bidTarget struct {
	RowID     string
	Model     string
	ModelId   string
	NoUpdate  bool
	Reference string
}

// Service imports master data into the active runtime models while keeping one
// persisted external-id catalog for deterministic re-imports.
type Service struct {
	runtime            runtimeClient
	bids               map[string]bidTarget
	forceOverrideByBID bool
}

// importRuntimeContext marks seed CRUD as install-mode work so model-level
// bootstrap guards can tell deterministic seed replay apart from operator
// edits without weakening the invariants enforced for normal runtime writes.
func importRuntimeContext(ctx context.Context) context.Context {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return ctx
	}
	return orm.ContextWithEnvironment(ctx, env.WithContext(map[string]any{
		"install_mode": true,
	}))
}

// NewService constructs and returns the service handled by this package.
func NewService() *Service {
	return &Service{
		runtime: &defaultRuntimeClient{service: modelruntime.NewService()},
		bids:    make(map[string]bidTarget),
	}
}

// newServiceWithRuntime constructs and returns the service with runtime handled by this package.
func newServiceWithRuntime(runtime runtimeClient) *Service {
	return &Service{
		runtime: runtime,
		bids:    make(map[string]bidTarget),
	}
}

// SetForceOverrideByBID makes CLI-triggered imports overwrite existing
// records matched by external id even when the seed row is marked noupdate.
func (s *Service) SetForceOverrideByBID(enabled bool) {
	if s == nil {
		return
	}
	s.forceOverrideByBID = enabled
}

// EnsurePathImported verifies that every import record is already represented
// in sys.model.data and only executes one full import when metadata is missing.
func (s *Service) EnsurePathImported(ctx context.Context, path string) (*EnsureReport, error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}

	records, err := loadImportRecordsFromPath(path)
	if err != nil {
		return nil, err
	}
	return s.ensureImportedRecords(ctx, records, func() (*Report, error) {
		return s.ImportPath(ctx, path)
	})
}

// EnsurePathsImported verifies multiple import roots as one logical module
// bootstrap payload so startup can load every module seed directory together.
func (s *Service) EnsurePathsImported(ctx context.Context, paths []string) (*EnsureReport, error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}
	records, err := loadImportRecordsFromPaths(paths)
	if err != nil {
		return nil, err
	}
	return s.ensureImportedRecords(ctx, records, func() (*Report, error) {
		return s.ImportPaths(ctx, paths)
	})
}

// EnsureFSImported verifies that every import record in the provided fs.FS is
// already represented in sys.model.data and runs one import when metadata is
// missing. This keeps packaged binaries independent from runtime file layout.
func (s *Service) EnsureFSImported(ctx context.Context, fsys iofs.FS, root string) (*EnsureReport, error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}

	records, err := loadImportRecordsFromFS(fsys, root)
	if err != nil {
		return nil, err
	}
	return s.ensureImportedRecords(ctx, records, func() (*Report, error) {
		return s.ImportFS(ctx, fsys, root)
	})
}

// EnsureFSRootsImported verifies multiple embedded import roots as one logical
// module bootstrap payload.
func (s *Service) EnsureFSRootsImported(ctx context.Context, fsys iofs.FS, roots []string) (*EnsureReport, error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}
	records, err := loadImportRecordsFromFSRoots(fsys, roots)
	if err != nil {
		return nil, err
	}
	return s.ensureImportedRecords(ctx, records, func() (*Report, error) {
		return s.ImportFSRoots(ctx, fsys, roots)
	})
}

// expectedRecordIdentity is one record's dedup key for ensureImportedRecords:
// either its ExternalID (sys.model.data catalog models) or its own bid value
// (bidDirectModels), scoped by model since bid values are only unique within
// their own model's table, not globally like sys.model.data's complete_name.
type expectedRecordIdentity struct {
	Model string
	ID    string
}

// recordIdentity resolves the expected identity for one import record,
// branching the same way importRecord/resolveReferenceValue do.
func recordIdentity(record ImportRecord) expectedRecordIdentity {
	modelName := normalizeMasterDataModelName(record.Model)
	if bidDirectModels[modelName] {
		return expectedRecordIdentity{Model: modelName, ID: strings.TrimSpace(stringValue(record.Data["bid"]))}
	}
	return expectedRecordIdentity{Model: modelName, ID: strings.TrimSpace(record.ExternalID)}
}

// ensureImportedRecords ensures the imported records handled by this package.
func (s *Service) ensureImportedRecords(ctx context.Context, records []ImportRecord, importFn func() (*Report, error)) (*EnsureReport, error) {
	identities := uniqueExpectedRecordIdentities(records)
	report := &EnsureReport{
		Checked: len(identities),
		Missing: make([]string, 0),
	}
	for _, identity := range identities {
		var target bidTarget
		var found bool
		var err error
		if bidDirectModels[identity.Model] {
			target, found, err = s.lookupByBID(ctx, identity.Model, identity.ID)
		} else {
			target, found, err = s.lookupExternalID(ctx, identity.ID)
		}
		if err != nil {
			return nil, err
		}
		if !found {
			report.Missing = append(report.Missing, identity.ID)
			continue
		}
		exists, err := s.targetRecordExists(ctx, target.Model, target.ModelId)
		if err != nil {
			return nil, err
		}
		if !exists {
			report.Missing = append(report.Missing, identity.ID)
			continue
		}
		record, ok := findImportRecordByIdentity(records, identity)
		if !ok {
			continue
		}
		needsRefresh, err := s.targetRecordNeedsRefresh(ctx, record, target)
		if err != nil {
			return nil, err
		}
		if needsRefresh {
			report.Missing = append(report.Missing, identity.ID)
		}
	}
	if len(report.Missing) == 0 {
		return report, nil
	}

	importReport, err := importFn()
	if err != nil {
		return nil, err
	}
	report.Imported = true
	report.ImportReport = importReport
	return report, nil
}

// findImportRecordByIdentity finds the import record matching identity
// (model + external id or bid, depending on bidDirectModels).
func findImportRecordByIdentity(records []ImportRecord, identity expectedRecordIdentity) (ImportRecord, bool) {
	for _, record := range records {
		if recordIdentity(record) == identity {
			return record, true
		}
	}
	return ImportRecord{}, false
}

func (s *Service) targetRecordNeedsRefresh(ctx context.Context, record ImportRecord, target bidTarget) (bool, error) {
	modelName := normalizeMasterDataModelName(record.Model)
	if modelName != models.ResUserModelName {
		return false, nil
	}

	seedLogin := strings.TrimSpace(fmt.Sprint(record.Data["login"]))
	seedPassword := strings.TrimSpace(fmt.Sprint(record.Data["password"]))
	if seedLogin == "" && seedPassword == "" {
		return false, nil
	}

	rows, err := s.runtime.SearchRead(
		ctx,
		modelName,
		orm.RecordMap{"id": idFilterValue(target.ModelId)},
		[]string{"id", "login", "password"},
	)
	if err != nil {
		return false, err
	}
	if len(rows) == 0 {
		return true, nil
	}

	current := rows[0]
	if seedLogin != "" && strings.TrimSpace(fmt.Sprint(current["login"])) != seedLogin {
		return true, nil
	}
	if seedPassword == "" {
		return false, nil
	}

	currentPassword := strings.TrimSpace(fmt.Sprint(current["password"]))
	if currentPassword == "" {
		return true, nil
	}
	if strings.HasPrefix(seedPassword, "$2") {
		return currentPassword != seedPassword, nil
	}
	if bcrypt.CompareHashAndPassword([]byte(currentPassword), []byte(seedPassword)) != nil {
		return true, nil
	}
	return false, nil
}

// ImportPath imports one file or one directory of files in lexical order.
func (s *Service) ImportPath(ctx context.Context, path string) (report *Report, err error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}

	startedAt := time.Now()
	log.Infof("master-data import started: path=%s", path)
	defer func() {
		duration := time.Since(startedAt)
		if err != nil {
			log.Errorf("master-data import stopped: path=%s status=failed duration=%s err=%v", path, duration, err)
			return
		}
		if report == nil {
			log.Infof("master-data import stopped: path=%s status=completed duration=%s created=0 updated=0 skipped=0", path, duration)
			return
		}
		log.Infof(
			"master-data import stopped: path=%s status=completed duration=%s created=%d updated=%d skipped=%d",
			path,
			duration,
			report.Created,
			report.Updated,
			report.Skipped,
		)
	}()

	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}

	report = &Report{}
	if !info.IsDir() {
		fileReport, err := s.importFile(ctx, path)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, fileReport)
		report.Created += fileReport.Created
		report.Updated += fileReport.Updated
		report.Skipped += fileReport.Skipped
		return report, nil
	}

	files, err := collectImportFilesFromPath(path)
	if err != nil {
		return nil, err
	}

	for _, filePath := range files {
		fileReport, err := s.importFile(ctx, filePath)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, fileReport)
		report.Created += fileReport.Created
		report.Updated += fileReport.Updated
		report.Skipped += fileReport.Skipped
	}
	return report, nil
}

// ImportPaths imports multiple directories or files in deterministic order and
// merges the per-file report into one operator-facing summary.
func (s *Service) ImportPaths(ctx context.Context, paths []string) (*Report, error) {
	report := &Report{Files: make([]FileReport, 0)}
	// Walk each sortimportpaths(paths) entry in order so validation and side effects stay scoped to the current element.
	for _, path := range sortImportPaths(paths) {
		fileReport, err := s.ImportPath(ctx, path)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, fileReport.Files...)
		report.Created += fileReport.Created
		report.Updated += fileReport.Updated
		report.Skipped += fileReport.Skipped
	}
	return report, nil
}

// ImportFS imports one file or one directory from the provided fs.FS in
// lexical order. It is used by packaged binaries that embed module seed data.
func (s *Service) ImportFS(ctx context.Context, fsys iofs.FS, root string) (report *Report, err error) {
	if s == nil {
		log.Errorf("masterdata: service is nil")
		return nil, fmt.Errorf("masterdata: service is nil")
	}

	root = cleanFSPath(root)
	startedAt := time.Now()
	log.Infof("master-data import started: path=%s", root)
	defer func() {
		duration := time.Since(startedAt)
		if err != nil {
			log.Errorf("master-data import stopped: path=%s status=failed duration=%s err=%v", root, duration, err)
			return
		}
		if report == nil {
			log.Infof("master-data import stopped: path=%s status=completed duration=%s created=0 updated=0 skipped=0", root, duration)
			return
		}
		log.Infof(
			"master-data import stopped: path=%s status=completed duration=%s created=%d updated=%d skipped=%d",
			root,
			duration,
			report.Created,
			report.Updated,
			report.Skipped,
		)
	}()

	report = &Report{}
	files, err := collectImportFilesFromFS(fsys, root)
	if err != nil {
		return nil, err
	}
	for _, filePath := range files {
		fileReport, err := s.importFSFile(ctx, fsys, filePath)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, fileReport)
		report.Created += fileReport.Created
		report.Updated += fileReport.Updated
		report.Skipped += fileReport.Skipped
	}
	return report, nil
}

// ImportFSRoots imports multiple embedded directories or files in deterministic
// order and merges the per-file report into one operator-facing summary.
func (s *Service) ImportFSRoots(ctx context.Context, fsys iofs.FS, roots []string) (*Report, error) {
	report := &Report{Files: make([]FileReport, 0)}
	// Walk each sortimportpaths(roots) entry in order so validation and side effects stay scoped to the current element.
	for _, root := range sortImportPaths(roots) {
		fileReport, err := s.ImportFS(ctx, fsys, root)
		if err != nil {
			return nil, err
		}
		report.Files = append(report.Files, fileReport.Files...)
		report.Created += fileReport.Created
		report.Updated += fileReport.Updated
		report.Skipped += fileReport.Skipped
	}
	return report, nil
}

func (s *Service) importFile(ctx context.Context, path string) (FileReport, error) {
	records, err := loadImportRecords(path)
	if err != nil {
		return FileReport{}, err
	}

	log.Infof("master-data import file started: path=%s records=%d", path, len(records))
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
	log.Infof(
		"master-data import file completed: path=%s created=%d updated=%d skipped=%d",
		path,
		report.Created,
		report.Updated,
		report.Skipped,
	)
	return report, nil
}

// bidDirectModels lists models whose own bid column is authoritative for
// master-data import identity, bypassing the sys.model.data external-id
// catalog entirely (2026-08-01, user-confirmed scope: only models whose seed
// data already carried a redundant bid value alongside external_id). Every
// other importable model keeps using ImportRecord.ExternalID/sys.model.data
// unchanged — most have no bid column at all.
var bidDirectModels = map[string]bool{
	models.ResGroupModelName: true,
	models.SysRuleModelName:  true,
}

func (s *Service) importRecord(ctx context.Context, record ImportRecord) (string, error) {
	modelName := normalizeMasterDataModelName(record.Model)
	if modelName == "" {
		return "", s.recordError(record, "model is required")
	}

	modelMeta, ok := s.runtime.LookupModel(modelName)
	if !ok {
		return "", s.recordError(record, "unknown model %q", modelName)
	}

	values, err := s.resolveRecordValues(ctx, modelMeta, record.Data)
	if err != nil {
		return "", s.recordError(record, "%v", err)
	}

	// bidDirectModels match on their own bid column, never sys.model.data.
	if bidDirectModels[modelName] {
		return s.importRecordByBID(ctx, modelName, record, values)
	}

	if strings.TrimSpace(record.ExternalID) == "" {
		return "", s.recordError(record, "external id is required so sys.model.data stays authoritative")
	}

	existing, found, err := s.lookupExternalID(ctx, strings.TrimSpace(record.ExternalID))
	if err != nil {
		return "", s.recordError(record, "%v", err)
	}
	staleRowID := ""
	runtimeCtx := importRuntimeContext(ctx)

	if found {
		retiredModel := false
		if normalizeMasterDataModelName(existing.Model) != modelName {
			// An external id owned by a model that is no longer registered (e.g. sys.module.dashboard,
			// replaced by ui.view type=dashboard) is stale metadata: let this import re-own it instead
			// of aborting startup. A registered, different model is still a genuine id clash.
			if _, registered := s.runtime.LookupModel(normalizeMasterDataModelName(existing.Model)); registered {
				return "", s.recordError(record, "external id %q belongs to model %q, not %q", record.ExternalID, existing.Model, modelName)
			}
			retiredModel = true
		}
		targetExists := false
		if !retiredModel {
			var err error
			targetExists, err = s.targetRecordExists(ctx, modelName, existing.ModelId)
			if err != nil {
				return "", s.recordError(record, "%v", err)
			}
		}
		if !targetExists {
			log.Warnf(
				"master-data import found stale external id metadata: bid=%s model=%s model_id=%s row_id=%s",
				record.ExternalID,
				modelName,
				existing.ModelId,
				existing.RowID,
			)
			staleRowID = existing.RowID
			found = false
		}
	}

	// No live row behind the external id: a model with a natural key adopts the existing row with
	// the same key instead of creating a duplicate (e.g. a workflow step recreated by canvas save).
	if !found {
		matchedID, err := s.findImportMatch(ctx, modelName, values)
		if err != nil {
			return "", s.recordError(record, "%v", err)
		}
		if matchedID != "" {
			log.Infof("master-data import adopted existing row by natural key: external_id=%s model=%s target_id=%s",
				record.ExternalID, modelName, matchedID)
			existing = bidTarget{RowID: staleRowID, ModelId: matchedID}
			found = true
		}
	}

	if found {
		effectiveForceUpdate := record.ForceUpdate || s.forceOverrideByBID
		if existing.NoUpdate && !effectiveForceUpdate {
			log.Debugf(
				"master-data record skipped: source=%s:%d action=%s model=%s external_id=%s reason=noupdate force_update=%t",
				record.SourcePath,
				record.SourceLine,
				recordAction(record),
				modelName,
				record.ExternalID,
				effectiveForceUpdate,
			)
			return "skipped", nil
		}
		values, err = s.stripAlreadySetSecrets(ctx, modelName, existing.ModelId, values)
		if err != nil {
			return "", s.recordError(record, "%v", err)
		}
		log.Debugf(
			"master-data record apply: source=%s:%d op=write action=%s model=%s external_id=%s target_id=%s force_update=%t fields=%s values=%s",
			record.SourcePath,
			record.SourceLine,
			recordAction(record),
			modelName,
			record.ExternalID,
			existing.ModelId,
			effectiveForceUpdate,
			recordValueKeys(values),
			recordValuesJSON(values),
		)
		if _, err := s.runtime.Write(runtimeCtx, modelName, []string{existing.ModelId}, values); err != nil {
			log.Errorf(
				"master-data record apply failed: source=%s:%d op=write action=%s model=%s external_id=%s target_id=%s force_update=%t fields=%s values=%s err=%v",
				record.SourcePath,
				record.SourceLine,
				recordAction(record),
				modelName,
				record.ExternalID,
				existing.ModelId,
				effectiveForceUpdate,
				recordValueKeys(values),
				recordValuesJSON(values),
				err,
			)
			return "", s.recordError(record, "update %s[%s]: %v", modelName, existing.ModelId, err)
		}
		if err := s.saveExternalID(ctx, existing.RowID, record, existing.ModelId); err != nil {
			return "", s.recordError(record, "%v", err)
		}
		return "updated", nil
	}

	log.Debugf(
		"master-data record apply: source=%s:%d op=create action=%s model=%s external_id=%s force_update=%t fields=%s values=%s",
		record.SourcePath,
		record.SourceLine,
		recordAction(record),
		modelName,
		record.ExternalID,
		record.ForceUpdate,
		recordValueKeys(values),
		recordValuesJSON(values),
	)
	created, err := s.runtime.Create(runtimeCtx, modelName, values)
	if err != nil {
		log.Errorf(
			"master-data record apply failed: source=%s:%d op=create action=%s model=%s external_id=%s force_update=%t fields=%s values=%s err=%v",
			record.SourcePath,
			record.SourceLine,
			recordAction(record),
			modelName,
			record.ExternalID,
			record.ForceUpdate,
			recordValueKeys(values),
			recordValuesJSON(values),
			err,
		)
		return "", s.recordError(record, "create %s: %v", modelName, err)
	}

	targetID, ok := runtimeStringID(created["id"])
	if !ok {
		return "", s.recordError(record, "model %q did not return a numeric id; import requires id-backed models", modelName)
	}

	// One-time secret reveal: a model whose create() response includes a
	// plaintext "client_secret" (sys.oauth_client's create RuntimeMethod,
	// generated server-side when the seed data omits it) only returns that
	// plaintext on this exact create call — it is bcrypt-hashed at rest and
	// unrecoverable afterward. Surface it in the startup log now, since a
	// static secret must never be committed into the seed JSON itself. Later
	// boots find this row via external_id and take the Write branch above
	// instead, so this only fires once, on first creation.
	logCreatedSecretIfPresent(modelName, record.ExternalID, created)

	if err := s.saveExternalID(ctx, staleRowID, record, targetID); err != nil {
		return "", s.recordError(record, "%v", err)
	}
	return "created", nil
}

// sensitiveImportFields are secret-like columns that master-data reimport
// must never clobber once a real value is already stored. Seed files only
// ever carry placeholder/dev values (e.g. base.users' "admin"/"base1"
// passwords in modules/base/data/11_init.users.json), and action_reinit
// replays every record's fields verbatim — so writing these fields
// unconditionally on every reimport would silently reset a live, rotated
// credential back to its seed default. They're still written normally on
// first create, and on update when the stored value is still empty.
var sensitiveImportFields = []string{"password", "api_key", "client_secret", "access_token", "refresh_token"}

// stripAlreadySetSecrets drops sensitive fields from values when the target
// record already has a non-empty stored value for them, so a reimport only
// ever sets a secret on first creation or while it is still unset.
func (s *Service) stripAlreadySetSecrets(ctx context.Context, modelName, targetID string, values orm.RecordMap) (orm.RecordMap, error) {
	var present []string
	for _, field := range sensitiveImportFields {
		if _, ok := values[field]; ok {
			present = append(present, field)
		}
	}
	if len(present) == 0 {
		return values, nil
	}
	rows, err := s.runtime.SearchRead(ctx, modelName, orm.RecordMap{"id": idFilterValue(targetID)}, present)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return values, nil
	}
	current := rows[0]
	for _, field := range present {
		if strings.TrimSpace(stringValue(current[field])) != "" {
			delete(values, field)
		}
	}
	return values, nil
}

// logCreatedSecretIfPresent notes that a freshly created record came back with a
// one-time plaintext secret ("client_secret", sys.oauth_client's reveal-once-on-create
// convention). The secret itself is never logged (journals and log shippers keep it);
// an admin issues a usable one with the record's generate_client_secret action.
func logCreatedSecretIfPresent(modelName, externalID string, created orm.RecordMap) {
	secret, ok := created["client_secret"].(string)
	if !ok || secret == "" {
		return
	}
	log.Warnf(
		"master-data seeded a new credential; its generated secret is not logged, run generate_client_secret on it to obtain one: model=%s external_id=%s client_id=%v",
		modelName, externalID, created["client_id"],
	)
}

// importRecordByBID imports one record for a bidDirectModels model, matching
// on the model's own bid column directly instead of the sys.model.data
// external-id catalog. There is no separate catalog row to keep in sync —
// bid is a normal field, already present in values from resolveRecordValues.
func (s *Service) importRecordByBID(ctx context.Context, modelName string, record ImportRecord, values orm.RecordMap) (string, error) {
	bidValue := strings.TrimSpace(stringValue(values["bid"]))
	if bidValue == "" {
		return "", s.recordError(record, "bid is required so %s stays authoritative", modelName)
	}

	runtimeCtx := importRuntimeContext(ctx)
	target, found, err := s.lookupByBID(ctx, modelName, bidValue)
	if err != nil {
		return "", s.recordError(record, "%v", err)
	}

	if found {
		existingID := target.ModelId
		effectiveForceUpdate := record.ForceUpdate || s.forceOverrideByBID
		if record.NoUpdate && !effectiveForceUpdate {
			log.Debugf(
				"master-data record skipped: source=%s:%d action=%s model=%s bid=%s reason=noupdate force_update=%t",
				record.SourcePath,
				record.SourceLine,
				recordAction(record),
				modelName,
				bidValue,
				effectiveForceUpdate,
			)
			return "skipped", nil
		}
		strippedValues, err := s.stripAlreadySetSecrets(ctx, modelName, existingID, values)
		if err != nil {
			return "", s.recordError(record, "%v", err)
		}
		values = strippedValues
		log.Debugf(
			"master-data record apply: source=%s:%d op=write action=%s model=%s bid=%s target_id=%s force_update=%t fields=%s values=%s",
			record.SourcePath,
			record.SourceLine,
			recordAction(record),
			modelName,
			bidValue,
			existingID,
			effectiveForceUpdate,
			recordValueKeys(values),
			recordValuesJSON(values),
		)
		if _, err := s.runtime.Write(runtimeCtx, modelName, []string{existingID}, values); err != nil {
			log.Errorf(
				"master-data record apply failed: source=%s:%d op=write action=%s model=%s bid=%s target_id=%s force_update=%t fields=%s values=%s err=%v",
				record.SourcePath,
				record.SourceLine,
				recordAction(record),
				modelName,
				bidValue,
				existingID,
				effectiveForceUpdate,
				recordValueKeys(values),
				recordValuesJSON(values),
				err,
			)
			return "", s.recordError(record, "update %s[%s]: %v", modelName, existingID, err)
		}
		return "updated", nil
	}

	log.Debugf(
		"master-data record apply: source=%s:%d op=create action=%s model=%s bid=%s force_update=%t fields=%s values=%s",
		record.SourcePath,
		record.SourceLine,
		recordAction(record),
		modelName,
		bidValue,
		record.ForceUpdate,
		recordValueKeys(values),
		recordValuesJSON(values),
	)
	if _, err := s.runtime.Create(runtimeCtx, modelName, values); err != nil {
		log.Errorf(
			"master-data record apply failed: source=%s:%d op=create action=%s model=%s bid=%s force_update=%t fields=%s values=%s err=%v",
			record.SourcePath,
			record.SourceLine,
			recordAction(record),
			modelName,
			bidValue,
			record.ForceUpdate,
			recordValueKeys(values),
			recordValuesJSON(values),
			err,
		)
		return "", s.recordError(record, "create %s: %v", modelName, err)
	}
	return "created", nil
}

// resolveRecordValues resolves the record values handled by this package.
func (s *Service) resolveRecordValues(ctx context.Context, model *orm.ModelMetadata, input orm.RecordMap) (orm.RecordMap, error) {
	if model == nil {
		log.Errorf("model metadata is required")
		return nil, fmt.Errorf("model metadata is required")
	}

	out := make(orm.RecordMap, len(input))
	for key, value := range input {
		field, ok := lookupField(model, key)
		if !ok {
			if strings.TrimSpace(key) == "id" {
				continue
			}
			log.Errorf("unknown field %q on model %q", key, model.Name)
			return nil, fmt.Errorf("unknown field %q on model %q", key, model.Name)
		}

		resolved, err := s.resolveFieldValue(ctx, field, value)
		if err != nil {
			log.Errorf("%s.%s: %v", model.Name, field.Name, err)
			return nil, fmt.Errorf("%s.%s: %w", model.Name, field.Name, err)
		}
		out[field.Name] = resolved
	}
	return out, nil
}

// resolveFieldValue resolves the field value handled by this package.
func (s *Service) resolveFieldValue(ctx context.Context, field orm.FieldMetadata, value any) (any, error) {
	if value == nil {
		return nil, nil
	}

	switch field.Kind {
	case orm.FieldKindManyToOne, orm.FieldKindOneToOne:
		return s.resolveReferenceValue(ctx, field.Relation, value)
	case orm.FieldKindManyToMany, orm.FieldKindOneToMany:
		return s.resolveReferenceList(ctx, field.Relation, value)
	default:
		return value, nil
	}
}

// resolveReferenceValue resolves the reference value handled by this package.
func (s *Service) resolveReferenceValue(ctx context.Context, relationModel string, value any) (any, error) {
	if id, ok := runtimeInt64(value); ok {
		return id, nil
	}

	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" {
		return nil, nil
	}
	if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return parsed, nil
	}
	// Preserve backward compatibility for legacy seed data that still stores a
	// locale code such as "en_US" for res.users.lang_id while the API layer now
	// exposes the field as a many2one to tbl.lang.
	if strings.TrimSpace(relationModel) == models.ResLangModelName {
		rows, err := s.runtime.SearchRead(
			ctx,
			models.ResLangModelName,
			orm.RecordMap{"code": raw, "active": true},
			[]string{"id"},
		)
		if err != nil {
			return nil, err
		}
		if len(rows) > 0 {
			if id, ok := runtimeInt64(rows[0]["id"]); ok {
				return id, nil
			}
		}
	}

	// bidDirectModels resolve relation references against their own bid
	// column directly, never through sys.model.data (mirrors importRecordByBID).
	if bidDirectModels[strings.TrimSpace(relationModel)] {
		target, found, err := s.lookupByBID(ctx, relationModel, raw)
		if err != nil {
			return nil, err
		}
		if !found {
			log.Errorf("bid %q was not found on model %q", raw, relationModel)
			return nil, fmt.Errorf("bid %q was not found on model %q", raw, relationModel)
		}
		parsed, err := strconv.ParseInt(target.ModelId, 10, 64)
		if err != nil {
			log.Errorf("bid %q on model %q resolved to a non-numeric id", raw, relationModel)
			return nil, fmt.Errorf("bid %q on model %q resolved to a non-numeric id", raw, relationModel)
		}
		return parsed, nil
	}

	target, found, err := s.lookupExternalID(ctx, raw)
	if err != nil {
		return nil, err
	}
	if !found {
		log.Errorf("external id %q was not found", raw)
		return nil, fmt.Errorf("external id %q was not found", raw)
	}
	if strings.TrimSpace(relationModel) != "" && target.Model != strings.TrimSpace(relationModel) {
		log.Errorf("external id %q points to model %q, expected %q", raw, target.Model, relationModel)
		return nil, fmt.Errorf("external id %q points to model %q, expected %q", raw, target.Model, relationModel)
	}

	parsed, err := strconv.ParseInt(strings.TrimSpace(target.ModelId), 10, 64)
	if err != nil {
		log.Errorf("external id %q resolved to non-numeric id %q", raw, target.ModelId)
		return nil, fmt.Errorf("external id %q resolved to non-numeric id %q", raw, target.ModelId)
	}
	return parsed, nil
}

// resolveReferenceList resolves the reference list handled by this package.
func (s *Service) resolveReferenceList(ctx context.Context, relationModel string, value any) (any, error) {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			resolved, err := s.resolveReferenceValue(ctx, relationModel, item)
			if err != nil {
				return nil, err
			}
			out = append(out, fmt.Sprint(resolved))
		}
		return out, nil
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			resolved, err := s.resolveReferenceValue(ctx, relationModel, item)
			if err != nil {
				return nil, err
			}
			out = append(out, fmt.Sprint(resolved))
		}
		return out, nil
	default:
		return value, nil
	}
}

// lookupExternalID looks up the external id handled by this package.
// lookupByBID looks up a bidDirectModels record directly on its own table,
// bypassing the sys.model.data external-id catalog entirely.
func (s *Service) lookupByBID(ctx context.Context, modelName, bidValue string) (bidTarget, bool, error) {
	bidValue = strings.TrimSpace(bidValue)
	if bidValue == "" {
		return bidTarget{}, false, nil
	}
	rows, err := s.runtime.SearchRead(ctx, modelName, orm.RecordMap{"bid": bidValue}, []string{"id"})
	if err != nil {
		return bidTarget{}, false, err
	}
	if len(rows) == 0 {
		return bidTarget{}, false, nil
	}
	id := strings.TrimSpace(fmt.Sprint(rows[0]["id"]))
	return bidTarget{RowID: id, Model: modelName, ModelId: id}, true, nil
}

func (s *Service) lookupExternalID(ctx context.Context, externalID string) (bidTarget, bool, error) {
	externalID = strings.TrimSpace(externalID)
	if externalID == "" {
		return bidTarget{}, false, nil
	}
	// Walk each externalidlookupcandidates(externalid) entry in order so validation and side effects stay scoped to the current element.
	for _, candidate := range externalIDLookupCandidates(externalID) {
		if cached, ok := s.bids[candidate]; ok {
			s.bids[externalID] = cached
			return cached, true, nil
		}

		rows, err := s.runtime.SearchRead(
			ctx,
			models.SysModelDataModelName,
			orm.RecordMap{"complete_name": candidate},
			[]string{"id", "model", "model_id", "noupdate", "reference"},
		)
		if err != nil {
			return bidTarget{}, false, err
		}
		if len(rows) == 0 {
			continue
		}

		target := bidTarget{
			RowID:     strings.TrimSpace(fmt.Sprint(rows[0]["id"])),
			Model:     normalizeMasterDataModelName(fmt.Sprint(rows[0]["model"])),
			ModelId:   strings.TrimSpace(fmt.Sprint(rows[0]["model_id"])),
			NoUpdate:  boolValue(rows[0]["noupdate"]),
			Reference: strings.TrimSpace(fmt.Sprint(rows[0]["reference"])),
		}
		s.bids[candidate] = target
		s.bids[externalID] = target
		return target, true, nil
	}
	return bidTarget{}, false, nil
}

func (s *Service) saveExternalID(ctx context.Context, rowID string, record ImportRecord, targetID string) error {
	externalID := strings.TrimSpace(record.ExternalID)
	if externalID == "" {
		return nil
	}

	moduleName, xmlName := externalIDParts(record)
	values := orm.RecordMap{
		"name":          xmlName,
		"complete_name": externalID,
		"model":         strings.TrimSpace(record.Model),
		"module":        moduleName,
		"model_id":      targetID,
		"noupdate":      record.NoUpdate,
		"reference":     strings.TrimSpace(record.Model) + "," + targetID,
	}

	if strings.TrimSpace(rowID) == "" {
		created, err := s.runtime.Create(ctx, models.SysModelDataModelName, values)
		if err != nil {
			return err
		}
		rowID = strings.TrimSpace(fmt.Sprint(created["id"]))
	} else {
		if _, err := s.runtime.Write(ctx, models.SysModelDataModelName, []string{rowID}, values); err != nil {
			return err
		}
	}

	s.bids[externalID] = bidTarget{
		RowID:     rowID,
		Model:     strings.TrimSpace(record.Model),
		ModelId:   targetID,
		NoUpdate:  record.NoUpdate,
		Reference: strings.TrimSpace(record.Model) + "," + targetID,
	}
	return nil
}

// findImportMatch returns the id of the one existing row of modelName whose registered import match
// fields equal values, or "" when the model has no match fields, a field is unset, or zero or
// several rows match (several: let create run and its own uniqueness rule report the conflict).
func (s *Service) findImportMatch(ctx context.Context, modelName string, values orm.RecordMap) (string, error) {
	fields := orm.ImportMatchFields(modelName)
	if len(fields) == 0 {
		return "", nil
	}
	filter := make(orm.RecordMap, len(fields))
	for _, field := range fields {
		value, ok := values[field]
		if !ok || value == nil || strings.TrimSpace(fmt.Sprint(value)) == "" {
			return "", nil
		}
		filter[field] = value
	}
	rows, err := s.runtime.SearchRead(ctx, modelName, filter, []string{"id"})
	if err != nil {
		return "", fmt.Errorf("match existing %s by %s: %w", modelName, strings.Join(fields, ", "), err)
	}
	if len(rows) != 1 {
		return "", nil
	}
	id, _ := runtimeStringID(rows[0]["id"])
	return id, nil
}

func (s *Service) targetRecordExists(ctx context.Context, modelName, resID string) (bool, error) {
	modelName = normalizeMasterDataModelName(modelName)
	resID = strings.TrimSpace(resID)
	if modelName == "" || resID == "" {
		return false, nil
	}
	modelMeta, ok := s.runtime.LookupModel(modelName)
	if !ok {
		// Stale sys.model.data rows can still point at retired logical names
		// during bootstrap. Treat them as missing so one import pass can heal
		// the metadata instead of aborting startup.
		log.Warnf("master-data target model is no longer registered: model=%s model_id=%s", modelName, resID)
		return false, nil
	}
	if strings.TrimSpace(modelMeta.StorageName) == "" {
		filter := orm.RecordMap{"id": idFilterValue(resID)}
		rows, err := s.runtime.SearchRead(ctx, modelName, filter, []string{"id"})
		if err != nil {
			return false, err
		}
		return len(rows) > 0, nil
	}

	db := orm.BunDBFromContext(ctx)
	if db == nil {
		filter := orm.RecordMap{"id": idFilterValue(resID)}
		rows, err := s.runtime.SearchRead(ctx, modelName, filter, []string{"id"})
		if err != nil {
			return false, err
		}
		return len(rows) > 0, nil
	}

	query := db.NewSelect().
		TableExpr("? AS t", bun.Ident(modelMeta.StorageName)).
		ColumnExpr("1").
		Where("t.id = ?", idFilterValue(resID)).
		Limit(1)
	var marker int
	if err := query.Scan(ctx, &marker); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (s *Service) recordError(record ImportRecord, format string, args ...any) error {
	log.Errorf("%s:%d: %s", record.SourcePath, record.SourceLine, fmt.Sprintf(format, args...))
	return fmt.Errorf("%s:%d: %s", record.SourcePath, record.SourceLine, fmt.Sprintf(format, args...))
}

// DeleteReport summarizes what a module uninstall removed, per target model.
type DeleteReport struct {
	Module  string         `json:"module"`
	Deleted map[string]int `json:"deleted"`
}

// deletableRow is one row this module owns, ordered so later-created rows
// (more likely to reference earlier ones via FK) are deleted first.
type deletableRow struct {
	Model string
	ID    string
	rowID string // sys.model.data catalog row id, empty for bidDirectModels
}

// DeleteModuleData removes every record a module seeded through sys.model.data
// (and, for bidDirectModels, through its own bid column), then drops the
// sys.model.data catalog rows themselves. Deletion runs in reverse discovery
// order as a best-effort dependency heuristic; it stops at the first error so
// a failed uninstall never silently leaves the module half-removed.
func (s *Service) DeleteModuleData(ctx context.Context, moduleName string) (*DeleteReport, error) {
	moduleName = strings.TrimSpace(moduleName)
	if moduleName == "" {
		return nil, fmt.Errorf("module name is required")
	}

	rows, err := s.moduleOwnedRows(ctx, moduleName)
	if err != nil {
		return nil, err
	}

	report := &DeleteReport{Module: moduleName, Deleted: make(map[string]int)}
	// Delete children before the parents that likely created them by walking
	// the rows in reverse discovery order.
	for i := len(rows) - 1; i >= 0; i-- {
		row := rows[i]
		if _, ok := s.runtime.LookupModel(row.Model); !ok {
			// The owning model is already unregistered (module models unloaded
			// before data cleanup); there is nothing to unlink through the
			// runtime, so skip it and still drop the catalog row below.
			log.Warnf("master-data uninstall: skip unregistered model %s#%s (module %s)", row.Model, row.ID, moduleName)
			continue
		}
		if _, err := s.runtime.Unlink(ctx, row.Model, []string{row.ID}); err != nil {
			return report, fmt.Errorf("delete %s#%s (module %s): %w", row.Model, row.ID, moduleName, err)
		}
		report.Deleted[row.Model]++
	}

	catalogIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		if row.rowID != "" {
			catalogIDs = append(catalogIDs, row.rowID)
		}
	}
	if len(catalogIDs) > 0 {
		if _, err := s.runtime.Unlink(ctx, models.SysModelDataModelName, catalogIDs); err != nil {
			return report, fmt.Errorf("delete sys.model.data catalog rows for module %s: %w", moduleName, err)
		}
	}
	return report, nil
}

// moduleOwnedRows collects every row a module seeded: sys.model.data-cataloged
// rows first (in catalog id order), then bidDirectModels rows whose bid is
// namespaced under this module (e.g. "moduleName.some_group").
func (s *Service) moduleOwnedRows(ctx context.Context, moduleName string) ([]deletableRow, error) {
	catalogRows, err := s.runtime.SearchRead(
		ctx,
		models.SysModelDataModelName,
		orm.RecordMap{"module": moduleName},
		[]string{"id", "model", "model_id"},
	)
	if err != nil {
		return nil, err
	}

	rows := make([]deletableRow, 0, len(catalogRows))
	for _, catalogRow := range catalogRows {
		rows = append(rows, deletableRow{
			Model: normalizeMasterDataModelName(fmt.Sprint(catalogRow["model"])),
			ID:    strings.TrimSpace(fmt.Sprint(catalogRow["model_id"])),
			rowID: strings.TrimSpace(fmt.Sprint(catalogRow["id"])),
		})
	}

	prefix := moduleName + "."
	for modelName := range bidDirectModels {
		bidRows, err := s.runtime.SearchRead(ctx, modelName, orm.RecordMap{}, []string{"id", "bid"})
		if err != nil {
			return nil, err
		}
		for _, bidRow := range bidRows {
			bid := strings.TrimSpace(fmt.Sprint(bidRow["bid"]))
			if !strings.HasPrefix(bid, prefix) {
				continue
			}
			rows = append(rows, deletableRow{
				Model: modelName,
				ID:    strings.TrimSpace(fmt.Sprint(bidRow["id"])),
			})
		}
	}
	return rows, nil
}

func recordAction(record ImportRecord) string {
	action := strings.TrimSpace(record.Action)
	if action == "" {
		return "c"
	}
	return action
}

func recordValueKeys(values orm.RecordMap) string {
	if len(values) == 0 {
		return "[]"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return "[" + strings.Join(keys, ",") + "]"
}

func recordValuesJSON(values orm.RecordMap) string {
	if len(values) == 0 {
		return "{}"
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return fmt.Sprintf("<unmarshalable:%v>", err)
	}
	return string(raw)
}
