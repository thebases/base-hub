package orm

// File overview:
// - Responsibility: implements the metadata-driven ORM/runtime kernel for runtime methods, where storage, runtime dispatch, and security rules stay centralized.
// - Key symbols: `init`, `RegisterRuntimeMethod`, `RuntimeMethodFunc`, `RuntimeMethod`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"sort"
	"strings"
	"sync"
)

// RuntimeMethodFunc is the generic runtime-method body shape attached to model metadata.
type RuntimeMethodFunc func(ctx context.Context, model *ModelMetadata, ids []string, args []any, kwargs map[string]any) (any, error)

// RuntimeMethod describes one default or model-specific runtime method.
type RuntimeMethod struct {
	Name   string
	Public bool
	// Internal marks a method that only server-side callers (Lua/workflow steps, Go services via
	// ExecuteRuntimeMethod) may run. Client transports (/rpc, alias routes, MCP) refuse it with
	// "method not found" so a user with model access cannot invoke step logic that trusts its caller.
	Internal   bool
	ModelOnly  bool
	ReadOnly   bool
	UsesPathID bool
	Call       RuntimeMethodFunc
	// Description is plain text shown in the API docs; empty falls back to the built-in default
	// text for standard methods, and the box is hidden when neither exists.
	Description string
	// DescriptionFile is a path (`<module>/<file>`, resolved to <module>/<lang>/<file> under internal/documents/apidoc or modules/<module>/apidoc) to a markdown document opened from a
	// "Show Description" popup in the API docs. Independent of Description.
	DescriptionFile string
	// AliasRoute, when set, mounts this method as a friendly transport route
	// in addition to the canonical /rpc/{model}/{method}[/{id}] path. Two forms:
	//   - "orders/:id/cancel": POST "/base/<AliasRoute>" with the JSON-RPC
	//     {args, kwargs} body and {"result"} reply.
	//   - "GET /base/orders/:serial" (leading HTTP verb): REST form, mounted at
	//     that exact absolute path; path params, query keys and the JSON body
	//     object become kwargs (see MethodAlias for custom Bind/Render).
	// A UsesPathID method must include its own ":id" segment, which feeds ids.
	AliasRoute string
	// Aliases declares further alias routes for the same method (same two forms as AliasRoute),
	// each optionally with its own request/response adapter — one method often backs several
	// external paths. A model may also declare {Name: "<standard method>", Aliases: ...} with a
	// nil Call to alias an inherited method (read, search_read, create, write, unlink, ...).
	Aliases []MethodAlias
	// AliasAnonymous mounts AliasRoute as an unauthenticated, raw-transport endpoint for callers
	// that cannot hold a session (e.g. a browser tracker on a third-party site). The alias handler
	// skips RPC auth and EnsureRuntimeModelAccess, answers CORS for any origin, and passes the raw
	// request to Call as kwargs[AliasRequestKwarg] (*AliasRequest). Call may return an AliasResponse
	// to control status/headers/body. Because no ACL runs, Call must self-guard (module install
	// state, input validation) and must not expose data. Only valid with ModelOnly methods (no :id).
	// Works with both route forms: a legacy route mounts at POST /base/<route>, a REST-form route
	// ("POST /device/provision") at its exact verb + path, for external contracts that are public.
	AliasAnonymous bool
	// AliasForwardAuthorization (AliasAnonymous only) hands the request's Authorization header to
	// the method as AliasRequest.Authorization. Off by default so an anonymous method can never
	// see a caller's session credential; turn it on only when the method verifies its own
	// non-session credential (edc device bearer token) and never logs the value.
	AliasForwardAuthorization bool
	// Params declares the kwargs this method reads (see runtime_method_params.go). When set,
	// ExecuteRuntimeMethod enforces required/type before Call, and the sys.model.action Actions
	// menu prompts the user for them. Nil keeps the free-form kwargs contract.
	Params []RuntimeMethodParam
}

// StandardRuntimeHooks lets models override built-in runtime method names
// without re-declaring the shared metadata flags for each standard method.
// This keeps model files focused on business logic while the ORM keeps
// ownership of the conventional method surface.
type StandardRuntimeHooks struct {
	DefaultGet    RuntimeMethodFunc
	Search        RuntimeMethodFunc
	Read          RuntimeMethodFunc
	SearchRead    RuntimeMethodFunc
	Create        RuntimeMethodFunc
	Write         RuntimeMethodFunc
	Unlink        RuntimeMethodFunc
	NameCreate    RuntimeMethodFunc
	NameSearch    RuntimeMethodFunc
	NameGet       RuntimeMethodFunc
	ReadGroup     RuntimeMethodFunc
	CopyData      RuntimeMethodFunc
	ToggleActive  RuntimeMethodFunc
	Onchange      RuntimeMethodFunc
	ViewHeaderGet RuntimeMethodFunc
}

