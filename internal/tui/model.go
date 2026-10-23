package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Miku0139oao/linode-agent/internal/api"
	"github.com/Miku0139oao/linode-agent/internal/ops"
	"github.com/Miku0139oao/linode-agent/internal/render"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type resource struct {
	name   string   // tab label
	key    string   // cache key
	fields []string // table columns
	detail []string // detail panel fields
	load   func(context.Context) ([]ops.Object, error)
}

type dataMsg struct {
	key  string
	rows []ops.Object
	err  error
}

// Model is the root bubbletea model.
type Model struct {
	svc     *ops.Service
	profile string
	theme   Theme
	tabs    []resource
	active  int

	tbl     table.Model
	rows    map[string][]ops.Object
	errs    map[string]error
	visible []ops.Object

	filter    textinput.Model
	filtering bool
	showHelp  bool
	detail    bool
	detailOff int
	loading   bool
	width     int
	height    int
	spin      spinner.Model
	status    string

	allowWrite bool
	confirm    *confirmState
	notice     string
}

// New builds the TUI model in read-only mode; svc may be nil in tests.
func New(svc *ops.Service, profile string) *Model {
	return NewWithWrite(svc, profile, false)
}

// NewWithWrite builds the model, optionally enabling mutating keys.
func NewWithWrite(svc *ops.Service, profile string, allowWrite bool) *Model {
	th := DefaultTheme()
	t := table.New(table.WithFocused(true), table.WithHeight(15))
	st := table.DefaultStyles()
	st.Header = st.Header.Bold(true).
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(th.Dim).
		BorderBottom(true)
	st.Selected = st.Selected.
		Foreground(lipgloss.AdaptiveColor{Light: "#FFFFFF", Dark: "#0B0B0F"}).
		Background(th.Accent).
		Bold(false)
	t.SetStyles(st)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = th.Spinner

	fi := textinput.New()
	fi.Placeholder = "輸入關鍵字…"
	fi.CharLimit = 64
	fi.Prompt = "/ "

	m := &Model{
		svc:        svc,
		profile:    profile,
		theme:      th,
		tbl:        t,
		rows:       map[string][]ops.Object{},
		errs:       map[string]error{},
		filter:     fi,
		spin:       sp,
		loading:    true,
		allowWrite: allowWrite,
		tabs:       resources(svc),
	}
	m.rebuild()
	return m
}

func resources(svc *ops.Service) []resource {
	list := func(f func(context.Context, ops.ListOpts) ([]ops.Object, api.Meta, error)) func(context.Context) ([]ops.Object, error) {
		return func(ctx context.Context) ([]ops.Object, error) {
			rows, _, err := f(ctx, ops.ListOpts{Limit: 200})
			return rows, err
		}
	}
	return []resource{
		{
			name:   "Linodes",
			key:    "linodes",
			fields: []string{"id", "label", "status", "type", "region", "ipv4.0"},
			detail: []string{"id", "label", "status", "type", "region", "image", "ipv4", "ipv6", "tags", "created", "updated"},
			load:   list(svc.ListLinodes),
		},
		{
			name:   "Volumes",
			key:    "volumes",
			fields: []string{"id", "label", "size", "region", "linode_id", "filesystem_path"},
			detail: []string{"id", "label", "size", "region", "linode_id", "filesystem_path", "status", "tags"},
			load:   list(svc.ListVolumes),
		},
		{
			name:   "Images",
			key:    "images",
			fields: []string{"id", "label", "vendor", "type", "size", "status"},
			detail: []string{"id", "label", "vendor", "type", "size", "status", "description", "created", "expiry"},
			load:   list(svc.ListImages),
		},
		{
			name:   "Events",
			key:    "events",
			fields: []string{"id", "created", "action", "status", "entity.label"},
			detail: []string{"id", "created", "action", "status", "entity.label", "entity.type", "entity.url", "seen", "duration", "username"},
			load:   list(svc.ListEvents),
		},
		{
			name:   "Doctor",
			key:    "doctor",
			fields: []string{"email", "company", "balance", "country", "phone"},
			detail: []string{"email", "company", "balance", "country", "phone", "active_promotions", "capabilities"},
			load: func(ctx context.Context) ([]ops.Object, error) {
				acct, _, err := svc.WhoAmI(ctx)
				if err != nil {
					return nil, err
				}
				return []ops.Object{acct}, nil
			},
		},
	}
}

// Init starts the spinner and the first load.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, m.loadCmd())
}

func (m *Model) current() resource { return m.tabs[m.active] }

func (m *Model) loadCmd() tea.Cmd {
	if m.svc == nil {
		return nil
	}
	res := m.current()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		rows, err := res.load(ctx)
		return dataMsg{key: res.key, rows: rows, err: err}
	}
}

