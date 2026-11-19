package modelruntime

// File overview:
// - Responsibility: provides a service-facing facade over the ORM runtime so callers can execute metadata-driven model operations without importing the lower-level kernel directly.
// - Key symbols: `NewService`, `Execute`, `Request`, `Service`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// Request captures one generic runtime invocation so callers can use the
// metadata-driven dispatcher without binding themselves to one concrete model.
type Request struct {
	Model  string
	Method string
	IDs    []string
	Args   []any
	Kwargs map[string]any
}

// Service is the application-facing wrapper around the lower-level ORM runtime
// dispatcher. Keeping this boundary small makes it easier to move routes and
// orchestration code onto metadata-driven execution incrementally.
type Service struct{}

// NewService constructs and returns the service handled by this package.
func NewService() *Service {
	return &Service{}
}

// Execute performs one validated runtime method call and returns the raw method
// result for callers that need full flexibility.
func (s *Service) Execute(ctx context.Context, req Request) (any, error) {
	normalized := normalizeRequest(req)

	if err := validateRequest(normalized); err != nil {
		return nil, err
	}

	// Copy and normalize request inputs once at the service boundary so every
	// wrapper preserves caller ownership while runtime dispatch sees canonical names.
	return orm.ExecuteRuntimeMethod(
		ctx,
		normalized.Model,
		normalized.Method,
		normalized.IDs,
		normalized.Args,
		normalized.Kwargs,
	)
}

// FieldsGet exposes model field metadata through the same runtime contract used
// by future generic transports.
func (s *Service) FieldsGet(ctx context.Context, model string, fields []string) (map[string]orm.FieldMetadata, error) {
	return executeTyped[map[string]orm.FieldMetadata](ctx, s, Request{
		Model:  model,
		Method: "fields_get",
		Args:   []any{cloneStrings(fields)},
	})
}

// DefaultGet resolves runtime defaults for the requested fields so transports
// can bootstrap dynamic create forms without knowing model structs directly.
func (s *Service) DefaultGet(ctx context.Context, model string, fields []string) (map[string]any, error) {
	return executeTyped[map[string]any](ctx, s, Request{
		Model:  model,
		Method: "default_get",
		Kwargs: map[string]any{"fields": cloneStrings(fields)},
	})
}

func (s *Service) SearchRead(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
	return executeTyped[[]orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "search_read",
		Kwargs: map[string]any{
			"filter": cloneRecordMap(filter),
			"fields": cloneStrings(fields),
		},
	})
}

func (s *Service) NameSearch(ctx context.Context, model string, name string, filter orm.RecordMap, limit int) ([][2]any, error) {
	return executeTyped[[][2]any](ctx, s, Request{
		Model:  model,
		Method: "name_search",
		Args:   []any{name},
		Kwargs: map[string]any{
			"filter": cloneRecordMap(filter),
			"limit":  limit,
		},
	})
}

func (s *Service) NameGet(ctx context.Context, model string, ids []string) ([][2]any, error) {
	return executeTyped[[][2]any](ctx, s, Request{
		Model:  model,
		Method: "name_get",
		IDs:    cloneStrings(ids),
	})
}

func (s *Service) SearchFetch(ctx context.Context, model string, filter orm.RecordMap, fields []string) ([]orm.RecordMap, error) {
	return executeTyped[[]orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "search_fetch",
		Kwargs: map[string]any{
			"filter": cloneRecordMap(filter),
			"fields": cloneStrings(fields),
		},
	})
}

// ReadGroup reads the group handled by this package.
func (s *Service) ReadGroup(ctx context.Context, model string, filter orm.RecordMap, fields []string, groupBy []string) ([]orm.RecordMap, error) {
	return executeTyped[[]orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "read_group",
		Kwargs: map[string]any{
			"filter":  cloneRecordMap(filter),
			"fields":  cloneStrings(fields),
			"groupby": cloneStrings(groupBy),
		},
	})
}

// Read reads the read handled by this package.
func (s *Service) Read(ctx context.Context, model string, ids []string, fields []string) ([]orm.RecordMap, error) {
	return executeTyped[[]orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "read",
		IDs:    cloneStrings(ids),
		Kwargs: map[string]any{"fields": cloneStrings(fields)},
	})
}

// CopyData copies the data handled by this package.
func (s *Service) CopyData(ctx context.Context, model string, ids []string) ([]orm.RecordMap, error) {
	return executeTyped[[]orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "copy_data",
		IDs:    cloneStrings(ids),
	})
}

