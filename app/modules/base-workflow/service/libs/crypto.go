package libs

// File overview:
// - Responsibility: houses workflow-local cryptographic helpers used by module-specific signing, encryption, and payload canonicalization flows.
// - Key symbols: generateSecretHex, hmacSHA256, encryptAESCBC, buildQueryString.
// - Pattern: Keeps payment- and delivery-oriented crypto logic close to the workflow module so business-specific signing rules do not leak into shared utility packages.

// crypto.go contains self-contained signing and encryption helpers so the
// workflow service does not import the shared utils package for operations that
// belong purely to this module's business domain.

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"git.thebase.vn/base_hub/internal/services/log"
	"sort"
)

func generateSecretHex(length int) (string, error) {
	if length <= 0 {
		length = 32
	}
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		log.Errorf("generate secret: %v", err)
		return "", fmt.Errorf("generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func hmacSHA256(secret, message string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(message))
	return hex.EncodeToString(h.Sum(nil))
}

func encryptAESCBC(secretKey, plainText string) (string, error) {
	key, err := hex.DecodeString(secretKey)
	if err != nil {
		log.Errorf("aes: decode key: %v", err)
		return "", fmt.Errorf("aes: decode key: %w", err)
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		log.Errorf("aes: key must decode to 16, 24, or 32 bytes")
		return "", fmt.Errorf("aes: key must decode to 16, 24, or 32 bytes")
	}
	padded := pkcs7Pad([]byte(plainText), aes.BlockSize)
	block, err := aes.NewCipher(key)
	if err != nil {
		log.Errorf("aes: new cipher: %v", err)
		return "", fmt.Errorf("aes: new cipher: %w", err)
	}
	iv := make([]byte, aes.BlockSize) // zero IV — matches legacy behaviour
	mode := cipher.NewCBCEncrypter(block, iv)
	cipherText := make([]byte, len(padded))
	mode.CryptBlocks(cipherText, padded)
	return base64.StdEncoding.EncodeToString(cipherText), nil
}

// decryptAESCBC decrypts a base64 AES-CBC payload using the same legacy zero IV convention.
func decryptAESCBC(secretKey, cipherText string) (string, error) {
	key, err := hex.DecodeString(secretKey)
	if err != nil {
		log.Errorf("aes: decode key: %v", err)
		return "", fmt.Errorf("aes: decode key: %w", err)
	}
	if len(key) != 16 && len(key) != 24 && len(key) != 32 {
		log.Errorf("aes: key must decode to 16, 24, or 32 bytes")
		return "", fmt.Errorf("aes: key must decode to 16, 24, or 32 bytes")
	}

	rawCipherText, err := base64.StdEncoding.DecodeString(cipherText)
	if err != nil {
		log.Errorf("aes: decode ciphertext: %v", err)
		return "", fmt.Errorf("aes: decode ciphertext: %w", err)
	}
	if len(rawCipherText) == 0 || len(rawCipherText)%aes.BlockSize != 0 {
		log.Errorf("aes: ciphertext must be a non-empty multiple of block size")
		return "", fmt.Errorf("aes: ciphertext must be a non-empty multiple of block size")
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		log.Errorf("aes: new cipher: %v", err)
		return "", fmt.Errorf("aes: new cipher: %w", err)
	}
	iv := make([]byte, aes.BlockSize) // zero IV — matches legacy behaviour
	mode := cipher.NewCBCDecrypter(block, iv)
	plainText := make([]byte, len(rawCipherText))
	mode.CryptBlocks(plainText, rawCipherText)

	unpadded, err := pkcs7Unpad(plainText, aes.BlockSize)
	if err != nil {
		log.Errorf("aes: unpad plaintext: %v", err)
		return "", fmt.Errorf("aes: unpad plaintext: %w", err)
	}
	return string(unpadded), nil
}

// buildQueryString produces "key1=val1&key2=val2" from the supplied map.
// If keys is empty, all map keys are used in sorted order.
func buildQueryString(data map[string]interface{}, keys []string) (string, error) {
	if len(keys) == 0 {
		for k := range data {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	query := ""
	for i, key := range keys {
		val, ok := data[key]
		if !ok {
			log.Errorf("key %q not found in payload", key)
			return "", fmt.Errorf("key %q not found in payload", key)
		}
		if i > 0 {
			query += "&"
		}
		query += fmt.Sprintf("%s=%v", key, val)
	}
	return query, nil
}

func pkcs7Pad(data []byte, blockSize int) []byte {
	padding := blockSize - len(data)%blockSize
	pad := bytes.Repeat([]byte{byte(padding)}, padding)
	return append(data, pad...)
}

// pkcs7Unpad removes PKCS#7 padding after AES-CBC decrypt.
func pkcs7Unpad(data []byte, blockSize int) ([]byte, error) {
	if len(data) == 0 || len(data)%blockSize != 0 {
		log.Errorf("aes: invalid padded plaintext length")
		return nil, fmt.Errorf("aes: invalid padded plaintext length")
	}
	padding := int(data[len(data)-1])
	if padding <= 0 || padding > blockSize || padding > len(data) {
		log.Errorf("aes: invalid padding length")
		return nil, fmt.Errorf("aes: invalid padding length")
	}
	for _, b := range data[len(data)-padding:] {
		if int(b) != padding {
			log.Errorf("aes: invalid padding content")
			return nil, fmt.Errorf("aes: invalid padding content")
		}
	}
	return data[:len(data)-padding], nil
}
