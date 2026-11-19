package service

// Covers SEC-T-3: client_credentials must never yield a refresh token, and a
// stored refresh token that was not issued to a user must never be resolved
// as a base_users id. Runs against real Postgres in a throwaway schema (skips
// when unreachable) so the token-table SQL is exercised, while the base_users
// lookup is swapped via the lookupRBACIdentityByUserID seam.

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/orm"
	testutil "git.thebase.vn/base_hub/internal/test"
	authmodels "git.thebase.vn/base_hub/modules/base-auth/models"
	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

// setupOAuthTokenSchema creates a throwaway schema holding sys_oauth_client
// and sys_oauth_refresh_token, registers a search_path-pinned handle under the
// schema name (so dbBoundContext resolves it as the tenant DB) and installs an
// ephemeral signing key. Returns the handle and the db name to pass to the provider.
func setupOAuthTokenSchema(t *testing.T) (*bun.DB, string) {
	t.Helper()
	ctx := context.Background()
	cfg := testutil.EnvPostgresConfig()
	admin, err := orm.NewBunDB(cfg)
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(func() { _ = admin.Close() })
	schema := fmt.Sprintf("oauth_t3_test_%d", time.Now().UnixNano())
	if _, err := admin.DB.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Skipf("create schema (postgres unreachable?): %v", err)
	}
	t.Cleanup(func() { _, _ = admin.DB.ExecContext(ctx, "DROP SCHEMA "+schema+" CASCADE") })

	poolCfg, err := pgxpool.ParseConfig(fmt.Sprintf("host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.Name, cfg.SSLMode))
	if err != nil {
		t.Fatalf("parse pool config: %v", err)
	}
	poolCfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	poolCfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	db := bun.NewDB(stdlib.OpenDBFromPool(pool), pgdialect.New())
	t.Cleanup(func() { _ = db.Close(); pool.Close() })

	if _, err := db.NewCreateTable().Model((*authmodels.SysOauthRefreshTokenRecord)(nil)).Exec(ctx); err != nil {
		t.Fatalf("create refresh table: %v", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE TABLE sys_oauth_client (
		id BIGSERIAL PRIMARY KEY, client_id TEXT, client_secret_hash TEXT, service_name TEXT,
		role_id BIGINT, is_superadmin BOOLEAN, active BOOLEAN DEFAULT TRUE, redirect_uris JSONB,
		allowed_grants JSONB, is_public BOOLEAN)`); err != nil {
		t.Fatalf("create client table: %v", err)
	}

	orm.SetNamedBunDB(schema, &orm.BunDB{DB: db, Pool: pool})
	t.Cleanup(func() { orm.SetNamedBunDB(schema, nil) })

	key, err := NewEphemeralSigningKeyForTests()
	if err != nil {
		t.Fatalf("signing key: %v", err)
	}
	t.Cleanup(SetSigningKeyForTests(key))
	return db, schema
}

// stubUserLookup swaps the base_users lookup and records which ids reached it.
func stubUserLookup(t *testing.T, users map[int64]*orm.RBACIdentity) *[]int64 {
	t.Helper()
	var seen []int64
	prev := lookupRBACIdentityByUserID
	lookupRBACIdentityByUserID = func(_ context.Context, userID int64) (*orm.RBACIdentity, error) {
		seen = append(seen, userID)
		return users[userID], nil
	}
	t.Cleanup(func() { lookupRBACIdentityByUserID = prev })
	return &seen
}

func countRefreshRows(t *testing.T, db *bun.DB, where string, args ...any) int {
	t.Helper()
	n, err := db.NewSelect().Table("sys_oauth_refresh_token").Where(where, args...).Count(context.Background())
	if err != nil {
		t.Fatalf("count refresh rows: %v", err)
	}
	return n
}

func TestClientCredentialsIssuesNoRefreshTokenAndMarksClientSubject(t *testing.T) {
	db, dbName := setupOAuthTokenSchema(t)
	ctx := context.Background()
	hash, err := authmodels.HashClientSecret("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	var clientRowID int64
	if err := db.QueryRowContext(ctx, `INSERT INTO sys_oauth_client (client_id, client_secret_hash, role_id, is_superadmin)
		VALUES ('svc-a', ?, 7, FALSE) RETURNING id`, hash).Scan(&clientRowID); err != nil {
		t.Fatalf("insert client: %v", err)
	}

	p := NewBuiltinProvider()
	pair, err := p.IssueClientCredentials(ctx, dbName, "svc-a", "s3cret")
	if err != nil {
		t.Fatalf("client_credentials: %v", err)
	}
	if pair.AccessToken == "" || pair.RefreshToken != "" {
		t.Fatalf("want access token and no refresh token, got refresh=%t access=%t", pair.RefreshToken != "", pair.AccessToken != "")
	}
	if n := countRefreshRows(t, db, "TRUE"); n != 0 {
		t.Fatalf("client_credentials persisted %d refresh rows, want 0", n)
	}

	claims := &AccessTokenClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(pair.AccessToken, claims); err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if want := fmt.Sprintf("client:%d", clientRowID); claims.Subject != want {
		t.Fatalf("sub = %q, want %q", claims.Subject, want)
	}
	// Validate keeps the client's role binding usable by the bearer middleware.
	identity, err := p.Validate(ctx, pair.AccessToken)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if identity.AuthMode != authModeClientCredentials || identity.RoleID != 7 || !strings.HasPrefix(identity.Login, "client:") {
		t.Fatalf("unexpected client identity: %#v", identity)
	}

	// Plan T36: allowed_grants without client_credentials, or a public client, is refused even
	// with the right secret.
	for _, row := range []struct {
		clientID, grants string
		public           bool
	}{
		{"svc-code-only", `["authorization_code"]`, false},
		{"svc-public", `[]`, true},
	} {
		if _, err := db.ExecContext(ctx, `INSERT INTO sys_oauth_client (client_id, client_secret_hash, role_id, is_superadmin, allowed_grants, is_public)
			VALUES (?, ?, 7, FALSE, ?::jsonb, ?)`, row.clientID, hash, row.grants, row.public); err != nil {
			t.Fatalf("insert %s: %v", row.clientID, err)
		}
		if pair, err := p.IssueClientCredentials(ctx, dbName, row.clientID, "s3cret"); err == nil || pair != nil {
			t.Fatalf("%s: client_credentials must be refused", row.clientID)
		}
	}
}

func TestRefreshRefusesTokenNotIssuedToUser(t *testing.T) {
	db, dbName := setupOAuthTokenSchema(t)
	ctx := context.Background()
	seen := stubUserLookup(t, map[int64]*orm.RBACIdentity{1: {UserID: 1, Login: "admin", IsSuperadmin: true}})

	// A legacy client_credentials row: user_id holds sys_oauth_client id 1 and
	// subject_kind was never set. A sibling in the same family must die with it.
	for _, plaintext := range []string{"legacy-client-token", "legacy-client-sibling"} {
		if _, err := db.ExecContext(ctx, `INSERT INTO sys_oauth_refresh_token
			(user_id, db_name, token_hash, family_id, issued_at, expires_at) VALUES (1, ?, ?, 'fam-client', now(), now() + interval '1 day')`,
			dbName, authmodels.HashRefreshToken(plaintext)); err != nil {
			t.Fatalf("insert legacy row: %v", err)
		}
	}

	pair, err := NewBuiltinProvider().Refresh(ctx, dbName, "legacy-client-token")
	if err == nil || pair != nil {
		t.Fatalf("legacy client refresh token was accepted")
	}
	if len(*seen) != 0 {
		t.Fatalf("refused token still reached the base_users lookup with ids %v", *seen)
	}
	if n := countRefreshRows(t, db, "family_id = 'fam-client' AND revoked_at IS NULL"); n != 0 {
		t.Fatalf("%d tokens of the refused family are still live", n)
	}
}

func TestRefreshUserTokenStillRotates(t *testing.T) {
	db, dbName := setupOAuthTokenSchema(t)
	ctx := context.Background()
	stubUserLookup(t, map[int64]*orm.RBACIdentity{42: {UserID: 42, Login: "alice"}})

	p := NewBuiltinProvider()
	first, err := p.mintTokenPair(ctx, db, &orm.RBACIdentity{UserID: 42, Login: "alice", DBName: dbName}, "oauth_password")
	if err != nil {
		t.Fatalf("mint user pair: %v", err)
	}
	second, err := p.Refresh(ctx, dbName, first.RefreshToken)
	if err != nil {
		t.Fatalf("user refresh: %v", err)
	}
	if second.RefreshToken == "" || second.RefreshToken == first.RefreshToken {
		t.Fatalf("expected a rotated refresh token")
	}
	identity, err := p.Validate(ctx, second.AccessToken)
	if err != nil || identity.UserID != 42 {
		t.Fatalf("refreshed token identity = %#v, err=%v", identity, err)
	}
	if n := countRefreshRows(t, db, "user_id = 42 AND subject_kind = ?", authmodels.RefreshTokenSubjectUser); n != 2 {
		t.Fatalf("user refresh rows stamped as user = %d, want 2", n)
	}
}

// SEC-T-13: concurrent refreshes with one token must yield exactly one new
// pair; the losers see a revoked token and the family is revoked.
func TestRefreshConcurrentUseRotatesOnce(t *testing.T) {
	db, dbName := setupOAuthTokenSchema(t)
	ctx := context.Background()
	stubUserLookupConcurrent(t, &orm.RBACIdentity{UserID: 42, Login: "alice"})

	p := NewBuiltinProvider()
	first, err := p.mintTokenPair(ctx, db, &orm.RBACIdentity{UserID: 42, Login: "alice", DBName: dbName}, "oauth_password")
	if err != nil {
		t.Fatalf("mint user pair: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var successes atomic.Int64
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if pair, err := p.Refresh(ctx, dbName, first.RefreshToken); err == nil && pair != nil {
				successes.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := successes.Load(); got != 1 {
		t.Fatalf("concurrent refreshes succeeded %d times, want exactly 1", got)
	}
	// Mint inserts happen only for the winner: the original row plus one.
	if n := countRefreshRows(t, db, "user_id = 42"); n != 2 {
		t.Fatalf("refresh rows = %d, want 2 (original + one rotation)", n)
	}
	// The losers saw a reused token, so the family revoke must also have
	// killed the winner's freshly minted token.
	if n := countRefreshRows(t, db, "user_id = 42 AND revoked_at IS NULL"); n != 0 {
		t.Fatalf("%d tokens survived the breach response, want 0", n)
	}
}

// stubUserLookupConcurrent is stubUserLookup without the shared slice, safe
// for concurrent Refresh calls.
func stubUserLookupConcurrent(t *testing.T, identity *orm.RBACIdentity) {
	t.Helper()
	prev := lookupRBACIdentityByUserID
	lookupRBACIdentityByUserID = func(_ context.Context, userID int64) (*orm.RBACIdentity, error) {
		if userID != identity.UserID {
			return nil, nil
		}
		copied := *identity
		return &copied, nil
	}
	t.Cleanup(func() { lookupRBACIdentityByUserID = prev })
}
