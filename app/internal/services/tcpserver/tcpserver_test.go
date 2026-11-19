package tcpserver

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func echoHandler(_ context.Context, _ *Conn, f []byte) ([]byte, bool, error) { return f, false, nil }

func startServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.Addr == "" {
		cfg.Addr = "127.0.0.1:0"
	}
	if cfg.Framer == nil {
		cfg.Framer, _ = NewFramer("", false)
	}
	if cfg.Handler == nil {
		cfg.Handler = echoHandler
	}
	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s
}

func dial(t *testing.T, s *Server) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", s.Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func readAll(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, n)
	if _, err := io.ReadFull(c, buf); err != nil {
		t.Fatalf("read %d bytes: %v", n, err)
	}
	return buf
}

func expectClosed(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("expected connection closed, got %v", err)
	}
}

func TestFramersRoundTripSplitAndCoalesced(t *testing.T) {
	body := []byte("hello-body")
	for _, name := range []string{FramingLen2BCD, FramingLen2Bin, FramingSTXETX, FramingLen4ASCII} {
		for _, inc := range []bool{false, true} {
			f, err := NewFramer(name, inc)
			if err != nil {
				t.Fatal(err)
			}
			wire, err := f.Encode(body)
			if err != nil {
				t.Fatal(err)
			}
			// Two coalesced frames in one buffer.
			r := bufio.NewReader(bytes.NewReader(append(append([]byte{}, wire...), wire...)))
			for i := 0; i < 2; i++ {
				got, err := f.ReadFrame(r, 100)
				if err != nil || !bytes.Equal(got, body) {
					t.Fatalf("%s inc=%v coalesced #%d: %q %v", name, inc, i, got, err)
				}
			}
			// Single-byte writes through a pipe.
			pr, pw := io.Pipe()
			go func() {
				for _, b := range wire {
					pw.Write([]byte{b})
				}
				pw.Close()
			}()
			got, err := f.ReadFrame(bufio.NewReader(pr), 100)
			if err != nil || !bytes.Equal(got, body) {
				t.Fatalf("%s inc=%v split: %q %v", name, inc, got, err)
			}
		}
	}
}

func TestFramerHeaders(t *testing.T) {
	f, _ := NewFramer(FramingLen2BCD, false)
	wire, _ := f.Encode(make([]byte, 123))
	if wire[0] != 0x01 || wire[1] != 0x23 {
		t.Fatalf("bcd header % x", wire[:2])
	}
	f, _ = NewFramer(FramingLen2Bin, true)
	wire, _ = f.Encode(make([]byte, 10))
	if binary.BigEndian.Uint16(wire) != 12 {
		t.Fatalf("bin header incl: % x", wire[:2])
	}
	f, _ = NewFramer(FramingLen4ASCII, false)
	wire, _ = f.Encode(make([]byte, 7))
	if string(wire[:4]) != "0007" {
		t.Fatalf("ascii header %q", wire[:4])
	}
}

func TestFramerMalformed(t *testing.T) {
	read := func(name string, wire []byte, max int) error {
		f, _ := NewFramer(name, false)
		_, err := f.ReadFrame(bufio.NewReader(bytes.NewReader(wire)), max)
		return err
	}
	if err := read(FramingLen2BCD, []byte{0x0a, 0x01, 'x'}, 100); !errors.Is(err, ErrFraming) {
		t.Fatalf("bad BCD digit: %v", err)
	}
	if err := read(FramingLen2Bin, []byte{0xff, 0xff}, 100); !errors.Is(err, ErrFraming) {
		t.Fatalf("oversized: %v", err)
	}
	if err := read(FramingLen4ASCII, []byte("12a4"), 100); !errors.Is(err, ErrFraming) {
		t.Fatalf("non digit: %v", err)
	}
	if err := read(FramingSTXETX, append([]byte{0x02}, bytes.Repeat([]byte{'a'}, 50)...), 10); !errors.Is(err, ErrFraming) {
		t.Fatalf("missing ETX: %v", err)
	}
	if err := read(FramingSTXETX, []byte{'a'}, 10); !errors.Is(err, ErrFraming) {
		t.Fatalf("missing STX: %v", err)
	}
	if err := read(FramingLen2BCD, nil, 10); !errors.Is(err, io.EOF) {
		t.Fatalf("clean EOF: %v", err)
	}
	if _, err := NewFramer("nope", false); err == nil {
		t.Fatal("unknown framing must fail")
	}
}

