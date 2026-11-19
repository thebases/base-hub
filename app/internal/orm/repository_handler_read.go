package orm

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

func (h *Handler[T]) RuntimeSearch(ctx context.Context, filter RecordMap) ([]any, error) {
	records, err := h.runtimeSearchRecords(ctx, filter, nil)
	if err != nil {
		return nil, err
	}

	out := make([]any, 0, len(records))
	for _, record := range records {
		out = append(out, extractModelID(record))
	}
	return out, nil
}

func (h *Handler[T]) RuntimeSearchCount(ctx context.Context, filter RecordMap) (int64, error) {
	return h.runtimeSearchCount(ctx, filter)
}

func (h *Handler[T]) RuntimeRead(ctx context.Context, ids []string, fields []string) ([]RecordMap, error) {
	filter := RecordMap{"__ids": append([]string(nil), ids...)}
	if len(ids) > 0 {
		if len(ids) == 1 {
			filter["id"] = toInt64(ids[0])
		} else {
			filter["id"] = idsToFilterValues(ids)
		}
	}
	return h.runtimeSearchRecords(ctx, filter, fields)
}

func (h *Handler[T]) RuntimeSearchRead(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	return h.runtimeSearchRecords(ctx, filter, fields)
}

func (h *Handler[T]) toDocument(payload any) (T, error) {
	var zero T
	if payload == nil {
		log.Errorf("orm: payload is required")
		return zero, fmt.Errorf("orm: payload is required")
	}

	if typed, ok := payload.(T); ok {
		return typed, nil
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return zero, err
	}

	var document T
	if err := json.Unmarshal(raw, &document); err != nil {
		return zero, err
	}
	return document, nil
}

func (h *Handler[T]) recordsToMaps(records []T) ([]RecordMap, error) {
	out := make([]RecordMap, 0, len(records))
	for _, record := range records {
		converted, err := toRecordMap(record)
		if err != nil {
			return nil, err
		}
		out = append(out, converted)
	}
	return out, nil
}

func (h *Handler[T]) runtimeEnvironment() *Environment {
	if h == nil || h.repo == nil {
		return nil
	}
	return NewEnvironment(h.repo.db, 0, nil, false)
}

func (h *Handler[T]) runtimeSearchRecords(ctx context.Context, filter RecordMap, fields []string) ([]RecordMap, error) {
	directFilter, domain, order, offset, limit, nameNeedle, ids, err := h.runtimeSearchOptions(filter)
	if err != nil {
		return nil, err
	}
	var (
		rows   []RecordMap
		runErr error
	)
	if runtimeSearchPGXPool(ctx) != nil {
		rows, runErr = h.runtimeSearchDocumentsPGX(ctx, directFilter, domain, order, offset, limit, nameNeedle)
		if runErr != nil {
			return nil, runErr
		}
	} else {
		records, fallbackErr := h.runtimeSearchDocuments(ctx, directFilter, domain, order, offset, limit, nameNeedle)
		if fallbackErr != nil {
			return nil, fallbackErr
		}
		rows, runErr = h.recordsToMaps(records)
		if runErr != nil {
			return nil, runErr
		}
	}
	if len(ids) > 0 {
		rows = reorderRecordsByIDs(rows, ids)
	}
	// Collapse any translated jsonb envelope that survived the resolved SQL
	// projection (alternate scan paths) so clients always receive plain
	// language-resolved strings (SRS-F-021).
	resolveTranslatedRecordValues(h.metadata, rows, runtimeRequestLang(ctx))
	rows, runErr = h.enrichRuntimeRecords(ctx, rows, fields)
	if runErr != nil {
		return nil, runErr
	}
	return filterFields(rows, fields), nil
}

func (h *Handler[T]) runtimeSearchCount(ctx context.Context, filter RecordMap) (int64, error) {
	directFilter, domain, _, _, _, nameNeedle, _, err := h.runtimeSearchOptions(filter)
	if err != nil {
		return 0, err
	}
	if runtimeSearchPGXPool(ctx) != nil {
		return h.runtimeSearchCountPGX(ctx, directFilter, domain, nameNeedle)
	}
	query, err := h.runtimeSelectQuery(ctx, directFilter, domain, nameNeedle)
	if err != nil {
		return 0, err
	}
	count, err := query.Count(ctx)
	return int64(count), err
}

