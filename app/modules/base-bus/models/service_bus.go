package models

// File overview:
// - Responsibility: defines the base-bus module's runtime model metadata and server-side behavior for the durable service-bus event log.
// - Key symbols: `ServiceBusGlobalChannel`, `ServiceBusRecord`.
// - Pattern: Leans on metadata-driven registration so storage mapping, runtime methods, and behavior hooks stay centralized instead of being scattered across handlers.
// - Pattern: Threads `context.Context` through the main execution paths so cancellation, auth scope, and transaction state can follow the work end to end.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/mqtt"
	busqueries "git.thebase.vn/base_hub/modules/base-bus/queries"
	"github.com/uptrace/bun"
)

// ServiceBusRecord stores one durable service-bus event so frontend clients can
// recover after reconnects and operators can inspect recent traffic.
type ServiceBusRecord struct {
	orm.BaseModel
	Channel   string         `bun:"channel" json:"channel" meta:"index,label:Channel,help:Stores the channel value for this record."`
	EventType string         `bun:"event_type" json:"event_type" meta:"index,label:Event Type,help:Stores the event type value for this record."`
	Payload   map[string]any `bun:"payload,type:jsonb,nullzero" json:"payload" meta:"kind:json,label:Payload,help:Stores the payload payload in JSON format."`
	CreatedBy int64          `bun:"created_by" json:"created_by" meta:"index,label:Created By,help:Stores the numeric value for created by."`
	CreatedAt time.Time      `bun:"created_at" json:"created_at" meta:"index,label:Created At,help:Stores the date and time for created at."`
}

const ServiceBusModelName = "base.service.bus"

// ServiceBusModelSpec stays exported: tests inspect its runtime methods.
var ServiceBusModelSpec = orm.ModelSpec{
	Name:        ServiceBusModelName,
	Label:       "Service Bus",
	Description: "Durable frontend/backend service bus event log",
	RuntimeMethods: []orm.RuntimeMethod{
		// publish is server-side only (plan T16): Go services, Lua and workflow steps fan events out
		// to other users' channels, so client transports must not reach it. The MCP bus.publish
		// tool checks EnsureServiceBusPublishAllowed and then calls it server-side.
		{Name: "publish", Internal: true, ModelOnly: true, Call: publishServiceBus},
		{Name: "poll", Public: true, ModelOnly: true, ReadOnly: true, Call: pollServiceBus},
		{Name: "recent", Public: true, ModelOnly: true, ReadOnly: true, Call: recentServiceBus},
		{Name: "last_id", Public: true, ModelOnly: true, ReadOnly: true, Call: lastIDServiceBus},
		{Name: "get_default_channels", Public: true, ModelOnly: true, ReadOnly: true, Call: defaultChannelsServiceBus},
	},
}

// ServiceBusModel owns the durable service-bus event stream exposed to the
// polling frontend transport and to operator tooling.
var ServiceBusModel = orm.Define[ServiceBusRecord](&Models, 300, ServiceBusModelSpec)

// ServiceBusNotifyChannel is the Postgres LISTEN/NOTIFY channel used to push
// wake-up signals to every backend instance's WebSocket stream handler when
// the MQTT transport is not active. The NOTIFY payload only carries the
// base_service_bus channel name (not the row itself) — recipients re-poll
// `base_service_bus` for anything newer than their own cursor, so this is
// purely a low-latency "something changed, go check" signal, not a delivery
// guarantee (Postgres NOTIFY is fire-and-forget and drops payloads for
// listeners that aren't currently connected; the row itself is the durable
// source of truth, matching the existing polling transport's semantics).
const ServiceBusNotifyChannel = "service_bus_channel"

func ServiceBusGlobalChannel() string {
	return "servicebus:global"
}

func ServiceBusUserChannel(userID int64) string {
	return fmt.Sprintf("servicebus:user:%d", userID)
}

func ServiceBusDefaultChannels(userID int64) []string {
	channels := []string{ServiceBusGlobalChannel()}
	if userID > 0 {
		channels = append(channels, ServiceBusUserChannel(userID))
	}
	return channels
}

