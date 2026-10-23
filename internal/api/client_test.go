package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("test-token")
	c.BaseURL = srv.URL
	c.HTTPClient = srv.Client()
	c.MaxRetries = 2
	return c
}

func TestGetObject(t *testing.T) {
	var gotAuth, gotUA string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		if r.URL.Path != "/account" {
			t.Errorf("path = %q, want /account", r.URL.Path)
		}
		fmt.Fprint(w, `{"email":"me@example.com","company":"","balance":12.5,"active_promotions":[]}`)
	}))
	var acct struct {
		Email   string  `json:"email"`
		Balance float64 `json:"balance"`
	}
	if _, err := c.Get(context.Background(), "/account", nil, nil, &acct); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acct.Email != "me@example.com" || acct.Balance != 12.5 {
		t.Fatalf("decoded = %+v", acct)
	}
	if gotAuth != "Bearer test-token" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotUA == "" {
		t.Error("user agent empty")
	}
}

func TestListPagination(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch page {
		case "1":
			fmt.Fprint(w, `{"data":[{"id":1},{"id":2}],"page":1,"pages":2,"results":3}`)
		case "2":
			fmt.Fprint(w, `{"data":[{"id":3}],"page":2,"pages":2,"results":3}`)
		default:
			t.Errorf("unexpected page %q", page)
		}
	}))
	items, meta, err := c.ListAll(context.Background(), "/linode/instances", nil, nil, 2, 0)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %d, want 3", len(items))
	}
	if meta.Results != 3 || meta.Page != 2 {
		t.Fatalf("meta = %+v", meta)
	}
}

func TestListAllHonoursMaxItems(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":1},{"id":2},{"id":3}],"page":1,"pages":5,"results":15}`)
	}))
	items, _, err := c.ListAll(context.Background(), "/linode/instances", nil, nil, 3, 2)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
}

func TestFilterHeader(t *testing.T) {
	var raw string
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw = r.Header.Get("X-Filter")
		fmt.Fprint(w, `{"data":[],"page":1,"pages":1,"results":0}`)
	}))
	filter := map[string]any{"status": "running", "label": map[string]any{"+contains": "web"}}
	if _, _, err := c.ListPage(context.Background(), "/linode/instances", nil, filter, 1, 25); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatalf("filter header not valid json: %q (%v)", raw, err)
	}
	if decoded["status"] != "running" {
		t.Fatalf("filter = %v", decoded)
	}
	q, _ := json.Marshal(decoded["label"])
	if string(q) != `{"+contains":"web"}` {
		t.Fatalf("nested filter = %s", q)
	}
}

func TestRetryOn429(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			fmt.Fprint(w, `{"errors":[{"reason":"Too many requests"}]}`)
			return
		}
		fmt.Fprint(w, `{"data":[],"page":1,"pages":1,"results":0}`)
	}))
	start := time.Now()
	if _, _, err := c.ListPage(context.Background(), "/linode/instances", nil, nil, 1, 10); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if atomic.LoadInt32(&calls) != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("retry took too long: %v", elapsed)
	}
}

func TestNoRetryOn404(t *testing.T) {
	var calls int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"errors":[{"reason":"Not found","field":"id"}]}`)
	}))
	_, err := c.Get(context.Background(), "/linode/instances/999", nil, nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !IsNotFound(err) {
		t.Fatalf("IsNotFound(%v) = false", err)
	}
	ae, ok := AsAPIError(err)
	if !ok || ae.Status != 404 || ae.Reason != "Not found" || ae.Field != "id" {
		t.Fatalf("api error = %+v (ok=%v)", ae, ok)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 404)", calls)
	}
}

func TestIsAuth(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"errors":[{"reason":"Invalid Token"}]}`)
	}))
	_, _, err := c.Send(context.Background(), http.MethodGet, "/account", nil, nil, nil)
	if !IsAuth(err) {
		t.Fatalf("IsAuth(%v) = false", err)
	}
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("not an APIError: %v", err)
	}
}

func TestPageSizeClampedToAPIWindow(t *testing.T) {
	var q url.Values
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		fmt.Fprint(w, `{"data":[],"page":1,"pages":1,"results":0}`)
	}))
	// The live API rejects page_size < 25 with HTTP 400.
	if _, _, err := c.ListPage(context.Background(), "/linode/instances", nil, nil, 1, 2); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if got := q.Get("page_size"); got != "25" {
		t.Fatalf("page_size = %q, want 25", got)
	}
	if _, _, err := c.ListPage(context.Background(), "/linode/instances", nil, nil, 1, 900); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if got := q.Get("page_size"); got != "500" {
		t.Fatalf("page_size = %q, want 500", got)
	}
}

func TestQueryEncoding(t *testing.T) {
	var q url.Values
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		fmt.Fprint(w, `{"data":[],"page":1,"pages":1,"results":0}`)
	}))
	query := url.Values{"page_size": {"25"}}
	if _, _, err := c.ListPage(context.Background(), "/linode/instances", query, nil, 2, 10); err != nil {
		t.Fatalf("ListPage: %v", err)
	}
	if q.Get("page") != "2" || q.Get("page_size") != "25" {
		t.Fatalf("query = %v", q)
	}
}
