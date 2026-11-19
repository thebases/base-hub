package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for repository bun, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `NewBunRepository`, `Insert`, `RecordMap`, `InsertResult`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"encoding/json"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
	"reflect"
	"strings"
	"time"
	"unicode"
)

type RecordMap map[string]any

type InsertResult struct {
	InsertedID   any
	Acknowledged bool
}

type UpdateResult struct {
	Acknowledged  bool
	MatchedCount  int64
	ModifiedCount int64
	UpsertedCount int64
	UpsertedID    any
}

type DeleteResult struct {
	Acknowledged bool
	DeletedCount int64
}

// BunRepository keeps the generic repository contract Bun-native.
type BunRepository[T any] struct {
	db        *bun.DB
	tableName string
	// typ mirrors T as a reflect.Type so the shared, non-generic bun* helpers
	// (also used by dynamicRepository for runtime-published models) can stay
	// single-implementation instead of forking per compile-time/dynamic type.
	typ reflect.Type
}

func NewBunRepository[T any](tableName string) *BunRepository[T] {
	// Repository instances bind to the shared Bun connection pool when one
	// exists. If the DB is not yet connected (e.g. during CLI model
	// registration before any database is opened) we still create the repo so
	// h.repo is never nil; db will be nil and the runtime will fall back to
	// the context-bound DB at call time.
	db := GetBunDB()
	if db == nil {
		log.Warnf("BunDB is not initialized")
	}
	return &BunRepository[T]{db: db, tableName: tableName, typ: reflect.TypeOf((*T)(nil)).Elem()}
}

func (r *BunRepository[T]) Insert(ctx context.Context, document T) (*InsertResult, error) {
	return r.insertWithDB(ctx, r.db, document)
}

// excludeColumns lets runtime callers keep columns the generic statement must
// not touch (translated jsonb columns, SRS-F-021) out of the INSERT entirely.
func (r *BunRepository[T]) insertWithDB(ctx context.Context, db bun.IDB, document T, excludeColumns ...string) (*InsertResult, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return nil, fmt.Errorf("BunRepository is not initialized")
	}
	if db == nil {
		// Fall back to the repository default when callers do not inject a
		// transaction-aware handle.
		db = r.db
	}
	return bunInsertWithDB(ctx, db, r.tableName, r.typ, &document, excludeColumns...)
}

// bunInsertWithDB implements one bun INSERT ... RETURNING id against a
// caller-owned document pointer. It is shared by BunRepository[T] (static,
// compile-time-known models) and dynamicRepository (runtime-published custom
// models) because the underlying logic never actually needed the compile-time
// type parameter -- only a reflect.Type for column/tag introspection, which
// both callers already have (T's reflect.Type, or the reflect.StructOf type
// synthesized for a published model).
func bunInsertWithDB(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, document any, excludeColumns ...string) (*InsertResult, error) {
	// Honor model lifecycle hooks before handing the record to Bun.
	callBeforeCreate(document)

	// Use Bun's native RETURNING support so inserted ids come back through the
	// active PostgreSQL/Bun path without driver-specific fallback queries.
	query := db.NewInsert().Model(document).ExcludeColumn(append([]string{"id"}, excludeColumns...)...).Returning("id")
	query = applyModelTable(query, tableName, typ)

	var insertedID int64
	if err := query.Scan(ctx, &insertedID); err != nil {
		return nil, err
	}
	// Reflect the generated id back into the caller-owned document shape so the
	// returned payload matches the inserted database row.
	assignModelID(document, insertedID)
	return &InsertResult{
		InsertedID:   extractModelID(document),
		Acknowledged: true,
	}, nil
}

func (r *BunRepository[T]) Count(ctx context.Context, filter RecordMap) (int64, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return 0, fmt.Errorf("BunRepository is not initialized")
	}
	return bunCount(ctx, r.db, r.tableName, r.typ, filter)
}

// bunCount is the shared, non-generic implementation behind BunRepository[T].Count
// and dynamicRepository.Count (see bunInsertWithDB's doc comment for why this
// split exists).
func bunCount(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, filter RecordMap) (int64, error) {
	// Build the count query against the model type so Bun can derive columns.
	query := db.NewSelect().Model(reflect.New(typ).Interface())
	query = applyModelTable(query, tableName, typ)
	query = applyFilters(query, filter, typ)

	count, err := query.Count(ctx)
	return int64(count), err
}

// Find finds the find handled by this package.
func (r *BunRepository[T]) Find(ctx context.Context, filter RecordMap) ([]T, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return nil, fmt.Errorf("BunRepository is not initialized")
	}

	var results []T
	// Scan into a slice so Bun materializes every matching row into the generic
	// record type chosen by the handler.
	query := r.db.NewSelect().Model(&results)
	query = applyModelTable(query, r.tableName, r.typ)
	query = applyFilters(query, filter, r.typ)

	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	return results, nil
}

