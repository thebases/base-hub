package routes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	internalmodels "git.thebase.vn/base_hub/internal/models"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	testutil "git.thebase.vn/base_hub/internal/test"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
	"github.com/gofiber/fiber/v3"
)

const testWebhookSecret = "s3cret"

// setupPublicWebhook wires a public webhook workflow "demo.topic" (id 9) with the given trigger
// settings and returns a pointer to the run names the engine was asked to execute.
func setupPublicWebhook(t *testing.T, settings workflowWebhookSettings) *[]string {
	t.Helper()
	setRPCTestConfig(t)
	restoreWorkflowRouteHooks(t)
	settings.Serves = true
	workflowRouteModuleEnabled = func(context.Context, string) (bool, error) { return true, nil }
	workflowRouteModelRegistered = func() bool { return true }
	workflowRouteLookupFlow = func(context.Context, string) (*internalmodels.FlowRecord, error) {
		flow := &internalmodels.FlowRecord{Name: "Public", Topic: "demo.topic"}
		flow.ID = 9
		return flow, nil
	}
	workflowRouteServesWebhook = func(context.Context, int64) (workflowWebhookSettings, error) { return settings, nil }
	workflowRouteCurrentIdentity = func(fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) { return nil, nil }
	workflowRouteResolveWebhookSecret = func(_ context.Context, _ *internalmodels.FlowRecord, ref string) (string, error) {
		if ref != "@cred:wh_secret" {
			return "", errors.New("unexpected ref " + ref)
		}
		return testWebhookSecret, nil
	}
	webhookNow = func() time.Time { return time.Unix(1_700_000_000, 0) }
	runs := []string{}
	workflowRouteExecute = func(ctx context.Context, _ string, _ []byte) error {
		runs = append(runs, models.WorkflowRequestIDFromContext(ctx))
		return nil
	}
	return &runs
}