// Copy copies the copy handled by this package.
func (s *Service) Copy(ctx context.Context, model string, ids []string) (any, error) {
	return s.Execute(ctx, Request{
		Model:  model,
		Method: "copy",
		IDs:    cloneStrings(ids),
	})
}

func (s *Service) Exists(ctx context.Context, model string, ids []string) ([]any, error) {
	return executeTyped[[]any](ctx, s, Request{
		Model:  model,
		Method: "exists",
		IDs:    cloneStrings(ids),
	})
}

// Create creates the create handled by this package.
func (s *Service) Create(ctx context.Context, model string, values orm.RecordMap) (orm.RecordMap, error) {
	return executeTyped[orm.RecordMap](ctx, s, Request{
		Model:  model,
		Method: "create",
		Args:   []any{cloneRecordMap(values)},
	})
}

func (s *Service) Onchange(ctx context.Context, model string, values orm.RecordMap, changedFields []string, fieldSpec map[string]any) (map[string]any, error) {
	return executeTyped[map[string]any](ctx, s, Request{
		Model:  model,
		Method: "onchange",
		Args:   []any{cloneRecordMap(values), cloneStrings(changedFields)},
		Kwargs: map[string]any{"fields_spec": cloneMap(fieldSpec)},
	})
}

func (s *Service) ToggleActive(ctx context.Context, model string, ids []string) (bool, error) {
	return executeTyped[bool](ctx, s, Request{
		Model:  model,
		Method: "toggle_active",
		IDs:    cloneStrings(ids),
	})
}

func (s *Service) Write(ctx context.Context, model string, ids []string, values orm.RecordMap) (bool, error) {
	return executeTyped[bool](ctx, s, Request{
		Model:  model,
		Method: "write",
		IDs:    cloneStrings(ids),
		Args:   []any{cloneRecordMap(values)},
	})
}

func (s *Service) Unlink(ctx context.Context, model string, ids []string) (bool, error) {
	return executeTyped[bool](ctx, s, Request{
		Model:  model,
		Method: "unlink",
		IDs:    cloneStrings(ids),
	})
}

func (s *Service) Search(ctx context.Context, model string, filter orm.RecordMap) ([]any, error) {
	return executeTyped[[]any](ctx, s, Request{
		Model:  model,
		Method: "search",
		Kwargs: map[string]any{"filter": cloneRecordMap(filter)},
	})
}

func (s *Service) SearchCount(ctx context.Context, model string, filter orm.RecordMap) (int64, error) {
	return executeTyped[int64](ctx, s, Request{
		Model:  model,
		Method: "search_count",
		Kwargs: map[string]any{"filter": cloneRecordMap(filter)},
	})
}

func executeTyped[T any](ctx context.Context, svc *Service, req Request) (T, error) {
	var zero T
	modelName, methodName := requestNames(req)

	result, err := svc.Execute(ctx, req)
	if err != nil {
		return zero, err
	}

	typed, ok := result.(T)
	if !ok {
		log.Errorf(
			"modelruntime: %s on %s returned %T",
			methodName,
			modelName,
			result,
		)
		return zero, fmt.Errorf(
			"modelruntime: %s on %s returned %T",
			methodName,
			modelName,
			result,
		)
	}
	return typed, nil
}

func normalizeRequest(req Request) Request {
	modelName, methodName := requestNames(req)
	return Request{
		Model:  modelName,
		Method: methodName,
		IDs:    cloneStrings(req.IDs),
		Args:   cloneAnySlice(req.Args),
		Kwargs: cloneMap(req.Kwargs),
	}
}

func requestNames(req Request) (string, string) {
	return strings.TrimSpace(req.Model), strings.TrimSpace(req.Method)
}

// validateRequest validates the request handled by this package.
func validateRequest(req Request) error {
	if req.Model == "" {
		log.Errorf("modelruntime: model is required")
		return fmt.Errorf("modelruntime: model is required")
	}
	if req.Method == "" {
		log.Errorf("modelruntime: method is required")
		return fmt.Errorf("modelruntime: method is required")
	}
	return nil
}

func cloneStrings(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string(nil), in...)
}

func cloneAnySlice(in []any) []any {
	if in == nil {
		return nil
	}
	return append([]any(nil), in...)
}

// cloneRecordMap clones the record map handled by this package.
func cloneRecordMap(in orm.RecordMap) orm.RecordMap {
	if in == nil {
		return nil
	}

	out := make(orm.RecordMap, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
