// ui_view_request.go: argument parsing for the ui.view RPC methods, split out of ui_view.go.
package models

import (
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

type uiViewRequest struct {
	Model    string
	ViewType string
	ViewID   int64
}

type uiViewsRequest struct {
	Model     string
	ViewTypes []string
	ViewIDs   map[string]int64
}

type uiViewCompareRequest struct {
	ViewID        int64
	Mode          string
	CompareViewID int64
}

// resolveUIViewID resolves the ui view id handled by this package.
func resolveUIViewID(ids []string, args []any, kwargs map[string]any) (int64, error) {
	if len(ids) > 0 {
		return parsePositiveInt64(ids[0])
	}
	if len(args) > 0 {
		if value, ok := uiViewIDFromAny(args[0]); ok && value > 0 {
			return value, nil
		}
	}
	if kwargs != nil {
		if value, ok := uiViewIDFromAny(kwargs["view_id"]); ok && value > 0 {
			return value, nil
		}
	}
	log.Errorf("ui.view requires a positive view id")
	return 0, fmt.Errorf("ui.view requires a positive view id")
}

func uiViewIDFromAny(raw any) (int64, bool) {
	if value, ok := int64FromAny(raw); ok && value > 0 {
		return value, true
	}
	switch typed := raw.(type) {
	case []any:
		if len(typed) == 0 {
			return 0, false
		}
		// Accept legacy RPC shapes such as [[117]] where the first positional
		// argument still carries a one-record id list.
		return uiViewIDFromAny(typed[0])
	case []string:
		if len(typed) == 0 {
			return 0, false
		}
		return uiViewIDFromAny(typed[0])
	default:
		return 0, false
	}
}

// parseUIViewRequest parses the ui view request handled by this package.
func parseUIViewRequest(args []any, kwargs map[string]any) (uiViewRequest, error) {
	request := uiViewRequest{
		Model:    firstNonEmpty(stringArg(args, 0), stringMapValue(kwargs, "model"), stringMapValue(kwargs, "model_name")),
		ViewType: normalizeViewType(firstNonEmpty(stringArg(args, 1), stringMapValue(kwargs, "view_type"), "form")),
	}
	if request.Model == "" {
		log.Errorf("ui.view.get_view requires model")
		return uiViewRequest{}, fmt.Errorf("ui.view.get_view requires model")
	}
	if viewID, ok := int64FromAny(firstNonNil(argValue(args, 2), mapValue(kwargs, "view_id"))); ok {
		request.ViewID = viewID
	}
	return request, nil
}

// parseUIViewsRequest parses the ui views request handled by this package.
func parseUIViewsRequest(args []any, kwargs map[string]any) (uiViewsRequest, error) {
	request := uiViewsRequest{
		Model:   firstNonEmpty(stringArg(args, 0), stringMapValue(kwargs, "model"), stringMapValue(kwargs, "model_name")),
		ViewIDs: map[string]int64{},
	}
	if request.Model == "" {
		log.Errorf("ui.view.get_views requires model")
		return uiViewsRequest{}, fmt.Errorf("ui.view.get_views requires model")
	}
	request.ViewTypes = stringSlice(firstNonNil(argValue(args, 1), mapValue(kwargs, "view_types")))
	if len(request.ViewTypes) == 0 {
		request.ViewTypes = []string{"list", "form"}
	}
	if rawMap, ok := firstNonNil(argValue(args, 2), mapValue(kwargs, "view_ids")).(map[string]any); ok {
		for viewType, rawID := range rawMap {
			if viewID, ok := int64FromAny(rawID); ok {
				request.ViewIDs[normalizeViewType(viewType)] = viewID
			}
		}
	}
	for i := range request.ViewTypes {
		request.ViewTypes[i] = normalizeViewType(request.ViewTypes[i])
	}
	return request, nil
}

// parseUIViewCompareRequest parses the ui view compare request handled by this package.
func parseUIViewCompareRequest(ids []string, args []any, kwargs map[string]any) (uiViewCompareRequest, error) {
	request := uiViewCompareRequest{
		Mode: strings.TrimSpace(firstNonEmpty(
			stringArg(args, 0),
			stringMapValue(kwargs, "mode"),
			stringMapValue(kwargs, "reset_mode"),
			"soft",
		)),
	}
	viewID, err := resolveUIViewID(ids, nil, kwargs)
	if err != nil {
		log.Errorf("ui.view.compare_arch requires ids: %v", err)
		return uiViewCompareRequest{}, fmt.Errorf("ui.view.compare_arch requires ids: %w", err)
	}
	request.ViewID = viewID
	if compareViewID, ok := int64FromAny(firstNonNil(argValue(args, 1), mapValue(kwargs, "compare_view_id"))); ok {
		request.CompareViewID = compareViewID
	}
	return request, nil
}

// It parses kwargs["domain"] through the shared orm.ParseDomain domain-tree
// parser (instead of a hand-rolled "=" / "in"-only reader) so ui.view search_read
// supports the same operators (ilike, !=, and/or/not, ...) as every other model's
// generic search_read path.
func searchFilterFromKwargs(kwargs map[string]any) orm.RecordMap {
	filter := orm.RecordMap{}
	if kwargs == nil {
		return filter
	}
	if domain, err := orm.ParseDomain(kwargs["domain"]); err == nil && domain.Kind != "" && domain.Kind != orm.DomainKindTrue {
		filter["__domain"] = domain
	}
	if order, ok := stringFromAny(kwargs["order"]); ok && order != "" {
		filter["__order"] = order
	}
	if limit, ok := int64FromAny(kwargs["limit"]); ok && limit > 0 {
		filter["__limit"] = limit
	}
	if offset, ok := int64FromAny(kwargs["offset"]); ok && offset >= 0 {
		filter["__offset"] = offset
	}
	return filter
}

// mergeUIViewDomain ANDs a positional (args[0]) domain onto filter's existing
// __domain (set from kwargs, if any) rather than overwriting it, so callers
// that pass both a kwargs domain and a positional one get the intersection.
func mergeUIViewDomain(filter orm.RecordMap, raw any) error {
	domain, err := orm.ParseDomain(raw)
	if err != nil {
		return err
	}
	if domain.Kind == "" || domain.Kind == orm.DomainKindTrue {
		return nil
	}
	if existing, ok := filter["__domain"].(orm.DomainNode); ok && existing.Kind != "" {
		filter["__domain"] = orm.And(existing, domain)
		return nil
	}
	filter["__domain"] = domain
	return nil
}
