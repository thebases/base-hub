package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"

	"git.thebase.vn/base_hub/internal/config"
	"git.thebase.vn/base_hub/internal/services/tcpserver"
	"git.thebase.vn/base_hub/modules/base-workflow/models"
)

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// useTCPConfig enables the transport on a single free loopback port range.
func useTCPConfig(t *testing.T, port int) {
	t.Helper()
	prev := config.Get().Tcp
	config.Update(func(c *config.AppConfig) {
		c.Tcp = config.TCPConfig{UseTCP: true, Bind: "127.0.0.1", DefaultPort: port, PortMin: port, PortMax: port}
	})
	t.Cleanup(func() { config.Update(func(c *config.AppConfig) { c.Tcp = prev }) })
}

func newTCPRegistry() *workflowTriggerRegistry {
	return &workflowTriggerRegistry{
		registered: map[string]registeredTrigger{},
		kafkaTopic: map[string]string{},
		mqttTopics: map[string]*mqttTopicSubscription{},
		tcpPorts:   map[int]string{},
	}
}

func tcpTrigger(id int64, port int, extra map[string]any) models.WorkflowTriggerRecord {
	workflowID := int64(1)
	cfg := map[string]any{"port": int64(port), "framing": "len2_bcd"}
	for k, v := range extra {
		cfg[k] = v
	}
	tr := models.WorkflowTriggerRecord{WorkflowID: &workflowID, TriggerType: "tcp", Topic: "tcp:" + strconv.Itoa(port), IsActive: true, Config: cfg}
	tr.ID = id
	return tr
}

func stubTCPRun(t *testing.T, fn func(input map[string]any) ([]byte, bool, error)) {
	t.Helper()
	prev := tcpTriggerRun
	tcpTriggerRun = func(_ context.Context, _ string, _ models.WorkflowTriggerRecord, input []byte) ([]byte, bool, error) {
		var env map[string]any
		if err := json.Unmarshal(input, &env); err != nil {
			t.Errorf("envelope is not JSON: %v", err)
		}
		return fn(env)
	}
	t.Cleanup(func() { tcpTriggerRun = prev })
}

func tcpDial(t *testing.T, port int) net.Conn {
	t.Helper()
	var c net.Conn
	var err error
	for i := 0; i < 50; i++ {
		if c, err = net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second); err == nil {
			t.Cleanup(func() { c.Close() })
			return c
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(err)
	return nil
}

func bcdFrame(body []byte) []byte {
	f, _ := tcpserver.NewFramer("len2_bcd", false)
	w, _ := f.Encode(body)
	return w
}

// frameReaders keeps one buffered reader per connection so pipelined replies are not lost between reads.
var frameReaders sync.Map

func readFrame(t *testing.T, c net.Conn) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	f, _ := tcpserver.NewFramer("len2_bcd", false)
	r, _ := frameReaders.LoadOrStore(c, bufio.NewReader(c))
	b, err := f.ReadFrame(r.(*bufio.Reader), 4096)
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return b
}

func expectTCPClosedNoData(t *testing.T, c net.Conn) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := c.Read(make([]byte, 16))
	if n != 0 || err == nil {
		t.Fatalf("expected close with no data, got n=%d err=%v", n, err)
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		t.Fatal("connection stayed open")
	}
}

