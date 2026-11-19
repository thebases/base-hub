package utils

// File overview:
// - Responsibility: collects shared helper logic for utils, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `StructToMap`, `ToTimestamp`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"strconv"
	"time"

	"git.thebase.vn/base_hub/internal/services/log"
)

const randomAlphaNumericAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
const randomNumericAlphabet = "0123456789"

// StructToMap converts any struct into a map[string]interface{}
// using JSON tags as keys.
func StructToMap[T any](dto T) (map[string]interface{}, error) {
	data, err := json.Marshal(dto)
	if err != nil {
		return nil, err
	}

	var result map[string]interface{}
	err = json.Unmarshal(data, &result)
	return result, err
}

// convert time like "2025-08-19T14:39:40+07:00" to timestamp
func ToTimestamp(datetimeStr string) int64 {
	// Parse the datetime string with timezone
	t, err := time.Parse(time.RFC3339, datetimeStr)
	if err != nil {
		log.Debugf("ToTimestamp failed: %s", err.Error())
		return 0
	}
	// Convert to Unix timestamp
	return t.Unix()
}

// convert time like "2025-08-19T14:39:40+07:00" to timestamp
func Str2Int(amount string) int {
	// Parse the datetime string with timezone
	t, err := strconv.Atoi(amount)
	if err != nil {
		log.Debugf("To Int failed: %s", err.Error())
		return 0
	}
	// Convert to Unix timestamp
	return t
}

// RandomHexString returns a crypto-random hex string from the requested number of bytes.
// For example, length=8 returns 8 random bytes encoded as 16 hex characters.
func RandomHexString(length int) (string, error) {
	if length <= 0 {
		return "", nil
	}
	out := make([]byte, length)
	if _, err := rand.Read(out); err != nil {
		return "", err
	}
	return hex.EncodeToString(out), nil
}

// RandomAlphaNumericString returns a crypto-random alphanumeric string with the requested length.
func RandomAlphaNumericString(length int) (string, error) {
	return randomStringFromAlphabet(length, randomAlphaNumericAlphabet)
}

// RandomNumericString returns a crypto-random digits-only string with the requested length.
func RandomNumericString(length int) (string, error) {
	return randomStringFromAlphabet(length, randomNumericAlphabet)
}

func randomStringFromAlphabet(length int, alphabet string) (string, error) {
	if length <= 0 || len(alphabet) == 0 {
		return "", nil
	}
	out := make([]byte, length)
	limit := big.NewInt(int64(len(alphabet)))
	for index := range out {
		next, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", err
		}
		out[index] = alphabet[next.Int64()]
	}
	return string(out), nil
}
