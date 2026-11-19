package models

// Access guard for tbl.website_site and tbl.website_theme (plan T4, owner decision
// D1 = (c)).
//
// base.group_user has blanket CRUD on tbl.* by design. A site row carries the public
// header/footer HTML, custom head/footer HTML, theme and publish state of a site that
// is served same-origin with the SPA, so an internal user able to write it can place
// script that runs in an admin's session. Only base.group_system may create, change,
// publish or delete sites and theme presets. Reads stay on standard ACL (the builder
// and public rendering read them). Pages, menus and the other website models are not
// guarded here.
//
// custom_head_html / custom_footer_html are raw, unsanitized HTML injected into every
// published page (plan T22): only a superadmin (sudo env) may set them to a non-empty
// value; base.group_system may still clear them.

import (
	"context"
	"slices"
	"strings"

	"git.thebase.vn/base_hub/internal/orm"
)

const websiteSiteManagerGroupBID = "base.group_system"

// websiteSiteRawHTMLFields hold unsanitized HTML; setting them needs a superadmin.
var websiteSiteRawHTMLFields = []string{"custom_head_html", "custom_footer_html"}

// websiteGuardedModelNames are the models whose mutations need base.group_system.
var websiteGuardedModelNames = []string{WebsiteSiteModelName, WebsiteThemeModelName}

// Test seams over kernel helpers that need the auth database.
var (
	websiteSiteCallerGroupBIDs = orm.CallerGroupBIDs
	websiteSiteDefaultAccess   = orm.EnsureDefaultModelAccess
)

func init() {
	for _, name := range websiteGuardedModelNames {
		orm.RegisterAccessHandler(name, websiteSiteAccess)
	}
}

// websiteSiteAccess keeps the standard ACL check, then requires base.group_system
// for every non-read operation (create/write/unlink and mutating custom methods
// such as action_publish, which the dispatcher checks as "write"). Sudo, nil env
// and callers without an auth mode (seed import, boot, publish schedule) keep the
// kernel default, matching ensureRBACModelAccess.
func websiteSiteAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := websiteSiteDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if operation == "read" || env == nil || env.IsSudo() {
		return nil
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return nil
	}
	groups, err := websiteSiteCallerGroupBIDs(orm.ContextWithEnvironment(ctx, env))
	if err != nil {
		return err
	}
	if !slices.Contains(groups, websiteSiteManagerGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can %s %s", websiteSiteManagerGroupBID, operation, model.Name)
	}
	if model.Name == WebsiteSiteModelName {
		for _, field := range websiteSiteRawHTMLFields {
			if raw, ok := values[field]; ok && raw != nil {
				if text, isText := raw.(string); isText && strings.TrimSpace(text) == "" {
					continue
				}
				return orm.Forbiddenf("permission denied: only a superadmin can set %s on %s", field, model.Name)
			}
		}
	}
	return nil
}
