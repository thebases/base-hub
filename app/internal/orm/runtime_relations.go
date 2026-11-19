package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime relations, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `normalizeX2ManyCommands`, `normalizeX2ManyCommandsForField`, `x2ManyCommand`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Uses Bun-facing model or repository patterns so schema mapping stays explicit and transaction-aware inside the backend data layer.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

type x2ManyCommand struct {
	// op follows the relation command convention:
	// 0 create, 1 update, 2 delete target, 3 unlink relation,
	// 4 link existing target, 5 clear all, 6 replace with ids.
	op     int
	id     string
	values RecordMap
	ids    []string
}

func normalizeX2ManyCommands(raw any) []x2ManyCommand {
	switch typed := raw.(type) {
	case []x2ManyCommand:
		commands := make([]x2ManyCommand, 0, len(typed))
		for _, command := range typed {
			commands = append(commands, x2ManyCommand{
				op:     command.op,
				id:     command.id,
				values: cloneRecordMap(command.values),
				ids:    append([]string(nil), command.ids...),
			})
		}
		return commands
	case []any:
		// The RPC-style payload is usually a list of 3-tuples describing x2many
		// mutations, so preserve that convention first.
		commands := make([]x2ManyCommand, 0, len(typed))
		for _, item := range typed {
			if tuple, ok := item.([]any); ok && len(tuple) >= 3 {
				command := x2ManyCommand{op: int(toInt64(tuple[0])), id: fmt.Sprint(tuple[1])}
				if values, err := anyToRecordMap(tuple[2]); err == nil && values != nil {
					// Commands that create or update carry a values map.
					command.values = values
				} else {
					// Link/replace-style commands carry one or more target ids.
					command.ids = stringSliceArg(tuple[2])
				}
				commands = append(commands, command)
			}
		}
		return commands
	case []string:
		// A bare id list is treated as "replace with these ids" for convenience.
		return []x2ManyCommand{{op: 6, ids: append([]string(nil), typed...)}}
	case [][2]any:
		ids := make([]string, 0, len(typed))
		for _, tuple := range typed {
			id, ok := runtimeStringID(tuple[0])
			if !ok {
				return nil
			}
			ids = append(ids, id)
		}
		return []x2ManyCommand{{op: 6, ids: ids}}
	case []int64:
		ids := make([]string, 0, len(typed))
		for _, id := range typed {
			ids = append(ids, fmt.Sprint(id))
		}
		return []x2ManyCommand{{op: 6, ids: ids}}
	case RecordMap:
		return []x2ManyCommand{{op: 0, values: typed}}
	case map[string]any:
		return []x2ManyCommand{{op: 0, values: RecordMap(typed)}}
	default:
		return nil
	}
}

func normalizeX2ManyCommandsForField(field FieldMetadata, raw any) []x2ManyCommand {
	commands := normalizeX2ManyCommands(raw)
	if len(commands) > 0 || field.Kind != FieldKindManyToMany {
		return commands
	}
	// Many2many fields also accept a plain list of ids, so synthesize the
	// equivalent replace command when the caller skipped tuple syntax.
	ids := relationIDStrings(raw)
	if len(ids) == 0 {
		return nil
	}
	return []x2ManyCommand{{op: 6, ids: ids}}
}

func relationIDStrings(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return append([]string(nil), typed...)
	case [][2]any:
		ids := make([]string, 0, len(typed))
		for _, tuple := range typed {
			id, ok := runtimeStringID(tuple[0])
			if !ok {
				return nil
			}
			ids = append(ids, id)
		}
		return ids
	case []int64:
		ids := make([]string, 0, len(typed))
		for _, id := range typed {
			ids = append(ids, fmt.Sprint(id))
		}
		return ids
	case []int:
		ids := make([]string, 0, len(typed))
		for _, id := range typed {
			ids = append(ids, fmt.Sprint(id))
		}
		return ids
	case []any:
		ids := make([]string, 0, len(typed))
		for _, item := range typed {
			id, ok := relatedRecordID(item)
			if !ok {
				return nil
			}
			ids = append(ids, id)
		}
		return ids
	default:
		return nil
	}
}

