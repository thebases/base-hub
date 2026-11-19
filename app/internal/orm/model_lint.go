package orm

// File overview:
// - Responsibility: static model lint (plan T86, owner decision D8: a CI test runs it, boot never fails on it).
//   Checks the registered models for mistakes that registration itself tolerates silently.
// - Key symbols: `ValidateRegisteredModels`, `ModelLintFinding`, `RegisteredModelFindings`.

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
)

// Lint finding kinds. Tests allowlist findings by Kind + Model + Field.
const (
	LintRegistrationError   = "registration_error"
	LintUnknownRelation     = "unknown_relation"
	LintJSONBunMismatch     = "json_bun_mismatch"
	LintUnknownMetaKey      = "unknown_meta_key"
	LintUnknownHookField    = "unknown_hook_field"
	LintRedundantStorageTag = "redundant_storage_name"
)

// ModelLintFinding is one lint result. Field is empty for model-level findings.
type ModelLintFinding struct {
	Kind    string
	Model   string
	Field   string
	Message string
}

// String renders the finding as "kind model.field: message" for test output and allowlists.
func (f ModelLintFinding) String() string {
	target := f.Model
	if f.Field != "" {
		target += "." + f.Field
	}
	return f.Kind + " " + target + ": " + f.Message
}

// knownMetaKeys are the `meta:"..."` keys parseFieldMetaOptions consumers read
// (model_definition.go, model_registry.go). Anything else is silently ignored by
// registration, which is how `selection:` inside meta went unnoticed.
var knownMetaKeys = map[string]struct{}{
	"label": {}, "name": {}, "help": {}, "required": {}, "unique": {}, "index": {}, "indexed": {},
	"readonly": {}, "secret": {}, "stored": {}, "primary": {}, "kind": {}, "type": {}, "model": {},
	"inverse": {}, "relation_table": {}, "relation_column": {}, "relation_key": {}, "ondelete": {},
	"related": {}, "depends": {}, "compute": {}, "compute_sudo": {}, "translate": {},
	"use_sequence": {}, "sequence_code": {}, "filter": {},
}

// modelLintState keeps what registration saw but did not keep on the metadata:
// failed registrations (the model is simply missing afterwards) and redundant
// storage-name overrides (indistinguishable from the default once registered).
var modelLintState = struct {
	mu                 sync.Mutex
	registrationErrors map[string]error
	redundantStorage   map[string]struct{}
}{
	registrationErrors: map[string]error{},
	redundantStorage:   map[string]struct{}{},
}

func recordModelRegistrationError(name string, err error) {
	modelLintState.mu.Lock()
	defer modelLintState.mu.Unlock()
	modelLintState.registrationErrors[name] = err
}

// recordRedundantStorageName notes a spec whose explicit storage name equals the
// name-derived default, so `WithStorageName` on it can be deleted.
func recordRedundantStorageName(spec ModelSpec) {
	if spec.StorageNameFunc == nil || spec.Name == "" || spec.StorageNameFunc() != canonicalModelTableName(spec.Name) {
		return
	}
	modelLintState.mu.Lock()
	defer modelLintState.mu.Unlock()
	modelLintState.redundantStorage[spec.Name] = struct{}{}
}

// ValidateRegisteredModels joins every lint finding into one error (nil when clean).
func ValidateRegisteredModels() error {
	var errs []error
	for _, finding := range RegisteredModelFindings() {
		errs = append(errs, errors.New(finding.String()))
	}
	return errors.Join(errs...)
}

// RegisteredModelFindings lints the default registry and returns the findings in a stable order,
// so a test can subtract an allowlist from them.
func RegisteredModelFindings() []ModelLintFinding {
	models := RegisteredModels()
	var findings []ModelLintFinding
	findings = append(findings, registrationFindings()...)
	for i := range models {
		findings = append(findings, lintModelFields(&models[i])...)
		findings = append(findings, lintModelHooks(&models[i])...)
	}
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].String() < findings[j].String() })
	return findings
}

