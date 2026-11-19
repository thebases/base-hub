package config

// File overview:
// - Responsibility: verifies configuration parsing, defaults, and environment overrides for config.
// - Key symbols: focused Test cases plus file-local fixtures and assertion helpers.
// - Pattern: Locks down config normalization rules so deployments keep the same effective runtime settings across refactors.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/pgdialect"
)

func TestConfigParamBoolValue(t *testing.T) {
	testCases := []struct {
		name     string
		input    any
		expected bool
	}{
		{name: "bool true", input: true, expected: true},
		{name: "bool false", input: false, expected: false},
		{name: "string true", input: "true", expected: true},
		{name: "string mixed case true", input: "TrUe", expected: true},
		{name: "string false", input: "false", expected: false},
		{name: "invalid string", input: "enabled", expected: false},
		{name: "int one", input: 1, expected: true},
		{name: "int zero", input: 0, expected: false},
		{name: "float one", input: 1.0, expected: true},
		{name: "nil", input: nil, expected: false},
	}

	for _, testCase := range testCases {
		if actual := configParamBoolValue(testCase.input); actual != testCase.expected {
			t.Fatalf("%s: expected %t, got %t", testCase.name, testCase.expected, actual)
		}
	}
}

func TestHomeDBName(t *testing.T) {
	previous := *Get()
	t.Cleanup(func() {
		Set(previous)
	})

	Update(func(c *AppConfig) { c.PostgresqlDb.HomeDBName = "control_plane" })
	if got := HomeDBName(); got != "control_plane" {
		t.Fatalf("expected explicit home db name, got %q", got)
	}

	// The home DB is the only startup bootstrap target, so it must never
	// resolve to empty: an unset home_db_name falls back to the default.
	Update(func(c *AppConfig) { c.PostgresqlDb.HomeDBName = "" })
	if got := HomeDBName(); got != DefaultHomeDBName {
		t.Fatalf("expected default home db name %q, got %q", DefaultHomeDBName, got)
	}
}

func TestInitialMasterDataImportEnabledUsesSysConfigParam(t *testing.T) {
	db, cleanup, err := openConfigTestBunDB()
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()

	if err := ensureSystemConfigTable(ctx, db); err != nil {
		if isConfigTestConnectivityError(err) {
			t.Skipf("postgres fixture unavailable: %v", err)
		}
		t.Fatalf("ensure sys.config_param table: %v", err)
	}

	var previous []systemConfigRow
	allRows := []systemConfigRow{}
	if err := db.NewSelect().
		Model(&allRows).
		Scan(ctx); err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("load init_import fixture: %v", err)
	}
	for _, row := range allRows {
		if canonicalSystemConfigKey(row.Name) == initialImportConfigKey {
			previous = append(previous, row)
		}
	}
	t.Cleanup(func() {
		if cleanupErr := deleteConfigKeyVariants(ctx, db, initialImportConfigKey); cleanupErr != nil {
			t.Fatalf("cleanup init_import row: %v", cleanupErr)
		}
		if len(previous) > 0 {
			if cleanupErr := upsertSystemConfigRows(ctx, db, previous); cleanupErr != nil {
				t.Fatalf("restore init_import row: %v", cleanupErr)
			}
		}
	})

	if err := deleteConfigKeyVariants(ctx, db, initialImportConfigKey); err != nil {
		t.Fatalf("clear init_import row: %v", err)
	}

	enabled, err := InitialMasterDataImportEnabled(ctx, db)
	if err != nil {
		t.Fatalf("read missing init_import gate: %v", err)
	}
	if enabled {
		t.Fatalf("expected missing init_import row to disable bootstrap")
	}

	if err := SetInitialMasterDataImportEnabled(ctx, db, true); err != nil {
		t.Fatalf("set init_import=true: %v", err)
	}
	enabled, err = InitialMasterDataImportEnabled(ctx, db)
	if err != nil {
		t.Fatalf("read init_import=true gate: %v", err)
	}
	if !enabled {
		t.Fatalf("expected init_import=true to enable bootstrap")
	}

	var persisted systemConfigRow
	if err := db.NewSelect().
		Model(&persisted).
		Where("name = ?", initialImportConfigKey).
		Limit(1).
		Scan(ctx); err != nil {
		t.Fatalf("read persisted init_import row: %v", err)
	}
	if persisted.Name != initialImportConfigKey {
		t.Fatalf("expected persisted key %q, got %q", initialImportConfigKey, persisted.Name)
	}
	if !configParamBoolValue(persisted.Value) {
		t.Fatalf("expected persisted init_import value to be true, got %#v", persisted.Value)
	}

	if err := SetInitialMasterDataImportEnabled(ctx, db, false); err != nil {
		t.Fatalf("set init_import=false: %v", err)
	}
	enabled, err = InitialMasterDataImportEnabled(ctx, db)
	if err != nil {
		t.Fatalf("read init_import=false gate: %v", err)
	}
	if enabled {
		t.Fatalf("expected init_import=false to disable bootstrap")
	}
}

