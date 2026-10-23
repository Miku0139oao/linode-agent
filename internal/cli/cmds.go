package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Miku0139oao/linode-agent/internal/config"
	"github.com/Miku0139oao/linode-agent/internal/ops"
	"github.com/Miku0139oao/linode-agent/internal/render"
	"github.com/spf13/cobra"
)

func newVersionCmd(o *Options) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "顯示版本",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(o.Stdout, "linode-agent %s\n", Version)
			return nil
		},
	}
}

// ---- auth ----

func newAuthCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "auth", Short: "憑證管理"}

	whoami := &cobra.Command{
		Use:   "whoami",
		Short: "驗證 token 並顯示帳號資訊",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, cfg, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			acct, meta, err := svc.WhoAmI(ctx)
			if err != nil {
				return err
			}
			if o.JSON {
				return render.JSON(o.Stdout, map[string]any{
					"profile":    cfg.Profile,
					"token_from": cfg.TokenFrom,
					"account":    acct,
					"meta":       meta,
				})
			}
			fmt.Fprintf(o.Stdout, "profile   : %s\n", cfg.Profile)
			fmt.Fprintf(o.Stdout, "token from: %s\n", cfg.TokenFrom)
			for _, f := range []string{"email", "company", "balance", "phone", "country"} {
				if v := render.Field(acct, f); v != nil {
					fmt.Fprintf(o.Stdout, "%-10s: %s\n", f, render.Cell(v))
				}
			}
			if meta.RequestID != "" {
				fmt.Fprintf(o.Stdout, "request-id: %s\n", meta.RequestID)
			}
			return nil
		},
	}

	var token string
	login := &cobra.Command{
		Use:   "login",
		Short: "儲存 API token 到 ~/.config/linode-agent/credentials (0600)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if token == "" {
				fmt.Fprint(o.Stderr, "貼上 Linode Personal Access Token 後按 Enter：")
				line, err := bufio.NewReader(os.Stdin).ReadString('\n')
				if err != nil && line == "" {
					return fmt.Errorf("讀取 token 失敗（管線輸入請以換行結尾）：%w", err)
				}
				token = strings.TrimSpace(line)
			}
			if token == "" {
				return fmt.Errorf("token 不可為空")
			}
			profile := o.Profile
			if profile == "" {
				profile = config.DefaultProfile
			}
			path := config.CredsPath()
			if err := config.SaveToken(path, profile, token); err != nil {
				return err
			}
			fmt.Fprintf(o.Stdout, "已儲存 profile %q 到 %s（權限 0600）\n", profile, path)
			return nil
		},
	}
	login.Flags().StringVar(&token, "token", "", "直接提供 token（不建議，會留在 shell history）")

	cmd.AddCommand(whoami, login)
	return cmd
}

// ---- linodes ----

func newLinodesCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "linodes", Short: "Linode 執行個體"}

	list := &cobra.Command{
		Use:   "list",
		Short: "列出執行個體",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListLinodes(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "label", "status", "type", "region", "ipv4"})
		},
	}

	get := &cobra.Command{
		Use:   "get <id>",
		Short: "取得單一執行個體",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseInt(args[0], "id")
			if err != nil {
				return err
			}
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			obj, _, err := svc.GetLinode(ctx, id)
			if err != nil {
				return err
			}
			return printObject(o, obj, []string{"id", "label", "status", "type", "region", "ipv4", "tags"})
		},
	}

	cmd.AddCommand(list, get)
	cmd.AddCommand(newLinodeWriteCmds(o)...)
	return cmd
}

// ---- generic read-only lists ----

func newVolumesCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "volumes", Short: "區塊儲存 Volume"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出 volumes",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListVolumes(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "label", "size", "region", "linode_id", "filesystem_path"})
		},
	})
	cmd.AddCommand(newVolumeWriteCmds(o)...)
	return cmd
}

func newImagesCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "images", Short: "磁碟映像"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出 images",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListImages(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "label", "vendor", "type", "size", "status"})
		},
	})
	cmd.AddCommand(newImageWriteCmds(o)...)
	return cmd
}

func newRegionsCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "regions", Short: "區域"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出 regions",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListRegions(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "label", "country", "status"})
		},
	})
	return cmd
}

func newEventsCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "events", Short: "帳號事件"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出 events",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListEvents(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"id", "created", "action", "status", "entity.label"})
		},
	})
	return cmd
}

func newTagsCmd(o *Options) *cobra.Command {
	cmd := &cobra.Command{Use: "tags", Short: "標籤"}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "列出 tags",
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			rows, meta, err := svc.ListTags(ctx, listOpts(o))
			if err != nil {
				return err
			}
			return printList(o, rows, meta, []string{"tag"})
		},
	})
	return cmd
}

// ---- escape hatch ----

func newAPICmd(o *Options) *cobra.Command {
	var queries []string
	var body string
	cmd := &cobra.Command{
		Use:   "api <METHOD> <PATH>",
		Short: "任意 API 呼叫（逃生口）",
		Example: "  linode-agent api GET /account/events --query page_size=5\n" +
			"  linode-agent api POST /linode/instances --body '{\"region\":\"us-east\"}'",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			method := strings.ToUpper(args[0])
			path := args[1]
			q, err := parseQueries(queries)
			if err != nil {
				return err
			}
			var payload any
			if strings.TrimSpace(body) != "" {
				if err := json.Unmarshal([]byte(body), &payload); err != nil {
					return fmt.Errorf("--body 不是合法 JSON：%w", err)
				}
			}
			svc, _, err := service(o)
			if err != nil {
				return err
			}
			ctx, cancel := ctxWithTimeout()
			defer cancel()
			out, meta, err := svc.Raw(ctx, method, path, q, payload)
			if err != nil {
				return err
			}
			if o.JSON {
				return render.JSON(o.Stdout, out)
			}
			// Tabled output for array responses, JSON otherwise.
			if arr, ok := out.([]any); ok {
				rows := make([]ops.Object, 0, len(arr))
				for _, item := range arr {
					if m, ok := item.(map[string]any); ok {
						rows = append(rows, m)
					}
				}
				fields := o.Fields
				if len(fields) == 0 {
					fields = keysOf(rows)
				}
				if len(rows) > 0 {
					return printList(o, rows, meta, fields)
				}
			}
			return render.JSON(o.Stdout, out)
		},
	}
	cmd.Flags().StringArrayVar(&queries, "query", nil, "query 參數 key=value，可重複")
	cmd.Flags().StringVar(&body, "body", "", "JSON request body")
	return cmd
}

func keysOf(rows []ops.Object) []string {
	if len(rows) == 0 {
		return nil
	}
	keys := make([]string, 0, len(rows[0]))
	for k := range rows[0] {
		keys = append(keys, k)
	}
	return keys
}
