// ui_view_resolve.go: resolution of a ui.view row into its combined arch (primary, extensions, chatter), split out of ui_view.go.
package models

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

type sysUIViewRow struct {
	bun.BaseModel `bun:"table:ui_view,alias:ui_view"`
	ID            int64  `bun:"id"`
	Name          string `bun:"name"`
	Model         string `bun:"model"`
	Key           string `bun:"key"`
	Priority      int64  `bun:"priority"`
	Type          string `bun:"type"`
	Arch          string `bun:"arch"`
	ArchBase      string `bun:"arch_base"`
	ArchDB        string `bun:"arch_db"`
	ArchFS        string `bun:"arch_fs"`
	ArchUpdated   bool   `bun:"arch_updated"`
	ArchPrev      string `bun:"arch_prev"`
	InheritID     *int64 `bun:"inherit_id"`
	Mode          string `bun:"mode"`
	Active        bool   `bun:"active"`
	UseChatter    bool   `bun:"use_chatter"`
	Module        string `bun:"module"`
	Sequence      int64  `bun:"sequence"`
}

type resolvedUIView struct {
	ViewID       any
	Name         string
	Model        string
	Type         string
	Arch         string
	PrimaryID    int64
	ExtensionIDs []int64
	Source       string
}

// buildUIViewResponse builds the ui view response handled by this package.
func buildUIViewResponse(ctx context.Context, request uiViewRequest) (map[string]any, error) {
	resolved, meta, err := resolveUIView(ctx, request)
	if err != nil {
		return nil, err
	}
	pattern, usedFields, err := patternFromArch(resolved.Arch, meta)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"view_id":     resolved.ViewID,
		"name":        resolved.Name,
		"model":       resolved.Model,
		"type":        resolved.Type,
		"arch":        resolved.Arch,
		"fields":      uiViewFieldMap(meta),
		"used_fields": usedFields,
		"pattern":     pattern,
		"meta": map[string]any{
			"source":          resolved.Source,
			"primary_view_id": resolved.PrimaryID,
			"extension_ids":   resolved.ExtensionIDs,
		},
	}, nil
}

