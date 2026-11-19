package config

// File overview:
// - Responsibility: config structs (AppConfig and sections), resolved-value accessors and bootstrap Postgres helpers.
// - Key symbols: `AppConfig`, `AppSettings`, `MqttClient`, `AuthServiceConfig`.

import (
	"fmt"
	"path/filepath"
	"strings"

	"git.thebase.vn/base_hub/internal/i18n"
)

// =========== STRUCT DEFINE ==========

// Config structs
// AppConfig stores application settings
type AppConfig struct {
	App          AppSettings       `mapstructure:"app" yaml:"app"`
	Server       ServerConfig      `mapstructure:"server" yaml:"server"`
	PostgresqlDb PostgresqlConfig  `mapstructure:"postgresql" yaml:"postgresql"`
	Logging      LoggingConfig     `mapstructure:"logging" yaml:"logging"`
	Knowledge    KnowledgeConfig   `mapstructure:"knowledge" yaml:"knowledge"`
	CORS         CORS              `mapstructure:"cors" yaml:"cors"`
	DestUrl      string            `mapstructure:"destination_url" yaml:"destinationUrl"`
	AuthService  AuthServiceConfig `mapstructure:"auth_service" yaml:"authService"`
	MqttClient   MqttClient        `mapstructure:"mqtt" yaml:"mqtt"`
	Kafka        KafkaConfig       `mapstructure:"kafka" yaml:"kafka"`
	Tcp          TCPConfig         `mapstructure:"tcp" yaml:"tcp"`
}

type AppSettings struct {
	Name       string `mapstructure:"app_name" yaml:"appName"`
	Version    string `mapstructure:"version" yaml:"version"`
	Env        string `mapstructure:"env" yaml:"env"`
	InitImport bool   `mapstructure:"init_import" yaml:"initImport"`
	// UseMultiTenant selects the tenancy mode. false (including when the key
	// is absent from config — the Go zero value) runs single-tenant: the
	// template database is never provisioned and modules/database is not
	// installed for the home DB. true runs multi-tenant mode. See
	// MultiTenantEnabled.
	UseMultiTenant bool `mapstructure:"use_multi_tenant" yaml:"useMultiTenant"`
	// Language is the comma-separated list of UI languages offered by the SPA
	// language switcher (e.g. "en_us,vi_vn,ja_jp"). Empty means DefaultAppLanguages.
	// See AppLanguages.
	Language string `mapstructure:"language" yaml:"language"`
	// EncryptKey is the app-wide secret encryption key (app.encryptKey), file-only (never stored in
	// sys.config_param). Modules use it to encrypt secrets at rest (e.g. device signing secrets);
	// rotating it invalidates what was encrypted with the old key.
	EncryptKey string `mapstructure:"encrypt_key" yaml:"encryptKey"`
	// DeviceProvisioningEnable gates device self-provisioning endpoints such as POST /device/provision
	// (app.deviceProvisioningEnable). Pointer so an absent key keeps the module default (enabled).
	DeviceProvisioningEnable *bool `mapstructure:"device_provisioning_enable" yaml:"deviceProvisioningEnable,omitempty"`
	// DiscussInboundWebhookSecret protects the discuss inbound-email webhook
	// (app.discussInboundWebhookSecret), sent by the relay in the X-Discuss-Webhook-Secret header.
	// Empty disables the webhook (every request is refused with 403).
	DiscussInboundWebhookSecret string `mapstructure:"discuss_inbound_webhook_secret" yaml:"discussInboundWebhookSecret"`
	// SupportApikey (app.supportApikey, default false) lets /rpc callers authenticate with an
	// X-API-Key header matching base_users.api_key, in addition to OAuth bearer tokens. Never
	// honored on /mcp.
	SupportApikey bool `mapstructure:"support_apikey" yaml:"supportApikey"`
}
type MqttClient struct {
	Enable                bool   `mapstructure:"enable" yaml:"enable"`
	UseSSL                bool   `mapstructure:"use_ssl" yaml:"useSsl"`
	Domain                string `mapstructure:"domain" yaml:"domain"`
	Port                  string `mapstructure:"port" yaml:"port"`
	ClientID              string `mapstructure:"client_id" yaml:"clientId"`
	Username              string `mapstructure:"user_name" yaml:"userName"`
	Password              string `mapstructure:"password" yaml:"password"`
	TLSInsecureSkipVerify bool   `mapstructure:"tls_insecure_skip_verify" yaml:"tlsInsecureSkipVerify"`
	TLSCAFile             string `mapstructure:"tls_ca_file" yaml:"tlsCaFile"`
	TLSCertFile           string `mapstructure:"tls_cert_file" yaml:"tlsCertFile"`
	TLSKeyFile            string `mapstructure:"tls_key_file" yaml:"tlsKeyFile"`
	// PublicWSURL is the browser-reachable MQTT-over-WebSocket listener (e.g.
	// "wss://mqtt.thebase.vn/mqtt"), separate from Domain/Port which is the
	// backend's TCP/TLS-only connection to the broker. Only set this to a
	// listener that actually enforces per-topic ACLs at the broker — the
	// base-bus module hands Username/Password to any authenticated browser
	// session once this is non-empty (see base-bus/routes/service_bus_route.go).
	PublicWSURL string `mapstructure:"public_ws_url" yaml:"publicWsUrl"`
}

