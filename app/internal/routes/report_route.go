package routes

// File overview:
// - Responsibility: implements the GET /report/{report_name}/{ids} transport (SRS-F-020) —
//   authenticated, access-checked report rendering to HTML or PDF with optional
//   attachment persistence.
// - Key symbols: `RegisterReportRoutes`, `handleReportRender`, `serveReportPDF`.
// - Error contract (SRS-F-020): unknown report 404, denied record access 403, template
//   failure 500 (template name logged service-side), engine unreachable 503 while the
//   HTML path keeps working.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"strconv"
	"strings"

	"git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/filestore"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/report"
	"github.com/gofiber/fiber/v3"
)

// RegisterReportRoutes mounts the report transport. The /report prefix is
// excluded from the SPA fallback in server.go (same reservation pattern as
// /web/content).
func RegisterReportRoutes(router fiber.Router) {
	router.Get("/report/:report_name/:ids", handleReportRender)
}

// handleReportRender renders one report definition for the requested record
// ids. Authentication and environment binding mirror /web/content so the RPC
// auth semantics apply unchanged.
func handleReportRender(c fiber.Ctx) error {
	identity, c, err := webContentEnvironment(c)
	if err != nil {
		return err
	}
	requestCtx := c.Context()

	reportName := strings.TrimSpace(c.Params("report_name"))
	ids, err := parseRPCPathIDs(c.Params("ids"), false)
	if err != nil || len(ids) == 0 {
		return fiber.NewError(fiber.StatusBadRequest, "report requires at least one record id")
	}

	definitionRow, err := models.FindSysReportDefinition(requestCtx, reportName)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "report definition lookup failed")
	}
	// Unknown report names are a 404 by contract — definitions are catalog
	// metadata, not user data, so there is nothing to leak here.
	if definitionRow == nil {
		return fiber.NewError(fiber.StatusNotFound, fmt.Sprintf("unknown report %q", reportName))
	}
	def := report.DefinitionFromRecord(definitionRow)
	// The render, the cached-PDF lookup and the write-generation read all go through
	// orm.ExecuteRuntimeMethod, which applies record rules but not the model ACL that /rpc
	// checks first; check read access on the report's model here once for every output (plan T36).
	if err := ensureReportModelReadable(requestCtx, def.Model, ids); err != nil {
		return err
	}

	output := strings.ToLower(strings.TrimSpace(c.Query("type", "pdf")))
	switch output {
	case "html":
		html, _, renderErr := report.RenderHTML(requestCtx, def, ids)
		if renderErr != nil {
			return mapReportRenderError(renderErr)
		}
		c.Set(fiber.HeaderContentType, fiber.MIMETextHTMLCharsetUTF8)
		return c.Send(html)
	case "pdf":
		return serveReportPDF(c, requestCtx, identity.DBName, def, ids)
	default:
		return fiber.NewError(fiber.StatusBadRequest, "type must be html or pdf")
	}
}

// serveReportPDF renders the PDF path with the attach=true idempotency
// contract: an unchanged record returns its stored attachment instead of
// re-rendering (SRS-F-020 acceptance: idempotent per record write-generation).
func serveReportPDF(c fiber.Ctx, requestCtx context.Context, dbName string, def report.Definition, ids []string) error {
	// The attachment cache only applies to single-record renders bound to one
	// target row; multi-id documents render fresh every time. Models without an
	// updated_at column have no write-generation marker, so caching would serve
	// stale documents forever — those render fresh as well.
	attachable := def.Attach && len(ids) == 1 && reportModelHasUpdateMarker(def.Model)
	fileName := ""
	if attachable {
		// The generation read enforces target-record access (ACL + record rules
		// + company scope) before any cached attachment can be served.
		generation, err := reportRecordGeneration(requestCtx, def.Model, ids[0])
		if err != nil {
			return mapReportRenderError(err)
		}
		fileName = report.AttachmentFileName(def, ids[0], generation)
		// Serve the stored render when this exact write-generation was already
		// produced; any cache failure falls through to a fresh render.
		if served, err := serveStoredReportAttachment(c, requestCtx, dbName, def, ids[0], fileName); err == nil && served {
			return nil
		}
	}

	html, _, err := report.RenderHTML(requestCtx, def, ids)
	if err != nil {
		return mapReportRenderError(err)
	}
	pdf, err := report.ConvertHTMLToPDF(requestCtx, report.EngineURL(requestCtx), html, def.PaperFormat)
	if err != nil {
		return mapReportRenderError(err)
	}

	// Persist the render as a sys.attachment on the target record (best
	// effort: caching failure must never fail an already-successful render).
	if attachable {
		if _, createErr := orm.ExecuteRuntimeMethod(requestCtx, "sys.attachment", "create", nil, []any{map[string]any{
			"name":      fileName,
			"datas":     base64.StdEncoding.EncodeToString(pdf),
			"sys_model": def.Model,
			"model_id":  ids[0],
			"mimetype":  "application/pdf",
		}}, nil); createErr != nil {
			log.Warnf("report: persist attachment for %s[%s]: %v", def.ReportName, ids[0], createErr)
		}
	}

	c.Set(fiber.HeaderContentType, "application/pdf")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`inline; filename="%s"`, sanitizeContentFilename(reportPDFFileName(def, ids))))
	return c.Send(pdf)
}

