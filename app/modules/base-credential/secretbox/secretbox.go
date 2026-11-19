package secretbox

// File overview:
// - Responsibility: encrypts stored secrets (sys.credential, base.workflow.credential) under a key
//   derived from `app.encryptKey`, re-encrypts them for the zero-key migration and for the
//   `rotate-encrypt-key` CLI, and strips secret columns from runtime read results.
// - Key symbols: `Seal`, `Open`, `Reseal`, `ResealColumns`, `AppKey`, `RedactedReadMethods`.
// - Format: "v1:" + base64(nonce || AES-256-GCM ciphertext). The AES key is
//   HKDF-SHA256(app.encryptKey, info "base-hub/secretbox/<label>"), so each model gets its own key.
//   A value without the prefix is legacy: written before 2026-11-14 under an all-zero key because
//   the old master-key setters were never called. Open still reads legacy values so a row the boot
//   migration has not reached keeps working; every new write uses v1.
// - Key handling: the key is read from config on each call (file-only, never stored in a DB) and
//   is never logged. An empty key fails closed with ErrKeyNotConfigured.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"

	"git.thebase.vn/base_hub/internal/config"
)

const versionPrefix = "v1:"

// ErrKeyNotConfigured means app.encryptKey is empty, so nothing can be encrypted or decrypted.
var ErrKeyNotConfigured = errors.New("app.encryptKey is not configured")

// AppKey returns the configured app.encryptKey ("" when unset).
func AppKey() string {
	return strings.TrimSpace(config.Get().App.EncryptKey)
}

// IsCurrent reports whether value is in the current (v1, app-key) format.
func IsCurrent(value string) bool {
	return strings.HasPrefix(value, versionPrefix)
}

func deriveKey(appKey, label string) ([]byte, error) {
	appKey = strings.TrimSpace(appKey)
	if appKey == "" {
		return nil, ErrKeyNotConfigured
	}
	return hkdf.Key(sha256.New, []byte(appKey), nil, "base-hub/secretbox/"+label, 32)
}

// Seal encrypts plaintext for label under appKey.
func Seal(appKey, label, plaintext string) (string, error) {
	key, err := deriveKey(appKey, label)
	if err != nil {
		return "", err
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("secretbox: generate nonce: %w", err)
	}
	sealed := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return versionPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a value written by Seal for label under appKey, or a legacy zero-key value.
func Open(appKey, label, value string) (string, error) {
	if encoded, ok := strings.CutPrefix(value, versionPrefix); ok {
		key, err := deriveKey(appKey, label)
		if err != nil {
			return "", err
		}
		return openGCM(key, encoded)
	}
	var legacyZeroKey [32]byte
	return openGCM(legacyZeroKey[:], value)
}

// Reseal returns value encrypted under newKey. A v1 value that already opens under newKey is
// returned unchanged (changed=false), so a rerun is a no-op. Otherwise the value is opened under
// oldKey (or the legacy zero key) and sealed again under newKey. The zero-key migration passes the
// current key as both oldKey and newKey.
func Reseal(oldKey, newKey, label, value string) (resealed string, changed bool, err error) {
	if strings.TrimSpace(value) == "" {
		return value, false, nil
	}
	if IsCurrent(value) {
		if _, err := Open(newKey, label, value); err == nil {
			return value, false, nil
		}
	}
	plaintext, err := Open(oldKey, label, value)
	if err != nil {
		return "", false, err
	}
	resealed, err = Seal(newKey, label, plaintext)
	if err != nil {
		return "", false, err
	}
	return resealed, true, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("secretbox: %w", err)
	}
	return gcm, nil
}

func openGCM(key []byte, encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("secretbox: decode base64: %w", err)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	if len(raw) < gcm.NonceSize() {
		return "", fmt.Errorf("secretbox: ciphertext too short")
	}
	nonce, sealed := raw[:gcm.NonceSize()], raw[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, sealed, nil)
	if err != nil {
		return "", fmt.Errorf("secretbox: decrypt: %w", err)
	}
	return string(plaintext), nil
}
