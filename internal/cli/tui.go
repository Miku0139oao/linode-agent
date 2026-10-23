package cli

import (
	"fmt"
	"os"

	"github.com/Miku0139oao/linode-agent/internal/tui"
	"github.com/spf13/cobra"
)

// isTTY reports whether stdout is an interactive terminal.
func isTTY() bool {
	info, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

func newTUICmd(o *Options) *cobra.Command {
	var (
		snapshot   bool
		width      int
		height     int
		allowWrite bool
	)
	cmd := &cobra.Command{
		Use:   "tui",
		Short: "啟動終端介面（人用的儀表板）",
		Long: "以全螢幕 TUI 瀏覽 Linodes / Volumes / Images / Events，鍵盤操作，不需記指令。\n" +
			"加 --snapshot 可輸出一張純文字畫面（不需 TTY，適合文件或 CI）。",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cfg, err := service(o)
			if err != nil {
				return err
			}
			if snapshot {
				out, err := tui.Snapshot(svc, cfg.Profile, width, height)
				if err != nil {
					return err
				}
				fmt.Fprint(o.Stdout, out)
				return nil
			}
			if !isTTY() {
				return fmt.Errorf("tui 需要互動式終端；在腳本或管道中請用 CLI 子命令，或加 --snapshot")
			}
			return tui.Run(svc, cfg.Profile, allowWrite)
		},
	}
	cmd.Flags().BoolVar(&snapshot, "snapshot", false, "輸出一張純文字畫面後結束")
	cmd.Flags().IntVar(&width, "width", 100, "--snapshot 的畫面寬度")
	cmd.Flags().IntVar(&height, "height", 28, "--snapshot 的畫面高度")
	cmd.Flags().BoolVar(&allowWrite, "allow-write", false, "啟用 b/s/R/x 操作鍵（每次仍會跳確認）")
	return cmd
}
