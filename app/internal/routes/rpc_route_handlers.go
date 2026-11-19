package routes

// File overview:
// - Responsibility: JSON-RPC HTTP handlers for the /rpc, /rpc/db, /rpc_meta, and /rpc/{model}/{method} endpoints registered in rpc_route.go.
// - Key symbols: `handleDatabaseJSONRPC`, `handleModelMetaJSONRPC`, `handleCallKW`, `handleCallKWWithID`, `handleModelJSONRPC`.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/base"
	"git.thebase.vn/base_hub/internal/modelruntime"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/gofiber/fiber/v3"
)

func handleDatabaseJSONRPC(c fiber.Ctx) error {
	// Superadmin only for every method: `list` names every tenant DB on the server (plan T36).
	if _, err := requireRPCIdentity(c, true); err != nil {
		return err
	}
	request, err := decodeJSONRPCRequest(c.Body())
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(nil, -32700, err))
	}

	method := strings.TrimSpace(c.Params("method"))
	switch method {
	case "list":
		return c.JSON(newJSONRPCSuccessResponse(request.ID, map[string]any{
			"databases": orm.RegisteredDBNames(),
			"current":   currentRPCDBName(),
		}))
	case "init":
		// Connect to a named database, create its schema, and register it in the
		// ORM registry so subsequent requests can use it (superadmin, checked above).
		if dbInitFunc == nil {
			return c.Status(fiber.StatusNotImplemented).JSON(newJSONRPCErrorResponse(
				request.ID, -32000, fmt.Errorf("db init is not available on this server"),
			))
		}
		name, _ := request.Params["name"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(
				request.ID, -32602, fmt.Errorf("params.name is required"),
			))
		}
		if err := dbInitFunc(c.Context(), name); err != nil {
			var fiberErr *fiber.Error
			if errors.As(err, &fiberErr) {
				return fiberErr
			}
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, -32000, err))
		}
		return c.JSON(newJSONRPCSuccessResponse(request.ID, map[string]any{
			"initialized": true,
			"database":    name,
		}))
	default:
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(
			request.ID,
			-32000,
			fmt.Errorf("database method %q is not supported by this backend", method),
		))
	}
}

func handleModelMetaJSONRPC(c fiber.Ctx) error {
	if _, err := requireRPCIdentity(c, true); err != nil {
		return err
	}
	request, err := decodeJSONRPCRequest(c.Body())
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(nil, -32700, err))
	}

	method := strings.TrimSpace(c.Params("method"))
	switch method {
	case "publish":
		// Admin-only: promotes a metadata-only sys.model (created via
		// create/add_field) into a live, DB-backed runtime model. See
		// orm.PublishModel for the full pipeline.
		if _, err := requireRPCIdentity(c, true); err != nil {
			return err
		}
		name, _ := request.Params["model"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(
				request.ID, -32602, fmt.Errorf("params.model is required"),
			))
		}
		meta, err := orm.PublishModel(c.Context(), name)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, -32000, err))
		}
		return c.JSON(newJSONRPCSuccessResponse(request.ID, map[string]any{"model": meta}))
	case "unpublish":
		// Admin-only: takes a published custom model back out of the live
		// runtime registry without touching its sys.model catalog row or its
		// physical table/data. See orm.UnpublishModel.
		if _, err := requireRPCIdentity(c, true); err != nil {
			return err
		}
		name, _ := request.Params["model"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(
				request.ID, -32602, fmt.Errorf("params.model is required"),
			))
		}
		if err := orm.UnpublishModel(c.Context(), name); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, -32000, err))
		}
		return c.JSON(newJSONRPCSuccessResponse(request.ID, map[string]any{"unpublished": true}))
	case "reload_registry":
		if err := orm.SyncModelRegistry(c.Context()); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(request.ID, -32000, err))
		}
		return c.JSON(newJSONRPCSuccessResponse(request.ID, map[string]any{"reloaded": true}))
	default:
		return c.Status(fiber.StatusBadRequest).JSON(newJSONRPCErrorResponse(
			request.ID,
			-32000,
			fmt.Errorf("metadata method %q is not supported by this backend", method),
		))
	}
}

