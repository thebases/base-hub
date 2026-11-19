package tcpserver

// File overview:
//   - Responsibility: parses a PROXY protocol header (v1 text, v2 binary; TCP over IPv4/IPv6) from the
//     start of a connection, before any TLS handshake. Only the client address is extracted.

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
)

// Proxy protocol modes.
const (
	ProxyNone = "none"
	ProxyV1   = "v1"
	ProxyV2   = "v2"
	ProxyAuto = "auto"
)

var proxyV2Sig = []byte("\r\n\r\n\x00\r\nQUIT\n")

var errProxyHeader = errors.New("tcpserver: bad PROXY header")

// NormalizeProxyMode maps "" to none and reports whether mode is supported.
func NormalizeProxyMode(mode string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", ProxyNone:
		return ProxyNone, true
	case ProxyV1:
		return ProxyV1, true
	case ProxyV2:
		return ProxyV2, true
	case ProxyAuto:
		return ProxyAuto, true
	}
	return mode, false
}

// readProxyHeader consumes one PROXY header from r. src is the original client address, or nil for a
// v2 LOCAL command / v1 UNKNOWN (the caller keeps the socket peer address).
func readProxyHeader(r *bufio.Reader, mode string) (src net.Addr, err error) {
	// Decide from the first byte so a short frame without a header is rejected immediately instead
	// of blocking until the handshake deadline waiting for 12 bytes.
	first, err := r.Peek(1)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errProxyHeader, err)
	}
	switch first[0] {
	case proxyV2Sig[0]:
		sig, err := r.Peek(len(proxyV2Sig))
		if err != nil || !bytes.Equal(sig, proxyV2Sig) {
			return nil, fmt.Errorf("%w: bad v2 signature", errProxyHeader)
		}
		if mode == ProxyV1 {
			return nil, fmt.Errorf("%w: v2 header on a v1 listener", errProxyHeader)
		}
		return readProxyV2(r)
	case 'P':
		sig, err := r.Peek(6)
		if err != nil || string(sig) != "PROXY " {
			return nil, fmt.Errorf("%w: bad v1 signature", errProxyHeader)
		}
		if mode == ProxyV2 {
			return nil, fmt.Errorf("%w: v1 header on a v2 listener", errProxyHeader)
		}
		return readProxyV1(r)
	}
	return nil, fmt.Errorf("%w: missing", errProxyHeader)
}

func readProxyV1(r *bufio.Reader) (net.Addr, error) {
	line, err := r.ReadSlice('\n') // the v1 line is at most 107 bytes; the default buffer is larger
	if err != nil || len(line) > 107 || !bytes.HasSuffix(line, []byte("\r\n")) {
		return nil, fmt.Errorf("%w: v1 line", errProxyHeader)
	}
	parts := strings.Fields(strings.TrimSpace(string(line)))
	if len(parts) >= 2 && parts[1] == "UNKNOWN" {
		return nil, nil
	}
	if len(parts) != 6 || (parts[1] != "TCP4" && parts[1] != "TCP6") {
		return nil, fmt.Errorf("%w: v1 fields", errProxyHeader)
	}
	ip := net.ParseIP(parts[2])
	port, perr := strconv.Atoi(parts[4])
	if ip == nil || perr != nil || port < 0 || port > 65535 {
		return nil, fmt.Errorf("%w: v1 address", errProxyHeader)
	}
	return &net.TCPAddr{IP: ip, Port: port}, nil
}

func readProxyV2(r *bufio.Reader) (net.Addr, error) {
	fixed := make([]byte, 16)
	if _, err := io.ReadFull(r, fixed); err != nil {
		return nil, fmt.Errorf("%w: v2 header: %v", errProxyHeader, err)
	}
	if fixed[12]>>4 != 2 {
		return nil, fmt.Errorf("%w: v2 version", errProxyHeader)
	}
	cmd, fam := fixed[12]&0x0f, fixed[13]
	n := int(binary.BigEndian.Uint16(fixed[14:16]))
	if n > 536 { // generous cap for address block + TLVs
		return nil, fmt.Errorf("%w: v2 length", errProxyHeader)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("%w: v2 payload: %v", errProxyHeader, err)
	}
	if cmd == 0 { // LOCAL: health check from the proxy, no client address
		return nil, nil
	}
	if cmd != 1 {
		return nil, fmt.Errorf("%w: v2 command", errProxyHeader)
	}
	switch fam {
	case 0x11: // TCP over IPv4
		if n < 12 {
			return nil, fmt.Errorf("%w: v2 ipv4 block", errProxyHeader)
		}
		return &net.TCPAddr{IP: net.IP(append([]byte(nil), payload[0:4]...)), Port: int(binary.BigEndian.Uint16(payload[8:10]))}, nil
	case 0x21: // TCP over IPv6
		if n < 36 {
			return nil, fmt.Errorf("%w: v2 ipv6 block", errProxyHeader)
		}
		return &net.TCPAddr{IP: net.IP(append([]byte(nil), payload[0:16]...)), Port: int(binary.BigEndian.Uint16(payload[32:34]))}, nil
	}
	return nil, nil // UDP/unix or unspecified: keep the socket peer
}
