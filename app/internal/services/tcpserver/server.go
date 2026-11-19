package tcpserver

// File overview:
//   - Responsibility: a raw TCP (optionally TLS/mTLS, optionally behind a PROXY protocol header) frame
//     server. It delimits frames with a Framer, hands each body to a Handler and writes back the framed
//     reply. It never inspects frame contents.
//   - Standard library only: no Fiber, ORM or workflow imports.
//   - Concurrency: one goroutine per connection (with recover); frames of one connection are handled
//     sequentially so request/response order is kept; global and per-IP connection caps bound resources.
//   - Shutdown: stop accepting, cancel handler contexts, close connections, wait up to the ctx deadline.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Conn carries facts about one connection; the server never checks them.
type Conn struct {
	ID         string
	RemoteAddr string // real client address (PROXY header applied)
	LocalPort  int
	TLS        bool
	ClientCN   string // verified client certificate common name (mTLS), else ""
	Seq        int64  // 1-based frame number on this connection, set before each Handler call
}

// Handler processes one frame body. A non-empty reply is framed and written. closeAfter closes the
// connection after the write. An error closes the connection without writing.
type Handler func(ctx context.Context, c *Conn, frame []byte) (reply []byte, closeAfter bool, err error)

// Limits bound connections, frames and time. Zero values fall back to the defaults below.
type Limits struct {
	MaxConns      int           // per listener, default 256
	MaxConnsPerIP int           // default 32
	MaxFrameBytes int           // default 4096
	FirstFrame    time.Duration // time allowed to send the first frame, default 10s
	Idle          time.Duration // time allowed between frames, default 120s
	Handshake     time.Duration // PROXY header + TLS handshake, default 10s
	Reply         time.Duration // Handler context timeout, default 30s
	Write         time.Duration // reply write deadline, default 10s
}

func (l Limits) withDefaults() Limits {
	if l.MaxConns <= 0 {
		l.MaxConns = 256
	}
	if l.MaxConnsPerIP <= 0 {
		l.MaxConnsPerIP = 32
	}
	if l.MaxFrameBytes <= 0 {
		l.MaxFrameBytes = 4096
	}
	if l.FirstFrame <= 0 {
		l.FirstFrame = 10 * time.Second
	}
	if l.Idle <= 0 {
		l.Idle = 120 * time.Second
	}
	if l.Handshake <= 0 {
		l.Handshake = 10 * time.Second
	}
	if l.Reply <= 0 {
		l.Reply = 30 * time.Second
	}
	if l.Write <= 0 {
		l.Write = 10 * time.Second
	}
	return l
}

// Config configures a Server.
type Config struct {
	Addr           string
	TLS            *tls.Config // nil = plain TCP
	Framer         Framer
	Handler        Handler
	Limits         Limits
	AllowCIDRs     []*net.IPNet // empty = allow all
	ProxyMode      string       // none|v1|v2|auto
	TrustedProxies []*net.IPNet // peers allowed to send a PROXY header
}

// Stats is a point-in-time copy of the counters.
type Stats struct {
	Accepted, Rejected, Active, Frames, BytesIn, BytesOut  int64
	FramingErrors, TLSFailures, HandlerErrors, ProxyErrors int64
}

// Server is one listener.
type Server struct {
	cfg    Config
	proxy  string
	lim    Limits
	ln     net.Listener
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	perIP  map[string]int
	closed bool

	accepted, rejected, active, frames, bytesIn, bytesOut atomic.Int64
	framingErrs, tlsFails, handlerErrs, proxyErrs         atomic.Int64
}

// New validates cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Framer == nil || cfg.Handler == nil {
		return nil, errors.New("tcpserver: framer and handler are required")
	}
	mode, ok := NormalizeProxyMode(cfg.ProxyMode)
	if !ok {
		return nil, fmt.Errorf("tcpserver: unknown proxy mode %q", cfg.ProxyMode)
	}
	if mode != ProxyNone && len(cfg.TrustedProxies) == 0 {
		return nil, errors.New("tcpserver: proxy protocol requires trusted proxies")
	}
	return &Server{cfg: cfg, proxy: mode, lim: cfg.Limits.withDefaults(), conns: map[net.Conn]struct{}{}, perIP: map[string]int{}}, nil
}

// Start binds the listener (failing synchronously, e.g. port in use) and begins accepting.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", s.cfg.Addr)
	if err != nil {
		return err
	}
	s.ln = ln
	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.wg.Add(1)
	go s.acceptLoop()
	return nil
}

// Addr is the bound listen address (useful with port 0).
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Snapshot returns the counters.
func (s *Server) Snapshot() Stats {
	return Stats{
		Accepted: s.accepted.Load(), Rejected: s.rejected.Load(), Active: s.active.Load(),
		Frames: s.frames.Load(), BytesIn: s.bytesIn.Load(), BytesOut: s.bytesOut.Load(),
		FramingErrors: s.framingErrs.Load(), TLSFailures: s.tlsFails.Load(),
		HandlerErrors: s.handlerErrs.Load(), ProxyErrors: s.proxyErrs.Load(),
	}
}

// Shutdown stops accepting, closes live connections and waits for their goroutines until ctx ends.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	if s.ln != nil {
		_ = s.ln.Close()
	}
	if s.cancel != nil {
		s.cancel()
	}
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()
	for {
		raw, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			time.Sleep(50 * time.Millisecond) // transient (e.g. EMFILE): back off, keep serving
			continue
		}
		if !s.track(raw) {
			s.rejected.Add(1)
			_ = raw.Close()
			continue
		}
		s.accepted.Add(1)
		s.wg.Add(1)
		go s.serveConn(raw)
	}
}