func (h *Handler[T]) applyManyToManyCommands(ctx context.Context, field FieldMetadata, recordID string, commands []x2ManyCommand) error {
	db := runtimeDB(ctx, h)
	if h.repo == nil || db == nil || field.RelationTable == "" {
		return nil
	}
	// Resolve the related model once so every command in the batch shares the
	// same executor and metadata view.
	relationExecutor, relationModel, err := h.relatedRuntime(field)
	if err != nil {
		return err
	}
	for _, command := range commands {
		switch command.op {
		case 0:
			// Create the related record first, then link it through the relation table.
			created, err := relationExecutor.RuntimeCreate(ctx, cloneRecordMap(command.values))
			if err != nil {
				return err
			}
			targetID, ok := runtimeStringID(created["id"])
			if !ok {
				log.Errorf("orm: related create for %s returned no id", relationModel.Name)
				return fmt.Errorf("orm: related create for %s returned no id", relationModel.Name)
			}
			if err := h.applyManyToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 4, id: targetID}}); err != nil {
				return err
			}
		case 1:
			// Update mutates the related record itself and leaves the join row intact.
			if _, err := relationExecutor.RuntimeWrite(ctx, []string{command.id}, cloneRecordMap(command.values)); err != nil {
				return err
			}
		case 2:
			// Delete is a two-step operation: remove the join, then delete the target row.
			if err := h.applyManyToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 3, id: command.id}}); err != nil {
				return err
			}
			if _, err := relationExecutor.RuntimeUnlink(ctx, []string{command.id}); err != nil {
				return err
			}
		case 3:
			// Unlink only removes one row from the relation table.
			if _, err := db.NewDelete().
				TableExpr("?", bun.Ident(field.RelationTable)).
				Where("? = ?", bun.Ident(field.RelationColumn), toInt64(recordID)).
				Where("? = ?", bun.Ident(field.RelationKey), toInt64(command.id)).
				Exec(ctx); err != nil {
				return err
			}
		case 4:
			// Link inserts one join-table row pointing at an existing target id.
			if _, err := db.NewInsert().
				TableExpr("?", bun.Ident(field.RelationTable)).
				Model(&map[string]any{field.RelationColumn: toInt64(recordID), field.RelationKey: toInt64(command.id)}).
				Exec(ctx); err != nil {
				return err
			}
		case 5:
			// Clear removes all joins for the current source record.
			if _, err := db.NewDelete().
				TableExpr("?", bun.Ident(field.RelationTable)).
				Where("? = ?", bun.Ident(field.RelationColumn), toInt64(recordID)).
				Exec(ctx); err != nil {
				return err
			}
		case 6:
			// Replace is intentionally modeled as clear then relink so the final
			// join-table state matches the provided id set exactly.
			if err := h.applyManyToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 5}}); err != nil {
				return err
			}
			for _, targetID := range command.ids {
				if err := h.applyManyToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 4, id: targetID}}); err != nil {
					return err
				}
			}
		}
	}
	// Any relation mutation invalidates cached relation-derived fields for the source record.
	h.invalidateRelationCaches(ctx, field, []string{recordID}, commands)
	return nil
}

func (h *Handler[T]) applyOneToManyCommands(ctx context.Context, field FieldMetadata, recordID string, commands []x2ManyCommand) error {
	if h.repo == nil || strings.TrimSpace(field.Relation) == "" {
		return nil
	}
	// The inverse field on the child model is the actual stored foreign key, so
	// every one2many command is translated into writes on that child field.
	childExecutor, childModel, err := h.relatedRuntime(field)
	if err != nil {
		return err
	}
	inverse, err := childModel.Field(field.Inverse)
	if err != nil {
		return err
	}
	for _, command := range commands {
		switch command.op {
		case 0:
			// Create attaches the new child to the current parent via the inverse key.
			values := cloneRecordMap(command.values)
			values[inverse.Name] = toInt64(recordID)
			if _, err := childExecutor.RuntimeCreate(ctx, values); err != nil {
				return err
			}
		case 1:
			// Update also keeps the inverse key pinned so the child cannot drift away.
			values := cloneRecordMap(command.values)
			values[inverse.Name] = toInt64(recordID)
			if _, err := childExecutor.RuntimeWrite(ctx, []string{command.id}, values); err != nil {
				return err
			}
		case 2:
			// Delete removes the child record entirely.
			if _, err := childExecutor.RuntimeUnlink(ctx, []string{command.id}); err != nil {
				return err
			}
		case 3:
			// Unlink detaches the child unless the inverse contract requires cascade delete.
			if inverse.OnDelete == "CASCADE" {
				if _, err := childExecutor.RuntimeUnlink(ctx, []string{command.id}); err != nil {
					return err
				}
				continue
			}
			if _, err := childExecutor.RuntimeWrite(ctx, []string{command.id}, RecordMap{inverse.Name: nil}); err != nil {
				return err
			}
		case 4:
			// Link reassigns an existing child to this parent by updating the inverse key.
			if _, err := childExecutor.RuntimeWrite(ctx, []string{command.id}, RecordMap{inverse.Name: toInt64(recordID)}); err != nil {
				return err
			}
		case 5:
			// Clear expands into unlink operations for each currently related child.
			existingIDs, err := h.readX2ManyIDs(ctx, field, recordID)
			if err != nil {
				return err
			}
			for _, existingID := range existingIDs {
				if err := h.applyOneToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 3, id: existingID}}); err != nil {
					return err
				}
			}
		case 6:
			// Replace first clears the existing relation set, then links the provided ids.
			if err := h.applyOneToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 5}}); err != nil {
				return err
			}
			for _, targetID := range command.ids {
				if err := h.applyOneToManyCommands(ctx, field, recordID, []x2ManyCommand{{op: 4, id: targetID}}); err != nil {
					return err
				}
			}
		}
	}
	h.invalidateRelationCaches(ctx, field, []string{recordID}, commands)
	return nil
}

