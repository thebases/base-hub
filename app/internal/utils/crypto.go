package utils

// File overview:
// - Responsibility: collects shared helper logic for crypto, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `Encode`, `Decode`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"git.thebase.vn/base_hub/internal/services/log"
)

// Encode encodes the encode handled by this package.
func Encode(str string) string {
	return base64.StdEncoding.EncodeToString([]byte(str))
}

// Decode decodes the decode handled by this package.
func Decode(str string) (string, error) {
	data, err := base64.StdEncoding.DecodeString(str)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Recommend use length = 32 bytes ~ 64 hex characters ~ 256-bit secret
func GenerateSecretHex(length int) (string, error) {
	bytes := make([]byte, length)
	_, err := rand.Read(bytes)
	if err != nil {
		return "", err
	}
	log.Infof("key: %s", hex.EncodeToString(bytes))
	return hex.EncodeToString(bytes), nil
}

// GenerateHMAC256 generates an HMAC-SHA256 signature
func GenerateHMAC256(secret string, message string) string {
	key := []byte(secret)
	h := hmac.New(sha256.New, key)
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

func DecryptAES(secretKey string, encryptData string) (string, error) {
	// Convert key to 16/24/32 bytes (this assumes it's already of valid length)
	// key := []byte(secretKey)
	key, err := hex.DecodeString(secretKey)
	if err != nil {
		return "", fmt.Errorf("aes: decode key: %w", err)
	}

	// Zero IV (16 bytes)
	iv := make([]byte, aes.BlockSize)

	// Decode Base64 input
	ciphertext, err := base64.StdEncoding.DecodeString(encryptData)
	if err != nil {
		return "", err
	}

	// Create AES cipher block
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	// CBC panics on partial blocks, so reject malformed ciphertext before decrypting.
	if len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return "", fmt.Errorf("aes: ciphertext must be a non-empty multiple of block size")
	}

	// Use CBC mode
	mode := cipher.NewCBCDecrypter(block, iv)
	decrypted := make([]byte, len(ciphertext))
	mode.CryptBlocks(decrypted, ciphertext)

	// Remove PKCS#7 padding
	decrypted = pkcs7Unpad(decrypted)
	return string(decrypted), nil
}

func EncryptAES(secretKey string, plainText string) (string, error) {
	// key := []byte(secretKey)
	key, err := hex.DecodeString(secretKey)
	if err != nil {
		panic(err)
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		log.Errorf("invalid key length: must be 16, 24, or 32 bytes")
		return "", fmt.Errorf("invalid key length: must be 16, 24, or 32 bytes")
	}

	// PKCS#7 padding
	paddedText := pkcs7Pad([]byte(plainText), aes.BlockSize)

	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}

	iv := make([]byte, aes.BlockSize) // zero IV
	mode := cipher.NewCBCEncrypter(block, iv)

	cipherText := make([]byte, len(paddedText))
	mode.CryptBlocks(cipherText, paddedText)
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	pad := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, pad...)
}

// PKCS#7 unpadding (same as PKCS#5)
func pkcs7Unpad(data []byte) []byte {
	length := len(data)
	if length == 0 {
		return data
	}
	paddingLen := int(data[length-1])
	if paddingLen > length {
		return data
	}
	return data[:(length - paddingLen)]
}

// Function for signature a json object by a array key of json object
// BuildQueryStringFromJSON builds a string like "key1=value1&key2=value2" from a JSON map and a list of keys
func BuildQueryStringFromJSON(data map[string]interface{}, keys []string) (string, error) {
	keys = normalizedQueryKeys(data, keys)
	var builder strings.Builder

	for i, key := range keys {
		val, ok := data[key]
		if !ok {
			log.Errorf("key '%s' not found in data", key)
			return "", fmt.Errorf("key '%s' not found in data", key)
		}
		if i > 0 {
			builder.WriteByte('&')
		}
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(fmt.Sprint(val))
	}
	return builder.String(), nil
}

func normalizedQueryKeys(data map[string]interface{}, keys []string) []string {
	if len(keys) > 0 {
		sorted := append([]string(nil), keys...)
		sort.Strings(sorted)
		return sorted
	}
	// Copy map keys once so callers that reuse their input slice do not observe
	// in-place mutation while the query string remains deterministically ordered.
	sorted := make([]string, 0, len(data))
	for key := range data {
		sorted = append(sorted, key)
	}
	sort.Strings(sorted)
	return sorted
}

// SignJSONHMAC builds the canonical query string of data (keyList order, sorted) and signs it with HMAC-SHA256.
// An empty key generates a random 256-bit hex key. Returns (key, rawData, signature).
func SignJSONHMAC(key string, data map[string]interface{}, keyList []string) (string, string, string, error) {
	if key == "" {
		newKey, err := GenerateSecretHex(32)
		if err != nil {
			return "", "", "", err
		}
		key = newKey
	}
	raw, err := BuildQueryStringFromJSON(data, keyList)
	if err != nil {
		return "", "", "", err
	}
	return key, raw, GenerateHMAC256(key, raw), nil
}

// EncryptJSONAES minifies data and encrypts it with AES-CBC. An empty key generates a random 256-bit hex key.
// Returns (key, minifiedJSON, encryptedBase64).
func EncryptJSONAES(key string, data map[string]interface{}) (string, string, string, error) {
	if key == "" {
		newKey, err := GenerateSecretHex(32)
		if err != nil {
			return "", "", "", err
		}
		key = newKey
	}
	minified, err := json.Marshal(data)
	if err != nil {
		return "", "", "", err
	}
	encrypted, err := EncryptAES(key, string(minified))
	if err != nil {
		return "", "", "", err
	}
	return key, string(minified), encrypted, nil
}
