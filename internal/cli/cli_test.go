package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockAPI serves the handful of endpoints the CLI integration test touches.
func mockAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/account", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"errors":[{"reason":"Invalid Token"}]}`)
			return
		}
		fmt.Fprint(w, `{"email":"ops@example.com","company":"Acme","balance":42.5,"phone":"","country":"TW"}`)
	})
	mux.HandleFunc("/linode/instances", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"data":[
			{"id":1001,"label":"web1","status":"running","type":"g6-nanode-1","region":"us-east","ipv4":["192.0.2.10"],"tags":["prod"]},
			{"id":1002,"label":"db1","status":"offline","type":"g6-standard-2","region":"eu-west","ipv4":["198.51.100.7"],"tags":[]}
		],"page":1,"pages":1,"results":2}`)
	})
	return httptest.NewServer(mux)
}

func run(t *testing.T, args ...string) (string, string, int) {
	t.Helper()
	var out, errBuf bytes.Buffer
	code := Execute(args, &out, &errBuf)
	return out.String(), errBuf.String(), code
}

func TestWhoAmITable(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, errOut, code := run(t, "auth", "whoami", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "ops@example.com") || !strings.Contains(out, "Acme") {
		t.Fatalf("stdout = %q", out)
	}
}

func TestWhoAmIJSON(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, errOut, code := run(t, "auth", "whoami", "--json", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	var decoded struct {
		Account map[string]any `json:"account"`
		Profile string         `json:"profile"`
	}
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if decoded.Account["email"] != "ops@example.com" {
		t.Fatalf("account = %+v", decoded.Account)
	}
	if decoded.Profile != "default" {
		t.Fatalf("profile = %q", decoded.Profile)
	}
}

func TestLinodesListTable(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, errOut, code := run(t, "linodes", "list", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	for _, want := range []string{"ID", "LABEL", "STATUS", "web1", "running", "192.0.2.10"} {
		if !strings.Contains(out, want) {
			t.Fatalf("stdout missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "(2 筆") {
		t.Fatalf("missing count footer:\n%s", out)
	}
}

func TestLinodesListFieldsProjection(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, errOut, code := run(t, "linodes", "list", "--fields", "id,label", "--json", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("json: %v", err)
	}
	if len(rows) != 2 || rows[0]["label"] != "web1" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestAuthFailureExitCode(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "wrong-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	_, _, code := run(t, "auth", "whoami", "--api-url", srv.URL)
	if code != 3 {
		t.Fatalf("exit = %d, want 3", code)
	}
}

func TestMissingTokenExitCode(t *testing.T) {
	t.Setenv("LINODE_TOKEN", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, errOut, code := run(t, "auth", "whoami")
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stderr %s)", code, errOut)
	}
}

func TestUnknownCommandExitCode(t *testing.T) {
	_, _, code := run(t, "nope")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestAPIEscapeHatch(t *testing.T) {
	srv := mockAPI(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	out, errOut, code := run(t, "api", "GET", "/linode/instances", "--json", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "web1") {
		t.Fatalf("stdout = %s", out)
	}
}
