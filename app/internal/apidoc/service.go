package apidoc

// File overview:
// - Responsibility: builds API documentation structures for service, translating runtime metadata into discoverable docs and OpenAPI-friendly payloads.
// - Key symbols: `NewService`, `WithConfig`, `AccessChecker`, `Service`.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// AccessChecker decides whether one model should be visible to the caller.
// Returning a non-nil error hides the model from index/openapi responses and
// fails explicit detail requests.
type AccessChecker func(ctx context.Context, model orm.ModelMetadata) error

// Service turns runtime registry metadata into API documents.
type Service struct {
	config        Config
	listModels    func() []orm.ModelMetadata
	accessChecker AccessChecker
}

type Option func(*Service)

// NewService constructs and returns the service handled by this package.
func NewService(opts ...Option) *Service {
	svc := &Service{
		config:     Config{Title: "API Documentation", Version: "dev", BasePath: "/"},
		listModels: orm.RegisteredModels,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(svc)
		}
	}
	return svc
}

func WithConfig(config Config) Option {
	return func(svc *Service) {
		svc.config = config
	}
}

func WithModelSource(source func() []orm.ModelMetadata) Option {
	return func(svc *Service) {
		if source != nil {
			svc.listModels = source
		}
	}
}

func WithAccessChecker(checker AccessChecker) Option {
	return func(svc *Service) {
		svc.accessChecker = checker
	}
}

func (s *Service) Index(ctx context.Context) (IndexDocument, error) {
	models, err := s.accessibleModels(ctx)
	if err != nil {
		return IndexDocument{}, err
	}

	moduleSet := make(map[string]struct{}, len(models))
	docModels := make([]IndexModel, 0, len(models))
	for _, model := range models {
		moduleName := moduleNameForModel(model.Name)
		moduleSet[moduleName] = struct{}{}

		fields := make(map[string]IndexField, len(model.Fields))
		for _, field := range model.Fields {
			fields[field.Name] = IndexField{String: fieldLabel(field)}
		}

		docModels = append(docModels, IndexModel{
			Model:   model.Name,
			Name:    modelLabel(model),
			Fields:  fields,
			Methods: publicMethodNames(model.RuntimeMethods),
		})
	}

	modules := make([]string, 0, len(moduleSet))
	for moduleName := range moduleSet {
		modules = append(modules, moduleName)
	}
	sort.Strings(modules)
	sort.Slice(docModels, func(i, j int) bool {
		return docModels[i].Model < docModels[j].Model
	})
	return IndexDocument{Modules: modules, Models: docModels}, nil
}

func (s *Service) Model(ctx context.Context, modelName string) (ModelDocument, error) {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		log.Errorf("apidoc: model name is required")
		return ModelDocument{}, fmt.Errorf("apidoc: model name is required")
	}

	model, ok := s.lookupModel(modelName)
	if !ok {
		log.Errorf("apidoc: model %q is not registered", modelName)
		return ModelDocument{}, fmt.Errorf("apidoc: model %q is not registered", modelName)
	}
	if err := s.checkAccess(ctx, model); err != nil {
		return ModelDocument{}, err
	}

	lang := LanguageFromContext(ctx)
	fields := make(map[string]FieldDocument, len(model.Fields))
	moduleName := moduleNameForModel(model.Name)
	for _, field := range model.Fields {
		fields[field.Name] = fieldDocument(field, moduleName)
	}
	return ModelDocument{
		Model:   model.Name,
		Name:    modelLabel(model),
		Doc:     optionalString(model.Description),
		DocFile: descriptionFileBody(lang, model.DescriptionFile),
		Fields:  fields,
		Methods: publicMethodMap(model, s.config.BasePath, lang),
	}, nil
}

func (s *Service) OpenAPI(ctx context.Context) (map[string]any, error) {
	models, err := s.accessibleModels(ctx)
	if err != nil {
		return nil, err
	}
	return buildOpenAPI(s.config, models, LanguageFromContext(ctx)), nil
}

func (s *Service) accessibleModels(ctx context.Context) ([]orm.ModelMetadata, error) {
	models := s.listModels()
	sort.Slice(models, func(i, j int) bool {
		return models[i].Name < models[j].Name
	})

	out := make([]orm.ModelMetadata, 0, len(models))
	for _, model := range models {
		if err := s.checkAccess(ctx, model); err != nil {
			continue
		}
		out = append(out, model)
	}
	return out, nil
}

// lookupModel looks up the model handled by this package.
func (s *Service) lookupModel(name string) (orm.ModelMetadata, bool) {
	// Walk each listmodels() entry in order so validation and side effects stay scoped to the current element.
	for _, model := range s.listModels() {
		if model.Name == name {
			return model, true
		}
	}
	return orm.ModelMetadata{}, false
}

func (s *Service) checkAccess(ctx context.Context, model orm.ModelMetadata) error {
	if s.accessChecker == nil {
		return nil
	}
	return s.accessChecker(ctx, model)
}

func moduleNameForModel(modelName string) string {
	modelName = strings.TrimSpace(modelName)
	if modelName == "" {
		return "core"
	}
	parts := strings.Split(modelName, ".")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		return "core"
	}
	return parts[0]
}

func modelLabel(model orm.ModelMetadata) string {
	if strings.TrimSpace(model.Label) != "" {
		return model.Label
	}
	return model.Name
}

func fieldLabel(field orm.FieldMetadata) string {
	if strings.TrimSpace(field.Label) != "" {
		return field.Label
	}
	return field.Name
}

func fieldDocument(field orm.FieldMetadata, moduleName string) FieldDocument {
	return FieldDocument{
		Name:       field.Name,
		String:     fieldLabel(field),
		Type:       fieldTypeLabel(field),
		Kind:       field.Kind,
		Required:   field.Required,
		Readonly:   field.ReadOnly,
		Store:      field.Stored,
		Searchable: fieldSearchable(field),
		Sortable:   fieldSortable(field),
		Help:       strings.TrimSpace(field.Help),
		Default:    strings.TrimSpace(field.Default),
		Relation:   strings.TrimSpace(field.Relation),
		OnDelete:   strings.TrimSpace(field.OnDelete),
		Module:     moduleName,
		Selection:  append([]orm.SelectionOption(nil), field.Selection...),
		Related:    append([]string(nil), field.Related...),
		Depends:    append([]string(nil), field.Depends...),
		Compute:    strings.TrimSpace(field.Compute),
	}
}

func fieldTypeLabel(field orm.FieldMetadata) string {
	if strings.TrimSpace(field.Type) != "" {
		return field.Type
	}
	if field.Kind != "" {
		return string(field.Kind)
	}
	return "any"
}

func fieldSearchable(field orm.FieldMetadata) bool {
	switch field.Kind {
	case orm.FieldKindOneToMany, orm.FieldKindManyToMany:
		return false
	default:
		return true
	}
}

func fieldSortable(field orm.FieldMetadata) bool {
	switch field.Kind {
	case orm.FieldKindOneToMany, orm.FieldKindManyToMany, orm.FieldKindJSON:
		return false
	default:
		return field.Stored
	}
}

func optionalString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func normalizeBasePath(basePath string) string {
	basePath = strings.TrimSpace(basePath)
	if basePath == "" || basePath == "/" {
		return ""
	}
	if !strings.HasPrefix(basePath, "/") {
		basePath = "/" + basePath
	}
	return strings.TrimRight(basePath, "/")
}
