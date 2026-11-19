package orm

// File overview:
// - Responsibility: plan T20. A client domain that walks into another model (a many2one path such
//   as "partner_id.email", or an any / not any on a relation) must respect that model's read ACL
//   and read record rules; otherwise a filter result is an oracle on rows the caller cannot read.
// - Key symbols: `scopeDomainTraversals`.
// - Pattern: rewrite the caller's domain before the root model's record rules are merged in
//   (runtimeSearchFilter), because the SQL compiler has no context. Only non-sudo RPC-identity
//   callers are affected (rbacPolicyApplies), the same population as ACL and record rules:
//   - target read ACL denied: the condition becomes FALSE (a constant reveals nothing, even
//     under NOT), with a warning log, instead of failing the whole search;
//   - target has a read record-rule domain: "a.rest op v" becomes
//     ("a", "any", [rest op v AND rule]) and an any's nested domain is ANDed with the rule.
//     Without a rule the dotted leaf is kept as is, so plain paths compile exactly as before.
//   Record-rule domains themselves are trusted and never rewritten.

import (
	"context"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// scopeDomainTraversals returns node with every relational traversal scoped to the caller's read
// access on the target model.
func scopeDomainTraversals(ctx context.Context, model *ModelMetadata, node DomainNode) (DomainNode, error) {
	if model == nil || !rbacPolicyApplies(EnvironmentFromContext(ctx)) {
		return node, nil
	}
	return scopeDomainNode(ctx, model, node)
}

func scopeDomainNode(ctx context.Context, model *ModelMetadata, node DomainNode) (DomainNode, error) {
	switch node.Kind {
	case DomainKindAnd, DomainKindOr, DomainKindNot:
		scoped := node
		scoped.Children = make([]DomainNode, 0, len(node.Children))
		for _, child := range node.Children {
			next, err := scopeDomainNode(ctx, model, child)
			if err != nil {
				return DomainNode{}, err
			}
			scoped.Children = append(scoped.Children, next)
		}
		return scoped, nil
	case DomainKindCondition:
		if isAnyOperator(node.Operator) {
			return scopeAnyCondition(ctx, model, node)
		}
		return scopePathCondition(ctx, model, node)
	default:
		return node, nil
	}
}

// scopeAnyCondition scopes ("rel", "any", child) to the relation's readable rows.
func scopeAnyCondition(ctx context.Context, model *ModelMetadata, node DomainNode) (DomainNode, error) {
	field, ok := runtimeLookupField(model, node.Field)
	if !ok {
		return node, nil // the normalizer reports unknown fields
	}
	target, ok := LookupSharedModel(field.Relation)
	if !ok {
		return node, nil
	}
	child, ok := node.Value.(DomainNode)
	if !ok {
		return node, nil
	}
	rule, readable, err := domainTargetReadScope(ctx, target)
	if err != nil {
		return DomainNode{}, err
	}
	if !readable {
		return domainTraversalDenied(ctx, model, node, target), nil
	}
	scopedChild, err := scopeDomainNode(ctx, target, child)
	if err != nil {
		return DomainNode{}, err
	}
	scoped := node
	scoped.Value = andDomainRule(scopedChild, rule)
	return scoped, nil
}

// scopePathCondition scopes a dotted leaf whose first segment is a many2one/one2one.
func scopePathCondition(ctx context.Context, model *ModelMetadata, node DomainNode) (DomainNode, error) {
	first, rest, dotted := strings.Cut(strings.TrimSpace(node.Field), ".")
	if !dotted {
		return node, nil
	}
	field, ok := runtimeLookupField(model, first)
	if !ok || (field.Kind != FieldKindManyToOne && field.Kind != FieldKindOneToOne) {
		return node, nil // json sub-keys stay on the same row; errors are the normalizer's job
	}
	target, ok := LookupSharedModel(field.Relation)
	if !ok {
		return node, nil
	}
	rule, readable, err := domainTargetReadScope(ctx, target)
	if err != nil {
		return DomainNode{}, err
	}
	if !readable {
		return domainTraversalDenied(ctx, model, node, target), nil
	}
	inner := node
	inner.Field = rest
	scopedInner, err := scopeDomainNode(ctx, target, inner)
	if err != nil {
		return DomainNode{}, err
	}
	if isTrivialDomain(rule) && scopedInner.Field == rest && scopedInner.Kind == DomainKindCondition && !isAnyOperator(scopedInner.Operator) {
		// Nothing to restrict on this hop or below: keep the original path (unchanged SQL).
		return node, nil
	}
	return DomainNode{
		Kind:     DomainKindCondition,
		Field:    field.Name,
		Operator: "any",
		Value:    andDomainRule(scopedInner, rule),
	}, nil
}

// domainTargetReadScope reports whether the caller may read target and its read record-rule
// domain (TrueDomain when none applies).
func domainTargetReadScope(ctx context.Context, target *ModelMetadata) (DomainNode, bool, error) {
	if err := ensureModelAccess(ctx, EnvironmentFromContext(ctx), target, "read", nil, nil); err != nil {
		return DomainNode{}, false, nil
	}
	if !shouldEnforceRecordRules(ctx, target) {
		return TrueDomain(), true, nil
	}
	rule, err := recordRuleDomain(ctx, EnvironmentFromContext(ctx), target, "read")
	if err != nil {
		return DomainNode{}, false, err
	}
	if rule.Kind == "" {
		rule = TrueDomain()
	}
	return rule, true, nil
}

func domainTraversalDenied(ctx context.Context, model *ModelMetadata, node DomainNode, target *ModelMetadata) DomainNode {
	env := EnvironmentFromContext(ctx)
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	log.Warnf("orm: user %d domain %q on %s traverses %s without read access; condition is false", userID, node.Field, model.Name, target.Name)
	return FalseDomain()
}

func isTrivialDomain(node DomainNode) bool {
	return node.Kind == "" || node.Kind == DomainKindTrue
}

func andDomainRule(node, rule DomainNode) DomainNode {
	if isTrivialDomain(rule) {
		return node
	}
	return And(node, rule)
}
