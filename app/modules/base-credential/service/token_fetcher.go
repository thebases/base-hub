package service

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// authRequestTimeout bounds one third-party auth call, including reading the body.
const authRequestTimeout = 15 * time.Second

// maxAuthResponseBytes caps what a misbehaving auth endpoint can make us buffer.
const maxAuthResponseBytes = 1 << 20

// TokenFetcher performs the single POST a credential login needs. It is the
// seam that lets login tests run against a fake instead of a live endpoint.
type TokenFetcher interface {
	PostAuth(ctx context.Context, url, contentType string, body []byte) (status int, response []byte, err error)
}

// tokenFetcher is the fetcher LoginCredential uses; tests replace it.
var tokenFetcher TokenFetcher = httpTokenFetcher{client: &http.Client{Timeout: authRequestTimeout}}

type httpTokenFetcher struct {
	client *http.Client
}

func (f httpTokenFetcher) PostAuth(ctx context.Context, url, contentType string, body []byte) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthResponseBytes))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("read auth response: %w", err)
	}
	return resp.StatusCode, raw, nil
}
