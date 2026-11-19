package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime domain, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `TrueDomain`, `FalseDomain`, `DomainKind`, `DomainNode`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

type DomainKind string

const (
	DomainKindTrue      DomainKind = "true"
	DomainKindFalse     DomainKind = "false"
	DomainKindCondition DomainKind = "condition"
	DomainKindAnd       DomainKind = "and"
	DomainKindOr        DomainKind = "or"
	DomainKindNot       DomainKind = "not"
)

// DomainNode is the parsed representation of a domain tree.
type DomainNode struct {
	Kind     DomainKind
	Field    string
	Operator string
	Value    any
	Children []DomainNode
}

// TrueDomain returns the identity domain that matches all rows.
func TrueDomain() DomainNode {
	return DomainNode{Kind: DomainKindTrue}
}

// FalseDomain returns the empty-result domain.
func FalseDomain() DomainNode {
	return DomainNode{Kind: DomainKindFalse}
}

// normalizeDomainOperator is the single place operator spelling is canonicalised
// (trim + lower-case); parse-time callers use it so later stages can compare
// DomainNode.Operator directly.
func normalizeDomainOperator(operator string) string {
	return strings.ToLower(strings.TrimSpace(operator))
}

// Where builds one condition leaf.
func Where(field, operator string, value any) DomainNode {
	return DomainNode{
		Kind:     DomainKindCondition,
		Field:    strings.TrimSpace(field),
		Operator: normalizeDomainOperator(operator),
		Value:    value,
	}
}

// And combines children with boolean AND.
func And(children ...DomainNode) DomainNode {
	return DomainNode{Kind: DomainKindAnd, Children: compactDomainChildren(children)}
}

// Or combines children with boolean OR.
func Or(children ...DomainNode) DomainNode {
	return DomainNode{Kind: DomainKindOr, Children: compactDomainChildren(children)}
}

// Not negates one child domain.
func Not(child DomainNode) DomainNode {
	return DomainNode{Kind: DomainKindNot, Children: []DomainNode{child}}
}

func compactDomainChildren(children []DomainNode) []DomainNode {
	out := make([]DomainNode, 0, len(children))
	for _, child := range children {
		if child.Kind == "" {
			continue
		}
		out = append(out, child)
	}
	return out
}

// ParseDomain converts RPC-style domain tokens into a typed domain tree.
func ParseDomain(raw any) (DomainNode, error) {
	if raw == nil {
		return TrueDomain(), nil
	}
	tokens, err := normalizeDomainPayload(raw)
	if err != nil {
		return DomainNode{}, err
	}
	if len(tokens) == 0 {
		return TrueDomain(), nil
	}
	normalized, err := normalizeDomainTokens(tokens)
	if err != nil {
		return DomainNode{}, err
	}
	index := 0
	node, err := parseNormalizedDomain(normalized, &index)
	if err != nil {
		return DomainNode{}, err
	}
	if index != len(normalized) {
		log.Errorf("orm: domain contains trailing tokens")
		return DomainNode{}, fmt.Errorf("orm: domain contains trailing tokens")
	}
	return node, nil
}

// normalizeDomainForModel rewrites one parsed domain tree so condition values
// match the target field metadata before SQL compilation or in-memory checks.
func normalizeDomainForModel(model *ModelMetadata, node DomainNode) (DomainNode, error) {
	switch node.Kind {
	case "", DomainKindTrue, DomainKindFalse:
		return node, nil
	}
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return DomainNode{}, fmt.Errorf("orm: model metadata is required")
	}

	switch node.Kind {
	case DomainKindCondition:
		return normalizeDomainConditionNode(model, node)
	case DomainKindAnd, DomainKindOr, DomainKindNot:
		normalized := node
		normalized.Children = make([]DomainNode, 0, len(node.Children))
		for _, child := range node.Children {
			nextChild, err := normalizeDomainForModel(model, child)
			if err != nil {
				return DomainNode{}, err
			}
			normalized.Children = append(normalized.Children, nextChild)
		}
		return normalized, nil
	default:
		return node, nil
	}
}

