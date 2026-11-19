package routes

// File overview:
// - Responsibility: implements the MCP module's Fiber transport handlers and request-bound orchestration for `/mcp*` endpoints.
// - Key symbols: `RegisterMCPRoutes`, `RegisterRoutes`, `HandleJSONRPC`.
// - Pattern: Keeps MCP-specific HTTP transport ownership inside the module while translating MCP tool calls into the
//   same runtime model dispatch pipeline `/rpc` uses, so ACL, record-rule, and company-scope enforcement stay unified.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sync/singleflight"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/mcpkit"
	"git.thebase.vn/base_hub/internal/orm"
	rpcroutes "git.thebase.vn/base_hub/internal/routes"
	idempotencystore "git.thebase.vn/base_hub/internal/services/idempotency"
	"git.thebase.vn/base_hub/internal/services/log"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// bridgeScript is the official stdio<->HTTP bridge, embedded so GET
// /mcp/bridge always serves a ready-to-run download and users never need to
// write their own bridge program (see mcp-bridge-user-guide "Option A" /
// "Option B"). Only needed by MCP clients configured for a local stdio
// server instead of a remote URL; clients that support a remote URL need no
// bridge at all.
//
//go:embed static/base-hub-mcp-bridge.js
var bridgeScript []byte

const bridgeScriptFilename = "base-hub-mcp-bridge.js"

const (
	mcpRouteBasePath = "/mcp"
	jsonRPCVersion   = "2.0"

	// mcpProtocolVersion is the MCP spec revision this gateway implements for
	// the "initialize" handshake. Claude's Messages API connector and
	// OpenAI's Responses API `mcp` tool type both require a successful
	// initialize exchange before tools/list or tools/call — without it,
	// those clients never get past connection setup even though the rest of
	// the JSON-RPC surface is correct.
	mcpProtocolVersion = "2025-06-18"
	mcpServerName      = "base-hub-mcp"
	mcpServerVersion   = "0.3.0"
)

const (
	jsonRPCParseError     = -32700
	jsonRPCInvalidRequest = -32600
	jsonRPCMethodNotFound = -32601
	jsonRPCInvalidParams  = -32602
	jsonRPCApplicationErr = -32000
)

const (
	// mcpSessionIDHeader / mcpProtocolVersionHeader are the two Streamable
	// HTTP transport headers this gateway participates in. Both are optional
	// on the client side here (see mcpCreateSession/HandleJSONRPC): a caller
	// that never sends them keeps working exactly as before this was added —
	// only a caller that opts in by sending mcpSessionIDHeader gets it
	// validated. This keeps DeepSeek/Kimi-style agent-host adapters (whose
	// header propagation isn't confirmed spec-complete, per
	// references/mcp-server.md) from being broken by a hard requirement.
	mcpSessionIDHeader       = "Mcp-Session-Id"
	mcpProtocolVersionHeader = "MCP-Protocol-Version"
)

// mcpSessionTTL is how long a session id stays valid without another request
// referencing it (sliding expiry, extended on every mcpTouchSession call).
const mcpSessionTTL = 30 * time.Minute

// mcpSession binds a Streamable HTTP session id to the identity that
// initialized it (tenant db + user id, never the bearer token itself) so a
// guessed or leaked session id can't be replayed against a different
// identity's requests — mcpTouchSession treats a mismatch the same as an
// unknown id. No business/tool state is ever stored on a session: every tool
// call still resolves authorization fresh from the bearer identity on each
// request, so a session expiring or living only in this process's memory
// (same convention as mcpCancelRegistry above; no cross-replica sharing)
// never changes what a call is allowed to do, only whether the session id
// itself is still considered valid for the client to reuse.
type mcpSession struct {
	dbName    string
	userID    int64
	expiresAt time.Time
}

var (
	mcpSessionRegistryMu sync.Mutex
	mcpSessionRegistry   = map[string]*mcpSession{}
	mcpSessionLastSweep  time.Time
)

// mcpSessionSweepInterval throttles the expired-session sweep run on create.
const mcpSessionSweepInterval = time.Minute

// mcpSweepSessionsLocked drops expired sessions that were never touched again
// (mcpTouchSession only evicts the id it is asked about). Caller holds the mutex.
func mcpSweepSessionsLocked(now time.Time) {
	if now.Sub(mcpSessionLastSweep) < mcpSessionSweepInterval {
		return
	}
	mcpSessionLastSweep = now
	for id, session := range mcpSessionRegistry {
		if now.After(session.expiresAt) {
			delete(mcpSessionRegistry, id)
		}
	}
}

// mcpMaxSessionsPerIdentity bounds the registry per (db, user) (plan T30):
// a client that re-initializes without DELETE /mcp would otherwise add one
// entry per call for mcpSessionTTL.
const mcpMaxSessionsPerIdentity = 32

// mcpEvictOwnerSessionsLocked makes room for one more session of (dbName,
// userID) by dropping that owner's expired sessions, then the ones closest
// to expiry. Evicting instead of refusing keeps initialize working; a client
// on an evicted id gets 404 and re-initializes. Caller holds the mutex.
func mcpEvictOwnerSessionsLocked(dbName string, userID int64, now time.Time) {
	var owned []string
	for id, session := range mcpSessionRegistry {
		if session.dbName != dbName || session.userID != userID {
			continue
		}
		if now.After(session.expiresAt) {
			delete(mcpSessionRegistry, id)
			continue
		}
		owned = append(owned, id)
	}
	for len(owned) >= mcpMaxSessionsPerIdentity {
		oldest := 0
		for i := range owned {
			if mcpSessionRegistry[owned[i]].expiresAt.Before(mcpSessionRegistry[owned[oldest]].expiresAt) {
				oldest = i
			}
		}
		delete(mcpSessionRegistry, owned[oldest])
		owned = append(owned[:oldest], owned[oldest+1:]...)
	}
}

