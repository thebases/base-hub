package engine

// File overview:
// - Responsibility: evaluates step conditions (bindings, operators, value comparison) for the linear and DAG engines.
// - Key symbols: `shouldRunWorkflowStep`, `evaluateWorkflowCondition`, `normalizeConditionObject`, `normalizeConditionList`.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"git.thebase.vn/base_hub/internal/orm"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// ErrUnknownConditionOperator: a leaf condition has a key that is not an operator (plan T63). It
// used to be ignored, so a typo such as {"field":"amount","gt":100} matched every payload.
var ErrUnknownConditionOperator = errors.New("workflow: unknown condition operator")

// conditionLeafKeys are the keys a leaf condition ({"field": ..., <operators>}) may hold.
var conditionLeafKeys = map[string]struct{}{
	"field": {}, "exists": {}, "empty": {}, "equals": {}, "not_equals": {}, "in": {}, "not_in": {},
	"gt": {}, "gte": {}, "lt": {}, "lte": {}, "contains": {}, "regex": {},
}

// conditionNumericOps are the ordered comparisons (plan T103); each holds when cmp(actual, expected) does.
var conditionNumericOps = []struct {
	key string
	ok  func(actual, expected float64) bool
}{
	{"gt", func(a, e float64) bool { return a > e }},
	{"gte", func(a, e float64) bool { return a >= e }},
	{"lt", func(a, e float64) bool { return a < e }},
	{"lte", func(a, e float64) bool { return a <= e }},
}

// maxConditionRegexLen caps an authored pattern. Go's RE2 already matches in linear time; the cap
// bounds compile cost and the compiled program size of a pattern taken from a step param.
const maxConditionRegexLen = 512

// maxConditionRegexCache bounds the compiled-pattern cache: patterns come from authored steps (a
// small set), but a #reference can render a payload value into the pattern, so the cache stops
// growing instead of keeping every distinct value.
const maxConditionRegexCache = 256

var (
	conditionRegexCache   sync.Map // pattern -> *regexp.Regexp
	conditionRegexEntries atomic.Int64
)

// conditionReferencePattern finds #field.path references inside a condition value (compiled once).
var conditionReferencePattern = regexp.MustCompile(`#(\w+(?:\.\w+)*)`)

func shouldRunWorkflowStep(step workflowmodels.Step, payload []byte) (bool, error) {
	rawCondition, ok := step.Params["condition"]
	if !ok || rawCondition == nil {
		return true, nil
	}

	bindings := payloadForAudit(payload)
	return evaluateWorkflowCondition(rawCondition, bindings)
}

func evaluateWorkflowCondition(raw any, bindings map[string]any) (bool, error) {
	condition, err := normalizeConditionObject(raw)
	if err != nil {
		return false, err
	}
	if len(condition) == 0 {
		return true, nil
	}

	if rawAll, exists := condition["all"]; exists {
		items, err := normalizeConditionList(rawAll)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			matched, err := evaluateWorkflowCondition(item, bindings)
			if err != nil {
				return false, err
			}
			if !matched {
				return false, nil
			}
		}
		return true, nil
	}

	if rawAny, exists := condition["any"]; exists {
		items, err := normalizeConditionList(rawAny)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			matched, err := evaluateWorkflowCondition(item, bindings)
			if err != nil {
				return false, err
			}
			if matched {
				return true, nil
			}
		}
		return false, nil
	}

	if rawNot, exists := condition["not"]; exists {
		matched, err := evaluateWorkflowCondition(rawNot, bindings)
		if err != nil {
			return false, err
		}
		return !matched, nil
	}

	field := strings.TrimSpace(fmt.Sprint(condition["field"]))
	if field == "" {
		return false, fmt.Errorf("workflow: condition.field is required")
	}
	if err := checkConditionLeafKeys(field, condition); err != nil {
		return false, err
	}

	actual, exists := lookupConditionBinding(bindings, field)
	if rawExists, hasExists := condition["exists"]; hasExists {
		expected, ok := rawExists.(bool)
		if !ok {
			return false, fmt.Errorf("workflow: condition.exists for %s must be a boolean", field)
		}
		if exists != expected {
			return false, nil
		}
	}

	if rawEmpty, hasEmpty := condition["empty"]; hasEmpty {
		expected, ok := rawEmpty.(bool)
		if !ok {
			return false, fmt.Errorf("workflow: condition.empty for %s must be a boolean", field)
		}
		if conditionValueEmpty(actual) != expected {
			return false, nil
		}
	}

	if rawEquals, hasEquals := condition["equals"]; hasEquals {
		expected := renderConditionValue(rawEquals, bindings)
		if !conditionValuesEqual(actual, expected) {
			return false, nil
		}
	}

	if rawNotEquals, hasNotEquals := condition["not_equals"]; hasNotEquals {
		expected := renderConditionValue(rawNotEquals, bindings)
		if conditionValuesEqual(actual, expected) {
			return false, nil
		}
	}

	if rawIn, hasIn := condition["in"]; hasIn {
		items, err := normalizeConditionValues(rawIn)
		if err != nil {
			return false, err
		}
		matched := false
		for _, item := range items {
			if conditionValuesEqual(actual, renderConditionValue(item, bindings)) {
				matched = true
				break
			}
		}
		if !matched {
			return false, nil
		}
	}

	if rawNotIn, hasNotIn := condition["not_in"]; hasNotIn {
		items, err := normalizeConditionValues(rawNotIn)
		if err != nil {
			return false, err
		}
		for _, item := range items {
			if conditionValuesEqual(actual, renderConditionValue(item, bindings)) {
				return false, nil
			}
		}
	}

	return evaluateConditionMatchOps(field, condition, actual, exists, bindings)
}