func normalizeDomainConditionNode(model *ModelMetadata, node DomainNode) (DomainNode, error) {
	normalized := node
	if isAnyOperator(node.Operator) {
		field, err := model.Field(node.Field)
		if err != nil {
			return DomainNode{}, err
		}
		relationModel, ok := LookupSharedModel(field.Relation)
		if !ok {
			log.Errorf("orm: relation model %q is not registered", field.Relation)
			return DomainNode{}, fmt.Errorf("orm: relation model %q is not registered", field.Relation)
		}
		child, ok := node.Value.(DomainNode)
		if !ok {
			log.Errorf("orm: any operator requires nested domain")
			return DomainNode{}, fmt.Errorf("orm: any operator requires nested domain")
		}
		nextChild, err := normalizeDomainForModel(relationModel, child)
		if err != nil {
			return DomainNode{}, err
		}
		normalized.Value = nextChild
		return normalized, nil
	}

	field, err := resolveDomainFieldMetadata(model, node.Field)
	if err != nil {
		return DomainNode{}, err
	}
	normalized.Value = normalizeDomainConditionValue(field, node.Operator, node.Value)
	return normalized, nil
}

// resolveDomainFieldMetadata resolves the terminal field metadata for one domain path.
func resolveDomainFieldMetadata(model *ModelMetadata, path string) (FieldMetadata, error) {
	segments := splitNonEmpty(path, ".")
	return resolveDomainFieldSegments(model, segments)
}

func resolveDomainFieldSegments(model *ModelMetadata, segments []string) (FieldMetadata, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return FieldMetadata{}, fmt.Errorf("orm: model metadata is required")
	}
	if len(segments) == 0 {
		log.Errorf("orm: empty domain field path")
		return FieldMetadata{}, fmt.Errorf("orm: empty domain field path")
	}
	field, err := model.Field(segments[0])
	if err != nil {
		return FieldMetadata{}, err
	}
	if len(segments) == 1 {
		return field, nil
	}
	// A dotted path into a json field ("metadata.batteryDetail.health") is a
	// text extraction, so its values normalize as plain text, not as json.
	if field.Kind == FieldKindJSON {
		if err := validateJSONDomainKeys(segments[1:]); err != nil {
			return FieldMetadata{}, err
		}
		return FieldMetadata{Name: field.Name, Kind: FieldKindText}, nil
	}
	if (field.Kind != FieldKindManyToOne && field.Kind != FieldKindOneToOne) || strings.TrimSpace(field.Relation) == "" {
		log.Errorf("orm: %s is not a traversable many2one path on %s", field.Name, model.Name)
		return FieldMetadata{}, fmt.Errorf("orm: %s is not a traversable many2one path on %s", field.Name, model.Name)
	}
	nextModel, ok := LookupSharedModel(field.Relation)
	if !ok {
		log.Errorf("orm: relation model %q is not registered", field.Relation)
		return FieldMetadata{}, fmt.Errorf("orm: relation model %q is not registered", field.Relation)
	}
	return resolveDomainFieldSegments(nextModel, segments[1:])
}

// operator must already be normalized: DomainNode.Operator is set only by Where.
func normalizeDomainConditionValue(field FieldMetadata, normalizedOperator string, value any) any {
	if normalizedOperator == "in" || normalizedOperator == "not in" {
		items := toAnySlice(value)
		normalizedItems := make([]any, 0, len(items))
		for _, item := range items {
			normalizedItems = append(normalizedItems, normalizeDomainScalarValue(field, item))
		}
		return normalizedItems
	}
	return normalizeDomainScalarValue(field, value)
}

