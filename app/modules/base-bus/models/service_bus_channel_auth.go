package models

// Channel authorization for the service bus (plan T16).
//
// Channel names are chosen by the client on every subscribe path (/bus/subscribe, peek, /bus/ws,
// /bus/sse, the poll/recent runtime methods and their MCP tools), so the server decides which of
// them the caller may read. Granted: the global channel, the caller's own
// servicebus:user:<id> channel, and channels a module vouches for through
// RegisterServiceBusChannelAuthorizer (discuss user/channel membership, workflow run topics).
// Everything else is dropped. Sudo environments (server-side code, superadmins) read any channel.
//
// Publishing is not decided here: `publish` is an Internal method (server-side callers only) and
// the MCP bus.publish tool requires base.group_system (see EnsureServiceBusPublishAllowed).

import (
	"context"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
)

// ServiceBusChannelAuthorizer reports whether userID may receive events on channel. It runs under
// the caller's context (tenant DB, ACL environment). An error denies the channel.
type ServiceBusChannelAuthorizer func(ctx context.Context, userID int64, channel string) (bool, error)

var (
	serviceBusChannelAuthorizersMu sync.RWMutex
	serviceBusChannelAuthorizers   = map[string]ServiceBusChannelAuthorizer{}
)

// RegisterServiceBusChannelAuthorizer lets a module vouch for channels starting with prefix (for
// example "discuss:channel:"). Call it from the module's init(). The longest matching prefix wins.
func RegisterServiceBusChannelAuthorizer(prefix string, fn ServiceBusChannelAuthorizer) {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" || fn == nil {
		panic("base-bus: channel authorizer needs a prefix and a function")
	}
	serviceBusChannelAuthorizersMu.Lock()
	defer serviceBusChannelAuthorizersMu.Unlock()
	serviceBusChannelAuthorizers[prefix] = fn
}

// lookupServiceBusChannelAuthorizer returns the authorizer with the longest prefix matching channel.
func lookupServiceBusChannelAuthorizer(channel string) (string, ServiceBusChannelAuthorizer) {
	serviceBusChannelAuthorizersMu.RLock()
	defer serviceBusChannelAuthorizersMu.RUnlock()
	bestPrefix := ""
	var best ServiceBusChannelAuthorizer
	for prefix, fn := range serviceBusChannelAuthorizers {
		if strings.HasPrefix(channel, prefix) && len(prefix) > len(bestPrefix) {
			bestPrefix, best = prefix, fn
		}
	}
	return bestPrefix, best
}

// AuthorizeServiceBusChannels returns the subset of channels the caller in ctx may receive, in
// the input order. A nil environment is treated as an anonymous caller (global channel only).
func AuthorizeServiceBusChannels(ctx context.Context, channels []string) []string {
	env := orm.EnvironmentFromContext(ctx)
	if env != nil && env.IsSudo() {
		return channels
	}
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	out := make([]string, 0, len(channels))
	for _, channel := range channels {
		if serviceBusChannelAllowed(ctx, userID, channel) {
			out = append(out, channel)
		} else {
			// Debug, not Warn: peek clients resend their channels on every poll and a stream
			// whose membership was revoked is denied again on every wake-up.
			log.Debugf("base.service.bus: user %d denied channel %q", userID, channel)
		}
	}
	return out
}

func serviceBusChannelAllowed(ctx context.Context, userID int64, channel string) bool {
	if channel == ServiceBusGlobalChannel() {
		return true
	}
	if rest, ok := strings.CutPrefix(channel, "servicebus:user:"); ok {
		return userID > 0 && ServiceBusOwnUserChannel(userID, rest)
	}
	prefix, authorize := lookupServiceBusChannelAuthorizer(channel)
	if authorize == nil {
		return false
	}
	key := serviceBusAuthCacheKey{db: serviceBusEnvDBName(ctx), userID: userID, channel: channel}
	if allowed, ok := serviceBusAuthCacheGet(key); ok {
		return allowed
	}
	allowed, err := authorize(ctx, userID, channel)
	if err != nil {
		// Errors are not cached: a transient DB failure must not deny the channel for the TTL.
		log.Warnf("base.service.bus: authorizer %q for channel %q: %v", prefix, channel, err)
		return false
	}
	serviceBusAuthCachePut(key, allowed)
	return allowed
}

// Module authorizer decisions are cached briefly: every NOTIFY wakes every open /bus/ws and
// /bus/sse stream, and each wake re-polls (and so re-authorizes) all of its channels, which
// would otherwise cost one or more queries per hooked channel per stream per event. A revoked
// membership therefore stops delivery within serviceBusAuthCacheTTL instead of immediately.
const serviceBusAuthCacheTTL = 15 * time.Second

