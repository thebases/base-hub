package tcpserver

// File overview:
//   - Responsibility: builds the server TLS config (TLS 1.2+, optional client-certificate verification)
//     with certificates re-read from disk when the files change, so rotation needs no restart.

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
	"time"
)

// certReloadEvery is how often the cert/key files are re-stat'ed.
const certReloadEvery = 60 * time.Second

type reloadingCert struct {
	certFile, keyFile string
	every             time.Duration
	mu                sync.Mutex
	cert              *tls.Certificate
	certMod, keyMod   time.Time
	checkedAt         time.Time
}

func modTime(path string) (time.Time, error) {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}, err
	}
	return st.ModTime(), nil
}

func (c *reloadingCert) load() error {
	cm, err := modTime(c.certFile)
	if err != nil {
		return err
	}
	km, err := modTime(c.keyFile)
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(c.certFile, c.keyFile)
	if err != nil {
		return err
	}
	c.cert, c.certMod, c.keyMod = &cert, cm, km
	return nil
}

func (c *reloadingCert) get(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.checkedAt) >= c.every {
		c.checkedAt = time.Now()
		cm, cerr := modTime(c.certFile)
		km, kerr := modTime(c.keyFile)
		// A failed re-stat or re-load keeps serving the previous certificate.
		if cerr == nil && kerr == nil && (!cm.Equal(c.certMod) || !km.Equal(c.keyMod)) {
			_ = c.load()
		}
	}
	return c.cert, nil
}

// NewTLSConfig builds a server TLS config. clientCAFile non-empty turns on RequireAndVerifyClientCert.
func NewTLSConfig(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	return newTLSConfig(certFile, keyFile, clientCAFile, certReloadEvery)
}

func newTLSConfig(certFile, keyFile, clientCAFile string, every time.Duration) (*tls.Config, error) {
	rc := &reloadingCert{certFile: certFile, keyFile: keyFile, every: every, checkedAt: time.Now()}
	if err := rc.load(); err != nil {
		return nil, fmt.Errorf("tcpserver: load tls keypair: %w", err)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: rc.get}
	if clientCAFile != "" {
		pem, err := os.ReadFile(clientCAFile)
		if err != nil {
			return nil, fmt.Errorf("tcpserver: read client CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("tcpserver: client CA file has no certificates")
		}
		cfg.ClientCAs = pool
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