func normalizeDomainScalarValue(field FieldMetadata, value any) any {
	switch field.Kind {
	case FieldKindID, FieldKindInteger:
		return normalizeRuntimeIntegerValue(value)
	case FieldKindBoolean:
		return normalizeRuntimeBooleanValue(value)
	case FieldKindManyToOne, FieldKindOneToOne:
		return normalizeRuntimeFieldValue(field, value)
	case FieldKindOneToMany, FieldKindManyToMany:
		// Relation collections also receive boolean false sentinels from some
		// clients, so collapse those to nil before domain evaluation.
		if boolValue, ok := value.(bool); ok && !boolValue {
			return nil
		}
		if relationID, ok := relatedRecordID(value); ok {
			return normalizeRuntimeIntegerValue(relationID)
		}
		return value
	default:
		// ("field", "=", False) means "unset" for every non-boolean
		// scalar (date, datetime, char, float, ...). Left as a Go bool it would
		// compile to `col = false` and PostgreSQL rejects the "f" literal on
		// non-boolean columns (e.g. SQLSTATE 22007 on timestamptz), so collapse
		// it to nil and let the compiler emit IS NULL / IS NOT NULL.
		if boolValue, ok := value.(bool); ok && !boolValue {
			return nil
		}
		return value
	}
}

func normalizeDomainPayload(raw any) ([]any, error) {
	switch typed := raw.(type) {
	case []any:
		return typed, nil
	case [3]any:
		return []any{[]any{typed[0], typed[1], typed[2]}}, nil
	default:
		log.Errorf("orm: unsupported domain payload %T", raw)
		return nil, fmt.Errorf("orm: unsupported domain payload %T", raw)
	}
}

func normalizeDomainTokens(domain []any) ([]any, error) {
	if len(domain) == 0 {
		return []any{}, nil
	}
	result := make([]any, 0, len(domain))
	expected := 1
	arity := map[string]int{"!": 1, "&": 2, "|": 2}
	for _, token := range domain {
		if expected == 0 {
			result = append([]any{"&"}, result...)
			expected = 1
		}
		if leaf, ok, err := normalizeDomainLeaf(token); err != nil {
			return nil, err
		} else if ok {
			expected--
			result = append(result, leaf)
			continue
		}
		operator, ok := token.(string)
		if !ok {
			log.Errorf("orm: invalid domain token %T", token)
			return nil, fmt.Errorf("orm: invalid domain token %T", token)
		}
		expected += arity[operator] - 1
		result = append(result, operator)
	}
	if expected != 0 {
		log.Errorf("orm: domain is syntactically not correct")
		return nil, fmt.Errorf("orm: domain is syntactically not correct")
	}
	return result, nil
}

func normalizeDomainLeaf(token any) ([]any, bool, error) {
	switch typed := token.(type) {
	case []any:
		if len(typed) != 3 {
			return nil, false, nil
		}
		leaf := append([]any(nil), typed...)
		if operator, _ := leaf[1].(string); isAnyOperator(operator) {
			nested, err := normalizeDomainPayload(leaf[2])
			if err != nil {
				return nil, false, err
			}
			leaf[2], err = normalizeDomainTokens(nested)
			if err != nil {
				return nil, false, err
			}
		}
		return leaf, true, nil
	case [3]any:
		return normalizeDomainLeaf([]any{typed[0], typed[1], typed[2]})
	default:
		return nil, false, nil
	}
}

