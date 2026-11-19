package masterdata

// File overview:
// - Responsibility: orders import files by cross-file external_id dependencies so producers load before consumers.
// - Key symbols: `importFileDependencyNode`, `reorderImportFilesByDependencies`, `importProducerPathForExternalID`, `importRecordDependencyExternalIDs`.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

type importFileDependencyNode struct {
	path    string
	order   int
	records []ImportRecord
}

// reorderImportFilesByDependencies keeps the deterministic base sort while
// moving provider files ahead of consumer files when relation fields reference
// external IDs created by other imports in the same batch.
func reorderImportFilesByDependencies(paths []string, load func(string) ([]ImportRecord, error)) ([]string, error) {
	if len(paths) < 2 {
		return paths, nil
	}

	nodes := make([]importFileDependencyNode, 0, len(paths))
	producers := make(map[string]string, len(paths))
	for index, path := range paths {
		records, err := load(path)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, importFileDependencyNode{
			path:    path,
			order:   index,
			records: records,
		})
		for _, record := range records {
			externalID := strings.TrimSpace(record.ExternalID)
			if externalID == "" {
				continue
			}
			if _, exists := producers[externalID]; !exists {
				producers[externalID] = path
			}
		}
	}

	dependencies := make(map[string]map[string]struct{}, len(nodes))
	dependents := make(map[string][]string, len(nodes))
	indegree := make(map[string]int, len(nodes))
	for _, node := range nodes {
		dependencies[node.path] = make(map[string]struct{})
	}
	for _, node := range nodes {
		for _, record := range node.records {
			for _, dependencyID := range importRecordDependencyExternalIDs(record) {
				producerPath, ok := importProducerPathForExternalID(producers, dependencyID)
				if !ok || producerPath == node.path {
					continue
				}
				if _, exists := dependencies[node.path][producerPath]; exists {
					continue
				}
				dependencies[node.path][producerPath] = struct{}{}
				dependents[producerPath] = append(dependents[producerPath], node.path)
				indegree[node.path]++
			}
		}
	}

	available := make([]string, 0, len(nodes))
	orderIndex := make(map[string]int, len(nodes))
	for _, node := range nodes {
		orderIndex[node.path] = node.order
		if indegree[node.path] == 0 {
			available = append(available, node.path)
		}
	}

	result := make([]string, 0, len(nodes))
	visited := make(map[string]bool, len(nodes))
	for len(available) > 0 {
		sort.SliceStable(available, func(i, j int) bool {
			return orderIndex[available[i]] < orderIndex[available[j]]
		})
		current := available[0]
		available = available[1:]
		if visited[current] {
			continue
		}
		visited[current] = true
		result = append(result, current)
		for _, dependent := range dependents[current] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				available = append(available, dependent)
			}
		}
	}

	// Preserve the original deterministic order if cyclic or opaque references
	// remain after dependency analysis.
	if len(result) != len(nodes) {
		for _, path := range paths {
			if visited[path] {
				continue
			}
			result = append(result, path)
		}
	}
	return result, nil
}

func importProducerPathForExternalID(producers map[string]string, externalID string) (string, bool) {
	for _, candidate := range externalIDLookupCandidates(externalID) {
		if producerPath, ok := producers[candidate]; ok {
			return producerPath, true
		}
	}
	return "", false
}

func importRecordDependencyExternalIDs(record ImportRecord) []string {
	modelMeta, ok := orm.LookupModel(record.Model)
	if !ok || modelMeta == nil {
		return nil
	}

	dependencies := make([]string, 0)
	for fieldName, value := range record.Data {
		field, ok := importRecordFieldMetadata(modelMeta, fieldName)
		if !ok || strings.TrimSpace(field.Relation) == "" {
			continue
		}
		switch field.Kind {
		case orm.FieldKindManyToOne, orm.FieldKindOneToOne:
			if dependency, ok := importDependencyExternalID(value); ok {
				dependencies = append(dependencies, dependency)
			}
		case orm.FieldKindManyToMany, orm.FieldKindOneToMany:
			dependencies = append(dependencies, importDependencyExternalIDs(value)...)
		}
	}
	return dependencies
}

func importRecordFieldMetadata(model *orm.ModelMetadata, requested string) (orm.FieldMetadata, bool) {
	if model == nil {
		return orm.FieldMetadata{}, false
	}
	for _, field := range model.Fields {
		if requested == field.Name || requested == field.GoName || requested == field.JSONName || requested == field.BunColumn {
			return field, true
		}
	}
	return orm.FieldMetadata{}, false
}

func importDependencyExternalIDs(value any) []string {
	switch typed := value.(type) {
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if dependency, ok := importDependencyExternalID(item); ok {
				out = append(out, dependency)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if dependency, ok := importDependencyExternalID(item); ok {
				out = append(out, dependency)
			}
		}
		return out
	default:
		if dependency, ok := importDependencyExternalID(value); ok {
			return []string{dependency}
		}
		return nil
	}
}

func importDependencyExternalID(value any) (string, bool) {
	raw := strings.TrimSpace(fmt.Sprint(value))
	if raw == "" {
		return "", false
	}
	if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return "", false
	}
	return raw, true
}
