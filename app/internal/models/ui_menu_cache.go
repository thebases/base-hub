package models

// File overview:
// - Responsibility: shared, cross-instance cache of the user-independent half of ui.menu.load_menus (active rows with their actions already resolved), stored in a base.kvt row.
// - Key symbols: `loadUIMenuResolvedRows`, `uiMenuSourceFingerprint`, `uiMenuCacheEntry`.
// - Pattern: the cache is validated by a fingerprint of the source tables read from the shared database on every request, so any instance sees any write (ORM, seed import, raw SQL) without a bus event, and a stale entry is never served.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"github.com/uptrace/bun"
)

const (
	// uiMenuKvtModelName is base.kvt (modules/base); internal/ must not import modules/*, so it is addressed by name.
	uiMenuKvtModelName = "base.kvt"
	// uiMenuKvtTypeTechnical is base.kvt's technical-only access tier, which keeps cache rows out of every normal user's and system admin's reach.
	uiMenuKvtTypeTechnical = 3
	// uiMenuCacheSchemaVersion invalidates stored entries when the cached row shape or the resolution logic changes.
	uiMenuCacheSchemaVersion = 1
)

// uiMenuCacheSources are the models whose rows feed action resolution: the
// menus themselves, both action kinds, and views (default/preferred view ids).
var uiMenuCacheSources = []string{UIMenuModelName, UIActionWindowModelName, UIActionClientModelName, UIViewModelName}

var uiMenuLangPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_@-]{0,15}$`)

// uiMenuCacheEntry is the jsonb value stored in base.kvt.
type uiMenuCacheEntry struct {
	Version     int             `json:"version"`
	Fingerprint string          `json:"fingerprint"`
	Rows        []uiMenuTreeRow `json:"rows"`
	// ParentsWithChildren lists every parent id that had a child before any row was dropped (see hadChildren).
	ParentsWithChildren []int64 `json:"parents_with_children"`
}

// uiMenuCacheWarnAt throttles cache-failure logs so a missing base.kvt table cannot flood the log on every menu load.
var uiMenuCacheWarnAt atomic.Int64

func warnUIMenuCache(format string, args ...any) {
	now := time.Now().Unix()
	last := uiMenuCacheWarnAt.Load()
	if now-last < 60 || !uiMenuCacheWarnAt.CompareAndSwap(last, now) {
		return
	}
	log.Warnf(format, args...)
}

// loadUIMenuResolvedRows returns every active menu row with its action
// resolved, plus the parents that had children. The result is identical for all
// users (the per-user gates run afterwards), so it is cached in base.kvt per
// language. Any cache problem, or anything that makes the rows differ per user,
// degrades to a per-caller build that never touches the cache.
func loadUIMenuResolvedRows(ctx context.Context) ([]uiMenuTreeRow, map[int64]bool, error) {
	env := orm.EnvironmentFromContext(ctx)
	if env == nil || env.DB() == nil {
		return buildUIMenuCallerRows(ctx)
	}
	if _, ok := orm.LookupModel(uiMenuKvtModelName); !ok {
		return buildUIMenuCallerRows(ctx)
	}
	// A code-registered record rule on ui.menu filters rows per caller, which a
	// shared sudo-built cache cannot honour.
	if orm.HasRecordRuleHandler(UIMenuModelName) {
		return buildUIMenuCallerRows(ctx)
	}
	// The fingerprint is taken before the build so a write that lands mid-build
	// leaves the stored entry already out of date and the next request rebuilds.
	fingerprint, menuRuleCount, err := uiMenuSourceFingerprint(ctx, env)
	if err != nil {
		warnUIMenuCache("ui.menu cache disabled for this load: fingerprint: %v", err)
		return buildUIMenuCallerRows(ctx)
	}
	// An active sys.rule on ui.menu likewise makes the visible rows per-caller.
	if menuRuleCount > 0 {
		return buildUIMenuCallerRows(ctx)
	}
	key := uiMenuCacheKvtName(env)
	sudoCtx := orm.ContextWithEnvironment(ctx, env.WithUser(env.UserID(), true))

	kvtID, entry, err := readUIMenuCacheEntry(sudoCtx, key)
	if err != nil {
		warnUIMenuCache("ui.menu cache read: %v", err)
	} else if entry != nil && entry.Version == uiMenuCacheSchemaVersion && entry.Fingerprint == fingerprint {
		return entry.Rows, hadChildrenFromParents(entry.ParentsWithChildren), nil
	}

	// Build as sudo so the cached content never depends on the filling caller's
	// own ACL; the caller's visibility is applied by the gates in fetchUIMenuRows.
	rows, hadChildren, err := buildUIMenuResolvedRows(sudoCtx)
	if err != nil {
		return nil, nil, err
	}
	writeUIMenuCacheEntry(sudoCtx, env, kvtID, key, uiMenuCacheEntry{
		Version:             uiMenuCacheSchemaVersion,
		Fingerprint:         fingerprint,
		Rows:                rows,
		ParentsWithChildren: parentsFromHadChildren(hadChildren),
	})
	return rows, hadChildren, nil
}

// uiMenuCacheKvtName scopes the entry by language: menu and action names are
// resolved to the environment language before they are cached.
func uiMenuCacheKvtName(env *orm.Environment) string {
	lang := "en"
	if raw, ok := env.ContextValue("lang"); ok {
		if value, _ := raw.(string); uiMenuLangPattern.MatchString(strings.TrimSpace(value)) {
			lang = strings.TrimSpace(value)
		}
	}
	return "ui.menu.resolved:" + lang
}

// uiMenuLargeTableRows is the estimated row count above which a source table
// is fingerprinted by write counters only: count(*)/max(updated_at) would scan
// it on every request.
const uiMenuLargeTableRows = 50000

// uiMenuLargeTablesTTL bounds how stale the row-count estimate may get.
const uiMenuLargeTablesTTL = 5 * time.Minute

type uiMenuLargeTables struct {
	at    time.Time
	large map[string]bool
}

// uiMenuLargeTablesByDB memoizes the size estimate per logical database.
var uiMenuLargeTablesByDB sync.Map

// uiMenuLargeTableSet returns which source tables are big, from pg_class
// estimates (free, no scan) refreshed at most every uiMenuLargeTablesTTL.
// A table never analyzed (reltuples -1) counts as small.
func uiMenuLargeTableSet(ctx context.Context, env *orm.Environment, storage []string) map[string]bool {
	if cached, ok := uiMenuLargeTablesByDB.Load(env.DBName()); ok {
		if entry := cached.(uiMenuLargeTables); time.Since(entry.at) < uiMenuLargeTablesTTL {
			return entry.large
		}
	}
	large := map[string]bool{}
	placeholders := make([]string, len(storage))
	args := make([]any, len(storage))
	for i, name := range storage {
		placeholders[i] = "to_regclass(?)"
		args[i] = name
	}
	var rows []struct {
		Name string `bun:"name"`
		N    int64  `bun:"n"`
	}
	query := "SELECT relname AS name, reltuples::bigint AS n FROM pg_class WHERE oid IN (" + strings.Join(placeholders, ",") + ")"
	// A failed estimate just means every table is treated as small (exact fingerprint).
	if err := env.DB().NewRaw(query, args...).Scan(ctx, &rows); err == nil {
		for _, row := range rows {
			if row.N > uiMenuLargeTableRows {
				large[row.Name] = true
			}
		}
	}
	uiMenuLargeTablesByDB.Store(env.DBName(), uiMenuLargeTables{at: time.Now(), large: large})
	return large
}

// uiMenuSourceFingerprint reads, per source table, the cumulative write counter
// from pg_stat_user_tables (inserts+updates+deletes: moves on any DML, including
// raw SQL that skips updated_at, and costs no scan) plus, for tables that are
// not large, an exact count and max(updated_at), which see ORM writes the
// instant they commit (pg stats may lag by a second or more). It also returns
// the number of active sys.rule rows targeting ui.menu. One round trip.
func uiMenuSourceFingerprint(ctx context.Context, env *orm.Environment) (string, int64, error) {
	storage := make([]string, len(uiMenuCacheSources))
	for i, name := range uiMenuCacheSources {
		meta, ok := orm.LookupModel(name)
		if !ok || strings.TrimSpace(meta.StorageName) == "" {
			return "", 0, fmt.Errorf("source model %q is not registered", name)
		}
		storage[i] = meta.StorageName
	}
	ruleMeta, ok := orm.LookupModel(SysRuleModelName)
	if !ok || strings.TrimSpace(ruleMeta.StorageName) == "" {
		return "", 0, fmt.Errorf("source model %q is not registered", SysRuleModelName)
	}
	large := uiMenuLargeTableSet(ctx, env, storage)

	var query strings.Builder
	var args []any
	for i, table := range storage {
		if i > 0 {
			query.WriteString(" UNION ALL ")
		}
		stat := "coalesce((SELECT n_tup_ins + n_tup_upd + n_tup_del FROM pg_stat_user_tables WHERE relid = to_regclass(?)), 0)"
		if large[table] {
			fmt.Fprintf(&query, "SELECT %d AS k, %s AS st, -1::bigint AS n, ''::text AS m", i, stat)
			args = append(args, table)
			continue
		}
		fmt.Fprintf(&query, "SELECT %d AS k, %s AS st, count(*) AS n, coalesce(max(updated_at)::text, '') AS m FROM ?", i, stat)
		args = append(args, table, bun.Ident(table))
	}
	// Last row: active rules on ui.menu (carried in n; st/m unused).
	fmt.Fprintf(&query, " UNION ALL SELECT %d AS k, 0::bigint AS st, count(*) AS n, ''::text AS m FROM ? WHERE model_name = ? AND active", len(storage))
	args = append(args, bun.Ident(ruleMeta.StorageName), UIMenuModelName)
	query.WriteString(" ORDER BY k")

	var rows []struct {
		K  int    `bun:"k"`
		St int64  `bun:"st"`
		N  int64  `bun:"n"`
		M  string `bun:"m"`
	}
	if err := env.DB().NewRaw(query.String(), args...).Scan(ctx, &rows); err != nil {
		return "", 0, err
	}
	if len(rows) != len(storage)+1 {
		return "", 0, fmt.Errorf("fingerprint returned %d rows, want %d", len(rows), len(storage)+1)
	}
	parts := make([]string, 0, len(storage))
	for _, row := range rows[:len(storage)] {
		parts = append(parts, fmt.Sprintf("%d:%d@%s", row.St, row.N, row.M))
	}
	return strings.Join(parts, "|"), rows[len(storage)].N, nil
}

// readUIMenuCacheEntry returns the kvt row id (0 when absent) and the decoded entry (nil when absent or unreadable).
func readUIMenuCacheEntry(ctx context.Context, key string) (int64, *uiMenuCacheEntry, error) {
	rows, err := orm.RuntimeRepositorySearchRead(ctx, orm.RuntimeModelRef(uiMenuKvtModelName, nil),
		orm.RecordMap{"name": key, "__limit": 1}, []string{"id", "value"})
	if err != nil {
		return 0, nil, err
	}
	if len(rows) == 0 {
		return 0, nil, nil
	}
	id, _ := int64FromAny(rows[0]["id"])
	var raw []byte
	switch value := rows[0]["value"].(type) {
	case nil:
		return id, nil, nil
	case string:
		raw = []byte(value)
	case []byte:
		raw = value
	default:
		encoded, err := json.Marshal(value)
		if err != nil {
			return id, nil, nil
		}
		raw = encoded
	}
	var entry uiMenuCacheEntry
	if err := json.Unmarshal(raw, &entry); err != nil {
		// A corrupt entry is just a miss; the rebuild overwrites it.
		return id, nil, nil
	}
	return id, &entry, nil
}

// writeUIMenuCacheEntry stores the entry best-effort. It only writes when the
// environment holds a plain DB handle: the ORM then runs the write in its own
// short transaction, so a failure (e.g. two instances creating the same unique
// key at once) cannot poison a transaction the request is still using.
func writeUIMenuCacheEntry(ctx context.Context, env *orm.Environment, kvtID int64, key string, entry uiMenuCacheEntry) {
	if _, plainDB := env.DB().(*bun.DB); !plainDB {
		return
	}
	if err := orm.EnsureWritableEnvironment(env); err != nil {
		return
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		warnUIMenuCache("ui.menu cache encode: %v", err)
		return
	}
	var value map[string]any
	if err := json.Unmarshal(encoded, &value); err != nil {
		warnUIMenuCache("ui.menu cache encode: %v", err)
		return
	}
	model := orm.RuntimeModelRef(uiMenuKvtModelName, nil)
	if kvtID > 0 {
		_, err = orm.RuntimeRepositoryWrite(ctx, model, []string{fmt.Sprint(kvtID)}, orm.RecordMap{"value": value})
	} else {
		_, err = orm.RuntimeRepositoryCreate(ctx, model, orm.RecordMap{"name": key, "value": value, "type": uiMenuKvtTypeTechnical})
	}
	if err != nil {
		warnUIMenuCache("ui.menu cache write: %v", err)
	}
}

func parentsFromHadChildren(hadChildren map[int64]bool) []int64 {
	out := make([]int64, 0, len(hadChildren))
	for id, had := range hadChildren {
		if had {
			out = append(out, id)
		}
	}
	return out
}

func hadChildrenFromParents(parents []int64) map[int64]bool {
	out := make(map[int64]bool, len(parents))
	for _, id := range parents {
		out[id] = true
	}
	return out
}