func signWebhook(ts int64, key, body string) string {
	mac := hmac.New(sha256.New, []byte(testWebhookSecret))
	mac.Write([]byte(strconv.FormatInt(ts, 10) + "." + key + "." + body))
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, body string, headers map[string]string) *http.Response {
	t.Helper()
	req := testutil.NewJSONRequest(t, http.MethodPost, "/wh/demo.topic", body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := newWorkflowRouteTestApp().Test(req)
	if err != nil {
		t.Fatalf("workflow request: %v", err)
	}
	return resp
}

func TestWebhookSignatureRequiredWhenConfigured(t *testing.T) {
	runs := setupPublicWebhook(t, workflowWebhookSettings{HMACSecretRef: "@cred:wh_secret"})
	const body = `{"ok":true}`
	now := webhookNow().Unix()

	cases := []struct {
		name    string
		headers map[string]string
	}{
		{"missing", nil},
		{"wrong secret", map[string]string{webhookTimestampHeader: strconv.FormatInt(now, 10), webhookSignatureHeader: "sha256=" + strings.Repeat("ab", 32)}},
		{"stale timestamp", map[string]string{webhookTimestampHeader: strconv.FormatInt(now-301, 10), webhookSignatureHeader: signWebhook(now-301, "", body)}},
		{"future timestamp", map[string]string{webhookTimestampHeader: strconv.FormatInt(now+301, 10), webhookSignatureHeader: signWebhook(now+301, "", body)}},
		// A captured request replayed under a fresh Idempotency-Key must not verify.
		{"key swapped", map[string]string{webhookTimestampHeader: strconv.FormatInt(now, 10), webhookSignatureHeader: signWebhook(now, "a", body), webhookIdempotencyHeader: "b"}},
	}
	for _, tc := range cases {
		resp := postWebhook(t, body, tc.headers)
		testutil.RequireStatus(t, resp, http.StatusUnauthorized)
	}
	if len(*runs) != 0 {
		t.Fatalf("unsigned/invalid requests executed: %v", *runs)
	}

	resp := postWebhook(t, body, map[string]string{
		webhookTimestampHeader: strconv.FormatInt(now-60, 10),
		webhookSignatureHeader: signWebhook(now-60, "", body),
	})
	testutil.RequireStatus(t, resp, http.StatusOK)
	if len(*runs) != 1 || !strings.HasPrefix((*runs)[0], "wh:bean_ipn:9:sig:") {
		t.Fatalf("signed request run names = %v, want one wh:bean_ipn:9:sig:<digest>", *runs)
	}
}

func TestWebhookReplayAndIdempotencyKeyAreDeduplicated(t *testing.T) {
	runs := setupPublicWebhook(t, workflowWebhookSettings{HMACSecretRef: "@cred:wh_secret"})
	seen := map[string]bool{}
	workflowRouteRunExists = func(_ context.Context, name string) (bool, error) { return seen[name], nil }
	inner := workflowRouteExecute
	workflowRouteExecute = func(ctx context.Context, topic string, body []byte) error {
		seen[models.WorkflowRequestIDFromContext(ctx)] = true
		return inner(ctx, topic, body)
	}

	const body = `{"ok":true}`
	now := webhookNow().Unix()
	signed := map[string]string{webhookTimestampHeader: strconv.FormatInt(now, 10), webhookSignatureHeader: signWebhook(now, "", body)}
	keyed := map[string]string{
		webhookTimestampHeader:   strconv.FormatInt(now, 10),
		webhookSignatureHeader:   signWebhook(now, "order-1", body),
		webhookIdempotencyHeader: "order-1",
	}
	for _, headers := range []map[string]string{signed, signed, keyed, keyed} {
		resp := postWebhook(t, body, headers)
		testutil.RequireStatus(t, resp, http.StatusOK)
	}
	if len(*runs) != 2 {
		t.Fatalf("executed %d runs (%v), want 2: each replay must be a duplicate", len(*runs), *runs)
	}
	if (*runs)[1] != "wh:bean_ipn:9:anon:order-1" {
		t.Fatalf("keyed run name = %q", (*runs)[1])
	}
	resp := postWebhook(t, body, keyed)
	if got := testutil.ReadBody(t, resp); !strings.Contains(got, `"status":"duplicate"`) {
		t.Fatalf("replay body = %s, want status duplicate", got)
	}
}

func TestWebhookUnsignedIdempotencyKeyAndBodyLimit(t *testing.T) {
	runs := setupPublicWebhook(t, workflowWebhookSettings{MaxBodyBytes: 16})

	resp := postWebhook(t, `{"padding":"0123456789"}`, nil)
	testutil.RequireStatus(t, resp, http.StatusRequestEntityTooLarge)

	resp = postWebhook(t, `{}`, map[string]string{webhookIdempotencyHeader: "bad key with spaces"})
	testutil.RequireStatus(t, resp, http.StatusBadRequest)

	resp = postWebhook(t, `{}`, map[string]string{webhookIdempotencyHeader: "evt_42"})
	testutil.RequireStatus(t, resp, http.StatusOK)
	if len(*runs) != 1 || (*runs)[0] != "wh:bean_ipn:9:anon:evt_42" {
		t.Fatalf("run names = %v", *runs)
	}
}

func TestWebhookAnonymousErrorIsGeneric(t *testing.T) {
	setupPublicWebhook(t, workflowWebhookSettings{})
	workflowRouteExecute = func(context.Context, string, []byte) error {
		return errors.New(`pq: relation "tbl_secret_table" does not exist`)
	}
	resp := postWebhook(t, `{}`, nil)
	testutil.RequireStatus(t, resp, http.StatusBadRequest)
	if body := testutil.ReadBody(t, resp); strings.Contains(body, "tbl_secret_table") || !strings.Contains(body, "workflow execution failed") {
		t.Fatalf("anonymous error body leaks internals: %s", body)
	}
}

func TestNormalizeWebhookTriggerConfig(t *testing.T) {
	if _, err := models.NormalizeWebhookTriggerConfig(map[string]any{"hmac_secret": "plaintext"}); err == nil {
		t.Fatal("literal hmac_secret must be refused")
	}
	if _, err := models.NormalizeWebhookTriggerConfig(map[string]any{"max_body_bytes": float64(models.WebhookMaxBodyBytesLimit + 1)}); err == nil {
		t.Fatal("max_body_bytes above the server limit must be refused")
	}
	got, err := models.NormalizeWebhookTriggerConfig(map[string]any{"hmac_secret": " @cred:wh ", "max_body_bytes": "2048", "junk": 1})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if got["hmac_secret"] != "@cred:wh" || got["max_body_bytes"] != int64(2048) || len(got) != 2 {
		t.Fatalf("normalized = %#v", got)
	}
}

func TestWebhookRunNamesDoNotCollide(t *testing.T) {
	names := map[string]bool{}
	for _, name := range []string{
		webhookRunName("db", 1, 0, "b:c", nil),
		webhookRunName("db", 12, 0, "c", nil),
		webhookRunName("db", 1, 7, "b:c", nil),
		webhookRunName("db", 1, 8, "b:c", nil),
		webhookRunName("db", 1, 0, "", []byte("sig")),
		webhookRunName("db", 1, 0, "sig:"+strings.Repeat("0", 32), nil),
	} {
		if names[name] {
			t.Fatalf("run name collision: %s", name)
		}
		names[name] = true
	}
}