// resolveManyToOneDisplay resolves the many to one display handled by this package.
func (h *Handler[T]) resolveManyToOneDisplay(ctx context.Context, field FieldMetadata, value any) any {
	return resolveManyToOneDisplayCore(ctx, field, value)
}

// resolveManyToOneDisplayCore is the shared implementation behind
// Handler[T].resolveManyToOneDisplay and dynamicRuntimeExecutor's equivalent
// -- the body never touched T, only the relation's own registered executor.
func resolveManyToOneDisplayCore(ctx context.Context, field FieldMetadata, value any) any {
	relation, ok := LookupSharedModel(field.Relation)
	if !ok {
		return value
	}
	executor, ok := lookupRuntimeExecutor(relation.Name)
	if !ok {
		return value
	}
	id, ok := runtimeStringID(value)
	// A non-numeric value is a legacy stored code (base_users.lang = "en_US"), not a row id.
	if !ok || !isNumericRelationID(id) {
		return value
	}
	rows, err := executor.RuntimeRead(ctx, []string{id}, []string{"id", relation.RecName})
	if err != nil || len(rows) == 0 {
		return value
	}
	return [2]any{rows[0]["id"], relationLabelValue(rows[0][relation.RecName])}
}

// isNumericRelationID reports whether a many2one value can be a row id (all digits).
func isNumericRelationID(id string) bool {
	if id == "" {
		return false
	}
	for _, r := range id {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (h *Handler[T]) prefetchManyToOneDisplays(ctx context.Context, field FieldMetadata, records []RecordMap) (map[string][2]any, error) {
	return prefetchManyToOneDisplaysCore(ctx, field, records)
}

// prefetchManyToOneDisplaysCore is the shared implementation behind
// Handler[T].prefetchManyToOneDisplays and dynamicRuntimeExecutor's
// equivalent -- the body never touched T, only the relation's own
// registered executor.
func prefetchManyToOneDisplaysCore(ctx context.Context, field FieldMetadata, records []RecordMap) (map[string][2]any, error) {
	relation, ok := LookupSharedModel(field.Relation)
	if !ok {
		return nil, nil
	}
	executor, ok := lookupRuntimeExecutor(relation.Name)
	if !ok {
		return nil, nil
	}
	uniqueIDs := make([]string, 0, len(records))
	seen := make(map[string]struct{}, len(records))
	for _, record := range records {
		id, ok := runtimeStringID(record[field.Name])
		// Non-numeric values are legacy stored codes; reading them as ids would fail the whole query.
		if !ok || !isNumericRelationID(id) {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		uniqueIDs = append(uniqueIDs, id)
	}
	if len(uniqueIDs) == 0 {
		return map[string][2]any{}, nil
	}
	env := EnvironmentFromContext(ctx)
	missingIDs := append([]string(nil), uniqueIDs...)
	if env != nil {
		missingIDs = missingIDs[:0]
		cachedNames := env.CachedFields(relation.Name, relation.RecName, uniqueIDs)
		for _, id := range uniqueIDs {
			if _, ok := cachedNames[id]; ok {
				continue
			}
			missingIDs = append(missingIDs, id)
		}
	}
	if len(missingIDs) > 0 {
		rows, err := executor.RuntimeRead(ctx, missingIDs, []string{"id", relation.RecName})
		if err != nil {
			return nil, err
		}
		if env != nil {
			for _, row := range rows {
				id, ok := runtimeStringID(row["id"])
				if !ok {
					continue
				}
				env.CacheField(relation.Name, id, relation.RecName, relationLabelValue(row[relation.RecName]))
			}
		}
	}
	out := make(map[string][2]any, len(uniqueIDs))
	cachedNames := env.CachedFields(relation.Name, relation.RecName, uniqueIDs)
	for _, id := range uniqueIDs {
		if displayName, ok := cachedNames[id]; ok {
			out[id] = [2]any{toInt64(id), displayName}
		}
	}
	return out, nil
}

// prefetchRelationDisplaysByIDs batches display-name lookups for relation ids
// so x2many read payloads can emit the same [id, name] tuples as many2one
// fields without triggering one read per related row.
func prefetchRelationDisplaysByIDs(ctx context.Context, relationModelName string, ids []string) (map[string][2]any, error) {
	relation, ok := LookupSharedModel(relationModelName)
	if !ok {
		return map[string][2]any{}, nil
	}
	executor, ok := lookupRuntimeExecutor(relation.Name)
	if !ok {
		return map[string][2]any{}, nil
	}
	uniqueIDs := make([]string, 0, len(ids))
	seen := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		normalized := strings.TrimSpace(id)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		uniqueIDs = append(uniqueIDs, normalized)
	}
	if len(uniqueIDs) == 0 {
		return map[string][2]any{}, nil
	}
	env := EnvironmentFromContext(ctx)
	missingIDs := append([]string(nil), uniqueIDs...)
	if env != nil {
		missingIDs = missingIDs[:0]
		for _, id := range uniqueIDs {
			if _, ok := env.CachedField(relation.Name, id, relation.RecName); ok {
				continue
			}
			missingIDs = append(missingIDs, id)
		}
	}
	if len(missingIDs) > 0 {
		rows, err := executor.RuntimeRead(ctx, missingIDs, []string{"id", relation.RecName})
		if err != nil {
			return nil, err
		}
		if env != nil {
			for _, row := range rows {
				id, ok := runtimeStringID(row["id"])
				if !ok {
					continue
				}
				env.CacheField(relation.Name, id, relation.RecName, relationLabelValue(row[relation.RecName]))
			}
		}
	}
	out := make(map[string][2]any, len(uniqueIDs))
	for _, id := range uniqueIDs {
		if env != nil {
			if displayName, ok := env.CachedField(relation.Name, id, relation.RecName); ok {
				out[id] = [2]any{toInt64(id), displayName}
			}
		}
	}
	return out, nil
}

func (h *Handler[T]) prefetchX2ManyIDs(ctx context.Context, field FieldMetadata, parentIDs []string) (map[string][]string, error) {
	db := runtimeDB(ctx, h)
	if h.repo == nil || db == nil || len(parentIDs) == 0 {
		return map[string][]string{}, nil
	}
	sort.Strings(parentIDs)
	result := make(map[string][]string, len(parentIDs))
	for _, parentID := range parentIDs {
		result[parentID] = []string{}
	}
	missingParentIDs := append([]string(nil), parentIDs...)
	if env := EnvironmentFromContext(ctx); env != nil {
		missingParentIDs = env.MissingPrefetchIDs(h.metadata.Name, field.Name, parentIDs)
		for _, parentID := range parentIDs {
			if cached, ok := env.CachedField(h.metadata.Name, parentID, field.Name); ok {
				if ids, ok := cached.([]string); ok {
					result[parentID] = append([]string(nil), ids...)
				}
			}
		}
	}
	if len(missingParentIDs) == 0 {
		return result, nil
	}
	switch field.Kind {
	case FieldKindManyToMany:
		var rows []struct {
			ParentID int64 `bun:"parent_id"`
			ChildID  int64 `bun:"child_id"`
		}
		if err := db.NewSelect().
			TableExpr("? AS rel", bun.Ident(field.RelationTable)).
			ColumnExpr("rel.? AS parent_id", bun.Ident(field.RelationColumn)).
			ColumnExpr("rel.? AS child_id", bun.Ident(field.RelationKey)).
			Where("rel.? IN (?)", bun.Ident(field.RelationColumn), bun.In(idsToFilterValues(missingParentIDs))).
			OrderExpr("rel.? ASC", bun.Ident(field.RelationColumn)).
			OrderExpr("rel.? ASC", bun.Ident(field.RelationKey)).
			Scan(ctx, &rows); err != nil {
			return nil, err
		}
		for _, row := range rows {
			key := fmt.Sprint(row.ParentID)
			result[key] = append(result[key], fmt.Sprint(row.ChildID))
		}
	case FieldKindOneToMany:
		childExecutor, childModel, err := h.relatedRuntime(field)
		if err != nil {
			return nil, err
		}
		inverse, err := childModel.Field(field.Inverse)
		if err != nil {
			return nil, err
		}
		rows, err := childExecutor.RuntimeSearchRead(ctx, RecordMap{inverse.Name: idsToFilterValues(missingParentIDs), "__order": "id asc"}, []string{"id", inverse.Name})
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			parentID, ok := relatedRecordID(row[inverse.Name])
			if !ok {
				parentID = fmt.Sprint(toInt64(row[inverse.Name]))
			}
			childID, ok := runtimeStringID(row["id"])
			if !ok {
				continue
			}
			result[parentID] = append(result[parentID], childID)
		}
	}
	if env := EnvironmentFromContext(ctx); env != nil {
		for _, parentID := range missingParentIDs {
			env.CacheField(h.metadata.Name, parentID, field.Name, append([]string(nil), result[parentID]...))
		}
		env.MarkPrefetched(h.metadata.Name, field.Name, missingParentIDs)
	}
	return result, nil
}

// readX2ManyIDs reads the 2 many i ds handled by this package.
func (h *Handler[T]) readX2ManyIDs(ctx context.Context, field FieldMetadata, recordID string) ([]string, error) {
	db := runtimeDB(ctx, h)
	if h.repo == nil || db == nil {
		return nil, nil
	}
	switch field.Kind {
	case FieldKindManyToMany:
		var rows []struct {
			ID int64 `bun:"id"`
		}
		if err := db.NewSelect().
			TableExpr("? AS rel", bun.Ident(field.RelationTable)).
			ColumnExpr("rel.? AS id", bun.Ident(field.RelationKey)).
			Where("rel.? = ?", bun.Ident(field.RelationColumn), toInt64(recordID)).
			Scan(ctx, &rows); err != nil {
			return nil, err
		}
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, fmt.Sprint(row.ID))
		}
		return out, nil
	case FieldKindOneToMany:
		relation, ok := LookupSharedModel(field.Relation)
		if !ok {
			return nil, nil
		}
		inverse, err := relation.Field(field.Inverse)
		if err != nil {
			return nil, err
		}
		executor, ok := lookupRuntimeExecutor(relation.Name)
		if !ok {
			return nil, nil
		}
		rows, err := executor.RuntimeSearchRead(ctx, RecordMap{inverse.Name: toInt64(recordID)}, []string{"id"})
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			if id, ok := runtimeStringID(row["id"]); ok {
				out = append(out, id)
			}
		}
		return out, nil
	default:
		return nil, nil
	}
}

