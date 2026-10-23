package cli

import (
	"fmt"

	"github.com/mark3labs/mcp-go/server"
	"github.com/miku0139/linode-agent/internal/mcpserver"
	"github.com/spf13/cobra"
)

func newMCPCmd(o *Options) *cobra.Command {
	var httpAddr string
	var allowWrite bool

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "MCP server（給 agent 呼叫）",
		Long:  "把 Linode 操作暴露成 MCP 工具。預設 stdio（本機 agent），--http 給遠端 agent。",
	}

	serve := &cobra.Command{
		Use:   "serve",
		Short: "啟動 MCP server",
		Long: "預設走 stdio：stdout 只輸出 JSON-RPC，任何診斷訊息都走 stderr。\n" +
			"用 --http :8080 啟動 Streamable HTTP（端點 /mcp）。",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cfg, err := service(o)
			if err != nil {
				return err
			}
			s := mcpserver.New(svc, mcpserver.Options{Version: Version, AllowWrite: allowWrite})
			if httpAddr != "" {
				fmt.Fprintf(o.Stderr, "MCP Streamable HTTP 監聽 %s（profile %s，endpoint /mcp）\n", httpAddr, cfg.Profile)
				return server.NewStreamableHTTPServer(s).Start(httpAddr)
			}
			fmt.Fprintf(o.Stderr, "MCP stdio 啟動（profile %s，%d 個工具）\n", cfg.Profile, len(s.ListTools()))
			return server.ServeStdio(s)
		},
	}
	serve.Flags().StringVar(&httpAddr, "http", "", "以 Streamable HTTP 提供，例如 :8080（預設 stdio）")
	serve.Flags().BoolVar(&allowWrite, "allow-write", false, "開放寫入（Phase 2；目前只影響逃生口白名單）")

	cmd.AddCommand(serve)
	return cmd
}
