package orm

import (
	"reflect"
	"sync"
)

// bunTypeIndex is the per-struct-type result of the tag walk that column
// lookups used to repeat on every filter and update key. Go types are
// immutable after boot (including reflect.StructOf types), so the index is
// built once per type and shared read-only afterwards.
type bunTypeIndex struct {
	// columns maps every accepted key (Go field name, json name or column)
	// to its bun column. The first match in field order wins, as before.
	columns map[string]string
	// jsonb holds the columns whose bun tag declares type:jsonb.
	jsonb map[string]bool
}

var bunTypeIndexCache sync.Map // reflect.Type -> *bunTypeIndex

func bunTypeIndexFor(typ reflect.Type) *bunTypeIndex {
	if cached, ok := bunTypeIndexCache.Load(typ); ok {
		return cached.(*bunTypeIndex)
	}
	idx := &bunTypeIndex{columns: map[string]string{}, jsonb: map[string]bool{}}
	idx.walk(typ)
	for column := range idx.columnSet() {
		idx.jsonb[column] = isJSONBColumnByType(typ, column)
	}
	actual, _ := bunTypeIndexCache.LoadOrStore(typ, idx)
	return actual.(*bunTypeIndex)
}

func (idx *bunTypeIndex) columnSet() map[string]struct{} {
	set := make(map[string]struct{}, len(idx.columns))
	for _, column := range idx.columns {
		set[column] = struct{}{}
	}
	return set
}

// walk mirrors the original recursive lookup order: an embedded struct is
// searched before the embedding field itself, and earlier fields win.
func (idx *bunTypeIndex) walk(typ reflect.Type) {
	if typ.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Anonymous {
			embeddedType := field.Type
			if embeddedType.Kind() == reflect.Pointer {
				embeddedType = embeddedType.Elem()
			}
			idx.walk(embeddedType)
		}
		column := parseTagName(field.Tag.Get("bun"))
		if column == "" {
			column = toSnakeCase(field.Name)
		}
		for _, key := range []string{field.Name, parseTagName(field.Tag.Get("json")), column} {
			if key == "" {
				continue
			}
			if _, exists := idx.columns[key]; !exists {
				idx.columns[key] = column
			}
		}
	}
}
