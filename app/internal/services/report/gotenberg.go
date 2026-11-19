package report

// File overview:
// - Responsibility: PDF conversion for SRS-F-020 through an external Gotenberg engine —
//   the binary stays dependency-light by delegating chromium rendering to the service
//   configured at `report.engine_url` (owner decision 2026-07-11: Gotenberg API shape).
// - Key symbols: `EngineURL`, `ConvertHTMLToPDF`, `ErrEngineUnavailable`.
// - Error contract (SRS-F-020): engine unreachable keeps the HTML path working and maps
//   to 503 with the engine status at the transport.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// ErrEngineUnavailable marks failures reaching the PDF engine so the transport
// can answer 503 (engine status) instead of a generic 500.
var ErrEngineUnavailable = errors.New("report: pdf engine unavailable")

const engineConfigParamKey = "report.engine_url"

// engineHTTPClient is package state so tests can stub the engine; the timeout
// bounds a hung chromium render instead of pinning a request goroutine forever.
var engineHTTPClient = &http.Client{Timeout: 60 * time.Second}

// paperDimensions maps sys.report paper formats onto Gotenberg's inch-based
// page parameters. The set mirrors the sys.report selection field.
var paperDimensions = map[string][2]string{
	"A4":     {"8.27", "11.69"},
	"A5":     {"5.83", "8.27"},
	"letter": {"8.5", "11"},
	"legal":  {"8.5", "14"},
}

// EngineURL resolves the PDF engine endpoint from the home DB sys.config_param.
// Home DB only: the engine is deployment infrastructure (SRS §8.2), and tenant
// DBs must not be able to redirect other tenants' document rendering.
func EngineURL(ctx context.Context) string {
	homeDBName := strings.TrimSpace(config.HomeDBName())
	homeDB := orm.ResolveRegisteredDatabase(homeDBName)
	if homeDB == nil || homeDB.DB == nil {
		return ""
	}
	var raw string
	if err := homeDB.DB.NewRaw(
		`SELECT value::text FROM sys_config_param WHERE name = ? LIMIT 1`,
		engineConfigParamKey,
	).Scan(ctx, &raw); err != nil {
		return ""
	}
	// jsonb text form quotes string values; strip before use.
	return strings.Trim(strings.TrimSpace(raw), `"`)
}

// ConvertHTMLToPDF posts rendered HTML to the Gotenberg chromium route and
// returns the PDF bytes. An empty engineURL or any transport/engine failure
// wraps ErrEngineUnavailable — HTML rendering has no engine dependency, so the
// caller can still serve `type=html` when this fails.
func ConvertHTMLToPDF(ctx context.Context, engineURL string, html []byte, paperFormat string) ([]byte, error) {
	engineURL = strings.TrimRight(strings.TrimSpace(engineURL), "/")
	if engineURL == "" {
		return nil, fmt.Errorf("%w: %s is not configured", ErrEngineUnavailable, engineConfigParamKey)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// Gotenberg's chromium HTML route requires the entry document to be named
	// index.html inside the multipart payload.
	part, err := writer.CreateFormFile("files", "index.html")
	if err != nil {
		return nil, fmt.Errorf("report: build engine payload: %w", err)
	}
	if _, err := part.Write(html); err != nil {
		return nil, fmt.Errorf("report: write engine payload: %w", err)
	}
	if dims, ok := paperDimensions[strings.TrimSpace(paperFormat)]; ok {
		// Page geometry rides as plain form fields per the Gotenberg contract.
		_ = writer.WriteField("paperWidth", dims[0])
		_ = writer.WriteField("paperHeight", dims[1])
	}
	// Print backgrounds so styled documents (receipts, statements) keep their
	// visual identity in PDF output.
	_ = writer.WriteField("printBackground", "true")
	if err := writer.Close(); err != nil {
		return nil, fmt.Errorf("report: finalize engine payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, engineURL+"/forms/chromium/convert/html", &body)
	if err != nil {
		return nil, fmt.Errorf("report: build engine request: %w", err)
	}
	request.Header.Set("Content-Type", writer.FormDataContentType())

	response, err := engineHTTPClient.Do(request)
	if err != nil {
		log.Errorf("report: pdf engine request failed: %v", err)
		return nil, fmt.Errorf("%w: %v", ErrEngineUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("%w: read engine response: %v", ErrEngineUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		log.Errorf("report: pdf engine returned %d: %s", response.StatusCode, strings.TrimSpace(string(payload)))
		return nil, fmt.Errorf("%w: engine status %d", ErrEngineUnavailable, response.StatusCode)
	}
	return payload, nil
}
