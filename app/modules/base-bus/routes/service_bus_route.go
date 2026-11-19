package routes

// File overview:
// - Responsibility: implements Fiber transport handlers and request-bound orchestration for the base-bus module's service-bus route surface.
// - Key symbols: `RegisterRoutes`, `handleServiceBusTransportConfig`, `serviceBusTransportConfigResponse`, `serviceBusSubscribeResponse`.
// - Pattern: Keeps transport concerns at the HTTP boundary: requests are parsed and validated here before business work is delegated deeper into services or the ORM runtime.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	internalroutes "git.thebase.vn/base_hub/internal/routes"
	"git.thebase.vn/base_hub/internal/services/log"
	baseauth "git.thebase.vn/base_hub/modules/base-auth/service"
	busmodels "git.thebase.vn/base_hub/modules/base-bus/models"
	busqueries "git.thebase.vn/base_hub/modules/base-bus/queries"
	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
)

// serviceBusStreamHeartbeatInterval is how often an idle stream re-polls and
// sends a ping — this is what makes an idle connection still notice a dead client
// (via the next failed WriteMessage) and keeps proxies that time out silent
// connections from killing it.
const serviceBusStreamHeartbeatInterval = 20 * time.Second

// serviceBusStreamRecheckInterval is how soon a stream re-polls after a
// page was held back behind an id that may still commit.
const serviceBusStreamRecheckInterval = 2 * time.Second

// shuttingDown is closed exactly once by Shutdown so every live stream
// goroutine and the per-DB listeners (busHub) notice and unwind: fasthttp's
// graceful shutdown does not track hijacked WebSocket connections, so
// without it streams would outlive app.ShutdownWithContext and keep polling
// a pool that is about to close.
var (
	shuttingDown     = make(chan struct{})
	shuttingDownOnce sync.Once
	// shutdownCtx is cancelled with shuttingDown so waits can derive from it
	// instead of spawning a watcher goroutine per wake-up.
	shutdownCtx, shutdownCancel = context.WithCancel(context.Background())
)

// Shutdown signals every active /bus/ws and /bus/sse stream and every
// per-DB listener to stop. Call this before closing the DB pool during
// server shutdown.
func Shutdown() {
	shuttingDownOnce.Do(func() {
		close(shuttingDown)
		shutdownCancel()
	})
}

type serviceBusChannelPayload struct {
	Params struct {
		Channels any   `json:"channels"`
		Last     int64 `json:"last"`
	} `json:"params"`
	Channels any   `json:"channels"`
	Last     int64 `json:"last"`
}

type serviceBusLastNotificationPayload struct {
	Params struct {
		LastNotificationID int64 `json:"last_notification_id"`
	} `json:"params"`
	LastNotificationID int64 `json:"last_notification_id"`
}

// RegisterRoutes lets the base-bus module satisfy internal/modules.HTTPRouteRegistrar.
func RegisterRoutes(router fiber.Router) {
	RegisterServiceBusRoutes(router)
}

// RegisterServiceBusRoutes mounts the frontend-facing service-bus transport
// routes used by the bus client (WebSocket push, MQTT handoff, and HTTP
// long-poll fallback).
func RegisterServiceBusRoutes(router fiber.Router) {
	router.Get("/bus/transport_config", handleServiceBusTransportConfig)
	router.Post("/bus/subscribe", handleServiceBusSubscribe)
	router.Post("/bus/has_missed_notifications", handleServiceBusHasMissedNotifications)
	router.Get("/websocket/health", handleServiceBusHealth)
	router.Post("/websocket/peek_notifications", handleServiceBusPeekNotifications)
	// Upgrade check runs as regular middleware (auth + channel/cursor
	// resolution happen here, stashed into Locals), then websocket.New's
	// handler takes over the hijacked connection.
	router.Use("/bus/ws", serviceBusWebSocketUpgrade)
	router.Get("/bus/ws", websocket.New(handleServiceBusWebSocket))
	// SSE is the same push stream over a plain streaming HTTP response. It
	// needs no Upgrade handshake, so it survives proxies that strip Upgrade
	// (the browser falls back to it after repeated /bus/ws failures).
	router.Get("/bus/sse", handleServiceBusSSE)
}

