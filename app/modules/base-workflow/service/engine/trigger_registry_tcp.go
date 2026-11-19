package engine

// File overview:
// - Responsibility: wires `tcp` workflow triggers to the raw TCP transport (internal/services/tcpserver).
//   One trigger = one port = one listener. The transport only delimits frames; each frame body goes into
//   the workflow as an opaque hex envelope and the workflow's final output is written back as the reply.
// - Reply contract (the only convention the transport knows): the final output is a JSON object;
//   `reply_hex` is hex-decoded, framed and written; `close:true` closes after writing. A missing or
//   undecodable `reply_hex`, a failed run or a timeout writes nothing and closes the connection.
//   The transport never builds a reply of its own and never looks inside the body.
// - Concurrency: the listener's trigger row is held in an atomic pointer so handlers never take the
//   registry lock (Shutdown waits for handlers and runs outside that lock).

import (
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/orm"
	"git.thebase.vn/base_hub/internal/services/log"
	"git.thebase.vn/base_hub/internal/services/tcpserver"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

// tcpShutdownTimeout bounds how long unregistering waits for live connections to drain.
const tcpShutdownTimeout = 5 * time.Second

// tcpListener is the live listener of one registered tcp trigger.
type tcpListener struct {
	port    int
	server  *tcpserver.Server
	trigger atomic.Pointer[models.WorkflowTriggerRecord]
}

func (l *tcpListener) shutdown() {
	ctx, cancel := context.WithTimeout(context.Background(), tcpShutdownTimeout)
	defer cancel()
	if err := l.server.Shutdown(ctx); err != nil {
		log.Errorf("workflow: tcp listener :%d shutdown: %v", l.port, err)
	}
}

// tcpTriggerRun runs one frame through the workflow; swappable in tests.
var tcpTriggerRun = func(ctx context.Context, dbName string, trigger models.WorkflowTriggerRecord, input []byte) ([]byte, bool, error) {
	runCtx, err := triggerDBContext(ctx, dbName)
	if err != nil {
		return nil, false, err
	}
	return executeWorkflowTriggerWithOutput(runCtx, trigger, input)
}

// sameTCPConfig reports whether both rows are tcp triggers with identical transport settings and
// the same workflow, so a re-sync can keep the live listener.
func sameTCPConfig(previous, next models.WorkflowTriggerRecord) bool {
	if strings.TrimSpace(previous.TriggerType) != "tcp" || strings.TrimSpace(next.TriggerType) != "tcp" {
		return false
	}
	if previous.WorkflowID == nil || next.WorkflowID == nil || *previous.WorkflowID != *next.WorkflowID {
		return false
	}
	a, _ := json.Marshal(previous.Config)
	b, _ := json.Marshal(next.Config)
	return string(a) == string(b)
}

// startTCPTriggerLocked binds the listener for entry's trigger; r.mu must be held. It returns the
// listener and its port. Bind failures (port in use by another process) surface as registration errors.
func (r *workflowTriggerRegistry) startTCPTriggerLocked(entry *registeredTrigger) (*tcpListener, int, error) {
	cfg := config.Get().Tcp.WithDefaults()
	if !cfg.UseTCP {
		return nil, 0, orm.InvalidParamsf("workflow: tcp trigger requires tcp.useTcp in the server config")
	}
	if err := cfg.Validate(); err != nil {
		return nil, 0, fmt.Errorf("workflow: %w", err)
	}
	trigger := entry.trigger
	port := int(configInt(trigger.Config, "port", int64(cfg.DefaultPort)))
	if port < cfg.PortMin || port > cfg.PortMax {
		return nil, 0, orm.InvalidParamsf("workflow: tcp trigger port %d outside %d-%d", port, cfg.PortMin, cfg.PortMax)
	}
	if owner, taken := r.tcpPorts[port]; taken && owner != entry.key {
		return nil, 0, orm.Conflictf("workflow: tcp port %d is already assigned to trigger %s", port, owner)
	}

	framer, err := tcpserver.NewFramer(configString(trigger.Config, "framing"), configBool(trigger.Config, "len_includes_header"))
	if err != nil {
		return nil, 0, fmt.Errorf("workflow: %w", err)
	}
	tlsCfg, err := tcpTLSConfig(cfg, configString(trigger.Config, "tls_mode"))
	if err != nil {
		return nil, 0, err
	}
	allow, err := parseCIDRList(cfg.AllowCIDRs)
	if err != nil {
		return nil, 0, fmt.Errorf("workflow: tcp.allowCidrs: %w", err)
	}
	trusted, err := parseCIDRList(cfg.TrustedProxies)
	if err != nil {
		return nil, 0, fmt.Errorf("workflow: tcp.trustedProxies: %w", err)
	}

	listener := &tcpListener{port: port}
	listener.trigger.Store(&trigger)
	dbName := entry.dbName
	key := entry.key
	handler := func(ctx context.Context, c *tcpserver.Conn, frame []byte) ([]byte, bool, error) {
		current := listener.trigger.Load()
		triggerHealth.deliveryStart(key)
		input, err := buildTCPEnvelope(c, frame, time.Now())
		if err != nil {
			triggerHealth.deliveryEnd(key, err)
			return nil, true, err
		}
		out, ok, err := tcpTriggerRun(ctx, dbName, *current, input)
		triggerHealth.deliveryEnd(key, err)
		if err != nil {
			log.Warnf("workflow: tcp trigger %s run failed: %v", key, err)
			return nil, true, err // failure or timeout: nothing is written, the connection closes
		}
		if !ok {
			return nil, true, nil
		}
		reply, closeAfter := parseTCPReply(out)
		return reply, closeAfter || len(reply) == 0, nil
	}

	srv, err := tcpserver.New(tcpserver.Config{
		Addr:           net.JoinHostPort(cfg.Bind, strconv.Itoa(port)),
		TLS:            tlsCfg,
		Framer:         framer,
		Handler:        handler,
		AllowCIDRs:     allow,
		ProxyMode:      cfg.ProxyProtocol,
		TrustedProxies: trusted,
		Limits: tcpserver.Limits{
			MaxConns:      cfg.MaxConns,
			MaxConnsPerIP: cfg.MaxConnsPerIP,
			MaxFrameBytes: cfg.MaxFrameBytes,
			FirstFrame:    time.Duration(cfg.FirstFrameS) * time.Second,
			Idle:          time.Duration(cfg.IdleTimeoutS) * time.Second,
			Reply:         time.Duration(configInt(trigger.Config, "reply_timeout_ms", 30000)) * time.Millisecond,
		},
	})
	if err != nil {
		return nil, 0, fmt.Errorf("workflow: %w", err)
	}
	if err := srv.Start(); err != nil {
		log.Errorf("workflow: tcp trigger %s cannot listen on :%d: %v", key, port, err)
		return nil, 0, fmt.Errorf("workflow: tcp listen :%d: %w", port, err)
	}
	listener.server = srv
	return listener, port, nil
}

func tcpTLSConfig(cfg config.TCPConfig, mode string) (*tls.Config, error) {
	switch mode {
	case "", "none":
		return nil, nil
	case "tls":
		return tcpserver.NewTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile, "")
	case "mtls":
		return tcpserver.NewTLSConfig(cfg.TLSCertFile, cfg.TLSKeyFile, cfg.TLSClientCAFile)
	}
	return nil, orm.InvalidParamsf("workflow: tcp trigger tls_mode %q is not supported", mode)
}

