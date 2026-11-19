package models

// File overview:
// - Responsibility: submissions of builder form blocks (`form[data-bh-form="<key>"]`). The public
//   endpoint is anonymous, so it validates, caps, throttles and stores the submission itself.
// - Key symbols: `WebsiteFormSubmissionModel`, `submitWebsiteForm`, `RegisterWebsiteFormTarget`.
// - Pattern: a form key may be bound to a Go target (registered by a module that depends on website)
//   which receives the cleaned fields instead of the generic store. Visitors can never name a model:
//   only registered keys reach a target, and every other key is stored here as plain data.

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

const WebsiteFormSubmissionModelName = "tbl.website_form_submission"

const (
	formSubmissionMaxFields   = 30
	formSubmissionMaxValueLen = 5000
	formSubmissionMaxBody     = 64 * 1024
	formSubmissionMaxPerIP    = 10
)

var (
	formKeyRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	formFieldRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
)

type WebsiteFormSubmissionRecord struct {
	orm.BaseModel
	SiteID    *int64         `bun:"site_id" json:"site_id" meta:"kind:many2one,model:tbl.website_site,ondelete:SET NULL,index,label:Site,help:Site whose form received the submission."`
	FormKey   string         `bun:"form_key" json:"form_key" meta:"index,required,label:Form,help:Key of the form block (data-bh-form)."`
	Data      map[string]any `bun:"data,type:jsonb,nullzero" json:"data" meta:"kind:json,label:Data,help:The submitted fields."`
	State     string         `bun:"state,notnull,default:'new'" json:"state" meta:"required,index,label:Status,selection:new|New;done|Done;spam|Spam,help:Triage state."`
	IP        string         `bun:"ip" json:"ip" meta:"readonly,index,label:IP,help:Client IP, used for throttling."`
	UserAgent string         `bun:"user_agent" json:"user_agent" meta:"readonly,label:User Agent,help:Client user agent."`
}

var WebsiteFormSubmissionModel = orm.Define[WebsiteFormSubmissionRecord](&Models, 460, orm.ModelSpec{
	Name:        WebsiteFormSubmissionModelName,
	Label:       "Website Form Submissions",
	Description: "Submissions of form blocks placed on website pages",
	RuntimeMethods: []orm.RuntimeMethod{
		{
			Name:           "submit_public",
			Public:         true,
			ModelOnly:      true,
			AliasRoute:     "POST /website/runtime/form",
			AliasAnonymous: true,
			Call:           submitWebsiteForm,
			Description:    "Anonymous endpoint behind form blocks. Body: {form, fields: {name: value}, website_url (honeypot)}.",
		},
	},
})

// WebsiteFormTarget receives the cleaned fields of a submission for one form key.
type WebsiteFormTarget func(ctx context.Context, siteID int64, fields map[string]string) error

var (
	formTargetsMu sync.RWMutex
	formTargets   = map[string]WebsiteFormTarget{}
)

// RegisterWebsiteFormTarget binds a form key to a module-owned handler (for example a helpdesk
// ticket creator). Call it from the module's Bootstrap; registering a key again replaces it (Bootstrap may run more than once).
func RegisterWebsiteFormTarget(key string, target WebsiteFormTarget) error {
	if !formKeyRe.MatchString(key) || target == nil {
		return errInvalidFormTarget
	}
	formTargetsMu.Lock()
	defer formTargetsMu.Unlock()
	formTargets[key] = target
	return nil
}

var errInvalidFormTarget = &formTargetError{}

type formTargetError struct{}

func (*formTargetError) Error() string {
	return "website form target: key must match [a-z0-9_-], handler required"
}

type formSubmission struct {
	Form   string         `json:"form"`
	Fields map[string]any `json:"fields"`
	Trap   string         `json:"website_url"`
}

// cleanFormFields enforces the caps and returns string-valued fields. Values that are not scalars
// are rejected rather than coerced.
func cleanFormFields(raw map[string]any) (map[string]string, bool) {
	if len(raw) == 0 || len(raw) > formSubmissionMaxFields {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if !formFieldRe.MatchString(k) {
			return nil, false
		}
		var text string
		switch t := v.(type) {
		case string:
			text = t
		case bool:
			if t {
				text = "on"
			}
		case float64:
			text = strings.TrimRight(strings.TrimRight(jsonNumber(t), "0"), ".")
		case nil:
		default:
			return nil, false
		}
		if utf8.RuneCountInString(text) > formSubmissionMaxValueLen {
			return nil, false
		}
		out[k] = strings.TrimSpace(text)
	}
	return out, true
}

func jsonNumber(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}

func submitWebsiteForm(ctx context.Context, model *orm.ModelMetadata, _ []string, _ []any, kwargs map[string]any) (any, error) {
	request, ok := orm.AliasRequestFromKwargs(kwargs)
	if !ok {
		return nil, nil
	}
	if reply, closed := websiteAnonymousRouteClosed(ctx); closed {
		return reply, nil
	}
	if len(request.Body) > formSubmissionMaxBody {
		return contactReply(400, false, "request too large"), nil
	}
	var in formSubmission
	if err := json.Unmarshal(request.Body, &in); err != nil || !formKeyRe.MatchString(in.Form) {
		return contactReply(400, false, "invalid request"), nil
	}
	if strings.TrimSpace(in.Trap) != "" {
		return contactReply(200, true, "sent"), nil
	}
	fields, ok := cleanFormFields(in.Fields)
	if !ok {
		return contactReply(400, false, "invalid fields"), nil
	}
	ref := orm.RuntimeModelRef(WebsiteFormSubmissionModelName, model)
	if request.IP != "" {
		recent, err := orm.RuntimeRepositorySearchRead(ctx, ref, orm.RecordMap{"ip": request.IP, "__order": "id desc", "__limit": formSubmissionMaxPerIP}, []string{"id", "created_at"})
		if err != nil {
			return nil, err
		}
		if len(recent) >= formSubmissionMaxPerIP && recentWithin(recent[len(recent)-1]["created_at"], contactRequestWindow) {
			return contactReply(429, false, "too many requests, try again later"), nil
		}
	}
	siteID := contactSiteIDForHost(ctx, request.Headers["host"])
	formTargetsMu.RLock()
	target := formTargets[in.Form]
	formTargetsMu.RUnlock()
	if target != nil {
		if err := target(ctx, siteID, fields); err != nil {
			log.Errorf("website: form target %s: %v", in.Form, err)
			return contactReply(400, false, "could not process the form"), nil
		}
	}
	// Stored in every case: it is the audit record and the throttle's window, even when a target also handled it.
	names := make([]string, 0, len(fields))
	for k := range fields {
		names = append(names, k)
	}
	sort.Strings(names)
	data := make(map[string]any, len(fields))
	for _, k := range names {
		data[k] = fields[k]
	}
	values := orm.RecordMap{"form_key": in.Form, "data": data, "state": "new", "ip": request.IP, "user_agent": truncateRunes(request.UserAgent, 500)}
	if siteID > 0 {
		values["site_id"] = siteID
	}
	if _, err := orm.RuntimeRepositoryCreate(ctx, ref, values); err != nil {
		log.Errorf("website: store form submission: %v", err)
		return contactReply(500, false, "could not save the submission"), nil
	}
	return contactReply(200, true, "sent"), nil
}
