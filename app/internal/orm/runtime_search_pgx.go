package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime search pgx, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `runtimeSearchDocumentsPGX`, `runtimeSearchCountPGX`, `runtimeSelectColumn`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/uptrace/bun"
)

type runtimeSelectColumn struct {
	FieldName string
	Column    string
	Kind      FieldKind
	// Translate marks a jsonb-backed translated column (SRS-F-021) so the
	// SELECT list projects the language-resolved text instead of the envelope.
	Translate bool
}

func (h *Handler[T]) runtimeSearchDocumentsPGX(ctx context.Context, directFilter RecordMap, domain DomainNode, order string, offset, limit int, nameNeedle string) ([]RecordMap, error) {
	if h == nil || h.repo == nil {
		log.Errorf("orm: pgx runtime search is not initialized")
		return nil, fmt.Errorf("orm: pgx runtime search is not initialized")
	}
	return h.store().searchDocumentsPGX(ctx, h.runtimeSelectColumns(), directFilter, domain, order, offset, limit, nameNeedle)
}

// runtimeStore.searchDocumentsPGX is the shared implementation behind
// Handler[T].runtimeSearchDocumentsPGX and dynamicRuntimeExecutor's equivalent
// (see runtimeStore.selectQuery's doc comment for why this split exists).
func (s runtimeStore) searchDocumentsPGX(ctx context.Context, selectColumns []runtimeSelectColumn, directFilter RecordMap, domain DomainNode, order string, offset, limit int, nameNeedle string) ([]RecordMap, error) {
	meta, tableName, typ := s.meta, s.table, s.typ
	pool := runtimeSearchPGXPool(ctx)
	if meta == nil || pool == nil {
		log.Errorf("orm: pgx runtime search is not initialized")
		return nil, fmt.Errorf("orm: pgx runtime search is not initialized")
	}

	sql, args, err := buildRuntimeSearchSQL(meta, tableName, typ, directFilter, domain, order, offset, limit, nameNeedle, selectColumns, runtimeRequestLang(ctx))
	if err != nil {
		return nil, err
	}

	rows, err := pool.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]RecordMap, 0, 32)
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return nil, err
		}
		if len(values) != len(selectColumns) {
			log.Errorf("orm: runtime search column mismatch for %s: got %d values, expected %d", meta.Name, len(values), len(selectColumns))
			return nil, fmt.Errorf("orm: runtime search column mismatch for %s: got %d values, expected %d", meta.Name, len(values), len(selectColumns))
		}

		record := make(RecordMap, len(selectColumns))
		for index, column := range selectColumns {
			record[column.FieldName] = normalizeRuntimeScannedValue(column.Kind, values[index])
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (h *Handler[T]) runtimeSearchCountPGX(ctx context.Context, directFilter RecordMap, domain DomainNode, nameNeedle string) (int64, error) {
	if h == nil || h.repo == nil {
		log.Errorf("orm: pgx runtime count is not initialized")
		return 0, fmt.Errorf("orm: pgx runtime count is not initialized")
	}
	return h.store().searchCountPGX(ctx, directFilter, domain, nameNeedle)
}

// runtimeStore.searchCountPGX is the shared implementation behind
// Handler[T].runtimeSearchCountPGX and dynamicRuntimeExecutor's equivalent.
func (s runtimeStore) searchCountPGX(ctx context.Context, directFilter RecordMap, domain DomainNode, nameNeedle string) (int64, error) {
	meta, tableName, typ := s.meta, s.table, s.typ
	pool := runtimeSearchPGXPool(ctx)
	if meta == nil || pool == nil {
		log.Errorf("orm: pgx runtime count is not initialized")
		return 0, fmt.Errorf("orm: pgx runtime count is not initialized")
	}

	sql, args, err := buildRuntimeSearchCountSQL(meta, tableName, typ, directFilter, domain, nameNeedle, runtimeRequestLang(ctx))
	if err != nil {
		return 0, err
	}

	var count int64
	if err := pool.QueryRow(ctx, sql, args...).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

func runtimeSearchPGXPool(ctx context.Context) *pgxpool.Pool {
	if env := EnvironmentFromContext(ctx); env != nil {
		switch env.DB().(type) {
		case bun.Tx, *bun.Tx:
			// Model-driven writes and recomputes must stay on the Bun-owned
			// transaction so reads see in-flight changes consistently.
			return nil
		}
		if pool := ResolvePGXPool(env.DBName()); pool != nil {
			return pool
		}
	}
	return GetPGXPool()
}

func (h *Handler[T]) runtimeSelectColumns() []runtimeSelectColumn {
	if h == nil {
		return nil
	}
	if len(h.runtimeSelectColumnsCache) > 0 {
		return h.runtimeSelectColumnsCache
	}
	return buildRuntimeSelectColumns(h.metadata)
}

func buildRuntimeSelectColumns(meta *ModelMetadata) []runtimeSelectColumn {
	if meta == nil {
		return nil
	}
	columns := make([]runtimeSelectColumn, 0, len(meta.Fields))
	seen := map[string]struct{}{}
	for _, field := range meta.Fields {
		if !runtimeFieldBackedBySQLColumn(field) {
			continue
		}
		column := strings.TrimSpace(field.BunColumn)
		if column == "" {
			column = strings.TrimSpace(field.Name)
		}
		if column == "" {
			continue
		}
		if _, exists := seen[field.Name]; exists {
			continue
		}
		seen[field.Name] = struct{}{}
		columns = append(columns, runtimeSelectColumn{
			FieldName: field.Name,
			Column:    column,
			Kind:      field.Kind,
			Translate: field.Translate,
		})
	}
	return columns
}

// buildRuntimeSearchSQL builds the shared pgx runtime search SQL. It moved off
// Handler[T] onto free functions parameterized by *ModelMetadata + tableName
// (see runtimeStore.selectQuery's doc comment for why) so
// dynamicRuntimeExecutor (published custom models) gets the same pgx
// fast-search path as static models instead of a slower bun-only fallback.
func buildRuntimeSearchSQL(meta *ModelMetadata, tableName string, typ reflect.Type, directFilter RecordMap, domain DomainNode, order string, offset, limit int, nameNeedle string, selectColumns []runtimeSelectColumn, lang string) (string, []any, error) {
	if meta == nil {
		log.Errorf("orm: runtime handler is not initialized")
		return "", nil, fmt.Errorf("orm: runtime handler is not initialized")
	}
	if len(selectColumns) == 0 {
		log.Errorf("orm: no SQL-backed runtime fields are available for %s", meta.Name)
		return "", nil, fmt.Errorf("orm: no SQL-backed runtime fields are available for %s", meta.Name)
	}

	alias := runtimeQueryRootAlias(typ, tableName)
	baseAlias := quoteIdent(alias)
	selectList := make([]string, 0, len(selectColumns))
	for _, column := range selectColumns {
		// Translated columns project the language-resolved text (SRS-F-021) so
		// the serialization contract stays a plain string per SRS-F-004.
		if column.Translate {
			selectList = append(selectList, fmt.Sprintf("%s AS %s", translatedProjectionSQL(baseAlias, column.Column, lang), quoteIdent(column.Column)))
			continue
		}
		selectList = append(selectList, fmt.Sprintf("%s.%s AS %s", baseAlias, quoteIdent(column.Column), quoteIdent(column.Column)))
	}

	fromSQL, clauses, args, err := buildRuntimeSearchFromAndWhereSQL(meta, tableName, typ, directFilter, domain, nameNeedle, baseAlias, lang)
	if err != nil {
		return "", nil, err
	}

	var builder strings.Builder
	builder.WriteString("SELECT ")
	builder.WriteString(strings.Join(selectList, ", "))
	builder.WriteString(fromSQL)
	if len(clauses) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(clauses, " AND "))
	}

	orderClause, err := runtimeOrderSQL(meta, typ, baseAlias, order, lang)
	if err != nil {
		return "", nil, err
	}
	if orderClause != "" {
		builder.WriteString(" ORDER BY ")
		builder.WriteString(orderClause)
	}
	if offset > 0 {
		args = append(args, offset)
		builder.WriteString(" OFFSET ")
		builder.WriteString(pgxPlaceholder(len(args)))
	}
	if limit > 0 {
		args = append(args, limit)
		builder.WriteString(" LIMIT ")
		builder.WriteString(pgxPlaceholder(len(args)))
	}
	return builder.String(), args, nil
}