// evaluateConditionMatchOps applies the T103 operators (gt/gte/lt/lte/contains/regex) of one leaf.
// A missing field never matches them; a present value of the wrong type is an error, so a
// comparison that can never be true is reported instead of silently skipping steps.
func evaluateConditionMatchOps(field string, condition map[string]any, actual any, exists bool, bindings map[string]any) (bool, error) {
	for _, op := range conditionNumericOps {
		rawExpected, has := condition[op.key]
		if !has {
			continue
		}
		expected, ok := conditionNumber(renderConditionValue(rawExpected, bindings))
		if !ok {
			return false, fmt.Errorf("workflow: condition.%s for %s must be a number", op.key, field)
		}
		if !exists || actual == nil {
			return false, nil
		}
		value, ok := conditionNumber(actual)
		if !ok {
			return false, fmt.Errorf("workflow: condition.%s for %s needs a numeric field value, got %T", op.key, field, actual)
		}
		if !op.ok(value, expected) {
			return false, nil
		}
	}
	if rawContains, has := condition["contains"]; has {
		matched, err := conditionContains(field, actual, renderConditionValue(rawContains, bindings))
		if err != nil || !matched {
			return false, err
		}
	}
	if rawRegex, has := condition["regex"]; has {
		pattern, ok := renderConditionValue(rawRegex, bindings).(string)
		if !ok {
			return false, fmt.Errorf("workflow: condition.regex for %s must be a string", field)
		}
		re, err := compileConditionRegex(pattern)
		if err != nil {
			return false, fmt.Errorf("workflow: condition.regex for %s: %w", field, err)
		}
		text, ok := conditionScalarText(actual)
		if !ok {
			if actual == nil {
				return false, nil
			}
			return false, fmt.Errorf("workflow: condition.regex for %s needs a string field value, got %T", field, actual)
		}
		if !re.MatchString(text) {
			return false, nil
		}
	}
	return true, nil
}

// conditionNumber accepts only JSON numbers (float64 from a decoded payload, json.Number, Go ints
// from authored params). Numeric strings are refused on purpose: "10" > "9" must not depend on a
// silent conversion the author did not ask for.
func conditionNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

// conditionContains: a string field contains the expected text; an array field contains an element
// equal to expected. A missing field does not match.
func conditionContains(field string, actual, expected any) (bool, error) {
	switch typed := actual.(type) {
	case nil:
		return false, nil
	case string:
		needle, ok := expected.(string)
		if !ok {
			return false, fmt.Errorf("workflow: condition.contains for %s must be a string when the field is a string", field)
		}
		return strings.Contains(typed, needle), nil
	case []any:
		for _, item := range typed {
			if conditionValuesEqual(item, expected) {
				return true, nil
			}
		}
		return false, nil
	default:
		return false, fmt.Errorf("workflow: condition.contains for %s needs a string or array field value, got %T", field, actual)
	}
}

