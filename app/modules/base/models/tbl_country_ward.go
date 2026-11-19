package models

// File overview:
// - Responsibility: defines the `base` module model contract for country ward, including field metadata and name search/get runtime methods.
// - Key symbols: `ResCountryWardModel`, `ResCountryWardRecord`, `nameSearchResCountryWard`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

type ResCountryWardRecord struct {
	orm.BaseModel
	StateID   int64  `bun:"state_id" json:"state_id" meta:"kind:many2one,model:tbl.country.state,ondelete:RESTRICT,index,label:State ID,help:Links this record to the related tbl.country.state record for state id."`
	CountryID int64  `bun:"country_id" json:"country_id" meta:"kind:many2one,model:tbl.country,ondelete:RESTRICT,index,label:Country ID,help:Links this record to the related tbl.country record for country id."`
	Name      string `bun:"name" json:"name" meta:"translate,index,label:Name,help:Stores the human readable name for this record."`
	Code      string `bun:"code" json:"code" meta:"index,label:Code,help:Stores the code value for this record."`
}

const ResCountryWardModelName = "tbl.country.ward"

var ResCountryWardDefinition = orm.ModelDefinition(ResCountryWardModelName, (*ResCountryWardRecord)(nil))

var ResCountryWardModelSpec = orm.ModelSpec{
	Name:        ResCountryWardModelName,
	Label:       "Country Ward",
	Description: "Administrative subdivision metadata",
	Definition:  ResCountryWardDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: nameSearchResCountryWard},
		{Name: "name_get", Public: true, ReadOnly: true, Call: nameGetResCountryWard},
	},
}

var ResCountryWardModel = orm.Define[ResCountryWardRecord](&Models, 260, ResCountryWardModelSpec)

func nameSearchResCountryWard(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	limit := int64Value(kwargs["limit"])
	if limit <= 0 {
		limit = 100
	}
	// A client call cannot list past server.maxReadLimit (plan T36).
	limit = int64(orm.ClampClientReadLimit(ctx, model, int(limit)))
	rows := []struct {
		ID          int64  `bun:"id"`
		Name        string `bun:"name"`
		Code        string `bun:"code"`
		StateName   string `bun:"state_name"`
		CountryCode string `bun:"country_code"`
	}{}
	wardNameExpr := orm.TranslatedColumnExpr(ctx, "w", "name")
	// The state name is translated too (tbl.country.state.name).
	stateNameExpr := orm.TranslatedColumnExpr(ctx, "s", "name")
	query := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country_ward AS w").
		ColumnExpr("w.id").
		ColumnExpr(wardNameExpr + " AS name").
		ColumnExpr("w.code").
		ColumnExpr(stateNameExpr + " AS state_name").
		ColumnExpr("c.code AS country_code").
		Join("LEFT JOIN tbl_country_state AS s ON s.id = w.state_id").
		Join("LEFT JOIN tbl_country AS c ON c.id = w.country_id")
	if name != "" {
		query = query.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.
				Where(wardNameExpr+" ILIKE ?", "%"+name+"%").
				WhereOr("w.code ILIKE ?", strings.ToUpper(name)+"%").
				WhereOr(stateNameExpr+" ILIKE ?", "%"+name+"%").
				WhereOr("c.code ILIKE ?", strings.ToUpper(name)+"%")
		})
	}
	if err := query.OrderExpr(wardNameExpr+" ASC").Limit(int(limit)).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{row.ID, strings.TrimSpace(row.Name)})
	}
	return out, nil
}

func nameGetResCountryWard(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	rows := []struct {
		ID   int64  `bun:"id"`
		Name string `bun:"name"`
	}{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country_ward AS w").
		ColumnExpr("w.id").
		ColumnExpr(orm.TranslatedColumnExpr(ctx, "w", "name")+" AS name").
		Where("w.id IN (?)", bun.In(idsToAny(ids))).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{row.ID, strings.TrimSpace(row.Name)})
	}
	return out, nil
}
