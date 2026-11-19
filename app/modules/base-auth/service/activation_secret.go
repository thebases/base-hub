package service

// File overview:
// - Responsibility: manages the server-only HMAC key used to hash activation codes at rest.
//   Generated on first boot if absent and persisted in `sys_config_param` on the home
//   control-plane DB, the same runtime-secret convention keys.go uses for the OAuth signing key —
//   never written to the YAML config file.
// - Key symbols: `LoadOrCreateActivationCodeSecret`.

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/utils"
	"github.com/uptrace/bun"
)

const activationCodeSecretConfigParam = "base_auth.activation_code_secret"

var (
	// activationSecretMu guards activationSecretValue and is held across the first load, so
	// concurrent callers share one load instead of generating competing secrets.
	activationSecretMu    sync.Mutex
	activationSecretValue string
	// loadActivationSecret is the home-DB loader; tests swap it to exercise the caching rules.
	loadActivationSecret = loadOrCreateActivationCodeSecretFromHomeDB
)

// LoadOrCreateActivationCodeSecret returns the process-wide activation-code
// HMAC secret, generating and persisting one on first use if none is stored
// yet. Safe for concurrent use. Only a successful load is cached, so a failed
// first load (home DB not up yet) is retried by the next caller.
// Tests override it via SetActivationCodeSecretForTests (runtime_secret_cache_test.go).
func LoadOrCreateActivationCodeSecret(ctx context.Context) (string, error) {
	activationSecretMu.Lock()
	defer activationSecretMu.Unlock()
	if activationSecretValue != "" {
		return activationSecretValue, nil
	}
	secret, err := loadActivationSecret(ctx)
	if err != nil {
		return "", err
	}
	activationSecretValue = secret
	return secret, nil
}

func loadOrCreateActivationCodeSecretFromHomeDB(ctx context.Context) (string, error) {
	homeDBName := strings.TrimSpace(config.HomeDBName())
	registered, err := orm.EnsureNamedBunDB(homeDBName)
	if err != nil {
		return "", fmt.Errorf("activation: resolve home database for code secret: %w", err)
	}
	return loadOrCreateActivationCodeSecretFromDB(ctx, registered.DB)
}

// loadOrCreateActivationCodeSecretFromDB reads the stored secret, creating it when absent.
// The insert never overwrites and is followed by a re-read, so nodes booting together all
// adopt the one stored secret; otherwise codes issued on one node would fail on another.
func loadOrCreateActivationCodeSecretFromDB(ctx context.Context, db *bun.DB) (string, error) {
	existing, err := readConfigParamString(ctx, db, activationCodeSecretConfigParam)
	if err != nil {
		return "", fmt.Errorf("activation: read code secret config: %w", err)
	}
	if existing != "" {
		return existing, nil
	}
	secret, err := utils.GenerateSecretHex(32)
	if err != nil {
		return "", fmt.Errorf("activation: generate code secret: %w", err)
	}
	if err := insertConfigParamIfAbsent(ctx, db, activationCodeSecretConfigParam, secret); err != nil {
		return "", fmt.Errorf("activation: persist code secret: %w", err)
	}
	stored, err := readConfigParamString(ctx, db, activationCodeSecretConfigParam)
	if err != nil {
		return "", fmt.Errorf("activation: re-read code secret config: %w", err)
	}
	if stored == "" {
		return "", fmt.Errorf("activation: code secret missing after insert")
	}
	return stored, nil
}
