package card

// File overview:
// - Responsibility: collects shared helper logic for card, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `DetectCardType`, `prefixInRange`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"fmt"
	"strings"
)

// DetectCardType returns the card type based on BIN rules
func DetectCardType(cardNumber string) string {
	if len(cardNumber) < 6 {
		return "Unknown"
	}

	switch {
	case strings.HasPrefix(cardNumber, "4"):
		return "VISA"

	case strings.HasPrefix(cardNumber, "34") || strings.HasPrefix(cardNumber, "37"):
		return "AMEX"

	case prefixInRange(cardNumber, 51, 55, 2) || prefixInRange(cardNumber, 2221, 2720, 4):
		return "MASTERCARD"

	case prefixInRange(cardNumber, 3528, 3589, 4):
		return "JCB"

	case strings.HasPrefix(cardNumber, "6011"),
		prefixInRange(cardNumber, 622126, 622925, 6),
		prefixInRange(cardNumber, 644, 649, 3),
		strings.HasPrefix(cardNumber, "65"):
		return "DISCOVER"

	case strings.HasPrefix(cardNumber, "62") || strings.HasPrefix(cardNumber, "81"):
		return "UPI"

	case prefixInRange(cardNumber, 300, 305, 3),
		strings.HasPrefix(cardNumber, "36"),
		prefixInRange(cardNumber, 38, 39, 2):
		return "DINERS CLUB"
	case prefixInRange(cardNumber, 970400, 970499, 6),
		strings.HasPrefix(cardNumber, "9704"):
		return "NAPAS"
	default:
		return "Unknown"
	}
}

// prefixInRange checks if the numeric prefix of a given length is within a range
func prefixInRange(cardNumber string, min, max int, length int) bool {
	if len(cardNumber) < length {
		return false
	}
	prefix := cardNumber[:length]
	var val int
	_, err := fmt.Sscanf(prefix, "%d", &val)
	if err != nil {
		return false
	}
	return val >= min && val <= max
}