// parseCIDRList accepts CIDRs or bare IPs.
func parseCIDRList(items []string) ([]*net.IPNet, error) {
	out := make([]*net.IPNet, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if !strings.Contains(item, "/") {
			ip := net.ParseIP(item)
			if ip == nil {
				return nil, fmt.Errorf("invalid address %q", item)
			}
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			item = fmt.Sprintf("%s/%d", item, bits)
		}
		_, n, err := net.ParseCIDR(item)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// tcpEnvelope is the workflow run input for one frame: raw data and connection facts only.
type tcpEnvelope struct {
	Transport  string `json:"transport"`
	LocalPort  int    `json:"local_port"`
	RemoteAddr string `json:"remote_addr"`
	TLS        bool   `json:"tls"`
	ClientCN   string `json:"client_cn"`
	ConnID     string `json:"conn_id"`
	Seq        int64  `json:"seq"`
	ReceivedAt string `json:"received_at"`
	PayloadLen int    `json:"payload_len"`
	PayloadHex string `json:"payload_hex"`
}

func buildTCPEnvelope(c *tcpserver.Conn, frame []byte, now time.Time) ([]byte, error) {
	return json.Marshal(tcpEnvelope{
		Transport: "tcp", LocalPort: c.LocalPort, RemoteAddr: c.RemoteAddr, TLS: c.TLS, ClientCN: c.ClientCN,
		ConnID: c.ID, Seq: c.Seq, ReceivedAt: now.UTC().Format(time.RFC3339Nano),
		PayloadLen: len(frame), PayloadHex: hex.EncodeToString(frame),
	})
}

// parseTCPReply applies the reply contract to the workflow's final output. A nil reply means
// "write nothing"; the caller closes the connection in that case.
func parseTCPReply(output []byte) (reply []byte, closeAfter bool) {
	var out struct {
		ReplyHex string `json:"reply_hex"`
		Close    bool   `json:"close"`
	}
	if err := json.Unmarshal(output, &out); err != nil {
		return nil, true
	}
	if strings.TrimSpace(out.ReplyHex) != "" {
		decoded, err := hex.DecodeString(strings.TrimSpace(out.ReplyHex))
		if err != nil || len(decoded) == 0 {
			return nil, true
		}
		reply = decoded
	}
	return reply, out.Close
}

// executeWorkflowTriggerWithOutput runs one delivery and also returns the run's final payload when
// the run succeeded (ok=false when it ended without publishing one).
func executeWorkflowTriggerWithOutput(ctx context.Context, trigger models.WorkflowTriggerRecord, input []byte) ([]byte, bool, error) {
	sink := &models.WorkflowOutput{}
	if err := executeWorkflowTrigger(models.ContextWithWorkflowOutputSink(ctx, sink), trigger, input); err != nil {
		return nil, false, err
	}
	out, ok := sink.Get()
	return out, ok, nil
}
