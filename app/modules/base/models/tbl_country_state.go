package models

// File overview:
// - Responsibility: defines the `base` module model contract for country state, including field metadata and runtime methods.
// - Key symbols: `ResCountryStateModel`, `ResCountryStateRecord`, `nameSearchResCountryState`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

type ResCountryStateRecord struct {
	orm.BaseModel
	CountryID int64  `bun:"country_id" json:"country_id" meta:"kind:many2one,model:tbl.country,ondelete:RESTRICT,index,label:Country ID,help:Links this record to the related tbl.country record for country id."`
	Name      string `bun:"name" json:"name" meta:"translate,index,label:Name,help:Stores the human readable name for this record."`
	Code      string `bun:"code" json:"code" meta:"index,label:Code,help:Stores the code value for this record."`
	TZ        string `bun:"tz" json:"tz" meta:"label:Timezone,help:IANA timezone name (for example Asia/Ho_Chi_Minh) used to display datetimes to users located here."`
}

const ResCountryStateModelName = "tbl.country.state"

var ResCountryStateDefinition = orm.ModelDefinition(ResCountryStateModelName, (*ResCountryStateRecord)(nil))

var ResCountryStateModelSpec = orm.ModelSpec{
	Name:        ResCountryStateModelName,
	Label:       "Country State",
	Description: "Administrative subdivision metadata",
	Definition:  ResCountryStateDefinition,
	RuntimeMethods: []orm.RuntimeMethod{
		{Name: "name_search", Public: true, ModelOnly: true, ReadOnly: true, Call: nameSearchResCountryState},
		{Name: "name_get", Public: true, ReadOnly: true, Call: nameGetResCountryState},
		{Name: "_get_name_search_domain", Public: false, ModelOnly: true, ReadOnly: true, Call: getNameSearchDomainResCountryState},
	},
}

var ResCountryStateModel = orm.Define[ResCountryStateRecord](&Models, 250, ResCountryStateModelSpec)

func nameSearchResCountryState(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
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
		CountryCode string `bun:"country_code"`
	}{}
	nameExpr := orm.TranslatedColumnExpr(ctx, "s", "name")
	query := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country_state AS s").
		ColumnExpr("s.id").
		ColumnExpr(nameExpr + " AS name").
		ColumnExpr("s.code").
		ColumnExpr("c.code AS country_code").
		Join("LEFT JOIN tbl_country AS c ON c.id = s.country_id")
	if name != "" {
		query = query.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.
				Where(nameExpr+" ILIKE ?", "%"+name+"%").
				WhereOr("s.code ILIKE ?", strings.ToUpper(name)+"%")
		})
	}
	if err := query.OrderExpr(nameExpr+" ASC").Limit(int(limit)).Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{row.ID, countryStateDisplayName(row.Name, row.CountryCode, true)})
	}
	return out, nil
}

func nameGetResCountryState(ctx context.Context, _ *orm.ModelMetadata, ids []string, _ []any, _ map[string]any) (any, error) {
	rows := []struct {
		ID          int64  `bun:"id"`
		Name        string `bun:"name"`
		CountryCode string `bun:"country_code"`
	}{}
	if err := orm.BunDBFromContext(ctx).NewSelect().
		TableExpr("tbl_country_state AS s").
		ColumnExpr("s.id").
		ColumnExpr(orm.TranslatedColumnExpr(ctx, "s", "name")+" AS name").
		ColumnExpr("c.code AS country_code").
		Join("LEFT JOIN tbl_country AS c ON c.id = s.country_id").
		Where("s.id IN (?)", bun.In(idsToAny(ids))).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	out := make([][2]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, [2]any{row.ID, countryStateDisplayName(row.Name, row.CountryCode, true)})
	}
	return out, nil
}

// getNameSearchDomainResCountryState retrieves the name search domain res country state handled by this package.
func getNameSearchDomainResCountryState(_ context.Context, _ *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	name := firstNonEmpty(strings.TrimSpace(stringArg(args, 0)), strings.TrimSpace(stringValue(kwargs["name"])))
	operator := firstNonEmpty(strings.TrimSpace(stringArg(args, 1)), strings.TrimSpace(stringValue(kwargs["operator"])), "ilike")
	state, country, ok := splitFormattedCountryState(name)
	if !ok {
		return []any{[]any{"id", "=", int64(0)}}, nil
	}
	return []any{
		[]any{"name", operator, state},
		"|",
		[]any{"country_id.name", "ilike", country},
		[]any{"country_id.code", "=", country},
	}, nil
}

func countryStateDisplayName(name, countryCode string, formatted bool) string {
	name = strings.TrimSpace(name)
	countryCode = strings.TrimSpace(countryCode)
	if !formatted || countryCode == "" {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, countryCode)
}

func splitFormattedCountryState(name string) (string, string, bool) {
	name = strings.TrimSpace(name)
	if !strings.HasSuffix(name, ")") {
		return "", "", false
	}
	open := strings.LastIndex(name, "(")
	if open <= 0 {
		return "", "", false
	}
	state := strings.TrimSpace(name[:open])
	country := strings.TrimSpace(strings.TrimSuffix(name[open+1:], ")"))
	if state == "" || country == "" {
		return "", "", false
	}
	return state, country, true
}
