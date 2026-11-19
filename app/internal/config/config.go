package config

// File overview:
// - Responsibility: owns configuration loading and normalization so every backend subsystem reads the same operational contract.
// - Key symbols: `current`, `init`, `Get`, `Set`.

import (
	"log"
	"os"
	"sync/atomic"

	"github.com/spf13/viper"
	"github.com/uptrace/bun"
	"gopkg.in/yaml.v3"
)

// Config structs

// =========== GLOBAL VARIANT ==========

// current is the process-wide runtime config. It is replaced as a whole
// (SyncRuntimeConfig can run while requests read it), so readers must go
// through Get and never mutate the returned value.
var current atomic.Pointer[AppConfig]

func init() { current.Store(&AppConfig{}) }

// Get returns the current config snapshot. Treat it as read-only.
func Get() *AppConfig { return current.Load() }

// Set publishes cfg as the new config snapshot.
func Set(cfg AppConfig) { current.Store(&cfg) }

// Update copies the current snapshot, applies fn and publishes the result.
// Intended for tests and boot-time overrides; concurrent Updates are last-writer-wins.
func Update(fn func(*AppConfig)) {
	next := *current.Load()
	fn(&next)
	current.Store(&next)
}

var bootstrapConfig AppConfig
var bootstrapConfigLoaded bool
var loadedConfigPath string
var loadedConfigSettings map[string]any

const (
	systemConfigTable       = "sys_config_param"
	legacySystemConfigTable = "sys_config"
	initialImportConfigKey  = "init_import"
)

type systemConfigRow struct {
	bun.BaseModel `bun:"table:sys_config_param"`
	Name          string `bun:"name,unique" json:"name"`
	Value         any    `bun:"value,type:jsonb,nullzero" json:"value"`
}

// =========== CONFIC FUNCTIONS ==========

// LoadConfig reads config from the YAML file only. Environment variables are deliberately not
// consulted (no viper.AutomaticEnv): every runtime setting must live in the config, required
// settings under `app`, so the file/sys.config_param is the single auditable source.
func LoadConfig(configPath string) {
	viper.SetConfigFile(configPath)

	if err := viper.ReadInConfig(); err != nil {
		log.Fatalf("Error reading config file: %v", err)
	}

	// Keep the raw settings map so we can seed or repair sys.config_param later.
	loadedConfigSettings = normalizeConfigStructure(canonicalizeConfigMapKeys(normalizeConfigValue(viper.AllSettings())).(map[string]any))

	// Decode from the canonicalized map so snake_case stays the source of truth
	// while legacy camelCase files still load during the migration window.
	if err := decodeConfigMap(cloneConfigMap(loadedConfigSettings)); err != nil {
		log.Fatalf("Error unmarshalling config: %v", err)
	}
	bootstrapConfig = *Get()
	bootstrapConfigLoaded = true

	// Keep the source path so runtime config can be written back to disk later.
	loadedConfigPath = configPath
	log.Printf("Loaded config: %+v\n", redactedForLog(*Get()))
}

// redactedForLog returns a copy of cfg with every secret replaced, for the boot log
// (journal/stdout). Add new secret config fields here.
func redactedForLog(cfg AppConfig) AppConfig {
	redact := func(v *string) {
		if *v != "" {
			*v = "[REDACTED]"
		}
	}
	redact(&cfg.App.EncryptKey)
	redact(&cfg.App.DiscussInboundWebhookSecret)
	redact(&cfg.MqttClient.Password)
	redact(&cfg.AuthService.ClientSecret)
	redact(&cfg.PostgresqlDb.Password)
	if len(cfg.PostgresqlDb.Databases) > 0 {
		dbs := make(map[string]PostgresqlConfig, len(cfg.PostgresqlDb.Databases))
		for name, db := range cfg.PostgresqlDb.Databases {
			redact(&db.Password)
			dbs[name] = db
		}
		cfg.PostgresqlDb.Databases = dbs
	}
	return cfg
}

// WriteConfigToFile writes the current Config to a specified YAML file
func WriteConfigToFile(path string) {
	data, err := yaml.Marshal(Get())
	if err != nil {
		log.Fatalf("Error marshaling config to YAML: %v", err)
	}

	err = writeConfigFile(path, data)
	if err != nil {
		log.Fatalf("Error writing config file: %v", err)
	}

	log.Printf("Config successfully written to %s\n", path)
}

// writeConfigFile writes a config YAML owner-only: it holds DB/MQTT passwords and
// app.encryptKey. os.WriteFile keeps the mode of an existing file, so chmod as well.
func writeConfigFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

// WriteConfig writes to the originally loaded config path
func WriteConfig() {
	if loadedConfigPath == "" {
		log.Fatal("No config path specified. Load config first.")
	}
	WriteConfigToFile(loadedConfigPath)
}
