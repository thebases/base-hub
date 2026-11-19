package cmd

// File overview:
// - Responsibility: idempotency-key validation, storage selection and expiry sweep for the server middleware.
// - Key symbols: `expiringStore`, `idempotencyGCStore`, `ErrInvalidIdempotencyKey`, `shouldSkipIdempotencyMiddleware`,
//   `scopeIdempotencyKey`.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	cronservice "git.thebase.vn/base_hub/internal/services/cron"
	idempotencystore "git.thebase.vn/base_hub/internal/services/idempotency"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

// expiringStore narrows idempotencystore.PGStorage down to the one method the
// GC job actually needs, so the GC path can be tested/faked without a real
// Postgres-backed store.
type expiringStore interface {
	DeleteExpired(ctx context.Context) (int64, error)
}

var idempotencyGCStore expiringStore

var ErrInvalidIdempotencyKey = fmt.Errorf("invalid idempotency key")

const (
	idempotencyKeyHeader = "X-Idempotency-Key"
	// idempotencyScopedPrefix marks a key rewritten by scopeIdempotencyKey. The
	// fiber middleware only ever sees scoped keys: a client value is always
	// replaced or removed first, so a client cannot submit a pre-scoped key.
	idempotencyScopedPrefix = "scoped:"
)

func shouldSkipIdempotencyMiddleware(c fiber.Ctx) bool {
	return fiber.IsMethodSafe(c.Method())
}

// scopeIdempotencyKey (SEC-T-14) runs right before the fiber idempotency
// middleware and replaces the client X-Idempotency-Key with
// sha256(db, credential, method, path, key). The fiber middleware keys its
// cache on the header value alone, so without this anyone presenting a key
// seen before received the cached response of another caller.
//
// The caller is bound by the credential it presents (Authorization header, or
// X-API-Key) instead of the resolved uid: auth runs per route, after this
// middleware, and a replay is only ever served to whoever holds the same
// credential. A retry with a refreshed token therefore executes again.
//
// Not cached at all (header removed): /oauth/* and /auth/* (token responses
// must never be replayed) and requests with no credential (anonymous callers cannot be told
// apart).
func scopeIdempotencyKey(c fiber.Ctx) error {
	if shouldSkipIdempotencyMiddleware(c) {
		return c.Next()
	}
	raw := c.Get(idempotencyKeyHeader)
	if raw == "" {
		return c.Next()
	}
	credential := idempotencyCredential(c)
	if credential == "" || isIdempotencyExcludedPath(c.Path()) {
		c.Request().Header.Del(idempotencyKeyHeader)
		return c.Next()
	}
	if l, wl := len(raw), 36; l != wl {
		return fiber.NewError(fiber.StatusBadRequest, fmt.Sprintf("%v: invalid length: %d != %d", ErrInvalidIdempotencyKey, l, wl))
	}
	c.Request().Header.Set(idempotencyKeyHeader, scopedIdempotencyKey(idempotencyDBName(c), credential, c.Method(), c.Path(), raw))
	return c.Next()
}

// scopedIdempotencyKey hashes the parts with a NUL separator so no two
// different tuples can produce the same input.
func scopedIdempotencyKey(dbName, credential, method, path, key string) string {
	h := sha256.New()
	for _, part := range []string{dbName, credential, strings.ToUpper(method), path, key} {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return idempotencyScopedPrefix + hex.EncodeToString(h.Sum(nil))
}

// idempotencyCredential returns the credential the request authenticates
// with, the same inputs currentRPCIdentity reads. Only its hash is ever stored.
func idempotencyCredential(c fiber.Ctx) string {
	if auth := strings.TrimSpace(c.Get(fiber.HeaderAuthorization)); auth != "" {
		return "authorization:" + auth
	}
	if apiKey := strings.TrimSpace(c.Get("X-API-Key")); apiKey != "" {
		return "api-key:" + apiKey
	}
	return ""
}

// idempotencyDBName is the tenant bindRequestDatabase pinned, else home.
func idempotencyDBName(c fiber.Ctx) string {
	if dbName, ok := c.Locals(orm.ContextKeyDatabaseName).(string); ok && strings.TrimSpace(dbName) != "" {
		return strings.TrimSpace(dbName)
	}
	return config.HomeDBName()
}

// isIdempotencyExcludedPath covers the token endpoints under /oauth and their
// legacy /auth aliases (base-auth registers both). Case-insensitive because
// fiber routes /OAuth/token to the same handler.
func isIdempotencyExcludedPath(path string) bool {
	path = strings.ToLower(path)
	for _, prefix := range []string{"/oauth", "/auth"} {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// validateIdempotencyKey accepts only keys produced by scopeIdempotencyKey.
func validateIdempotencyKey(k string) error {
	if !strings.HasPrefix(k, idempotencyScopedPrefix) || len(k) != len(idempotencyScopedPrefix)+sha256.Size*2 {
		return fmt.Errorf("%w: key not scoped", ErrInvalidIdempotencyKey)
	}
	return nil
}

func resolveIdempotencyStorage(ctx context.Context) fiber.Storage {
	idempotencyGCStore = nil
	homeDB, err := orm.EnsureNamedBunDB(config.HomeDBName())
	if err != nil || homeDB == nil || homeDB.DB == nil {
		log.Warnf("idempotency: home DB unavailable, falling back to in-memory storage: %v", err)
		return nil
	}
	store, err := idempotencystore.NewPGStorage(ctx, homeDB.DB)
	if err != nil {
		log.Warnf("idempotency: postgres storage init failed, falling back to in-memory storage: %v", err)
		return nil
	}
	idempotencyGCStore = store
	if err := cronservice.RegisterHandler("platform.idempotency_gc", deleteExpiredIdempotencyEntries); err != nil {
		log.Warnf("idempotency: register gc handler: %v", err)
	}
	return store
}

func deleteExpiredIdempotencyEntries(ctx context.Context, _ map[string]any) error {
	if env := orm.EnvironmentFromContext(ctx); env != nil &&
		!strings.EqualFold(strings.TrimSpace(env.DBName()), strings.TrimSpace(config.HomeDBName())) {
		return nil
	}
	if idempotencyGCStore == nil {
		return nil
	}
	deleted, err := idempotencyGCStore.DeleteExpired(ctx)
	if err != nil {
		return err
	}
	if deleted > 0 {
		log.Infof("idempotency: gc removed %d expired entries", deleted)
	}
	return nil
}
