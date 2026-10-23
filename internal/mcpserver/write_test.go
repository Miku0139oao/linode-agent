package mcpserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/miku0139/linode-agent/internal/api"
	"github.com/miku0139/linode-agent/internal/ops"
)

func toolNames(t *testing.T, s *server.MCPServer) map[string]bool {
	t.Helper()
	names := map[string]bool{}
	for n := range s.ListTools() {
		names[n] = true
	}
	return names
}

func callWrite(t *testing.T, s *server.MCPServer, tool string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	st := s.GetTool(tool)
	if st == nil {
		t.Fatalf("tool %q not registered", tool)
	}
	res, err := st.Handler(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: tool, Arguments: args},
	})
	if err != nil {
		t.Fatalf("%s handler error: %v", tool, err)
	}
	return res
}

func testService() *ops.Service {
	c := api.New("test-token")
	c.BaseURL = "http://127.0.0.1:1" // dry-run paths must never dial
	c.MaxRetries = 0
	return ops.New(c)
}

func TestWriteToolsHiddenByDefault(t *testing.T) {
	s := New(testService(), Options{Version: "test"})
	names := toolNames(t, s)
	if names["linodes_reboot"] || names["volumes_delete"] {
		t.Fatalf("write tools must not register without AllowWrite: %v", names)
	}
	if !names["linodes_list"] {
		t.Fatal("read tools missing")
	}
}

func TestWriteToolsRegisteredWithAllowWrite(t *testing.T) {
	s := New(testService(), Options{Version: "test", AllowWrite: true})
	for _, want := range []string{
		"linodes_reboot", "linodes_boot", "linodes_shutdown", "linodes_delete",
		"linodes_create", "linodes_resize", "linodes_update",
		"volumes_create", "volumes_attach", "volumes_detach", "volumes_resize", "volumes_delete",
		"images_capture",
	} {
		if !toolNames(t, s)[want] {
			t.Errorf("missing write tool %q", want)
		}
	}
}

func TestWriteRequiresConfirmWhenNotDryRun(t *testing.T) {
	s := New(testService(), Options{Version: "test", AllowWrite: true})
	res := callWrite(t, s, "linodes_reboot", map[string]any{"id": float64(1), "dry_run": false})
	if !res.IsError {
		t.Fatal("expected refusal without confirm=true")
	}
	if !strings.Contains(contentText(t, res), "confirm") {
		t.Fatalf("error must mention confirm: %s", contentText(t, res))
	}
}

func TestWriteDefaultsToPreview(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateDir)

	s := New(testService(), Options{Version: "test", AllowWrite: true})
	res := callWrite(t, s, "linodes_reboot", map[string]any{"id": float64(42)})
	if res.IsError {
		t.Fatalf("preview should succeed: %s", contentText(t, res))
	}
	body := contentText(t, res)
	if !strings.Contains(body, `"dry_run":true`) || !strings.Contains(body, "linode/42") {
		t.Fatalf("body = %s", body)
	}

	// Even a preview is audited.
	b, err := os.ReadFile(filepath.Join(stateDir, "linode-agent", "audit.jsonl"))
	if err != nil {
		t.Fatalf("audit log missing: %v", err)
	}
	if !strings.Contains(string(b), `"action":"linode_reboot"`) {
		t.Fatalf("audit = %s", b)
	}
}

func TestWriteRejectsMissingRequiredArgs(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := New(testService(), Options{Version: "test", AllowWrite: true})
	res := callWrite(t, s, "linodes_resize", map[string]any{"id": float64(1), "dry_run": true})
	if !res.IsError {
		t.Fatal("expected error for missing type")
	}
}

func TestCreateHidesRootPassInOutput(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s := New(testService(), Options{Version: "test", AllowWrite: true})
	res := callWrite(t, s, "linodes_create", map[string]any{
		"region": "jp-osa", "type": "g6-nanode-1", "root_pass": "hunter2-secret",
	})
	if res.IsError {
		t.Fatalf("preview failed: %s", contentText(t, res))
	}
	body := contentText(t, res)
	if strings.Contains(body, "hunter2-secret") {
		t.Fatalf("root password leaked: %s", body)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("bad json: %v", err)
	}
	if payload["dry_run"] != true {
		t.Fatalf("payload = %v", payload)
	}
}