// It always reports the WebSocket transport (`/bus/ws`, SSE fallback), which
// is safe across multiple backend instances since NOTIFY fans out through
// Postgres itself. Browser MQTT (`mqtt.public_ws_url`) is disabled: the only
// broker account is the server's own, and handing it to every signed-in user
// let any of them publish or subscribe as the server. It returns once the
// broker issues short-lived per-user credentials with topic ACLs (plan T10).
func handleServiceBusTransportConfig(c fiber.Ctx) error {
	if _, err := internalroutes.RequireRPCIdentity(c, false); err != nil {
		return err
	}
	return c.JSON(fiber.Map{"result": serviceBusTransportConfig()})
}

// serviceBusTransportConfig is the browser transport advertisement. It must
// never carry broker credentials. `sse` advertises the Upgrade-free fallback;
// older clients ignore it.
func serviceBusTransportConfig() fiber.Map {
	return fiber.Map{
		"transport": "websocket",
		"ws":        fiber.Map{"url": "/bus/ws"},
		"sse":       fiber.Map{"url": "/bus/sse"},
	}
}

func handleServiceBusSubscribe(c fiber.Ctx) error {
	identity, err := internalroutes.RequireRPCIdentity(c, false)
	if err != nil {
		return err
	}
	channels, last, err := parseServiceBusChannelPayload(c)
	if err != nil {
		return err
	}
	normalized := authorizedServiceBusChannels(c.Context(), identity, channels)
	currentLastID, err := serviceBusLastID(c.Context(), identity)
	if err != nil {
		// Untyped DB failures render as a masked internal error (APIErrorHandler);
		// the raw driver text must not reach the client.
		return fmt.Errorf("bus subscribe: %w", err)
	}
	if last > currentLastID {
		last = currentLastID
	}
	return c.JSON(serviceBusSubscribeEnvelope(normalized, last))
}

func handleServiceBusHasMissedNotifications(c fiber.Ctx) error {
	if _, err := internalroutes.RequireRPCIdentity(c, false); err != nil {
		return err
	}
	lastID, err := parseServiceBusLastNotificationID(c)
	if err != nil {
		return err
	}
	if lastID <= 0 {
		return c.JSON(fiber.Map{"result": false})
	}

	count, err := orm.BunDBFromContext(c.Context()).NewSelect().
		TableExpr("base_service_bus").
		Where("id = ?", lastID).
		Count(c.Context())
	if err != nil {
		return fmt.Errorf("bus has_missed_notifications: %w", err)
	}
	return c.JSON(fiber.Map{"result": count == 0})
}

func handleServiceBusHealth(c fiber.Ctx) error {
	// Reuse the health route expected by the existing frontend worker shape.
	return c.JSON(fiber.Map{"status": "pass"})
}

func handleServiceBusPeekNotifications(c fiber.Ctx) error {
	identity, err := internalroutes.RequireRPCIdentity(c, false)
	if err != nil {
		return err
	}
	channels, last, err := parseServiceBusChannelPayload(c)
	if err != nil {
		return err
	}
	normalized := authorizedServiceBusChannels(c.Context(), identity, channels)
	requestCtx := serviceBusRuntimeContext(c.Context(), identity)
	result, err := modelruntime.NewService().Execute(requestCtx, modelruntime.Request{
		Model:  busmodels.ServiceBusModelName,
		Method: "poll",
		Args:   []any{normalized, last},
	})
	if err != nil {
		// Typed runtime errors keep their status; anything else is masked as internal.
		return err
	}
	rows, ok := result.([]map[string]any)
	if !ok {
		return fmt.Errorf("unexpected service bus poll result %T", result)
	}
	return c.JSON(fiber.Map{
		"result": fiber.Map{
			"channels":      normalized,
			"channel_keys":  normalized,
			"notifications": rows,
		},
	})
}