// buildRuntimeSearchCountSQL builds the shared pgx runtime count SQL.
func buildRuntimeSearchCountSQL(meta *ModelMetadata, tableName string, typ reflect.Type, directFilter RecordMap, domain DomainNode, nameNeedle, lang string) (string, []any, error) {
	if meta == nil {
		log.Errorf("orm: runtime handler is not initialized")
		return "", nil, fmt.Errorf("orm: runtime handler is not initialized")
	}

	alias := runtimeQueryRootAlias(typ, tableName)
	baseAlias := quoteIdent(alias)
	fromSQL, clauses, args, err := buildRuntimeSearchFromAndWhereSQL(meta, tableName, typ, directFilter, domain, nameNeedle, baseAlias, lang)
	if err != nil {
		return "", nil, err
	}

	var builder strings.Builder
	builder.WriteString("SELECT COUNT(*)")
	builder.WriteString(fromSQL)
	if len(clauses) > 0 {
		builder.WriteString(" WHERE ")
		builder.WriteString(strings.Join(clauses, " AND "))
	}
	return builder.String(), args, nil
}

// buildRuntimeSearchFromAndWhereSQL builds the shared pgx FROM/WHERE fragment.
func buildRuntimeSearchFromAndWhereSQL(meta *ModelMetadata, tableName string, typ reflect.Type, directFilter RecordMap, domain DomainNode, nameNeedle, baseAlias, lang string) (string, []string, []any, error) {
	if meta == nil {
		log.Errorf("orm: runtime handler is not initialized")
		return "", nil, nil, fmt.Errorf("orm: runtime handler is not initialized")
	}

	clauses := make([]string, 0, len(directFilter)+2)
	args := make([]any, 0, len(directFilter)*2)
	for key, value := range directFilter {
		// Non-stored computed fields have no SQL column, so filtering on one must
		// fail with an explicit contract error (SRS-F-018) instead of leaking a
		// PostgreSQL undefined-column error to the caller.
		if field, ok := runtimeLookupField(meta, key); ok && strings.TrimSpace(field.Compute) != "" && !field.Stored {
			log.Errorf("orm: field %q on %s is a non-stored computed field and is not searchable", field.Name, meta.Name)
			return "", nil, nil, fmt.Errorf("orm: field %q on %s is a non-stored computed field and is not searchable", field.Name, meta.Name)
		}
		column := quoteIdent(resolveFilterColumnName(typ, key))
		left := baseAlias + "." + column
		// Translated columns are jsonb: direct filters must compare the
		// language-resolved text with en fallback (SRS-F-021).
		if field, ok := runtimeLookupField(meta, key); ok && field.Translate {
			left = translatedColumnSQL(baseAlias, translatedFieldColumn(field), lang)
		}
		if sliceValue, ok := normalizeSliceFilterValue(value); ok {
			if len(sliceValue) == 0 {
				clauses = append(clauses, "1 = 0")
				continue
			}
			placeholders := make([]string, 0, len(sliceValue))
			for _, item := range sliceValue {
				args = append(args, item)
				placeholders = append(placeholders, pgxPlaceholder(len(args)))
			}
			clauses = append(clauses, fmt.Sprintf("%s IN (%s)", left, strings.Join(placeholders, ", ")))
			continue
		}
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf("%s = %s", left, pgxPlaceholder(len(args))))
	}

	if domain.Kind != "" && domain.Kind != DomainKindTrue {
		whereSQL, whereArgs, err := domain.CompileWithBaseAliasLang(meta, baseAlias, lang)
		if err != nil {
			return "", nil, nil, err
		}
		whereSQL, err = translateBunPlaceholdersToPGX(whereSQL, len(args))
		if err != nil {
			return "", nil, nil, err
		}
		// Parenthesize the compiled domain before joining: clauses are combined
		// with AND, and an unwrapped OR-domain would rebind precedence
		// ("id = $1 AND a OR b" reads as "(id = $1 AND a) OR b"), which breaks
		// record-rule enforcement and can leak rows across company scopes.
		clauses = append(clauses, "("+whereSQL+")")
		args = append(args, whereArgs...)
	}

	if strings.TrimSpace(nameNeedle) != "" && strings.TrimSpace(meta.RecName) != "" && meta.RecName != "id" {
		field, ok := runtimeLookupField(meta, meta.RecName)
		if ok {
			column := strings.TrimSpace(field.BunColumn)
			if column == "" {
				column = strings.TrimSpace(field.Name)
			}
			args = append(args, likePattern(nameNeedle))
			// A translated rec-name matches against the language-resolved text so
			// name_search behaves identically to what the caller reads back.
			if field.Translate {
				clauses = append(clauses, fmt.Sprintf("LOWER(%s) LIKE LOWER(%s)", translatedColumnSQL(baseAlias, translatedFieldColumn(field), lang), pgxPlaceholder(len(args))))
			} else {
				clauses = append(clauses, fmt.Sprintf("LOWER(CAST(%s.%s AS text)) LIKE LOWER(%s)", baseAlias, quoteIdent(column), pgxPlaceholder(len(args))))
			}
		}
	}

	fromSQL := fmt.Sprintf(" FROM %s AS %s", quoteIdent(tableName), baseAlias)
	return fromSQL, clauses, args, nil
}

