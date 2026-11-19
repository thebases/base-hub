package service

// File overview:
// - Responsibility: manages the RSA keypair used to sign/verify builtin OAuth 2.0 access tokens.
//   The private key is generated on first boot if absent and persisted in `sys_config_param` on
//   the home control-plane DB (never in the YAML config file), the same storage convention used
//   by internal/services/filestore for other runtime-only secrets (RBAC/OAuth backend plan, §3).
//   Key rotation stays a documented manual op for v1: replace the stored PEM, keep the old public
//   key valid for the outstanding access-token TTL, then drop it — not automated here.
// - Key symbols: `SigningKey`, `LoadOrCreateSigningKey`.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"github.com/uptrace/bun"
)

const (
	oauthPrivateKeyConfigParam       = "auth_service.rsa_private_key"
	oauthKeyIDConfigParam            = "auth_service.rsa_key_id"
	legacyOAuthPrivateKeyConfigParam = "auth.oauth.rsa_private_key"
	legacyOAuthKeyIDConfigParam      = "auth.oauth.rsa_key_id"
	oauthRSAKeyBits                  = 2048
)

// SigningKey bundles the keypair used to sign and verify builtin access tokens.
type SigningKey struct {
	KeyID      string
	PrivateKey *rsa.PrivateKey
	PublicKey  *rsa.PublicKey
}

var (
	// signingKeyMu guards signingKeyValue and is held across the first load, so concurrent
	// callers wait for one load instead of racing several key generations in this process.
	signingKeyMu    sync.Mutex
	signingKeyValue *SigningKey
	// loadSigningKey is the home-DB loader; tests swap it to exercise the caching rules.
	loadSigningKey = loadOrCreateSigningKeyFromHomeDB
)

// LoadOrCreateSigningKey returns the process-wide signing key, generating and
// persisting one on first use if none is stored yet. Safe for concurrent use.
// Only a successful load is cached: a failed one (home DB not up yet, cancelled
// ctx) is retried by the next caller instead of disabling token issuance until
// restart. SetSigningKeyForTests overrides it.
func LoadOrCreateSigningKey(ctx context.Context) (*SigningKey, error) {
	signingKeyMu.Lock()
	defer signingKeyMu.Unlock()
	if signingKeyValue != nil {
		return signingKeyValue, nil
	}
	key, err := loadSigningKey(ctx)
	if err != nil {
		return nil, err
	}
	signingKeyValue = key
	return key, nil
}

// SetSigningKeyForTests overrides the process-wide signing key so DB-free
// unit tests can mint/validate builtin OAuth JWTs without a live Postgres
// connection to load or persist the RSA key from sys_config_param (the
// production path in loadOrCreateSigningKeyFromHomeDB). Returns a restore
// func the caller must invoke (typically via t.Cleanup) once done. Test-only:
// production issuance always goes through LoadOrCreateSigningKey's home-DB
// path; this bypasses it entirely while an override is active.
func SetSigningKeyForTests(key *SigningKey) (restore func()) {
	signingKeyMu.Lock()
	prevValue := signingKeyValue
	signingKeyValue = key
	signingKeyMu.Unlock()
	return func() {
		signingKeyMu.Lock()
		signingKeyValue = prevValue
		signingKeyMu.Unlock()
	}
}

// NewEphemeralSigningKeyForTests generates a fresh in-memory RSA keypair with
// no DB or filesystem persistence, for use with SetSigningKeyForTests — every
// test process gets its own throwaway key.
func NewEphemeralSigningKeyForTests() (*SigningKey, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, oauthRSAKeyBits)
	if err != nil {
		return nil, fmt.Errorf("auth: generate ephemeral test signing key: %w", err)
	}
	return &SigningKey{KeyID: "test", PrivateKey: privateKey, PublicKey: &privateKey.PublicKey}, nil
}

func loadOrCreateSigningKeyFromHomeDB(ctx context.Context) (*SigningKey, error) {
	homeDBName := strings.TrimSpace(config.HomeDBName())
	registered, err := orm.EnsureNamedBunDB(homeDBName)
	if err != nil {
		return nil, fmt.Errorf("auth: resolve home database for signing key: %w", err)
	}
	return loadOrCreateSigningKeyFromDB(ctx, registered.DB)
}