// serveStoredReportAttachment looks up the write-generation attachment and
// streams its verified blob. Returns served=false when no valid cached render
// exists so the caller re-renders.
func serveStoredReportAttachment(c fiber.Ctx, requestCtx context.Context, dbName string, def report.Definition, recordID, fileName string) (bool, error) {
	modelID, err := strconv.ParseInt(recordID, 10, 64)
	if err != nil {
		return false, err
	}
	rows, err := orm.ExecuteRuntimeMethod(requestCtx, "sys.attachment", "search_read", nil, nil, map[string]any{
		"filter": map[string]any{
			"sys_model": def.Model,
			"model_id":  modelID,
			"name":      fileName,
		},
		"fields": []string{"id", "checksum"},
		"limit":  1,
	})
	if err != nil {
		return false, err
	}
	record, ok := firstWebContentRecord(rows)
	if !ok {
		return false, nil
	}
	checksum := strings.TrimSpace(webContentString(record["checksum"]))
	if checksum == "" {
		return false, nil
	}
	file, err := filestore.Open(requestCtx, dbName, checksum)
	if err != nil {
		log.Warnf("report: cached blob %s missing, re-rendering: %v", checksum, err)
		return false, nil
	}
	data, err := io.ReadAll(file)
	_ = file.Close()
	if err != nil {
		return false, nil
	}
	// Serve only verified bytes: a checksum mismatch means the cached blob is
	// corrupt, so fall through to a fresh render instead of serving it.
	sum := sha256.Sum256(data)
	if !strings.EqualFold(hex.EncodeToString(sum[:]), checksum) {
		log.Errorf("report: cached blob %s failed verification, re-rendering", checksum)
		return false, nil
	}
	c.Set(fiber.HeaderContentType, "application/pdf")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf(`inline; filename="%s"`, sanitizeContentFilename(fileName)))
	return true, c.Send(data)
}

// ensureReportModelReadable runs the model read ACL /rpc applies before `read`. A denial is the
// SRS-F-020 403; an unknown model is a 404 (the definition names a model this build lacks).
func ensureReportModelReadable(ctx context.Context, modelName string, ids []string) error {
	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, fmt.Sprintf("report model %q is not registered", modelName))
	}
	readMethod, ok := meta.LookupRuntimeMethod("read")
	if !ok {
		return fiber.NewError(fiber.StatusNotFound, fmt.Sprintf("report model %q has no read method", modelName))
	}
	if err := orm.EnsureRuntimeModelAccess(ctx, meta, readMethod, ids); err != nil {
		log.Warnf("report: read access denied on %s: %v", modelName, err)
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to the requested records")
	}
	return nil
}

// reportModelHasUpdateMarker reports whether the target model exposes the
// updated_at column the write-generation token derives from.
func reportModelHasUpdateMarker(modelName string) bool {
	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return false
	}
	_, err := meta.Field("updated_at")
	return err == nil
}

// reportRecordGeneration reads the target record's update marker through the
// runtime (which enforces read access) and hashes it into the compact
// generation token embedded in attachment names.
func reportRecordGeneration(ctx context.Context, model string, recordID string) (string, error) {
	raw, err := orm.ExecuteRuntimeMethod(ctx, model, "read", []string{recordID}, []any{nil, []string{"id", "updated_at"}}, nil)
	if err != nil {
		return "", err
	}
	record, ok := firstWebContentRecord(raw)
	if !ok {
		log.Errorf("report: record %s/%s is not readable", model, recordID)
		return "", fmt.Errorf("report: some requested records are not readable")
	}
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(fmt.Sprint(record["updated_at"])))
	return fmt.Sprintf("%016x", hasher.Sum64()), nil
}

// reportPDFFileName builds the download name for fresh renders.
func reportPDFFileName(def report.Definition, ids []string) string {
	return fmt.Sprintf("%s-%s.pdf", def.ReportName, strings.Join(ids, "-"))
}

// mapReportRenderError translates service-layer failures onto the SRS-F-020
// HTTP error contract.
func mapReportRenderError(err error) error {
	if err == nil {
		return nil
	}
	// Engine failures are explicitly 503 with the engine status: the HTML path
	// still works, so callers can degrade instead of treating this as fatal.
	if errors.Is(err, report.ErrEngineUnavailable) {
		return fiber.NewError(fiber.StatusServiceUnavailable, err.Error())
	}
	message := err.Error()
	// Denied or filtered records are 403 — the caller lacks access to at least
	// one requested id (SRS-F-020 error table).
	if strings.Contains(message, "not readable") || strings.Contains(strings.ToLower(message), "access") {
		return fiber.NewError(fiber.StatusForbidden, "you do not have access to the requested records")
	}
	return fiber.NewError(fiber.StatusInternalServerError, message)
}

// fiber:context-methods migrated
