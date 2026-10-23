package tui

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/miku0139/linode-agent/internal/ops"
)

func fixture() *Model {
	m := New(nil, "test")
	m.width, m.height = 100, 30
	m.loading = false
	m.rows["linodes"] = []ops.Object{
		{"id": float64(1001), "label": "web1", "status": "running", "type": "g6-nanode-1", "region": "us-east", "ipv4": []any{"192.0.2.10"}},
		{"id": float64(1002), "label": "db1", "status": "offline", "type": "g6-standard-2", "region": "eu-west", "ipv4": []any{"198.51.100.7"}},
	}
	m.rebuild()
	return m
}

func press(m *Model, key string) {
	var msg tea.KeyMsg
	if len(key) == 1 {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	} else {
		msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
	}
	_, _ = m.Update(msg)
}

func TestViewRendersRowsAndChrome(t *testing.T) {
	m := fixture()
	v := m.View()
	for _, want := range []string{"Linodes", "web1", "db1", "running", "192.0.2.10", "profile: test", "?:說明"} {
		if !strings.Contains(v, want) {
			t.Fatalf("View missing %q:\n%s", want, v)
		}
	}
}

func TestTabSwitchByNumber(t *testing.T) {
	m := fixture()
	press(m, "2")
	if m.active != 1 {
		t.Fatalf("active = %d, want 1", m.active)
	}
	if !strings.Contains(m.View(), "Volumes") {
		t.Fatalf("echo: %s", m.View())
	}
}

func TestHelpOverlay(t *testing.T) {
	m := fixture()
	press(m, "?")
	if !m.showHelp {
		t.Fatal("help not shown")
	}
	if !strings.Contains(m.View(), "鍵位") {
		t.Fatalf("help body missing")
	}
	press(m, "esc")
	if m.showHelp {
		t.Fatal("help not dismissed")
	}
}

func TestFilterNarrowsRows(t *testing.T) {
	m := fixture()
	m.filter.SetValue("db1")
	m.rebuild()
	if len(m.visible) != 1 {
		t.Fatalf("visible = %d, want 1", len(m.visible))
	}
	v := m.View()
	if strings.Contains(v, "web1") {
		t.Fatalf("filter did not exclude web1:\n%s", v)
	}
	m.filter.SetValue("")
	m.rebuild()
	if len(m.visible) != 2 {
		t.Fatalf("visible = %d, want 2 after clearing", len(m.visible))
	}
}

func TestDetailPanel(t *testing.T) {
	m := fixture()
	press(m, "enter")
	if !m.detail {
		t.Fatal("detail not opened")
	}
	v := m.View()
	if !strings.Contains(v, "詳情") || !strings.Contains(v, "LABEL") {
		t.Fatalf("detail view = %s", v)
	}
	press(m, "esc")
	if m.detail {
		t.Fatal("detail not closed")
	}
}

func TestStatusGlyphsAreNotColourOnly(t *testing.T) {
	m := fixture()
	v := m.View()
	// Symbol + text: the state word must be present even without colour.
	if !strings.Contains(v, "running") || !strings.Contains(v, "offline") {
		t.Fatalf("state words missing:\n%s", v)
	}
}

func TestDataMsgUpdatesCache(t *testing.T) {
	m := fixture()
	m.loading = true
	rows := []ops.Object{{"id": float64(9), "label": "new1", "status": "running"}}
	_, _ = m.Update(dataMsg{key: "linodes", rows: rows})
	if len(m.rows["linodes"]) != 1 {
		t.Fatalf("rows = %d", len(m.rows["linodes"]))
	}
	if !strings.Contains(m.status, "已更新") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestDataMsgErrorSurfacesInStatus(t *testing.T) {
	m := fixture()
	m.loading = true
	_, _ = m.Update(dataMsg{key: "linodes", err: errFake{}})
	if !strings.Contains(m.status, "錯誤") {
		t.Fatalf("status = %q", m.status)
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }

func fixtureWrite() *Model {
	m := NewWithWrite(nil, "test", true)
	m.width, m.height = 100, 30
	m.loading = false
	m.rows["linodes"] = []ops.Object{
		{"id": float64(1001), "label": "web1", "status": "running", "type": "g6-nanode-1", "region": "us-east"},
	}
	m.rebuild()
	return m
}

func TestWriteKeysDisabledInReadOnly(t *testing.T) {
	m := fixture()
	press(m, "R")
	if m.confirm != nil {
		t.Fatal("confirm modal opened in read-only mode")
	}
	if !strings.Contains(m.notice, "唯讀") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestConfirmModalOpensAndCancels(t *testing.T) {
	m := fixtureWrite()
	press(m, "R")
	if m.confirm == nil {
		t.Fatal("confirm modal did not open")
	}
	v := m.View()
	if !strings.Contains(v, "確認執行") || !strings.Contains(v, "web1") {
		t.Fatalf("confirm view = %s", v)
	}
	press(m, "n")
	if m.confirm != nil {
		t.Fatal("confirm modal did not close")
	}
	if !strings.Contains(m.notice, "已取消") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestConfirmModalForDeleteWarnsIrreversible(t *testing.T) {
	m := fixtureWrite()
	press(m, "x")
	if m.confirm == nil {
		t.Fatal("delete confirm did not open")
	}
	if v := m.View(); !strings.Contains(v, "不可逆") {
		t.Fatalf("delete confirm should warn: %s", v)
	}
	press(m, "esc")
	if m.confirm != nil {
		t.Fatal("esc did not cancel")
	}
}

func TestWriteKeysIgnoredOnNonLinodeTabs(t *testing.T) {
	m := fixtureWrite()
	press(m, "2") // Volumes
	press(m, "R")
	if m.confirm != nil {
		t.Fatal("confirm opened on a non-linode tab")
	}
	if !strings.Contains(m.notice, "Linodes") {
		t.Fatalf("notice = %q", m.notice)
	}
}

// TestPreviewSnapshot writes the rendered screen to $TUI_PREVIEW with ANSI codes
// stripped, for eyeballing layout without a terminal.
func TestPreviewSnapshot(t *testing.T) {
	path := os.Getenv("TUI_PREVIEW")
	if path == "" {
		t.Skip("set TUI_PREVIEW=/path/to/out.txt to dump the rendered view")
	}
	m := fixture()
	m.rows["events"] = []ops.Object{
		{"id": float64(9001), "created": "2026-10-23T12:00:00", "action": "linode_boot", "status": "finished", "entity": map[string]any{"label": "web1"}},
	}
	m.status = "已更新 · 2 筆 · 12:34:56"
	plain := stripANSI(m.View())
	if err := os.WriteFile(path, []byte(plain), 0o644); err != nil {
		t.Fatalf("write preview: %v", err)
	}
}