func NormalizeServiceBusChannels(raw any, userID int64, includeDefaults bool) []string {
	seen := map[string]struct{}{}
	channels := make([]string, 0, 8)

	addChannel := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		channels = append(channels, value)
	}

	var visit func(any)
	visit = func(value any) {
		switch typed := value.(type) {
		case nil:
			return
		case string:
			addChannel(typed)
		case []string:
			for _, entry := range typed {
				addChannel(entry)
			}
		case []any:
			parts := make([]string, 0, len(typed))
			allScalar := true
			for _, entry := range typed {
				switch scalar := entry.(type) {
				case string:
					trimmed := strings.TrimSpace(scalar)
					if trimmed != "" {
						parts = append(parts, trimmed)
					}
				case int:
					parts = append(parts, strconv.Itoa(scalar))
				case int64:
					parts = append(parts, strconv.FormatInt(scalar, 10))
				case float64:
					parts = append(parts, strconv.FormatInt(int64(scalar), 10))
				default:
					allScalar = false
				}
			}
			if allScalar && len(parts) > 0 {
				addChannel(strings.Join(parts, ":"))
				return
			}
			for _, entry := range typed {
				visit(entry)
			}
		default:
			addChannel(strings.TrimSpace(fmt.Sprint(typed)))
		}
	}

	visit(raw)
	if includeDefaults {
		// Walk each servicebusdefaultchannels(userid) entry in order so validation and side effects stay scoped to the current element.
		for _, channel := range ServiceBusDefaultChannels(userID) {
			addChannel(channel)
		}
	}
	sort.Strings(channels)
	return channels
}

func publishServiceBus(ctx context.Context, model *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	if len(args) < 3 {
		log.Errorf("base.service.bus.publish requires channels, event_type, and payload")
		return nil, orm.InvalidParamsf("base.service.bus.publish requires channels, event_type, and payload")
	}
	env := orm.EnvironmentFromContext(ctx)
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}

	channels := NormalizeServiceBusChannels(args[0], userID, false)
	if len(channels) == 0 {
		log.Errorf("base.service.bus.publish requires at least one channel")
		return nil, orm.InvalidParamsf("base.service.bus.publish requires at least one channel")
	}
	eventType := strings.TrimSpace(fmt.Sprint(args[1]))
	if eventType == "" {
		log.Errorf("base.service.bus.publish event_type is required")
		return nil, orm.InvalidParamsf("base.service.bus.publish event_type is required")
	}
	payload, err := anyToServiceBusPayload(args[2])
	if err != nil {
		return nil, err
	}

	// MQTT is the push leg only when browser-facing MQTT is configured;
	// otherwise NOTIFY wakes the /bus/ws and /bus/sse listeners.
	mqttActive := config.Get().MqttClient.Enable && strings.TrimSpace(config.Get().MqttClient.PublicWSURL) != ""
	createdAt := time.Now().UTC()
	rows := make([]busqueries.ServiceBusRow, 0, len(channels))
	err = runServiceBusPublishTx(ctx, func(txCtx context.Context) error {
		for _, channel := range channels {
			created, createErr := orm.RuntimeRepositoryCreate(txCtx, orm.RuntimeModelRef(ServiceBusModelName, model), orm.RecordMap{
				"channel":    channel,
				"event_type": eventType,
				"payload":    payload,
				"created_by": userID,
				"created_at": createdAt,
			})
			if createErr != nil {
				return createErr
			}
			createdID, _ := int64FromServiceBusAny(created["id"])
			rows = append(rows, busqueries.ServiceBusRow{
				ID: createdID, Channel: channel, EventType: eventType, Payload: payload, CreatedBy: userID, CreatedAt: createdAt,
			})
		}
		if mqttActive {
			return nil
		}
		// NOTIFY in the same transaction as the inserts: Postgres delivers it
		// only on commit, so a woken listener always finds the row, and a
		// rolled-back publish wakes nobody (plan T70, BUG-M-17).
		return notifyServiceBusChannels(txCtx, channels)
	})
	if err != nil {
		return nil, err
	}

	createdIDs := make([]int64, 0, len(rows))
	for _, row := range rows {
		if row.ID > 0 {
			createdIDs = append(createdIDs, row.ID)
		}
	}
	if mqttActive {
		// Joined a caller's transaction: queued until it commits, dropped on
		// rollback. Owned transaction: already committed, so this runs now.
		orm.AfterCommit(ctx, func(context.Context) {
			for _, row := range rows {
				publishServiceBusMQTT(row.Channel, serviceBusRowMap(row))
			}
		})
	}
	return createdIDs, nil
}

