package models

// File overview:
// - Responsibility: implements outbound email sending — EnqueueMail creates a
//   tbl.discuss_mail queue row, and the cron-driven SendQueuedMail sweep attempts
//   delivery through the lowest-Sequence active tbl.discuss_mail_server via
//   Go's standard net/smtp (crypto/tls added for UseTLS-configured servers).
//   This repo has no existing SMTP infra: with zero active tbl.discuss_mail_server
//   rows configured (the expected state in this environment, which holds no
//   real SMTP credentials), SendQueuedMail marks every pending row
//   "exception" with a "no mail server configured" reason and returns nil —
//   that is the correct no-op path, not a bug. Ownership: discuss module.
//   Audit-sensitivity: low (no balance/payment mutation), but every attempt
//   is durably recorded on its tbl.discuss_mail row before/after the network call
//   so failures are always auditable.
// - Key symbols: `EnqueueMail`, `SendQueuedMail`, `sendOneMail`, `InitMailCron`.
// - Pattern: same-package writes go through `orm.RuntimeRepositoryCreate/
//   Write/SearchRead` (never `Handler[T].Create`), matching service.go's
//   ctx-preservation rationale. The sweep loop stays flat (no nested
//   closures); `sendOneMail` is the one helper extracted from it, mirroring
//   service.go's `createNotificationAndPublish` precedent ("one operation,
//   one focused function"). Cron registration mirrors base-automation's
//   `service.InitRuntime` (`cron.RegisterHandler` at boot, `cron.
//   UnregisterHandler` on shutdown) — the real registration API read from
//   `internal/services/cron/cron.go` before wiring this in.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"github.com/uptrace/bun"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/cron"
	"git.thebase.vn/base_hub/internal/services/log"
)

// mailCronHandlerKey is the sys.cron `handler` value that dispatches to
// SendQueuedMail. No sys.cron seed row is added by this task (see the plan's
// explicit scope), so this handler currently only fires if/when an operator
// or another module creates a matching sys.cron row.
const mailCronHandlerKey = "discuss.send_queued_mail"

// EnqueueMail creates one tbl.discuss_mail row with State="outgoing", the
// mail_thread `message_post`-with-email-notification equivalent. It never
// sends synchronously — SendQueuedMail's sweep performs the actual delivery.
func EnqueueMail(ctx context.Context, messageID *int64, emailTo string, emailCc, subject, bodyHTML *string) (int64, error) {
	values := orm.RecordMap{
		"email_to": emailTo,
		"state":    "outgoing",
	}
	if messageID != nil {
		values["message_id"] = *messageID
	}
	if emailCc != nil {
		values["email_cc"] = *emailCc
	}
	if subject != nil {
		values["subject"] = *subject
	}
	if bodyHTML != nil {
		values["body_html"] = *bodyHTML
	}

	created, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(DiscussMailModelName, nil), values)
	if err != nil {
		return 0, err
	}
	return int64Value(created["id"]), nil
}

const (
	// mailClaimBatchLimit bounds how many rows one claim takes.
	mailClaimBatchLimit = 100
	// mailClaimMaxBatches bounds one sweep (mailClaimBatchLimit * this many rows).
	mailClaimMaxBatches = 20
	// mailSendingStaleAfter is how long a row may sit in "sending" before a later
	// sweep treats the claiming process as dead and re-queues it. It must exceed
	// the SMTP timeout so a live send is never re-claimed.
	mailSendingStaleAfter = 10 * time.Minute
	// smtpDialTimeout and smtpSessionTimeout bound a single delivery attempt.
	smtpDialTimeout    = 10 * time.Second
	smtpSessionTimeout = 60 * time.Second
)

// mailDeliver performs one SMTP delivery; a var so tests can substitute a fake transport.
var mailDeliver = deliverMail

// SendQueuedMail is the cron-invoked sweep. It atomically claims outgoing
// tbl.discuss_mail rows (outgoing -> sending, FOR UPDATE SKIP LOCKED) so
// overlapping ticks or nodes never send the same row twice, resolves the
// active tbl.discuss_mail_server with the lowest Sequence, and attempts delivery
// for each claimed row. With no active server configured, every claimed row is
// marked exception and this returns nil — the expected no-op path when no SMTP
// infra is configured.
func SendQueuedMail(ctx context.Context) error {
	db := mailDB(ctx)
	if db == nil {
		return fmt.Errorf("tbl.discuss_mail: database is not initialized")
	}
	var server *DiscussMailServerRecord
	serverLoaded := false
	for batch := 0; batch < mailClaimMaxBatches; batch++ {
		claimed, err := claimOutgoingMail(ctx, db, mailClaimBatchLimit)
		if err != nil {
			return err
		}
		if len(claimed) == 0 {
			return nil
		}
		if !serverLoaded {
			found := false
			server, found, err = activeMailServer(ctx)
			if err != nil || !found {
				server = nil
			}
			serverLoaded = true
			if err != nil {
				// Give the claimed rows back so the next tick retries them.
				releaseClaimedMail(ctx, db, claimed)
				return err
			}
		}
		if server == nil {
			markAllPendingNoServer(ctx, claimed)
			continue
		}
		// Walk each claimed row in order so one row's send attempt never blocks or
		// interleaves with another's bookkeeping.
		for _, row := range claimed {
			if ctx.Err() != nil {
				releaseClaimedMail(ctx, db, claimed)
				return ctx.Err()
			}
			sendOneMail(ctx, server, row)
		}
		if len(claimed) < mailClaimBatchLimit {
			return nil
		}
	}
	return nil
}