type AuthServiceConfig struct {
	Enable       bool   `mapstructure:"enable" yaml:"enable"`
	Endpoint     string `mapstructure:"endpoint" yaml:"endpoint"`
	ClientID     string `mapstructure:"client_id" yaml:"clientId"`
	ClientSecret string `mapstructure:"client_secret" yaml:"clientSecret"`
	Scopes       string `mapstructure:"scopes" yaml:"scopes"`
	ServiceName  string `mapstructure:"service_name" yaml:"serviceName"`
	// Issuer/Audience/JWKSURL/ClockSkewSecs feed external-OIDC token
	// validation (modules/base-auth OIDCProvider); RoleClaim/LoginClaim map
	// IdP claims to a base_users row; AutoProvision defaults false so an IdP
	// token can never silently create a backend account (RBAC/OAuth backend
	// plan §4/§8).
	Issuer        string `mapstructure:"issuer" yaml:"issuer"`
	Audience      string `mapstructure:"audience" yaml:"audience"`
	JWKSURL       string `mapstructure:"jwks_url" yaml:"jwksUrl"`
	RoleClaim     string `mapstructure:"role_claim" yaml:"roleClaim"`
	LoginClaim    string `mapstructure:"login_claim" yaml:"loginClaim"`
	AutoProvision bool   `mapstructure:"auto_provision" yaml:"autoProvision"`
	ClockSkewSecs int    `mapstructure:"clock_skew_secs" yaml:"clockSkewSecs"`
	// TenantClaim names the IdP claim whose string value is the tenant database a token is
	// valid for. Empty binds every IdP token to the home DB only, so one IdP token is never
	// accepted on another tenant.
	TenantClaim string `mapstructure:"tenant_claim" yaml:"tenantClaim"`
}

