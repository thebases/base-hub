package nodes

// File overview:
// - Responsibility: the hmac_verify@v1 node (plan T103) — checks an inbound HMAC signature found in
//   the payload against a signed message field of the same payload with a constant-time compare.
//   A mismatch fails the step without retries.
// - Why no "raw body" source: a step's input is the request body itself (/wh passes no headers to
//   steps), so a signature carried in the payload is always part of those bytes and could never
//   verify. A body signed in a header is verified by the webhook trigger's hmac_secret (plan T43).
// - Key symbols: `HmacVerifyAction`.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"strings"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

// errHMACVerifyMismatch is the one error text a mismatch produces, so the run history never tells a
// caller which part (encoding, length, digest) was wrong.
var errHMACVerifyMismatch = errors.New("hmac_verify: signature mismatch")

func init() {
	registerNode(nodeDef{
		Code:        "hmac_verify",
		Version:     1,
		Sequence:    75,
		Label:       "HMAC Verify",
		Description: "Verify an HMAC signature carried in the payload against a signed payload field; a mismatch fails the step without retries.",
		Category:    "Security",
		Params: []paramDef{
			{Name: "hmac_key", Label: "HMAC Key", Type: "string", Required: true, Column: "hmac_key", Secret: secretCredentialRef,
				Help: "Shared secret as a @cred:<name> or @syscred:<name> reference."},
			{Name: "algorithm", Label: "Algorithm", Type: "select", Default: "sha256", ExtraKey: "algorithm",
				Options: []paramOption{{Value: "sha256", Label: "HMAC-SHA256"}, {Value: "sha512", Label: "HMAC-SHA512"}}},
			{Name: "signature_field", Label: "Signature Field", Type: "string", Required: true, ExtraKey: "signature_field",
				Help: "Dotted payload path of the received signature (for example signature or meta.sig)."},
			{Name: "message_field", Label: "Message Field", Type: "string", Required: true, ExtraKey: "message_field",
				Help: "Dotted payload path of the signed message. Text is signed as is; any other value as compact JSON with sorted keys."},
			{Name: "encoding", Label: "Encoding", Type: "select", Default: "hex", ExtraKey: "encoding",
				Options: []paramOption{{Value: "hex", Label: "Hex"}, {Value: "base64", Label: "Base64"}}},
		},
	}, HmacVerifyAction)
}

// HmacVerifyAction passes the payload through with `_hmac_verified: true` when the signature
// matches. Configuration errors are ordinary (retryable) step errors; a mismatch is not retryable.
func HmacVerifyAction(_ context.Context, req WorkflowActionRequest) ([]byte, error) {
	// UseNumber keeps large integers exact in both the signed message and the passed-on payload.
	input, err := decodeJSONObjectNumbers(req.Payload)
	if err != nil {
		return nil, fmt.Errorf("hmac_verify: %w", err)
	}
	params := req.Step.Params
	key := firstStringParam(params, "hmac_key", "key")
	if key == "" {
		return nil, fmt.Errorf("hmac_verify: hmac_key is required")
	}
	newHash, err := hmacVerifyHash(firstStringParam(params, "algorithm"))
	if err != nil {
		return nil, err
	}
	message, err := hmacVerifyMessage(params, input)
	if err != nil {
		return nil, err
	}
	signatureField := strings.TrimSpace(firstStringParam(params, "signature_field"))
	if signatureField == "" {
		return nil, fmt.Errorf("hmac_verify: signature_field is required")
	}
	encoding := strings.ToLower(strings.TrimSpace(firstStringParam(params, "encoding")))
	if encoding != "" && encoding != "hex" && encoding != "base64" {
		return nil, fmt.Errorf("hmac_verify: unknown encoding %q (use hex or base64)", encoding)
	}
	rawSignature, _ := lookupNestedBinding(input, signatureField)
	received, ok := decodeHMACSignature(encoding, rawSignature)
	mac := hmac.New(newHash, []byte(key))
	mac.Write(message)
	// hmac.Equal is constant time; a missing or undecodable signature is the same mismatch.
	if !ok || !hmac.Equal(mac.Sum(nil), received) {
		return nil, fmt.Errorf("%w: %w", errHMACVerifyMismatch, workflowmodels.ErrWorkflowStepNonRetryable)
	}
	output := cloneJSONObject(input)
	output["_hmac_verified"] = true
	return json.Marshal(output)
}

func hmacVerifyHash(algorithm string) (func() hash.Hash, error) {
	switch strings.ToLower(strings.TrimSpace(algorithm)) {
	case "", "sha256":
		return sha256.New, nil
	case "sha512":
		return sha512.New, nil
	default:
		return nil, fmt.Errorf("hmac_verify: unknown algorithm %q (use sha256 or sha512)", algorithm)
	}
}

// hmacVerifyMessage returns the signed bytes of the message field.
func hmacVerifyMessage(params map[string]any, input map[string]any) ([]byte, error) {
	field := strings.TrimSpace(firstStringParam(params, "message_field"))
	value, ok := lookupNestedBinding(input, field)
	if field == "" || !ok {
		return nil, fmt.Errorf("hmac_verify: message field %q not found in the payload", field)
	}
	if text, isText := value.(string); isText {
		return []byte(text), nil
	}
	// encoding/json sorts map keys, so the same value always signs the same bytes. HTML escaping
	// is off (a signer's canonical JSON keeps < > & literal) and numbers were decoded as
	// json.Number, so they are written back digit for digit.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("hmac_verify: encode message: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// decodeJSONObjectNumbers is decodeJSONObject with numbers kept as json.Number.
func decodeJSONObjectNumbers(payload []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("expected JSON object payload: %w", err)
	}
	if decoded == nil {
		return nil, fmt.Errorf("expected JSON object payload")
	}
	return decoded, nil
}

func decodeHMACSignature(encoding string, raw any) ([]byte, bool) {
	text, ok := raw.(string)
	text = strings.TrimSpace(text)
	if !ok || text == "" {
		return nil, false
	}
	var decoded []byte
	var err error
	if encoding == "base64" {
		decoded, err = base64.StdEncoding.DecodeString(text)
		if err != nil {
			decoded, err = base64.RawURLEncoding.DecodeString(strings.TrimRight(text, "="))
		}
	} else {
		decoded, err = hex.DecodeString(text)
	}
	return decoded, err == nil
}