// publishServiceBusMQTT is swappable in tests (no broker there).
var publishServiceBusMQTT = PublishServiceBusMQTT

// runServiceBusPublishTx runs fn in the caller's transaction when one is
// bound, otherwise in a transaction this method owns, so the inserts and
// their NOTIFY always commit or roll back together.
func runServiceBusPublishTx(ctx context.Context, fn func(context.Context) error) error {
	db, ok := orm.DBFromContext(ctx).(*bun.DB)
	if !ok || db == nil {
		// Already a transaction (or an unmanaged handle in tests): its
		// commit is what fires the NOTIFY.
		return fn(ctx)
	}
	env := orm.EnvironmentFromContext(ctx)
	if env == nil {
		// Bind a system environment on db first: the after-commit scope flushes
		// on the environment's DB, and without one queued callbacks would keep
		// the committed tx handle (and no DB name).
		env = orm.NewEnvironment(db, 0, nil, false)
		ctx = orm.ContextWithEnvironment(ctx, env)
	}
	// Own scope: after-commit work of the inserts (record events) must wait
	// for this transaction, not leak into an outer caller's queue.
	return orm.WithAfterCommitScope(ctx, func(scopeCtx context.Context) error {
		return db.RunInTx(scopeCtx, nil, func(txCtx context.Context, tx bun.Tx) error {
			return fn(orm.ContextWithEnvironment(txCtx, env.WithDB(tx)))
		})
	})
}

// notifyServiceBusChannels sends one wake-up per channel on ctx's DB handle.
// An error aborts the publish: inside a transaction a failed statement
// poisons it anyway, so logging and carrying on would only hide the failure.
func notifyServiceBusChannels(ctx context.Context, channels []string) error {
	db := orm.DBFromContext(ctx)
	if db == nil {
		return nil
	}
	for _, channel := range channels {
		if _, err := db.ExecContext(ctx, "SELECT pg_notify(?, ?)", ServiceBusNotifyChannel, channel); err != nil {
			return fmt.Errorf("base.service.bus.publish: pg_notify channel %q: %w", channel, err)
		}
	}
	return nil
}

func pollServiceBus(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	if len(args) == 0 {
		return []map[string]any{}, nil
	}
	env := orm.EnvironmentFromContext(ctx)
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	channels := AuthorizeServiceBusChannels(ctx, NormalizeServiceBusChannels(args[0], userID, true))
	if len(channels) == 0 {
		return []map[string]any{}, nil
	}
	lastID, _ := int64FromServiceBusAny(argAtServiceBus(args, 1))
	rows, _, err := PollServiceBusRows(ctx, channels, lastID)
	return rows, err
}

// PollServiceBusRows reads one page (busqueries.ServiceBusPollLimit) of rows
// newer than lastID on already-authorized channels. held reports rows kept
// back behind an id that may still commit; push streams use it to re-check
// soon even when no NOTIFY arrives.
func PollServiceBusRows(ctx context.Context, channels []string, lastID int64) ([]map[string]any, bool, error) {
	now := time.Now().UTC()
	rows, held, err := busqueries.PollServiceBusPage(
		ctx,
		orm.PGXPoolFromContext(ctx),
		channels,
		lastID,
		now.Add(-5*time.Second),
		now,
		busqueries.ServiceBusPollLimit,
	)
	if err != nil {
		return nil, false, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, serviceBusRowMap(row))
	}
	return out, held, nil
}