func TestServerEchoAndConnFacts(t *testing.T) {
	var mu sync.Mutex
	var seqs []int64
	s := startServer(t, Config{Handler: func(_ context.Context, c *Conn, f []byte) ([]byte, bool, error) {
		mu.Lock()
		seqs = append(seqs, c.Seq)
		mu.Unlock()
		if c.TLS || c.LocalPort == 0 || c.RemoteAddr == "" || c.ID == "" {
			t.Errorf("bad conn facts %+v", c)
		}
		return f, false, nil
	}})
	c := dial(t, s)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("abc"))
	for i := 0; i < 2; i++ {
		c.Write(wire)
		if got := readAll(t, c, len(wire)); !bytes.Equal(got, wire) {
			t.Fatalf("echo %d: % x", i, got)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seqs) != 2 || seqs[0] != 1 || seqs[1] != 2 {
		t.Fatalf("seq %v", seqs)
	}
	if st := s.Snapshot(); st.Frames != 2 || st.Accepted != 1 {
		t.Fatalf("stats %+v", st)
	}
}

func TestServerHandlerClosesAndErrors(t *testing.T) {
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	cases := map[string]Handler{
		"closeAfter": func(context.Context, *Conn, []byte) ([]byte, bool, error) { return []byte("r"), true, nil },
		"error": func(context.Context, *Conn, []byte) ([]byte, bool, error) {
			return []byte("r"), false, errors.New("boom")
		},
		"panic": func(context.Context, *Conn, []byte) ([]byte, bool, error) { panic("boom") },
	}
	for name, h := range cases {
		s := startServer(t, Config{Handler: h})
		c := dial(t, s)
		c.Write(wire)
		if name == "closeAfter" {
			readAll(t, c, 3) // header + 1 byte
		}
		expectClosed(t, c)
		if name != "closeAfter" && s.Snapshot().HandlerErrors != 1 {
			t.Fatalf("%s: handler errors %+v", name, s.Snapshot())
		}
	}
}

func TestServerEmptyReplyWritesNothingKeepsOpen(t *testing.T) {
	n := 0
	s := startServer(t, Config{Handler: func(_ context.Context, _ *Conn, f []byte) ([]byte, bool, error) {
		n++
		if n == 1 {
			return nil, false, nil
		}
		return f, false, nil
	}})
	c := dial(t, s)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	c.Write(wire)
	c.Write(wire)
	if got := readAll(t, c, len(wire)); !bytes.Equal(got, wire) {
		t.Fatalf("got % x", got)
	}
}

func TestServerFramingErrorClosesAndPeerCloseIsClean(t *testing.T) {
	s := startServer(t, Config{})
	c := dial(t, s)
	c.Write([]byte{0xff, 0xff, 1})
	expectClosed(t, c)
	if s.Snapshot().FramingErrors != 1 {
		t.Fatalf("stats %+v", s.Snapshot())
	}
	c2 := dial(t, s)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	c2.Write(wire)
	readAll(t, c2, len(wire))
	c2.Close() // one connection per transaction: must not count as an error
	time.Sleep(50 * time.Millisecond)
	if st := s.Snapshot(); st.FramingErrors != 1 || st.HandlerErrors != 0 {
		t.Fatalf("stats %+v", st)
	}
}

func TestServerTimeouts(t *testing.T) {
	s := startServer(t, Config{Limits: Limits{FirstFrame: 100 * time.Millisecond, Idle: 150 * time.Millisecond}})
	c := dial(t, s)
	expectClosed(t, c) // never sends the first frame
	c2 := dial(t, s)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	c2.Write(wire)
	readAll(t, c2, len(wire))
	expectClosed(t, c2) // idle after the first frame
}

func TestServerLimits(t *testing.T) {
	s := startServer(t, Config{Limits: Limits{MaxConns: 2, MaxConnsPerIP: 10}})
	a, b := dial(t, s), dial(t, s)
	time.Sleep(50 * time.Millisecond)
	c := dial(t, s)
	expectClosed(t, c)
	_, _ = a, b
	if s.Snapshot().Rejected < 1 {
		t.Fatalf("stats %+v", s.Snapshot())
	}

	s2 := startServer(t, Config{Limits: Limits{MaxConnsPerIP: 1}})
	d := dial(t, s2)
	time.Sleep(50 * time.Millisecond)
	e := dial(t, s2)
	expectClosed(t, e)
	_ = d
}

func TestServerCIDRGate(t *testing.T) {
	_, other, _ := net.ParseCIDR("10.9.9.0/24")
	s := startServer(t, Config{AllowCIDRs: []*net.IPNet{other}})
	expectClosed(t, dial(t, s))
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	s2 := startServer(t, Config{AllowCIDRs: []*net.IPNet{loop}})
	c := dial(t, s2)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	c.Write(wire)
	readAll(t, c, len(wire))
}

