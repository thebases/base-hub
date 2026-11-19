package test

// File overview:
// - Responsibility: provides backend test-support runtime helpers for test even though this file itself is part of the non-`_test.go` support surface.
// - Key symbols: `RestoreConfig`, `SwapBunDB`.
// - Pattern: Reads operational behavior from shared config so environments can tune the same code paths without forking implementation logic.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/jackc/pgx/v5"
)

// RequireDBIntegrationTests skips the calling test unless database-creating
// integration tests were explicitly opted into, since they create/drop real
// PostgreSQL databases against the configured cluster.
func RequireDBIntegrationTests(t *testing.T) {
	t.Helper()
	if os.Getenv("BASE_HUB_RUN_DB_INTEGRATION_TESTS") != "1" {
		t.Skip("set BASE_HUB_RUN_DB_INTEGRATION_TESTS=1 to allow database-creating integration tests")
	}
}

// OpenAdminConn opens a raw pgx connection for admin-level DDL (CREATE/DROP
// DATABASE) that integration tests need outside of the ORM's bun pool,
// falling back to the cluster's "postgres" maintenance database when cfg.Name
// isn't reachable directly.
func OpenAdminConn(ctx context.Context, cfg config.PostgresqlConfig) (*pgx.Conn, error) {
	dbName := strings.TrimSpace(cfg.Name)
	if dbName == "" {
		dbName = "postgres"
	}
	connString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, dbName, cfg.SSLMode,
	)
	conn, err := pgx.Connect(ctx, connString)
	if err == nil {
		return conn, nil
	}

	maintenanceConnString := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, "postgres", cfg.SSLMode,
	)
	return pgx.Connect(ctx, maintenanceConnString)
}

func RestoreConfig(t *testing.T, mutate func(*config.AppConfig)) {
	t.Helper()

	previous := *config.Get()
	config.Update(mutate)
	t.Cleanup(func() {
		config.Set(previous)
	})
}

func SwapBunDB(t *testing.T, bunDB *orm.BunDB) {
	t.Helper()

	previous := orm.ResolveDatabase("")
	orm.SetBunDB(bunDB)
	t.Cleanup(func() {
		if previous == nil {
			orm.SetBunDB(nil)
			return
		}
		orm.SetBunDB(previous)
	})
}

func EnvPostgresConfig() config.PostgresqlConfig {
	return config.PostgresqlConfig{
		Enable:   true,
		Host:     getenvDefault("RUNTIME_GRAPH_TEST_DB_HOST", "localhost"),
		Port:     getenvIntDefault("RUNTIME_GRAPH_TEST_DB_PORT", 5432),
		User:     getenvDefault("RUNTIME_GRAPH_TEST_DB_USER", "thebase"),
		Password: getenvDefault("RUNTIME_GRAPH_TEST_DB_PASSWORD", "theBase15112023@"),
		Name:     getenvDefault("RUNTIME_GRAPH_TEST_DB_NAME", "base_hub_test"),
		SSLMode:  getenvDefault("RUNTIME_GRAPH_TEST_DB_SSLMODE", "disable"),
	}
}

// NewJSONRequest constructs and returns the json request handled by this package.
func NewJSONRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(method, target, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request %s %s: %v", method, target, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return req
}

// NewJSONRequestWithBearer constructs a JSON request carrying an
// Authorization: Bearer header. Replaces the removed NewJSONRequestWithAPIKey
// (X-API-Key was deleted, RBAC/OAuth backend plan §1/§7.4) — callers now pass
// an OAuth access token (real or minted via baseauth.MintAccessTokenForTests)
// instead of the static service key.
func NewJSONRequestWithBearer(t *testing.T, method, target, body, accessToken string) *http.Request {
	t.Helper()

	req := NewJSONRequest(t, method, target, body)
	req.Header.Set("Authorization", "Bearer "+accessToken)
	return req
}

// RequireStatus requires the status handled by this package.
func RequireStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	if resp.StatusCode != want {
		t.Fatalf("unexpected status: got %d want %d", resp.StatusCode, want)
	}
}

func DecodeJSON[T any](t *testing.T, reader io.Reader) T {
	t.Helper()

	var payload T
	if err := json.NewDecoder(reader).Decode(&payload); err != nil {
		t.Fatalf("decode json payload: %v", err)
	}
	return payload
}

// ReadBody reads the body handled by this package.
func ReadBody(t *testing.T, resp *http.Response) string {
	t.Helper()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return string(body)
}

func getenvDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func getenvIntDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