// mailDB resolves the tenant database from ctx, falling back to the default pool.
func mailDB(ctx context.Context) bun.IDB {
	if env := orm.EnvironmentFromContext(ctx); env != nil && env.DB() != nil {
		return env.DB()
	}
	return orm.GetBunDB()
}

// claimOutgoingMail moves up to limit outgoing rows to state "sending" in one
// statement and returns them. SKIP LOCKED means concurrent sweeps never claim
// the same row. Rows stuck in "sending" past mailSendingStaleAfter (claimer
// crashed mid-send) are re-queued by the same statement, so a crash delays a
// mail instead of losing it; that makes delivery at-least-once.
func claimOutgoingMail(ctx context.Context, db bun.IDB, limit int) ([]orm.RecordMap, error) {
	if limit <= 0 {
		limit = mailClaimBatchLimit
	}
	type claimedRow struct {
		ID       int64   `bun:"id"`
		EmailTo  string  `bun:"email_to"`
		EmailCc  *string `bun:"email_cc"`
		Subject  *string `bun:"subject"`
		BodyHTML *string `bun:"body_html"`
	}
	var rows []claimedRow
	err := db.NewRaw(`
		UPDATE tbl_discuss_mail AS m
		SET state = 'sending', updated_at = now()
		WHERE m.id IN (
			SELECT t.id FROM tbl_discuss_mail AS t
			WHERE t.active = TRUE
			  AND ((t.state = 'outgoing' AND (t.scheduled_at IS NULL OR t.scheduled_at <= now()))
			       OR (t.state = 'sending' AND t.updated_at < now() - make_interval(secs => ?)))
			ORDER BY t.id ASC
			LIMIT ?
			FOR UPDATE SKIP LOCKED
		)
		RETURNING m.id, m.email_to, m.email_cc, m.subject, m.body_html
	`, int(mailSendingStaleAfter.Seconds()), limit).Scan(ctx, &rows)
	if err != nil {
		return nil, err
	}
	out := make([]orm.RecordMap, 0, len(rows))
	for _, r := range rows {
		row := orm.RecordMap{"id": r.ID, "email_to": r.EmailTo}
		if r.EmailCc != nil {
			row["email_cc"] = *r.EmailCc
		}
		if r.Subject != nil {
			row["subject"] = *r.Subject
		}
		if r.BodyHTML != nil {
			row["body_html"] = *r.BodyHTML
		}
		out = append(out, row)
	}
	return out, nil
}

// releaseClaimedMail returns still-unsent claimed rows to "outgoing" (best effort).
// Only rows still in "sending" are touched, so rows already marked sent/exception
// by sendOneMail are never resurrected.
func releaseClaimedMail(ctx context.Context, db bun.IDB, rows []orm.RecordMap) {
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, int64Value(row["id"]))
	}
	if len(ids) == 0 {
		return
	}
	// A cancelled sweep ctx must not stop the release itself.
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if _, err := db.NewUpdate().Table("tbl_discuss_mail").
		Set("state = 'outgoing'").Set("updated_at = now()").
		Where("id IN (?)", bun.In(ids)).Where("state = 'sending'").
		Exec(releaseCtx); err != nil {
		log.Warnf("tbl.discuss_mail: release of %d claimed rows failed: %v", len(ids), err)
	}
}

// activeMailServer returns the active tbl.discuss_mail_server with the lowest
// Sequence, if any exist.
func activeMailServer(ctx context.Context) (*DiscussMailServerRecord, bool, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(DiscussMailServerModelName, nil), orm.RecordMap{
		"active": true,
	}, []string{"id", "host", "port", "username", "password", "use_tls", "sequence"})
	if err != nil {
		return nil, false, err
	}
	if len(rows) == 0 {
		return nil, false, nil
	}

	best := rows[0]
	bestSequence := int64Value(best["sequence"])
	// Walk each remaining row in order, keeping the lowest-Sequence one seen so far.
	for _, row := range rows[1:] {
		sequence := int64Value(row["sequence"])
		if sequence < bestSequence {
			best = row
			bestSequence = sequence
		}
	}

	server := &DiscussMailServerRecord{
		Host:   stringValue(best["host"]),
		Port:   int(int64Value(best["port"])),
		UseTLS: boolValue(best["use_tls"]),
	}
	server.ID = int64Value(best["id"])
	if username := stringValue(best["username"]); username != "" {
		server.Username = &username
	}
	if password := stringValue(best["password"]); password != "" {
		server.Password = &password
	}
	return server, true, nil
}

