package models

// File overview:
// - Responsibility: tickets left by visitors through the "Contact us" page template. The public
//   submit endpoint is an anonymous alias route, so it validates, throttles and stores the request
//   itself; admins triage the rows from the Contact Requests screen.
// - Key symbols: `WebsiteContactRequestModel`, `submitWebsiteContactRequest`.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const WebsiteContactRequestModelName = "tbl.website_contact_request"

const (
	contactRequestMaxPerWindow = 5
	contactRequestWindow       = 10 * time.Minute
)

type WebsiteContactRequestRecord struct {
	orm.BaseModel
	SiteID    *int64 `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:SET NULL,index,label:Site,help:Site whose contact form received the request."`
	Name      string `bun:"name" json:"name" meta:"index,required,label:Name,help:Visitor name."`
	Email     string `bun:"email" json:"email" meta:"index,required,label:Email,help:Visitor email used to reply."`
	Subject   string `bun:"subject" json:"subject" meta:"label:Subject,help:Short subject of the request."`
	Message   string `bun:"message" json:"message" meta:"kind:text,required,label:Message,help:Request body."`
	State     string `bun:"state,notnull,default:'new'" json:"state" meta:"required,index,label:Status,selection:new|New;in_progress|In Progress;done|Done;spam|Spam,help:Triage state of the ticket."`
	IP        string `bun:"ip" json:"ip" meta:"readonly,index,label:IP,help:Client IP, used for throttling."`
	UserAgent string `bun:"user_agent" json:"user_agent" meta:"readonly,label:User Agent,help:Client user agent."`
}

var WebsiteContactRequestModel = orm.Define[WebsiteContactRequestRecord](&Models, 460, orm.ModelSpec{
	Name:        WebsiteContactRequestModelName,
	Label:       "Website Contact Requests",
	Description: "Requests (tickets) submitted through the website Contact us page",
	RuntimeMethods: []orm.RuntimeMethod{
		{
			Name:           "submit_public",
			Public:         true,
			ModelOnly:      true,
			AliasRoute:     "POST /website/runtime/contact",
			AliasAnonymous: true,
			Call:           submitWebsiteContactRequest,
			Description:    "Anonymous endpoint behind the Contact us page form. Body: {name, email, subject?, message}.",
		},
	},
})

func init() {
	// The builder's form block with key "contact-us" files a contact request, like the Contact us template.
	if err := RegisterWebsiteFormTarget("contact-us", contactFormTarget); err != nil {
		panic(err)
	}
}

type contactSubmission struct {
	Name    string `json:"name"`
	Email   string `json:"email"`
	Subject string `json:"subject"`
	Message string `json:"message"`
	// Honeypot: a hidden input real visitors never fill.
	Trap string `json:"website_url"`
}

func contactReply(status int, ok bool, message string) orm.AliasResponse {
	body, _ := json.Marshal(map[string]any{"ok": ok, "message": message})
	return orm.AliasResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(body)}
}

