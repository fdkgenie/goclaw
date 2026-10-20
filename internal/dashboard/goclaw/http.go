package goclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout = 15 * time.Second
)

// HTTPClient is an HTTP client for calling the GoClaw Gateway's read-only API.
// Every request injects Authorization: Bearer <key> and
// X-GoClaw-Tenant-Id: <tenant_id>. Paths are checked against the allowlist
// before the request is issued (fail-closed).
type HTTPClient struct {
	http     *http.Client
	baseURL  string
	apiKey   string
	tenantID string
}

// NewHTTPClient creates an HTTP client. timeout defaults to 15s if zero.
func NewHTTPClient(baseURL, apiKey, tenantID string, timeout time.Duration) *HTTPClient {
	if timeout == 0 {
		timeout = defaultHTTPTimeout
	}
	return &HTTPClient{
		http:     &http.Client{Timeout: timeout},
		baseURL:  baseURL,
		apiKey:   apiKey,
		tenantID: tenantID,
	}
}

// Get issues a GET request to path if allow-listed, decodes JSON into out.
func (c *HTTPClient) Get(ctx context.Context, path string, out any) error {
	if !IsAllowedHTTPPath("GET", stripQuery(path)) {
		LogBlocked("http", "GET", path)
		return fmt.Errorf("http path not allowed: GET %s", path)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("X-GoClaw-Tenant-Id", c.tenantID)

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("gateway GET %s: status %d: %s", path, resp.StatusCode, string(body))
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// stripQuery returns the URL path without the query string, for allowlist
// matching. The query is preserved on the actual request.
func stripQuery(path string) string {
	if i := strings.IndexByte(path, '?'); i >= 0 {
		return path[:i]
	}
	return path
}