// resolveUIView resolves the ui view handled by this package.
func resolveUIView(ctx context.Context, request uiViewRequest) (resolvedUIView, *orm.ModelMetadata, error) {
	modelMeta, ok := orm.LookupModel(request.Model)
	if !ok {
		log.Errorf("ui.view unknown model %q", request.Model)
		return resolvedUIView{}, nil, fmt.Errorf("ui.view unknown model %q", request.Model)
	}
	viewType := normalizeViewType(request.ViewType)
	if viewType == "" {
		viewType = "form"
	}

	if request.ViewID > 0 {
		row, err := loadUIViewRowByID(ctx, request.ViewID)
		if err != nil {
			return resolvedUIView{}, nil, err
		}
		resolved, err := resolveStoredUIView(ctx, row)
		if err != nil {
			return resolvedUIView{}, nil, err
		}
		if resolved.Model == "" {
			resolved.Model = request.Model
		}
		if resolved.Type == "" {
			resolved.Type = viewType
		}
		return resolved, modelMeta, nil
	}

	row, err := loadDefaultUIView(ctx, request.Model, viewType)
	if err == nil {
		resolved, err := resolveStoredUIView(ctx, row)
		if err != nil {
			return resolvedUIView{}, nil, err
		}
		if resolved.Model == "" {
			resolved.Model = request.Model
		}
		if resolved.Type == "" {
			resolved.Type = viewType
		}
		return resolved, modelMeta, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isUIViewDBUnavailable(err) {
		return resolvedUIView{}, nil, err
	}
	return resolvedUIView{
		ViewID:       false,
		Name:         generatedViewName(modelMeta, viewType),
		Model:        request.Model,
		Type:         viewType,
		Arch:         synthesizeUIViewArch(modelMeta, viewType),
		PrimaryID:    0,
		ExtensionIDs: []int64{},
		Source:       "generated",
	}, modelMeta, nil
}

// loadDefaultUIView loads the default ui view handled by this package.
func loadDefaultUIView(ctx context.Context, modelName, viewType string) (sysUIViewRow, error) {
	db, err := requireUIViewDB(ctx)
	if err != nil {
		return sysUIViewRow{}, err
	}
	var row sysUIViewRow
	err = db.NewSelect().
		Model(&row).
		Where("model = ?", modelName).
		Where("type = ?", viewType).
		Where("mode = ?", "primary").
		Where("active = ?", true).
		OrderExpr("priority ASC").
		OrderExpr("id ASC").
		Limit(1).
		Scan(ctx)
	return row, err
}

// loadUIViewRowByID loads the ui view row by id handled by this package.
func loadUIViewRowByID(ctx context.Context, viewID int64) (sysUIViewRow, error) {
	db, err := requireUIViewDB(ctx)
	if err != nil {
		return sysUIViewRow{}, err
	}
	var row sysUIViewRow
	err = db.NewSelect().
		Model(&row).
		Where("id = ?", viewID).
		Limit(1).
		Scan(ctx)
	return row, err
}

// resolveStoredUIView resolves the stored ui view handled by this package.
func resolveStoredUIView(ctx context.Context, row sysUIViewRow) (resolvedUIView, error) {
	primary, err := resolvePrimaryUIView(ctx, row)
	if err != nil {
		return resolvedUIView{}, err
	}

	// Start from the primary view architecture and fold extensions on top of it
	// in order so the final XML matches the inheritance chain the UI expects.
	merged := firstNonEmpty(rowArch(primary))
	if merged == "" {
		log.Errorf("ui.view %d has empty arch", primary.ID)
		return resolvedUIView{}, fmt.Errorf("ui.view %d has empty arch", primary.ID)
	}

	extensions, err := loadUIViewExtensions(ctx, primary.ID, primary.Model)
	if err != nil {
		return resolvedUIView{}, err
	}
	extensionIDs := make([]int64, 0, len(extensions))
	for _, extension := range extensions {
		arch := rowArch(extension)
		if arch == "" {
			continue
		}
		// Each extension is merged into the latest combined arch so sibling
		// overrides see the result of earlier patches instead of the base XML.
		merged, err = mergeUIViewArch(merged, arch)
		if err != nil {
			return resolvedUIView{}, err
		}
		extensionIDs = append(extensionIDs, extension.ID)
	}

	// A form view flagged use_chatter docks the discuss chatter panel via the
	// same xpath-patch machinery a checked-in extension view would use,
	// without requiring one — set the flag on the primary view row instead of
	// authoring a separate ui.view record per model.
	if primary.UseChatter && !strings.Contains(merged, "<chatter") {
		patched, err := mergeUIViewArch(merged, chatterXPathExtensionArch)
		// A non-form view (or one with no <sheet> node) can't anchor the
		// xpath patch; log and keep the unpatched arch rather than failing
		// the whole view resolution over an optional attachment.
		if err != nil {
			log.Errorf("ui.view %d use_chatter patch failed: %v", primary.ID, err)
		} else {
			merged = patched
		}
	}

	return resolvedUIView{
		ViewID:       row.ID,
		Name:         firstNonEmpty(row.Name, primary.Name),
		Model:        firstNonEmpty(row.Model, primary.Model),
		Type:         normalizeViewType(firstNonEmpty(row.Type, primary.Type)),
		Arch:         merged,
		PrimaryID:    primary.ID,
		ExtensionIDs: extensionIDs,
		Source:       "stored",
	}, nil
}

// chatterXPathExtensionArch docks the discuss chatter panel onto a form
// view's //sheet node — the same patch a checked-in extension ui.view row
// would apply, expressed as a synthetic extension so use_chatter can reuse
// mergeUIViewArch/applyViewOperation instead of a second mutation path.
const chatterXPathExtensionArch = `<data><xpath expr="//sheet" position="after"><chatter/></xpath></data>`

// resolvePrimaryUIView resolves the primary ui view handled by this package.
func resolvePrimaryUIView(ctx context.Context, row sysUIViewRow) (sysUIViewRow, error) {
	current := row
	seen := map[int64]struct{}{}
	for current.InheritID != nil && strings.EqualFold(strings.TrimSpace(current.Mode), "extension") {
		if _, ok := seen[current.ID]; ok {
			log.Errorf("ui.view inheritance cycle detected at %d", current.ID)
			return sysUIViewRow{}, fmt.Errorf("ui.view inheritance cycle detected at %d", current.ID)
		}
		seen[current.ID] = struct{}{}
		parent, err := loadUIViewRowByID(ctx, *current.InheritID)
		if err != nil {
			return sysUIViewRow{}, err
		}
		// Walk upward until a non-extension record becomes the canonical primary
		// view that contributes the base architecture for later merges.
		current = parent
	}
	return current, nil
}

// loadUIViewExtensions loads the ui view extensions handled by this package.
func loadUIViewExtensions(ctx context.Context, parentID int64, modelName string) ([]sysUIViewRow, error) {
	db, err := requireUIViewDB(ctx)
	if err != nil {
		return nil, err
	}
	rows := make([]sysUIViewRow, 0)
	query := db.NewSelect().
		Model(&rows).
		Where("inherit_id = ?", parentID).
		Where("active = ?", true).
		OrderExpr("priority ASC").
		OrderExpr("id ASC")
	if strings.TrimSpace(modelName) != "" {
		query = query.Where("(model = ? OR COALESCE(model, '') = '')", modelName)
	}
	if err := query.Scan(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}

	out := make([]sysUIViewRow, 0)
	for _, row := range rows {
		if strings.EqualFold(strings.TrimSpace(row.Mode), "extension") {
			out = append(out, row)
		}
		children, err := loadUIViewExtensions(ctx, row.ID, modelName)
		if err != nil {
			return nil, err
		}
		out = append(out, children...)
	}
	return out, nil
}

// rowArch returns the effective architecture XML for a stored UI view row.
// It prefers the database override first and then falls back through the static architecture fields in priority order.
func rowArch(row sysUIViewRow) string {
	return strings.TrimSpace(firstNonEmpty(row.ArchDB, row.Arch, row.ArchBase))
}

// buildUIViewArchComparison builds the ui view arch comparison handled by this package.
func buildUIViewArchComparison(ctx context.Context, current sysUIViewRow, request uiViewCompareRequest) (map[string]any, error) {
	currentArch := rowArch(current)
	compareArch := ""
	compareName := ""
	switch request.Mode {
	case "", "soft":
		compareArch = strings.TrimSpace(current.ArchPrev)
		compareName = "Previous Arch"
	case "hard":
		archFS := strings.TrimSpace(current.ArchFS)
		if archFS != "" {
			arch, err := readUIViewArchFS(archFS)
			if err != nil {
				return nil, err
			}
			compareArch = arch
		}
		compareName = "File Arch"
	case "other_view":
		if request.CompareViewID <= 0 {
			log.Errorf("ui.view.compare_arch mode other_view requires compare_view_id")
			return nil, fmt.Errorf("ui.view.compare_arch mode other_view requires compare_view_id")
		}
		compareView, err := loadUIViewRowByID(ctx, request.CompareViewID)
		if err != nil {
			return nil, err
		}
		compareArch = rowArch(compareView)
		compareName = uiViewDisplayName(compareView)
	default:
		log.Errorf("ui.view.compare_arch mode must be soft, hard, or other_view")
		return nil, fmt.Errorf("ui.view.compare_arch mode must be soft, hard, or other_view")
	}

	formattedCurrent := formatUIViewArchForCompare(currentArch)
	formattedCompare := formatUIViewArchForCompare(compareArch)
	return map[string]any{
		"view_id":         current.ID,
		"reset_mode":      request.Mode,
		"current_name":    uiViewDisplayName(current),
		"current_arch":    currentArch,
		"compare_name":    compareName,
		"arch_to_compare": compareArch,
		"has_diff":        formattedCurrent != formattedCompare,
		// Keep the payload HTML-friendly so the frontend can drop it into the
		// existing read-only diff surface without re-running a backend compare.
		"arch_diff": buildUIViewArchDiffHTML(
			firstNonEmpty(formattedCurrent, currentArch),
			firstNonEmpty(formattedCompare, compareArch),
		),
	}, nil
}

func uiViewDisplayName(row sysUIViewRow) string {
	name := strings.TrimSpace(firstNonEmpty(row.Name, fmt.Sprintf("View %d", row.ID)))
	if key := strings.TrimSpace(row.Key); key != "" {
		return fmt.Sprintf("%s (%s)", name, key)
	}
	return name
}