func TestInitialMasterDataImportEnabledAcceptsLowercaseAlias(t *testing.T) {
	db, cleanup, err := openConfigTestBunDB()
	if err != nil {
		t.Skipf("open postgres fixture: %v", err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()

	if err := ensureSystemConfigTable(ctx, db); err != nil {
		if isConfigTestConnectivityError(err) {
			t.Skipf("postgres fixture unavailable: %v", err)
		}
		t.Fatalf("ensure sys.config_param table: %v", err)
	}

	var previous []systemConfigRow
	allRows := []systemConfigRow{}
	if err := db.NewSelect().
		Model(&allRows).
		Scan(ctx); err != nil && err.Error() != "sql: no rows in result set" {
		t.Fatalf("load init_import aliases: %v", err)
	}
	for _, row := range allRows {
		if canonicalSystemConfigKey(row.Name) == initialImportConfigKey {
			previous = append(previous, row)
		}
	}
	t.Cleanup(func() {
		if cleanupErr := deleteConfigKeyVariants(ctx, db, initialImportConfigKey); cleanupErr != nil {
			t.Fatalf("cleanup init_import aliases: %v", cleanupErr)
		}
		if len(previous) > 0 {
			if cleanupErr := upsertSystemConfigRows(ctx, db, previous); cleanupErr != nil {
				t.Fatalf("restore init_import aliases: %v", cleanupErr)
			}
		}
	})

	if err := deleteConfigKeyVariants(ctx, db, initialImportConfigKey); err != nil {
		t.Fatalf("clear init_import aliases: %v", err)
	}
	if _, err := db.NewInsert().
		Model(&systemConfigRow{Name: "initImport", Value: true}).
		Exec(ctx); err != nil {
		t.Fatalf("insert legacy initImport alias: %v", err)
	}

	enabled, err := InitialMasterDataImportEnabled(ctx, db)
	if err != nil {
		t.Fatalf("read legacy initImport alias: %v", err)
	}
	if !enabled {
		t.Fatalf("expected legacy initImport alias to enable bootstrap")
	}
}

func openConfigTestBunDB() (*bun.DB, func(), error) {
	host := getenvDefault("RUNTIME_GRAPH_TEST_DB_HOST", "localhost")
	port := getenvIntDefault("RUNTIME_GRAPH_TEST_DB_PORT", 5432)
	user := getenvDefault("RUNTIME_GRAPH_TEST_DB_USER", "thebase")
	password := getenvDefault("RUNTIME_GRAPH_TEST_DB_PASSWORD", "theBase15112023@")
	name := getenvDefault("RUNTIME_GRAPH_TEST_DB_NAME", "base_hub_test")
	sslMode := getenvDefault("RUNTIME_GRAPH_TEST_DB_SSLMODE", "disable")

	dsn := fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		user,
		password,
		host,
		port,
		name,
		sslMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, nil, err
	}
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, nil, err
	}
	sqldb := stdlib.OpenDBFromPool(pool)
	bundb := bun.NewDB(sqldb, pgdialect.New())
	cleanup := func() {
		_ = bundb.Close()
		_ = sqldb.Close()
		pool.Close()
	}
	return bundb, cleanup, nil
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

func isConfigTestConnectivityError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "dial tcp") ||
		strings.Contains(message, "connect") ||
		strings.Contains(message, "operation not permitted")
}

