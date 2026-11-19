package routes

// /wh hardening (plan T43): per-trigger HMAC signature with a replay window, Idempotency-Key as
// the run name, a per-route body cap, and error text that does not leak internals to anonymous
// callers.
//
// Signature contract, opt-in per workflow through its webhook trigger row
// (config.hmac_secret = "@cred:<name>" or "@syscred:<name>"):
//
//	X-Webhook-Timestamp: <unix seconds>
//	X-Webhook-Signature: sha256=<hex HMAC-SHA256(secret, timestamp + "." + idempotency_key + "." + body)>
//
// idempotency_key is the Idempotency-Key header value, empty when absent. It is signed so a
// captured request cannot be replayed under a fresh key. The timestamp must be within
// webhookSignatureWindow of the server clock.
//
// Replay and duplicate delivery are stopped by the run name, which base.workflow.run keeps unique:
// wh:<db>:<workflow id>:<caller>:<Idempotency-Key> (caller = u<uid> or anon, so one user's keys
// never collide with another's), or for a signed request without a key
// wh:<db>:<workflow id>:sig:<digest of the signature>. The workflow id (not the free-text topic)
// and the fixed caller/sig segment keep names of different workflows or kinds from colliding.
// A name that already has a run answers 200 "duplicate" without executing again. Keys only
// protect against squatting when the webhook is signed or group-restricted: on an unsigned public
// webhook anyone can send a key first.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	internalmodels "git.thebase.vn/base_hub/internal/models"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
	"git.thebase.vn/base_hub/modules/base-workflow/service/libs"
	"github.com/jackc/pgx/v5/pgconn"
)

const (
	webhookTimestampHeader   = "X-Webhook-Timestamp"
	webhookSignatureHeader   = "X-Webhook-Signature"
	webhookIdempotencyHeader = "Idempotency-Key"
	webhookSignaturePrefix   = "sha256="
	webhookSignatureWindow   = 5 * time.Minute
)

// webhookIdempotencyKeyPattern bounds the caller key: it becomes part of a unique run name.
var webhookIdempotencyKeyPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

var (
	errWebhookSignatureMissing = errors.New("webhook signature required")
	errWebhookSignatureInvalid = errors.New("webhook signature invalid")
	errWebhookTimestampInvalid = errors.New("webhook timestamp outside the allowed window")
)

// workflowWebhookSettings is what /wh needs from the workflow's webhook trigger rows.
type workflowWebhookSettings struct {
	// Serves is false when the workflow has a non-webhook transport and no active webhook row.
	Serves bool
	// HMACSecretRef is the @cred:/@syscred: reference; empty means unsigned requests are accepted.
	HMACSecretRef string
	// MaxBodyBytes is the body cap; 0 means workflowmodels.WebhookDefaultMaxBodyBytes.
	MaxBodyBytes int64
	// AuditPayload is the webhook row's audit_payload mode (plan T45); empty means the default.
	AuditPayload string
}

func (s workflowWebhookSettings) bodyLimit() int64 {
	if s.MaxBodyBytes > 0 {
		return s.MaxBodyBytes
	}
	return workflowmodels.WebhookDefaultMaxBodyBytes
}

var (
	webhookNow = time.Now
	// workflowRouteResolveWebhookSecret resolves the trigger's hmac_secret in the workflow's
	// credential scope (T41), so another workflow's or company's credential never matches.
	workflowRouteResolveWebhookSecret = func(ctx context.Context, flow *internalmodels.FlowRecord, ref string) (string, error) {
		return libs.ResolveCredentialRef(workflowmodels.ContextWithWorkflowScope(ctx, flow.ID, flow.CompanyID), ref)
	}
	workflowRouteRunExists = func(ctx context.Context, runName string) (bool, error) {
		run, err := workflowRouteLookupRunByName(ctx, runName)
		return run != nil, err
	}
)

