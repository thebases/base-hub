package models

// File overview:
// - Responsibility: install-state gate for the website module's anonymous alias routes.
// - Key symbols: `websiteAnonymousRouteClosed`.
// - Pattern: alias routes are mounted once per process for every DB, so an anonymous handler
//   must check that `website` is installed in the request's DB before touching its tables.

import (
	"context"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/queries"
	"git.thebase.vn/base_hub/internal/services/log"
)

// websiteInstalledInRequestDB reports whether `website` is installed in the DB bound to ctx
// (the anonymous alias handler binds it from ?db= / X-Database-Name, else the home DB).
// A variable so tests can run the handlers without a database.
var websiteInstalledInRequestDB = func(ctx context.Context) (bool, error) {
	return queries.ModuleInstalled(ctx, orm.PGXPoolFromContext(ctx), "website")
}

// websiteAnonymousRouteClosed returns the 404 reply an anonymous website route gives when the
// module is not installed in the request's DB (or the catalog cannot be read), and ok=false
// when the route may proceed. A tenant without `website` must not accept contact requests,
// form submissions or data-source reads into tables it never installed.
func websiteAnonymousRouteClosed(ctx context.Context) (orm.AliasResponse, bool) {
	installed, err := websiteInstalledInRequestDB(ctx)
	if err != nil {
		log.Errorf("website: anonymous route install check: %v", err)
		return contactReply(404, false, "not found"), true
	}
	if !installed {
		return contactReply(404, false, "not found"), true
	}
	return orm.AliasResponse{}, false
}