// mcpCreateSession issues a new Streamable HTTP session id on a successful
// "initialize" call and registers it against the caller's identity.
func mcpCreateSession(identity *orm.RBACIdentity) string {
	id := uuid.NewString()
	now := time.Now()
	mcpSessionRegistryMu.Lock()
	mcpSweepSessionsLocked(now)
	mcpEvictOwnerSessionsLocked(identity.DBName, identity.UserID, now)
	mcpSessionRegistry[id] = &mcpSession{
		dbName:    identity.DBName,
		userID:    identity.UserID,
		expiresAt: now.Add(mcpSessionTTL),
	}
	mcpSessionRegistryMu.Unlock()
	return id
}

// mcpTouchSession validates a client-supplied session id against identity
// and slides its expiry forward. Returns false when the id is unknown,
// expired, or was issued to a different identity — callers answer 404 either
// way per the Streamable HTTP spec (the client's remedy is the same either
// way: re-initialize), rather than distinguishing the reason in the response.
func mcpTouchSession(id string, identity *orm.RBACIdentity) bool {
	mcpSessionRegistryMu.Lock()
	defer mcpSessionRegistryMu.Unlock()
	session, ok := mcpSessionRegistry[id]
	if !ok {
		return false
	}
	if time.Now().After(session.expiresAt) {
		delete(mcpSessionRegistry, id)
		return false
	}
	if identity == nil || session.dbName != identity.DBName || session.userID != identity.UserID {
		return false
	}
	session.expiresAt = time.Now().Add(mcpSessionTTL)
	return true
}

// mcpDeleteSession backs DELETE /mcp explicit session termination. Returns
// false when the id was already unknown/expired so the handler can answer
// 404 instead of a misleading 204.
func mcpDeleteSession(id string) bool {
	mcpSessionRegistryMu.Lock()
	defer mcpSessionRegistryMu.Unlock()
	if _, ok := mcpSessionRegistry[id]; !ok {
		return false
	}
	delete(mcpSessionRegistry, id)
	return true
}

var (
	mcpRouteListTools = mcpListTools
)

// mcpCancelRegistry tracks the cancel func for every in-flight tools/call
// so a client-sent "notifications/cancelled" (MCP/JSON-RPC's standard abort
// signal) can actually stop server-side work instead of being accepted and
// silently ignored.
//
// Keys are owner-scoped (SEC-T-12, BUG-M-10): db, user id, login, MCP session
// id and the JSON-RPC id (fmt.Sprint, so numeric and string ids match). Ids
// like 1, 2, 3 are reused by every client, so an id-only key let any caller
// cancel another caller's call. Each entry carries a sequence number so a
// finishing call removes only its own entry, never a newer call that reused
// the same key.
type mcpCancelEntry struct {
	seq    uint64
	cancel context.CancelFunc
}

var (
	mcpCancelRegistryMu  sync.Mutex
	mcpCancelRegistry    = map[string]mcpCancelEntry{}
	mcpCancelRegistrySeq uint64
)

// mcpCancelToken identifies one registered call for mcpUnregisterCancelableCall.
type mcpCancelToken struct {
	key string
	seq uint64
}

// mcpCancelKey builds the owner-scoped registry key; "" when requestID is nil.
func mcpCancelKey(identity *orm.RBACIdentity, sessionID string, requestID any) string {
	if requestID == nil || identity == nil {
		return ""
	}
	return fmt.Sprintf("%s\x00%d\x00%s\x00%s\x00%v", identity.DBName, identity.UserID, identity.Login, sessionID, requestID)
}

// mcpRegisterCancelableCall records cancel for the caller's requestID (when
// the request carries an id) and returns the token handleToolsCall must pass
// to mcpUnregisterCancelableCall once the call finishes either way.
func mcpRegisterCancelableCall(identity *orm.RBACIdentity, sessionID string, requestID any, cancel context.CancelFunc) mcpCancelToken {
	key := mcpCancelKey(identity, sessionID, requestID)
	if key == "" {
		return mcpCancelToken{}
	}
	mcpCancelRegistryMu.Lock()
	mcpCancelRegistrySeq++
	seq := mcpCancelRegistrySeq
	mcpCancelRegistry[key] = mcpCancelEntry{seq: seq, cancel: cancel}
	mcpCancelRegistryMu.Unlock()
	return mcpCancelToken{key: key, seq: seq}
}

func mcpUnregisterCancelableCall(token mcpCancelToken) {
	if token.key == "" {
		return
	}
	mcpCancelRegistryMu.Lock()
	if entry, ok := mcpCancelRegistry[token.key]; ok && entry.seq == token.seq {
		delete(mcpCancelRegistry, token.key)
	}
	mcpCancelRegistryMu.Unlock()
}

// mcpCancelRequestedCall looks up and invokes the cancel func for a
// "notifications/cancelled" requestId payload sent by the same caller (same
// identity and session id). Returns false when no matching in-flight call is
// registered (already finished, unknown id, or another caller's call) — the
// caller still answers 202 Accepted either way per the transport spec.
func mcpCancelRequestedCall(identity *orm.RBACIdentity, sessionID string, requestID any) bool {
	key := mcpCancelKey(identity, sessionID, requestID)
	if key == "" {
		return false
	}
	mcpCancelRegistryMu.Lock()
	entry, ok := mcpCancelRegistry[key]
	mcpCancelRegistryMu.Unlock()
	if !ok {
		return false
	}
	entry.cancel()
	return true
}

