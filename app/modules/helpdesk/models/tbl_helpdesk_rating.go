package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_rating` model — a customer
//   satisfaction rating on a closed ticket. Ownership: helpdesk module.
//   Audit-sensitivity: low.
// - Key symbols: `init`, `HelpdeskRatingRecord`, `HelpdeskRatingModelSpec`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// HelpdeskRatingRecord stores one rating submission. Value is a plain int
// (1/3/5 mapped sad/ok/happy) — a simple column, not a separate polymorphic
// rating.rating model, since no existing generic rating primitive exists in
// this repo (plan §3 item 8).
type HelpdeskRatingRecord struct {
	orm.BaseModel
	TicketID int64   `bun:"ticket_id,notnull" json:"ticket_id" meta:"kind:many2one,model:tbl.helpdesk_ticket,ondelete:CASCADE,required,index,label:Ticket,help:Links this rating to the ticket it was submitted for."`
	Value    int     `bun:"value,notnull" json:"value" meta:"required,index,label:Value,help:Stores the satisfaction score: 1 (sad), 3 (ok), or 5 (happy)."`
	Feedback *string `bun:"feedback" json:"feedback" meta:"kind:text,label:Feedback,help:Stores optional free-text feedback accompanying the rating."`
	// Label is a stored:false compute derived straight from Value
	// (>=5 top, ==3 ok, else ko) — used by the list view's decoration
	// classes to color/badge each row without a bespoke smiley widget.
	Label string `bun:"label" json:"label" meta:"compute:_compute_label,stored:false,readonly,label:Satisfaction,help:Stores a top/ok/ko classification of Value for list-view decoration." selection:"(top,Satisfied),(ok,Neutral),(ko,Dissatisfied)"`
}

const HelpdeskRatingModelName = "tbl.helpdesk_rating"

var HelpdeskRatingModelSpec = orm.ModelSpec{
	Name:        HelpdeskRatingModelName,
	Label:       "Helpdesk Ratings",
	Description: "Customer satisfaction ratings owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskRatingModelName, (*HelpdeskRatingRecord)(nil)),
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createHelpdeskRating,
	},
}

var HelpdeskRatingModel = orm.NewHandlerWithModel[HelpdeskRatingRecord](HelpdeskRatingModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskRatingModelName, 910, HelpdeskRatingModel.InitDefault, HelpdeskRatingModel.Shutdown)
	orm.RegisterCompute(HelpdeskRatingModelName, "label", computeRatingLabel)
}

// computeRatingLabel is the stored:false compute handler for Label — one
// batch query for every requested id's Value, same shape as
// tbl_helpdesk_ticket_hooks.go's computeDuplicateCount.
func computeRatingLabel(ctx context.Context, records orm.RecordSet, _ orm.FieldMetadata) (map[string]any, error) {
	type row struct {
		ID    int64 `bun:"id"`
		Value int   `bun:"value"`
	}
	var rows []row
	db := orm.BunDBFromContext(ctx)
	idsAsAny := make([]any, 0, len(records.IDs()))
	for _, id := range records.IDs() {
		if value, ok := int64FromAny(id); ok {
			idsAsAny = append(idsAsAny, value)
		}
	}
	if len(idsAsAny) == 0 {
		return map[string]any{}, nil
	}
	if err := db.NewSelect().
		Table("tbl_helpdesk_rating").
		Column("id", "value").
		Where("id IN (?)", bun.In(idsAsAny)).
		Scan(ctx, &rows); err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, r := range rows {
		out[fmt.Sprint(r.ID)] = ratingLabelForValue(int64(r.Value))
	}
	return out, nil
}

// ratingLabelForValue classifies a raw 1-5 rating value into top/ok/ko.
func ratingLabelForValue(value int64) string {
	switch {
	case value >= 5:
		return "top"
	case value == 3:
		return "ok"
	default:
		return "ko"
	}
}

// createHelpdeskRating creates the rating row, then — best-effort — posts a
// chatter note on the rated ticket summarizing the score/feedback.
func createHelpdeskRating(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.helpdesk_rating.create requires values")
	}
	working := cloneRecordMap(values)

	created, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(HelpdeskRatingModelName, model), working)
	if err != nil {
		return nil, err
	}

	ticketID := int64Value(working["ticket_id"])
	if ticketID > 0 {
		note := fmt.Sprintf("Customer rating submitted: %d/5.", int64Value(working["value"]))
		if feedback := stringValue(working["feedback"]); feedback != "" {
			note += " Feedback: " + feedback
		}
		if _, err := PostTicketMessage(ctx, ticketID, nil, note, true, nil); err != nil {
			// Best-effort: the rating row is already durable; a chatter-note
			// failure must not unwind the rating submission itself.
			_ = err
		}
	}
	return created, nil
}
