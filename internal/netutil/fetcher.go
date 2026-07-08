// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package netutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"time"
)

// Fetcher is a configurable HTTP client for simple GET requests.
type Fetcher struct {
	Timeout      time.Duration
	UserAgent    string
	MaxRedirects int
	ProxyURL     string
	RetryCount   int
	RetryBackoff time.Duration
	MaxBodyBytes int64
}

// Fetch performs an HTTP GET for url and returns the response body.
func (f *Fetcher) Fetch(ctx context.Context, urlStr string) (*Response, error) {
	if f.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.Timeout)
		defer cancel()
	}

	lastErr := fmt.Errorf("fetch failed")
	attempts := max(f.RetryCount+1, 1)
	for i := 0; i < attempts; i++ {
		if i > 0 && f.RetryBackoff > 0 {
			select {
			case <-time.After(f.RetryBackoff * time.Duration(i)):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		resp, err := f.fetchOnce(ctx, urlStr)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !isRetryable(err) {
			return nil, err
		}
	}
	return nil, lastErr
}

func (f *Fetcher) fetchOnce(ctx context.Context, urlStr string) (*Response, error) {
	client, err := f.httpClient()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{
			URL:        urlStr,
			StatusCode: resp.StatusCode,
			Status:     resp.Status,
		}
	}

	body, err := f.readBody(resp.Body)
	if err != nil {
		return nil, err
	}

	return &Response{
		URL:         resp.Request.URL.String(),
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        body,
	}, nil
}

func (f *Fetcher) httpClient() (*http.Client, error) {
	transport := &http.Transport{
		Proxy:           http.ProxyFromEnvironment,
		MaxConnsPerHost: 10,
	}
	if f.ProxyURL != "" {
		u, err := url.Parse(f.ProxyURL)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy URL: %w", err)
		}
		transport.Proxy = http.ProxyURL(u)
	}

	checkRedirect := func(req *http.Request, via []*http.Request) error {
		if len(via) >= max(f.MaxRedirects, 0) {
			return fmt.Errorf("stopped after %d redirects", len(via))
		}
		return nil
	}
	if f.MaxRedirects < 0 {
		checkRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	}

	return &http.Client{
		Transport:     transport,
		CheckRedirect: checkRedirect,
	}, nil
}

func (f *Fetcher) readBody(r io.Reader) ([]byte, error) {
	limit := f.MaxBodyBytes
	if limit <= 0 {
		limit = 32 << 20 // 32 MiB default
	}
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response body exceeds %d bytes", limit)
	}
	return data, nil
}

// HTTPError is returned for non-2xx status codes.
type HTTPError struct {
	URL        string
	StatusCode int
	Status     string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("HTTP %d %s for %s", e.StatusCode, e.Status, e.URL)
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout()
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == syscall.ECONNRESET || errno == syscall.ECONNREFUSED || errno == syscall.EPIPE
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