type jsonRPCRequest struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id"`
	Method  string         `json:"method"`
	Params  map[string]any `json:"params"`
}

type jsonRPCResponse struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      any            `json:"id,omitempty"`
	Result  map[string]any `json:"result,omitempty"`
	Error   *jsonRPCError  `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// ToolDescriptor, mcpToolError, and their constructor now live in
// internal/mcpkit (see that package's mcpkit.go) so every module's mcp/
// package can build the same tool-catalog/error shapes without importing
// this package. These are kept as thin aliases/wrappers so every existing
// call site below (and in mcp_tools_website.go/mcp_tools_workflow.go/
// mcp_tools_knowledge.go) keeps compiling unchanged -- base.* tool behavior
// stays byte-for-byte identical across this move (verified by the existing
// mcp_route_test.go suite).
type ToolDescriptor = mcpkit.ToolDescriptor

// mcpToolError carries the HTTP status a tool failure should surface as, so
// HandleJSONRPC can map errors from deep inside the runtime dispatch back to
// the status codes documented in the SRS API contract (section 6.3).
type mcpToolError = mcpkit.ToolError

func newMCPToolError(status int, code, message string) *mcpToolError {
	return mcpkit.NewToolError(status, code, message)
}

// RegisterRoutes lets the module satisfy internal/modules.HTTPRouteRegistrar.
func RegisterRoutes(router fiber.Router) {
	RegisterMCPRoutes(router)
}

// RegisterMCPRoutes attaches the MCP gateway surface to the shared Fiber app.
func RegisterMCPRoutes(router fiber.Router) {
	if router == nil {
		return
	}

	group := router.Group(mcpRouteBasePath)
	group.Get("/health", HandleHealth)
	group.Get("/bridge", HandleBridgeDownload)
	group.Get("/tools", HandleTools)
	group.Post("/", HandleJSONRPC)
	group.Post("", HandleJSONRPC)
	// Streamable HTTP allows a GET on the MCP endpoint to open a
	// server-initiated SSE stream; this gateway has no server-initiated
	// messages, so it declines explicitly with 405 rather than a bare 404,
	// which is the behavior the transport spec calls for in that case.
	group.Get("/", HandleJSONRPCStreamUnsupported)
	group.Get("", HandleJSONRPCStreamUnsupported)
	// DELETE explicitly ends a session a client obtained from "initialize"
	// (see mcpSessionIDHeader) — optional per the transport spec, since a
	// session also expires on its own via mcpSessionTTL.
	group.Delete("/", HandleJSONRPCSessionDelete)
	group.Delete("", HandleJSONRPCSessionDelete)
}

// HandleJSONRPCStreamUnsupported answers GET /mcp. This gateway only
// supports the single-JSON-response mode of Streamable HTTP, not the
// server-initiated SSE stream some clients probe for.
func HandleJSONRPCStreamUnsupported(c fiber.Ctx) error {
	return c.Status(fiber.StatusMethodNotAllowed).JSON(fiber.Map{
		"error": "this MCP gateway does not support server-initiated SSE streaming",
	})
}

// HandleJSONRPCSessionDelete answers DELETE /mcp: explicit Streamable HTTP
// session termination. Requires the same bearer auth as every other MCP
// endpoint (SRS-F-001) — mcpTouchSession's identity match doubles as proof
// the caller owns the session before mcpDeleteSession removes it.
func HandleJSONRPCSessionDelete(c fiber.Ctx) error {
	identity, err := requireMCPIdentity(c)
	if err != nil {
		return err
	}
	sessionID := strings.TrimSpace(c.Get(mcpSessionIDHeader))
	if sessionID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": mcpSessionIDHeader + " header is required"})
	}
	if !mcpTouchSession(sessionID, identity) {
		return c.SendStatus(fiber.StatusNotFound)
	}
	mcpDeleteSession(sessionID)
	return c.SendStatus(fiber.StatusNoContent)
}

// HandleHealth returns the MCP gateway readiness state. Left unauthenticated
// (SRS assumption 1.5) so orchestration/readiness probes never need a credential.
func HandleHealth(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"name":      "mcp",
		"status":    "healthy",
		"path":      mcpRouteBasePath,
		"transport": "jsonrpc",
		"adapter":   "base-platform-runtime",
	})
}

// HandleBridgeDownload serves the official stdio<->HTTP bridge script.
// Left unauthenticated like /mcp/health: the file carries no secret (the
// caller's own OAuth access token — still passed via the BASE_HUB_MCP_KEY
// env var for compatibility with existing bridge configs — is supplied
// later via environment variables when they run it), and requiring a
// credential just to fetch installation tooling would defeat the point of a
// copy-paste-ready download.
func HandleBridgeDownload(c fiber.Ctx) error {
	c.Set(fiber.HeaderContentType, "application/javascript; charset=utf-8")
	c.Set(fiber.HeaderContentDisposition, fmt.Sprintf("attachment; filename=%q", bridgeScriptFilename))
	return c.Send(bridgeScript)
}

// HandleTools returns the current MCP tool catalog for discovery clients.
// SRS-F-001: every capability discovery/execution surface under /mcp requires
// MCP bearer authentication except /mcp/health.
func HandleTools(c fiber.Ctx) error {
	identity, err := requireMCPIdentity(c)
	if err != nil {
		return err
	}
	tools := mcpRouteListTools(c.Context(), identity)
	return c.JSON(fiber.Map{
		"tools": tools,
		"count": len(tools),
		"path":  mcpRouteBasePath + "/tools",
	})
}

// HandleJSONRPC accepts the MCP JSON-RPC transport entrypoint.
// Request body (jsonRPCRequest):
//
//	{
//	  "jsonrpc": "2.0",           // required, must equal jsonRPCVersion ("2.0")
//	  "id": <any>,                // request id, echoed back in the response (optional per JSON-RPC, but needed to correlate)
//	  "method": "initialize" | "tools/list" | "tools/call" | "notifications/initialized",
//	  "params": {                 // required for "tools/call"; ignored otherwise
//	    "name": "<tool name>",
//	    "arguments": { ... }       // tool-specific input, forwarded into the runtime model pipeline
//	  }
//	}
func HandleJSONRPC(c fiber.Ctx) error {
	// Streamable HTTP clients send "Accept: application/json, text/event-stream";
	// this gateway only ever answers application/json (see
	// HandleJSONRPCStreamUnsupported's doc comment on declining SSE), so
	// reject only the case where the client has explicitly said it cannot
	// accept that. A missing Accept header is treated as accepting anything,
	// for callers written before this check existed.
	if !mcpAcceptsJSON(c) {
		return c.Status(fiber.StatusNotAcceptable).JSON(fiber.Map{
			"error": "Accept header must include application/json",
		})
	}

	request, err := decodeJSONRPCRequest(c.Body())
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(nil, jsonRPCParseError, fmt.Errorf("invalid json-rpc payload: %v", err)))
	}
	if request.JSONRPC != jsonRPCVersion {
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCInvalidRequest, fmt.Errorf("jsonrpc must be %s", jsonRPCVersion)))
	}

	identity, err := requireMCPIdentity(c)
	if err != nil {
		return err
	}

	method := strings.TrimSpace(request.Method)

	// Session/protocol-version checks only apply once a client has actually
	// initialized — "initialize" itself is what creates the session id and
	// pins the protocol version, so it can't be validated against either yet.
	if method != "initialize" {
		if pv := strings.TrimSpace(c.Get(mcpProtocolVersionHeader)); pv != "" && pv != mcpProtocolVersion {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCInvalidRequest, fmt.Errorf(
				"unsupported %s %q; server supports %q", mcpProtocolVersionHeader, pv, mcpProtocolVersion,
			)))
		}
		// A session id is only enforced when the client actually sends one —
		// see mcpSessionIDHeader's doc comment for why this stays opt-in.
		if sessionID := strings.TrimSpace(c.Get(mcpSessionIDHeader)); sessionID != "" && !mcpTouchSession(sessionID, identity) {
			return c.Status(fiber.StatusNotFound).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCInvalidRequest, fmt.Errorf(
				"mcp session %q not found or expired; re-initialize to obtain a new one", sessionID,
			)))
		}
	}

	// A JSON-RPC notification (no "id" member, decoded here as a nil ID) must
	// never receive a JSON-RPC envelope back — the Streamable HTTP transport
	// spec requires a bare 202 Accepted instead. "notifications/initialized"
	// is the one every conforming client sends right after "initialize".
	// "notifications/cancelled" carries the target call's own id inside
	// params.requestId (not this notification's own id, which stays absent
	// per spec) — mcpCancelRequestedCall cancels that in-flight tools/call's
	// context so it can actually stop, instead of the previous no-op.
	if request.ID == nil && strings.HasPrefix(method, "notifications/") {
		if method == "notifications/cancelled" {
			mcpCancelRequestedCall(identity, strings.TrimSpace(c.Get(mcpSessionIDHeader)), request.Params["requestId"])
		}
		return c.SendStatus(fiber.StatusAccepted)
	}

	switch method {
	case "initialize":
		// Mcp-Session-Id is returned but never required back (see
		// mcpSessionIDHeader) — a client that adopts it gets the later
		// validation/DELETE-termination lifecycle; one that ignores it keeps
		// working exactly as before this existed.
		c.Set(mcpSessionIDHeader, mcpCreateSession(identity))
		return c.JSON(newJSONRPCSuccessResponse(request.ID, fiber.Map{
			"protocolVersion": mcpProtocolVersion,
			"capabilities": fiber.Map{
				"tools": fiber.Map{"listChanged": false},
			},
			"serverInfo": fiber.Map{
				"name":    mcpServerName,
				"version": mcpServerVersion,
			},
		}))
	case "tools/list":
		tools := mcpRouteListTools(c.Context(), identity)
		return c.JSON(newJSONRPCSuccessResponse(request.ID, fiber.Map{
			"tools": tools,
			"count": len(tools),
		}))
	case "tools/call":
		return handleToolsCall(c, identity, request)
	default:
		return c.JSON(newJSONRPCErrorResponse(request.ID, jsonRPCMethodNotFound, fmt.Errorf("unsupported mcp method %q", strings.TrimSpace(request.Method))))
	}
}

// handleToolsCall dispatches one MCP tools/call request into the generic
// runtime model pipeline and reports auditable success/failure telemetry
// (SRS-F-008) without ever logging the caller's raw bearer credential.
func handleToolsCall(c fiber.Ctx, identity *orm.RBACIdentity, request jsonRPCRequest) error {
	start := time.Now()
	toolName, _ := request.Params["name"].(string)
	toolName = strings.TrimSpace(toolName)
	arguments := mcpParseToolArguments(request.Params["arguments"])

	// Cancellation: a request ID lets a later "notifications/cancelled" abort
	// this in-flight call's context (see mcpCancelRegistry). Requests with no
	// ID (bare notifications never reach here) can't be cancelled individually.
	ctx, cancel := context.WithCancel(c.Context())
	defer cancel()
	cancelToken := mcpRegisterCancelableCall(identity, strings.TrimSpace(c.Get(mcpSessionIDHeader)), request.ID, cancel)
	defer mcpUnregisterCancelableCall(cancelToken)

	result, toolErr := mcpDispatchWithIdempotency(ctx, identity, toolName, arguments)
	logMCPToolCall(identity, toolName, arguments, time.Since(start), toolErr)
	if toolErr != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return c.Status(fiber.StatusRequestTimeout).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCApplicationErr, fmt.Errorf("tool call cancelled")))
		}
		var mcpErr *mcpToolError
		if asMCPToolError(toolErr, &mcpErr) {
			return c.Status(mcpErr.Status).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCApplicationErr, fmt.Errorf("%s: %s", mcpErr.Code, mcpErr.Message)))
		}
		return c.Status(fiber.StatusInternalServerError).JSON(newJSONRPCErrorResponse(request.ID, jsonRPCApplicationErr, toolErr))
	}
	return c.JSON(newJSONRPCSuccessResponse(request.ID, toolCallResult(result)))
}

// mcpParseToolArguments normalizes params.arguments into a map. The MCP spec
// requires arguments to be a JSON object, and that is what a spec-compliant
// client sends -- but some client-side adapters/tool-bindings (notably ones
// bridging from OpenAI-style function calling, where "arguments" is a
// JSON-encoded *string*, not an object) forward that string as-is instead of
// parsing it first. A bare `arguments.(map[string]any)` type assertion
// silently discards a string payload (ok=false -> nil -> empty map), which
// then surfaces downstream as a misleading "arguments.path is required" even
// though the caller did supply a path. Accept both shapes so a stringified
// object still reaches the tool handler.
func mcpParseToolArguments(raw any) map[string]any {
	switch v := raw.(type) {
	case map[string]any:
		return v
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return map[string]any{}
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil && parsed != nil {
			return parsed
		}
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

// toolCallResult shapes a successful tools/call payload to the MCP result
// contract: a "text" content block is mandatory because Claude's connector
// and any DeepSeek/Kimi tool-calling adapter only read `content`, never a
// custom "json" block (which is not a spec-defined content type and gets
// silently dropped); structuredContent carries the same data for clients
// that support it. Every result is run through trimLargeResponseFields first
// since toolCallResult itself is what
// duplicates a result's payload across both content[0].text and
// structuredContent — any tool returning a large text/json field hits the
// same "MCP bridge silently drops an oversized response" failure mode this
// guards against, not only the one call site that first surfaced it.
func toolCallResult(result any) fiber.Map {
	// Times become UTC Unix seconds so the text copy matches the JSON-RPC wire format.
	trimmed := base.TimesToUnix(trimLargeResponseFields(result))
	text, err := json.Marshal(trimmed)
	if err != nil {
		text = []byte("null")
	}
	return fiber.Map{
		"content":           []fiber.Map{{"type": "text", "text": string(text)}},
		"structuredContent": trimmed,
		"isError":           false,
	}
}

// trimLargeResponseFields now lives in internal/mcpkit (mcpkit.go) so every
// module's mcp/ package can apply the same "<field>_bytes" trimming to its
// own tool results, not just base-mcp's. Kept as a var (not a func decl) so
// the wide range of call sites below (toolCallResult, etc.) keep compiling
// unchanged.
var trimLargeResponseFields = mcpkit.TrimLargeResponseFields

func asMCPToolError(err error, target **mcpToolError) bool {
	typed, ok := err.(*mcpToolError)
	if ok {
		*target = typed
	}
	return ok
}

// dispatchMCPTool routes one generic MCP tool name to the underlying model
// metadata or runtime-method dispatch. Every branch reuses the exact runtime
// entrypoints /rpc uses (orm.LookupModel, orm.EnsureRuntimeModelAccess,
// modelruntime.Service) so MCP never reimplements model business logic
// (SRS-F-005 processing rule 2).
func dispatchMCPTool(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	if toolName == "" {
		return nil, newMCPToolError(fiber.StatusBadRequest, "invalid_params", "params.name is required")
	}
	// base.batch.execute is the one builtin that stays here: its per-step
	// recursive re-entry needs this function's own choke point (below), not
	// base's own dispatch -- see mcpBatchExecute's doc comment. Every other
	// tool name -- base.*, website.*, knowledge.*, workflow.*, and any future
	// module's mcp/ package -- now dispatches through the generic,
	// install-state-filtered provider cache (mcp_provider_cache.go) with no
	// special-cased branch remaining, per the mcp-module-scaffold plan's
	// Phase 5/7: modules/base-mcp/routes registers no tools of its own.
	if toolName == "base.batch.execute" {
		return mcpBatchExecute(ctx, identity, arguments)
	}
	provider, ok := mcpLookupToolProvider(ctx, identity, toolName)
	if !ok {
		return nil, mcpNotFoundWithHint(ctx, identity, "tool_not_found", fmt.Sprintf("unknown mcp tool %q", toolName), toolName)
	}
	return provider.Dispatch(ctx, identity, toolName, arguments)
}

// mcpBatchExecute backs base.batch.execute: runs an ordered list of steps
// (each the same {name, arguments} shape as a tools/call) inside one DB
// transaction via mcpRunInTenantTx, so a multi-step flow that this tool
// catalog otherwise forces across several separate tools/call round-trips
// (e.g. base.model.create_custom -> base.model.add_field -> base.model.publish,
// or several base.record.create calls that must all succeed together) is
// atomic: any step's failure rolls back every step already applied earlier in
// the same batch instead of leaving partial state the caller has to unwind
// by hand. Each step still goes through the exact same dispatchMCPTool/
// mcpExecuteRuntimeMethod choke point as a standalone call, so ACL/record-rule/
// company-scope enforcement is identical per step.
func mcpBatchExecute(ctx context.Context, identity *orm.RBACIdentity, arguments map[string]any) (any, error) {
	rawSteps, err := requireArrayArg(arguments, "steps")
	if err != nil {
		return nil, err
	}
	if len(rawSteps) == 0 {
		return nil, newMCPToolError(fiber.StatusUnprocessableEntity, "invalid_params", "arguments.steps must be a non-empty array")
	}

	type mcpBatchStep struct {
		name      string
		arguments map[string]any
	}
	steps := make([]mcpBatchStep, 0, len(rawSteps))
	for i, raw := range rawSteps {
		stepMap, ok := raw.(map[string]any)
		if !ok {
			return nil, newMCPToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf("arguments.steps[%d] must be an object", i))
		}
		name := strings.TrimSpace(stringFromAny(stepMap["name"]))
		if name == "" {
			return nil, newMCPToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf("arguments.steps[%d].name is required", i))
		}
		if name == "base.batch.execute" {
			return nil, newMCPToolError(fiber.StatusUnprocessableEntity, "invalid_params", "base.batch.execute cannot be nested inside itself")
		}
		stepArgs, _ := stepMap["arguments"].(map[string]any)
		if stepArgs == nil {
			stepArgs = map[string]any{}
		}
		steps = append(steps, mcpBatchStep{name: name, arguments: stepArgs})
	}

	results := make([]fiber.Map, 0, len(steps))
	txErr := mcpRunInTenantTx(ctx, identity, func(txCtx context.Context) error {
		for i, step := range steps {
			result, err := dispatchMCPTool(txCtx, identity, step.name, step.arguments)
			if err != nil {
				return fmt.Errorf("step %d (%s): %w", i, step.name, err)
			}
			results = append(results, fiber.Map{"name": step.name, "result": result})
		}
		return nil
	})
	if txErr != nil {
		var mcpErr *mcpToolError
		if errors.As(txErr, &mcpErr) {
			return nil, mcpErr
		}
		return nil, newMCPToolError(fiber.StatusBadRequest, "invalid_operation", txErr.Error())
	}
	return fiber.Map{"steps": results}, nil
}

// mcpIdempotentToolNames are the create-type tools that require
// arguments.idempotency_key: an MCP client retrying a timed-out tools/call
// (the call reached the server and mutated data, but the client never saw
// the response) previously had no way to avoid creating a duplicate record —
// there was no dedupe key anywhere in the create path. website.page.design_upsert
// only needs one when it is actually creating a page (arguments.page_id
// absent); when page_id is given it is an update, which is naturally
// idempotent already (same id, same values in, same record out).
//
// base.batch.execute is keyed as a whole: its steps call dispatchMCPTool
// directly (inside one tx) and never pass through this gate, so without a
// batch-level key a retried batch re-runs every create step. The cached value
// is the full {"steps":[...]} result; a failed batch rolled back and is not
// cached, so a retry with the same key re-executes it.
var mcpIdempotentToolNames = map[string]bool{
	"base.batch.execute":         true,
	"base.record.create":         true,
	"website.record.create":      true,
	"base.model.create_custom":   true,
	"website.page.design_upsert": true,
	"knowledge.doc.create":       true,
	"workflow.record.create":     true,
	"workflow.import":            true,
	"auth.record.create":         true,
	"automation.record.create":   true,
	"calendar.record.create":     true,
	"credential.record.create":   true,
	"website_blog.record.create": true,
}

// mcpToolRequiresIdempotencyKey reports whether this specific call needs
// arguments.idempotency_key, per mcpIdempotentToolNames. An intermediate
// chunk call to website.page.design_upsert (see mcp_tools_website.go's html
// chunk-upload support) never creates or mutates the page itself — it only
// buffers a piece of html server-side — so it is exempt even though the tool
// name is otherwise idempotency-required; the actual create/update only
// happens on the call that completes the upload, which is checked exactly
// like a non-chunked call once assembled.
func mcpToolRequiresIdempotencyKey(toolName string, arguments map[string]any) bool {
	if !mcpIdempotentToolNames[toolName] {
		return false
	}
	if toolName == "website.page.design_upsert" {
		if mcpIsIntermediateHTMLChunkCall(arguments) {
			return false
		}
		_, hasPageID := arguments["page_id"]
		return !hasPageID
	}
	return true
}

// mcpIsIntermediateHTMLChunkCall is a transport-level, minimal re-check of
// whether arguments describe a not-yet-final chunk of
// website.page.design_upsert's chunked html upload (see
// modules/website/mcp/dispatch.go's identically-named helper, which owns the
// real parsing/validation). Duplicated here (rather than imported) because
// this package must never import a module's package directly (see
// code-pattern.md's "Decoupled Plugin Module Pattern") -- this is the one
// place base-mcp's own transport-level idempotency-key gate
// (mcpToolRequiresIdempotencyKey) needs to know about one specific tool's
// argument shape without owning that tool. A malformed chunk call is not
// treated as intermediate here either -- it falls through to
// mcpToolRequiresIdempotencyKey's normal "page_id absent" idempotency
// requirement, and the real validation error still surfaces once dispatch
// reaches modules/website/mcp.
func mcpIsIntermediateHTMLChunkCall(arguments map[string]any) bool {
	if _, hasUploadID := arguments["upload_id"]; !hasUploadID {
		return false
	}
	total, totalOK := int64FromArgument(arguments["chunk_total"])
	index, indexOK := int64FromArgument(arguments["chunk_index"])
	if !totalOK || !indexOK || total <= 0 || index < 0 || index >= total {
		return false
	}
	return index < total-1
}

// mcpIdempotencyTTL is how long a create call's result stays cached for
// replay under the same idempotency_key. Long enough to cover realistic
// client retry windows (a slow tool call plus a few backoff retries), short
// enough that the home-DB table doesn't grow unbounded between GC sweeps.
const mcpIdempotencyTTL = 24 * time.Hour

// idempotencyCache narrows idempotencystore.PGStorage down to the methods the
// MCP tool-dedupe and chunked-upload-session paths actually use, so these
// call sites can be tested against a fake cache instead of a real
// Postgres-backed store.
type idempotencyCache interface {
	Get(key string) ([]byte, error)
	Set(key string, val []byte, exp time.Duration) error
	Delete(key string) error
}

var (
	mcpIdempotencyStoreMu sync.Mutex
	mcpIdempotencyStore   idempotencyCache
)

// mcpGetIdempotencyStore lazily binds a PGStorage instance to the home
// database (config.HomeDBName()), reusing the exact same PostgreSQL-backed
// fiber.Storage adapter internal/cmd/server.go already uses for HTTP-level
// idempotency, rather than inventing a second storage mechanism. The home DB
// is shared across every tenant, so cache keys are namespaced by tenant DB
// name + user id + tool name (mcpIdempotencyKey) to keep tenants isolated.
//
// Only success is cached so a transient failure is retried on the next call.
func mcpGetIdempotencyStore() (idempotencyCache, error) {
	mcpIdempotencyStoreMu.Lock()
	defer mcpIdempotencyStoreMu.Unlock()
	if mcpIdempotencyStore != nil {
		return mcpIdempotencyStore, nil
	}
	homeDB, err := orm.EnsureNamedBunDB(config.HomeDBName())
	if err != nil || homeDB == nil || homeDB.DB == nil {
		return nil, fmt.Errorf("mcp idempotency: resolve home db: %w", err)
	}
	store, err := idempotencystore.NewPGStorage(context.Background(), homeDB.DB)
	if err != nil {
		return nil, err
	}
	mcpIdempotencyStore = store
	return mcpIdempotencyStore, nil
}

// mcpIdempotencyKey namespaces a caller-supplied idempotency_key by tenant DB
// + user + tool, so the same literal key from two different tenants/users/
// tools can never collide.
func mcpIdempotencyKey(identity *orm.RBACIdentity, toolName, key string) string {
	dbName, userID := "", int64(0)
	if identity != nil {
		dbName, userID = identity.DBName, identity.UserID
	}
	return fmt.Sprintf("mcp:idem:%s:%d:%s:%s", dbName, userID, toolName, key)
}

// mcpDispatchWithIdempotency wraps dispatchMCPTool for create-type tools
// (mcpIdempotentToolNames): requires arguments.idempotency_key, replays a
// cached result on a repeated key instead of re-executing (so a client retry
// after a timeout can never double-create), and caches a successful result
// for mcpIdempotencyTTL. Non-idempotent tools and lookup/store failures
// (store unavailable, e.g. home DB down) fall back to a plain dispatchMCPTool
// call — availability of the create tool itself is not made to depend on the
// dedupe store staying up, which would turn an ops issue on a side-channel
// into an outage of the primary write path.
func mcpDispatchWithIdempotency(ctx context.Context, identity *orm.RBACIdentity, toolName string, arguments map[string]any) (any, error) {
	if !mcpToolRequiresIdempotencyKey(toolName, arguments) {
		return mcpIdempotentDispatch(ctx, identity, toolName, arguments)
	}
	key := strings.TrimSpace(stringFromAny(arguments["idempotency_key"]))
	if key == "" {
		return nil, newMCPToolError(fiber.StatusUnprocessableEntity, "invalid_params", fmt.Sprintf(
			"arguments.idempotency_key is required for %q so a client retry after a timeout cannot create a duplicate record", toolName,
		))
	}
	store, storeErr := mcpGetIdempotencyStore()
	if storeErr != nil {
		log.Errorf("mcp idempotency: store unavailable, proceeding without dedupe: %v", storeErr)
		return mcpIdempotentDispatch(ctx, identity, toolName, arguments)
	}
	cacheKey := mcpIdempotencyKey(identity, toolName, key)
	// Collapse overlapping identical calls (same tenant/user/tool/key) into one
	// execution: without this, two requests that both miss the cache before
	// either stores its result would each create a record. Single-node only;
	// across nodes the store check below still covers sequential retries.
	//
	// The shared execution is detached from the first caller's cancellation
	// (WithoutCancel keeps the tenant env/DB values) and bounded by
	// mcpIdempotentCallTimeout: otherwise one caller's notifications/cancelled
	// or disconnect would fail every waiter collapsed onto the same key. Each
	// waiter still returns on its own ctx.Done(); the work then finishes and
	// caches its result, so that caller's retry replays it.
	resultCh := mcpIdempotencyFlight.DoChan(cacheKey, func() (any, error) {
		// Runs only for the leading caller, so ctx here is the leader's.
		sharedCtx, cancelShared := context.WithTimeout(context.WithoutCancel(ctx), mcpIdempotentCallTimeout)
		defer cancelShared()
		if cached, err := store.Get(cacheKey); err == nil && len(cached) > 0 {
			var replay any
			if json.Unmarshal(cached, &replay) == nil {
				return replay, nil
			}
		}
		result, err := mcpIdempotentDispatch(sharedCtx, identity, toolName, arguments)
		if err != nil {
			return nil, err
		}
		if encoded, marshalErr := json.Marshal(result); marshalErr == nil {
			if setErr := store.Set(cacheKey, encoded, mcpIdempotencyTTL); setErr != nil {
				log.Errorf("mcp idempotency: cache result for %q: %v", toolName, setErr)
			}
		}
		return result, nil
	})
	select {
	case res := <-resultCh:
		return res.Val, res.Err
	case <-ctx.Done():
		// Only this waiter stops waiting; the shared execution carries on.
		return nil, ctx.Err()
	}
}

// mcpIdempotentCallTimeout bounds one deduplicated create-tool execution that
// runs detached from its callers' cancellation; it matches the server's
// default request read timeout (internal/cmd/server.go defaultServerReadTimeout).
const mcpIdempotentCallTimeout = 120 * time.Second

// mcpIdempotentDispatch is the dispatcher the idempotency wrapper calls; a var so tests can count executions.
var mcpIdempotentDispatch = dispatchMCPTool

// mcpIdempotencyFlight dedupes in-flight create-tool calls per idempotency cache key.
var mcpIdempotencyFlight singleflight.Group

// requireArrayArg and mcpRunInTenantTx now live in internal/mcpkit (see
// mcpkit.go/dispatch.go) so every module's mcp/ package shares the exact same
// argument-validation and dispatch plumbing. Kept as wrapper vars so the
// existing call sites keep compiling unchanged.
var (
	requireArrayArg  = mcpkit.RequireArrayArg
	mcpRunInTenantTx = mcpkit.RunInTenantTx
)

// requireMCPIdentity resolves and authenticates the /mcp bearer credential.
// SRS-F-001: X-API-Key must never authenticate /mcp. OAuth 2.0 is now the
// sole /mcp credential (RBAC/OAuth backend plan §1/§7.4) — the dedicated
// base.users.mcp_api_key column/lookup was removed once this path was proven
// in Phase 1; rotate an /mcp client's access by minting it its own
// sys.oauth_client (service caller) or user login instead.
func requireMCPIdentity(c fiber.Ctx) (*orm.RBACIdentity, error) {
	dbName, err := rpcroutes.ResolveRPCDBName(rpcroutes.RequestDBNameRaw(c))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	// Deliberately no eager DB-connectivity check here: JWT bearer validation
	// is self-contained (signature/exp/iss against this process's own key, no
	// network call — plan §3), so an unreachable target DB must not block
	// authentication itself. A genuinely unreachable DB still surfaces its own
	// error once the authenticated request actually dispatches against it.

	authHeader := strings.TrimSpace(c.Get(fiber.HeaderAuthorization))
	if authHeader == "" {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "authorization is required")
	}
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "bearer") {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "authorization header must use the Bearer scheme")
	}

	identity, err := baseauth.CurrentValidator().Validate(c.Context(), parts[1])
	if err != nil || identity == nil {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "invalid bearer token")
	}
	if err := rpcroutes.EnsureIdentityDBMatch(identity.DBName, dbName); err != nil {
		return nil, fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	identity.DBName = dbName
	return identity, nil
}

// logMCPToolCall records who executed which model operation through MCP
// (SRS-F-008). Only tool/model/method names and outcome are logged; argument
// payloads and the bearer credential are never written to logs.
func logMCPToolCall(identity *orm.RBACIdentity, toolName string, arguments map[string]any, elapsed time.Duration, toolErr error) {
	model := strings.TrimSpace(stringFromAny(arguments["model"]))
	method := strings.TrimSpace(stringFromAny(arguments["method"]))
	userID := int64(0)
	dbName := ""
	if identity != nil {
		userID = identity.UserID
		dbName = identity.DBName
	}
	if toolErr != nil {
		log.Warnf("mcp_tool_call user_id=%d db=%s tool=%s model=%s method=%s status=failure latency_ms=%d error=%v",
			userID, dbName, toolName, model, method, elapsed.Milliseconds(), toolErr)
		return
	}
	log.Infof("mcp_tool_call user_id=%d db=%s tool=%s model=%s method=%s status=success latency_ms=%d",
		userID, dbName, toolName, model, method, elapsed.Milliseconds())
}

// mcpAcceptsJSON reports whether the request's Accept header, if present,
// permits an application/json response.
func mcpAcceptsJSON(c fiber.Ctx) bool {
	accept := strings.TrimSpace(c.Get(fiber.HeaderAccept))
	if accept == "" {
		return true
	}
	return strings.Contains(accept, "application/json") || strings.Contains(accept, "*/*")
}

func decodeJSONRPCRequest(body []byte) (jsonRPCRequest, error) {
	var request jsonRPCRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return jsonRPCRequest{}, err
	}
	if request.Params == nil {
		request.Params = map[string]any{}
	}
	if strings.TrimSpace(request.Method) == "" {
		return jsonRPCRequest{}, fmt.Errorf("method is required")
	}
	return request, nil
}

func newJSONRPCSuccessResponse(id any, result map[string]any) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Result:  result,
	}
}

func newJSONRPCErrorResponse(id any, code int, err error) jsonRPCResponse {
	message := "unknown error"
	if err != nil {
		message = err.Error()
	}
	return jsonRPCResponse{
		JSONRPC: jsonRPCVersion,
		ID:      id,
		Error: &jsonRPCError{
			Code:    code,
			Message: message,
		},
	}
}

// stringFromAny now lives in internal/mcpkit (mcpkit.go); idsFromAny moved
// there too as an unexported helper behind mcpkit.RequireIDsArg (its only
// caller repo-wide). Kept as a wrapper var so every existing call site keeps
// compiling unchanged.
var stringFromAny = mcpkit.StringFromAny

// int64FromArgument now lives in internal/mcpkit (mcpkit.go) too, so
// website/base-workflow/base's own mcp/ packages can reuse the same scalar
// argument conversion. Kept as a wrapper var here for
// mcpIsIntermediateHTMLChunkCall's own transport-level chunk re-check above.
var int64FromArgument = mcpkit.Int64FromArgument