// FindOne finds the one handled by this package.
func (r *BunRepository[T]) FindOne(ctx context.Context, filter RecordMap) (*T, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return nil, fmt.Errorf("BunRepository is not initialized")
	}

	var result T
	// Apply Limit(1) before scan to keep the query deterministic for callers
	// expecting one logical record.
	query := r.db.NewSelect().Model(&result).Limit(1)
	query = applyModelTable(query, r.tableName, r.typ)
	query = applyFilters(query, filter, r.typ)

	if err := query.Scan(ctx); err != nil {
		return nil, err
	}
	return &result, nil
}

func (r *BunRepository[T]) Update(ctx context.Context, filter RecordMap, update RecordMap) (*UpdateResult, error) {
	return r.updateWithDB(ctx, r.db, filter, update)
}

func (r *BunRepository[T]) updateWithDB(ctx context.Context, db bun.IDB, filter RecordMap, update RecordMap) (*UpdateResult, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return nil, fmt.Errorf("BunRepository is not initialized")
	}
	if db == nil {
		db = r.db
	}
	return bunUpdateWithDB(ctx, db, r.tableName, r.typ, filter, update)
}

// bunUpdateWithDB is the shared, non-generic implementation behind
// BunRepository[T].updateWithDB and dynamicRepository.updateWithDB.
func bunUpdateWithDB(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, filter RecordMap, update RecordMap) (*UpdateResult, error) {
	// Canonicalize logical field aliases before generating SQL so one payload
	// cannot assign the same physical column twice.
	update = sanitizeUpdateMap(typ, update)

	query := db.NewUpdate().Model(reflect.New(typ).Interface())
	query = applyModelTable(query, tableName, typ)
	for key, value := range update {
		// Generic map-based UPDATE statements bypass Bun's struct field type
		// mapping, so JSONB columns must be marshaled and cast explicitly or
		// scalar payloads like `true` hit PostgreSQL as plain booleans.
		if isJSONBUpdateColumn(typ, key) {
			jsonValue, err := marshalJSONBUpdateValue(value)
			if err != nil {
				return nil, err
			}
			if jsonValue == nil {
				query = query.Set("? = NULL", bun.Ident(key))
				continue
			}
			query = query.Set("? = ?::jsonb", bun.Ident(key), string(jsonValue))
			continue
		}
		// The update map is already normalized to storage columns, so bind each
		// canonical column exactly once.
		query = query.Set("? = ?", bun.Ident(key), value)
	}
	query = applyFilters(query, filter, typ)

	result, err := query.Exec(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return &UpdateResult{
		Acknowledged:  true,
		MatchedCount:  rows,
		ModifiedCount: rows,
	}, nil
}

func (r *BunRepository[T]) Delete(ctx context.Context, filter RecordMap) (*DeleteResult, error) {
	return r.deleteWithDB(ctx, r.db, filter)
}

func (r *BunRepository[T]) deleteWithDB(ctx context.Context, db bun.IDB, filter RecordMap) (*DeleteResult, error) {
	if r == nil || r.db == nil {
		log.Errorf("BunRepository is not initialized")
		return nil, fmt.Errorf("BunRepository is not initialized")
	}
	if db == nil {
		db = r.db
	}
	return bunDeleteWithDB(ctx, db, r.tableName, r.typ, filter)
}

// bunDeleteWithDB is the shared, non-generic implementation behind
// BunRepository[T].deleteWithDB and dynamicRepository.deleteWithDB.
func bunDeleteWithDB(ctx context.Context, db bun.IDB, tableName string, typ reflect.Type, filter RecordMap) (*DeleteResult, error) {
	// Delete starts from the model type so Bun can still infer table metadata
	// even without a concrete document instance.
	query := db.NewDelete().Model(reflect.New(typ).Interface())
	query = applyModelTable(query, tableName, typ)
	query = applyFilters(query, filter, typ)

	result, err := query.Exec(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return &DeleteResult{
		Acknowledged: true,
		DeletedCount: rows,
	}, nil
}

type whereCapable[T any] interface {
	Where(query string, args ...interface{}) T
}

type modelTableExprCapable[T any] interface {
	ModelTableExpr(query string, args ...interface{}) T
}

// applyModelTable and every helper below it used to be parameterized by a
// compile-time type argument [T any], but nothing in their bodies actually
// needs T to be known at compile time -- they only ever use it to get a
// reflect.Type for tag/column introspection. Taking that reflect.Type as an
// explicit parameter instead lets BunRepository[T] (static, Go-struct models)
// and dynamicRepository (runtime-published custom models, whose Go type is
// synthesized via reflect.StructOf and can never be a compile-time type
// argument) share one implementation instead of forking the ORM's write path.
func applyModelTable[Q modelTableExprCapable[Q]](query Q, tableName string, typ reflect.Type) Q {
	if tableName == "" {
		return query
	}
	// Keep the default struct-derived alias stable while redirecting the query
	// to the logical storage table configured for the registered model.
	alias := modelTableAlias(typ)
	if alias == "" {
		alias = strings.TrimSpace(tableName)
	}
	return query.ModelTableExpr("? AS ?", bun.Ident(tableName), bun.Ident(alias))
}

func modelTableAlias(typ reflect.Type) string {
	typ = dereferenceType(typ)
	if typ.Name() == "" {
		return ""
	}
	if alias := explicitBunModelAlias(typ); alias != "" {
		return alias
	}
	return snakeCaseIdentifier(typ.Name())
}

func explicitBunModelAlias(typ reflect.Type) string {
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.Anonymous || field.Type != reflect.TypeOf(bun.BaseModel{}) {
			continue
		}
		options := parseBunTagOptions(field.Tag.Get("bun"))
		if alias := strings.TrimSpace(options["alias"]); alias != "" {
			return alias
		}
	}
	return ""
}

func applyFilters[Q whereCapable[Q]](query Q, filter RecordMap, typ reflect.Type) Q {
	for key, value := range filter {
		// Resolve logical filter keys through metadata so runtime calls can use
		// Go names, canonical names, or explicit filter aliases.
		column := bun.Ident(resolveFilterColumnName(typ, key))
		if sliceValue, ok := normalizeSliceFilterValue(value); ok {
			if len(sliceValue) == 0 {
				// An empty IN filter should match nothing rather than everything.
				query = query.Where("1 = 0")
				continue
			}
			// Expand slice-like values into SQL IN predicates.
			query = query.Where("? IN (?)", column, bun.In(sliceValue))
			continue
		}
		// Default scalar filters compile to equality predicates.
		query = query.Where("? = ?", column, value)
	}
	return query
}

func resolveFilterColumnName(typ reflect.Type, key string) string {
	switch key {
	case "", "_":
		return key
	case "id":
		return "id"
	default:
		if column, ok := lookupFilterColumnName(typ, key); ok {
			return column
		}
		return toSnakeCase(key)
	}
}

func normalizeSliceFilterValue(value any) ([]any, bool) {
	if value == nil {
		return nil, false
	}

	// Reflection lets one helper support []string, []int, and similar filter
	// payloads without enumerating every concrete slice type.
	rv := reflect.ValueOf(value)
	if !rv.IsValid() {
		return nil, false
	}
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil, false
	}
	if rv.Type().Elem().Kind() == reflect.Uint8 {
		// Treat []byte as a scalar blob, not as an IN-list payload.
		return nil, false
	}

	out := make([]any, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out = append(out, rv.Index(i).Interface())
	}
	return out, true
}