func TestTCPTriggerEchoEnvelopeAndOrder(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	var mu sync.Mutex
	var envs []map[string]any
	stubTCPRun(t, func(env map[string]any) ([]byte, bool, error) {
		mu.Lock()
		envs = append(envs, env)
		mu.Unlock()
		return []byte(`{"reply_hex":"` + env["payload_hex"].(string) + `","other":1}`), true, nil
	})
	reg := newTCPRegistry()
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, nil)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Shutdown)

	c := tcpDial(t, port)
	bodies := [][]byte{{0x60, 0x00, 0x01}, {0x02, 0x00}, {0xff}}
	for _, b := range bodies { // pipelined: replies must come back in request order
		c.Write(bcdFrame(b))
	}
	for i, b := range bodies {
		if got := readFrame(t, c); !bytes.Equal(got, b) {
			t.Fatalf("reply %d = % x, want % x", i, got, b)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	env := envs[0]
	if env["transport"] != "tcp" || env["payload_hex"] != "600001" || env["payload_len"] != float64(3) ||
		env["tls"] != false || env["seq"] != float64(1) || env["local_port"] != float64(port) ||
		env["conn_id"] == "" || env["remote_addr"] == "" || env["received_at"] == "" || env["client_cn"] != "" {
		t.Fatalf("envelope wrong: %#v", env)
	}
	if envs[2]["seq"] != float64(3) {
		t.Fatalf("seq must count frames on the connection: %#v", envs[2]["seq"])
	}
}

func TestTCPTriggerReplyContract(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	outputs := map[string]struct {
		out []byte
		ok  bool
		err error
	}{
		"01": {[]byte(`{"reply_hex":"aabb"}`), true, nil},              // reply, stay open
		"02": {[]byte(`{"reply_hex":"aabb","close":true}`), true, nil}, // reply then close
		"03": {[]byte(`{"close":true}`), true, nil},                    // close, no reply
		"04": {[]byte(`{"nothing":1}`), true, nil},                     // no reply_hex: close
		"05": {[]byte(`{"reply_hex":"zz"}`), true, nil},                // undecodable: close
		"06": {nil, false, nil},                                        // no output published: close
		"07": {nil, false, errors.New("workflow failed")},              // failure: close
		"08": {[]byte(`not json`), true, nil},
	}
	stubTCPRun(t, func(env map[string]any) ([]byte, bool, error) {
		o := outputs[env["payload_hex"].(string)]
		return o.out, o.ok, o.err
	})
	reg := newTCPRegistry()
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, nil)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reg.Shutdown)

	send := func(key string) net.Conn {
		c := tcpDial(t, port)
		raw, _ := hex.DecodeString(key)
		c.Write(bcdFrame(raw))
		return c
	}
	c := send("01")
	if got := readFrame(t, c); !bytes.Equal(got, []byte{0xaa, 0xbb}) {
		t.Fatalf("reply % x", got)
	}
	raw, _ := hex.DecodeString("01")
	c.Write(bcdFrame(raw)) // connection stayed open for a second frame
	readFrame(t, c)

	c = send("02")
	readFrame(t, c)
	expectTCPClosedNoData(t, c)

	for _, k := range []string{"03", "04", "05", "06", "07", "08"} {
		expectTCPClosedNoData(t, send(k))
	}
}

func TestTCPTriggerGuardsAndLifecycle(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	stubTCPRun(t, func(env map[string]any) ([]byte, bool, error) {
		return []byte(`{"reply_hex":"` + env["payload_hex"].(string) + `"}`), true, nil
	})
	reg := newTCPRegistry()
	t.Cleanup(reg.Shutdown)

	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, nil)); err != nil {
		t.Fatal(err)
	}
	// Another trigger (or tenant) on the same port is refused.
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(6, port, nil)); err == nil {
		t.Fatal("same port, other trigger must conflict")
	}
	if err := reg.Register(dbContext("tenant_b"), tcpTrigger(5, port, nil)); err == nil {
		t.Fatal("same port, other tenant must conflict")
	}
	if len(reg.registered) != 1 {
		t.Fatalf("failed registrations must not be recorded: %d", len(reg.registered))
	}

	// An unchanged re-sync keeps the live connection.
	c := tcpDial(t, port)
	c.Write(bcdFrame([]byte{1}))
	readFrame(t, c)
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, nil)); err != nil {
		t.Fatal(err)
	}
	c.Write(bcdFrame([]byte{2}))
	if got := readFrame(t, c); !bytes.Equal(got, []byte{2}) {
		t.Fatalf("connection must survive an unchanged re-sync, got % x", got)
	}

	// A changed config on the same key re-binds the same port (old listener closed first).
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, map[string]any{"framing": "len2_bin"})); err != nil {
		t.Fatalf("changed config on the same port: %v", err)
	}
	expectTCPClosedNoData(t, c)

	// Unregister frees the port: the port is bindable again.
	if err := reg.Unregister(dbContext("tenant_a"), 5); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("port still held after unregister: %v", err)
	}
	// A port used by another process is a registration error, not a panic.
	if err := reg.Register(dbContext("tenant_a"), tcpTrigger(5, port, nil)); err == nil {
		t.Fatal("bind on a busy port must fail")
	}
	ln.Close()
	if len(reg.tcpPorts) != 0 {
		t.Fatalf("tcpPorts leaked: %v", reg.tcpPorts)
	}
}