// RuntimeMethods materializes the standard hook overrides into ordinary
// RuntimeMethod entries using the built-in metadata contract for each method.
func (hooks StandardRuntimeHooks) RuntimeMethods() []RuntimeMethod {
	// Keep the standard-name mapping centralized so model specs can provide
	// only behavior while the ORM preserves the canonical flags.
	type standardHookSpec struct {
		name string
		call RuntimeMethodFunc
	}
	specs := []standardHookSpec{
		{name: "default_get", call: hooks.DefaultGet},
		{name: "search", call: hooks.Search},
		{name: "read", call: hooks.Read},
		{name: "search_read", call: hooks.SearchRead},
		{name: "create", call: hooks.Create},
		{name: "write", call: hooks.Write},
		{name: "unlink", call: hooks.Unlink},
		{name: "name_create", call: hooks.NameCreate},
		{name: "name_search", call: hooks.NameSearch},
		{name: "name_get", call: hooks.NameGet},
		{name: "read_group", call: hooks.ReadGroup},
		{name: "copy_data", call: hooks.CopyData},
		{name: "toggle_active", call: hooks.ToggleActive},
		{name: "onchange", call: hooks.Onchange},
		{name: "view_header_get", call: hooks.ViewHeaderGet},
	}

	methods := make([]RuntimeMethod, 0, len(specs))
	for _, spec := range specs {
		if spec.call == nil {
			continue
		}
		base, ok := LookupRuntimeMethod(spec.name)
		if !ok {
			// Every supported standard hook must map to a built-in method. Skip
			// defensively here so startup does not panic on partial refactors.
			continue
		}
		base.Call = spec.call
		methods = append(methods, base)
	}
	return methods
}

// RuntimeMethodMetadata is the serializable view stored on registered model metadata.
type RuntimeMethodMetadata struct {
	Name       string `json:"name"`
	Public     bool   `json:"public"`
	ModelOnly  bool   `json:"model_only"`
	ReadOnly   bool   `json:"read_only"`
	UsesPathID bool   `json:"uses_path_id,omitempty"`
	AliasRoute string `json:"alias_route,omitempty"`
	// Description / DescriptionFile mirror the RuntimeMethod fields of the same name.
	Description     string `json:"description,omitempty"`
	DescriptionFile string `json:"description_file,omitempty"`
	// AliasRoutes lists the extra RuntimeMethod.Aliases routes (normalized) for metadata export.
	AliasRoutes []string `json:"alias_routes,omitempty"`
	// AliasAnonymous mirrors RuntimeMethod.AliasAnonymous for metadata export.
	AliasAnonymous bool `json:"alias_anonymous,omitempty"`
	// Params mirrors RuntimeMethod.Params (normalized) for metadata export.
	Params []RuntimeMethodParam `json:"params,omitempty"`
}

var runtimeMethodRegistry = struct {
	mu      sync.RWMutex
	methods map[string]RuntimeMethod
}{
	methods: map[string]RuntimeMethod{},
}

func init() {
	// Seed the package-level registry with the built-in runtime methods once at
	// process startup.
	for _, method := range defaultRuntimeMethods() {
		MustRegisterRuntimeMethod(method)
	}
}

// RegisterRuntimeMethod installs or replaces one default runtime method.
func RegisterRuntimeMethod(method RuntimeMethod) (RuntimeMethod, bool, error) {
	method.Name = strings.TrimSpace(method.Name)
	if method.Name == "" {
		log.Errorf("orm: runtime method requires a name")
		return RuntimeMethod{}, false, fmt.Errorf("orm: runtime method requires a name")
	}
	if method.Call == nil {
		log.Errorf("orm: runtime method %q requires a call function", method.Name)
		return RuntimeMethod{}, false, fmt.Errorf("orm: runtime method %q requires a call function", method.Name)
	}
	method.AliasRoute = normalizeAliasRoute(method.AliasRoute)
	for i := range method.Aliases {
		method.Aliases[i].Route = normalizeAliasRoute(method.Aliases[i].Route)
	}

	runtimeMethodRegistry.mu.Lock()
	defer runtimeMethodRegistry.mu.Unlock()

	previous, hadPrevious := runtimeMethodRegistry.methods[method.Name]
	runtimeMethodRegistry.methods[method.Name] = method
	return previous, hadPrevious, nil
}

