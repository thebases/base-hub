package vietqr

// File overview:
// - Responsibility: collects shared helper logic for crc, giving the backend one reusable implementation for this low-level concern.
// - Key symbols: `StringToUtf8ByteArray`, `string`, `CRC`.
// - Pattern: Keeps one focused responsibility per file so adjacent packages can reuse this logic without depending on unrelated transport or persistence details.

import (
	"fmt"
	"strings"
)

// CRC functions
type CRC struct{}

// Convert string to UTF-8 byte array
func (c CRC) StringToUtf8ByteArray(str string) []byte {
	out := []byte{}
	for _, r := range str {
		if r < 128 {
			out = append(out, byte(r))
		} else if r < 2048 {
			out = append(out, byte((r>>6)|192))
			out = append(out, byte((r&63)|128))
		} else if r < 65536 {
			out = append(out, byte((r>>12)|224))
			out = append(out, byte(((r>>6)&63)|128))
			out = append(out, byte((r&63)|128))
		}
	}
	return out
}

// Calculate CRC16
func (c CRC) GetCRC16(data []byte) string {
	const polynomial uint16 = 0x1021
	var crc uint16 = 0xFFFF

	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if (crc & 0x8000) != 0 {
				crc = (crc << 1) ^ polynomial
			} else {
				crc <<= 1
			}
		}
	}
	crc &= 0xFFFF
	return strings.ToUpper(fmt.Sprintf("%04X", crc))
}