// serviceBusWebSocketUpgrade resolves auth, channels, and the starting
// cursor before the connection is hijacked into a WebSocket, then stashes
// them on Locals for handleServiceBusWebSocket to read. A native browser
// WebSocket cannot set an Authorization header on its handshake request, so
// auth here is a `token` query parameter instead of the Bearer header every
// other /rpc-style route uses — deliberately scoped to just this route
// rather than widening the shared RequireRPCIdentity/Bearer path, since a
// token-in-URL fallback is a real (if standard-practice) exposure trade-off
// that shouldn't silently apply to the whole RPC surface.
func serviceBusWebSocketUpgrade(c fiber.Ctx) error {
	identity, normalized, lastID, err := serviceBusStreamParams(c)
	if err != nil {
		return err
	}

	c.Locals("sb_identity", identity)
	c.Locals("sb_channels", normalized)
	c.Locals("sb_last", lastID)

	if websocket.IsWebSocketUpgrade(c) {
		return c.Next()
	}
	// An authenticated client that reaches here almost always sent a real
	// upgrade that a reverse proxy stripped (nginx without
	// proxy_http_version 1.1 / Upgrade+Connection headers, or Cloudflare
	// with WebSockets disabled). Log what actually arrived so the hop that
	// dropped it is identifiable — never the token.
	log.Warnf("bus websocket: 426 not an upgrade request: connection=%q upgrade=%q proto=%q x_forwarded_for=%q",
		c.Get(fiber.HeaderConnection), c.Get(fiber.HeaderUpgrade), c.Protocol(), c.Get(fiber.HeaderXForwardedFor))
	return fiber.ErrUpgradeRequired
}

// serviceBusStreamParams authenticates a push-stream request (WS or SSE) and
// resolves its channel list and resume cursor from the query string — both
// transports use query params because neither browser API can set headers.
// SSE reconnects may also carry Last-Event-ID; the larger cursor wins so a
// resumed stream never replays rows the client already received.
func serviceBusStreamParams(c fiber.Ctx) (*internalroutes.RPCRequestIdentity, []string, int64, error) {
	identity, err := serviceBusWebSocketIdentity(c)
	if err != nil {
		return nil, nil, 0, err
	}
	// Refuse before upgrading so the client sees 429 instead of a stream that
	// closes at once; runServiceBusStream's acquire is the hard bound.
	if !streamSlots.available(identity.DBName, identity.UserID, serviceBusMaxStreamsPerDB) {
		return nil, nil, 0, fiber.NewError(fiber.StatusTooManyRequests, "too many open bus streams")
	}
	var rawChannels any
	if channelsParam := strings.TrimSpace(c.Query("channels")); channelsParam != "" {
		rawChannels = strings.Split(channelsParam, ",")
	}
	normalized := authorizedServiceBusChannels(c.Context(), identity, rawChannels)
	lastID, _ := strconv.ParseInt(c.Query("last", "0"), 10, 64)
	if eventID, parseErr := strconv.ParseInt(strings.TrimSpace(c.Get("Last-Event-ID")), 10, 64); parseErr == nil && eventID > lastID {
		lastID = eventID
	}
	return identity, normalized, lastID, nil
}