func (h *Handler[T]) runtimeSearchOptions(filter RecordMap) (RecordMap, DomainNode, string, int, int, string, []string, error) {
	return runtimeSearchOptionsCore(h.metadata, filter)
}

// runtimeSearchOptionsCore is the shared implementation behind
// Handler[T].runtimeSearchOptions and dynamicRuntimeExecutor's equivalent
// (see runtimeStore in runtime_store.go for why this split
// exists) -- it only ever needed *ModelMetadata, never a compile-time T.
func runtimeSearchOptionsCore(meta *ModelMetadata, filter RecordMap) (RecordMap, DomainNode, string, int, int, string, []string, error) {
	direct := RecordMap{}
	var domain DomainNode
	var order string
	var offset int
	var limit int
	var nameNeedle string
	var ids []string
	for key, value := range filter {
		switch key {
		case "__domain":
			domain, _ = value.(DomainNode)
		case "__order":
			order, _ = value.(string)
		case "__offset":
			offset = int(toInt64(value))
		case "__limit":
			limit = int(toInt64(value))
		case "__name":
			nameNeedle, _ = value.(string)
		case "__ids":
			ids = stringSliceArg(value)
		default:
			direct[key] = value
		}
	}
	if domain.Kind == "" {
		domain = TrueDomain()
	}
	// Normalize domain values against field metadata once here so every search
	// path shares the same coercion rules before SQL or fallback evaluation.
	normalizedDomain, err := normalizeDomainForModel(meta, domain)
	if err != nil {
		return nil, DomainNode{}, "", 0, 0, "", nil, err
	}
	domain = normalizedDomain
	if order == "" {
		if meta != nil {
			order = meta.DefaultOrder
		}
	}
	return direct, domain, order, offset, limit, nameNeedle, ids, nil
}

func reorderRecordsByIDs(records []RecordMap, ids []string) []RecordMap {
	if len(ids) == 0 || len(records) < 2 {
		return records
	}

	// Index requested ids once, then rebuild the output in caller order so
	// readback stays O(n) instead of sorting the full record slice.
	requested := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		requested[id] = struct{}{}
	}
	grouped := make(map[string][]RecordMap, len(records))
	extras := make([]RecordMap, 0)
	for _, record := range records {
		recordID, _ := runtimeStringID(record["id"])
		if _, ok := requested[recordID]; !ok {
			extras = append(extras, record)
			continue
		}
		grouped[recordID] = append(grouped[recordID], record)
	}

	ordered := make([]RecordMap, 0, len(records))
	for _, id := range ids {
		ordered = append(ordered, grouped[id]...)
	}
	return append(ordered, extras...)
}

func (h *Handler[T]) runtimeSearchDocuments(ctx context.Context, directFilter RecordMap, domain DomainNode, order string, offset, limit int, nameNeedle string) ([]T, error) {
	var records []T
	query, err := h.runtimeSelectQueryForModel(ctx, directFilter, domain, nameNeedle, &records)
	if err != nil {
		return nil, err
	}
	// Translated jsonb columns cannot scan into the struct's string fields, so
	// models with translated fields enumerate an explicit projection that
	// resolves each translated column to the environment language in SQL
	// (SRS-F-021). Non-translated models keep Bun's default column list.
	if modelHasTranslatedFields(h.metadata) {
		lang := runtimeRequestLang(ctx)
		baseAlias := quoteIdent(runtimeQueryRootAlias(reflect.TypeOf((*T)(nil)).Elem(), h.repo.tableName))
		for _, field := range h.metadata.Fields {
			if !runtimeFieldBackedBySQLColumn(field) {
				continue
			}
			column := translatedFieldColumn(field)
			if column == "" {
				continue
			}
			if field.Translate {
				query = query.ColumnExpr(translatedProjectionSQL(baseAlias, column, lang) + " AS " + quoteIdent(column))
				continue
			}
			query = query.Column(column)
		}
	}
	if err := h.applyRuntimeOrder(ctx, query, order); err != nil {
		return nil, err
	}
	if offset > 0 {
		query.Offset(offset)
	}
	if limit > 0 {
		query.Limit(limit)
	}
	if err := query.Scan(ctx); err != nil {
		log.Warnf("orm: runtime search documents failed for %s sql=%s err=%v", h.metadata.Name, query.String(), err)
		return nil, err
	}
	return records, nil
}