func deleteConfigKeyVariants(ctx context.Context, db *bun.DB, canonicalKey string) error {
	rows := []systemConfigRow{}
	if err := db.NewSelect().Model(&rows).Scan(ctx); err != nil && err.Error() != "sql: no rows in result set" {
		return err
	}
	names := make([]string, 0, len(rows))
	for _, row := range rows {
		if canonicalSystemConfigKey(row.Name) == canonicalKey {
			names = append(names, row.Name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	_, err := db.NewDelete().
		Model((*systemConfigRow)(nil)).
		Where("name IN (?)", bun.In(uniqueStrings(names))).
		Exec(ctx)
	return err
}

func TestFlattenAndExpandSystemConfigRows(t *testing.T) {
	input := map[string]any{
		"app_name": "ETL-WEBHOOK Group Task A",
		"kafka": map[string]any{
			"enable":      true,
			"is_producer": true,
			"brokers":     []any{"127.0.0.1:9092", "127.0.0.2:9092"},
		},
		"server": map[string]any{
			"port": 8180,
		},
	}

	rows := flattenConfigMapToRows(input)
	expectedNames := []string{
		"app_name",
		"kafka.brokers",
		"kafka.enable",
		"kafka.is_producer",
		"server.port",
	}

	if len(rows) != len(expectedNames) {
		t.Fatalf("expected %d flattened rows, got %d", len(expectedNames), len(rows))
	}

	for index, expectedName := range expectedNames {
		if rows[index].Name != expectedName {
			t.Fatalf("expected flattened name %q at index %d, got %q", expectedName, index, rows[index].Name)
		}
	}

	expanded, err := expandSystemConfigRows(rows)
	if err != nil {
		t.Fatalf("expandSystemConfigRows returned error: %v", err)
	}

	if !reflect.DeepEqual(expanded, input) {
		t.Fatalf("expected expanded config %#v, got %#v", input, expanded)
	}
}

func TestFlattenConfigMapToRowsCanonicalizesInitImportKey(t *testing.T) {
	rows := flattenConfigMapToRows(map[string]any{
		"initimport": true,
		"server": map[string]any{
			"port": 8180,
		},
	})

	names := make([]string, 0, len(rows))
	for _, row := range rows {
		names = append(names, row.Name)
	}

	if !reflect.DeepEqual(names, []string{"init_import", "server.port"}) {
		t.Fatalf("expected canonical flattened keys, got %#v", names)
	}
}

func TestDecodeConfigMap(t *testing.T) {
	input := map[string]any{
		"app": map[string]any{
			"app_name": "bean-ipn",
			"version":  "1.0",
			"env":      "development",
		},
		"server": map[string]any{
			"host":         "localhost",
			"port":         8180,
			"base_path":    "/",
			"support_flow": false,
		},
		"postgresql": map[string]any{
			"enable":      true,
			"db_host":     "localhost",
			"db_port":     5432,
			"db_user":     "postgres",
			"db_password": "postgres",
			"db_sslmode":  "disable",
		},
		"logging": map[string]any{
			"level":    1,
			"pattern":  "pattern",
			"use_file": true,
			"path":     "./logs/ipn.log",
			"req_path": "./logs/ipn_req.log",
		},
		"cors": map[string]any{
			"allow_origins":     "*",
			"allow_methods":     "GET,POST",
			"allow_headers":     "Origin",
			"expose_headers":    "X-Test",
			"allow_credentials": false,
		},
		"auth_service": map[string]any{
			"endpoint":      "https://auth.local",
			"client_id":     "client-id",
			"client_secret": "client-secret",
			"scopes":        "read",
			"service_name":  "auth",
		},
		"mqtt": map[string]any{
			"enable":    true,
			"use_ssl":   false,
			"domain":    "mqtt.local",
			"port":      "1883",
			"client_id": "bean-ipn",
			"user_name": "user",
			"password":  "pass",
		},
		"kafka": map[string]any{
			"enable":      true,
			"is_producer": true,
			"is_comsumer": false,
			"use_dedupe":  true,
			"brokers":     []any{"127.0.0.1:9092"},
			"topics":      []any{"bank_ipn_topic"},
		},
		"destination_url": "https://example.local",
	}

	if err := decodeConfigMap(input); err != nil {
		t.Fatalf("decodeConfigMap returned error: %v", err)
	}

	if Get().App.Name != "bean-ipn" {
		t.Fatalf("expected app name bean-ipn, got %q", Get().App.Name)
	}
	if !Get().Kafka.Enable {
		t.Fatalf("expected kafka.enable to be true")
	}
	if Get().Server.Port != 8180 {
		t.Fatalf("expected server port 8180, got %d", Get().Server.Port)
	}
	if len(Get().Kafka.Brokers) != 1 || Get().Kafka.Brokers[0] != "127.0.0.1:9092" {
		t.Fatalf("expected kafka brokers to decode correctly, got %#v", Get().Kafka.Brokers)
	}
}

func TestDecodeConfigMapAcceptsCamelCaseKeys(t *testing.T) {
	input := map[string]any{
		"app": map[string]any{
			"appName": "bean-ipn",
			"version": "1.0",
			"env":     "development",
		},
		"server": map[string]any{
			"host":         "localhost",
			"port":         8180,
			"basePath":     "/",
			"supportFlow":  false,
			"sharedFolder": "./shared",
			"maxReadLimit": 2500,
		},
		"postgresql": map[string]any{
			"enable":     true,
			"dbHost":     "localhost",
			"dbPort":     5432,
			"dbUser":     "postgres",
			"dbPassword": "postgres",
			"dbSslmode":  "disable",
			"dbFilter":   "^home$",
			"homeDbName": "home",
		},
		"logging": map[string]any{
			"level":   1,
			"pattern": "pattern",
			"useFile": true,
			"path":    "./logs/ipn.log",
			"reqPath": "./logs/ipn_req.log",
		},
		"cors": map[string]any{
			"allowOrigins":     "*",
			"allowMethods":     "GET,POST",
			"allowHeaders":     "Origin",
			"exposeHeaders":    "X-Test",
			"allowCredentials": false,
		},
		"authService": map[string]any{
			"endpoint":     "https://auth.local",
			"clientId":     "client-id",
			"clientSecret": "client-secret",
			"scopes":       "read",
			"serviceName":  "auth",
		},
		"mqtt": map[string]any{
			"enable":   true,
			"useSsl":   false,
			"domain":   "mqtt.local",
			"port":     "1883",
			"clientId": "bean-ipn",
			"userName": "user",
			"password": "pass",
		},
		"kafka": map[string]any{
			"enable":     true,
			"isProducer": true,
			"isComsumer": false,
			"useDedupe":  true,
			"brokers":    []any{"127.0.0.1:9092"},
			"topics":     []any{"bank_ipn_topic"},
		},
		"destinationUrl": "https://example.local",
		"knowledge": map[string]any{
			"publicDocsPath": "public_docs",
		},
	}

	if err := decodeConfigMap(input); err != nil {
		t.Fatalf("decodeConfigMap returned error for camelCase input: %v", err)
	}

	if Get().App.Name != "bean-ipn" {
		t.Fatalf("expected app name bean-ipn, got %q", Get().App.Name)
	}
	if Get().Server.MaxReadLimit != 2500 || Get().Server.ResolvedMaxReadLimit() != 2500 {
		t.Fatalf("server.maxReadLimit = %d", Get().Server.MaxReadLimit)
	}
	if Get().Server.BasePath != "/" {
		t.Fatalf("expected server base path /, got %q", Get().Server.BasePath)
	}
	if Get().Server.SharedFolder != "./shared" {
		t.Fatalf("expected server shared folder ./shared, got %q", Get().Server.SharedFolder)
	}
	if Get().PostgresqlDb.Host != "localhost" || Get().PostgresqlDb.DBFilter != "^home$" {
		t.Fatalf("expected postgresql camelCase keys to decode, got %#v", Get().PostgresqlDb)
	}
	if Get().CORS.AllowHeaders != "Origin" {
		t.Fatalf("expected cors allow headers to decode, got %q", Get().CORS.AllowHeaders)
	}
	if Get().AuthService.ClientID != "client-id" {
		t.Fatalf("expected auth service client id to decode, got %q", Get().AuthService.ClientID)
	}
	if Get().MqttClient.ClientID != "bean-ipn" || Get().MqttClient.Username != "user" {
		t.Fatalf("expected mqtt camelCase keys to decode, got %#v", Get().MqttClient)
	}
	if !Get().Kafka.IsProducer || Get().Kafka.UseDedupe != true {
		t.Fatalf("expected kafka camelCase keys to decode, got %#v", Get().Kafka)
	}
	if Get().Knowledge.PublicDocsPath != "public_docs" {
		t.Fatalf("expected knowledge keys to decode, got %#v", Get().Knowledge)
	}
	if got, want := Get().KnowledgePublicDocsRoot("acme"), filepath.Join("./shared", "acme", "public_docs"); got != want {
		t.Fatalf("expected tenant-scoped knowledge docs root %q, got %q", want, got)
	}
}

func TestNormalizeConfigStructureMergesLegacyOAuthSections(t *testing.T) {
	input := map[string]any{
		"oauth": map[string]any{
			"issuer":                   "legacy-issuer",
			"accessTokenTtlMinutes":    45,
			"refresh_token_ttl_days":   60,
			"unrelated_legacy_setting": "kept",
		},
		"auth": map[string]any{
			"oauth": map[string]any{
				"rsaKeyId":        "legacy-key",
				"rsa_private_key": "legacy-private",
			},
		},
		"server": map[string]any{
			"issuer": "server-wins",
		},
		"authService": map[string]any{
			"enable": false,
		},
	}

	normalized := normalizeConfigStructure(canonicalizeConfigMapKeys(input).(map[string]any))
	serverSection := normalized["server"].(map[string]any)
	authServiceSection := normalized["auth_service"].(map[string]any)
	oauthSection := normalized["oauth"].(map[string]any)

	if serverSection["issuer"] != "server-wins" {
		t.Fatalf("expected existing server issuer to win, got %#v", serverSection["issuer"])
	}
	if serverSection["access_token_ttl_minutes"] != 45 || serverSection["refresh_token_ttl_days"] != 60 {
		t.Fatalf("expected oauth ttl values to merge into server, got %#v", serverSection)
	}
	if authServiceSection["rsa_key_id"] != "legacy-key" || authServiceSection["rsa_private_key"] != "legacy-private" {
		t.Fatalf("expected auth.oauth values to merge into auth_service, got %#v", authServiceSection)
	}
	if oauthSection["unrelated_legacy_setting"] != "kept" {
		t.Fatalf("expected unknown oauth keys to stay in oauth section, got %#v", oauthSection)
	}
	if _, exists := normalized["auth"]; exists {
		t.Fatalf("expected empty legacy auth section to be removed, got %#v", normalized["auth"])
	}
}

func TestFileOnlyConfigKeyNeverRoundTripsThroughRows(t *testing.T) {
	rows := flattenConfigMapToRows(map[string]any{
		"app": map[string]any{"encrypt_key": "k", "app_name": "x"},
	})
	for _, row := range rows {
		if IsFileOnlyConfigKey(row.Name) {
			t.Fatalf("file-only key %q was flattened into a sys.config_param row", row.Name)
		}
	}
	// A stray DB copy (any alias spelling) is ignored on the way back in.
	expanded, err := expandSystemConfigRows([]systemConfigRow{{Name: "app.encryptKey", Value: "stale"}, {Name: "app.app_name", Value: "x"}})
	if err != nil {
		t.Fatalf("expandSystemConfigRows: %v", err)
	}
	if _, ok := expanded["app"].(map[string]any)["encrypt_key"]; ok {
		t.Fatalf("expected DB copy of encrypt_key to be ignored, got %#v", expanded)
	}
}

func TestOverlayFileOnlyConfigTakesValueFromLoadedFile(t *testing.T) {
	previous := loadedConfigSettings
	t.Cleanup(func() { loadedConfigSettings = previous })
	loadedConfigSettings = map[string]any{"app": map[string]any{"encrypt_key": "from-file"}}

	configMap := map[string]any{"app": map[string]any{"app_name": "x"}}
	if err := overlayFileOnlyConfig(configMap); err != nil {
		t.Fatalf("overlayFileOnlyConfig: %v", err)
	}
	if got := configMap["app"].(map[string]any)["encrypt_key"]; got != "from-file" {
		t.Fatalf("expected encrypt_key from file, got %#v", got)
	}
}

func TestConfigGetSetConcurrent(t *testing.T) {
	previous := *Get()
	t.Cleanup(func() { Set(previous) })
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				_ = Get().App.Name
			}
		}()
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				Update(func(c *AppConfig) { c.App.Name = "x" })
			}
		}()
	}
	wg.Wait()
}

