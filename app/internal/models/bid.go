package models

// File overview:
// - Responsibility: the single canonical generator for `bid` values, the
//   universal globally-unique record identifier used across every model.
// - Key symbols: `GenerateBID`.

import (
	"crypto/rand"
	"strings"
)

const (
	bidRandomAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	bidRandomLength   = 8
	bidMaxLength      = 32
)

// GenerateBID builds a bid in the canonical format
// <model_name>-<8 random alphanumeric chars>, capped to a hard
// 32-character overall length. This is the single generator every caller
// must use instead of hand-rolling its own suffix logic, so the format
// stays consistent everywhere a bid is server-generated (MCP-created custom
// models/fields, etc). modelName may be empty, in which case it falls back
// to "record". Master-data import never calls this — imported records use
// the bid value already present in the seed file, never a generated one.
func GenerateBID(modelName string) string {
	modelSeg := simplifyBIDSegment(modelName, 0)
	if modelSeg == "" {
		modelSeg = "record"
	}
	randomSeg := randomBIDSegment(bidRandomLength)

	// The random segment plus its separating dash has a fixed length;
	// whatever remains of the 32-char budget goes to the model-name segment
	// so the overall bid never exceeds bidMaxLength.
	maxModelLen := bidMaxLength - len(randomSeg) - 1
	if maxModelLen < 0 {
		maxModelLen = 0
	}
	if len(modelSeg) > maxModelLen {
		modelSeg = modelSeg[:maxModelLen]
	}

	if modelSeg == "" {
		return randomSeg
	}
	return modelSeg + "-" + randomSeg
}

// simplifyBIDSegment lowercases raw, keeps only [a-z0-9], and optionally
// caps the result length (0 means uncapped).
func simplifyBIDSegment(raw string, capLen int) string {
	lowered := strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	for _, r := range lowered {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	simplified := b.String()
	if capLen > 0 && len(simplified) > capLen {
		simplified = simplified[:capLen]
	}
	return simplified
}

// randomBIDSegment returns n random characters drawn from
// bidRandomAlphabet using crypto/rand. Falls back to an all-zero-index
// (i.e. all 'a') segment only if crypto/rand itself is unavailable, which is
// effectively unrecoverable process health rather than a real code path.
func randomBIDSegment(n int) string {
	buf := make([]byte, n)
	out := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		for i := range out {
			out[i] = bidRandomAlphabet[0]
		}
		return string(out)
	}
	for i, v := range buf {
		out[i] = bidRandomAlphabet[int(v)%len(bidRandomAlphabet)]
	}
	return string(out)
}