// Update implements tea.Model.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.resize()
		return m, nil

	case dataMsg:
		if msg.key == m.current().key {
			m.loading = false
			if msg.err != nil {
				m.errs[msg.key] = msg.err
				m.status = "錯誤：" + msg.err.Error()
			} else {
				delete(m.errs, msg.key)
				m.rows[msg.key] = msg.rows
				m.status = fmt.Sprintf("已更新 · %d 筆 · %s", len(msg.rows), time.Now().Format("15:04:05"))
			}
			m.rebuild()
		}
		return m, nil

	case actionDoneMsg:
		if msg.err != nil {
			m.notice = "失敗：" + msg.err.Error()
			m.status = m.notice
			return m, nil
		}
		if msg.res != nil {
			m.notice = "✓ " + msg.res.Action + " " + msg.res.Target
			m.status = m.notice
		}
		m.loading = true
		return m, tea.Batch(m.spin.Tick, m.loadCmd())

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		if !m.loading {
			return m, nil
		}
		return m, cmd

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	if m.showHelp {
		switch key {
		case "?", "esc", "q":
			m.showHelp = false
		}
		return m, nil
	}
	if m.confirm != nil {
		return m.handleConfirmKey(msg)
	}
	if m.detail {
		switch key {
		case "esc", "enter", "q":
			m.detail = false
			m.detailOff = 0
		case "j", "down":
			m.detailOff++
		case "k", "up":
			if m.detailOff > 0 {
				m.detailOff--
			}
		}
		return m, nil
	}
	if m.filtering {
		switch key {
		case "esc":
			m.filtering = false
			m.filter.SetValue("")
			m.filter.Blur()
			m.rebuild()
			return m, nil
		case "enter":
			m.filtering = false
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.rebuild()
		return m, cmd
	}

	switch key {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "?":
		m.showHelp = true
		return m, nil
	case "/":
		m.filtering = true
		m.filter.Focus()
		return m, textinput.Blink
	case "r":
		m.loading = true
		m.status = "載入中…"
		return m, tea.Batch(m.spin.Tick, m.loadCmd())
	case "enter":
		if len(m.visible) > 0 {
			m.detail = true
			m.detailOff = 0
		}
		return m, nil
	case "tab":
		m.switchTo((m.active + 1) % len(m.tabs))
		return m, nil
	case "b":
		m.beginAction("boot")
		return m, nil
	case "s":
		m.beginAction("shutdown")
		return m, nil
	case "R":
		m.beginAction("reboot")
		return m, nil
	case "x":
		m.beginAction("delete")
		return m, nil
	case "shift+tab":
		m.switchTo((m.active - 1 + len(m.tabs)) % len(m.tabs))
		return m, nil
	case "1", "2", "3", "4", "5":
		idx := int(key[0] - '1')
		if idx < len(m.tabs) {
			m.switchTo(idx)
		}
		return m, nil
	}

	var cmd tea.Cmd
	m.tbl, cmd = m.tbl.Update(msg)
	return m, cmd
}

func (m *Model) switchTo(idx int) {
	m.active = idx
	m.detailOff = 0
	m.rebuild()
	// Load on demand if not cached yet.
	if _, ok := m.rows[m.current().key]; !ok {
		m.loading = true
		m.status = "載入中…"
	}
}

func (m *Model) resize() {
	h := m.height - 8
	if h < 3 {
		h = 3
	}
	m.tbl.SetHeight(h)
	m.resizeColumns()
}

func (m *Model) resizeColumns() {
	res := m.current()
	n := len(res.fields)
	if n == 0 || m.width <= 0 {
		return
	}
	usable := m.width - 4 // table padding
	per := usable / n
	if per < 8 {
		per = 8
	}
	cols := make([]table.Column, n)
	for i, f := range res.fields {
		title := render.ColumnTitle(f)
		cols[i] = table.Column{Title: title, Width: per}
	}
	m.tbl.SetColumns(cols)
}

// rebuild applies the filter to the cached rows and refreshes table contents.
func (m *Model) rebuild() {
	res := m.current()
	all := m.rows[res.key]
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))

	vis := make([]ops.Object, 0, len(all))
	for _, row := range all {
		if q == "" || rowMatches(row, res.fields, q) {
			vis = append(vis, row)
		}
	}
	m.visible = vis

	if len(m.tbl.Columns()) != len(res.fields) {
		m.resizeColumns()
	}
	rows := make([]table.Row, 0, len(vis))
	for _, obj := range vis {
		cells := make([]string, len(res.fields))
		for i, f := range res.fields {
			v := render.Field(obj, f)
			if f == "status" {
				if s, ok := v.(string); ok {
					cells[i] = m.theme.statusDot(s) + " " + s
					continue
				}
			}
			cells[i] = render.Cell(v)
		}
		rows = append(rows, table.Row(cells))
	}
	m.tbl.SetRows(rows)

	// Shrink-wrap the table so short lists don't leave a wall of blank rows.
	maxRows := m.height - 9
	if maxRows < 3 {
		maxRows = 3
	}
	h := len(rows) + 3 // header + separator + rows
	if h > maxRows {
		h = maxRows
	}
	if h < 6 {
		h = 6
	}
	m.tbl.SetHeight(h)
}

