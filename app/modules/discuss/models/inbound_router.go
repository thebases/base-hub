package models

// File overview:
// - Responsibility: ports a simplified inbound mail-thread
//   message_route/message_new/message_update dispatch — given a parsed
//   inbound email, decide whether it is a reply to an existing
//   tbl.discuss_message (by Message-ID header matching), a new/forced-thread
//   message routed through a tbl.discuss_alias, or unroutable. Ownership: discuss
//   module. Audit-sensitivity: low.
// - Key symbols: `InboundEmail`, `RouteInboundEmail`.
// - Pattern: provider-agnostic — this file has no knowledge of any specific
//   email/webhook vendor's payload shape; the HTTP boundary
//   (routes/inbound_email_route.go) is responsible for translating a
//   provider's actual webhook body into the InboundEmail struct below.
//   Same-package model reads/writes go through orm.RuntimeRepository* per
//   this module's established ctx-preservation convention (see service.go).

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// InboundEmail is the provider-agnostic shape RouteInboundEmail consumes.
// MessageIDHeader is this email's own Message-ID (stored on the created
// tbl.discuss_message so a later reply to THIS email can be matched back);
// InReplyTo/References are the headers used to match THIS email against an
// earlier tbl.discuss_message it is replying to.
type InboundEmail struct {
	MessageIDHeader string
	InReplyTo       string
	References      []string
	FromEmail       string
	ToEmails        []string
	CcEmails        []string
	Subject         string
	BodyHTML        string
}

// InboundRouteOutcome reports what RouteInboundEmail actually did, for the
// webhook handler to log/respond with — never an error for the "no route
// matched" case, bouncing without crashing.
type InboundRouteOutcome struct {
	Status    string // "replied_to_existing" | "created_via_alias" | "no_route" | "duplicate"
	ResModel  string
	ResID     int64
	MessageID int64
}

// RouteInboundEmail decides where an inbound email belongs and posts it
// there. It never returns an error for "no matching thread or alias" — that
// is a normal, expected outcome (Status: "no_route"), only infrastructure
// failures (a failed DB read/write) are returned as errors.
//
// Idempotency (plan T73): a mail relay retries until it gets a 2xx, so the
// same email can arrive more than once, also concurrently. An email whose
// Message-ID is already stored on a tbl.discuss_message is a "duplicate"
// success that creates nothing. The routing runs in one transaction, so when
// two deliveries race, the loser's insert hits the unique message_id_header
// index (SQLSTATE 23505), its alias-created record rolls back with it, and it
// too reports the stored message as a duplicate.
func RouteInboundEmail(ctx context.Context, email InboundEmail) (InboundRouteOutcome, error) {
	email.MessageIDHeader = strings.TrimSpace(email.MessageIDHeader)
	if outcome, found, err := findInboundDuplicate(ctx, email.MessageIDHeader); err != nil || found {
		return outcome, err
	}

	var outcome InboundRouteOutcome
	err := discussTx(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = routeInboundEmail(txCtx, email)
		return err
	})
	if err != nil && isUniqueViolation(err) {
		if duplicate, found, lookupErr := findInboundDuplicate(ctx, email.MessageIDHeader); lookupErr == nil && found {
			return duplicate, nil
		}
	}
	return outcome, err
}

// findInboundDuplicate returns the stored message carrying messageIDHeader as
// a "duplicate" outcome. An empty header cannot be deduplicated.
func findInboundDuplicate(ctx context.Context, messageIDHeader string) (InboundRouteOutcome, bool, error) {
	if messageIDHeader == "" {
		return InboundRouteOutcome{}, false, nil
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussMessageModelName, nil), orm.RecordMap{
		"message_id_header": messageIDHeader,
	}, []string{"id", "res_model", "res_id"})
	if err != nil || len(rows) == 0 {
		return InboundRouteOutcome{}, false, err
	}
	log.Infof("discuss inbound email: message-id %q already stored, skipping duplicate delivery", messageIDHeader)
	resModel, _ := rows[0]["res_model"].(string)
	return InboundRouteOutcome{
		Status:    "duplicate",
		ResModel:  resModel,
		ResID:     int64Value(rows[0]["res_id"]),
		MessageID: int64Value(rows[0]["id"]),
	}, true, nil
}

