package tui

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/miku0139/linode-agent/internal/ops"
)

var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

// stripANSI removes terminal escape sequences.
func stripANSI(s string) string { return ansiPattern.ReplaceAllString(s, "") }

// Snapshot renders a single frame with live data and returns it as plain text.
// It is the non-interactive equivalent of opening the TUI (used for docs, CI
// screenshots and quick sanity checks in a pipe).
func Snapshot(svc *ops.Service, profile string, width, height int) (string, error) {
	if width <= 0 {
		width = 100
	}
	if height <= 0 {
		height = 28
	}
	m := New(svc, profile)
	m.width, m.height = width, height
	m.loading = false

	if svc != nil {
		for _, res := range m.tabs {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			rows, err := res.load(ctx)
			cancel()
			if err != nil {
				m.errs[res.key] = err
				continue
			}
			m.rows[res.key] = rows
		}
	}
	if len(m.errs) > 0 {
		m.status = fmt.Sprintf("%d 個資源載入失敗（仍顯示其餘資料）", len(m.errs))
	} else {
		m.status = fmt.Sprintf("snapshot · %d 筆 · %s", len(m.rows[m.current().key]), time.Now().Format("15:04:05"))
	}
	m.rebuild()
	return stripANSI(m.View()), nil
}