type serviceBusAuthCacheKey struct {
	db      string
	userID  int64
	channel string
}

type serviceBusAuthCacheEntry struct {
	allowed bool
	expires time.Time
}

var (
	serviceBusAuthCacheMu sync.Mutex
	serviceBusAuthCache   = map[serviceBusAuthCacheKey]serviceBusAuthCacheEntry{}
)

func serviceBusAuthCacheGet(key serviceBusAuthCacheKey) (bool, bool) {
	serviceBusAuthCacheMu.Lock()
	defer serviceBusAuthCacheMu.Unlock()
	entry, ok := serviceBusAuthCache[key]
	if !ok || time.Now().After(entry.expires) {
		return false, false
	}
	return entry.allowed, true
}

func serviceBusAuthCachePut(key serviceBusAuthCacheKey, allowed bool) {
	now := time.Now()
	serviceBusAuthCacheMu.Lock()
	defer serviceBusAuthCacheMu.Unlock()
	// Bound memory without a sweeper goroutine: drop expired entries once the map grows.
	if len(serviceBusAuthCache) >= 4096 {
		for k, entry := range serviceBusAuthCache {
			if now.After(entry.expires) {
				delete(serviceBusAuthCache, k)
			}
		}
	}
	serviceBusAuthCache[key] = serviceBusAuthCacheEntry{allowed: allowed, expires: now.Add(serviceBusAuthCacheTTL)}
}

func serviceBusEnvDBName(ctx context.Context) string {
	if env := orm.EnvironmentFromContext(ctx); env != nil {
		return env.DBName()
	}
	return ""
}

// ServiceBusOwnUserChannel reports whether the id suffix of a per-user channel names userID.
// Modules use it for their own "<module>:user:<id>" channels.
func ServiceBusOwnUserChannel(userID int64, idSuffix string) bool {
	id, err := strconv.ParseInt(strings.TrimSpace(idSuffix), 10, 64)
	return err == nil && userID > 0 && id == userID
}

// serviceBusPublisherGroupBID may publish arbitrary events through client transports (MCP).
const serviceBusPublisherGroupBID = "base.group_system"

// Test seam over the kernel helper that needs the auth database.
var serviceBusCallerGroupBIDs = orm.CallerGroupBIDs

// EnsureServiceBusPublishAllowed guards client-transport publishing (the MCP bus.publish tool):
// events land on other users' channels and the global channel (toasts, menu reloads), so only
// sudo or base.group_system callers may publish. Server-side Go/Lua/workflow code calls the
// Internal `publish` method directly and is not affected.
func EnsureServiceBusPublishAllowed(ctx context.Context) error {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		return orm.Forbiddenf("permission denied: publishing on the service bus needs an authenticated caller")
	}
	if env.IsSudo() {
		return nil
	}
	groups, err := serviceBusCallerGroupBIDs(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(groups, serviceBusPublisherGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can publish on the service bus", serviceBusPublisherGroupBID)
	}
	return nil
}

func init() {
	orm.RegisterAccessHandler(ServiceBusModelName, serviceBusLogAccess)
}

// Test seam over the kernel default ACL check.
var serviceBusDefaultAccess = orm.EnsureDefaultModelAccess

// serviceBusLogAccess limits generic record access to base.service.bus (read/search_read through
// /rpc and the bus.record.* MCP tools) to base.group_system: the rows span every user's channels,
// so the event log itself is an operator view. Users receive their own events through the
// channel-filtered poll/recent methods and push streams. Sudo, nil env and callers without an auth
// mode keep the kernel default, matching ensureRBACModelAccess.
func serviceBusLogAccess(ctx context.Context, env *orm.Environment, model *orm.ModelMetadata, operation string, ids []string, values orm.RecordMap) error {
	if err := serviceBusDefaultAccess(ctx, env, model, operation, ids, values); err != nil {
		return err
	}
	if env == nil || env.IsSudo() {
		return nil
	}
	if _, ok := env.ContextValue(orm.RBACContextAuthModeKey); !ok {
		return nil
	}
	groups, err := serviceBusCallerGroupBIDs(orm.ContextWithEnvironment(ctx, env))
	if err != nil {
		return err
	}
	if !slices.Contains(groups, serviceBusPublisherGroupBID) {
		return orm.Forbiddenf("permission denied: only %s can %s %s", serviceBusPublisherGroupBID, operation, ServiceBusModelName)
	}
	return nil
}