// routeInboundEmail is RouteInboundEmail's routing body, run inside its transaction.
func routeInboundEmail(ctx context.Context, email InboundEmail) (InboundRouteOutcome, error) {
	if resModel, resID, found, err := findReplyTarget(ctx, email); err != nil {
		return InboundRouteOutcome{}, err
	} else if found {
		return postInboundReply(ctx, email, resModel, resID)
	}

	alias, found, err := findMatchingAlias(ctx, email.ToEmails, email.CcEmails)
	if err != nil {
		return InboundRouteOutcome{}, err
	}
	if !found {
		log.Warnf("discuss inbound email: no reply target or alias matched for message-id %q", email.MessageIDHeader)
		return InboundRouteOutcome{Status: "no_route"}, nil
	}
	return routeThroughAlias(ctx, email, alias)
}

// findReplyTarget matches email.InReplyTo/References against an existing
// tbl.discuss_message's MessageIDHeader, returning the matched message's own
// (res_model, res_id) target — a reply always attaches to the SAME record
// the original message was posted on, not to the message itself.
func findReplyTarget(ctx context.Context, email InboundEmail) (string, int64, bool, error) {
	candidates := make([]string, 0, len(email.References)+1)
	if strings.TrimSpace(email.InReplyTo) != "" {
		candidates = append(candidates, strings.TrimSpace(email.InReplyTo))
	}
	for _, ref := range email.References {
		if trimmed := strings.TrimSpace(ref); trimmed != "" {
			candidates = append(candidates, trimmed)
		}
	}
	if len(candidates) == 0 {
		return "", 0, false, nil
	}

	// Walk each candidates entry in order, most recent (In-Reply-To) first, so
	// the first hit is preferred over older References entries.
	for _, candidate := range candidates {
		rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussMessageModelName, nil), orm.RecordMap{
			"message_id_header": candidate,
		}, []string{"res_model", "res_id"})
		if err != nil {
			return "", 0, false, err
		}
		if len(rows) > 0 {
			resModel, _ := rows[0]["res_model"].(string)
			return resModel, int64Value(rows[0]["res_id"]), true, nil
		}
	}
	return "", 0, false, nil
}

// postInboundReply posts the inbound email as a new tbl.discuss_message on the
// same (resModel, resID) as the message it replies to.
func postInboundReply(ctx context.Context, email InboundEmail, resModel string, resID int64) (InboundRouteOutcome, error) {
	body := email.BodyHTML
	messageID, err := PostMessage(ctx, resModel, resID, nil, nil, "email", subjectOrNil(email.Subject), &body, nil, messageIDHeaderOrNil(email.MessageIDHeader), nil)
	if err != nil {
		return InboundRouteOutcome{}, err
	}
	return InboundRouteOutcome{Status: "replied_to_existing", ResModel: resModel, ResID: resID, MessageID: messageID}, nil
}

// findMatchingAlias matches the local-part of every to/cc address against
// tbl.discuss_alias.AliasName, returning the first match. Address matching is
// local-part-only (this module has no alias_domain concept, unlike some mail systems'
// mail.alias.domain) — a known simplification, documented in the plan.
func findMatchingAlias(ctx context.Context, toEmails, ccEmails []string) (DiscussAliasRecord, bool, error) {
	addresses := append(append([]string{}, toEmails...), ccEmails...)
	for _, address := range addresses {
		localPart := emailLocalPart(address)
		if localPart == "" {
			continue
		}
		rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussAliasModelName, nil), orm.RecordMap{
			"alias_name": localPart,
		}, []string{"id", "alias_name", "alias_model_id", "alias_defaults", "alias_force_thread_id", "alias_contact"})
		if err != nil {
			return DiscussAliasRecord{}, false, err
		}
		if len(rows) > 0 {
			return aliasRecordFromRow(rows[0]), true, nil
		}
	}
	return DiscussAliasRecord{}, false, nil
}