func TestTCPTriggerDisabledAndRange(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	config.Update(func(c *config.AppConfig) { c.Tcp.UseTCP = false })
	reg := newTCPRegistry()
	if err := reg.Register(dbContext("t"), tcpTrigger(1, port, nil)); err == nil {
		t.Fatal("useTcp=false must refuse registration")
	}
	config.Update(func(c *config.AppConfig) { c.Tcp.UseTCP = true })
	if err := reg.Register(dbContext("t"), tcpTrigger(1, port+1, nil)); err == nil {
		t.Fatal("port outside the configured range must be refused")
	}
	if _, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 200*time.Millisecond); err == nil {
		t.Fatal("nothing must listen when registration failed")
	}
}

func TestTCPTriggerShutdownClosesListeners(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	stubTCPRun(t, func(map[string]any) ([]byte, bool, error) { return []byte(`{"reply_hex":"01"}`), true, nil })
	reg := newTCPRegistry()
	if err := reg.Register(dbContext("t"), tcpTrigger(1, port, nil)); err != nil {
		t.Fatal(err)
	}
	c := tcpDial(t, port)
	c.Write(bcdFrame([]byte{1}))
	readFrame(t, c)
	reg.Shutdown()
	expectTCPClosedNoData(t, c)
	if len(reg.tcpPorts) != 0 || len(reg.registered) != 0 {
		t.Fatal("Shutdown must reset registry state")
	}
}

func TestTCPTriggerSlowRunDoesNotWriteAfterTimeout(t *testing.T) {
	port := freePort(t)
	useTCPConfig(t, port)
	stubTCPRun(t, func(map[string]any) ([]byte, bool, error) {
		time.Sleep(300 * time.Millisecond)
		return nil, false, context.DeadlineExceeded
	})
	reg := newTCPRegistry()
	t.Cleanup(reg.Shutdown)
	if err := reg.Register(dbContext("t"), tcpTrigger(1, port, map[string]any{"reply_timeout_ms": int64(100)})); err != nil {
		t.Fatal(err)
	}
	c := tcpDial(t, port)
	c.Write(bcdFrame([]byte{1}))
	expectTCPClosedNoData(t, c)
}

func TestParseTCPReply(t *testing.T) {
	if r, c := parseTCPReply([]byte(`{"reply_hex":" 0a0B "}`)); !bytes.Equal(r, []byte{0x0a, 0x0b}) || c {
		t.Fatalf("%x %v", r, c)
	}
	if r, c := parseTCPReply([]byte(`{"reply_hex":"","close":true}`)); r != nil || !c {
		t.Fatalf("%x %v", r, c)
	}
	if _, err := io.ReadAll(bytes.NewReader(nil)); err != nil {
		t.Fatal(err)
	}
}

func TestSameTCPConfigIgnoresWorkflowChange(t *testing.T) {
	a := tcpTrigger(1, 10123, nil)
	b := tcpTrigger(1, 10123, nil)
	if !sameTCPConfig(a, b) {
		t.Fatal("identical rows must match")
	}
	other := int64(2)
	b.WorkflowID = &other
	if sameTCPConfig(a, b) {
		t.Fatal("a changed workflow must not be treated as unchanged")
	}
}

func TestParseCIDRList(t *testing.T) {
	nets, err := parseCIDRList([]string{"10.0.0.0/8", " 127.0.0.1 ", "::1", ""})
	if err != nil || len(nets) != 3 {
		t.Fatalf("%v %v", nets, err)
	}
	if _, err := parseCIDRList([]string{"nope"}); err == nil {
		t.Fatal("invalid entry must fail")
	}
}