// parseNormalizedDomain parses the normalized domain handled by this package.
func parseNormalizedDomain(tokens []any, index *int) (DomainNode, error) {
	if *index >= len(tokens) {
		log.Errorf("orm: unexpected end of domain")
		return DomainNode{}, fmt.Errorf("orm: unexpected end of domain")
	}
	token := tokens[*index]
	*index++
	switch typed := token.(type) {
	case string:
		switch strings.TrimSpace(typed) {
		case "!":
			child, err := parseNormalizedDomain(tokens, index)
			if err != nil {
				return DomainNode{}, err
			}
			return Not(child), nil
		case "&":
			left, err := parseNormalizedDomain(tokens, index)
			if err != nil {
				return DomainNode{}, err
			}
			right, err := parseNormalizedDomain(tokens, index)
			if err != nil {
				return DomainNode{}, err
			}
			return And(left, right), nil
		case "|":
			left, err := parseNormalizedDomain(tokens, index)
			if err != nil {
				return DomainNode{}, err
			}
			right, err := parseNormalizedDomain(tokens, index)
			if err != nil {
				return DomainNode{}, err
			}
			return Or(left, right), nil
		default:
			log.Errorf("orm: unsupported domain operator %q", typed)
			return DomainNode{}, fmt.Errorf("orm: unsupported domain operator %q", typed)
		}
	case []any:
		field, _ := typed[0].(string)
		operator, _ := typed[1].(string)
		if field == "" || operator == "" {
			log.Errorf("orm: domain leaf requires field and operator")
			return DomainNode{}, fmt.Errorf("orm: domain leaf requires field and operator")
		}
		if isAnyOperator(operator) {
			nestedTokens, ok := typed[2].([]any)
			if !ok {
				log.Errorf("orm: nested any domain must be token list")
				return DomainNode{}, fmt.Errorf("orm: nested any domain must be token list")
			}
			childIndex := 0
			child, err := parseNormalizedDomain(nestedTokens, &childIndex)
			if err != nil {
				return DomainNode{}, err
			}
			return Where(field, operator, child), nil
		}
		return Where(field, operator, typed[2]), nil
	default:
		log.Errorf("orm: unsupported domain token %T", token)
		return DomainNode{}, fmt.Errorf("orm: unsupported domain token %T", token)
	}
}

func isAnyOperator(operator string) bool {
	switch normalizeDomainOperator(operator) {
	case "any", "not any", "any!", "not any!":
		return true
	default:
		return false
	}
}

// Compile produces a parameterized SQL WHERE fragment for one model.
func (d DomainNode) Compile(model *ModelMetadata) (string, []any, error) {
	baseAlias := quoteIdent(model.StorageName)
	if strings.Trim(baseAlias, "\"") == "" {
		baseAlias = quoteIdent(model.Name)
	}
	return d.CompileWithBaseAlias(model, baseAlias)
}

// CompileWithBaseAlias produces a parameterized SQL WHERE fragment while
// honoring the caller-selected root table alias from the active Bun query.
// Translated fields resolve against the base language; language-aware callers
// should use CompileWithBaseAliasLang.
func (d DomainNode) CompileWithBaseAlias(model *ModelMetadata, baseAlias string) (string, []any, error) {
	return d.CompileWithBaseAliasLang(model, baseAlias, translationBaseLang)
}

// CompileWithBaseAliasLang compiles the domain with an explicit environment
// language so conditions on translated fields (SRS-F-021) compare the
// language-resolved text instead of the raw jsonb envelope.
func (d DomainNode) CompileWithBaseAliasLang(model *ModelMetadata, baseAlias, lang string) (string, []any, error) {
	switch d.Kind {
	case DomainKindTrue:
		return "TRUE", nil, nil
	case DomainKindFalse:
		return "FALSE", nil, nil
	case DomainKindCondition:
		return compileDomainCondition(model, d, baseAlias, lang)
	case DomainKindAnd, DomainKindOr:
		if len(d.Children) == 0 {
			log.Errorf("orm: empty %s domain", d.Kind)
			return "", nil, fmt.Errorf("orm: empty %s domain", d.Kind)
		}
		joiner := " AND "
		if d.Kind == DomainKindOr {
			joiner = " OR "
		}
		parts := make([]string, 0, len(d.Children))
		args := make([]any, 0)
		for _, child := range d.Children {
			sql, childArgs, err := child.CompileWithBaseAliasLang(model, baseAlias, lang)
			if err != nil {
				return "", nil, err
			}
			parts = append(parts, "("+sql+")")
			args = append(args, childArgs...)
		}
		return strings.Join(parts, joiner), args, nil
	case DomainKindNot:
		if len(d.Children) != 1 {
			log.Errorf("orm: not domain expects one child")
			return "", nil, fmt.Errorf("orm: not domain expects one child")
		}
		sql, args, err := d.Children[0].CompileWithBaseAliasLang(model, baseAlias, lang)
		if err != nil {
			return "", nil, err
		}
		return "NOT (" + sql + ")", args, nil
	default:
		log.Errorf("orm: unsupported domain kind %q", d.Kind)
		return "", nil, fmt.Errorf("orm: unsupported domain kind %q", d.Kind)
	}
}

