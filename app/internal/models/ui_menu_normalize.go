package models

// File overview:
// - Responsibility: normalises ui.menu create/write values and their action references.
// - Key symbols: `normalizeUIMenuValues`, `normalizeUIMenuAction`, `normalizeUIMenuWindowAction`, `normalizeUIMenuClientAction`.

import (
	"context"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

func normalizeUIMenuValues(ctx context.Context, values orm.RecordMap, fallbackURL string, creating bool) (orm.RecordMap, error) {
	// Clone caller-owned values so normalization never mutates transport-owned
	// maps that might be reused by tests or higher layers.
	normalized := cloneRecordMap(values)

	// Enforce a non-empty display name whenever the caller sets one, and also on
	// create so the menu tree cannot end up with blank labels.
	if nameRaw, ok := normalized["name"]; ok {
		name := strings.TrimSpace(uiMenuNameText(nameRaw))
		if name == "" {
			log.Errorf("ui.menu name is required")
			return nil, fmt.Errorf("ui.menu name is required")
		}
		// A {lang: value} payload stays an envelope (the ORM writes every key);
		// only a plain value is replaced by its trimmed text.
		if envelope, isEnvelope := nameRaw.(map[string]any); isEnvelope {
			normalized["name"] = trimUIMenuNameEnvelope(envelope)
		} else {
			normalized["name"] = name
		}
	} else if creating {
		log.Errorf("ui.menu name is required")
		return nil, fmt.Errorf("ui.menu name is required")
	}

	// Apply stable defaults up front so both insert and update paths converge on
	// the same stored values when callers omit optional fields.
	if creating {
		if _, ok := normalized["active"]; !ok {
			normalized["active"] = true
		}
		if _, ok := normalized["sequence"]; !ok {
			normalized["sequence"] = int64(10)
		}
	}

	// Canonicalize supported action payloads and route_path together because the
	// sidebar should navigate to the same destination regardless of which field
	// callers happened to provide.
	actionValue, hasAction := normalized["action"]
	routePath := strings.TrimSpace(stringValue(normalized["route_path"]))
	if !hasAction && routePath == "" {
		routePath = strings.TrimSpace(fallbackURL)
	}
	action, routePath, err := normalizeUIMenuAction(actionValue, routePath)
	if err != nil {
		return nil, err
	}
	if action != nil {
		normalized["action"] = action
		normalized["route_path"] = routePath
	} else {
		delete(normalized, "action")
		if routePath != "" {
			normalized["route_path"] = routePath
		}
	}

	// Resolve the stored complete_name during write time so list/search clients
	// can sort or display the hierarchical label without recomputing it.
	if nameRaw, ok := normalized["name"]; ok {
		parentID, _, err := optionalInt64FromRecord(normalized, "parent_id")
		if err != nil {
			return nil, err
		}
		completeName, err := computeUIMenuCompleteName(ctx, strings.TrimSpace(uiMenuNameText(nameRaw)), parentID)
		if err != nil {
			return nil, err
		}
		normalized["complete_name"] = completeName
	}
	return normalized, nil
}

func normalizeUIMenuAction(rawAction any, routePath string) (map[string]any, string, error) {
	// Empty action plus empty route keeps the row as a pure branch menu.
	if rawAction == nil && strings.TrimSpace(routePath) == "" {
		return nil, "", nil
	}

	// Accept bare route_path-only payloads and turn them into the same stored
	// URL action shape used for explicit action dictionaries.
	if rawAction == nil {
		url := strings.TrimSpace(routePath)
		if url == "" {
			return nil, "", nil
		}
		return map[string]any{
			"type":   "ui.actions.url",
			"url":    url,
			"target": "current",
		}, url, nil
	}

	// Canonicalize action maps strictly so unsupported shapes fail fast instead
	// of leaking into the sidebar contract.
	actionMap, err := actionMapValue(rawAction)
	if err != nil {
		return nil, "", err
	}
	actionType := strings.TrimSpace(stringValue(actionMap["type"]))
	if actionType == "" {
		actionType = "ui.actions.url"
	}
	switch actionType {
	case "ui.actions.url":
		url := strings.TrimSpace(stringValue(actionMap["url"]))
		if url == "" {
			url = strings.TrimSpace(routePath)
		}
		if url == "" {
			log.Errorf("ui.menu URL action requires url")
			return nil, "", fmt.Errorf("ui.menu URL action requires url")
		}
		target := strings.TrimSpace(stringValue(actionMap["target"]))
		if target == "" {
			target = "current"
		}
		return map[string]any{
			"type":   actionType,
			"url":    url,
			"target": target,
		}, url, nil
	case UIActionWindowModelName:
		return normalizeUIMenuWindowAction(actionMap, routePath)
	case UIActionClientModelName:
		return normalizeUIMenuClientAction(actionMap, routePath)
	default:
		log.Errorf("ui.menu unsupported action type %q", actionType)
		return nil, "", fmt.Errorf("ui.menu unsupported action type %q", actionType)
	}
}

func normalizeUIMenuWindowAction(actionMap map[string]any, routePath string) (map[string]any, string, error) {
	out := map[string]any{
		"type": UIActionWindowModelName,
	}
	if id, ok := int64FromAny(actionMap["id"]); ok && id > 0 {
		out["id"] = id
	}
	if bidValue := strings.TrimSpace(stringValue(actionMap["bid"])); bidValue != "" {
		out["bid"] = bidValue
	}
	resModel := strings.TrimSpace(stringValue(actionMap["sys_model"]))
	if resModel != "" {
		out["sys_model"] = resModel
	}
	if name := strings.TrimSpace(stringValue(actionMap["name"])); name != "" {
		out["name"] = name
	}
	url := strings.TrimSpace(stringValue(actionMap["url"]))
	if url == "" {
		url = strings.TrimSpace(routePath)
	}
	if url == "" {
		url = windowActionRoutePath(resModel)
	}
	if url != "" {
		out["url"] = url
	}
	if _, hasID := out["id"]; !hasID && strings.TrimSpace(stringValue(out["bid"])) == "" {
		log.Errorf("ui.menu ui.actions.window action requires id or bid")
		return nil, "", fmt.Errorf("ui.menu ui.actions.window action requires id or bid")
	}
	return out, url, nil
}

func normalizeUIMenuClientAction(actionMap map[string]any, routePath string) (map[string]any, string, error) {
	out := map[string]any{
		"type": UIActionClientModelName,
	}
	if id, ok := int64FromAny(actionMap["id"]); ok && id > 0 {
		out["id"] = id
	}
	if bidValue := strings.TrimSpace(stringValue(actionMap["bid"])); bidValue != "" {
		out["bid"] = bidValue
	}
	if tag := strings.TrimSpace(stringValue(actionMap["tag"])); tag != "" {
		out["tag"] = tag
	}
	if name := strings.TrimSpace(stringValue(actionMap["name"])); name != "" {
		out["name"] = name
	}
	if resModel := strings.TrimSpace(stringValue(actionMap["sys_model"])); resModel != "" {
		out["sys_model"] = resModel
	}
	if url := strings.TrimSpace(firstNonEmpty(stringValue(actionMap["url"]), routePath)); url != "" {
		out["url"] = url
	}
	if _, hasID := out["id"]; !hasID && strings.TrimSpace(stringValue(out["bid"])) == "" {
		log.Errorf("ui.menu ui.actions.client action requires id or bid")
		return nil, "", fmt.Errorf("ui.menu ui.actions.client action requires id or bid")
	}
	return out, strings.TrimSpace(stringValue(out["url"])), nil
}

// uiMenuNameText returns the text a name payload stands for: the value itself,
// or the en base of a {lang: value} translation envelope (the language the
// stored complete_name is derived from).
func uiMenuNameText(raw any) string {
	if envelope, ok := raw.(map[string]any); ok {
		return stringValue(envelope["en"])
	}
	return stringValue(raw)
}

// trimUIMenuNameEnvelope trims every language value and keeps null entries
// (a null removes that language key on write).
func trimUIMenuNameEnvelope(envelope map[string]any) map[string]any {
	out := make(map[string]any, len(envelope))
	for lang, value := range envelope {
		if value == nil {
			out[lang] = nil
			continue
		}
		out[lang] = strings.TrimSpace(stringValue(value))
	}
	return out
}