func runtimeOrderSQL(meta *ModelMetadata, typ reflect.Type, baseAlias, order, lang string) (string, error) {
	order = strings.TrimSpace(order)
	if order == "" {
		return "", nil
	}
	partsOut := make([]string, 0, 4)
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
		// Translated columns order by the language-resolved text so sorting
		// matches the serialized value the caller sees (SRS-F-021).
		if field, ok := runtimeLookupField(meta, fieldName); ok && field.Translate {
			partsOut = append(partsOut, fmt.Sprintf("%s %s", translatedColumnSQL(baseAlias, translatedFieldColumn(field), lang), direction))
			continue
		}
		partsOut = append(partsOut, fmt.Sprintf("%s.%s %s", baseAlias, quoteIdent(column), direction))
	}
	return strings.Join(partsOut, ", "), nil
}

func pgxPlaceholder(index int) string {
	return "$" + strconv.Itoa(index)
}

func translateBunPlaceholdersToPGX(sql string, offset int) (string, error) {
	if strings.TrimSpace(sql) == "" {
		return sql, nil
	}
	var builder strings.Builder
	inSingle := false
	inDouble := false
	nextIndex := offset + 1
	for i := 0; i < len(sql); i++ {
		ch := sql[i]
		switch ch {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
			builder.WriteByte(ch)
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
			builder.WriteByte(ch)
		case '?':
			if inSingle || inDouble {
				builder.WriteByte(ch)
				continue
			}
			builder.WriteString(pgxPlaceholder(nextIndex))
			nextIndex++
		default:
			builder.WriteByte(ch)
		}
	}
	if inSingle || inDouble {
		log.Errorf("orm: unbalanced quotes while translating SQL placeholders")
		return "", fmt.Errorf("orm: unbalanced quotes while translating SQL placeholders")
	}
	return builder.String(), nil
}

func normalizeRuntimeScannedValue(kind FieldKind, value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case []byte:
		switch kind {
		case FieldKindJSON:
			if len(typed) == 0 {
				return map[string]any{}
			}
			var out any
			if err := json.Unmarshal(typed, &out); err == nil {
				return out
			}
		case FieldKindText, FieldKindSelection, FieldKindDate, FieldKindDateTime:
			return string(typed)
		}
		return string(typed)
	case time.Time:
		return typed
	}
	return value
}