func compileDomainCondition(model *ModelMetadata, node DomainNode, baseAlias, lang string) (string, []any, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", nil, fmt.Errorf("orm: model metadata is required")
	}
	if isAnyOperator(node.Operator) {
		return compileAnyCondition(model, node, baseAlias, lang)
	}
	column, err := resolveDomainPath(model, baseAlias, node.Field, lang)
	if err != nil {
		return "", nil, err
	}
	switch node.Operator {
	case "=", "==":
		if node.Value == nil {
			return column + " IS NULL", nil, nil
		}
		return column + " = ?", []any{node.Value}, nil
	case "!=", "<>":
		if node.Value == nil {
			return column + " IS NOT NULL", nil, nil
		}
		return column + " <> ?", []any{node.Value}, nil
	case ">", ">=", "<", "<=":
		return column + " " + node.Operator + " ?", []any{node.Value}, nil
	case "like":
		// Cast to text so a like/ilike filter on a bigint/numeric/many2one column matches its text
		// form instead of failing with "function lower(bigint) does not exist".
		return "CAST(" + column + " AS text) LIKE ?", []any{likePattern(node.Value)}, nil
	case "ilike":
		return "LOWER(CAST(" + column + " AS text)) LIKE LOWER(?)", []any{likePattern(node.Value)}, nil
	case "in":
		items := toAnySlice(node.Value)
		if len(items) == 0 {
			return "FALSE", nil, nil
		}
		return column + " IN (" + runtimeValuePlaceholders(len(items)) + ")", items, nil
	case "not in":
		items := toAnySlice(node.Value)
		if len(items) == 0 {
			return "TRUE", nil, nil
		}
		return column + " NOT IN (" + runtimeValuePlaceholders(len(items)) + ")", items, nil
	default:
		log.Errorf("orm: unsupported domain operator %q", node.Operator)
		return "", nil, fmt.Errorf("orm: unsupported domain operator %q", node.Operator)
	}
}

func runtimeValuePlaceholders(count int) string {
	if count <= 0 {
		return ""
	}
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = "?"
	}
	return strings.Join(parts, ", ")
}

