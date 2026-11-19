// runtime_domain_eval.go: in-memory evaluation of a domain against one record (EvaluateDomain), split out of repository_handler_read.go.
package orm

import (
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// EvaluateDomain evaluates a parsed domain tree against one record snapshot.
// Exported so non-generic callers (business-rule constraint handlers, and
// anything else that only has a RecordMap, not a Handler[T]) can reuse the
// same domain-matching semantics as the generic repository search path.
func EvaluateDomain(record RecordMap, domain DomainNode) (bool, error) {
	switch domain.Kind {
	case DomainKindTrue:
		return true, nil
	case DomainKindFalse:
		return false, nil
	case DomainKindAnd:
		for _, child := range domain.Children {
			ok, err := EvaluateDomain(record, child)
			if err != nil || !ok {
				return ok, err
			}
		}
		return true, nil
	case DomainKindOr:
		for _, child := range domain.Children {
			ok, err := EvaluateDomain(record, child)
			if err == nil && ok {
				return true, nil
			}
		}
		return false, nil
	case DomainKindNot:
		ok, err := EvaluateDomain(record, domain.Children[0])
		return !ok, err
	case DomainKindCondition:
		value := runtimeResolveRecordPath(record, domain.Field)
		return compareRuntimeValue(value, domain.Operator, domain.Value), nil
	default:
		log.Errorf("orm: unsupported domain kind %q", domain.Kind)
		return false, fmt.Errorf("orm: unsupported domain kind %q", domain.Kind)
	}
}

func runtimeResolveRecordPath(record RecordMap, path string) any {
	current := any(record)
	// Walk each ") entry in order so validation and side effects stay scoped to the current element.
	for _, segment := range splitNonEmpty(path, ".") {
		switch typed := current.(type) {
		case RecordMap:
			current = typed[segment]
		case map[string]any:
			current = typed[segment]
		case [2]any:
			if segment == "id" {
				current = typed[0]
			} else {
				current = typed[1]
			}
		default:
			return nil
		}
	}
	return current
}

// compareRuntimeValue compares the runtime value handled by this package.
func compareRuntimeValue(left any, operator string, right any) bool {
	operator = normalizeDomainOperator(operator)
	switch operator {
	case "=", "==":
		return fmt.Sprint(left) == fmt.Sprint(right)
	case "!=", "<>":
		return fmt.Sprint(left) != fmt.Sprint(right)
	case "like":
		return strings.Contains(fmt.Sprint(left), fmt.Sprint(right))
	case "ilike":
		return strings.Contains(strings.ToLower(fmt.Sprint(left)), strings.ToLower(fmt.Sprint(right)))
	case "in":
		// Walk each toanyslice(right) entry in order so validation and side effects stay scoped to the current element.
		for _, item := range toAnySlice(right) {
			if fmt.Sprint(left) == fmt.Sprint(item) {
				return true
			}
		}
		return false
	case "not in":
		return !compareRuntimeValue(left, "in", right)
	case ">", ">=", "<", "<=":
		lv := toInt64(left)
		rv := toInt64(right)
		switch operator {
		case ">":
			return lv > rv
		case ">=":
			return lv >= rv
		case "<":
			return lv < rv
		default:
			return lv <= rv
		}
	default:
		return false
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}