func TestServerShutdownWithLiveConns(t *testing.T) {
	s := startServer(t, Config{})
	c := dial(t, s)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	c.Write(wire)
	readAll(t, c, len(wire))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, c)
	if _, err := net.DialTimeout("tcp", s.Addr().String(), 200*time.Millisecond); err == nil {
		t.Fatal("listener must be closed")
	}
}

func TestServerStartFailsWhenPortBusy(t *testing.T) {
	s := startServer(t, Config{})
	f, _ := NewFramer("", false)
	s2, _ := New(Config{Addr: s.Addr().String(), Framer: f, Handler: echoHandler})
	if err := s2.Start(); err == nil {
		t.Fatal("expected bind error")
	}
}

// ---- PROXY protocol ----

func proxyV2(src string, port uint16) []byte {
	ip := net.ParseIP(src).To4()
	h := append([]byte{}, proxyV2Sig...)
	h = append(h, 0x21, 0x11, 0x00, 12)
	h = append(h, ip...)
	h = append(h, 127, 0, 0, 1)
	h = binary.BigEndian.AppendUint16(h, port)
	return binary.BigEndian.AppendUint16(h, 10123)
}

func TestProxyProtocol(t *testing.T) {
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	_, none, _ := net.ParseCIDR("10.9.9.0/24")
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	var mu sync.Mutex
	var remote string
	h := func(_ context.Context, c *Conn, b []byte) ([]byte, bool, error) {
		mu.Lock()
		remote = c.RemoteAddr
		mu.Unlock()
		return b, false, nil
	}
	cases := []struct {
		name    string
		mode    string
		trusted []*net.IPNet
		header  []byte
		ok      bool
		want    string
	}{
		{"v1 trusted", ProxyV1, []*net.IPNet{loop}, []byte("PROXY TCP4 203.0.113.9 127.0.0.1 51234 10123\r\n"), true, "203.0.113.9:51234"},
		{"v2 trusted", ProxyV2, []*net.IPNet{loop}, proxyV2("203.0.113.9", 51234), true, "203.0.113.9:51234"},
		{"auto v2", ProxyAuto, []*net.IPNet{loop}, proxyV2("203.0.113.10", 1), true, "203.0.113.10:1"},
		{"auto v1", ProxyAuto, []*net.IPNet{loop}, []byte("PROXY TCP4 203.0.113.11 127.0.0.1 2 10123\r\n"), true, "203.0.113.11:2"},
		{"untrusted peer", ProxyV1, []*net.IPNet{none}, []byte("PROXY TCP4 203.0.113.9 127.0.0.1 1 2\r\n"), false, ""},
		{"missing header", ProxyV1, []*net.IPNet{loop}, nil, false, ""},
		{"v2 on v1 listener", ProxyV1, []*net.IPNet{loop}, proxyV2("203.0.113.9", 1), false, ""},
	}
	for _, tc := range cases {
		s := startServer(t, Config{Handler: h, ProxyMode: tc.mode, TrustedProxies: tc.trusted})
		c := dial(t, s)
		c.Write(append(append([]byte{}, tc.header...), wire...))
		if tc.ok {
			readAll(t, c, len(wire))
			mu.Lock()
			if remote != tc.want {
				t.Errorf("%s: remote %q want %q", tc.name, remote, tc.want)
			}
			mu.Unlock()
		} else {
			expectClosed(t, c)
		}
	}
	if _, err := New(Config{Framer: f, Handler: h, ProxyMode: ProxyV1}); err == nil {
		t.Fatal("proxy mode without trusted proxies must fail")
	}
}

// ---- TLS ----

type pki struct {
	dir                   string
	caFile                string
	serverCert, serverKey string
	clientCert, clientKey string
	ca                    *x509.Certificate
	caKey                 *ecdsa.PrivateKey
}

func writePEM(t *testing.T, path, typ string, der []byte) {
	t.Helper()
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
}

func issue(t *testing.T, p *pki, cn string, server bool, certPath, keyPath string) {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	if server {
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
	} else {
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, p.ca, &key.PublicKey, p.caKey)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(key)
	writePEM(t, certPath, "CERTIFICATE", der)
	writePEM(t, keyPath, "EC PRIVATE KEY", kd)
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	p := &pki{dir: t.TempDir()}
	p.caKey, _ = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &p.caKey.PublicKey, p.caKey)
	p.ca, _ = x509.ParseCertificate(der)
	p.caFile = filepath.Join(p.dir, "ca.pem")
	writePEM(t, p.caFile, "CERTIFICATE", der)
	p.serverCert, p.serverKey = filepath.Join(p.dir, "s.pem"), filepath.Join(p.dir, "s.key")
	p.clientCert, p.clientKey = filepath.Join(p.dir, "c.pem"), filepath.Join(p.dir, "c.key")
	issue(t, p, "server", true, p.serverCert, p.serverKey)
	issue(t, p, "EDC-000123", false, p.clientCert, p.clientKey)
	return p
}

