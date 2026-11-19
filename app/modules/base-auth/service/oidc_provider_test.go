package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"github.com/golang-jwt/jwt/v5"
)

func TestOIDCTokenDBName(t *testing.T) {
	cases := []struct {
		name, claim, home string
		claims            jwt.MapClaims
		want              string
		wantErr           bool
	}{
		{name: "no claim configured binds to home", home: "home", claims: jwt.MapClaims{"tenant": "other"}, want: "home"},
		{name: "no claim and no home fails", claims: jwt.MapClaims{}, wantErr: true},
		{name: "claim value names the tenant", claim: "tenant", home: "home", claims: jwt.MapClaims{"tenant": " t1 "}, want: "t1"},
		{name: "missing claim fails closed", claim: "tenant", home: "home", claims: jwt.MapClaims{}, wantErr: true},
		{name: "non-string claim fails closed", claim: "tenant", home: "home", claims: jwt.MapClaims{"tenant": 7}, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := oidcTokenDBName(tc.claims, tc.claim, tc.home)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got (%q, %v), want %q", got, err, tc.want)
			}
		})
	}
}

// oidcTestIdP serves a JWKS with one RSA key (kid "k1") and counts fetches.
type oidcTestIdP struct {
	key     *rsa.PrivateKey
	fetches atomic.Int32
	server  *httptest.Server
}

func newOIDCTestIdP(t *testing.T) *oidcTestIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &oidcTestIdP{key: key}
	idp.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		idp.fetches.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kid": "k1",
			"kty": "RSA",
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(idp.server.Close)

	prev := config.Get()
	cfg := *prev
	cfg.AuthService.JWKSURL = idp.server.URL
	cfg.AuthService.Issuer = "https://idp.test"
	cfg.AuthService.Audience = "base-hub"
	cfg.AuthService.TenantClaim = "tenant"
	config.Set(cfg)
	t.Cleanup(func() { config.Set(*prev) })
	return idp
}

func (idp *oidcTestIdP) sign(t *testing.T, kid string, claims jwt.MapClaims) string {
	t.Helper()
	base := jwt.MapClaims{
		"iss":                "https://idp.test",
		"aud":                "base-hub",
		"exp":                time.Now().Add(time.Hour).Unix(),
		"preferred_username": "alice",
	}
	for k, v := range claims {
		base[k] = v
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, base)
	token.Header["kid"] = kid
	signed, err := token.SignedString(idp.key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

// A token without the configured tenant claim is refused after signature
// checks and before any user lookup (no DB is needed to reach the error).
func TestOIDCValidateRequiresTenantClaim(t *testing.T) {
	idp := newOIDCTestIdP(t)
	p := NewOIDCProvider()
	_, err := p.Validate(context.Background(), idp.sign(t, "k1", nil))
	if err == nil || !strings.Contains(err.Error(), `missing claim "tenant"`) {
		t.Fatalf("expected missing tenant claim error, got %v", err)
	}
}

// Unknown kids refetch the JWKS at most once per jwksMinRefetch.
func TestOIDCUnknownKidRefetchThrottled(t *testing.T) {
	idp := newOIDCTestIdP(t)
	now := time.Unix(1_700_000_000, 0)
	p := NewOIDCProvider()
	p.nowFunc = func() time.Time { return now }

	for i := 0; i < 20; i++ {
		if _, err := p.Validate(context.Background(), idp.sign(t, "forged", nil)); err == nil {
			t.Fatal("token with unknown kid must be rejected")
		}
	}
	// One TTL fetch plus one forced refetch, not one per request.
	if got := idp.fetches.Load(); got != 2 {
		t.Fatalf("JWKS fetched %d times, want 2", got)
	}

	now = now.Add(jwksMinRefetch + time.Second)
	if _, err := p.Validate(context.Background(), idp.sign(t, "forged", nil)); err == nil {
		t.Fatal("token with unknown kid must be rejected")
	}
	if got := idp.fetches.Load(); got != 3 {
		t.Fatalf("JWKS fetched %d times after the window, want 3", got)
	}
}
