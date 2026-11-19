package routes

// File overview:
// - Responsibility: exposes a provider-agnostic HTTP webhook that accepts a
//   parsed inbound email and hands it to the discuss module's routing logic.
//   Ownership: discuss module.
// - Key symbols: `RegisterRoutes`, `handleInboundEmail`.
// - Pattern: mirrors base-bus/routes' shape (a routes package with the
//   handler functions colocated, no separate handlers/ package — that is
//   this repo's real convention despite the "handlers/ + routes/" phrasing
//   in code-pattern.md's summary table). This endpoint is called by an
//   external mail provider/relay, not by an authenticated app user, so it
//   cannot use the RPC Bearer-token identity path every other route uses —
//   see requireInboundWebhookSecret for the shared-secret auth it uses
//   instead (fail-closed when unset).

import (
	"crypto/subtle"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	discussmodels "git.thebase.vn/base_hub/modules/discuss/models"
	"github.com/gofiber/fiber/v3"
)

// The inbound-email webhook is protected by the shared secret in
// `app.discussInboundWebhookSecret` (config.Get().App.DiscussInboundWebhookSecret),
// sent as the X-Discuss-Webhook-Secret header. The specific email provider
// chosen later (SendGrid/Mailgun/Postmark/a custom relay/...) must send it,
// or the operator's reverse proxy must inject it — this module has no
// vendor-specific signature verification since no vendor has been chosen yet.
// It fails closed: an empty secret disables the webhook (403), because the
// route is anonymous and creates records (SEC-D-3).

// inboundWebhookSecretHeader carries the shared webhook secret.
const inboundWebhookSecretHeader = "X-Discuss-Webhook-Secret"

// inboundEmailPayload is the provider-agnostic JSON contract this webhook
// accepts. A real vendor's webhook adapter (translating that vendor's actual
// payload shape into this one) is a separate, vendor-specific follow-up.
type inboundEmailPayload struct {
	MessageIDHeader string   `json:"message_id_header"`
	InReplyTo       string   `json:"in_reply_to"`
	References      []string `json:"references"`
	FromEmail       string   `json:"from_email"`
	ToEmails        []string `json:"to_emails"`
	CcEmails        []string `json:"cc_emails"`
	Subject         string   `json:"subject"`
	BodyHTML        string   `json:"body_html"`
}

// RegisterRoutes lets the discuss module satisfy internal/modules.HTTPRouteRegistrar.
func RegisterRoutes(router fiber.Router) {
	router.Post("/discuss/inbound_email", handleInboundEmail)
}

// handleInboundEmail parses the webhook body and delegates to
// discussmodels.RouteInboundEmail. A "no matching thread or alias" outcome is
// still a 200 response (we bounce rather than errors on an unroutable
// email) — only a malformed request body or an infrastructure failure
// returns a non-2xx status.
func handleInboundEmail(c fiber.Ctx) error {
	if err := requireInboundWebhookSecret(c); err != nil {
		return err
	}

	var payload inboundEmailPayload
	if err := c.Bind().Body(&payload); err != nil {
		return orm.InvalidParamsf("invalid inbound email payload: %v", err)
	}

	outcome, err := discussmodels.RouteInboundEmail(c.Context(), discussmodels.InboundEmail{
		MessageIDHeader: payload.MessageIDHeader,
		InReplyTo:       payload.InReplyTo,
		References:      payload.References,
		FromEmail:       payload.FromEmail,
		ToEmails:        payload.ToEmails,
		CcEmails:        payload.CcEmails,
		Subject:         payload.Subject,
		BodyHTML:        payload.BodyHTML,
	})
	if err != nil {
		// Typed errors keep their status; anything else is classified (and its
		// text hidden) by the app-wide APIErrorHandler.
		return err
	}

	return c.JSON(fiber.Map{"result": fiber.Map{
		"status":     outcome.Status,
		"res_model":  outcome.ResModel,
		"res_id":     outcome.ResID,
		"message_id": outcome.MessageID,
	}})
}

// requireInboundWebhookSecret checks the X-Discuss-Webhook-Secret header
// against app.discussInboundWebhookSecret. The `?secret=` query form is no
// longer accepted: query strings end up in proxy and access logs. Both an
// unset secret and a mismatch are 403; the comparison is constant-time so the
// response timing does not leak how much of the secret matched.
func requireInboundWebhookSecret(c fiber.Ctx) error {
	expected := strings.TrimSpace(config.Get().App.DiscussInboundWebhookSecret)
	if expected == "" {
		return orm.Forbiddenf("inbound email webhook is disabled")
	}
	provided := strings.TrimSpace(c.Get(inboundWebhookSecretHeader))
	if subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		return orm.Forbiddenf("invalid or missing webhook secret")
	}
	return nil
}