type ServerConfig struct {
	Host        string `mapstructure:"host" yaml:"host"`
	Port        int    `mapstructure:"port" yaml:"port"`
	BasePath    string `mapstructure:"base_path" yaml:"basePath"`
	SupportFlow bool   `mapstructure:"support_flow" yaml:"supportFlow"`
	// StaticPath is the local folder attachments (and other user-uploaded
	// static files) are stored under. Defaults to "./data/files" via
	// ResolvedStaticPath when unset so the feature works out of the box.
	StaticPath   string `mapstructure:"static_path" yaml:"staticPath"`
	SharedFolder string `mapstructure:"shared_folder" yaml:"sharedFolder"`
	// Issuer is the "iss" claim minted into and verified against builtin
	// OAuth access tokens (modules/base-auth). Defaults to "base-hub" when empty.
	Issuer string `mapstructure:"issuer" yaml:"issuer"`
	// AccessTokenTTLMinutes defaults to 15 when zero.
	AccessTokenTTLMinutes int `mapstructure:"access_token_ttl_minutes" yaml:"accessTokenTtlMinutes"`
	// RefreshTokenTTLDays defaults to 30 when zero.
	RefreshTokenTTLDays int `mapstructure:"refresh_token_ttl_days" yaml:"refreshTokenTtlDays"`
	// TrustedProxies lists the reverse proxies (IPs or CIDRs) whose
	// X-Forwarded-For is believed. Empty: the client IP is the TCP peer, so
	// behind nginx every caller shares the proxy's IP in rate limits and logs.
	TrustedProxies []string `mapstructure:"trusted_proxies" yaml:"trustedProxies"`
	// ReadTimeoutS bounds reading one request (headers + body); 0 -> 120.
	ReadTimeoutS int `mapstructure:"read_timeout_s" yaml:"readTimeoutS"`
	// IdleTimeoutS closes idle keep-alive connections; 0 -> 120. There is no
	// write timeout on purpose: SSE and large downloads stream for longer.
	IdleTimeoutS int `mapstructure:"idle_timeout_s" yaml:"idleTimeoutS"`
	// MaxReadLimit caps the rows one client call (/rpc, REST alias, MCP) may list through
	// search/search_read/search_fetch/name_search; 0 -> DefaultMaxReadLimit. Server-side calls
	// and aggregates (read_group, cohort_data) are not capped.
	MaxReadLimit int `mapstructure:"max_read_limit" yaml:"maxReadLimit"`
	// OutboundAllowCIDRs lists IPs/CIDRs that user-authored outbound HTTP (workflow
	// post_output_object, notify, outbound retries) may reach although they are loopback,
	// private or link-local. Empty: only public addresses (internal/services/safehttp).
	OutboundAllowCIDRs []string `mapstructure:"outbound_allow_cidrs" yaml:"outboundAllowCidrs"`
}

// DefaultMaxReadLimit is the client read cap when server.maxReadLimit is unset.
const DefaultMaxReadLimit = 10000

// ResolvedMaxReadLimit returns the configured client read cap, defaulting to DefaultMaxReadLimit.
func (s ServerConfig) ResolvedMaxReadLimit() int {
	if s.MaxReadLimit > 0 {
		return s.MaxReadLimit
	}
	return DefaultMaxReadLimit
}

// DefaultStaticPath is used when Server.StaticPath is not configured.
const DefaultStaticPath = "./data/files"

// DefaultSharedFolder is the tenant-scoped shared runtime folder root.
const DefaultSharedFolder = "./data"

// ResolvedStaticPath returns the configured attachment storage root,
// defaulting to DefaultStaticPath so uploads never resolve to an empty path.
func ResolvedStaticPath() string {
	if path := strings.TrimSpace(Get().Server.StaticPath); path != "" {
		return path
	}
	return DefaultStaticPath
}

// ResolvedSharedFolder returns the configured tenant shared folder root.
func ResolvedSharedFolder() string {
	if path := strings.TrimSpace(Get().Server.SharedFolder); path != "" {
		return path
	}
	return DefaultSharedFolder
}

type PostgresqlConfig struct {
	Enable   bool   `mapstructure:"enable" yaml:"enable"`
	Host     string `mapstructure:"db_host" yaml:"dbHost"`
	Port     int    `mapstructure:"db_port" yaml:"dbPort"`
	User     string `mapstructure:"db_user" yaml:"dbUser"`
	Password string `mapstructure:"db_password" yaml:"dbPassword"`
	// Name is the physical database a connection targets. It is set
	// programmatically (per logical DB) right before dialing and is NOT a
	// config key: the legacy db_name key and its default data-plane database
	// were removed — home_db_name and template_db_name are the only
	// database-name settings the config file owns.
	Name           string                      `mapstructure:"-" yaml:"-"`
	HomeDBName     string                      `mapstructure:"home_db_name" yaml:"homeDbName"`
	TemplateDBName string                      `mapstructure:"template_db_name" yaml:"templateDbName"`
	SSLMode        string                      `mapstructure:"db_sslmode" yaml:"dbSslmode"` // usually "disable" in dev
	Databases      map[string]PostgresqlConfig `mapstructure:"databases" yaml:"databases"`
	DBFilter       string                      `mapstructure:"db_filter" yaml:"dbFilter"`
}

