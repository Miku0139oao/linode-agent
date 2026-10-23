package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// APIError is the single error type returned for any non-2xx response.
type APIError struct {
	Method     string
	Path       string
	Status     int
	Reason     string
	Field      string
	DocsURL    string
	RequestID  string
	RetryAfter time.Duration
	Cause      error
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: HTTP %d", e.Method, e.Path, e.Status)
	if e.Reason != "" {
		fmt.Fprintf(&b, ": %s", e.Reason)
	}
	if e.Field != "" {
		fmt.Fprintf(&b, " (field: %s)", e.Field)
	}
	if e.RequestID != "" {
		fmt.Fprintf(&b, " [request-id: %s]", e.RequestID)
	}
	return b.String()
}

func (e *APIError) Unwrap() error { return e.Cause }

// linodeErrorBody mirrors the documented error envelope.
type linodeErrorBody struct {
	Errors []struct {
		Reason string `json:"reason"`
		Field  string `json:"field"`
	} `json:"errors"`
}

func parseAPIError(method, path string, resp *http.Response, body []byte) *APIError {
	e := &APIError{
		Method:    method,
		Path:      path,
		Status:    resp.StatusCode,
		RequestID: firstNonEmpty(resp.Header.Get("X-Correlation-ID"), resp.Header.Get("X-Request-Id")),
	}
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := time.ParseDuration(ra + "s"); err == nil {
			e.RetryAfter = secs
		}
	}
	var parsed linodeErrorBody
	if err := json.Unmarshal(body, &parsed); err == nil && len(parsed.Errors) > 0 {
		e.Reason = parsed.Errors[0].Reason
		e.Field = parsed.Errors[0].Field
	} else {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 300 {
			snippet = snippet[:300] + "…"
		}
		e.Reason = snippet
	}
	return e
}

// IsNotFound reports whether err is a 404 from the API.
func IsNotFound(err error) bool { return statusIs(err, http.StatusNotFound) }

// IsAuth reports whether err is an authentication/authorisation failure.
func IsAuth(err error) bool {
	return statusIs(err, http.StatusUnauthorized) || statusIs(err, http.StatusForbidden)
}

// IsRateLimited reports whether err is a rate limit rejection.
func IsRateLimited(err error) bool { return statusIs(err, http.StatusTooManyRequests) }

func statusIs(err error, code int) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == code
	}
	return false
}

// AsAPIError unwraps err into *APIError when possible.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// readLimited reads at most 16 MiB of the response body.
func readLimited(r io.Reader) ([]byte, error) {
	const maxBody = 16 << 20
	return io.ReadAll(io.LimitReader(r, maxBody))
}