// conditionScalarText renders a string, number or bool field for regex matching.
func conditionScalarText(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case float64, json.Number, int, int64, bool:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

// compileConditionRegex compiles pattern (RE2: linear-time matching, no backtracking) once and keeps
// it in a bounded process cache.
func compileConditionRegex(pattern string) (*regexp.Regexp, error) {
	if len(pattern) > maxConditionRegexLen {
		return nil, fmt.Errorf("pattern longer than %d bytes", maxConditionRegexLen)
	}
	if cached, ok := conditionRegexCache.Load(pattern); ok {
		return cached.(*regexp.Regexp), nil
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, err
	}
	if conditionRegexEntries.Load() < maxConditionRegexCache {
		if _, loaded := conditionRegexCache.LoadOrStore(pattern, re); !loaded {
			conditionRegexEntries.Add(1)
		}
	}
	return re, nil
}

// checkConditionLeafKeys rejects a leaf condition with any key that is not a known operator.
func checkConditionLeafKeys(field string, condition map[string]any) error {
	unknown := make([]string, 0)
	for key := range condition {
		if _, ok := conditionLeafKeys[key]; !ok {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("%w for %s: %s (use exists, empty, equals, not_equals, in, not_in, gt, gte, lt, lte, contains, regex, or all/any/not)", ErrUnknownConditionOperator, field, strings.Join(unknown, ", "))
}

func normalizeConditionObject(raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case map[string]any:
		return typed, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("workflow: condition must be a JSON object")
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, fmt.Errorf("workflow: condition must be a JSON object")
		}
		return decoded, nil
	}
}

func normalizeConditionList(raw any) ([]any, error) {
	switch typed := raw.(type) {
	case []any:
		return typed, nil
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("workflow: condition list must be an array")
		}
		var decoded []any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil, fmt.Errorf("workflow: condition list must be an array")
		}
		return decoded, nil
	}
}

func normalizeConditionValues(raw any) ([]any, error) {
	switch typed := raw.(type) {
	case []any:
		return typed, nil
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("workflow: condition values must be an array")
	}
}

func lookupConditionBinding(bindings map[string]any, field string) (any, bool) {
	if bindings == nil {
		return nil, false
	}
	if value, ok := bindings[field]; ok {
		return value, true
	}

	current := any(bindings)
	for _, part := range strings.Split(field, ".") {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, exists := object[part]
		if !exists {
			return nil, false
		}
		current = value
	}
	return current, true
}

func renderConditionValue(raw any, bindings map[string]any) any {
	text, ok := raw.(string)
	if !ok {
		return raw
	}
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "#") && !strings.Contains(trimmed[1:], "#") {
		if value, ok := lookupConditionBinding(bindings, strings.TrimPrefix(trimmed, "#")); ok {
			return value
		}
		return raw
	}

	return conditionReferencePattern.ReplaceAllStringFunc(text, func(match string) string {
		value, ok := lookupConditionBinding(bindings, strings.TrimPrefix(match, "#"))
		if !ok {
			return match
		}
		return fmt.Sprint(value)
	})
}

func conditionValuesEqual(actual any, expected any) bool {
	switch typed := actual.(type) {
	case nil:
		return expected == nil
	case float64:
		switch other := expected.(type) {
		case float64:
			return typed == other
		case int:
			return typed == float64(other)
		case int64:
			return typed == float64(other)
		}
	case int:
		switch other := expected.(type) {
		case float64:
			return float64(typed) == other
		case int:
			return typed == other
		case int64:
			return int64(typed) == other
		}
	case int64:
		switch other := expected.(type) {
		case float64:
			return float64(typed) == other
		case int:
			return typed == int64(other)
		case int64:
			return typed == other
		}
	case bool:
		if other, ok := expected.(bool); ok {
			return typed == other
		}
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected)
}

func conditionValueEmpty(value any) bool {
	switch typed := value.(type) {
	case nil:
		return true
	case string:
		return strings.TrimSpace(typed) == ""
	case []any:
		return len(typed) == 0
	case []string:
		return len(typed) == 0
	case map[string]any:
		return len(typed) == 0
	default:
		return false
	}
}

// conditionBranchMatches reports whether the step's branch label matches the
// condition result in the payload. When conditionStepKey is non-empty the
// step-scoped key "_condition_result_<key>" is preferred over the global
// "_condition_result", enabling multiple condition nodes in one workflow.
// "yes"/"true" steps run when the condition was true; "no"/"false" steps run
// when it was false; any other label is treated as always-run.
func conditionBranchMatches(label string, conditionStepKey string, payload []byte) bool {
	bindings := payloadForAudit(payload)
	var result bool
	if conditionStepKey != "" {
		if v, ok := bindings["_condition_result_"+conditionStepKey].(bool); ok {
			result = v
		} else {
			result, _ = bindings["_condition_result"].(bool)
		}
	} else {
		result, _ = bindings["_condition_result"].(bool)
	}
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "yes", "true":
		return result
	case "no", "false":
		return !result
	default:
		return true
	}
}

// decodeRecord decodes one runtime row into T through orm.DecodeRecord, which collapses the
// [id, display_name] many2one pairs the runtime read renders (a trigger or flow row with a
// company_id used to fail to decode and drop every trigger of its database at sync time).
func decodeRecord[T any](record orm.RecordMap) (*T, error) {
	decoded, err := orm.DecodeRecord[T](record)
	if err != nil {
		return nil, err
	}
	return &decoded, nil
}