// DefaultHomeDBName is the control-plane database used when home_db_name is
// not set: the system cannot boot without a home DB, so the name must never
// resolve to empty.
const DefaultHomeDBName = "base-home"

// ResolvedHomeDBName returns this config's control-plane database name,
// defaulting to DefaultHomeDBName so callers never observe an empty home DB.
func (cfg PostgresqlConfig) ResolvedHomeDBName() string {
	if name := strings.TrimSpace(cfg.HomeDBName); name != "" {
		return name
	}
	return DefaultHomeDBName
}

// HomeDBName returns the configured control-plane database name from the
// runtime config (home_db_name, defaulting to "home").
func HomeDBName() string {
	return Get().PostgresqlDb.ResolvedHomeDBName()
}

// DefaultTemplateDBName is the clone-source database used when
// template_db_name is not set. Only meaningful in multi-tenant mode
// (MultiTenantEnabled); single-tenant mode never provisions this database.
const DefaultTemplateDBName = "base-template"

// ResolvedTemplateDBName returns this config's clone-template database name,
// defaulting to DefaultTemplateDBName so callers never observe an empty
// template DB.
func (cfg PostgresqlConfig) ResolvedTemplateDBName() string {
	if name := strings.TrimSpace(cfg.TemplateDBName); name != "" {
		return name
	}
	return DefaultTemplateDBName
}

// TemplateDBName returns the configured clone-template database name from the
// runtime config (template_db_name, defaulting to "service_temp").
func TemplateDBName() string {
	return Get().PostgresqlDb.ResolvedTemplateDBName()
}

// TemplateDBNameConfigured reports whether an admin has explicitly set
// template_db_name in the config file, as distinct from the service_temp
// fallback sentinel that ResolvedTemplateDBName applies when it's unset.
func TemplateDBNameConfigured() bool {
	return strings.TrimSpace(Get().PostgresqlDb.TemplateDBName) != ""
}

// BootstrapPostgresqlConfig returns the PostgreSQL settings loaded from disk at
// process start so runtime config rows cannot silently redirect provisioning.
func BootstrapPostgresqlConfig() PostgresqlConfig {
	if bootstrapConfigLoaded {
		return bootstrapConfig.PostgresqlDb
	}
	return Get().PostgresqlDb
}

// BootstrapHomeDBName resolves the control-plane database name from the
// boot-time config. The home DB is also the bootstrap/admin connection target,
// so admin maintenance paths must resolve it from the disk-loaded snapshot —
// runtime sys.config_param rows must never retarget admin traffic mid-process.
func BootstrapHomeDBName() string {
	return BootstrapPostgresqlConfig().ResolvedHomeDBName()
}

type KafkaConfig struct {
	Enable     bool     `mapstructure:"enable" yaml:"enable"`
	IsProducer bool     `mapstructure:"is_producer" yaml:"isProducer"`
	IsComsumer bool     `mapstructure:"is_comsumer" yaml:"isComsumer"`
	UseDedupe  bool     `mapstructure:"use_dedupe" yaml:"useDedupe"`
	Brokers    []string `mapstructure:"brokers" yaml:"brokers"` // multi-broker
	Topics     []string `mapstructure:"topics" yaml:"topics"`   // multi topic
}

type LoggingConfig struct {
	Level   int    `mapstructure:"level" yaml:"level"`
	Pattern string `mapstructure:"pattern" yaml:"pattern"`
	UseFile bool   `mapstructure:"use_file" yaml:"useFile"`
	Path    string `mapstructure:"path" yaml:"path"`
	ReqPath string `mapstructure:"req_path" yaml:"reqPath"`
}