func TestTCPConfigDefaultsAndValidate(t *testing.T) {
	d := TCPConfig{}.WithDefaults()
	if d.DefaultPort != 10123 || d.PortMin != 10000 || d.PortMax != 49999 || d.MaxConns != 256 ||
		d.MaxConnsPerIP != 32 || d.MaxFrameBytes != 4096 || d.FirstFrameS != 10 || d.IdleTimeoutS != 120 ||
		d.Bind != "0.0.0.0" || d.ProxyProtocol != "none" {
		t.Fatalf("unexpected defaults %#v", d)
	}
	if err := (TCPConfig{}).Validate(); err != nil {
		t.Fatalf("defaults must validate: %v", err)
	}
	if err := (TCPConfig{PortMin: 20000, PortMax: 15000}).Validate(); err == nil {
		t.Fatal("portMin > portMax must fail")
	}
	if err := (TCPConfig{PortMin: 20000, PortMax: 30000}).Validate(); err == nil {
		t.Fatal("default port outside the range must fail")
	}
}

func TestTCPConfigYAMLKeysRoundTrip(t *testing.T) {
	input := map[string]any{"tcp": map[string]any{
		"useTcp": true, "defaultPort": 10200, "maxConnsPerIp": 5, "firstFrameS": 3,
		"allowCidrs": []any{"10.0.0.0/8"}, "proxyProtocol": "v2", "trustedProxies": []any{"127.0.0.1/32"},
		"tlsCertFile": "/c.pem", "tlsKeyFile": "/k.pem", "tlsClientCaFile": "/ca.pem",
	}}
	if err := decodeConfigMap(input); err != nil {
		t.Fatal(err)
	}
	c := Get().Tcp
	if !c.UseTCP || c.DefaultPort != 10200 || c.MaxConnsPerIP != 5 || c.FirstFrameS != 3 || c.ProxyProtocol != "v2" ||
		len(c.AllowCIDRs) != 1 || len(c.TrustedProxies) != 1 || c.TLSCertFile != "/c.pem" || c.TLSClientCAFile != "/ca.pem" {
		t.Fatalf("camelCase tcp keys did not decode: %#v", c)
	}
	out := camelizeConfigMapKeys(canonicalizeConfigMapKeys(input)).(map[string]any)["tcp"].(map[string]any)
	for _, k := range []string{"useTcp", "maxConnsPerIp", "tlsClientCaFile", "trustedProxies", "firstFrameS"} {
		if _, ok := out[k]; !ok {
			t.Fatalf("camel round-trip lost %q: %#v", k, out)
		}
	}
}
