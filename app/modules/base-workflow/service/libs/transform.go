package libs

// File overview:
// - Responsibility: owns shared `workflow` module payload helpers used by transform, crypto, and action runtime flows.
// - Key symbols: `FlattenJSON`, `HmacAction`, `AesAction`, `DecryptAesAction`.
// - Pattern: Threads plain Go maps through helper paths so workflow actions can reuse the same normalization and crypto behavior.

import (
	"encoding/json"
	"fmt"

	"git.thebase.vn/base_hub/internal/services/log"
)

// FlattenJSON flattens the json handled by this package.
func FlattenJSON(data map[string]interface{}, result map[string]interface{}) {
	for key, value := range data {
		switch v := value.(type) {
		case map[string]interface{}:
			FlattenJSON(v, result)
		default:
			result[key] = v
		}
	}
}

// HmacAction computes an HMAC-SHA256 signature over the JSON payload fields.
// If key is empty, a random 32-byte hex key is generated.
func HmacAction(key string, jsonData map[string]interface{}, keyList []string) (string, string, string, error) {
	if len(key) == 0 {
		newKey, err := generateSecretHex(32)
		if err != nil {
			return "", "", "", err
		}
		key = newKey
	}
	queryStr, err := buildQueryString(jsonData, keyList)
	if err != nil {
		return "", "", "", err
	}
	return key, queryStr, hmacSHA256(key, queryStr), nil
}

// AesAction encrypts the JSON payload with AES-CBC.
// If key is empty, a random 32-byte hex key is generated.
func AesAction(key string, jsonData map[string]interface{}) (string, string, string, error) {
	if len(key) == 0 {
		newKey, err := generateSecretHex(32)
		if err != nil {
			log.Errorf("can't generate new key: %v", err)
			return "", "", "", fmt.Errorf("can't generate new key: %w", err)
		}
		key = newKey
	}
	minified, err := json.Marshal(jsonData)
	if err != nil {
		return "", "", "", err
	}
	encryptedData, err := encryptAESCBC(key, string(minified))
	if err != nil {
		return "", "", "", err
	}
	return key, string(minified), encryptedData, nil
}

// DecryptAesAction decrypts an AES-CBC payload back into its minified JSON string.
func DecryptAesAction(key string, encryptedData string) (string, error) {
	if len(key) == 0 {
		log.Errorf("aes decrypt: key is required")
		return "", fmt.Errorf("aes decrypt: key is required")
	}
	return decryptAESCBC(key, encryptedData)
}
