package nodes

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	workflowmodels "git.thebase.vn/base_hub/modules/base-workflow/models"
)

func hmacVerifyRequest(params map[string]any, payload string) WorkflowActionRequest {
	return WorkflowActionRequest{Step: workflowmodels.Step{Key: "verify", Params: params}, Payload: []byte(payload)}
}

func TestHmacVerifyActionFieldMessage(t *testing.T) {
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write([]byte("order-42"))
	sig := hex.EncodeToString(mac.Sum(nil))
	params := map[string]any{"hmac_key": "s3cret", "signature_field": "meta.sig", "message_source": "field", "message_field": "data.id"}
	out, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(params, `{"data":{"id":"order-42"},"meta":{"sig":"`+sig+`"}}`))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	var decoded map[string]any
	_ = json.Unmarshal(out, &decoded)
	if decoded["_hmac_verified"] != true {
		t.Fatalf("output = %s", out)
	}
}

func TestHmacVerifyActionSha512Base64(t *testing.T) {
	body := `{"amount":10}`
	mac := hmac.New(sha512.New, []byte("k"))
	mac.Write([]byte(body))
	sig := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	params := map[string]any{"hmac_key": "k", "algorithm": "sha512", "encoding": "base64", "signature_field": "sig", "message_field": "body"}
	payload, _ := json.Marshal(map[string]any{"body": body, "sig": sig})
	if _, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(params, string(payload))); err != nil {
		t.Fatalf("verify: %v", err)
	}
	// An object message is signed as compact JSON with sorted keys.
	mac = hmac.New(sha512.New, []byte("k"))
	mac.Write([]byte(`{"a":1,"b":2}`))
	payload, _ = json.Marshal(map[string]any{"body": map[string]any{"b": 2, "a": 1}, "sig": base64.StdEncoding.EncodeToString(mac.Sum(nil))})
	if _, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(params, string(payload))); err != nil {
		t.Fatalf("verify object: %v", err)
	}
}

func TestHmacVerifyActionMismatchIsNonRetryable(t *testing.T) {
	params := map[string]any{"hmac_key": "k", "signature_field": "sig", "message_field": "m"}
	for _, payload := range []string{`{"m":"x","sig":"00ff"}`, `{"m":"x","sig":"not-hex"}`, `{"m":"x"}`} {
		_, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(params, payload))
		if !errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) || !errors.Is(err, errHMACVerifyMismatch) {
			t.Fatalf("%s: err = %v, want non-retryable mismatch", payload, err)
		}
	}
	// Configuration problems stay ordinary step errors.
	for _, bad := range []map[string]any{
		{"hmac_key": "k", "signature_field": "sig", "message_field": "m", "algorithm": "md5"},
		{"hmac_key": "k", "signature_field": "sig", "message_field": "m", "encoding": "b32"},
		{"hmac_key": "k", "signature_field": "sig", "message_field": "missing"},
		{"signature_field": "sig", "message_field": "m"},
	} {
		_, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(bad, `{"m":"x","sig":"00"}`))
		if err == nil || errors.Is(err, workflowmodels.ErrWorkflowStepNonRetryable) {
			t.Fatalf("%v: err = %v, want a retryable config error", bad, err)
		}
	}
}

func TestHmacVerifyParamValidation(t *testing.T) {
	issues := workflowmodels.ValidateStepParams("hmac_verify", 1, map[string]any{"hmac_key": "plain"}, nil)
	params := map[string]bool{}
	for _, issue := range issues {
		params[issue.Param] = true
	}
	for _, want := range []string{"hmac_key", "signature_field", "message_field"} {
		if !params[want] {
			t.Fatalf("missing issue for %s: %#v", want, issues)
		}
	}
}

// An object message keeps < > & literal and big integers exact (plan T103 review), and the
// passed-on payload keeps the integer exact too.
func TestHmacVerifyActionCanonicalJSONKeepsHTMLAndBigNumbers(t *testing.T) {
	message := `{"id":12345678901234567890,"note":"a<b&c"}`
	mac := hmac.New(sha256.New, []byte("k"))
	mac.Write([]byte(message))
	payload := `{"body":` + message + `,"sig":"` + hex.EncodeToString(mac.Sum(nil)) + `"}`
	params := map[string]any{"hmac_key": "k", "signature_field": "sig", "message_field": "body"}
	out, err := HmacVerifyAction(context.Background(), hmacVerifyRequest(params, payload))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !strings.Contains(string(out), `"id":12345678901234567890`) {
		t.Fatalf("output lost the integer: %s", out)
	}
}
