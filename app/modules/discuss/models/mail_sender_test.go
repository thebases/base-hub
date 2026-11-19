package models

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSendMailSMTPStalledServerReturnsOnContextDeadline(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			defer conn.Close()
			time.Sleep(5 * time.Second) // accept, never send the 220 greeting
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	err = sendMailSMTP(ctx, ln.Addr().String(), "127.0.0.1", false, nil, "a@x", []string{"b@x"}, []byte("hi"))
	if err == nil {
		t.Fatal("expected an error from a stalled SMTP server")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("stalled server held the sender for %s", elapsed)
	}
}

func TestSendMailSMTPPlainDelivery(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		r := bufio.NewReader(conn)
		w := func(s string) { conn.Write([]byte(s + "\r\n")) }
		w("220 test")
		var body strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case inData && line == ".":
				inData = false
				got <- body.String()
				w("250 ok")
			case inData:
				body.WriteString(line + "\n")
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250 test")
			case line == "DATA":
				inData = true
				w("354 go")
			case line == "QUIT":
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	err = sendMailSMTP(context.Background(), ln.Addr().String(), "127.0.0.1", false, nil, "a@x", []string{"b@x"}, mustBuildMIMEMessage(t, "a@x", "b@x", "", "subj", "<p>hello</p>"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case body := <-got:
		if !strings.Contains(body, "Subject: subj") || !strings.Contains(body, "<p>hello</p>") {
			t.Fatalf("unexpected message: %q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never received the message")
	}
}

func mustBuildMIMEMessage(t *testing.T, from, to, cc, subject, body string) []byte {
	t.Helper()
	message, err := buildMIMEMessage(from, to, cc, subject, body)
	if err != nil {
		t.Fatalf("build MIME: %v", err)
	}
	return message
}

// T23: CR/LF in any header value is refused; the subject is Q-encoded; Date and Message-ID exist.
func TestBuildMIMEMessageHeaders(t *testing.T) {
	for _, tc := range [][4]string{
		{"a@x", "b@x\r\nBcc: evil@x", "", "s"},
		{"a@x", "b@x", "c@x\nBcc: evil@x", "s"},
		{"a@x", "b@x", "", "hi\r\nBcc: evil@x"},
	} {
		if _, err := buildMIMEMessage(tc[0], tc[1], tc[2], tc[3], "<p/>"); err == nil {
			t.Errorf("header injection accepted: %q", tc)
		}
	}
	message := string(mustBuildMIMEMessage(t, "no-reply@example.com", "b@x", "", "Xin chào", "<p/>"))
	for _, want := range []string{"Subject: =?utf-8?q?Xin_ch=C3=A0o?=\r\n", "\r\nDate: ", "\r\nMessage-ID: <", "@example.com>\r\n"} {
		if !strings.Contains(message, want) {
			t.Errorf("message missing %q:\n%s", want, message)
		}
	}
}