func (h *Handler[T]) runtimeSelectQuery(ctx context.Context, directFilter RecordMap, domain DomainNode, nameNeedle string) (*bun.SelectQuery, error) {
	return h.runtimeSelectQueryForModel(ctx, directFilter, domain, nameNeedle, (*T)(nil))
}

func (h *Handler[T]) runtimeSelectQueryForModel(ctx context.Context, directFilter RecordMap, domain DomainNode, nameNeedle string, model any) (*bun.SelectQuery, error) {
	if h == nil || h.repo == nil {
		log.Errorf("orm: BunRepository is not initialized")
		return nil, fmt.Errorf("orm: BunRepository is not initialized")
	}
	return h.store().selectQuery(ctx, runtimeDB(ctx, h), directFilter, domain, nameNeedle, model)
}

// runtimeStore.selectQuery builds the shared runtime SELECT query. It
// only ever needed *ModelMetadata plus a reflect.Type for tag introspection,
// not a compile-time T, so it is shared between Handler[T] (static models,
// which pass T's own reflect.Type explicitly so this never depends on
// whether the metadata went through registration) and dynamicRuntimeExecutor
// (published custom models) instead of being duplicated per implementation.
func (s runtimeStore) selectQuery(ctx context.Context, db bun.IDB, directFilter RecordMap, domain DomainNode, nameNeedle string, model any) (*bun.SelectQuery, error) {
	meta, tableName, typ := s.meta, s.table, s.typ
	if meta == nil || db == nil {
		log.Errorf("orm: BunRepository is not initialized")
		return nil, fmt.Errorf("orm: BunRepository is not initialized")
	}
	query := db.NewSelect().Model(model)
	query = applyModelTable(query, tableName, typ)
	query = excludeRuntimeVirtualColumns(query, meta)
	// Translated columns are jsonb: a plain equality/IN filter against them
	// must compare the language-resolved text, so those keys leave the generic
	// filter path and compile through translatedColumnSQL (SRS-F-021).
	if modelHasTranslatedFields(meta) {
		lang := runtimeRequestLang(ctx)
		baseAlias := quoteIdent(runtimeQueryRootAlias(typ, tableName))
		remaining := RecordMap{}
		for key, value := range directFilter {
			field, ok := runtimeLookupField(meta, key)
			if !ok || !field.Translate {
				remaining[key] = value
				continue
			}
			expr := translatedColumnSQL(baseAlias, translatedFieldColumn(field), lang)
			if sliceValue, ok := normalizeSliceFilterValue(value); ok {
				if len(sliceValue) == 0 {
					query = query.Where("1 = 0")
					continue
				}
				query = query.Where(expr+" IN (?)", bun.In(sliceValue))
				continue
			}
			query = query.Where(expr+" = ?", value)
		}
		directFilter = remaining
	}
	query = applyFilters(query, directFilter, typ)
	if domain.Kind != "" && domain.Kind != DomainKindTrue {
		// Compile runtime domains against the active Bun alias so the generated
		// WHERE fragment matches ModelTableExpr(table AS alias). The environment
		// language rides along so translated-field conditions resolve correctly.
		sql, args, err := domain.CompileWithBaseAliasLang(meta, quoteIdent(runtimeQueryRootAlias(typ, tableName)), runtimeRequestLang(ctx))
		if err != nil {
			return nil, err
		}
		query = query.Where(sql, args...)
	}
	if strings.TrimSpace(nameNeedle) != "" && strings.TrimSpace(meta.RecName) != "" && meta.RecName != "id" {
		field, ok := runtimeLookupField(meta, meta.RecName)
		if ok {
			column := field.BunColumn
			if column == "" {
				column = field.Name
			}
			// A translated rec-name needle must match the language-resolved text,
			// not the raw jsonb envelope.
			if field.Translate {
				expr := translatedColumnSQL(quoteIdent(runtimeQueryRootAlias(typ, tableName)), translatedFieldColumn(field), runtimeRequestLang(ctx))
				query = query.Where("LOWER("+expr+") LIKE LOWER(?)", likePattern(nameNeedle))
			} else {
				query = query.Where("LOWER(CAST(? AS text)) LIKE LOWER(?)", bun.Ident(column), likePattern(nameNeedle))
			}
		}
	}
	return query, nil
}