func recentServiceBus(ctx context.Context, _ *orm.ModelMetadata, _ []string, args []any, _ map[string]any) (any, error) {
	env := orm.EnvironmentFromContext(ctx)
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	channels := AuthorizeServiceBusChannels(ctx, NormalizeServiceBusChannels(argAtServiceBus(args, 0), userID, true))
	if len(channels) == 0 {
		return []map[string]any{}, nil
	}
	limit, _ := int64FromServiceBusAny(argAtServiceBus(args, 1))
	if limit <= 0 {
		limit = 50
	}

	rows, err := busqueries.RecentServiceBus(ctx, orm.PGXPoolFromContext(ctx), channels, limit)
	if err != nil {
		return nil, err
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, serviceBusRowMap(row))
	}
	return out, nil
}

func lastIDServiceBus(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	return busqueries.LastServiceBusID(ctx, orm.PGXPoolFromContext(ctx))
}

// defaultChannelsServiceBus selects the channels service bus handled by this package.
func defaultChannelsServiceBus(ctx context.Context, _ *orm.ModelMetadata, _ []string, _ []any, _ map[string]any) (any, error) {
	env := orm.EnvironmentFromContext(ctx)
	userID := int64(0)
	if env != nil {
		userID = env.UserID()
	}
	return ServiceBusDefaultChannels(userID), nil
}

func serviceBusRowMap(row busqueries.ServiceBusRow) map[string]any {
	return map[string]any{
		"id":         row.ID,
		"channel":    row.Channel,
		"event_type": row.EventType,
		"payload":    row.Payload,
		"created_by": row.CreatedBy,
		"created_at": row.CreatedAt,
	}
}

func anyToServiceBusPayload(raw any) (map[string]any, error) {
	switch typed := raw.(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return cloneServiceBusPayload(typed), nil
	case orm.RecordMap:
		return cloneServiceBusPayload(map[string]any(typed)), nil
	default:
		log.Errorf("base.service.bus payload must be an object")
		return nil, orm.InvalidParamsf("base.service.bus payload must be an object")
	}
}

// cloneServiceBusPayload clones the service bus payload handled by this package.
func cloneServiceBusPayload(in map[string]any) map[string]any {
	if in == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func int64FromServiceBusAny(raw any) (int64, bool) {
	switch typed := raw.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return 0, false
		}
		value, err := strconv.ParseInt(trimmed, 10, 64)
		if err != nil {
			return 0, false
		}
		return value, true
	default:
		return 0, false
	}
}

func argAtServiceBus(args []any, index int) any {
	if index < 0 || index >= len(args) {
		return nil
	}
	return args[index]
}

// PublishServiceBusMQTT publishes one service-bus row to the MQTT broker at a
// topic matching the channel name, QoS 1 (at-least-once) per the service-bus
// transport contract — the row itself (via base_service_bus) stays the
// durable source of truth, this is only the real-time push leg. Best-effort:
// a publish failure here does not roll back the already-durable DB row,
// matching the existing pg_notify best-effort semantics.
func PublishServiceBusMQTT(channel string, row map[string]any) {
	payload, err := marshalServiceBusMQTTPayload(row)
	if err != nil {
		log.Warnf("base.service.bus.publish: encode mqtt payload for channel %q: %v", channel, err)
		return
	}
	if _, err := mqtt.MqttPublish(channel, payload); err != nil {
		log.Warnf("base.service.bus.publish: mqtt publish channel %q: %v", channel, err)
	}
}

// marshalServiceBusMQTTPayload encodes a service-bus row into the same flat
// {id, channel, event_type, payload, created_by, created_at} JSON shape the
// WebSocket transport already sends, so frontend clients decode both
// transports identically.
func marshalServiceBusMQTTPayload(row map[string]any) (string, error) {
	encoded, err := json.Marshal(row)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}