// routeThroughAlias either posts onto the alias's forced thread, or creates a
// brand-new record on the alias's target model and posts the email onto it.
func routeThroughAlias(ctx context.Context, email InboundEmail, alias DiscussAliasRecord) (InboundRouteOutcome, error) {
	// Re-check the target at routing time: alias rows saved before the
	// save-time constraint existed (or written by raw SQL) must not let an
	// anonymous webhook call create rows on a sensitive model. A rejected
	// alias is a "no_route" outcome, not an error, so the relay stops retrying.
	if err := validateAliasTargetModel(ctx, alias.AliasModelID); err != nil {
		var ormErr *orm.Error
		if !errors.As(err, &ormErr) || ormErr.Code != orm.ErrInvalidParams {
			return InboundRouteOutcome{}, err
		}
		log.Errorf("discuss inbound email: alias %q refused: %v", alias.AliasName, err)
		return InboundRouteOutcome{Status: "no_route"}, nil
	}
	if alias.AliasForceThreadID != nil {
		return postInboundReply(ctx, email, alias.AliasModelID, *alias.AliasForceThreadID)
	}

	defaults := parseAliasDefaults(alias.AliasDefaults)
	created, err := orm.ExecuteRuntimeMethod(ctx, alias.AliasModelID, "create", nil, []any{defaults}, nil)
	if err != nil {
		return InboundRouteOutcome{}, err
	}
	createdMap, ok := created.(orm.RecordMap)
	if !ok {
		log.Errorf("discuss inbound email: alias %q create on %q returned unexpected type %T", alias.AliasName, alias.AliasModelID, created)
		return InboundRouteOutcome{Status: "no_route"}, nil
	}
	newResID := int64Value(createdMap["id"])
	if newResID <= 0 {
		log.Errorf("discuss inbound email: alias %q create on %q did not return an id", alias.AliasName, alias.AliasModelID)
		return InboundRouteOutcome{Status: "no_route"}, nil
	}
	return postInboundReply(ctx, email, alias.AliasModelID, newResID)
}

// parseAliasDefaults decodes AliasDefaults' JSON-object text into a plain
// map, returning an empty map for a nil/blank/unparsable value rather than
// failing the whole routing attempt over a malformed config row.
func parseAliasDefaults(raw *string) map[string]any {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return map[string]any{}
	}
	var defaults map[string]any
	if err := json.Unmarshal([]byte(*raw), &defaults); err != nil {
		log.Warnf("tbl.discuss_alias: alias_defaults is not valid JSON, ignoring: %v", err)
		return map[string]any{}
	}
	// The primary key is never a default; letting it through would make the
	// create collide with (or target) an existing row.
	delete(defaults, "id")
	return defaults
}

// aliasRecordFromRow rebuilds a DiscussAliasRecord from a generic
// RuntimeRepositorySearchRead row map (the generic ORM read path returns
// plain map[string]any, not a typed struct).
func aliasRecordFromRow(row map[string]any) DiscussAliasRecord {
	record := DiscussAliasRecord{}
	record.ID = int64Value(row["id"])
	record.AliasName, _ = row["alias_name"].(string)
	record.AliasModelID, _ = row["alias_model_id"].(string)
	if defaults, ok := row["alias_defaults"].(string); ok {
		record.AliasDefaults = &defaults
	}
	if forceThreadID := int64Value(row["alias_force_thread_id"]); forceThreadID > 0 {
		record.AliasForceThreadID = &forceThreadID
	}
	record.AliasContact, _ = row["alias_contact"].(string)
	return record
}

// emailLocalPart returns the part of an email address before "@", or "" for
// an address with no "@".
func emailLocalPart(address string) string {
	address = strings.TrimSpace(address)
	atIndex := strings.LastIndex(address, "@")
	if atIndex <= 0 {
		return ""
	}
	return address[:atIndex]
}

// subjectOrNil returns nil for an empty subject so PostMessage leaves the
// column at its own default rather than storing an explicit empty string.
func subjectOrNil(subject string) *string {
	if strings.TrimSpace(subject) == "" {
		return nil
	}
	return &subject
}

// messageIDHeaderOrNil mirrors subjectOrNil for the Message-ID header field.
func messageIDHeaderOrNil(header string) *string {
	if strings.TrimSpace(header) == "" {
		return nil
	}
	return &header
}