type KnowledgeConfig struct {
	// PublicDocsPath is the tenant-relative leaf folder for user-authored,
	// writable knowledge documents; the full storage root is always derived
	// as <SharedFolder>/<tenant db name>/<PublicDocsPath>, never a standalone
	// path.
	PublicDocsPath string `mapstructure:"public_docs_path" yaml:"publicDocsPath"`
}

type CORS struct {
	AllowOrigins     string `mapstructure:"allow_origins" yaml:"allowOrigins"`
	AllowMethods     string `mapstructure:"allow_methods" yaml:"allowMethods"`
	AllowHeaders     string `mapstructure:"allow_headers" yaml:"allowHeaders"`
	ExposeHeaders    string `mapstructure:"expose_headers" yaml:"exposeHeaders"`
	AllowCredentials bool   `mapstructure:"allow_credentials" yaml:"allowCredentials"`
}

func (cfg AppConfig) AppName() string {
	return strings.TrimSpace(cfg.App.Name)
}

func (cfg AppConfig) AppVersion() string {
	return strings.TrimSpace(cfg.App.Version)
}

func (cfg AppConfig) AppEnv() string {
	return strings.TrimSpace(cfg.App.Env)
}

func (cfg AppConfig) InitialImportEnabled() bool {
	return cfg.App.InitImport
}

// MultiTenantEnabled reports the configured tenancy mode (app.use_multi_tenant).
// false runs single-tenant: callers must skip template-database provisioning
// and modules/database installation for the home DB.
func (cfg AppConfig) MultiTenantEnabled() bool {
	return cfg.App.UseMultiTenant
}

// DefaultAppLanguages is used when app.language is unset or has no valid code.
const DefaultAppLanguages = "en_us,vi_vn"

// AppLanguages parses app.language ("en_us,vi_vn,ja_jp") into an ordered, de-duplicated
// list of language codes. Blank entries are dropped; an empty result falls back
// to DefaultAppLanguages so the switcher always has at least one option.
func (cfg AppConfig) AppLanguages() []string {
	seen := map[string]struct{}{}
	langs := []string{}
	for _, raw := range strings.Split(cfg.App.Language, ",") {
		code := i18n.NormalizeLang(raw)
		if code == "" {
			continue
		}
		if _, dup := seen[code]; dup {
			continue
		}
		seen[code] = struct{}{}
		langs = append(langs, code)
	}
	if len(langs) == 0 {
		return strings.Split(DefaultAppLanguages, ",")
	}
	return langs
}

// MultiTenantEnabled reads the tenancy mode from the runtime config.
func MultiTenantEnabled() bool {
	return Get().MultiTenantEnabled()
}

// DefaultKnowledgePublicDocsPath is used when Knowledge.PublicDocsPath is unset.
const DefaultKnowledgePublicDocsPath = "public_docs"

// KnowledgePublicDocsRoot returns the tenant-scoped filesystem root for
// user-authored, writable knowledge documents (the "public" namespace):
// <SharedFolder>/<dbName>/<publicDocsPath>. The storage root is always
// tenant-scoped so one tenant's edits can never land in another tenant's
// document tree.
func (cfg AppConfig) KnowledgePublicDocsRoot(dbName string) string {
	docsPath := strings.TrimSpace(cfg.Knowledge.PublicDocsPath)
	if docsPath == "" {
		docsPath = DefaultKnowledgePublicDocsPath
	}
	return filepath.Join(ResolvedSharedFolder(), strings.TrimSpace(dbName), docsPath)
}