func rowMatches(obj ops.Object, fields []string, q string) bool {
	for _, f := range fields {
		if strings.Contains(strings.ToLower(render.Cell(render.Field(obj, f))), q) {
			return true
		}
	}
	return false
}

// View renders the whole screen.
func (m *Model) View() string {
	if m.width == 0 {
		return "初始化中…"
	}
	if m.showHelp {
		return m.helpView()
	}
	if m.confirm != nil {
		return m.confirmView()
	}

	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n")
	b.WriteString(m.tabsView())
	b.WriteString("\n")

	if m.detail {
		b.WriteString(m.detailView())
	} else {
		b.WriteString(m.tbl.View())
		if len(m.visible) == 0 {
			b.WriteString("\n")
			if m.loading {
				b.WriteString(m.theme.Status.Render("  " + m.spin.View() + " 載入中…"))
			} else if err, ok := m.errs[m.current().key]; ok {
				b.WriteString(m.theme.Help.Render("  " + err.Error()))
			} else {
				b.WriteString(m.theme.Status.Render("  （沒有資料）"))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	b.WriteString(m.footerView())
	return b.String()
}

func (m *Model) headerView() string {
	left := " Linode Agent "
	mid := fmt.Sprintf("profile: %s ", m.profile)
	right := ""
	if m.loading {
		right = m.spin.View() + " "
	}
	pad := m.width - lipgloss.Width(left) - lipgloss.Width(mid) - lipgloss.Width(right)
	if pad < 1 {
		pad = 1
	}
	return m.theme.Header.Render(left) + " " + m.theme.Status.Render(mid) +
		strings.Repeat(" ", pad) + right
}

func (m *Model) tabsView() string {
	parts := make([]string, 0, len(m.tabs))
	for i, t := range m.tabs {
		label := fmt.Sprintf("%d %s", i+1, t.name)
		if n := len(m.rows[t.key]); n > 0 {
			label += fmt.Sprintf(" (%d)", n)
		}
		if i == m.active {
			parts = append(parts, m.theme.TabActive.Render(label))
		} else {
			parts = append(parts, m.theme.TabIdle.Render(label))
		}
	}
	return strings.Join(parts, "")
}

func (m *Model) footerView() string {
	if m.filtering {
		return " " + m.filter.View() + m.theme.Footer.Render("   esc 取消 · enter 確認")
	}
	keys := " ?:說明  tab:切換  /:篩選  r:刷新  enter:詳情  q:退出"
	if m.allowWrite && m.current().key == "linodes" {
		keys = " ?:說明  tab:切換  /:篩選  r:刷新  enter:詳情  b開 s關 R重 x刪  q:退出"
	}
	status := m.status
	if m.notice != "" {
		status = m.notice
	}
	gap := m.width - lipgloss.Width(keys) - lipgloss.Width(status) - 2
	if gap < 1 {
		gap = 1
	}
	return m.theme.Footer.Render(" "+keys) + strings.Repeat(" ", gap) + m.theme.Status.Render(status)
}

func (m *Model) detailView() string {
	if len(m.visible) == 0 {
		return m.theme.Status.Render("  （沒有選取項目）")
	}
	idx := m.tbl.Cursor()
	if idx < 0 {
		idx = 0
	}
	if idx >= len(m.visible) {
		idx = len(m.visible) - 1
	}
	obj := m.visible[idx]
	res := m.current()

	lines := make([]string, 0, len(res.detail))
	for _, f := range res.detail {
		v := render.Field(obj, f)
		label := strings.ToUpper(strings.ReplaceAll(f, ".", " "))
		lines = append(lines, fmt.Sprintf("%-16s %s", label, render.Cell(v)))
	}

	maxLines := m.height - 9
	if maxLines < 1 {
		maxLines = 1
	}
	off := m.detailOff
	if off > len(lines)-1 {
		off = max(0, len(lines)-1)
	}
	end := off + maxLines
	if end > len(lines) {
		end = len(lines)
	}
	body := strings.Join(lines[off:end], "\n")

	title := fmt.Sprintf("%s 詳情 · esc 返回 · j/k 捲動", res.name)
	return m.theme.DetailBox.Width(max(20, m.width-4)).Render(title + "\n\n" + body)
}

func (m *Model) helpView() string {
	lines := []string{
		"鍵位",
		"",
		"  j / k        上下移動（或方向鍵）",
		"  g / G        跳到頭 / 尾",
		"  enter        開啟詳情，再按 esc 返回",
		"  tab / shift+tab  切換資源頁",
		"  1…5          直接跳到第 N 頁",
		"  /            篩選（esc 清除）",
		"  r            重新載入目前頁面",
		"  ?            這份說明",
		"  q / ctrl+c   離開",
		"",
		"資料來自 Linode API v4；輪詢只抓目前可見的資源。",
	}
	body := strings.Join(lines, "\n")
	box := m.theme.HelpBox.Width(max(40, min(m.width-6, 72))).Render(body)
	return lipgloss.Place(max(40, m.width), max(10, m.height), lipgloss.Center, lipgloss.Center, box)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
