package safehttp

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAllowedRefusesNonPublicAddresses(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"fe80::1", "fc00::1", "0.0.0.0", "100.64.0.1", "224.0.0.1", "255.255.255.255",
		"::ffff:127.0.0.1", "::ffff:169.254.169.254", "64:ff9b::a9fe:a9fe", "2002:7f00:1::1",
	}
	for _, raw := range blocked {
		if Allowed(net.ParseIP(raw), nil) {
			t.Errorf("%s allowed, want blocked", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !Allowed(net.ParseIP(raw), nil) {
			t.Errorf("%s blocked, want allowed", raw)
		}
	}
	allow, bad := parseCIDRs([]string{"10.0.0.0/8", "127.0.0.1", "nope"})
	if len(bad) != 1 || len(allow) != 2 {
		t.Fatalf("parseCIDRs = %v, bad %v", allow, bad)
	}
	if !Allowed(net.ParseIP("10.9.9.9"), allow) || !Allowed(net.ParseIP("127.0.0.1"), allow) {
		t.Fatal("allow list entries must be dialable")
	}
	if Allowed(net.ParseIP("127.0.0.2"), allow) {
		t.Fatal("single-IP allow entry must not open its neighbours")
	}
}

func TestClientRefusesLoopbackAfterResolution(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	client := NewClient(Options{Timeout: 5 * time.Second, Allow: []*net.IPNet{}})
	// Both the literal IP and a hostname resolving to loopback are refused at dial time.
	for _, target := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		_, err := Do(context.Background(), client, req)
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("%s: err = %v, want ErrBlockedAddress", target, err)
		}
	}
}

func TestClientRedirectLimitAndScheme(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/loop":
			http.Redirect(w, r, srv.URL+"/loop", http.StatusFound)
		case "/file":
			http.Redirect(w, r, "file:///etc/passwd", http.StatusFound)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	defer srv.Close()

	loopback := []*net.IPNet{{IP: net.IPv4(127, 0, 0, 1).To4(), Mask: net.CIDRMask(32, 32)}}
	client := NewClient(Options{Timeout: 5 * time.Second, Allow: loopback})
	ctx := context.Background()

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/ok", nil)
	resp, err := Do(ctx, client, req)
	if err != nil {
		t.Fatalf("allow-listed target: %v", err)
	}
	resp.Body.Close()

	for _, path := range []string{"/loop", "/file"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
		if resp, err := Do(ctx, client, req); err == nil {
			resp.Body.Close()
			t.Fatalf("%s: redirect followed, want error", path)
		}
	}

	req, _ = http.NewRequest(http.MethodGet, "ftp://example.com/x", nil)
	if _, err := Do(ctx, client, req); err == nil {
		t.Fatal("ftp scheme accepted")
	}
}

func TestReadBodyCap(t *testing.T) {
	body, err := ReadBody(strings.NewReader("12345"), 5)
	if err != nil || string(body) != "12345" {
		t.Fatalf("at limit: %q, %v", body, err)
	}
	body, err = ReadBody(strings.NewReader("123456"), 5)
	if !errors.Is(err, ErrResponseTooLarge) || string(body) != "12345" {
		t.Fatalf("over limit: %q, %v", body, err)
	}
}
