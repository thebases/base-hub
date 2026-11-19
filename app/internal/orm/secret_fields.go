package orm

// File overview:
// - Responsibility: the `secret` field meta (plan T19). A field tagged `meta:"secret"` holds a
//   credential-like value (password hash, API key digest, reset token, ciphertext).
// - Key symbols: `RedactSecretFieldsForClient`, `ensureNoSecretFieldsInSearch`,
//   `ensureNoSecretFieldNames`.
// - Pattern: two independent guards.
//   1. Output: client transports (/rpc, REST aliases, MCP) pass every record-shaped result through
//      RedactSecretFieldsForClient, so the value is never serialized to any client, sudo included.
//      Server-side Go code (SMTP send, credential decryption, login) still reads it normally
//      because it never goes through a transport.
//   2. Input: for RPC-identity callers without sudo (same population as RBAC/record rules,
//      rbacPolicyApplies), a domain, direct filter, order or group-by naming a secret field is
//      refused, so `like`/`=`/sort/group results cannot be used as an oracle on the hidden value.
//      Sudo and internal callers keep full search semantics.

import (
	"context"
	"strings"
)

// secretRecordMethods are the runtime methods whose results carry stored field values. Metadata
// methods (fields_get, views, get_json_dto) are keyed by field name and must not be stripped.
var secretRecordMethods = map[string]struct{}{
	"read": {}, "search_read": {}, "search_fetch": {}, "read_group": {}, "cohort_data": {},
	"copy_data": {}, "copy": {}, "create": {}, "write": {}, "find_or_create": {},
	"onchange": {}, "default_get": {}, "toggle_active": {},
}

// modelSecretKeys returns every wire spelling of the model's secret fields (name, JSON name, Bun
// column), or nil when the model has none.
func modelSecretKeys(model *ModelMetadata) map[string]struct{} {
	if model == nil {
		return nil
	}
	var keys map[string]struct{}
	for _, field := range model.Fields {
		if !field.Secret {
			continue
		}
		if keys == nil {
			keys = map[string]struct{}{}
		}
		for _, key := range []string{field.Name, field.JSONName, field.BunColumn} {
			if key = strings.TrimSpace(key); key != "" {
				keys[key] = struct{}{}
			}
		}
	}
	return keys
}

// RedactSecretFieldsForClient removes the model's secret fields from a runtime method result
// before a client transport serializes it. It handles the record shapes the default methods
// return (one record, a list of records, onchange's {"value": {...}}) and read_group aggregate
// keys ("<field>:sum"). Results of other methods are returned unchanged.
func RedactSecretFieldsForClient(model *ModelMetadata, methodName string, result any) any {
	if _, ok := secretRecordMethods[strings.TrimSpace(methodName)]; !ok {
		return result
	}
	keys := modelSecretKeys(model)
	if len(keys) == 0 {
		return result
	}
	redactSecretValue(result, keys, 0)
	return result
}

func redactSecretValue(value any, keys map[string]struct{}, depth int) {
	if depth > 2 {
		return
	}
	switch typed := value.(type) {
	case RecordMap:
		redactSecretRecord(typed, keys, depth)
	case map[string]any:
		redactSecretRecord(typed, keys, depth)
	case []RecordMap:
		for _, record := range typed {
			redactSecretRecord(record, keys, depth)
		}
	case []map[string]any:
		for _, record := range typed {
			redactSecretRecord(record, keys, depth)
		}
	case []any:
		for _, item := range typed {
			redactSecretValue(item, keys, depth)
		}
	}
}

func redactSecretRecord(record map[string]any, keys map[string]struct{}, depth int) {
	for key := range record {
		name, _, _ := strings.Cut(key, ":")
		if _, secret := keys[name]; secret {
			delete(record, key)
		}
	}
	// onchange answers {"value": {...}, ...}: strip the nested values too.
	if nested, ok := record["value"]; ok {
		redactSecretValue(nested, keys, depth+1)
	}
}

// secretFieldGuardApplies reports whether ctx is a non-sudo RPC-identity caller.
func secretFieldGuardApplies(ctx context.Context) bool {
	return rbacPolicyApplies(EnvironmentFromContext(ctx))
}

// ensureNoSecretFieldsInSearch refuses a client search whose direct filter, domain or order names
// a secret field (directly or through a many2one path such as "create_uid.password").
func ensureNoSecretFieldsInSearch(ctx context.Context, model *ModelMetadata, filter RecordMap) error {
	if model == nil || !secretFieldGuardApplies(ctx) {
		return nil
	}
	for key, value := range filter {
		switch key {
		case "__domain":
			if domain, ok := value.(DomainNode); ok {
				if err := ensureNoSecretInDomain(model, domain); err != nil {
					return err
				}
			}
		case "__order":
			order, _ := value.(string)
			for _, clause := range strings.Split(order, ",") {
				parts := strings.Fields(clause)
				if len(parts) == 0 {
					continue
				}
				if err := ensureNoSecretPath(model, strings.TrimPrefix(parts[0], "-")); err != nil {
					return err
				}
			}
		default:
			if strings.HasPrefix(key, "__") {
				continue
			}
			if err := ensureNoSecretPath(model, key); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureNoSecretFieldNames refuses plain field names (read_group groupby/aggregates such as
// "password" or "password:count") that name a secret field, for non-sudo RPC callers.
func ensureNoSecretFieldNames(ctx context.Context, model *ModelMetadata, names []string) error {
	if model == nil || !secretFieldGuardApplies(ctx) {
		return nil
	}
	for _, name := range names {
		name, _, _ = strings.Cut(name, ":")
		if err := ensureNoSecretPath(model, name); err != nil {
			return err
		}
	}
	return nil
}

func ensureNoSecretInDomain(model *ModelMetadata, node DomainNode) error {
	if node.Kind == DomainKindCondition {
		return ensureNoSecretPath(model, node.Field)
	}
	for _, child := range node.Children {
		if err := ensureNoSecretInDomain(model, child); err != nil {
			return err
		}
	}
	return nil
}

// ensureNoSecretPath walks a (possibly dotted) field path through many2one relations and fails on
// the first secret field. Unknown segments are left to the normal resolver, which reports them.
func ensureNoSecretPath(model *ModelMetadata, path string) error {
	current := model
	for _, segment := range splitNonEmpty(strings.TrimSpace(path), ".") {
		if current == nil {
			return nil
		}
		field, ok := runtimeLookupField(current, segment)
		if !ok {
			return nil
		}
		if field.Secret {
			return InvalidParamsf("field %q on %s is secret and cannot be used to filter, sort or group", field.Name, current.Name)
		}
		if field.Kind != FieldKindManyToOne && field.Kind != FieldKindOneToOne {
			return nil
		}
		next, ok := LookupSharedModel(field.Relation)
		if !ok {
			return nil
		}
		current = next
	}
	return nil
}
