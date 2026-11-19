package tcpserver

// File overview:
//   - Responsibility: delimits one message on a raw TCP stream. A framer interprets only the length
//     header or the STX/ETX delimiters; the body is opaque bytes and is never decoded or validated.
//   - A malformed header (bad digit, length over the limit, missing ETX before the limit) returns an
//     error wrapping ErrFraming; the server closes the connection. That is framing, not message validation.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// ErrFraming marks a malformed frame header or delimiter.
var ErrFraming = errors.New("tcpserver: framing error")

// Framing names accepted by NewFramer.
const (
	FramingLen2BCD    = "len2_bcd"
	FramingLen2Bin    = "len2_bin"
	FramingSTXETX     = "stx_etx"
	FramingLen4ASCII  = "len4_ascii"
	stx               = 0x02
	etx               = 0x03
	maxHeaderCounted4 = 9999
)

// Framer reads one frame body from r and encodes one reply body into a wire frame.
type Framer interface {
	// ReadFrame returns the body (header stripped) of the next frame, at most max bytes.
	ReadFrame(r *bufio.Reader, max int) ([]byte, error)
	// Encode wraps body with the header (or delimiters) for the wire.
	Encode(body []byte) ([]byte, error)
}

// NormalizeFraming maps "" to the default framer name and reports whether name is supported.
func NormalizeFraming(name string) (string, bool) {
	switch name {
	case "":
		return FramingLen2BCD, true
	case FramingLen2BCD, FramingLen2Bin, FramingSTXETX, FramingLen4ASCII:
		return name, true
	}
	return name, false
}

// NewFramer builds the framer for name. lenIncludesHeader makes the length field count the header
// bytes too (ignored by stx_etx).
func NewFramer(name string, lenIncludesHeader bool) (Framer, error) {
	name, ok := NormalizeFraming(name)
	if !ok {
		return nil, fmt.Errorf("tcpserver: unknown framing %q", name)
	}
	switch name {
	case FramingSTXETX:
		return stxEtxFramer{}, nil
	default:
		return lenFramer{kind: name, includesHeader: lenIncludesHeader}, nil
	}
}

type lenFramer struct {
	kind           string
	includesHeader bool
}

func (f lenFramer) headerSize() int {
	if f.kind == FramingLen4ASCII {
		return 4
	}
	return 2
}

func (f lenFramer) ReadFrame(r *bufio.Reader, max int) ([]byte, error) {
	hs := f.headerSize()
	hdr := make([]byte, hs)
	if _, err := io.ReadFull(r, hdr); err != nil {
		// Clean EOF before the first header byte is a normal close; a partial header is not.
		if err == io.EOF {
			return nil, io.EOF
		}
		return nil, err
	}
	n, err := f.decode(hdr)
	if err != nil {
		return nil, err
	}
	if f.includesHeader {
		n -= hs
		if n < 0 {
			return nil, fmt.Errorf("%w: length %d shorter than header", ErrFraming, n+hs)
		}
	}
	if n > max {
		return nil, fmt.Errorf("%w: length %d exceeds limit %d", ErrFraming, n, max)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return body, nil
}

func (f lenFramer) decode(hdr []byte) (int, error) {
	switch f.kind {
	case FramingLen2Bin:
		return int(hdr[0])<<8 | int(hdr[1]), nil
	case FramingLen2BCD:
		n := 0
		for _, b := range hdr {
			hi, lo := int(b>>4), int(b&0x0f)
			if hi > 9 || lo > 9 {
				return 0, fmt.Errorf("%w: non-BCD length byte 0x%02x", ErrFraming, b)
			}
			n = n*100 + hi*10 + lo
		}
		return n, nil
	default: // len4_ascii
		n := 0
		for _, b := range hdr {
			if b < '0' || b > '9' {
				return 0, fmt.Errorf("%w: non-digit length byte 0x%02x", ErrFraming, b)
			}
			n = n*10 + int(b-'0')
		}
		return n, nil
	}
}

func (f lenFramer) Encode(body []byte) ([]byte, error) {
	hs := f.headerSize()
	n := len(body)
	if f.includesHeader {
		n += hs
	}
	out := make([]byte, 0, hs+len(body))
	switch f.kind {
	case FramingLen2Bin:
		if n > 0xffff {
			return nil, fmt.Errorf("%w: reply length %d too large", ErrFraming, n)
		}
		out = append(out, byte(n>>8), byte(n))
	case FramingLen2BCD:
		if n > maxHeaderCounted4 {
			return nil, fmt.Errorf("%w: reply length %d too large", ErrFraming, n)
		}
		out = append(out, byte((n/1000)<<4|(n/100)%10), byte(((n/10)%10)<<4|n%10))
	default:
		if n > maxHeaderCounted4 {
			return nil, fmt.Errorf("%w: reply length %d too large", ErrFraming, n)
		}
		out = append(out, []byte(fmt.Sprintf("%04d", n))...)
	}
	return append(out, body...), nil
}

type stxEtxFramer struct{}

func (stxEtxFramer) ReadFrame(r *bufio.Reader, max int) ([]byte, error) {
	b, err := r.ReadByte()
	if err != nil {
		return nil, err // io.EOF here is a clean close
	}
	if b != stx {
		return nil, fmt.Errorf("%w: expected STX, got 0x%02x", ErrFraming, b)
	}
	body := make([]byte, 0, 256)
	for {
		c, err := r.ReadByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if c == etx {
			return body, nil
		}
		if len(body) >= max {
			return nil, fmt.Errorf("%w: no ETX within %d bytes", ErrFraming, max)
		}
		body = append(body, c)
	}
}

func (stxEtxFramer) Encode(body []byte) ([]byte, error) {
	out := make([]byte, 0, len(body)+2)
	out = append(out, stx)
	out = append(out, body...)
	return append(out, etx), nil
}
