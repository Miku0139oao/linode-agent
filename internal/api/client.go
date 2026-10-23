package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public Linode API v4 root.
const DefaultBaseURL = "https://api.linode.com/v4"

// Meta carries pagination and rate-limit information for a response.
type Meta struct {
	Page          int
	Pages         int
	Results       int
	RateLimit     int
	RateRemaining int
	RequestID     string
}

// Client is a thin, retrying HTTP client for the Linode API.
// It is safe for concurrent use.
type Client struct {
	BaseURL    string
	Token      string
	UserAgent  string
	HTTPClient *http.Client

	// MaxRetries is the number of retries after the initial attempt.
	MaxRetries int
	// MinInterval throttles the gap between requests (0 disables).
	MinInterval time.Duration
	// Logf, when set, receives redacted debug lines.
	Logf func(format string, args ...any)

	mu       sync.Mutex
	lastCall time.Time
	rnd      *rand.Rand
}

// New returns a client with production defaults.
func New(token string) *Client {
	return &Client{
		BaseURL:     DefaultBaseURL,
		Token:       token,
		UserAgent:   "linode-agent/dev (+https://github.com/Miku0139oao/linode-agent)",
		HTTPClient:  &http.Client{Timeout: 30 * time.Second},
		MaxRetries:  3,
		MinInterval: 0,
		rnd:         rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

type envelope struct {
	Data    json.RawMessage `json:"data"`
	Page    int             `json:"page"`
	Pages   int             `json:"pages"`
	Results int             `json:"results"`
}

func (c *Client) baseURL() string {
	if c.BaseURL == "" {
		return DefaultBaseURL
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *Client) userAgent() string {
	if c.UserAgent == "" {
		return "linode-agent/dev"
	}
	return c.UserAgent
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient == nil {
		return &http.Client{Timeout: 30 * time.Second}
	}
	return c.HTTPClient
}

func (c *Client) logf(format string, args ...any) {
	if c.Logf != nil {
		c.Logf(format, args...)
	}
}

// throttle enforces MinInterval between calls.
func (c *Client) throttle(ctx context.Context) error {
	if c.MinInterval <= 0 {
		return nil
	}
	c.mu.Lock()
	wait := time.Duration(0)
	if !c.lastCall.IsZero() {
		if elapsed := time.Since(c.lastCall); elapsed < c.MinInterval {
			wait = c.MinInterval - elapsed
		}
	}
	c.lastCall = time.Now().Add(wait)
	c.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) backoff(attempt int, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	d := 400 * time.Millisecond << uint(attempt)
	if d > 15*time.Second {
		d = 15 * time.Second
	}
	jitter := time.Duration(0)
	if c.rnd != nil {
		jitter = time.Duration(c.rnd.Int63n(int64(250 * time.Millisecond)))
	}
	return d + jitter
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Send performs one logical request (with retries) and returns the raw
// envelope body plus metadata. body may be nil.
func (c *Client) Send(ctx context.Context, method, path string, query url.Values, filter map[string]any, body any) (json.RawMessage, Meta, error) {
	if path == "" {
		return nil, Meta{}, fmt.Errorf("api: empty path")
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	endpoint := c.baseURL() + path
	if len(query) > 0 {
		endpoint += "?" + query.Encode()
	}

	var payload []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, Meta{}, fmt.Errorf("api: encode body: %w", err)
		}
		payload = b
	}

	var filterHeader string
	if len(filter) > 0 {
		b, err := json.Marshal(filter)
		if err != nil {
			return nil, Meta{}, fmt.Errorf("api: encode filter: %w", err)
		}
		filterHeader = string(b)
	}

	attempts := c.MaxRetries + 1
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			var ra time.Duration
			if ae, ok := AsAPIError(lastErr); ok {
				ra = ae.RetryAfter
			}
			if err := sleepCtx(ctx, c.backoff(attempt-1, ra)); err != nil {
				return nil, Meta{}, err
			}
		}
		if err := c.throttle(ctx); err != nil {
			return nil, Meta{}, err
		}

		raw, meta, retryable, err := c.once(ctx, method, path, endpoint, filterHeader, payload)
		if err == nil {
			return raw, meta, nil
		}
		lastErr = err
		if !retryable || ctx.Err() != nil {
			return nil, Meta{}, err
		}
		c.logf("api: retrying %s %s after error: %v", method, path, err)
	}
	return nil, Meta{}, lastErr
}

func (c *Client) once(ctx context.Context, method, path, endpoint, filterHeader string, payload []byte) (json.RawMessage, Meta, bool, error) {
	var bodyReader io.Reader
	if payload != nil {
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bodyReader)
	if err != nil {
		return nil, Meta{}, false, fmt.Errorf("api: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent())
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if filterHeader != "" {
		req.Header.Set("X-Filter", filterHeader)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	// Never log the Authorization header: it carries the token.

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// Transport errors are retryable.
		return nil, Meta{}, true, &APIError{Method: method, Path: path, Cause: err, Reason: err.Error()}
	}
	defer resp.Body.Close()

	raw, readErr := readLimited(resp.Body)
	meta := Meta{
		RequestID:     firstNonEmpty(resp.Header.Get("X-Correlation-ID"), resp.Header.Get("X-Request-Id")),
		RateLimit:     atoiHeader(resp.Header, "X-RateLimit-Limit"),
		RateRemaining: atoiHeader(resp.Header, "X-RateLimit-Remaining"),
	}
	if readErr != nil {
		return nil, meta, true, &APIError{Method: method, Path: path, Status: resp.StatusCode, Cause: readErr, Reason: "read body: " + readErr.Error()}
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		ae := parseAPIError(method, path, resp, raw)
		retryable := ae.Status == http.StatusTooManyRequests || ae.Status >= 500
		return nil, meta, retryable, ae
	}

	if len(raw) == 0 {
		return nil, meta, false, nil
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err == nil {
		meta.Page, meta.Pages, meta.Results = env.Page, env.Pages, env.Results
		if env.Data != nil {
			return env.Data, meta, false, nil
		}
	}
	// Non-enveloped response: hand back the whole body.
	return raw, meta, false, nil
}

// Get fetches a single object into out.
func (c *Client) Get(ctx context.Context, path string, query url.Values, filter map[string]any, out any) (Meta, error) {
	data, meta, err := c.Send(ctx, http.MethodGet, path, query, filter, nil)
	if err != nil {
		return meta, err
	}
	if out == nil {
		return meta, nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return meta, fmt.Errorf("api: decode %s: %w", path, err)
	}
	return meta, nil
}

// clampPageSize keeps page_size inside the range the live API accepts.
// Verified against api.linode.com: page_size < 25 returns HTTP 400
// ("Must be 25-500"), and > 500 is rejected too.
func clampPageSize(n int) int {
	if n < 25 {
		return 25
	}
	if n > 500 {
		return 500
	}
	return n
}

// ListPage fetches one page and returns the raw items.
func (c *Client) ListPage(ctx context.Context, path string, query url.Values, filter map[string]any, page, pageSize int) ([]json.RawMessage, Meta, error) {
	if query == nil {
		query = url.Values{}
	}
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	if pageSize > 0 {
		query.Set("page_size", strconv.Itoa(clampPageSize(pageSize)))
	}
	data, meta, err := c.Send(ctx, http.MethodGet, path, query, filter, nil)
	if err != nil {
		return nil, meta, err
	}
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, meta, fmt.Errorf("api: decode list %s: %w", path, err)
	}
	return items, meta, nil
}

// ListAll walks pages until exhausted or maxItems is reached (0 = no cap).
func (c *Client) ListAll(ctx context.Context, path string, query url.Values, filter map[string]any, pageSize, maxItems int) ([]json.RawMessage, Meta, error) {
	if pageSize <= 0 || pageSize > 500 {
		pageSize = 100
	}
	pageSize = clampPageSize(pageSize)
	var all []json.RawMessage
	var last Meta
	for page := 1; ; page++ {
		items, meta, err := c.ListPage(ctx, path, cloneValues(query), filter, page, pageSize)
		if err != nil {
			return all, meta, err
		}
		last = meta
		all = append(all, items...)
		if maxItems > 0 && len(all) >= maxItems {
			all = all[:maxItems]
			break
		}
		if meta.Pages == 0 || page >= meta.Pages || len(items) == 0 {
			break
		}
	}
	return all, last, nil
}

func cloneValues(v url.Values) url.Values {
	out := make(url.Values, len(v))
	for k, vals := range v {
		cp := make([]string, len(vals))
		copy(cp, vals)
		out[k] = cp
	}
	return out
}

func atoiHeader(h http.Header, key string) int {
	v := h.Get(key)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}

// DecodeMap converts a raw JSON object into a generic map.
func DecodeMap(raw json.RawMessage) (map[string]any, error) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}