// MustRegisterRuntimeMethod installs one runtime method and panics on invalid declarations.
func MustRegisterRuntimeMethod(method RuntimeMethod) {
	if _, _, err := RegisterRuntimeMethod(method); err != nil {
		panic(err)
	}
}

// LookupRuntimeMethod returns one registered default runtime method.
func LookupRuntimeMethod(name string) (RuntimeMethod, bool) {
	runtimeMethodRegistry.mu.RLock()
	defer runtimeMethodRegistry.mu.RUnlock()

	method, ok := runtimeMethodRegistry.methods[strings.TrimSpace(name)]
	return method, ok
}

// RuntimeMethodNames returns registered default runtime method names in deterministic order.
func RuntimeMethodNames() []string {
	runtimeMethodRegistry.mu.RLock()
	defer runtimeMethodRegistry.mu.RUnlock()

	names := make([]string, 0, len(runtimeMethodRegistry.methods))
	for name := range runtimeMethodRegistry.methods {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// defaultRuntimeMethodMap selects the runtime method map handled by this package.
// A lock guards the shared registry or worker state here so concurrent callers do not race on updates.
func defaultRuntimeMethodMap() map[string]RuntimeMethod {
	methods := make(map[string]RuntimeMethod, len(runtimeMethodRegistry.methods))

	runtimeMethodRegistry.mu.RLock()
	defer runtimeMethodRegistry.mu.RUnlock()

	for name, method := range runtimeMethodRegistry.methods {
		// Copy the current registry snapshot so callers can merge overrides
		// without mutating the shared global map.
		methods[name] = method
	}
	return methods
}

func mergeRuntimeMethods(overrides []RuntimeMethod) (map[string]RuntimeMethod, []RuntimeMethodMetadata, error) {
	// Start from the default registry, then let model-specific overrides replace
	// individual entries by name.
	methods := defaultRuntimeMethodMap()
	for _, method := range overrides {
		method.Name = strings.TrimSpace(method.Name)
		if method.Name == "" {
			log.Errorf("orm: runtime method override requires a name")
			return nil, nil, fmt.Errorf("orm: runtime method override requires a name")
		}
		// An alias-only entry (nil Call) attaches routes to a method the model already has — a
		// built-in default or a StandardHooks override merged earlier — keeping its body and
		// flags. Only an unknown name with no body is a declaration error.
		if method.Call == nil {
			inherited, ok := methods[method.Name]
			if !ok || (method.AliasRoute == "" && len(method.Aliases) == 0) {
				log.Errorf("orm: runtime method override %q requires a call function", method.Name)
				return nil, nil, fmt.Errorf("orm: runtime method override %q requires a call function", method.Name)
			}
			if method.AliasRoute != "" {
				inherited.AliasRoute = method.AliasRoute
			}
			inherited.Aliases = append(append([]MethodAlias(nil), inherited.Aliases...), method.Aliases...)
			// Keep the override's docs; otherwise the inherited method silently drops them.
			if method.Description != "" {
				inherited.Description = method.Description
			}
			if method.DescriptionFile != "" {
				inherited.DescriptionFile = method.DescriptionFile
			}
			method = inherited
		}
		method.AliasRoute = normalizeAliasRoute(method.AliasRoute)
		for i := range method.Aliases {
			method.Aliases[i].Route = normalizeAliasRoute(method.Aliases[i].Route)
		}
		// Reject malformed param declarations at registration so a bad one fails boot loudly.
		params, err := normalizeRuntimeMethodParams(method.Name, method.Params)
		if err != nil {
			log.Errorf("%v", err)
			return nil, nil, err
		}
		method.Params = params
		methods[method.Name] = method
	}

	names := make([]string, 0, len(methods))
	for name := range methods {
		names = append(names, name)
	}
	// Sort for deterministic metadata output and stable test expectations.
	sort.Strings(names)

	metadata := make([]RuntimeMethodMetadata, 0, len(names))
	for _, name := range names {
		method := methods[name]
		// Strip executable function bodies from the serializable metadata view.
		metadata = append(metadata, RuntimeMethodMetadata{
			Name:            method.Name,
			Public:          method.Public,
			ModelOnly:       method.ModelOnly,
			ReadOnly:        method.ReadOnly,
			UsesPathID:      method.UsesPathID,
			AliasRoute:      method.AliasRoute,
			Description:     method.Description,
			DescriptionFile: method.DescriptionFile,
			AliasRoutes:     methodAliasRoutes(method.Aliases),
			// Exported so the API doc / catalog can flag the alias as unauthenticated.
			AliasAnonymous: method.AliasAnonymous,
			Params:         method.Params,
		})
	}
	return methods, metadata, nil
}

// AliasRequestKwarg is the kwargs key an AliasAnonymous method reads its *AliasRequest from.
// A JSON-RPC caller cannot forge it: decoded JSON is never a *AliasRequest, so the type
// assertion fails and the method can reject non-alias invocations.
const AliasRequestKwarg = "alias_request"

// AliasRequest is the raw transport view handed to an AliasAnonymous method.
type AliasRequest struct {
	Body      []byte
	UserAgent string
	// IP is the proxy-aware client IP as resolved by the HTTP server (trusted X-Forwarded-For).
	IP string
	// Headers holds the first value of each request header, keyed by lowercase name; credential
	// headers (Authorization, Cookie) are never copied in.
	Headers map[string]string
	// Authorization is the raw Authorization header, set only for methods that opt in with
	// RuntimeMethod.AliasForwardAuthorization.
	Authorization string
	// Params holds the REST route's ":param" values (":serial" -> "serial"); nil for legacy routes.
	Params map[string]string
}

// AliasRequestFromKwargs returns the transport request when the method was invoked through an
// anonymous alias route, and false for any other caller (RPC, MCP, internal dispatch).
func AliasRequestFromKwargs(kwargs map[string]any) (*AliasRequest, bool) {
	request, ok := kwargs[AliasRequestKwarg].(*AliasRequest)
	return request, ok && request != nil
}

// AliasResponse lets an AliasAnonymous method write a plain HTTP response instead of the
// default JSON-RPC {"result": ...} envelope. Status 0 means 200.
type AliasResponse struct {
	Status  int
	Headers map[string]string
	Body    string
}

// MethodAlias is one extra alias route of a runtime method (RuntimeMethod.Aliases). Route uses
// the AliasRoute grammar. Bind/Render/Error only apply to the REST form ("VERB /path"); nil
// keeps the default REST contract (params + query + body object as kwargs, 200 {"result"}).
type MethodAlias struct {
	Route string
	// Anonymous mounts just this REST-form route without a session (same contract as
	// RuntimeMethod.AliasAnonymous: raw *AliasRequest in kwargs, Call self-guards), leaving the
	// method's other routes authenticated. Not valid for UsesPathID methods; Bind/Render/Error
	// are ignored — Call returns an AliasResponse.
	Anonymous bool
	// ForwardAuthorization (Anonymous only) is RuntimeMethod.AliasForwardAuthorization for this route.
	ForwardAuthorization bool
	// Bind maps the request into call.IDs/Args/Kwargs/Context.
	Bind func(call *AliasCall) error
	// Render turns the method result into an HTTP status and JSON body.
	Render func(call *AliasCall, result any) (int, any, error)
	// Error maps a Bind/method/Render failure; status 0 falls back to the transport default.
	Error func(call *AliasCall, err error) (int, any)
	// BodyExample is the sample JSON request body shown in the Alias API docs for body-carrying
	// verbs (POST/PUT/PATCH). Doc-only: nil lets the doc generate one from the method params.
	BodyExample any
}

// AliasCall is the transport-neutral view of one REST-form alias request. The transport strips
// the ORM's environment-override kwargs ("sudo", "uid", "context") from Kwargs before dispatch
// and applies only the server-built Context, so client input copied into Kwargs can't escalate.
type AliasCall struct {
	Ctx context.Context
	// Params holds the path params named in the route (":serial" -> "serial").
	Params map[string]string
	// Query holds the first value of each query-string key.
	Query map[string]string
	// Body is a copy of the raw request body.
	Body    []byte
	IDs     []string
	Args    []any
	Kwargs  map[string]any
	Context map[string]any
	// Call runs another runtime method as the same caller behind the same ACL check and with the
	// same Context (count, re-read after write, relations).
	Call func(model, method string, ids []string, kwargs map[string]any) (any, error)
}

// JSONObject decodes Body as a JSON object; an empty body is an empty object and a malformed one
// is a 400 invalid_json AliasError.
func (c *AliasCall) JSONObject() (map[string]any, error) {
	out := map[string]any{}
	if c == nil || strings.TrimSpace(string(c.Body)) == "" {
		return out, nil
	}
	if err := json.Unmarshal(c.Body, &out); err != nil {
		return nil, &AliasError{Status: 400, Code: "invalid_json", Message: err.Error()}
	}
	return out, nil
}

// AliasError is a REST-form alias failure with an explicit status and machine code, rendered as
// {"error": Code, "message": Message}.
type AliasError struct {
	Status  int
	Code    string
	Message string
}

// Error implements error.
func (e *AliasError) Error() string { return e.Message }

// AliasReservedKwargs are the kwargs runtimeContextWithEnvironment turns into environment
// overrides; REST-form alias callers must never control them.
var AliasReservedKwargs = []string{"sudo", "uid", "context"}

// clientReservedContextKeys are environment context entries derived from the authenticated
// identity (auth mode, role, groups, company authorization, DB binding), plus the server-only
// mode flags `install_mode` (seed import: skips business guards such as the currency
// deactivation check) and `readonly_env` (report rendering). A client-supplied context may
// carry UI hints (lang, tz, allowed_company_ids, active_id, ...) but never these.
var clientReservedContextKeys = []string{
	RBACContextLoginKey, RBACContextRoleIDKey, RBACContextRoleKey, RBACContextAuthModeKey,
	RBACContextGroupIDsKey, ContextKeyAuthorizedCompanyIDs, ContextKeyDatabaseName,
	ContextKeyInstallMode, ReadOnlyEnvContextKey,
}

// ContextKeyInstallMode marks a master-data/seed import environment. Only server code sets it.
const ContextKeyInstallMode = "install_mode"

// SanitizeClientKwargs returns a copy of kwargs that is safe to take from an untrusted caller
// (/rpc body, MCP arguments): the ORM's `sudo` and `uid` environment overrides are dropped and
// identity-derived keys are removed from a nested `context` map. runtimeContextWithEnvironment
// applies those kwargs after the model ACL check, so leaving them in would let any caller with
// model access escalate to sudo, rebind the user, or rewrite its own group/company scope.
func SanitizeClientKwargs(kwargs map[string]any) map[string]any {
	if kwargs == nil {
		return nil
	}
	out := make(map[string]any, len(kwargs))
	for k, v := range kwargs {
		out[k] = v
	}
	delete(out, "sudo")
	delete(out, "uid")
	if raw, ok := out["context"].(map[string]any); ok {
		ctxCopy := make(map[string]any, len(raw))
		for k, v := range raw {
			ctxCopy[k] = v
		}
		for _, key := range clientReservedContextKeys {
			delete(ctxCopy, key)
		}
		out["context"] = ctxCopy
	} else if _, present := out["context"]; present {
		delete(out, "context")
	}
	return out
}

// ErrInternalMethodNotCallable is returned to client transports for an Internal method; it reads
// like a lookup miss on purpose.
func ErrInternalMethodNotCallable(model, method string) error {
	return fmt.Errorf("orm: runtime method %q is not registered on model %q", method, model)
}

// aliasRouteVerbs are the verbs accepted as the REST-form prefix of an alias route.
var aliasRouteVerbs = map[string]bool{"GET": true, "POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// ParseAliasRoute splits a normalized alias route. REST form ("GET /base/x/:id") returns the verb,
// the absolute path and rest=true; the legacy form ("orders/:id/cancel") returns POST,
// "/base/<route>" and rest=false. An empty route returns path "".
func ParseAliasRoute(route string) (verb, path string, rest bool) {
	route = strings.TrimSpace(route)
	if route == "" {
		return "", "", false
	}
	if head, tail, ok := strings.Cut(route, " "); ok && aliasRouteVerbs[strings.ToUpper(head)] {
		return strings.ToUpper(head), "/" + strings.Trim(strings.TrimSpace(tail), "/"), true
	}
	return "POST", "/base/" + strings.Trim(route, "/"), false
}

// normalizeAliasRoute canonicalizes a declared alias route. Legacy routes drop their
// leading/trailing slashes ("/orders/create" == "orders/create", mounted under "/base/");
// REST-form routes become "VERB /absolute/path".
func normalizeAliasRoute(route string) string {
	verb, path, rest := ParseAliasRoute(route)
	if rest {
		return verb + " " + path
	}
	return strings.Trim(strings.TrimSpace(route), "/")
}

// methodAliasRoutes lists the Route of each extra alias for metadata export.
func methodAliasRoutes(aliases []MethodAlias) []string {
	if len(aliases) == 0 {
		return nil
	}
	routes := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		routes = append(routes, alias.Route)
	}
	return routes
}

// cloneRuntimeMethodMap clones the runtime method map handled by this package.
func cloneRuntimeMethodMap(methods map[string]RuntimeMethod) map[string]RuntimeMethod {
	if len(methods) == 0 {
		return nil
	}

	// Shallow copy is enough because RuntimeMethod only contains immutable data
	// plus a function pointer.
	cloned := make(map[string]RuntimeMethod, len(methods))
	for name, method := range methods {
		cloned[name] = method
	}
	return cloned
}