func excludeRuntimeVirtualColumns(query *bun.SelectQuery, meta *ModelMetadata) *bun.SelectQuery {
	if query == nil || meta == nil {
		return query
	}
	columns := make([]string, 0, len(meta.Fields))
	for _, field := range meta.Fields {
		if !runtimeFieldBackedBySQLColumn(field) {
			column := strings.TrimSpace(field.BunColumn)
			if column == "" {
				column = strings.TrimSpace(field.Name)
			}
			if column != "" {
				columns = append(columns, column)
			}
		}
	}
	if len(columns) == 0 {
		return query
	}
	return query.ExcludeColumn(columns...)
}

func runtimeFieldBackedBySQLColumn(field FieldMetadata) bool {
	if !field.Stored {
		return false
	}
	switch field.Kind {
	case FieldKindOneToMany, FieldKindManyToMany:
		return false
	default:
		return true
	}
}

func runtimeQueryRootAlias(typ reflect.Type, tableName string) string {
	alias := strings.TrimSpace(modelTableAlias(typ))
	if alias != "" {
		return alias
	}
	return strings.TrimSpace(tableName)
}

func (h *Handler[T]) applyRuntimeOrder(ctx context.Context, query *bun.SelectQuery, order string) error {
	if h == nil || h.repo == nil {
		return nil
	}
	return h.store().applyOrder(ctx, query, order)
}

// runtimeStore.applyOrder is the shared implementation behind
// Handler[T].applyRuntimeOrder and dynamicRuntimeExecutor's equivalent (see
// runtimeStore.selectQuery's doc comment for why this split exists).
func (s runtimeStore) applyOrder(ctx context.Context, query *bun.SelectQuery, order string) error {
	meta, tableName, typ := s.meta, s.table, s.typ
	order = strings.TrimSpace(order)
	if order == "" {
		return nil
	}
	// Walk each split(order, ",") entry in order so validation and side effects stay scoped to the current element.
	for _, clause := range strings.Split(order, ",") {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		parts := strings.Fields(clause)
		fieldName := parts[0]
		direction := "ASC"
		if len(parts) > 1 && strings.EqualFold(parts[1], "desc") {
			direction = "DESC"
		}
		// A leading "-" is the reference module's own order-string shorthand
		// for descending (e.g. "-date"), distinct from this repo's own
		// "field desc" form above — accept both rather than let a "-field"
		// order string silently resolve to a bogus literal column name.
		if strings.HasPrefix(fieldName, "-") {
			fieldName = strings.TrimPrefix(fieldName, "-")
			direction = "DESC"
		}
		column := resolveFilterColumnName(typ, fieldName)
		// Translated columns must order by the language-resolved text, not the
		// raw jsonb value, so sorting matches what the caller actually sees
		// (SRS-F-021 acceptance criterion).
		if field, ok := runtimeLookupField(meta, fieldName); ok && field.Translate {
			baseAlias := quoteIdent(runtimeQueryRootAlias(typ, tableName))
			query = query.OrderExpr(translatedColumnSQL(baseAlias, translatedFieldColumn(field), runtimeRequestLang(ctx)) + " " + direction)
			continue
		}
		query = query.OrderExpr("? "+direction, bun.Ident(column))
	}
	return nil
}

// cloneRecordMap clones the record map handled by this package.
func cloneRecordMap(values RecordMap) RecordMap {
	if values == nil {
		return nil
	}
	cloned := make(RecordMap, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}