// submitWebsiteContactRequest stores one visitor request. There is no identity and no ACL on this
// route, so every guard lives here: field validation and length caps, a honeypot (answered with a
// fake success so bots learn nothing), and a per-IP window limit.
func submitWebsiteContactRequest(ctx context.Context, model *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	request, ok := orm.AliasRequestFromKwargs(kwargs)
	if !ok {
		return nil, nil
	}
	if reply, closed := websiteAnonymousRouteClosed(ctx); closed {
		return reply, nil
	}
	var in contactSubmission
	if err := json.Unmarshal(request.Body, &in); err != nil {
		return contactReply(400, false, "invalid request"), nil
	}
	if strings.TrimSpace(in.Trap) != "" {
		return contactReply(200, true, "sent"), nil
	}
	in.Name, in.Email = strings.TrimSpace(in.Name), strings.TrimSpace(in.Email)
	in.Subject, in.Message = strings.TrimSpace(in.Subject), strings.TrimSpace(in.Message)
	if in.Name == "" || in.Message == "" {
		return contactReply(400, false, "name and message are required"), nil
	}
	if _, err := mail.ParseAddress(in.Email); err != nil {
		return contactReply(400, false, "a valid email is required"), nil
	}
	if utf8.RuneCountInString(in.Name) > 200 || utf8.RuneCountInString(in.Email) > 320 ||
		utf8.RuneCountInString(in.Subject) > 300 || utf8.RuneCountInString(in.Message) > 5000 {
		return contactReply(400, false, "a field is too long"), nil
	}
	ref := orm.RuntimeModelRef(WebsiteContactRequestModelName, model)
	if request.IP != "" {
		recent, err := orm.RuntimeRepositorySearchRead(ctx, ref, orm.RecordMap{"ip": request.IP, "__order": "id desc", "__limit": contactRequestMaxPerWindow}, []string{"id", "created_at"})
		if err != nil {
			return nil, err
		}
		if len(recent) >= contactRequestMaxPerWindow && recentWithin(recent[len(recent)-1]["created_at"], contactRequestWindow) {
			return contactReply(429, false, "too many requests, try again later"), nil
		}
	}
	values := orm.RecordMap{
		"name": in.Name, "email": in.Email, "subject": in.Subject, "message": in.Message,
		"state": "new", "ip": request.IP, "user_agent": truncateRunes(request.UserAgent, 500),
	}
	if siteID := contactSiteIDForHost(ctx, request.Headers["host"]); siteID > 0 {
		values["site_id"] = siteID
	}
	if _, err := orm.RuntimeRepositoryCreate(ctx, ref, values); err != nil {
		log.Errorf("website: store contact request: %v", err)
		return contactReply(500, false, "could not save the request"), nil
	}
	return contactReply(200, true, "sent"), nil
}

// contactSiteIDForHost resolves the site the form was submitted on: the active domain binding of
// the request host, else the first active site (the runtime's own fallback). 0 when none.
func contactSiteIDForHost(ctx context.Context, host string) int64 {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.LastIndex(host, ":"); i > 0 && !strings.Contains(host, "]") {
		host = host[:i]
	}
	if host != "" {
		rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteDomainModelName, nil), orm.RecordMap{"host": host, "is_active": true, "__limit": 1}, []string{"site_id"})
		if err == nil && len(rows) > 0 {
			if id := anyToInt64(rows[0]["site_id"]); id > 0 {
				return id
			}
		}
	}
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(WebsiteSiteModelName, nil), orm.RecordMap{"is_active": true, "__order": "id asc", "__limit": 1}, []string{"id"})
	if err != nil || len(rows) == 0 {
		return 0
	}
	return anyToInt64(rows[0]["id"])
}

func recentWithin(value any, window time.Duration) bool {
	switch v := value.(type) {
	case time.Time:
		return time.Since(v) < window
	case string:
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			return time.Since(t) < window
		}
	}
	return false
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// contactFormTarget turns the fields of a "contact-us" form block into a contact request. Missing or
// invalid name, email or message is an error (the endpoint answers 400); the generic submission row
// is stored either way.
func contactFormTarget(ctx context.Context, siteID int64, fields map[string]string) error {
	name, email, message := fields["name"], fields["email"], fields["message"]
	if name == "" || message == "" {
		return fmt.Errorf("name and message are required")
	}
	if _, err := mail.ParseAddress(email); err != nil {
		return fmt.Errorf("a valid email is required")
	}
	values := orm.RecordMap{
		"name": truncateRunes(name, 200), "email": truncateRunes(email, 320),
		"subject": truncateRunes(fields["subject"], 300), "message": truncateRunes(message, 5000), "state": "new",
	}
	if siteID > 0 {
		values["site_id"] = siteID
	}
	_, err := orm.RuntimeRepositoryCreate(ctx, orm.RuntimeModelRef(WebsiteContactRequestModelName, nil), values)
	return err
}