func handleCallKW(c fiber.Ctx) error {
	return handleModelJSONRPC(c, false)
}

func handleCallKWWithID(c fiber.Ctx) error {
	return handleModelJSONRPC(c, true)
}

// handleModelJSONRPC keeps the transport thin: normalize the request path
// and args shape, then delegate the actual business logic to modelruntime.
func handleModelJSONRPC(c fiber.Ctx, pathCarriesID bool) error {
	modelName := strings.TrimSpace(c.Params("model"))
	methodName := strings.TrimSpace(c.Params("method"))
	if modelName == "" || methodName == "" {
		return fiber.NewError(fiber.StatusBadRequest, "model and method are required")
	}
	return executeModelJSONRPC(c, modelName, methodName, pathCarriesID)
}

// executeModelJSONRPC runs the shared model/method dispatch used by both the
// canonical /rpc/{model}/{method}[/{id}] routes and any RuntimeMethod
// AliasRoute mounted under /base/*.
func executeModelJSONRPC(c fiber.Ctx, modelName, methodName string, pathCarriesID bool) error {
	identity, err := requireRPCIdentity(c, false)
	if err != nil {
		return err
	}
	var payload CallKWRequestDoc
	if err := c.Bind().Body(&payload); err != nil {
		return rpcFail(c, orm.ErrParse, err.Error())
	}
	setRequestID(c, payload.ID)

	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return rpcFail(c, orm.ErrMethodNotFound, fmt.Sprintf("orm: model %q is not registered", modelName))
	}
	method, ok := meta.LookupRuntimeMethod(methodName)
	if !ok {
		return rpcFail(c, orm.ErrMethodNotFound, fmt.Sprintf("orm: runtime method %q is not registered on model %q", methodName, modelName))
	}

	// Non-public methods (the `_`-prefixed helpers such as _get_default_form_view) are callable
	// from Go only; /rpc refuses them like an unknown method, as MCP already does (plan T36).
	if method.Internal || !method.Public {
		return rpcFail(c, orm.ErrMethodNotFound, orm.ErrInternalMethodNotCallable(modelName, methodName).Error())
	}

	idsFromArgs, strippedArgsIDs := stripLegacyRPCIDArg(payload.Args)
	if shouldStripLegacyRPCIDArg(pathCarriesID, method.UsesPathID, idsFromArgs, strippedArgsIDs) {
		// Some clients often send record ids twice: once in the path and
		// again as args[0]. Strip the redundant payload form so runtime methods
		// see one stable signature regardless of which client called us.
		payload.Args = payload.Args[1:]
	}

	var ids []string
	if pathCarriesID {
		if !method.UsesPathID {
			return rpcFail(c, orm.ErrInvalidParams, fmt.Sprintf("model method %q uses /rpc/{model}/{method}, not /rpc/{model}/{method}/{id}", methodName))
		}
		parsedIDs, err := parseRPCPathIDs(c.Params("id"), methodAllowsZeroPathID(methodName))
		if err != nil {
			return rpcFail(c, orm.ErrInvalidParams, err.Error())
		}
		ids = parsedIDs
		if len(idsFromArgs) > 0 && !equalStringSlices(idsFromArgs, ids) {
			return rpcFail(c, orm.ErrInvalidParams, "body args[0] IDs must match the path ID")
		}
	} else if method.UsesPathID {
		return rpcFail(c, orm.ErrInvalidParams, fmt.Sprintf("model method %q requires /rpc/{model}/{method}/{id}", methodName))
	}
	requestCtx := rpcIdentityContext(c, identity)
	if err := orm.EnsureRuntimeModelAccess(requestCtx, meta, method, ids); err != nil {
		// Read-only methods degrade to their natural empty/zero result instead
		// of a 403: the UI treats "nothing here" and "not allowed to see it"
		// the same way, so a menu/dashboard/widget that renders whatever an
		// RPC returns just renders empty instead of surfacing a permission
		// error it has no good way to display. Mutating methods (create/write/
		// unlink/etc.) keep erroring — a blocked write must not look like it
		// silently no-op'd. The denial itself is still logged server-side for
		// audit purposes even though the client only sees an empty result.
		if method.ReadOnly {
			log.Warnf("rpc: %s", err.Error())
			return c.JSON(rpcSuccess(c, zeroReadResultForMethod(methodName)))
		}
		return rpcAccessDeniedError(err)
	}
	// Client kwargs must not carry the ORM environment overrides: they are applied after the ACL
	// check above and would otherwise grant sudo / another uid / rewritten company+group scope.
	payload.Kwargs = withFrontendRPCContext(orm.SanitizeClientKwargs(payload.Kwargs), methodName)

	// The row cap (server.maxReadLimit) binds this call only, not the server-side calls it makes.
	result, err := modelruntime.NewService().Execute(orm.ContextWithClientReadCap(requestCtx), modelruntime.Request{
		Model:  modelName,
		Method: methodName,
		IDs:    ids,
		Args:   payload.Args,
		Kwargs: cloneAnyMap(payload.Kwargs),
	})
	if err != nil {
		var fiberErr *fiber.Error
		var typed *orm.Error
		if errors.As(err, &fiberErr) {
			return err
		}
		// Typed errors keep their own status and code; anything else is a flat 400.
		if errors.As(err, &typed) {
			return err
		}
		return fiber.NewError(fiber.StatusBadRequest, err.Error())
	}
	return c.JSON(rpcSuccess(c, orm.RedactSecretFieldsForClient(meta, methodName, result)))
}