// loadOrCreateSigningKeyFromDB reads the stored signing key, creating it when absent. Every
// write is insert-if-absent followed by a re-read, so nodes booting together converge on the
// single stored key instead of each overwriting the other's and invalidating its tokens.
func loadOrCreateSigningKeyFromDB(ctx context.Context, db *bun.DB) (*SigningKey, error) {
	rawKey, err := readConfigParamString(ctx, db, oauthPrivateKeyConfigParam)
	if err != nil {
		return nil, fmt.Errorf("auth: read signing key config: %w", err)
	}
	if rawKey == "" {
		// During the config namespace migration, preserve already-deployed
		// signing keys by reading the legacy auth.oauth.* rows once and copying
		// them forward. Losing this key would invalidate every outstanding JWT.
		legacyKey, err := readConfigParamString(ctx, db, legacyOAuthPrivateKeyConfigParam)
		if err != nil {
			return nil, fmt.Errorf("auth: read legacy signing key config: %w", err)
		}
		keyID := "default"
		if legacyKey != "" {
			if legacyKeyID, _ := readConfigParamString(ctx, db, legacyOAuthKeyIDConfigParam); legacyKeyID != "" {
				keyID = legacyKeyID
			}
		} else {
			// Nothing stored yet: generate one. If another node inserts first, ours is discarded.
			privateKey, err := rsa.GenerateKey(rand.Reader, oauthRSAKeyBits)
			if err != nil {
				return nil, fmt.Errorf("auth: generate signing key: %w", err)
			}
			legacyKey = encodeSigningKey(privateKey)
		}
		// The key id goes first: a node that sees the key row must also see its id, or it
		// would sign with the right key under the wrong kid.
		if err := insertConfigParamIfAbsent(ctx, db, oauthKeyIDConfigParam, keyID); err != nil {
			return nil, fmt.Errorf("auth: persist signing key id: %w", err)
		}
		if err := insertConfigParamIfAbsent(ctx, db, oauthPrivateKeyConfigParam, legacyKey); err != nil {
			return nil, fmt.Errorf("auth: persist signing key: %w", err)
		}
		if rawKey, err = readConfigParamString(ctx, db, oauthPrivateKeyConfigParam); err != nil {
			return nil, fmt.Errorf("auth: re-read signing key config: %w", err)
		}
		if rawKey == "" {
			return nil, fmt.Errorf("auth: signing key missing after insert")
		}
	}
	rawKeyID, _ := readConfigParamString(ctx, db, oauthKeyIDConfigParam)
	key, err := decodeSigningKey(rawKey)
	if err != nil {
		return nil, fmt.Errorf("auth: decode stored signing key: %w", err)
	}
	if rawKeyID == "" {
		rawKeyID = "default"
	}
	return &SigningKey{KeyID: rawKeyID, PrivateKey: key, PublicKey: &key.PublicKey}, nil
}

func readConfigParamString(ctx context.Context, db *bun.DB, name string) (string, error) {
	var raw string
	if err := db.NewRaw(`SELECT value::text FROM sys_config_param WHERE name = ? LIMIT 1`, name).Scan(ctx, &raw); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return unquoteJSONConfigValue(raw), nil
}

// insertConfigParamIfAbsent stores one sys_config_param row unless the name already exists.
// It never overwrites: runtime secrets are generated once, and a second node that loses the
// insert race must adopt the stored value (callers re-read), not replace it.
func insertConfigParamIfAbsent(ctx context.Context, db *bun.DB, name, value string) error {
	// sys_config_param's canonical physical shape is (name, value) only —
	// see internal/config/config.go's systemConfigRow / ensureSystemConfigTable,
	// which is authoritative for already-deployed databases. No created_at/
	// updated_at columns exist there.
	_, err := db.ExecContext(ctx, `
		INSERT INTO sys_config_param (name, value)
		VALUES (?, to_jsonb(?::text))
		ON CONFLICT (name) DO NOTHING
	`, name, value)
	return err
}

// encodeSigningKey PEM-encodes the private key, then base64s the PEM text so
// it survives the jsonb round trip as an opaque string rather than a
// quote-stripping-fragile JSON string (RBAC/OAuth backend plan §5 note).
func encodeSigningKey(key *rsa.PrivateKey) string {
	pemBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	return base64.StdEncoding.EncodeToString(pem.EncodeToMemory(pemBlock))
}

func decodeSigningKey(encoded string) (*rsa.PrivateKey, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM block")
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

// unquoteJSONConfigValue strips the surrounding quotes jsonb::text adds
// around a stored string value (same caveat internal/services/filestore.go
// documents for the same query pattern).
func unquoteJSONConfigValue(raw string) string {
	return strings.Trim(strings.TrimSpace(raw), `"`)
}