// workflowWebhookSettingsFor reads the workflow's trigger rows. The canvas keeps the webhook
// trigger implicit (no row) and writes one row for any other transport, so the workflow serves
// /wh when it has an active webhook row or no transport row at all. An inactive MQTT/Kafka/...
// row still counts: disabling that trigger must not expose the workflow over HTTP. The HMAC and
// body settings come from any webhook row, the active one first, so deactivating the row does not
// silently drop the signature requirement.
func workflowWebhookSettingsFor(ctx context.Context, workflowID int64) (workflowWebhookSettings, error) {
	rows, err := modelruntime.NewService().SearchRead(ctx, workflowmodels.WorkflowTriggerModelName, orm.RecordMap{
		"workflow_id": workflowID,
		"__order":     "id asc",
	}, nil)
	if err != nil {
		log.Errorf("workflow route: load triggers workflow_id=%d: %v", workflowID, err)
		return workflowWebhookSettings{}, err
	}
	var settings workflowWebhookSettings
	hasOtherTransport, hasActiveWebhook := false, false
	var configSource map[string]any
	for _, row := range rows {
		triggerType, _ := row["trigger_type"].(string)
		active, _ := row["is_active"].(bool)
		if !strings.EqualFold(strings.TrimSpace(triggerType), workflowmodels.WorkflowTriggerTypeWebhook) {
			hasOtherTransport = true
			continue
		}
		config, _ := row["config"].(map[string]any)
		if active && !hasActiveWebhook {
			hasActiveWebhook = true
			configSource = config
		} else if configSource == nil {
			configSource = config
		}
	}
	settings.Serves = hasActiveWebhook || !hasOtherTransport
	if ref, ok := configSource[workflowmodels.WebhookTriggerConfigHMACSecret].(string); ok {
		settings.HMACSecretRef = strings.TrimSpace(ref)
	}
	if limit, ok := webhookConfigInt(configSource[workflowmodels.WebhookTriggerConfigMaxBodyBytes]); ok && limit > 0 {
		settings.MaxBodyBytes = min(limit, int64(workflowmodels.WebhookMaxBodyBytesLimit))
	}
	if mode, ok := configSource[workflowmodels.TriggerConfigAuditPayload].(string); ok {
		settings.AuditPayload = strings.TrimSpace(mode)
	}
	return settings, nil
}

// webhookConfigInt reads an integer from decoded jsonb (float64) or a typed value.
func webhookConfigInt(raw any) (int64, bool) {
	switch v := raw.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		return n, err == nil
	}
	return 0, false
}

// verifyWebhookSignature checks the X-Webhook-* headers against secret. It returns the
// signature bytes on success so the caller can derive a replay-proof run name.
func verifyWebhookSignature(secret []byte, timestampHeader, signatureHeader, idempotencyKey string, body []byte, now time.Time) ([]byte, error) {
	timestampHeader = strings.TrimSpace(timestampHeader)
	signatureHeader = strings.TrimSpace(signatureHeader)
	if timestampHeader == "" || signatureHeader == "" {
		return nil, errWebhookSignatureMissing
	}
	ts, err := strconv.ParseInt(timestampHeader, 10, 64)
	if err != nil {
		return nil, errWebhookTimestampInvalid
	}
	if skew := now.Sub(time.Unix(ts, 0)); skew > webhookSignatureWindow || skew < -webhookSignatureWindow {
		return nil, errWebhookTimestampInvalid
	}
	if !strings.HasPrefix(strings.ToLower(signatureHeader), webhookSignaturePrefix) {
		return nil, errWebhookSignatureInvalid
	}
	given, err := hex.DecodeString(signatureHeader[len(webhookSignaturePrefix):])
	if err != nil || len(given) != sha256.Size {
		return nil, errWebhookSignatureInvalid
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestampHeader))
	mac.Write([]byte("."))
	mac.Write([]byte(idempotencyKey))
	mac.Write([]byte("."))
	mac.Write(body)
	if !hmac.Equal(given, mac.Sum(nil)) {
		return nil, errWebhookSignatureInvalid
	}
	return given, nil
}

// webhookRunName builds the unique run name for a /wh request, or "" when the request carries
// neither an Idempotency-Key nor a signature (the engine then mints a random id).
func webhookRunName(dbName string, workflowID int64, callerUID int64, idempotencyKey string, signature []byte) string {
	prefix := fmt.Sprintf("wh:%s:%d:", strings.TrimSpace(dbName), workflowID)
	if idempotencyKey != "" {
		caller := "anon"
		if callerUID > 0 {
			caller = "u" + strconv.FormatInt(callerUID, 10)
		}
		return prefix + caller + ":" + idempotencyKey
	}
	if len(signature) > 0 {
		digest := sha256.Sum256(signature)
		return prefix + "sig:" + hex.EncodeToString(digest[:16])
	}
	return ""
}

// isUniqueViolation reports a Postgres unique_violation anywhere in the chain: the request that
// loses a concurrent race on the same run name.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
