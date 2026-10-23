package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/Miku0139oao/linode-agent/internal/api"
	"github.com/Miku0139oao/linode-agent/internal/config"
	"github.com/Miku0139oao/linode-agent/internal/ops"
	"github.com/Miku0139oao/linode-agent/internal/render"
	"github.com/spf13/cobra"
)

// Options are the global flags shared by every subcommand.
type Options struct {
	JSON     bool
	Fields   []string
	Profile  string
	APIURL   string
	Limit    int
	PageSize int
	NoColor  bool
	Verbose  bool

	Stdout io.Writer
	Stderr io.Writer
}

// Version is injected at build time via -ldflags.
var Version = "dev"

// NewRoot builds the command tree.
func NewRoot(o *Options) *cobra.Command {
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}

	root := &cobra.Command{
		Use:           "linode-agent",
		Short:         "Linode API 的 CLI / TUI / MCP 三合一工具",
		Long:          "linode-agent 用同一組 client 提供 CLI（腳本）、TUI（人）與 MCP（agent）三種介面。",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetOut(o.Stdout)
	root.SetErr(o.Stderr)

	pf := root.PersistentFlags()
	pf.BoolVar(&o.JSON, "json", false, "輸出完整 JSON（不經欄位投影）")
	pf.StringSliceVar(&o.Fields, "fields", nil, "只輸出指定欄位，逗號分隔，支援 a.b.0")
	pf.StringVar(&o.Profile, "profile", "", "憑證 profile（預設 LINODE_PROFILE 或 default）")
	pf.StringVar(&o.APIURL, "api-url", "", "覆寫 API base URL（測試用）")
	pf.IntVar(&o.Limit, "limit", 0, "最多回傳筆數，0 = 自動翻頁抓全部")
	pf.IntVar(&o.PageSize, "page-size", 0, "每頁筆數（預設 100，上限 500）")
	pf.BoolVar(&o.NoColor, "no-color", false, "關閉顏色輸出")
	pf.BoolVar(&o.Verbose, "verbose", false, "輸出除錯資訊")

	root.AddCommand(
		newAuthCmd(o),
		newLinodesCmd(o),
		newVolumesCmd(o),
		newImagesCmd(o),
		newRegionsCmd(o),
		newEventsCmd(o),
		newTagsCmd(o),
		newAPICmd(o),
		newNetworkCmd(o),
		newTUICmd(o),
		newMCPCmd(o),
		newVersionCmd(o),
	)

	// Bare invocation: launch the TUI on a terminal, print help otherwise.
	root.RunE = func(cmd *cobra.Command, args []string) error {
		if len(args) > 0 {
			return fmt.Errorf("未知指令：%s", strings.Join(args, " "))
		}
		if isTTY() {
			return newTUICmd(o).RunE(cmd, args)
		}
		return cmd.Help()
	}
	return root
}

// Execute runs the CLI and returns a process exit code.
func Execute(args []string, stdout, stderr io.Writer) int {
	o := &Options{Stdout: stdout, Stderr: stderr}
	root := NewRoot(o)
	root.SetArgs(args)
	root.SetOut(stdout)
	root.SetErr(stderr)
	err := root.Execute()
	if err == nil {
		return 0
	}

	var ae *api.APIError
	switch {
	case errors.As(err, &ae):
		switch {
		case ae.Status == 401 || ae.Status == 403:
			fmt.Fprintln(stderr, "認證失敗：", ae.Error())
			return 3
		case ae.Status == 429:
			fmt.Fprintln(stderr, "被速率限制：", ae.Error())
			return 4
		case ae.Status == 404:
			fmt.Fprintln(stderr, "找不到資源：", ae.Error())
			return 5
		}
	}
	fmt.Fprintln(stderr, "錯誤：", err)
	if errors.Is(err, config.ErrNoToken) {
		return 3
	}
	if strings.Contains(err.Error(), "unknown command") || strings.Contains(err.Error(), "unknown flag") {
		fmt.Fprintln(stderr, "（用 `linode-agent --help` 看清單）")
		return 2
	}
	return 1
}

// service resolves config and builds an ops.Service.
func service(o *Options) (*ops.Service, *config.Config, error) {
	cfg, err := config.Load(o.Profile)
	if err != nil {
		return nil, nil, err
	}
	client := api.New(cfg.Token)
	if o.APIURL != "" {
		client.BaseURL = o.APIURL
	} else if cfg.BaseURL != "" {
		client.BaseURL = cfg.BaseURL
	}
	if o.Verbose {
		client.Logf = func(format string, args ...any) {
			fmt.Fprintf(o.Stderr, "[debug] "+format+"\n", args...)
		}
	}
	return ops.New(client), cfg, nil
}

func listOpts(o *Options) ops.ListOpts {
	return ops.ListOpts{Limit: o.Limit, PageSize: o.PageSize}
}

// printList renders a list honoring --json / --fields.
func printList(o *Options, rows []ops.Object, meta api.Meta, defaultFields []string) error {
	fields := o.Fields
	if len(fields) == 0 {
		fields = defaultFields
	}
	if o.JSON {
		if err := render.JSON(o.Stdout, rows); err != nil {
			return err
		}
	} else {
		projected := render.Project(rows, fields)
		if err := render.Table(o.Stdout, projected, fields); err != nil {
			return err
		}
	}
	if !o.JSON {
		pageInfo := ""
		if meta.Pages > 0 {
			pageInfo = fmt.Sprintf(" · page %d/%d", max(meta.Page, 1), meta.Pages)
		}
		fmt.Fprintf(o.Stdout, "\n(%d 筆%s)\n", len(rows), pageInfo)
	}
	return nil
}

// printObject renders a single object.
func printObject(o *Options, obj ops.Object, defaultFields []string) error {
	if o.JSON || len(obj) == 0 {
		return render.JSON(o.Stdout, obj)
	}
	fields := o.Fields
	if len(fields) == 0 {
		fields = defaultFields
	}
	rows := []map[string]any{obj}
	return render.Table(o.Stdout, render.Project(rows, fields), fields)
}

func ctxWithTimeout() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 30e9)
}

func parseInt(s string, what string) (int, error) {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(s), "%d", &n); err != nil {
		return 0, fmt.Errorf("%s 必須是整數：%q", what, s)
	}
	return n, nil
}

func parseQueries(pairs []string) (url.Values, error) {
	q := url.Values{}
	for _, p := range pairs {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			return nil, fmt.Errorf("--query 格式應為 key=value：%q", p)
		}
		q.Set(k, v)
	}
	return q, nil
}