// rpcFail is the shared failure for malformed /rpc calls: a typed error rendered as JSON-RPC.
func rpcFail(_ fiber.Ctx, code int, message string) error {
	return orm.NewError(code, 0, message)
}

// rpcSuccess builds the success body; the id is the caller's own or a generated one.
func rpcSuccess(c fiber.Ctx, result any) fiber.Map {
	return fiber.Map{"id": requestID(c), "result": result}
}

// rpcIdentityContext rebuilds the ORM environment from the authenticated RPC identity before
// dispatch. This is the bridge from HTTP auth into model-level RBAC, company scoping, sudo, and
// any downstream context-aware hooks; /rpc and every alias route share it.
func rpcIdentityContext(c fiber.Ctx, identity *rpcRequestIdentity) context.Context {
	return orm.ContextWithEnvironment(c.Context(), orm.EnvironmentForIdentity(&orm.RBACIdentity{
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

// zeroReadResultForMethod returns the empty/zero-value shape a read-only
// runtime method's "result" field would carry on a legitimate empty outcome,
// so a permission-denied response can reuse it and look like "found
// nothing" rather than an error. Keyed by the field's data type: list-
// shaped methods get an empty array (so frontend .map/.forEach callers don't
// break), count gets 0, boolean predicates get false, and anything else
// (single-record reads, free-form metadata methods) gets nil.
func zeroReadResultForMethod(methodName string) any {
	switch strings.TrimSpace(methodName) {
	case "search", "search_read", "search_fetch", "read", "name_search", "name_get", "read_group":
		return []any{}
	case "search_count":
		return int64(0)
	case "has_access", "check_access_rights", "exists":
		return false
	case "fields_get", "default_get":
		return map[string]any{}
	default:
		return nil
	}
}

// registerRuntimeMethodAliasRoutes mounts every RuntimeMethod.AliasRoute and
// RuntimeMethod.Aliases route declared across registered models. Legacy routes
// mount at "POST /base/<route>" (JSON-RPC body); REST-form routes ("GET /base/x")
// mount at their exact verb + absolute path. It runs once at startup, after
// module init() has populated the model registry, so it only sees statically
// declared methods (dynamically published custom models don't carry Go-defined
// RuntimeMethods). Duplicate verb+path pairs are logged and skipped (first wins).
func registerRuntimeMethodAliasRoutes(router fiber.Router) {
	type aliasMount struct {
		verb, path, owner, model string
		method                   orm.RuntimeMethod
		alias                    orm.MethodAlias
		rest                     bool
	}
	var mounts []aliasMount
	seen := make(map[string]string)
	for _, meta := range orm.RegisteredModels() {
		modelName := meta.Name
		for _, methodMeta := range meta.RuntimeMethods {
			method, ok := meta.LookupRuntimeMethod(methodMeta.Name)
			if !ok {
				continue
			}
			aliases := append([]orm.MethodAlias{{Route: method.AliasRoute}}, method.Aliases...)
			for _, alias := range aliases {
				verb, path, rest := orm.ParseAliasRoute(alias.Route)
				if path == "" {
					continue
				}
				owner := modelName + "." + method.Name
				key := verb + " " + path
				if existing, ok := seen[key]; ok {
					log.Errorf("rpc: alias route %q from %q conflicts with %q, skipping", key, owner, existing)
					continue
				}
				seen[key] = owner
				mounts = append(mounts, aliasMount{verb: verb, path: path, owner: owner, model: modelName, method: method, alias: alias, rest: rest})
			}
		}
	}
	// Fiber matches in registration order, so a static sibling ("/status/versions") must be
	// mounted before a param route that would also match it ("/status/:serial").
	sort.SliceStable(mounts, func(i, j int) bool {
		return aliasParamCount(mounts[i].path) < aliasParamCount(mounts[j].path)
	})
	for _, mount := range mounts {
		switch {
		case mount.rest && mount.method.AliasAnonymous:
			// Exact-path public contracts (device bootstrap) need the REST form without a session.
			registerAnonymousAliasRoute(router, mount.verb, mount.path, mount.owner, mount.model, mount.method, mount.method.AliasForwardAuthorization)
		case mount.rest && mount.alias.Anonymous:
			// Per-alias opt-in: only this route is public, the method's other aliases keep session auth.
			registerAnonymousAliasRoute(router, mount.verb, mount.path, mount.owner, mount.model, mount.method, mount.alias.ForwardAuthorization)
		case mount.rest:
			router.Add([]string{mount.verb}, mount.path, newRESTAliasHandler(mount.model, mount.method, mount.alias))
		case mount.method.AliasAnonymous:
			registerAnonymousAliasRoute(router, fiber.MethodPost, mount.path, mount.owner, mount.model, mount.method, mount.method.AliasForwardAuthorization)
		default:
			router.Post(mount.path, newAliasRPCHandler(mount.model, mount.method.Name, mount.method.UsesPathID))
		}
	}
}

// aliasParamCount counts ":param" / "*" segments, the sort key that mounts static routes first.
func aliasParamCount(path string) int {
	count := 0
	for _, segment := range strings.Split(path, "/") {
		if strings.HasPrefix(segment, ":") || strings.HasPrefix(segment, "*") {
			count++
		}
	}
	return count
}

// newRESTAliasHandler serves a REST-form alias: same RPC identity and EnsureRuntimeModelAccess
// gate as /rpc, then the alias's Bind/Render adapters (or the default contract: path params,
// query keys and the JSON body object as kwargs, ":id" as the record ids, 200 {"result"}).
func newRESTAliasHandler(modelName string, method orm.RuntimeMethod, alias orm.MethodAlias) fiber.Handler {
	return func(c fiber.Ctx) error {
		identity, err := requireRPCIdentity(c, false)
		if err != nil {
			return writeRESTAliasError(c, alias, nil, &orm.AliasError{Status: fiber.StatusUnauthorized, Code: "unauthorized", Message: fiberErrorMessage(err)})
		}
		requestCtx := rpcIdentityContext(c, identity)
		call := restAliasCall(c, requestCtx, alias.Route)
		bind := alias.Bind
		if bind == nil {
			bind = defaultRESTAliasBind(method.UsesPathID)
		}
		if err := bind(call); err != nil {
			return writeRESTAliasError(c, alias, call, err)
		}
		result, err := executeRESTAliasMethod(requestCtx, modelName, method.Name, call.IDs, call.Args, call.Kwargs, call.Context)
		if err != nil {
			return writeRESTAliasError(c, alias, call, err)
		}
		if alias.Render == nil {
			return writeRESTAliasBody(c, fiber.StatusOK, fiber.Map{"id": requestID(c), "result": result})
		}
		status, body, err := alias.Render(call, result)
		if err != nil {
			return writeRESTAliasError(c, alias, call, err)
		}
		return writeRESTAliasBody(c, status, body)
	}
}

// writeRESTAliasBody sends a REST alias success body. It is marshalled directly because the app
// JSON encoder rewrites null to false.
func writeRESTAliasBody(c fiber.Ctx, status int, body any) error {
	// Module renderers build {"id": nil, "result": ...}; the kernel owns the id.
	if envelope, ok := body.(map[string]any); ok {
		if id, present := envelope["id"]; present && id == nil {
			envelope["id"] = requestID(c)
		}
	}
	payload, err := json.Marshal(base.TimesToUnix(body))
	if err != nil {
		return err
	}
	return c.Status(status).Type("json").Send(payload)
}

// restAliasCall copies the route's path params, first-value query keys and the raw body
// (fasthttp reuses its buffers once the handler returns), and wires call.Call to the same
// identity-bound, ACL-checked dispatch.
func restAliasCall(c fiber.Ctx, requestCtx context.Context, route string) *orm.AliasCall {
	_, path, _ := orm.ParseAliasRoute(route)
	params := map[string]string{}
	for _, segment := range strings.Split(path, "/") {
		if name, ok := strings.CutPrefix(segment, ":"); ok {
			params[name] = c.Params(name)
		}
	}
	query := map[string]string{}
	for key, value := range c.Queries() {
		query[key] = value
	}
	call := &orm.AliasCall{
		Ctx:    requestCtx,
		Params: params,
		Query:  query,
		Body:   append([]byte(nil), c.Body()...),
	}
	call.Call = func(model, method string, ids []string, kwargs map[string]any) (any, error) {
		// Follow-ups (count, re-read, relations) see the same server-built context as the main
		// call, so e.g. an active_test=false list and its total count agree.
		return executeRESTAliasMethod(requestCtx, model, method, ids, nil, kwargs, call.Context)
	}
	return call
}

// defaultRESTAliasBind is the REST-form contract when an alias declares no Bind: ":id" feeds the
// record ids of a UsesPathID method, and query keys, path params and the JSON body object (in
// increasing precedence) become kwargs.
func defaultRESTAliasBind(usesPathID bool) func(*orm.AliasCall) error {
	return func(call *orm.AliasCall) error {
		body, err := call.JSONObject()
		if err != nil {
			return err
		}
		kwargs := map[string]any{}
		for key, value := range call.Query {
			kwargs[key] = value
		}
		for key, value := range call.Params {
			kwargs[key] = value
		}
		for key, value := range body {
			kwargs[key] = value
		}
		if usesPathID {
			id := strings.TrimSpace(call.Params["id"])
			if id == "" {
				return &orm.AliasError{Status: fiber.StatusBadRequest, Code: "invalid_id", Message: "id is required"}
			}
			call.IDs = []string{id}
			delete(kwargs, "id")
		}
		call.Kwargs = kwargs
		return nil
	}
}

// executeRESTAliasMethod runs one runtime method for a REST-form alias. A denied
// EnsureRuntimeModelAccess is a 403 "forbidden" (an external REST contract reports it, unlike
// /rpc's silent empty read). The ORM's environment-override kwargs are dropped so client input
// can't escalate; only the server-built envContext is applied as kwargs["context"].
func executeRESTAliasMethod(ctx context.Context, modelName, methodName string, ids []string, args []any, kwargs, envContext map[string]any) (any, error) {
	meta, ok := orm.LookupModel(modelName)
	if !ok {
		return nil, fmt.Errorf("orm: model %q is not registered", modelName)
	}
	method, ok := meta.LookupRuntimeMethod(methodName)
	if !ok {
		return nil, fmt.Errorf("orm: runtime method %q is not registered on model %q", methodName, modelName)
	}
	// Same rule as /rpc: Internal and non-Public methods are not client-callable (plan T36).
	if method.Internal || !method.Public {
		return nil, &orm.AliasError{Status: fiber.StatusNotFound, Code: "not_found", Message: orm.ErrInternalMethodNotCallable(modelName, methodName).Error()}
	}
	if err := orm.EnsureRuntimeModelAccess(ctx, meta, method, ids); err != nil {
		log.Warnf("rpc: alias %s.%s: %s", modelName, methodName, err.Error())
		return nil, &orm.AliasError{Status: fiber.StatusForbidden, Code: "forbidden", Message: err.Error()}
	}
	safe := cloneAnyMap(kwargs)
	if safe == nil {
		safe = map[string]any{}
	}
	for _, key := range orm.AliasReservedKwargs {
		delete(safe, key)
	}
	if len(envContext) > 0 {
		safe["context"] = cloneAnyMap(envContext)
	}
	result, err := modelruntime.NewService().Execute(orm.ContextWithClientReadCap(ctx), modelruntime.Request{
		Model:  modelName,
		Method: methodName,
		IDs:    ids,
		Args:   args,
		Kwargs: safe,
	})
	if err != nil {
		return result, err
	}
	return orm.RedactSecretFieldsForClient(meta, methodName, result), nil
}

// writeRESTAliasError renders every REST-alias failure as the standard JSON-RPC style error body
// (no header opt-in: REST aliases are a new surface, not a legacy contract).
func writeRESTAliasError(c fiber.Ctx, alias orm.MethodAlias, call *orm.AliasCall, err error) error {
	var aliasErr *orm.AliasError
	var typed *orm.Error
	// The alias's Error hook classifies module failures first; its legacy (status, body) pair is
	// converted into the standard {"id","error":{"code","message"}} body.
	if alias.Error != nil && !errors.As(err, &aliasErr) && !errors.As(err, &typed) {
		if status, body := alias.Error(call, err); status != 0 {
			return writeAPIError(c, orm.LegacyError(status, body))
		}
	}
	return writeAPIError(c, toAPIError(err))
}

// fiberErrorMessage unwraps a fiber.Error's message so a 401 body doesn't repeat the status text.
func fiberErrorMessage(err error) string {
	var fiberErr *fiber.Error
	if errors.As(err, &fiberErr) {
		return fiberErr.Message
	}
	return err.Error()
}

// registerAnonymousAliasRoute mounts an AliasAnonymous method (verb + CORS preflight; legacy aliases
// pass POST). A path-ID method is refused: with no auth and no ACL there is nothing to scope a
// record id against.
func registerAnonymousAliasRoute(router fiber.Router, verb, path, owner, modelName string, method orm.RuntimeMethod, forwardAuthorization bool) {
	if method.UsesPathID {
		log.Errorf("rpc: anonymous alias route %q from %q requires a ModelOnly method, skipping", path, owner)
		return
	}
	router.Add([]string{verb}, path, newAnonymousAliasHandler(modelName, method.Name, forwardAuthorization))
	router.Options(path, handleAnonymousAliasPreflight(verb))
}

// newAnonymousAliasHandler dispatches an unauthenticated alias call. The request context already
// carries the DB bound by bindRequestDatabase (default DB, or ?db= / X-Database-Name), and no
// identity/ACL is applied — the method receives the raw request and owns validation and guards.
func newAnonymousAliasHandler(modelName, methodName string, forwardAuthorization bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		allowAnyOrigin(c)
		request := &orm.AliasRequest{
			// Copy: fasthttp reuses the body buffer once the handler returns.
			Body:      append([]byte(nil), c.Body()...),
			UserAgent: c.Get(fiber.HeaderUserAgent),
			IP:        c.IP(),
			Headers:   anonymousAliasHeaders(c),
			Params:    anonymousAliasParams(c),
		}
		if forwardAuthorization {
			request.Authorization = c.Get(fiber.HeaderAuthorization)
		}
		result, err := modelruntime.NewService().Execute(c.Context(), modelruntime.Request{
			Model:  modelName,
			Method: methodName,
			Kwargs: map[string]any{orm.AliasRequestKwarg: request},
		})
		if err != nil {
			// Never echo internal errors to an anonymous caller; the method maps client errors
			// to an AliasResponse itself.
			log.Errorf("rpc: anonymous alias %s.%s: %v", modelName, methodName, err)
			return c.Status(fiber.StatusInternalServerError).SendString("error")
		}
		return writeAnonymousAliasResult(c, result)
	}
}

// anonymousAliasParams copies the route's ":param" values (a REST-form route such as
// "GET /base/devices/config/:serial"); legacy routes have none.
func anonymousAliasParams(c fiber.Ctx) map[string]string {
	route := c.Route()
	if route == nil || len(route.Params) == 0 {
		return nil
	}
	params := make(map[string]string, len(route.Params))
	for _, name := range route.Params {
		params[name] = c.Params(name)
	}
	return params
}

// writeAnonymousAliasResult writes an AliasResponse verbatim, or falls back to the JSON-RPC envelope.
func writeAnonymousAliasResult(c fiber.Ctx, result any) error {
	response, ok := result.(orm.AliasResponse)
	if !ok {
		return c.JSON(fiber.Map{"result": result})
	}
	for name, value := range response.Headers {
		c.Set(name, value)
	}
	status := response.Status
	if status == 0 {
		status = fiber.StatusOK
	}
	return c.Status(status).SendString(response.Body)
}

// anonymousAliasHeaders copies the first value of each request header, dropping credentials so an
// anonymous method can never act on (or log) a caller's session.
func anonymousAliasHeaders(c fiber.Ctx) map[string]string {
	headers := map[string]string{}
	for name, values := range c.GetReqHeaders() {
		if len(values) == 0 || strings.EqualFold(name, fiber.HeaderAuthorization) || strings.EqualFold(name, fiber.HeaderCookie) {
			continue
		}
		// Lowercase keys: fasthttp's own canonicalization ("Cf-Ipcountry") is not what callers spell.
		headers[strings.ToLower(name)] = values[0]
	}
	return headers
}

// handleAnonymousAliasPreflight answers a CORS preflight for clients that send a non-simple request.
func handleAnonymousAliasPreflight(verb string) fiber.Handler {
	return func(c fiber.Ctx) error {
		allowAnyOrigin(c)
		c.Set(fiber.HeaderAccessControlAllowMethods, verb+", OPTIONS")
		c.Set(fiber.HeaderAccessControlAllowHeaders, "Content-Type")
		c.Set(fiber.HeaderAccessControlMaxAge, "86400")
		return c.SendStatus(fiber.StatusNoContent)
	}
}

// allowAnyOrigin lets any site read an anonymous alias response; it carries no credentials.
func allowAnyOrigin(c fiber.Ctx) {
	c.Set(fiber.HeaderAccessControlAllowOrigin, "*")
}

// newAliasRPCHandler binds one alias route to a fixed model/method pair so
// the same dispatch logic used by /rpc/{model}/{method}[/{id}] runs behind
// the friendlier /base/* path.
func newAliasRPCHandler(modelName, methodName string, usesPathID bool) fiber.Handler {
	return func(c fiber.Ctx) error {
		return executeModelJSONRPC(c, modelName, methodName, usesPathID)
	}
}

func withFrontendRPCContext(kwargs map[string]any, methodName string) map[string]any {
	next := cloneAnyMap(kwargs)
	if next == nil {
		next = map[string]any{}
	}
	contextValues, _ := next["context"].(map[string]any)
	contextNext := cloneAnyMap(contextValues)
	if contextNext == nil {
		contextNext = map[string]any{}
	}
	// Mark transport-originated mutations so model constraints can enforce
	// frontend-only rules without breaking bootstrap imports or internal jobs.
	contextNext["rpc_source"] = "frontend"
	if trimmed := strings.TrimSpace(methodName); trimmed != "" {
		contextNext["rpc_method"] = trimmed
	}
	next["context"] = contextNext
	return next
}

// fiber:context-methods migrated
