package models

// File overview:
// - Responsibility: defines the `tbl.helpdesk_ticket_comment` model — a simple
//   child model holding comments on a `tbl.helpdesk_ticket`. Ownership: helpdesk
//   module. Unlike base-calendar's recurrence->exception pair (which uses a
//   generic (sys_model, record_id) reference on the parent side because the
//   parent can target ANY registered model), a ticket comment's parent is
//   always exactly `tbl.helpdesk_ticket`, so this uses a real FK with
//   `ondelete:CASCADE` — mirroring base-calendar's exception model, which is
//   also a real FK to its own static parent table.
// - Key symbols: `init`, `HelpdeskTicketCommentRecord`, `HelpdeskTicketCommentModelSpec`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
)

// HelpdeskTicketCommentRecord stores one comment on a helpdesk ticket.
type HelpdeskTicketCommentRecord struct {
	orm.BaseModel
	TicketID  int64  `bun:"ticket_id,notnull" json:"ticket_id" meta:"kind:many2one,model:tbl.helpdesk_ticket,ondelete:CASCADE,index,required,label:Ticket,help:Links this comment to the tbl.helpdesk_ticket it belongs to."`
	Body      string `bun:"body,notnull" json:"body" meta:"kind:text,required,label:Body,help:Stores the comment text."`
	CompanyID *int64 `bun:"company_id" json:"company_id" meta:"kind:many2one,model:tbl.company,ondelete:SET NULL,index,label:Company ID,help:Inherits the company scope from the parent ticket; not settable independently."`
}

const HelpdeskTicketCommentModelName = "tbl.helpdesk_ticket_comment"

var HelpdeskTicketCommentModelSpec = orm.ModelSpec{
	Name:        HelpdeskTicketCommentModelName,
	Label:       "Helpdesk Ticket Comments",
	Description: "Comments on a tbl.helpdesk_ticket, owned by the helpdesk module",
	Definition:  orm.ModelDefinition(HelpdeskTicketCommentModelName, (*HelpdeskTicketCommentRecord)(nil)),
	StandardHooks: orm.StandardRuntimeHooks{
		Create: createHelpdeskTicketComment,
	},
}

var HelpdeskTicketCommentModel = orm.NewHandlerWithModel[HelpdeskTicketCommentRecord](HelpdeskTicketCommentModelSpec)

// init registers this package runtime during bootstrap so its models, hooks, or commands are available before requests arrive.
func init() {
	// Register runtime metadata during bootstrap so the model is available before requests arrive.
	RegisterRuntimeBootstrap(HelpdeskTicketCommentModelName, 901, HelpdeskTicketCommentModel.InitDefault, HelpdeskTicketCommentModel.Shutdown)
}

// createHelpdeskTicketComment overrides the generic create dispatcher only to
// pin company_id from the parent ticket rather than the creating caller's own
// session company, so a comment's company can never diverge from its ticket's
// (mirrors tbl.website_page inheriting from tbl.website_site).
func createHelpdeskTicketComment(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, kwargs map[string]any) (any, error) {
	values, err := recordMapFromArgs(args, kwargs, "values")
	if err != nil {
		return nil, err
	}
	if values == nil {
		return nil, fmt.Errorf("tbl.helpdesk_ticket_comment.create requires values")
	}
	working := cloneRecordMap(values)
	if err := inheritHelpdeskTicketCommentCompanyID(ctx, working); err != nil {
		return nil, err
	}
	return orm.CreateRecord(ctx, orm.RuntimeModelRef(HelpdeskTicketCommentModelName, model), working)
}

func inheritHelpdeskTicketCommentCompanyID(ctx context.Context, values orm.RecordMap) error {
	ticketID, ok := int64FromAny(values["ticket_id"])
	if !ok || ticketID <= 0 {
		return fmt.Errorf("tbl.helpdesk_ticket_comment ticket_id is required")
	}
	rows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(HelpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)}, []string{"company_id"})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("tbl.helpdesk_ticket %d not found", ticketID)
	}
	values["company_id"] = rows[0]["company_id"]
	return nil
}
