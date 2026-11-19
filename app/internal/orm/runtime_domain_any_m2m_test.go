package orm

// File overview:
// - Responsibility: regression for many2many `any`: the child condition must reference the
//   `target` alias the EXISTS subquery joins the related table under (Postgres rejects the
//   unaliased table name once it is aliased).

import (
	"strings"
	"testing"
)

type anyM2MTag struct {
	BaseModel
	Name string `bun:"name" json:"name"`
}

type anyM2MPost struct {
	BaseModel
	Name   string  `bun:"name" json:"name"`
	TagIDs []int64 `bun:"tag_ids,type:jsonb,nullzero" json:"tag_ids" meta:"kind:many2many,model:test.any_m2m.tag,relation_table:test_any_m2m_post_tag_rel,relation_column:post_id,relation_key:tag_id"`
}

func TestCompileAnyManyToManyUsesTargetAlias(t *testing.T) {
	registerDomainScopeModel[anyM2MTag](t, "test.any_m2m.tag", "test_any_m2m_tag")
	post := registerDomainScopeModel[anyM2MPost](t, "test.any_m2m.post", "test_any_m2m_post")

	node := DomainNode{Kind: DomainKindCondition, Field: "tag_ids", Operator: "any", Value: Where("id", "in", []int64{1, 2})}
	sql, _, err := node.CompileWithBaseAlias(post, `"root"`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sql, `JOIN "test_any_m2m_tag" target`) {
		t.Fatalf("expected aliased join, got %s", sql)
	}
	if strings.Contains(sql, `"test_any_m2m_tag"."id"`) || !strings.Contains(sql, `target."id"`) {
		t.Fatalf("child condition must use the target alias, got %s", sql)
	}
}
