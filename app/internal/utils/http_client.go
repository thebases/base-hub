package utils

// File overview:
// - Responsibility: collects shared helper logic for http client, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `CallApi`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
)

var defaultHTTPClient = &http.Client{Timeout: 10 * time.Second}

// Have to use http library for Fully HTTP/1.1 and HTTP/2 compliant which can be supported from bank system.
func CallApi(action string, urlStr string, headers map[string]string, body interface{}) (*http.Response, error) {
	action = strings.ToUpper(strings.TrimSpace(action))
	if action != http.MethodPost && action != http.MethodGet {
		log.Errorf("unsupported HTTP action: %s", action)
		return nil, fmt.Errorf("unsupported HTTP action: %s", action)
	}

	log.Info("========== Start HTTP client to call remote API ==========")
	log.Debugf("===== Request Action: %s", action)
	// Body and headers are not logged: they carry bank credentials, signatures and
	// Authorization values. The URL is logged without its query string for the same reason.
	log.Debugf("===== Request URL: %s", redactURLQuery(urlStr))

	reqHeaders := cloneHeaders(headers)
	requestBody, err := buildRequestBody(action, reqHeaders, body)
	if err != nil {
		log.Errorf("Error building request body: %v", err)
		return nil, err
	}

	req, err := http.NewRequest(action, urlStr, requestBody)
	if err != nil {
		log.Errorf("Error creating request: %v", err)
		return nil, err
	}

	for key, value := range reqHeaders {
		req.Header.Set(key, value)
	}
	req.Header.Set("Connection", "close")

	resp, err := defaultHTTPClient.Do(req)
	if err != nil {
		log.Errorf("Request failed: %v", err)
		return nil, err
	}

	log.Debugf("Response Status: %s", resp.Status)
	if err := logAndRestoreResponseBody(resp); err != nil {
		resp.Body.Close()
		return nil, err
	}

	log.Info("========== End HTTP client ==========")
	return resp, nil
}

// redactURLQuery drops the query string and userinfo from a URL for logging.
func redactURLQuery(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[unparseable url]"
	}
	u.User = nil
	if u.RawQuery != "" {
		u.RawQuery = "[redacted]"
	}
	u.Fragment = ""
	return u.String()
}

// buildRequestBody builds the request body handled by this package.
func buildRequestBody(action string, headers map[string]string, body interface{}) (io.Reader, error) {
	if action == http.MethodGet {
		return nil, nil
	}

	contentType := headers["Content-Type"]
	if contentType == "" {
		contentType = "application/json"
		headers["Content-Type"] = contentType
	}
	contentType = normalizeContentType(contentType)

	switch contentType {
	case "application/json":
		if body == nil {
			return nil, nil
		}

		jsonData, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		log.Debugf("===== Marshaled JSON: %s", string(jsonData))
		return bytes.NewReader(jsonData), nil
	case "application/x-www-form-urlencoded":
		switch v := body.(type) {
		case map[string]string:
			form := make(url.Values, len(v))
			for key, value := range v {
				form.Set(key, value)
			}
			encoded := form.Encode()
			log.Debugf("===== Form-encoded Body: %s", encoded)
			return strings.NewReader(encoded), nil
		case url.Values:
			encoded := v.Encode()
			log.Debugf("===== Form-encoded Body: %s", encoded)
			return strings.NewReader(encoded), nil
		default:
			log.Errorf("body must be map[string]string or url.Values for form encoding")
			return nil, fmt.Errorf("body must be map[string]string or url.Values for form encoding")
		}
	default:
		log.Errorf("unsupported Content-Type: %s", contentType)
		return nil, fmt.Errorf("unsupported Content-Type: %s", contentType)
	}
}

func normalizeContentType(contentType string) string {
	mediaType, _, err := mime.ParseMediaType(strings.TrimSpace(contentType))
	if err != nil {
		return strings.TrimSpace(contentType)
	}
	return mediaType
}

// cloneHeaders clones the headers handled by this package.
func cloneHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return map[string]string{}
	}

	cloned := make(map[string]string, len(headers))
	for key, value := range headers {
		cloned[key] = value
	}
	return cloned
}

// logAndRestoreResponseBody logs the and restore response body handled by this package.
func logAndRestoreResponseBody(resp *http.Response) error {
	if resp == nil || resp.Body == nil {
		return nil
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Errorf("read response body: %v", err)
		return fmt.Errorf("read response body: %w", err)
	}
	// Size only: provider responses can carry tokens and account data.
	log.Debugf("Response Body: %d bytes", len(bodyBytes))
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	return nil
}
