package handlers

// File overview:
// - Responsibility: implements the ticket<->task conversion actions
//   (mirrors the reference module's two conversion wizards, folded into two
//   plain endpoints per plan §3 — no separate wizard model). Ownership:
//   project-helpdesk bridge module. Audit-sensitivity: low.
// - Key symbols: `ConvertTicketToTask`, `ConvertTaskToTicket`.
// - Pattern: every model touch goes through `orm.RuntimeRepository*`/
//   `orm.ExecuteRuntimeMethod` targeting `tbl.helpdesk_ticket`/
//   `tbl.project_task`/`tbl.discuss_message` by name — this package never
//   imports `modules/helpdesk` or `modules/project`'s Go packages, per the
//   Decoupled Plugin Module Pattern (this bridge exists specifically so
//   neither module has to depend on the other).

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"github.com/gofiber/fiber/v3"
)

const (
	helpdeskTicketModelName = "tbl.helpdesk_ticket"
	projectTaskModelName    = "tbl.project_task"
	discussMessageModelName = "tbl.discuss_message"
)

// convertDBContext resolves the request's tenant DB name and binds a context
// to it, mirroring modules/base-workflow/routes/workflow_route.go's
// workflowRouteDBContext (the same two-line resolve+bind every custom RPC
// handler in this repo needs before checking identity).
func convertDBContext(c fiber.Ctx) (context.Context, error) {
	dbName, err := internalroutes.ResolveRPCDBName(internalroutes.RequestDBNameRaw(c))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return internalroutes.RPCContextForDB(c.Context(), dbName), nil
}

// ConvertTicketToTask handles POST /project-helpdesk/tickets/:id/convert-to-task.
func ConvertTicketToTask(c fiber.Ctx) error {
	ctx, err := convertDBContext(c)
	if err != nil {
		return err
	}
	if _, err := internalroutes.RequireRPCIdentity(c, false); err != nil {
		return err
	}
	ticketID, err := parsePositiveID(c.Params("id"))
	if err != nil {
		return err
	}
	var body struct {
		ProjectID int64 `json:"project_id"`
		StageID   int64 `json:"stage_id"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "payload must be valid JSON")
	}
	if body.ProjectID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "project_id is required")
	}

	ticketRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(helpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)}, []string{"subject", "description", "partner_id"})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if len(ticketRows) == 0 {
		return fiber.NewError(fiber.StatusNotFound, "ticket not found")
	}
	ticket := ticketRows[0]

	taskValues := orm.RecordMap{
		"name":        ticket["subject"],
		"description": ticket["description"],
		"partner_id":  ticket["partner_id"],
		"project_id":  body.ProjectID,
	}
	if body.StageID > 0 {
		taskValues["stage_id"] = body.StageID
	}
	createdTask, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(projectTaskModelName, nil), taskValues)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	taskID := int64FromRecord(createdTask["id"])

	if _, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(helpdeskTicketModelName, nil), []string{fmt.Sprint(ticketID)}, orm.RecordMap{"active": false}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	postConversionNote(ctx, helpdeskTicketModelName, ticketID, fmt.Sprintf("Ticket converted into task #%d.", taskID))
	postConversionNote(ctx, projectTaskModelName, taskID, fmt.Sprintf("Task created from helpdesk ticket #%d.", ticketID))

	return c.JSON(fiber.Map{"result": fiber.Map{"task_id": taskID}})
}

// ConvertTaskToTicket handles POST /project-helpdesk/tasks/:id/convert-to-ticket.
func ConvertTaskToTicket(c fiber.Ctx) error {
	ctx, err := convertDBContext(c)
	if err != nil {
		return err
	}
	if _, err := internalroutes.RequireRPCIdentity(c, false); err != nil {
		return err
	}
	taskID, err := parsePositiveID(c.Params("id"))
	if err != nil {
		return err
	}
	var body struct {
		TeamID  int64 `json:"team_id"`
		StageID int64 `json:"stage_id"`
	}
	if err := c.Bind().Body(&body); err != nil {
		return fiber.NewError(fiber.StatusBadRequest, "payload must be valid JSON")
	}
	if body.TeamID <= 0 {
		return fiber.NewError(fiber.StatusBadRequest, "team_id is required")
	}

	taskRows, err := orm.RuntimeRepositoryRead(ctx, orm.RuntimeModelRef(projectTaskModelName, nil), []string{fmt.Sprint(taskID)}, []string{"name", "description", "partner_id"})
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	if len(taskRows) == 0 {
		return fiber.NewError(fiber.StatusNotFound, "task not found")
	}
	task := taskRows[0]

	ticketValues := orm.RecordMap{
		"subject":     task["name"],
		"description": task["description"],
		"partner_id":  task["partner_id"],
		"team_id":     body.TeamID,
	}
	if body.StageID > 0 {
		ticketValues["stage_id"] = body.StageID
	}
	createdTicket, err := orm.CreateRecord(ctx, orm.RuntimeModelRef(helpdeskTicketModelName, nil), ticketValues)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}
	ticketID := int64FromRecord(createdTicket["id"])

	if _, err := orm.WriteRecords(ctx, orm.RuntimeModelRef(projectTaskModelName, nil), []string{fmt.Sprint(taskID)}, orm.RecordMap{"active": false}); err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, err.Error())
	}

	postConversionNote(ctx, projectTaskModelName, taskID, fmt.Sprintf("Task converted into helpdesk ticket #%d.", ticketID))
	postConversionNote(ctx, helpdeskTicketModelName, ticketID, fmt.Sprintf("Ticket created from project task #%d.", taskID))

	return c.JSON(fiber.Map{"result": fiber.Map{"ticket_id": ticketID}})
}

// postConversionNote posts a best-effort chatter note (author defaults to the
// calling user server-side) — never fails the conversion that already
// succeeded, mirroring modules/helpdesk/models/cron.go's best-effort chatter posts.
func postConversionNote(ctx context.Context, modelName string, recordID int64, body string) {
	args := []any{modelName, recordID, nil, nil, "comment", nil, body}
	_, _ = orm.ExecuteRuntimeMethod(ctx, discussMessageModelName, "post_message", nil, args, map[string]any{"is_note": true})
}

func parsePositiveID(raw string) (int64, error) {
	id, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil || id <= 0 {
		return 0, fiber.NewError(fiber.StatusBadRequest, "id must be a positive integer")
	}
	return id, nil
}

func int64FromRecord(raw any) int64 {
	switch typed := raw.(type) {
	case int64:
		return typed
	case int:
		return int64(typed)
	case float64:
		return int64(typed)
	default:
		return 0
	}
}