// markAllPendingNoServer marks every pending row exception with a
// "no mail server configured" reason — the expected state in an environment
// with no tbl.discuss_mail_server rows configured, not an error condition.
func markAllPendingNoServer(ctx context.Context, pendingRows []orm.RecordMap) {
	for _, row := range pendingRows {
		mailID := int64Value(row["id"])
		if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussMailModelName, nil), []string{strconv.FormatInt(mailID, 10)}, orm.RecordMap{
			"state":          "exception",
			"failure_reason": "no mail server configured",
		}); err != nil {
			log.Warnf("tbl.discuss_mail %d: failed to record no-server exception: %v", mailID, err)
		}
	}
}

// sendOneMail attempts delivery of one tbl.discuss_mail row through server, then
// records the outcome on that row. This is the one helper extracted from
// SendQueuedMail's sweep loop, matching service.go's createNotificationAndPublish
// precedent for "one operation, one focused function".
func sendOneMail(ctx context.Context, server *DiscussMailServerRecord, row orm.RecordMap) {
	mailID := int64Value(row["id"])
	err := mailDeliver(ctx, server, stringValue(row["email_to"]), stringValue(row["email_cc"]), stringValue(row["subject"]), stringValue(row["body_html"]))
	if err != nil {
		markMailException(ctx, mailID, server.ID, err)
		return
	}
	markMailSent(ctx, mailID, server.ID)
}

// deliverMail sends one email over SMTP, using an implicit-TLS connection
// when the server is configured with UseTLS (e.g. port 465 SMTPS), or a plain
// net/smtp.SendMail call otherwise (which itself opportunistically upgrades
// via STARTTLS when the server advertises it, e.g. port 587).
func deliverMail(ctx context.Context, server *DiscussMailServerRecord, to, cc, subject, bodyHTML string) error {
	if strings.TrimSpace(to) == "" {
		return orm.InvalidParamsf("tbl.discuss_mail: recipient is required")
	}
	recipients := []string{to}
	if cc != "" {
		recipients = append(recipients, splitAddressList(cc)...)
	}
	from := smtpFromAddress(server)
	message, err := buildMIMEMessage(from, to, cc, subject, bodyHTML)
	if err != nil {
		return err
	}
	auth, err := smtpAuthFor(server)
	if err != nil {
		return err
	}
	addr := fmt.Sprintf("%s:%d", server.Host, server.Port)
	return sendMailSMTP(ctx, addr, server.Host, server.UseTLS, auth, from, recipients, message)
}

// smtpFromAddress picks the envelope-from address: the server's own
// authenticated username when set, else a generic no-reply address at the
// server's host.
func smtpFromAddress(server *DiscussMailServerRecord) string {
	if server.Username != nil && *server.Username != "" {
		return *server.Username
	}
	return "no-reply@" + server.Host
}

// smtpAuthFor builds a PLAIN auth from the server's credentials, or nil when
// no username/password is configured (some SMTP relays allow anonymous send).
func smtpAuthFor(server *DiscussMailServerRecord) (smtp.Auth, error) {
	if server.Username == nil || server.Password == nil {
		return nil, nil
	}
	// The stored password is sealed at rest (mail_server_secret.go).
	password, err := openMailServerPassword(*server.Password)
	if err != nil {
		return nil, err
	}
	return smtp.PlainAuth("", *server.Username, password, server.Host), nil
}

