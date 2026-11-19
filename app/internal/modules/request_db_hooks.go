package modules

// File overview:
// - Responsibility: inbound hook letting the optional database module decide which
//   tenant databases a request may select (?db=, X-Database-Name, locked cookie),
//   without internal/ importing modules/database.
// - Key symbols: `RequestDBValidator`, `RegisterRequestDBValidator`, `CheckRequestDBName`,
//   `ErrRequestDBNotAllowed`, `ErrRequestDBUnavailable`.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"

	"git.thebase.vn/base_hub/internal/config"
)

// RequestDBValidator reports whether name is a tenant database a request may
// select. It is only consulted for names other than the home and template DBs,
// runs on the request hot path (so it must be cheap: cached), and returns an
// error when it cannot decide, which callers treat as a rejection.
type RequestDBValidator func(ctx context.Context, name string) (bool, error)

var (
	// ErrRequestDBNotAllowed: the name is not a selectable database (template,
	// unregistered or not running). Maps to HTTP 400.
	ErrRequestDBNotAllowed = errors.New("unknown database")
	// ErrRequestDBUnavailable: the validator could not look the name up.
	// Fail closed; maps to HTTP 503.
	ErrRequestDBUnavailable = errors.New("database is unavailable")
)

// atomic so tests can swap the validator while requests are in flight.
var requestDBValidator atomic.Pointer[RequestDBValidator]

// RegisterRequestDBValidator installs the tenant-registry check (the database
// module calls it from init()). nil clears it.
func RegisterRequestDBValidator(fn RequestDBValidator) {
	if fn == nil {
		requestDBValidator.Store(nil)
		return
	}
	requestDBValidator.Store(&fn)
}

// CheckRequestDBName is the request-DB allowlist (SEC-T-8): the home DB is
// always selectable, the clone-source template never is, and any other name
// must pass the registered validator. Without a validator (database module not
// compiled in, e.g. base-edc) other names keep the previous behaviour: whether
// they can be opened (EnsureNamedBunDBNoCreate) is the only gate.
func CheckRequestDBName(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if name == strings.TrimSpace(config.HomeDBName()) {
		return nil
	}
	// The template is a clone source: a request bound to it could write rows
	// that every future tenant then inherits. Rejected in every mode, also when
	// template_db_name is unset (the default name is what provisioning would use).
	if name == "" || name == strings.TrimSpace(config.TemplateDBName()) {
		return ErrRequestDBNotAllowed
	}
	fn := requestDBValidator.Load()
	if fn == nil {
		return nil
	}
	ok, err := (*fn)(ctx, name)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRequestDBUnavailable, err)
	}
	if !ok {
		return ErrRequestDBNotAllowed
	}
	return nil
}