func sanitizeUpdateMap(typ reflect.Type, update RecordMap) RecordMap {
	if update == nil {
		update = RecordMap{}
	}
	sanitized := make(RecordMap, len(update)+1)
	for key, value := range update {
		column := resolveFilterColumnName(typ, key)
		switch column {
		case "", "id", "created_at", "updated_at":
			// Keep immutable fields and repository-managed timestamps out of the
			// caller payload so they do not collide with canonical SQL columns.
			continue
		default:
			// Several payload keys can resolve to one column (alias, Go name,
			// column). Map order is random, so let the canonical key win.
			if _, taken := sanitized[column]; taken && key != column {
				continue
			}
			sanitized[column] = value
		}
	}
	// Keep UpdatedAt in sync in one place after alias normalization.
	sanitized["updated_at"] = time.Now().UTC()
	return sanitized
}

func callBeforeCreate(document any) {
	if document == nil {
		return
	}
	// Invoke the optional hook through a lightweight interface check so models
	// can opt in without depending on a shared base type.
	if hook, ok := document.(interface{ BeforeCreate() }); ok {
		hook.BeforeCreate()
	}
}

func extractModelID(document any) interface{} {
	value := reflect.ValueOf(document)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return nil
	}

	baseModel := value.FieldByName("BaseModel")
	if baseModel.IsValid() && baseModel.Kind() == reflect.Struct {
		id := baseModel.FieldByName("ID")
		if id.IsValid() {
			return id.Interface()
		}
	}

	id := value.FieldByName("ID")
	if id.IsValid() {
		return id.Interface()
	}
	return nil
}

