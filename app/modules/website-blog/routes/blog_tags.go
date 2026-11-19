package routes

// File overview:
// - Responsibility: tag-cloud grouping and sanitized body rendering helpers for the public blog.
// - Key symbols: `buildTagCloud`, `postBodyHTML`.

import (
	"html/template"
	"sort"

	"git.thebase.vn/base_hub/modules/website-blog/models"
)

// buildTagCloud groups tags that appear on at least one visible post, with usage counts. Ungrouped
// tags come last under an empty group name.
func buildTagCloud(catalog *blogCatalog, posts []blogPost) []tagGroup {
	counts := map[int64]int{}
	for _, p := range posts {
		for _, id := range p.TagIDs {
			counts[id]++
		}
	}
	byGroup := map[string][]tagInfo{}
	for id, n := range counts {
		tag, ok := catalog.Tags[id]
		if !ok {
			continue
		}
		tag.Count = n
		byGroup[tag.Category] = append(byGroup[tag.Category], tag)
	}
	names := make([]string, 0, len(byGroup))
	for name := range byGroup {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == "") != (names[j] == "") {
			return names[j] == ""
		}
		return names[i] < names[j]
	})
	groups := make([]tagGroup, 0, len(names))
	for _, name := range names {
		tags := byGroup[name]
		sort.Slice(tags, func(i, j int) bool { return tags[i].Name < tags[j].Name })
		groups = append(groups, tagGroup{Name: name, Tags: tags})
	}
	return groups
}

// postBodyHTML sanitizes again at render time (content is cleaned on save, but rows can arrive
// through import/SQL) and marks the result as safe for html/template.
func postBodyHTML(content string) template.HTML {
	return template.HTML(models.SanitizeBlogHTML(content)) //nolint:gosec // output of the allowlist sanitizer
}