func (p *pki) clientTLS(t *testing.T, withCert bool) *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(p.ca)
	cfg := &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
	if withCert {
		cert, err := tls.LoadX509KeyPair(p.clientCert, p.clientKey)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg
}

func TestTLSAndMTLS(t *testing.T) {
	p := newPKI(t)
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("secure"))
	var mu sync.Mutex
	var cn string
	h := func(_ context.Context, c *Conn, b []byte) ([]byte, bool, error) {
		mu.Lock()
		cn = c.ClientCN
		mu.Unlock()
		if !c.TLS {
			t.Error("Conn.TLS must be true")
		}
		return b, false, nil
	}

	tcfg, err := NewTLSConfig(p.serverCert, p.serverKey, "")
	if err != nil {
		t.Fatal(err)
	}
	s := startServer(t, Config{TLS: tcfg, Handler: h})
	c, err := tls.Dial("tcp", s.Addr().String(), p.clientTLS(t, false))
	if err != nil {
		t.Fatal(err)
	}
	c.Write(wire)
	readAll(t, c, len(wire))
	c.Close()

	mcfg, err := NewTLSConfig(p.serverCert, p.serverKey, p.caFile)
	if err != nil {
		t.Fatal(err)
	}
	s2 := startServer(t, Config{TLS: mcfg, Handler: h})
	c2, err := tls.Dial("tcp", s2.Addr().String(), p.clientTLS(t, true))
	if err != nil {
		t.Fatal(err)
	}
	c2.Write(wire)
	readAll(t, c2, len(wire))
	c2.Close()
	mu.Lock()
	if cn != "EDC-000123" {
		t.Fatalf("client cn %q", cn)
	}
	mu.Unlock()

	// Without a client cert the handshake completes client-side in TLS 1.3, but the server must
	// reject: the connection dies on first read and the handler never runs.
	mu.Lock()
	cn = "untouched"
	mu.Unlock()
	c3, err := tls.Dial("tcp", s2.Addr().String(), p.clientTLS(t, false))
	if err == nil {
		c3.Write(wire)
		_ = c3.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, rerr := c3.Read(make([]byte, 1)); rerr == nil {
			t.Fatal("mTLS must reject a client without a certificate")
		}
		c3.Close()
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if cn != "untouched" || s2.Snapshot().TLSFailures < 1 {
		t.Fatalf("handler ran or no tls failure: cn=%q %+v", cn, s2.Snapshot())
	}
	mu.Unlock()
}

func TestTLSBehindProxyProtocol(t *testing.T) {
	p := newPKI(t)
	_, loop, _ := net.ParseCIDR("127.0.0.0/8")
	tcfg, _ := NewTLSConfig(p.serverCert, p.serverKey, "")
	var mu sync.Mutex
	var remote string
	s := startServer(t, Config{TLS: tcfg, ProxyMode: ProxyV2, TrustedProxies: []*net.IPNet{loop},
		Handler: func(_ context.Context, c *Conn, b []byte) ([]byte, bool, error) {
			mu.Lock()
			remote = c.RemoteAddr
			mu.Unlock()
			return b, false, nil
		}})
	raw, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	raw.Write(proxyV2("203.0.113.9", 4000)) // PROXY header precedes the TLS handshake
	c := tls.Client(raw, p.clientTLS(t, false))
	f, _ := NewFramer("", false)
	wire, _ := f.Encode([]byte("x"))
	if _, err := c.Write(wire); err != nil {
		t.Fatal(err)
	}
	readAll(t, c, len(wire))
	mu.Lock()
	defer mu.Unlock()
	if remote != "203.0.113.9:4000" {
		t.Fatalf("remote %q", remote)
	}
}

func TestCertReload(t *testing.T) {
	p := newPKI(t)
	cfg, err := newTLSConfig(p.serverCert, p.serverKey, "", 0) // check on every handshake
	if err != nil {
		t.Fatal(err)
	}
	first, _ := cfg.GetCertificate(nil)
	issue(t, p, "server2", true, p.serverCert, p.serverKey)
	future := time.Now().Add(time.Minute)
	os.Chtimes(p.serverCert, future, future)
	os.Chtimes(p.serverKey, future, future)
	second, _ := cfg.GetCertificate(nil)
	if first == second || strings.EqualFold(string(first.Certificate[0]), string(second.Certificate[0])) {
		t.Fatal("certificate was not reloaded")
	}
}