func assignModelID(document any, id int64) {
	if document == nil {
		return
	}
	// Only pointer-to-struct payloads can receive the generated id.
	value := reflect.ValueOf(document)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return
	}
	value = value.Elem()
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return
	}
	if baseModel := value.FieldByName("BaseModel"); baseModel.IsValid() && baseModel.Kind() == reflect.Struct {
		if field := baseModel.FieldByName("ID"); field.IsValid() && field.CanSet() && field.Kind() == reflect.Int64 {
			// Prefer the embedded BaseModel id when the record uses the shared
			// persistence fragment.
			field.SetInt(id)
			return
		}
	}
	if field := value.FieldByName("ID"); field.IsValid() && field.CanSet() && field.Kind() == reflect.Int64 {
		field.SetInt(id)
	}
}

func isZeroModelID(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case int64:
		return typed == 0
	case int:
		return typed == 0
	case float64:
		return typed == 0
	case string:
		return strings.TrimSpace(typed) == "" || typed == "0"
	default:
		return false
	}
}

func lookupColumnName(typ reflect.Type, key string) (string, bool) {
	if typ == nil {
		return "", false
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return lookupColumnNameByType(typ, key)
}

func lookupFilterColumnName(typ reflect.Type, key string) (string, bool) {
	if column, ok := lookupColumnName(typ, key); ok {
		return column, true
	}

	if typ == nil {
		return "", false
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}

	fieldMeta, ok := lookupFieldMetadataByType(typ, key)
	if !ok {
		return "", false
	}

	column := fieldMeta.BunColumn
	if column == "" {
		column = fieldMeta.Name
	}
	if column == "" {
		column = toSnakeCase(fieldMeta.GoName)
	}
	return column, column != ""
}

func isJSONBUpdateColumn(typ reflect.Type, column string) bool {
	if typ == nil {
		return false
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		return false
	}
	return bunTypeIndexFor(typ).jsonb[column]
}

func isJSONBColumnByType(typ reflect.Type, column string) bool {
	if typ.Kind() != reflect.Struct {
		return false
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			if isJSONBColumnByType(embeddedType, column) {
				return true
			}
		}

		options := parseBunTagOptions(field.Tag.Get("bun"))
		fieldColumn := strings.TrimSpace(options["__name__"])
		if fieldColumn == "" {
			fieldColumn = toSnakeCase(field.Name)
		}
		if fieldColumn != column {
			continue
		}
		return strings.EqualFold(strings.TrimSpace(options["type"]), "jsonb")
	}
	return false
}

func marshalJSONBUpdateValue(value any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

// lookupColumnNameByType resolves a key through the cached per-type index.
func lookupColumnNameByType(typ reflect.Type, key string) (string, bool) {
	if typ.Kind() != reflect.Struct {
		return "", false
	}
	column, ok := bunTypeIndexFor(typ).columns[key]
	return column, ok
}

// lookupFieldMetadataByType looks up the field metadata by type handled by this package.
func lookupFieldMetadataByType(typ reflect.Type, key string) (FieldMetadata, bool) {
	if typ.Kind() != reflect.Struct {
		return FieldMetadata{}, false
	}

	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)

		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			if meta, ok := lookupFieldMetadataByType(embeddedType, key); ok {
				return meta, true
			}
		}

		if !field.IsExported() || field.Name == "_" {
			continue
		}

		bsonName, _ := parseStructTag(field.Tag.Get("bson"))
		jsonName, _ := parseStructTag(field.Tag.Get("json"))
		bunName, _ := parseStructTag(field.Tag.Get("bun"))
		metaOptions := parseFieldMetaOptions(field)
		name := firstNonEmpty(bsonName, jsonName, bunName, toSnakeCase(field.Name))
		filterKey := resolveFilterKey(name, metaOptions)

		if filterKey == "" || key != filterKey {
			continue
		}
		return FieldMetadata{
			Name:      name,
			GoName:    field.Name,
			BSONName:  bsonName,
			JSONName:  jsonName,
			BunColumn: bunName,
			FilterKey: filterKey,
		}, true
	}
	return FieldMetadata{}, false
}

// parseTagName parses the tag name handled by this package.
func parseTagName(tag string) string {
	if tag == "" {
		return ""
	}
	name := strings.Split(tag, ",")[0]
	if name == "-" {
		return ""
	}
	return name
}

func toSnakeCase(value string) string {
	if value == "" {
		return value
	}
	return snakeCaseIdentifier(value)
}

func snakeCaseIdentifier(value string) string {
	if value == "" {
		return value
	}

	var b strings.Builder
	b.Grow(len(value) + 4)
	runes := []rune(value)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			if i > 0 {
				prev := runes[i-1]
				nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
				if unicode.IsLower(prev) || unicode.IsDigit(prev) || (unicode.IsUpper(prev) && nextLower) {
					b.WriteByte('_')
				}
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func toRecordMap(value any) (RecordMap, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}

	var out RecordMap
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
