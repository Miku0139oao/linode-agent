package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/miku0139/linode-agent/internal/audit"
	"github.com/miku0139/linode-agent/internal/ops"
	"github.com/miku0139/linode-agent/internal/render"
)

// confirmState is the pending mutating action awaiting a y/n answer.
type confirmState struct {
	Title  string
	Detail []string
	Run    func(ctx context.Context) (*ops.Result, error)
}

// actionDoneMsg reports the outcome of a mutating action.
type actionDoneMsg struct {
	res *ops.Result
	err error
}

// selected returns the row under the cursor of the current tab.
func (m *Model) selected() ops.Object {
	idx := m.tbl.Cursor()
	if idx < 0 {
		idx = 0 // the table cursor starts at -1 before any move
	}
	if idx >= len(m.visible) {
		return nil
	}
	return m.visible[idx]
}

func idOf(obj ops.Object) int {
	if v, ok := render.Field(obj, "id").(float64); ok {
		return int(v)
	}
	return 0
}

// beginAction opens the confirmation for a linode action. It is a no-op unless
// the TUI was started with --allow-write and the Linodes tab is active.
func (m *Model) beginAction(kind string) {
	if !m.allowWrite {
		m.notice = "唯讀模式：重啟時加 --allow-write 才能操作"
		return
	}
	if m.current().key != "linodes" {
		m.notice = "這個頁面沒有可用動作（請切到 Linodes）"
		return
	}
	obj := m.selected()
	if obj == nil {
		m.notice = "沒有選取項目"
		return
	}
	id := idOf(obj)
	label := render.Cell(render.Field(obj, "label"))
	if id == 0 {
		m.notice = "取不到機器 id"
		return
	}

	svc := m.svc
	var cs confirmState
	switch kind {
	case "boot":
		cs = confirmState{
			Title:  fmt.Sprintf("開機 %s (#%d)", label, id),
			Detail: []string{"POST /linode/instances/" + itoa(id) + "/boot"},
			Run:    func(ctx context.Context) (*ops.Result, error) { return svc.BootLinode(ctx, id, false) },
		}
	case "shutdown":
		cs = confirmState{
			Title:  fmt.Sprintf("關機 %s (#%d)", label, id),
			Detail: []string{"POST /linode/instances/" + itoa(id) + "/shutdown", "會送出 graceful shutdown，機器上的服務會中斷"},
			Run:    func(ctx context.Context) (*ops.Result, error) { return svc.ShutdownLinode(ctx, id, false) },
		}
	case "reboot":
		cs = confirmState{
			Title:  fmt.Sprintf("重新開機 %s (#%d)", label, id),
			Detail: []string{"POST /linode/instances/" + itoa(id) + "/reboot"},
			Run:    func(ctx context.Context) (*ops.Result, error) { return svc.RebootLinode(ctx, id, false) },
		}
	case "delete":
		cs = confirmState{
			Title:  fmt.Sprintf("刪除 %s (#%d)", label, id),
			Detail: []string{"DELETE /linode/instances/" + itoa(id), "不可逆：磁碟、備份與設定一併消失"},
			Run:    func(ctx context.Context) (*ops.Result, error) { return svc.DeleteLinode(ctx, id, false) },
		}
	default:
		m.notice = "未知動作：" + kind
		return
	}
	m.confirm = &cs
}

// handleConfirmKey resolves the confirmation modal.
func (m *Model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y", "enter":
		cs := m.confirm
		m.confirm = nil
		if cs == nil {
			return m, nil
		}
		m.notice = "執行中…"
		return m, m.runAction(cs)
	default: // n / esc / q / anything else cancels
		m.confirm = nil
		m.notice = "已取消"
		return m, nil
	}
}

func (m *Model) runAction(cs *confirmState) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		res, err := cs.Run(ctx)
		if res != nil {
			e := audit.Entry{Action: res.Action, Target: res.Target, DryRun: res.DryRun, OK: err == nil, Request: res.Request}
			if err != nil {
				e.Error = err.Error()
			}
			_ = audit.Write(e)
		}
		return actionDoneMsg{res: res, err: err}
	}
}

// confirmView renders the modal that asks for a yes/no.
func (m *Model) confirmView() string {
	cs := m.confirm
	if cs == nil {
		return ""
	}
	lines := []string{
		"確認執行",
		"",
		"  " + cs.Title,
		"",
	}
	for _, d := range cs.Detail {
		lines = append(lines, "  "+d)
	}
	lines = append(lines, "", "  y / enter 執行    n / esc 取消")
	box := m.theme.HelpBox.
		BorderForeground(lipgloss.Color("#B91C1C")).
		Width(max(44, min(m.width-6, 76))).
		Render(strings.Join(lines, "\n"))
	return lipgloss.Place(max(40, m.width), max(10, m.height), lipgloss.Center, lipgloss.Center, box)
}

func itoa(n int) string { return fmt.Sprintf("%d", n) }