// TCPConfig configures the raw TCP transport used by workflow `tcp` triggers
// (internal/services/tcpserver). Off by default; TLS key material stays in config files and is never
// stored in the database.
type TCPConfig struct {
	UseTCP          bool     `mapstructure:"use_tcp"            yaml:"useTcp"`
	Bind            string   `mapstructure:"bind"               yaml:"bind"`           // "" -> 0.0.0.0
	DefaultPort     int      `mapstructure:"default_port"       yaml:"defaultPort"`    // 0 -> 10123
	PortMin         int      `mapstructure:"port_min"           yaml:"portMin"`        // 0 -> 10000
	PortMax         int      `mapstructure:"port_max"           yaml:"portMax"`        // 0 -> 49999
	MaxConns        int      `mapstructure:"max_conns"          yaml:"maxConns"`       // per listener, 0 -> 256
	MaxConnsPerIP   int      `mapstructure:"max_conns_per_ip"   yaml:"maxConnsPerIp"`  // 0 -> 32
	MaxFrameBytes   int      `mapstructure:"max_frame_bytes"    yaml:"maxFrameBytes"`  // 0 -> 4096
	FirstFrameS     int      `mapstructure:"first_frame_s"      yaml:"firstFrameS"`    // 0 -> 10
	IdleTimeoutS    int      `mapstructure:"idle_timeout_s"     yaml:"idleTimeoutS"`   // 0 -> 120
	AllowCIDRs      []string `mapstructure:"allow_cidrs"        yaml:"allowCidrs"`     // optional network gate
	ProxyProtocol   string   `mapstructure:"proxy_protocol"     yaml:"proxyProtocol"`  // none|v1|v2|auto, "" -> none
	TrustedProxies  []string `mapstructure:"trusted_proxies"    yaml:"trustedProxies"` // CIDRs allowed to send a PROXY header
	TLSCertFile     string   `mapstructure:"tls_cert_file"      yaml:"tlsCertFile"`
	TLSKeyFile      string   `mapstructure:"tls_key_file"       yaml:"tlsKeyFile"`
	TLSClientCAFile string   `mapstructure:"tls_client_ca_file" yaml:"tlsClientCaFile"` // enables mtls
}

// Default TCP transport values applied by WithDefaults.
const (
	DefaultTCPPort          = 10123
	DefaultTCPPortMin       = 10000
	DefaultTCPPortMax       = 49999
	defaultTCPMaxConns      = 256
	defaultTCPMaxConnsPerIP = 32
	defaultTCPMaxFrameBytes = 4096
	defaultTCPFirstFrameS   = 10
	defaultTCPIdleTimeoutS  = 120
)

// WithDefaults returns a copy with every zero numeric/string setting replaced by its default.
func (c TCPConfig) WithDefaults() TCPConfig {
	if c.Bind == "" {
		c.Bind = "0.0.0.0"
	}
	if c.DefaultPort == 0 {
		c.DefaultPort = DefaultTCPPort
	}
	if c.PortMin == 0 {
		c.PortMin = DefaultTCPPortMin
	}
	if c.PortMax == 0 {
		c.PortMax = DefaultTCPPortMax
	}
	if c.MaxConns == 0 {
		c.MaxConns = defaultTCPMaxConns
	}
	if c.MaxConnsPerIP == 0 {
		c.MaxConnsPerIP = defaultTCPMaxConnsPerIP
	}
	if c.MaxFrameBytes == 0 {
		c.MaxFrameBytes = defaultTCPMaxFrameBytes
	}
	if c.FirstFrameS == 0 {
		c.FirstFrameS = defaultTCPFirstFrameS
	}
	if c.IdleTimeoutS == 0 {
		c.IdleTimeoutS = defaultTCPIdleTimeoutS
	}
	if c.ProxyProtocol == "" {
		c.ProxyProtocol = "none"
	}
	return c
}

// Validate checks the (defaulted) settings: a usable port range and a default port inside it.
func (c TCPConfig) Validate() error {
	c = c.WithDefaults()
	if c.PortMin < 1 || c.PortMax > 65535 || c.PortMin > c.PortMax {
		return fmt.Errorf("tcp: port range [%d,%d] is invalid", c.PortMin, c.PortMax)
	}
	if c.DefaultPort < c.PortMin || c.DefaultPort > c.PortMax {
		return fmt.Errorf("tcp: defaultPort %d outside [%d,%d]", c.DefaultPort, c.PortMin, c.PortMax)
	}
	return nil
}
