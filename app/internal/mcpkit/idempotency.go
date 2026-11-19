package mcpkit

// File overview:
// - Responsibility: the shared home-DB-backed key/value store every MCP tool
//   group uses for idempotency-key replay (modules/base-mcp/routes's
//   mcpDispatchWithIdempotency) and for buffering multi-chunk uploads (e.g.
//   website's website.page.design_upsert chunked html upload).
// - Key symbols: IdempotencyCache, GetIdempotencyStore, IdempotencyKey.
// - Moved here (originally modules/base-mcp/routes/mcp_route.go) so a
//   module's own mcp/dispatch.go (website's chunk-upload buffering, in
//   particular) can share the exact same store instance/connection instead
//   of duplicating the home-DB PGStorage wiring.

import (
	"context"
	"fmt"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	idempotencystore "git.thebase.vn/base_hub/internal/services/idempotency"
)

// IdempotencyCache narrows idempotencystore.PGStorage down to the methods the
// MCP tool-dedupe and chunked-upload-session paths actually use, so these
// call sites can be tested against a fake cache instead of a real
// Postgres-backed store.
type IdempotencyCache interface {
	Get(key string) ([]byte, error)
	Set(key string, val []byte, exp time.Duration) error
	Delete(key string) error
}

var (
	idempotencyStoreMu sync.Mutex
	idempotencyStore   IdempotencyCache
)

// GetIdempotencyStore lazily binds a PGStorage instance to the home database
// (config.HomeDBName()), reusing the exact same PostgreSQL-backed
// fiber.Storage adapter internal/cmd/server.go already uses for HTTP-level
// idempotency, rather than inventing a second storage mechanism per module.
//
// Only success is cached: a transient failure (home DB not ready yet) is
// retried on the next call instead of poisoning the process until restart.
func GetIdempotencyStore() (IdempotencyCache, error) {
	idempotencyStoreMu.Lock()
	defer idempotencyStoreMu.Unlock()
	if idempotencyStore != nil {
		return idempotencyStore, nil
	}
	homeDB, err := orm.EnsureNamedBunDB(config.HomeDBName())
	if err != nil || homeDB == nil || homeDB.DB == nil {
		return nil, fmt.Errorf("mcp idempotency: resolve home db: %w", err)
	}
	store, err := idempotencystore.NewPGStorage(context.Background(), homeDB.DB)
	if err != nil {
		return nil, err
	}
	idempotencyStore = store
	return idempotencyStore, nil
}

// IdempotencyKey namespaces a caller-supplied idempotency_key by tenant DB +
// user + tool, so the same literal key from two different tenants/users/
// tools can never collide.
func IdempotencyKey(identity *orm.RBACIdentity, toolName, key string) string {
	dbName, userID := "", int64(0)
	if identity != nil {
		dbName, userID = identity.DBName, identity.UserID
	}
	return fmt.Sprintf("mcp:idem:%s:%d:%s:%s", dbName, userID, toolName, key)
}