func compileAnyCondition(model *ModelMetadata, node DomainNode, baseAlias, lang string) (string, []any, error) {
	field, err := model.Field(node.Field)
	if err != nil {
		return "", nil, err
	}
	child, ok := node.Value.(DomainNode)
	if !ok {
		log.Errorf("orm: any operator requires nested domain")
		return "", nil, fmt.Errorf("orm: any operator requires nested domain")
	}
	relationModel, ok := LookupSharedModel(field.Relation)
	if !ok {
		log.Errorf("orm: relation model %q is not registered", field.Relation)
		return "", nil, fmt.Errorf("orm: relation model %q is not registered", field.Relation)
	}
	// many2one/one2one any (produced by scopeDomainTraversals, plan T20): the related row must
	// exist and match. A dedicated alias keeps self-referential relations (parent_id) unambiguous.
	if field.Kind == FieldKindManyToOne || field.Kind == FieldKindOneToOne {
		alias := quoteIdent("any_" + model.Name + "_" + field.Name)
		childSQL, childArgs, err := child.CompileWithBaseAliasLang(relationModel, alias, lang)
		if err != nil {
			return "", nil, err
		}
		column := field.BunColumn
		if column == "" {
			column = field.Name
		}
		sql := fmt.Sprintf("EXISTS (SELECT 1 FROM %s AS %s WHERE %s.id = %s.%s AND (%s))", quoteIdent(relationModel.StorageName), alias, alias, baseAlias, quoteIdent(column), childSQL)
		if strings.HasPrefix(node.Operator, "not ") {
			sql = "NOT (" + sql + ")"
		}
		return sql, childArgs, nil
	}
	// Propagate the environment language so nested conditions on the relation's
	// translated fields resolve the same way as root-level conditions.
	// many2many joins the related table under the alias `target`, so the child condition must
	// reference that alias; one2many scans the related table unaliased.
	childAlias := quoteIdent(relationModel.StorageName)
	if field.Kind == FieldKindManyToMany {
		childAlias = "target"
	}
	childSQL, childArgs, err := child.CompileWithBaseAliasLang(relationModel, childAlias, lang)
	if err != nil {
		return "", nil, err
	}
	parentTable := baseAlias
	relatedTable := quoteIdent(relationModel.StorageName)
	var sql string
	var args []any
	switch field.Kind {
	case FieldKindOneToMany:
		inverse, err := relationModel.Field(field.Inverse)
		if err != nil {
			return "", nil, err
		}
		sql = fmt.Sprintf("EXISTS (SELECT 1 FROM %s WHERE %s = %s.id AND (%s))", relatedTable, quoteIdent(inverse.BunColumn), parentTable, childSQL)
		args = childArgs
	case FieldKindManyToMany:
		sql = fmt.Sprintf("EXISTS (SELECT 1 FROM %s rel JOIN %s target ON target.id = rel.%s WHERE rel.%s = %s.id AND (%s))", quoteIdent(field.RelationTable), relatedTable, quoteIdent(field.RelationKey), quoteIdent(field.RelationColumn), parentTable, childSQL)
		args = childArgs
	default:
		log.Errorf("orm: any operator requires one2many or many2many field, got %s", field.Kind)
		return "", nil, fmt.Errorf("orm: any operator requires one2many or many2many field, got %s", field.Kind)
	}
	if strings.HasPrefix(node.Operator, "not ") {
		sql = "NOT (" + sql + ")"
	}
	return sql, args, nil
}

// resolveDomainPath resolves the domain path handled by this package.
func resolveDomainPath(model *ModelMetadata, baseAlias, path, lang string) (string, error) {
	segments := splitNonEmpty(path, ".")
	return resolveDomainSegments(model, baseAlias, segments, lang)
}