// serviceBusWebSocketIdentity validates the `token` query param the same way
// RequireRPCIdentity validates a Bearer header, and resolves the tenant DB
// from either X-Database-Name or the `db` query param — the header is kept
// as an option for non-browser callers, but query param support is what
// actually matters here since browsers can't set headers on a WS handshake.
func serviceBusWebSocketIdentity(c fiber.Ctx) (*internalroutes.RPCRequestIdentity, error) {
	token := strings.TrimSpace(c.Query("token"))
	if token == "" {
		return nil, fiber.NewError(fiber.StatusUnauthorized, "bus websocket requires a token query parameter")
	}
	dbName, err := internalroutes.ResolveRPCDBName(internalroutes.RequestDBNameRaw(c))
	if err != nil {
		return nil, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	identity, err := baseauth.CurrentValidator().Validate(c.Context(), token)
	if err != nil || identity == nil {
		log.Errorf("bus websocket: invalid bearer token")
		return nil, fiber.NewError(fiber.StatusUnauthorized, "invalid bearer token")
	}
	if err := internalroutes.EnsureIdentityDBMatch(identity.DBName, dbName); err != nil {
		return nil, fiber.NewError(fiber.StatusUnauthorized, err.Error())
	}
	return &internalroutes.RPCRequestIdentity{
		UserID:     identity.UserID,
		Username:   identity.Login,
		RoleID:     identity.RoleID,
		RoleName:   identity.RoleName,
		GroupIDs:   append([]int64(nil), identity.GroupIDs...),
		DBName:     dbName,
		CompanyID:  identity.CompanyID,
		CompanyIDs: append([]int64(nil), identity.CompanyIDs...),
		AuthMode:   "oauth",
		Sudo:       identity.IsSuperadmin,
		Lang:       identity.Lang,
	}, nil
}

// serviceBusStreamSink is the transport-specific half of a push stream:
// runServiceBusStream owns the LISTEN subscription and polling, and
// hands each new row / heartbeat to the sink. done (optional) closes when
// the transport knows the client left; otherwise a failed write ends it.
type serviceBusStreamSink struct {
	name      string
	writeRow  func(row map[string]any) error
	heartbeat func() error
	done      <-chan struct{}
}

// handleServiceBusWebSocket is the real-time push transport: it subscribes
// to the database's shared LISTEN on busmodels.ServiceBusNotifyChannel and
// re-polls base_service_bus (same query as the HTTP-polling transport)
// whenever a NOTIFY wakes it up or the heartbeat interval elapses, writing each new row as a JSON WebSocket message. A dedicated
// reader goroutine exists only to detect the client closing the connection
// (and to service control-frame ping/pong) — this handler never expects the
// client to send application messages.
func handleServiceBusWebSocket(conn *websocket.Conn) {
	defer conn.Close()

	identity, _ := conn.Locals("sb_identity").(*internalroutes.RPCRequestIdentity)
	normalized, _ := conn.Locals("sb_channels").([]string)
	lastID, _ := conn.Locals("sb_last").(int64)
	if identity == nil {
		return
	}

	done := make(chan struct{})
	var closeOnce sync.Once
	closeConn := func() {
		closeOnce.Do(func() {
			close(done)
			_ = conn.Close()
		})
	}
	defer closeConn()

	// The read loop's only jobs are noticing the client went away and letting
	// the underlying library answer ping/pong control frames — nothing here
	// expects an application-level message from the client.
	go func() {
		defer closeConn()
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	runServiceBusStream(identity, normalized, lastID, serviceBusStreamSink{
		name:      "bus websocket",
		writeRow:  func(row map[string]any) error { return conn.WriteJSON(row) },
		heartbeat: func() error { return conn.WriteMessage(websocket.PingMessage, nil) },
		done:      done,
	})
}

// handleServiceBusSSE serves the same push stream as /bus/ws as
// Server-Sent Events. Plain HTTP streaming needs no Upgrade, so it works
// behind proxies that strip the WebSocket handshake. X-Accel-Buffering
// turns off nginx response buffering for this request even when the proxy
// location has proxy_buffering on; the heartbeat comment keeps idle
// proxies (Cloudflare drops silent streams after ~100s) from closing it.
func handleServiceBusSSE(c fiber.Ctx) error {
	identity, normalized, lastID, err := serviceBusStreamParams(c)
	if err != nil {
		return err
	}
	c.Set(fiber.HeaderContentType, "text/event-stream")
	c.Set(fiber.HeaderCacheControl, "no-cache, no-transform")
	c.Set("X-Accel-Buffering", "no")
	return c.SendStreamWriter(func(w *bufio.Writer) {
		// Flush headers plus a comment immediately so EventSource fires
		// `open` now instead of waiting for the first notification.
		if writeServiceBusSSE(w, ": connected\n\n") != nil {
			return
		}
		runServiceBusStream(identity, normalized, lastID, serviceBusStreamSink{
			name:      "bus sse",
			writeRow:  func(row map[string]any) error { return writeServiceBusSSERow(w, row) },
			heartbeat: func() error { return writeServiceBusSSE(w, ": ping\n\n") },
		})
	})
}

// writeServiceBusSSERow frames one bus row as an SSE message. The row id
// doubles as the SSE event id so a native reconnect resumes via
// Last-Event-ID (see serviceBusStreamParams).
func writeServiceBusSSERow(w *bufio.Writer, row map[string]any) error {
	payload, err := json.Marshal(row)
	if err != nil {
		return err
	}
	frame := "data: " + string(payload) + "\n\n"
	if id, ok := serviceBusStreamRowID(row); ok {
		frame = "id: " + strconv.FormatInt(id, 10) + "\n" + frame
	}
	return writeServiceBusSSE(w, frame)
}

// writeServiceBusSSE writes and flushes one SSE chunk; a flush error means
// the client (or a proxy hop) is gone and the stream should stop.
func writeServiceBusSSE(w *bufio.Writer, chunk string) error {
	if _, err := w.WriteString(chunk); err != nil {
		return err
	}
	return w.Flush()
}

// runServiceBusStream subscribes to the database's shared LISTEN (busHub)
// for the life of a push stream and re-polls base_service_bus on every
// wake-up, on every heartbeat (a safety net while the listener reconnects),
// and shortly after a poll that was held back behind an id still in flight
// (a rolled-back publish sends no NOTIFY to clear it). It returns when the
// client leaves, a write fails, or the server shuts down (see Shutdown).
func runServiceBusStream(identity *internalroutes.RPCRequestIdentity, normalized []string, lastID int64, sink serviceBusStreamSink) {
	// The request context is gone once the connection is hijacked; derive
	// from shutdownCtx so in-flight polls stop with the server.
	requestCtx := serviceBusRuntimeContext(shutdownCtx, identity)
	pool := orm.PGXPoolFromContext(requestCtx)
	if pool == nil {
		log.Warnf("%s: service bus database pool is not available", sink.name)
		return
	}
	releaseSlot, ok := streamSlots.acquire(identity.DBName, identity.UserID, serviceBusMaxStreamsPerDB)
	if !ok {
		log.Warnf("%s: stream limit reached for db %q user %d", sink.name, identity.DBName, identity.UserID)
		return
	}
	defer releaseSlot()

	sub, unsubscribe := busHub.subscribe(identity.DBName, normalized, serviceBusPGXConnect(pool.Config().ConnConfig))
	defer unsubscribe()

	heartbeat := time.NewTicker(serviceBusStreamHeartbeatInterval)
	defer heartbeat.Stop()
	held, ok := pollServiceBusStream(requestCtx, normalized, &lastID, sink)
	for ok {
		var recheck <-chan time.Time
		if held {
			recheck = time.After(serviceBusStreamRecheckInterval)
		}
		select {
		case <-sink.done:
			return
		case <-shuttingDown:
			return
		case <-sub.wake:
		case <-recheck:
		case <-heartbeat.C:
			if sink.heartbeat() != nil {
				return
			}
		}
		held, ok = pollServiceBusStream(requestCtx, normalized, &lastID, sink)
	}
}

// pollServiceBusStream sends everything newer than *lastID, page by page,
// advancing *lastID. ok is false the moment a write fails, which is the
// stream's signal to stop; a poll error is logged and retried on the next
// wake-up rather than ending the stream. held is the last page's hold-back
// flag (see busqueries.ServiceBusCommitLag).
func pollServiceBusStream(ctx context.Context, normalized []string, lastID *int64, sink serviceBusStreamSink) (held bool, ok bool) {
	for {
		// Authorization is re-checked on every poll so a revoked channel stops delivering.
		channels := busmodels.AuthorizeServiceBusChannels(ctx, normalized)
		if len(channels) == 0 {
			return false, true
		}
		rows, pageHeld, err := busmodels.PollServiceBusRows(ctx, channels, *lastID)
		if err != nil {
			log.Warnf("%s: poll: %v", sink.name, err)
			return false, true
		}
		for _, row := range rows {
			if id, idOK := serviceBusStreamRowID(row); idOK && id > *lastID {
				*lastID = id
			}
			if err := sink.writeRow(row); err != nil {
				return false, false
			}
		}
		if len(rows) < busqueries.ServiceBusPollLimit {
			return pageHeld, true
		}
	}
}

// serviceBusStreamRowID reads the "id" field a poll row carries (always an
// int64 from serviceBusRowMap, but modelruntime results travel as `any`).
func serviceBusStreamRowID(row map[string]any) (int64, bool) {
	id, ok := row["id"].(int64)
	return id, ok
}

func parseServiceBusChannelPayload(c fiber.Ctx) (any, int64, error) {
	var payload serviceBusChannelPayload
	if err := c.Bind().Body(&payload); err != nil {
		return nil, 0, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	// The worker posts both nested and flat payload shapes, so collapse them once
	// here and keep every route handler on the same normalized contract.
	if payload.Params.Channels != nil {
		return payload.Params.Channels, payload.Params.Last, nil
	}
	return payload.Channels, payload.Last, nil
}

func parseServiceBusLastNotificationID(c fiber.Ctx) (int64, error) {
	var payload serviceBusLastNotificationPayload
	if err := c.Bind().Body(&payload); err != nil {
		return 0, fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	if payload.Params.LastNotificationID != 0 {
		return payload.Params.LastNotificationID, nil
	}
	return payload.LastNotificationID, nil
}

// serviceBusSubscribeEnvelope includes `topics` (channel -> MQTT topic name)
// alongside the existing channels/channel_keys/last fields: today the topic
// name always equals the normalized channel name (channels are already valid
// MQTT topic strings — no wildcards, broker ACLs enforce isolation per
// service-bus.md), but keeping it a separate map lets the mapping diverge
// later without another response-shape migration.
func serviceBusSubscribeEnvelope(normalized []string, last int64) fiber.Map {
	topics := make(fiber.Map, len(normalized))
	for _, channel := range normalized {
		topics[channel] = channel
	}
	return fiber.Map{
		"result": fiber.Map{
			"channels":     normalized,
			"channel_keys": normalized,
			"topics":       topics,
			"last":         last,
		},
	}
}

func serviceBusLastID(ctx context.Context, identity *internalroutes.RPCRequestIdentity) (int64, error) {
	// Resolve the current high-water mark through the same runtime model so the
	// route layer does not duplicate business logic.
	result, err := modelruntime.NewService().Execute(serviceBusRuntimeContext(ctx, identity), modelruntime.Request{
		Model:  busmodels.ServiceBusModelName,
		Method: "last_id",
	})
	if err != nil {
		return 0, err
	}
	lastID, ok := result.(int64)
	if !ok {
		log.Errorf("unexpected service bus last_id result %T", result)
		return 0, fmt.Errorf("unexpected service bus last_id result %T", result)
	}
	return lastID, nil
}

// authorizedServiceBusChannels normalizes the requested channels (plus the caller's defaults) and
// keeps only those the caller may receive (plan T16), so the response and the stream's channel
// list show what was actually granted. The poll method re-checks on every call, which also drops
// a channel whose membership is revoked while a stream is open.
func authorizedServiceBusChannels(ctx context.Context, identity *internalroutes.RPCRequestIdentity, raw any) []string {
	normalized := busmodels.NormalizeServiceBusChannels(raw, identity.UserID, true)
	return busmodels.AuthorizeServiceBusChannels(serviceBusRuntimeContext(ctx, identity), normalized)
}

func serviceBusRuntimeContext(ctx context.Context, identity *internalroutes.RPCRequestIdentity) context.Context {
	// Bind the authenticated caller into the ORM environment so service-bus
	// methods can resolve per-user default channels safely.
	return orm.ContextWithEnvironment(ctx, orm.EnvironmentForIdentity(&orm.RBACIdentity{
		UserID:       identity.UserID,
		DBName:       identity.DBName,
		Login:        identity.Username,
		RoleID:       identity.RoleID,
		RoleName:     identity.RoleName,
		GroupIDs:     append([]int64(nil), identity.GroupIDs...),
		CompanyID:    identity.CompanyID,
		CompanyIDs:   append([]int64(nil), identity.CompanyIDs...),
		AuthMode:     identity.AuthMode,
		IsSuperadmin: identity.Sudo,
	}))
}

// fiber:context-methods migrated
