package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/miku0139/linode-agent/internal/api"
)

func TestToolsListed(t *testing.T) {
	s := New(nil, Options{Version: "test"})
	resp := s.HandleMessage(context.Background(),
		json.RawMessage(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	if resp == nil {
		t.Fatal("nil response")
	}
	b, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{
		"linodes_list", "linodes_get", "volumes_list", "images_list",
		"regions_list", "events_list", "tags_list", "account_get", "linode_api",
	} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("tools/list missing %q:\n%s", want, b)
		}
	}
}

func TestPathAllowedReadOnly(t *testing.T) {
	cases := []struct {
		method, path string
		allowWrite   bool
		want         bool
	}{
		{"GET", "/linode/instances", false, true},
		{"POST", "/linode/instances", false, false},
		{"POST", "/linode/instances", true, true},
		{"DELETE", "/account", true, false},
		{"DELETE", "/linode/instances/1", true, true},
		{"GET", "/account/oauth-clients", true, false},
		{"GET", "linode/instances", false, false},
		{"GET", "/linode/../account", false, false},
		{"PATCH", "/linode/instances/1", true, false},
	}
	for _, c := range cases {
		if got := pathAllowed(c.method, c.path, c.allowWrite); got != c.want {
			t.Errorf("pathAllowed(%q, %q, %v) = %v, want %v", c.method, c.path, c.allowWrite, got, c.want)
		}
	}
}

func TestSanitizeRedactsAndTruncates(t *testing.T) {
	long := strings.Repeat("x", 600)
	in := map[string]any{
		"access_token": "secret-value",
		"api_key":      "k",
		"password":     "p",
		"label":        long,
		"nested":       map[string]any{"client_secret": "s"},
	}
	out, ok := sanitize(in).(map[string]any)
	if !ok {
		t.Fatalf("sanitize returned %T", sanitize(in))
	}
	for _, k := range []string{"access_token", "api_key", "password"} {
		if out[k] != "***" {
			t.Errorf("%s = %v, want ***", k, out[k])
		}
	}
	nested, _ := out["nested"].(map[string]any)
	if nested["client_secret"] != "***" {
		t.Errorf("nested secret = %v", nested["client_secret"])
	}
	label, _ := out["label"].(string)
	if len(label) > 510 || !strings.HasSuffix(label, "…") {
		t.Errorf("label not truncated: len=%d", len(label))
	}
}

func TestListOptionsDefaults(t *testing.T) {
	o := listOptions(callRequest(map[string]any{}))
	if o.PageSize != 25 || o.Page != 1 {
		t.Fatalf("opts = %+v", o)
	}
	o = listOptions(callRequest(map[string]any{"page_size": float64(999), "page": float64(2)}))
	if o.PageSize != 500 || o.Page != 2 {
		t.Fatalf("opts = %+v (page_size must clamp to 500)", o)
	}
	o = listOptions(callRequest(map[string]any{"page_size": float64(5)}))
	if o.PageSize != 25 {
		t.Fatalf("opts = %+v (page_size must clamp up to 25)", o)
	}
}

func TestErrResultShape(t *testing.T) {
	res := errResult(&testErr{"boom"})
	if !res.IsError {
		t.Fatal("expected IsError")
	}
	body := contentText(t, res)
	if !strings.Contains(body, `"ok":false`) || !strings.Contains(body, "boom") {
		t.Fatalf("body = %s", body)
	}
}

func TestOkResultShape(t *testing.T) {
	res := okResult([]map[string]any{{"id": float64(1), "label": "web1", "secret": "x"}}, api.Meta{Page: 1, Pages: 1, Results: 1})
	if res.IsError {
		t.Fatal("unexpected error result")
	}
	body := contentText(t, res)
	if !strings.Contains(body, `"ok":true`) || !strings.Contains(body, "web1") {
		t.Fatalf("body = %s", body)
	}
	if strings.Contains(body, `"x"`) {
		t.Fatalf("secret leaked: %s", body)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }

func callRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Name: "test", Arguments: args}}
}

func contentText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