// resolveDomainSegments resolves the domain segments handled by this package.
func resolveDomainSegments(model *ModelMetadata, baseAlias string, segments []string, lang string) (string, error) {
	if model == nil {
		log.Errorf("orm: model metadata is required")
		return "", fmt.Errorf("orm: model metadata is required")
	}
	if len(segments) == 0 {
		log.Errorf("orm: empty domain field path")
		return "", fmt.Errorf("orm: empty domain field path")
	}
	field, err := model.Field(segments[0])
	if err != nil {
		return "", err
	}
	// Non-stored computed fields have no SQL column, so a domain condition on
	// one must fail with an explicit contract error (SRS-F-018) instead of
	// compiling into an undefined-column reference. o2m/m2m relations stay on
	// their dedicated EXISTS compilation path and are unaffected.
	if strings.TrimSpace(field.Compute) != "" && !field.Stored {
		log.Errorf("orm: field %q on %s is a non-stored computed field and is not searchable", field.Name, model.Name)
		return "", fmt.Errorf("orm: field %q on %s is a non-stored computed field and is not searchable", field.Name, model.Name)
	}
	column := field.BunColumn
	if column == "" {
		column = field.Name
	}
	if len(segments) == 1 {
		// Translated columns are jsonb: the condition must compare the
		// language-resolved text with en fallback (SRS-F-021), not the envelope.
		if field.Translate {
			return translatedColumnSQL(baseAlias, column, lang), nil
		}
		return baseAlias + "." + quoteIdent(column), nil
	}
	// json sub-key path compiles to a #>> text extraction; a missing key
	// yields NULL, so "=" never matches and "!=" never matches either (SQL
	// three-valued logic) — callers wanting "missing" use ("path", "=", False).
	if field.Kind == FieldKindJSON {
		return jsonDomainPathSQL(baseAlias+"."+quoteIdent(column), segments[1:])
	}
	if (field.Kind != FieldKindManyToOne && field.Kind != FieldKindOneToOne) || strings.TrimSpace(field.Relation) == "" {
		log.Errorf("orm: %s is not a traversable many2one path on %s", field.Name, model.Name)
		return "", fmt.Errorf("orm: %s is not a traversable many2one path on %s", field.Name, model.Name)
	}
	next, ok := LookupSharedModel(field.Relation)
	if !ok {
		log.Errorf("orm: relation model %q is not registered", field.Relation)
		return "", fmt.Errorf("orm: relation model %q is not registered", field.Relation)
	}
	nextAlias := quoteIdent("sub_" + model.Name + "_" + field.Name)
	nestedExpr, err := resolveDomainSegments(next, nextAlias, segments[1:], lang)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("(SELECT %s FROM %s AS %s WHERE %s.id = %s.%s)", nestedExpr, quoteIdent(next.StorageName), nextAlias, nextAlias, baseAlias, quoteIdent(column)), nil
}

// jsonDomainKeyPattern bounds json path keys to plain identifiers: they are
// inlined into the SQL as a text[] literal (a bound placeholder inside the
// column expression would land ahead of the condition's own args).
var jsonDomainKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// validateJSONDomainKeys rejects any json path key that is not a plain identifier.
func validateJSONDomainKeys(keys []string) error {
	for _, key := range keys {
		if !jsonDomainKeyPattern.MatchString(key) {
			log.Errorf("orm: invalid json domain path key %q", key)
			return fmt.Errorf("orm: invalid json domain path key %q", key)
		}
	}
	return nil
}

// jsonDomainPathSQL renders `<column> #>> '{k1,k2}'` for a validated key path.
func jsonDomainPathSQL(column string, keys []string) (string, error) {
	if err := validateJSONDomainKeys(keys); err != nil {
		return "", err
	}
	return "(" + column + " #>> '{" + strings.Join(keys, ",") + "}')", nil
}

func likePattern(value any) string {
	return "%" + fmt.Sprint(value) + "%"
}

func toAnySlice(value any) []any {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	case []int64:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	}
	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return []any{value}
	}
	out := make([]any, 0, rv.Len())
	for index := 0; index < rv.Len(); index++ {
		out = append(out, rv.Index(index).Interface())
	}
	return out
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func toInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int8:
		return int64(typed)
	case int16:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case uint:
		return int64(typed)
	case uint8:
		return int64(typed)
	case uint16:
		return int64(typed)
	case uint32:
		return int64(typed)
	case uint64:
		return int64(typed)
	case float64:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

// toFloat64 converts a runtime field value into a float64 without truncating
// fractional values, unlike toInt64. Used by aggregate sum accumulation
// (e.g. read_group's ":sum" aggregator) where the underlying measure field
// may be a decimal (float32/float64) rather than an integer.
func toFloat64(value any) float64 {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case float64:
		return typed
	case int:
		return float64(typed)
	case int8:
		return float64(typed)
	case int16:
		return float64(typed)
	case int32:
		return float64(typed)
	case int64:
		return float64(typed)
	case uint:
		return float64(typed)
	case uint8:
		return float64(typed)
	case uint16:
		return float64(typed)
	case uint32:
		return float64(typed)
	case uint64:
		return float64(typed)
	case string:
		parsed, _ := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed
	default:
		return 0
	}
}