// splitAddressList splits a comma-separated Cc field into individual
// trimmed, non-empty addresses.
func splitAddressList(list string) []string {
	parts := strings.Split(list, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// buildMIMEMessage renders a minimal RFC 5322 HTML email message. Header values containing CR or
// LF are refused (they would inject extra headers or recipients, plan T23); the subject is
// RFC 2047 Q-encoded so non-ASCII text survives; Date and Message-ID are always set.
func buildMIMEMessage(from, to, cc, subject, bodyHTML string) ([]byte, error) {
	for name, value := range map[string]string{"From": from, "To": to, "Cc": cc, "Subject": subject} {
		if strings.ContainsAny(value, "\r\n") {
			return nil, orm.InvalidParamsf("tbl.discuss_mail: %s header must not contain line breaks", name)
		}
	}
	var builder strings.Builder
	builder.WriteString("From: " + from + "\r\n")
	builder.WriteString("To: " + to + "\r\n")
	if cc != "" {
		builder.WriteString("Cc: " + cc + "\r\n")
	}
	builder.WriteString("Subject: " + mime.QEncoding.Encode("utf-8", subject) + "\r\n")
	builder.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	builder.WriteString("Message-ID: " + newMailMessageID(from) + "\r\n")
	builder.WriteString("MIME-Version: 1.0\r\n")
	builder.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	builder.WriteString("\r\n")
	builder.WriteString(bodyHTML)
	return []byte(builder.String()), nil
}

// newMailMessageID returns a unique "<random@domain>" Message-ID, using the sender's domain.
func newMailMessageID(from string) string {
	domain := "localhost"
	if at := strings.LastIndex(from, "@"); at >= 0 && at < len(from)-1 {
		domain = strings.Trim(from[at+1:], "<> ")
	}
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return "<" + hex.EncodeToString(raw) + "@" + domain + ">"
}

// sendMailSMTP delivers one message with bounded dial and session time.
// useTLS dials an implicit-TLS connection first (e.g. port 465 SMTPS); otherwise
// it connects in plaintext and upgrades via STARTTLS when the server advertises
// it (e.g. port 587), like net/smtp.SendMail, which has no timeouts.
func sendMailSMTP(ctx context.Context, addr, host string, useTLS bool, auth smtp.Auth, from string, recipients []string, message []byte) error {
	dialer := &net.Dialer{Timeout: smtpDialTimeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	// Bound the whole session so a stalled server cannot hold a claimed row forever.
	_ = conn.SetDeadline(time.Now().Add(smtpSessionTimeout))
	// Closing the conn is what interrupts a blocked read/write when ctx ends.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if useTLS {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: host})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			conn.Close()
			return err
		}
		conn = tlsConn
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer client.Close()

	if !useTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
				return err
			}
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(message); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// markMailSent records a successful delivery on a tbl.discuss_mail row.
func markMailSent(ctx context.Context, mailID, serverID int64) {
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussMailModelName, nil), []string{strconv.FormatInt(mailID, 10)}, orm.RecordMap{
		"state":          "sent",
		"sent_at":        time.Now().UTC(),
		"mail_server_id": serverID,
	}); err != nil {
		log.Warnf("tbl.discuss_mail %d: failed to record sent state: %v", mailID, err)
	}
}

// markMailException records a failed delivery on a tbl.discuss_mail row. The
// send failure itself is only logged as a warning here (matching this
// module's best-effort-secondary-effect convention) — the durable record of
// the failure is the tbl.discuss_mail row's own State/FailureReason.
func markMailException(ctx context.Context, mailID, serverID int64, cause error) {
	log.Warnf("tbl.discuss_mail %d: send failed: %v", mailID, cause)
	if _, err := orm.RuntimeRepositoryWrite(ctx, orm.RuntimeModelRef(DiscussMailModelName, nil), []string{strconv.FormatInt(mailID, 10)}, orm.RecordMap{
		"state":          "exception",
		"failure_reason": cause.Error(),
		"mail_server_id": serverID,
	}); err != nil {
		log.Warnf("tbl.discuss_mail %d: failed to record exception state: %v", mailID, err)
	}
}

// stringValue coerces a RecordMap value into a trimmed string, returning ""
// for anything that isn't a string (mirrors base-calendar's helpers_runtime.
// go:stringValue, which this package cannot import since it's another
// module's unexported helper).
func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case *string:
		if typed == nil {
			return ""
		}
		return strings.TrimSpace(*typed)
	default:
		return ""
	}
}

// boolValue coerces a RecordMap value of unknown boolean shape into bool,
// returning false for anything it can't convert.
func boolValue(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case *bool:
		return typed != nil && *typed
	default:
		return false
	}
}

// InitMailCron registers SendQueuedMail with the kernel's sys.cron scheduler
// under mailCronHandlerKey, mirroring base-automation's service.InitRuntime
// (`cron.RegisterHandler` at boot, `cron.UnregisterHandler` on shutdown).
// Wired from discuss.go's Bootstrap alongside the existing
// boot.RegisterRuntime("discuss.runtime.models", ...) call.
func InitMailCron() func() {
	if err := cron.RegisterHandler(mailCronHandlerKey, sendQueuedMailCronHandler); err != nil {
		log.Fatalf("discuss: register cron handler: %v", err)
	}
	return func() {
		cron.UnregisterHandler(mailCronHandlerKey)
	}
}

// sendQueuedMailCronHandler adapts SendQueuedMail to cron.HandlerFunc's
// (ctx, payload) signature — this handler ignores the payload entirely since
// SendQueuedMail always sweeps every outgoing row.
func sendQueuedMailCronHandler(ctx context.Context, _ map[string]any) error {
	return SendQueuedMail(ctx)
}
