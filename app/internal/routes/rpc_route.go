package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the rpc route surface.
// - Key symbols: `RegisterRPCRoutes`, `handleRPCRoot`, `RPCRootResultDoc`, `RPCRootResponseDoc`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.
// - File split: route registration and doc/shared types live here; see rpc_route_handlers.go
//   (JSON-RPC HTTP handlers), rpc_route_identity.go (auth identity + DB name resolution),
//   rpc_route_jsonrpc.go (JSON-RPC envelope helpers), rpc_route_params.go (path/args parsing helpers).

import (
	"context"

	"github.com/gofiber/fiber/v3"
)

const (
	rpcHeaderDatabaseName = "X-Database-Name"
)

const RPCHeaderDatabaseName = rpcHeaderDatabaseName

// RPCRootResultDoc documents GET /rpc.
type RPCRootResultDoc struct {
	Path      string   `json:"path" example:"/rpc"`
	Methods   []string `json:"methods" example:"GET"`
	ModelPath string   `json:"model_path" example:"/rpc/{model}/{method}"`
	IDPath    string   `json:"id_path" example:"/rpc/{model}/{method}/{id}"`
	DBPath    string   `json:"db_path" example:"/rpc/db/{method}"`
	MetaPath  string   `json:"meta_path" example:"/rpc_meta/model/{method}"`
}

// RPCRootResponseDoc documents the root RPC metadata response.
type RPCRootResponseDoc struct {
	Result RPCRootResultDoc `json:"result"`
}

// JSONRPCDatabaseRequestDoc documents POST /rpc/db/{method}.
type JSONRPCDatabaseRequestDoc struct {
	ID     any            `json:"id" example:"100"`
	Params map[string]any `json:"params"`
}

// JSONRPCErrorDoc documents one JSON-RPC style error.
type JSONRPCErrorDoc struct {
	Code    int    `json:"code" example:"-32000"`
	Message string `json:"message" example:"unsupported database method"`
}

// JSONRPCGenericResponseDoc documents generic JSON-RPC success or failure envelopes.
type JSONRPCGenericResponseDoc struct {
	ID     any              `json:"id,omitempty" example:"1"`
	Result any              `json:"result,omitempty"`
	Error  *JSONRPCErrorDoc `json:"error,omitempty"`
}

// CallKWRequestDoc documents POST /rpc/{model}/{method}.
type CallKWRequestDoc struct {
	// ID is an optional JSON scalar echoed back in the response body.
	ID     any            `json:"id,omitempty"`
	Args   []any          `json:"args"`
	Kwargs map[string]any `json:"kwargs"`
}

type jsonRPCRequest struct {
	ID     any            `json:"id"`
	Params map[string]any `json:"params"`
}

type jsonRPCResponse struct {
	ID     any              `json:"id,omitempty"`
	Result any              `json:"result,omitempty"`
	Error  *JSONRPCErrorDoc `json:"error,omitempty"`
}

// dbInitFunc is registered at server startup via SetDBInitFunc and is invoked
// by the /rpc/db/init endpoint to initialize a named database on demand.
var dbInitFunc func(ctx context.Context, logicalName string) error

// SetDBInitFunc registers the callback used by POST /rpc/db/init.
func SetDBInitFunc(f func(ctx context.Context, logicalName string) error) {
	dbInitFunc = f
}

type rpcRequestIdentity struct {
	UserID     int64
	Username   string
	RoleID     int64
	RoleName   string
	GroupIDs   []int64
	DBName     string
	CompanyID  int64
	CompanyIDs []int64
	AuthMode   string
	Sudo       bool
	// Lang is the user's preferred locale (base_users.lang), carried through so
	// GET /oauth/me (modules/base-auth/routes) can hydrate the frontend's
	// user_context after an /oauth/token login without a second RPC round trip.
	Lang string
}

type RPCRequestIdentity = rpcRequestIdentity

// RegisterRPCRoutes mounts the RPC compatibility surface on top of
// the metadata-driven runtime service already used by the repo.
func RegisterRPCRoutes(router fiber.Router) {
	router.Get("/rpc", handleRPCRoot)
	router.Post("/rpc/db/:method", handleDatabaseJSONRPC)
	router.Post("/rpc_meta/model/:method", handleModelMetaJSONRPC)
	router.Post("/rpc/:model/:method/:id", handleCallKWWithID)
	router.Post("/rpc/:model/:method", handleCallKW)
	// Mount any RuntimeMethod.AliasRoute declared by registered models as a
	// friendly "/base/<route>" shortcut over the same dispatch path.
	registerRuntimeMethodAliasRoutes(router)
}

func handleRPCRoot(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"path":       "/rpc",
			"methods":    []string{fiber.MethodGet},
			"model_path": "/rpc/{model}/{method}",
			"id_path":    "/rpc/{model}/{method}/{id}",
			"db_path":    "/rpc/db/{method}",
			"meta_path":  "/rpc_meta/model/{method}",
		},
	})
}