// track registers a connection under the global cap; false when full or shutting down.
func (s *Server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || len(s.conns) >= s.lim.MaxConns {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c net.Conn, ip string) {
	s.mu.Lock()
	delete(s.conns, c)
	if ip != "" {
		if s.perIP[ip]--; s.perIP[ip] <= 0 {
			delete(s.perIP, ip)
		}
	}
	s.mu.Unlock()
}

// reserveIP counts one connection for ip; false when the per-IP cap is reached.
func (s *Server) reserveIP(ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.perIP[ip] >= s.lim.MaxConnsPerIP {
		return false
	}
	s.perIP[ip]++
	return true
}

func inCIDRs(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func hostOf(a net.Addr) (string, net.IP) {
	h, _, err := net.SplitHostPort(a.String())
	if err != nil {
		h = a.String()
	}
	return h, net.ParseIP(h)
}

// bufConn reads through the buffered reader so bytes read ahead (PROXY parsing) are not lost.
type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (b *bufConn) Read(p []byte) (int, error) { return b.r.Read(p) }

func (s *Server) serveConn(raw net.Conn) {
	defer s.wg.Done()
	reservedIP := ""
	defer func() {
		if rec := recover(); rec != nil {
			s.handlerErrs.Add(1)
		}
		_ = raw.Close()
		s.untrack(raw, reservedIP)
	}()
	s.active.Add(1)
	defer s.active.Add(-1)

	_ = raw.SetDeadline(time.Now().Add(s.lim.Handshake))
	remote := raw.RemoteAddr()
	var nc net.Conn = raw
	if s.proxy != ProxyNone {
		_, peerIP := hostOf(remote)
		if peerIP == nil || !inCIDRs(peerIP, s.cfg.TrustedProxies) {
			s.rejected.Add(1)
			return
		}
		br := bufio.NewReaderSize(raw, 4096)
		src, err := readProxyHeader(br, s.proxy)
		if err != nil {
			s.proxyErrs.Add(1)
			return
		}
		if src != nil {
			remote = src
		}
		nc = &bufConn{Conn: raw, r: br}
	}
	host, ip := hostOf(remote)
	if len(s.cfg.AllowCIDRs) > 0 && (ip == nil || !inCIDRs(ip, s.cfg.AllowCIDRs)) {
		s.rejected.Add(1)
		return
	}
	if !s.reserveIP(host) {
		s.rejected.Add(1)
		return
	}
	reservedIP = host

	info := &Conn{ID: newConnID(), RemoteAddr: remote.String()}
	if la, ok := raw.LocalAddr().(*net.TCPAddr); ok {
		info.LocalPort = la.Port
	}
	if s.cfg.TLS != nil {
		tc := tls.Server(nc, s.cfg.TLS)
		if err := tc.Handshake(); err != nil {
			s.tlsFails.Add(1)
			return
		}
		info.TLS = true
		if st := tc.ConnectionState(); len(st.PeerCertificates) > 0 {
			info.ClientCN = st.PeerCertificates[0].Subject.CommonName
		}
		nc = tc
	}
	s.frameLoop(nc, info)
}

func (s *Server) frameLoop(nc net.Conn, info *Conn) {
	// nc already reads through the PROXY-header buffer (bufConn) when one was parsed.
	reader := bufio.NewReaderSize(nc, 4096)
	wait := s.lim.FirstFrame
	for {
		_ = nc.SetReadDeadline(time.Now().Add(wait))
		wait = s.lim.Idle
		body, err := s.cfg.Framer.ReadFrame(reader, s.lim.MaxFrameBytes)
		if err != nil {
			if errors.Is(err, ErrFraming) {
				s.framingErrs.Add(1)
			}
			return // EOF, idle timeout, shutdown, bad frame: all end the connection
		}
		_ = nc.SetReadDeadline(time.Time{})
		info.Seq++
		s.frames.Add(1)
		s.bytesIn.Add(int64(len(body)))

		reply, closeAfter, herr := s.callHandler(info, body)
		if herr != nil {
			s.handlerErrs.Add(1)
			return
		}
		if len(reply) > 0 {
			wire, err := s.cfg.Framer.Encode(reply)
			if err != nil {
				s.framingErrs.Add(1)
				return
			}
			_ = nc.SetWriteDeadline(time.Now().Add(s.lim.Write))
			if _, err := nc.Write(wire); err != nil {
				return
			}
			s.bytesOut.Add(int64(len(wire)))
		}
		if closeAfter {
			return
		}
	}
}

func (s *Server) callHandler(info *Conn, body []byte) (reply []byte, closeAfter bool, err error) {
	ctx, cancel := context.WithTimeout(s.ctx, s.lim.Reply)
	defer cancel()
	defer func() {
		if rec := recover(); rec != nil {
			reply, closeAfter, err = nil, true, fmt.Errorf("tcpserver: handler panic: %v", rec)
		}
	}()
	return s.cfg.Handler(ctx, info, body)
}

func newConnID() string {
	var b [8]byte
	_, _ = io.ReadFull(rand.Reader, b[:])
	return hex.EncodeToString(b[:])
}
