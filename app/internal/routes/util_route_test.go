package routes

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func utilTestApp() *fiber.App {
	app := fiber.New()
	registerUtilityRoutes(app)
	return app
}

func utilDo(t *testing.T, app *fiber.App, method, url, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func TestUtilityBanklist(t *testing.T) {
	app := utilTestApp()
	if code, body := utilDo(t, app, "GET", "/banklist/970416", ""); code != 200 || !strings.Contains(body, "ACB") {
		t.Fatalf("known bank: %d %s", code, body)
	}
	// base.ServerError carries the business code in the body; assert that, not the transport status.
	if _, body := utilDo(t, app, "GET", "/banklist/000000", ""); !strings.Contains(body, `"code":404`) {
		t.Fatalf("unknown bank: want code 404, got %s", body)
	}
}

func TestUtilityQrReadsQuery(t *testing.T) {
	code, body := utilDo(t, utilTestApp(), "GET", "/qr?binCode=970416&bankAccount=123456&amount=1000&billId=ABC", "")
	if code != 200 || !strings.Contains(body, "970416") {
		t.Fatalf("qr: %d %s", code, body)
	}
}

func TestUtilityAesRoundTripAndBadInput(t *testing.T) {
	app := utilTestApp()
	key := strings.Repeat("ab", 32)
	_, enc := utilDo(t, app, "POST", "/aes", `{"data":"hello","key":"`+key+`","isEncrypt":true}`)
	i := strings.Index(enc, `"encryptedData":"`)
	if i < 0 {
		t.Fatalf("encrypt: %s", enc)
	}
	ct := enc[i+len(`"encryptedData":"`):]
	ct = ct[:strings.Index(ct, `"`)]
	if _, dec := utilDo(t, app, "POST", "/aes", `{"data":"`+ct+`","key":"`+key+`"}`); !strings.Contains(dec, "hello") {
		t.Fatalf("decrypt: %s", dec)
	}
	// Malformed key / ciphertext must be an error response, not a panic.
	if code, _ := utilDo(t, app, "POST", "/aes", `{"data":"AAAA","key":"zz"}`); code != 500 {
		t.Fatalf("bad key: got %d", code)
	}
	if code, _ := utilDo(t, app, "POST", "/aes", `{"data":"AAAA","key":"`+key+`"}`); code != 500 {
		t.Fatalf("bad ciphertext: got %d", code)
	}
}

func TestUtilityAesHmac(t *testing.T) {
	code, body := utilDo(t, utilTestApp(), "POST", "/aeshmac", `{"jsonObj":{"id":1,"name":"a"},"keyList":["id","name"],"needSign":true,"needAES256":true}`)
	if code != 200 || !strings.Contains(body, "hmacSignData") || !strings.Contains(body, `id=1\u0026name=a`) {
		t.Fatalf("aeshmac: %d %s", code, body)
	}
}
