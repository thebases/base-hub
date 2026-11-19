package service

// File overview:
// - Responsibility: binds a request context to one named tenant database, mirroring
//   internal/routes/rpc_route.go's rpcContextForDB / modules/base-mcp/routes/mcp_route.go's
//   mcpContextForDB. Each module keeps its own copy of this small helper rather than importing
//   internal/routes, which would create an import cycle (internal/routes calls into this package
//   to validate OAuth bearer tokens).
// - Key symbols: `dbBoundContext`.

import (
	"context"
	"fmt"

	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

// dbBoundContext resolves dbName to its live *bun.DB handle and returns a
// context carrying an ORM environment scoped to it, so orm.AuthenticateRPCBasic
// and friends resolve the same database this request is issuing a token for.
func dbBoundContext(ctx context.Context, dbName string) (context.Context, *bun.DB, error) {
	if dbName == "" {
		return nil, nil, fmt.Errorf("auth: database name is required")
	}
	// dbName is unauthenticated request input (OAuth "db" field or
	// X-Database-Name header, resolved pre-login) — NoCreate so it can't
	// spray-create a database.
	registered, err := orm.EnsureNamedBunDBNoCreate(dbName)
	if err != nil {
		return nil, nil, fmt.Errorf("auth: resolve database %q: %w", dbName, err)
	}
	env := orm.NewEnvironmentWithDBName(registered.DB, dbName, 0, map[string]any{
		orm.ContextKeyDatabaseName: dbName,
	}, false)
	return orm.ContextWithEnvironment(ctx, env), registered.DB, nil
}