func (h *Handler[T]) invalidateRelationCaches(ctx context.Context, field FieldMetadata, parentIDs []string, commands []x2ManyCommand) {
	env := EnvironmentFromContext(ctx)
	if env == nil {
		return
	}
	env.Invalidate(h.metadata.Name, parentIDs)
	relatedIDs := make([]string, 0, len(commands))
	for _, command := range commands {
		if command.id != "" {
			relatedIDs = append(relatedIDs, command.id)
		}
		if len(command.ids) > 0 {
			relatedIDs = append(relatedIDs, command.ids...)
		}
	}
	if strings.TrimSpace(field.Relation) != "" && len(relatedIDs) > 0 {
		env.Invalidate(field.Relation, relatedIDs)
	}
}

func (h *Handler[T]) relatedRuntime(field FieldMetadata) (runtimeExecutor, *ModelMetadata, error) {
	relationModel, ok := LookupSharedModel(field.Relation)
	if !ok {
		log.Errorf("orm: relation model %q is not registered", field.Relation)
		return nil, nil, fmt.Errorf("orm: relation model %q is not registered", field.Relation)
	}
	executor, ok := lookupRuntimeExecutor(relationModel.Name)
	if !ok {
		log.Errorf("orm: no runtime executor registered for model %q", relationModel.Name)
		return nil, nil, fmt.Errorf("orm: no runtime executor registered for model %q", relationModel.Name)
	}
	return executor, relationModel, nil
}
