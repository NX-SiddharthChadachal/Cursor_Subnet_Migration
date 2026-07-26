// Package rest implements the service layer directly against the Nutanix v4
// REST APIs. It is the fallback used when the Go SDK backend is not a fit, and
// it is also the only place that can read the two legacy Prism Element
// inventories (protection domains and file server networks) that the v4
// namespaces do not cover.
package rest

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/model"
	"github.com/NX-SiddharthChadachal/Cursor_Subnet_Migration/internal/service"
)

// pageSize matches the maximum the v4 collections accept.
const pageSize = 100

// namespaceVersions lists the highest namespace version this tool understands.
// The client negotiates downwards when a Prism instance serves an older one, the
// same way the Nutanix SDKs do.
var namespaceVersions = map[string]string{
	"networking":     "v4.3",
	"vmm":            "v4.2",
	"clustermgmt":    "v4.2",
	"prism":          "v4.3",
	"dataprotection": "v4.3",
	"files":          "v4.0",
}

// httpError carries the status code so callers can distinguish "absent" from
// "broken".
type httpError struct {
	StatusCode int
	Method     string
	Path       string
	Body       string
}

func (e *httpError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 512 {
		body = body[:512] + "..."
	}
	if body == "" {
		return fmt.Sprintf("%s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.Method, e.Path, e.StatusCode, body)
}

// IsNotFound reports whether err is a 404 from a Prism API.
func IsNotFound(err error) bool {
	he, ok := err.(*httpError)
	return ok && he.StatusCode == http.StatusNotFound
}

// IsUnsupported reports whether err indicates the endpoint does not serve the
// requested API at all.
func IsUnsupported(err error) bool {
	he, ok := err.(*httpError)
	if !ok {
		return false
	}
	switch he.StatusCode {
	case http.StatusNotFound, http.StatusNotImplemented, http.StatusBadGateway, http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
}

// Client is a thin JSON-over-HTTPS client for one Prism endpoint.
type Client struct {
	endpoint model.Endpoint
	http     *http.Client

	mu       sync.Mutex
	resolved map[string]string
}

// NewClient builds a REST client for an endpoint.
func NewClient(ep model.Endpoint) *Client {
	transport := &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		MaxIdleConns:          20,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		// Prism ships with a self-signed certificate; operators overwhelmingly
		// run these tools with verification off, so it is opt-in rather than
		// silently disabled.
		TLSClientConfig: &tls.Config{InsecureSkipVerify: ep.Insecure}, //nolint:gosec // controlled by --insecure
	}
	return &Client{
		endpoint: ep,
		http:     &http.Client{Transport: transport, Timeout: 90 * time.Second},
		resolved: map[string]string{},
	}
}

// Endpoint returns the endpoint the client talks to.
func (c *Client) Endpoint() model.Endpoint { return c.endpoint }

// namespaceVersion returns the API version to use for a namespace, asking the
// server once and caching the answer.
func (c *Client) namespaceVersion(ctx context.Context, namespace string) string {
	preferred := namespaceVersions[namespace]

	c.mu.Lock()
	if v, ok := c.resolved[namespace]; ok {
		c.mu.Unlock()
		return v
	}
	c.mu.Unlock()

	resolved := preferred
	if served, err := c.probeNamespaceVersion(ctx, namespace); err == nil && served != "" {
		resolved = lowerVersion(preferred, served)
	}

	c.mu.Lock()
	c.resolved[namespace] = resolved
	c.mu.Unlock()
	return resolved
}

// probeNamespaceVersion asks a namespace which major.minor it serves. Prism
// answers an OPTIONS on the unversioned info path with a bare version string.
func (c *Client) probeNamespaceVersion(ctx context.Context, namespace string) (string, error) {
	var payload struct {
		Data string `json:"data"`
	}
	path := fmt.Sprintf("/api/%s/unversioned/info", namespace)
	if err := c.do(ctx, http.MethodOptions, path, nil, nil, &payload); err != nil {
		return "", err
	}
	return payload.Data, nil
}

// nsPath builds a fully qualified v4 path for a namespace-relative suffix.
func (c *Client) nsPath(ctx context.Context, namespace, suffix string) string {
	return fmt.Sprintf("/api/%s/%s/%s", namespace, c.namespaceVersion(ctx, namespace), strings.TrimPrefix(suffix, "/"))
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body any, out any) error {
	target := c.endpoint.BaseURL() + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request body for %s: %w", path, err)
		}
		reader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", path, err)
	}
	req.SetBasicAuth(c.endpoint.Username, c.endpoint.Password)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// The v4 APIs use this header as an idempotence token for safe retries.
	req.Header.Set("NTNX-Request-Id", newRequestID())

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response from %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &httpError{StatusCode: resp.StatusCode, Method: method, Path: path, Body: string(payload)}
	}
	if out == nil || len(payload) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode response from %s: %w", path, err)
	}
	return nil
}

// getJSON issues a GET and decodes the response into out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	return c.do(ctx, http.MethodGet, path, query, nil, out)
}

// postJSON issues a POST and decodes the response into out.
func (c *Client) postJSON(ctx context.Context, path string, body, out any) error {
	return c.do(ctx, http.MethodPost, path, nil, body, out)
}

// listPaged walks a v4 collection. decode receives one page of raw JSON and
// returns how many records it held.
func (c *Client) listPaged(ctx context.Context, path string, extra url.Values, decode func(raw json.RawMessage) (int, error)) error {
	const maxPages = 1000
	for page := 0; page < maxPages; page++ {
		query := url.Values{}
		for k, vs := range extra {
			query[k] = vs
		}
		query.Set("$page", fmt.Sprint(page))
		query.Set("$limit", fmt.Sprint(pageSize))

		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := c.getJSON(ctx, path, query, &envelope); err != nil {
			return err
		}
		if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
			return nil
		}
		n, err := decode(envelope.Data)
		if err != nil {
			return err
		}
		if n < pageSize {
			return nil
		}
	}
	return fmt.Errorf("aborting pagination of %s after %d pages", path, maxPages)
}

// translate maps transport errors onto the sentinel errors the components
// understand.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case IsNotFound(err):
		return fmt.Errorf("%w: %s", service.ErrNotFound, err)
	case IsUnsupported(err):
		return fmt.Errorf("%w: %s", service.ErrUnsupported, err)
	default:
		return err
	}
}

// lowerVersion returns the smaller of two "vMAJOR.MINOR" strings so that this
// tool never asks a Prism instance for a namespace version it does not serve.
func lowerVersion(a, b string) string {
	amaj, amin, aok := parseNamespaceVersion(a)
	bmaj, bmin, bok := parseNamespaceVersion(b)
	if !aok {
		return b
	}
	if !bok {
		return a
	}
	if bmaj < amaj || (bmaj == amaj && bmin < amin) {
		return b
	}
	return a
}

func parseNamespaceVersion(s string) (major, minor int, ok bool) {
	s = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(s), "v"))
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[0], "%d", &major); err != nil {
		return 0, 0, false
	}
	if _, err := fmt.Sscanf(parts[1], "%d", &minor); err != nil {
		return 0, 0, false
	}
	return major, minor, true
}