func registrationFindings() []ModelLintFinding {
	modelLintState.mu.Lock()
	defer modelLintState.mu.Unlock()
	var findings []ModelLintFinding
	for name, err := range modelLintState.registrationErrors {
		// A later successful registration of the same name supersedes the failure.
		if _, ok := LookupSharedModel(name); ok {
			continue
		}
		findings = append(findings, ModelLintFinding{Kind: LintRegistrationError, Model: name, Message: err.Error()})
	}
	for name := range modelLintState.redundantStorage {
		findings = append(findings, ModelLintFinding{Kind: LintRedundantStorageTag, Model: name,
			Message: "WithStorageName(" + canonicalModelTableName(name) + ") equals the default and can be dropped"})
	}
	return findings
}

// lintModelFields checks relation targets, json == bun column, and unknown meta keys.
func lintModelFields(model *ModelMetadata) []ModelLintFinding {
	var findings []ModelLintFinding
	for _, field := range model.Fields {
		if field.Relation != "" {
			if _, ok := LookupSharedModel(field.Relation); !ok {
				findings = append(findings, ModelLintFinding{Kind: LintUnknownRelation, Model: model.Name, Field: field.Name,
					Message: "relation target " + field.Relation + " is not registered"})
			}
		}
		if field.JSONName != "" && field.JSONName != "-" && field.BunColumn != "" && field.JSONName != field.BunColumn {
			findings = append(findings, ModelLintFinding{Kind: LintJSONBunMismatch, Model: model.Name, Field: field.Name,
				Message: fmt.Sprintf("json tag %q differs from bun column %q", field.JSONName, field.BunColumn)})
		}
	}
	if model.goReflectType != nil {
		findings = append(findings, lintMetaKeys(model.Name, model.goReflectType)...)
	}
	return findings
}

// lintMetaKeys walks the struct (embedded structs flattened, like registration does) and reports
// meta keys no consumer reads.
func lintMetaKeys(modelName string, typ reflect.Type) []ModelLintFinding {
	var findings []ModelLintFinding
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			if embedded := dereferenceType(field.Type); embedded.Kind() == reflect.Struct {
				findings = append(findings, lintMetaKeys(modelName, embedded)...)
			}
			continue
		}
		for key := range parseFieldMetaOptions(field) {
			if _, ok := knownMetaKeys[key]; ok {
				continue
			}
			name, _ := parseStructTag(field.Tag.Get("json"))
			findings = append(findings, ModelLintFinding{Kind: LintUnknownMetaKey, Model: modelName, Field: firstNonEmpty(name, field.Name),
				Message: "meta key " + key + " is not read by the registry"})
		}
	}
	return findings
}

// lintModelHooks checks that RegisterConstraint / RegisterOnchange field lists name real fields.
func lintModelHooks(model *ModelMetadata) []ModelLintFinding {
	known := make(map[string]struct{}, len(model.Fields))
	for _, field := range model.Fields {
		known[field.Name] = struct{}{}
	}
	runtimeHookRegistry.mu.RLock()
	defer runtimeHookRegistry.mu.RUnlock()
	var findings []ModelLintFinding
	for _, hook := range runtimeHookRegistry.constraints[model.Name] {
		findings = append(findings, unknownHookFields(model.Name, known, "RegisterConstraint", hook.fields)...)
	}
	for _, hook := range runtimeHookRegistry.onchanges[model.Name] {
		findings = append(findings, unknownHookFields(model.Name, known, "RegisterOnchange", hook.fields)...)
	}
	return findings
}

func unknownHookFields(modelName string, known map[string]struct{}, hook string, fields []string) []ModelLintFinding {
	var findings []ModelLintFinding
	for _, name := range fields {
		if _, ok := known[strings.TrimSpace(name)]; !ok {
			findings = append(findings, ModelLintFinding{Kind: LintUnknownHookField, Model: modelName, Field: name,
				Message: hook + " names a field the model does not have"})
		}
	}
	return findings
}
