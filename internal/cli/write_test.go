package cli

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeMock(t *testing.T) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	mux := http.NewServeMux()
	record := func(w http.ResponseWriter, r *http.Request, method string) {
		atomic.AddInt32(&calls, 1)
		if r.Method != method {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		fmt.Fprint(w, `{}`)
	}
	mux.HandleFunc("/linode/instances/1001/reboot", func(w http.ResponseWriter, r *http.Request) {
		record(w, r, http.MethodPost)
	})
	mux.HandleFunc("/linode/instances/1001", func(w http.ResponseWriter, r *http.Request) {
		record(w, r, http.MethodDelete)
	})
	mux.HandleFunc("/account/events", func(w http.ResponseWriter, r *http.Request) {
		now := time.Now().UTC().Format("2006-01-02T15:04:05")
		atomic.AddInt32(&calls, 1)
		fmt.Fprintf(w, `{"data":[{"id":9001,"action":"linode_reboot","status":"finished","created":%q,"entity":{"id":1001}}],"page":1,"pages":1,"results":1}`, now)
	})
	return httptest.NewServer(mux), &calls
}

func TestWriteDefaultsToDryRun(t *testing.T) {
	srv, calls := writeMock(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out, errOut, code := run(t, "linodes", "reboot", "1001", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "DRY-RUN") || !strings.Contains(out, "linode_reboot") {
		t.Fatalf("stdout = %q", out)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("dry-run sent %d requests", *calls)
	}
}

func TestWriteAppliesWithYes(t *testing.T) {
	srv, calls := writeMock(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	out, errOut, code := run(t, "linodes", "reboot", "1001", "--yes", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if strings.Contains(out, "DRY-RUN") {
		t.Fatalf("expected applied output, got %q", out)
	}
	if !strings.Contains(out, "linode_reboot") {
		t.Fatalf("stdout = %q", out)
	}
	if atomic.LoadInt32(calls) != 1 {
		t.Fatalf("calls = %d, want 1", *calls)
	}

	// Audit line must exist and record the applied action.
	b, err := os.ReadFile(filepath.Join(stateDir, "linode-agent", "audit.jsonl"))
	if err != nil {
		t.Fatalf("audit log missing: %v", err)
	}
	if !strings.Contains(string(b), `"action":"linode_reboot"`) || !strings.Contains(string(b), `"dry_run":false`) {
		t.Fatalf("audit log = %s", b)
	}
}

func TestDeleteIsDryRunByDefault(t *testing.T) {
	srv, calls := writeMock(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out, _, code := run(t, "linodes", "delete", "1001", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if !strings.Contains(out, "DRY-RUN") || !strings.Contains(out, "linode_delete") {
		t.Fatalf("stdout = %q", out)
	}
	if atomic.LoadInt32(calls) != 0 {
		t.Fatalf("delete leaked through in dry-run")
	}
}

func TestWriteWithWaitPollsEvent(t *testing.T) {
	srv, _ := writeMock(t)
	defer srv.Close()
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	out, errOut, code := run(t, "linodes", "reboot", "1001", "--yes", "--wait", "--api-url", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, errOut)
	}
	if !strings.Contains(out, "linode_reboot") {
		t.Fatalf("stdout = %q", out)
	}
	if !strings.Contains(out, "finished") && !strings.Contains(out, "9001") {
		t.Fatalf("wait output missing event: %q", out)
	}
}

func TestWriteHelpListsSafetyFlags(t *testing.T) {
	t.Setenv("LINODE_TOKEN", "test-token")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _, code := run(t, "linodes", "reboot", "--help")
	if code != 0 {
		t.Fatalf("exit = %d", code)
	}
	for _, want := range []string{"--dry-run", "--yes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("help missing %s:\n%s", want, out)
		}
	}
}
