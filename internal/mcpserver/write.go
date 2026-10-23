package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Miku0139oao/linode-agent/internal/audit"
	"github.com/Miku0139oao/linode-agent/internal/ops"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// writeHandler performs the action; dry=true must not touch the API.
type writeHandler func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error)

type registrar struct {
	s   *server.MCPServer
	svc *ops.Service
}

// rawResult renders a mutating result without the list-shaped envelope.
func rawResult(v any) *mcp.CallToolResult {
	b, err := json.Marshal(sanitize(v))
	if err != nil {
		return mcp.NewToolResultError("failed to encode result: " + err.Error())
	}
	return mcp.NewToolResultText(string(b))
}

func (r registrar) add(name, desc string, opts []mcp.ToolOption, fn writeHandler) {
	all := append([]mcp.ToolOption{
		mcp.WithDescription(desc + "｜預設 dry_run=true；要真的變更必須同時帶 dry_run=false 與 confirm=true。"),
		mcp.WithBoolean("dry_run", mcp.Description("true = 只回傳將送出的請求，不執行（預設）")),
		mcp.WithBoolean("confirm", mcp.Description("dry_run=false 時必須為 true，代表使用者已確認")),
		mcp.WithBoolean("wait", mcp.Description("true = 等到對應事件完成才回傳（最多 180 秒）")),
	}, opts...)

	r.s.AddTool(mcp.NewTool(name, all...), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		dry := req.GetBool("dry_run", true)
		confirm := req.GetBool("confirm", false)
		wait := req.GetBool("wait", false)
		if !dry && !confirm {
			return errResult(fmt.Errorf("拒絕執行：要真的變更請同時帶 dry_run=false 與 confirm=true")), nil
		}

		timeout := 60 * time.Second
		if !dry && wait {
			timeout = 200 * time.Second
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		res, err := fn(cctx, req, dry)
		if res != nil {
			e := audit.Entry{
				Action: res.Action, Target: res.Target, DryRun: res.DryRun,
				OK: err == nil, Request: res.Request,
			}
			if err != nil {
				e.Error = err.Error()
			}
			_ = audit.Write(e)
		}
		if err != nil {
			return errResult(err), nil
		}

		payload := map[string]any{
			"ok":      true,
			"dry_run": res.DryRun,
			"action":  res.Action,
			"target":  res.Target,
		}
		if res.Request != nil {
			payload["request"] = res.Request
		}
		if res.Response != nil {
			payload["response"] = res.Response
		}
		if len(res.Warnings) > 0 {
			payload["warnings"] = res.Warnings
		}
		if res.DryRun {
			payload["hint"] = "這是預覽；要真的執行請重送並帶 dry_run=false 與 confirm=true"
		}
		if !res.DryRun && wait {
			if ev, werr := r.svc.WaitForResult(cctx, res, 180*time.Second); werr != nil {
				payload["wait_error"] = werr.Error()
			} else {
				payload["event"] = ev
			}
		}
		return rawResult(payload), nil
	})
}

// registerWriteTools installs the mutating tool set (only with --allow-write).
func registerWriteTools(s *server.MCPServer, svc *ops.Service) {
	r := registrar{s: s, svc: svc}

	// ---- linode lifecycle ----
	r.add("linodes_boot", "開機。",
		[]mcp.ToolOption{mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			return svc.BootLinode(ctx, id, dry)
		})

	r.add("linodes_shutdown", "關機（graceful shutdown）。",
		[]mcp.ToolOption{mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			return svc.ShutdownLinode(ctx, id, dry)
		})

	r.add("linodes_reboot", "重新開機。",
		[]mcp.ToolOption{mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			return svc.RebootLinode(ctx, id, dry)
		})

	r.add("linodes_update", "修改 label / tags / group。",
		[]mcp.ToolOption{
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID")),
			mcp.WithString("label", mcp.Description("新名稱")),
			mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("新 tags（覆蓋）")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			in := ops.UpdateLinodeInput{}
			if v := req.GetString("label", ""); v != "" {
				in.Label = &v
			}
			if tags := req.GetStringSlice("tags", nil); tags != nil {
				in.Tags = tags
			}
			if in.Label == nil && in.Tags == nil {
				return nil, fmt.Errorf("label 或 tags 至少給一個")
			}
			return svc.UpdateLinode(ctx, id, in, dry)
		})

	r.add("linodes_resize", "變更機器規格（需關機或自動關機）。",
		[]mcp.ToolOption{
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID")),
			mcp.WithString("type", mcp.Required(), mcp.Description("目標規格，例如 g6-standard-1")),
			mcp.WithBoolean("allow_auto_disk_resize", mcp.Description("允許自動擴充磁碟")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			typ, err := req.RequireString("type")
			if err != nil || typ == "" {
				return nil, fmt.Errorf("type 必填")
			}
			return svc.ResizeLinode(ctx, id, ops.ResizeLinodeInput{
				Type:                typ,
				AllowAutoDiskResize: req.GetBool("allow_auto_disk_resize", false),
			}, dry)
		})

	r.add("linodes_delete", "刪除機器（不可逆）。",
		[]mcp.ToolOption{mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return nil, fmt.Errorf("id 必填且必須是整數")
			}
			return svc.DeleteLinode(ctx, id, dry)
		})

	r.add("linodes_create", "建立新機器。",
		[]mcp.ToolOption{
			mcp.WithString("region", mcp.Required(), mcp.Description("region id，例如 jp-osa")),
			mcp.WithString("type", mcp.Required(), mcp.Description("規格，例如 g6-nanode-1")),
			mcp.WithString("image", mcp.Description("image id，例如 linode/ubuntu24.04")),
			mcp.WithString("label", mcp.Description("機器名稱")),
			mcp.WithString("root_pass", mcp.Description("root 密碼（不會被記錄）")),
			mcp.WithArray("tags", mcp.WithStringItems(), mcp.Description("tags")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			region, err := req.RequireString("region")
			if err != nil || region == "" {
				return nil, fmt.Errorf("region 必填")
			}
			typ, err := req.RequireString("type")
			if err != nil || typ == "" {
				return nil, fmt.Errorf("type 必填")
			}
			return svc.CreateLinode(ctx, ops.CreateLinodeInput{
				Region:   region,
				Type:     typ,
				Image:    req.GetString("image", ""),
				Label:    req.GetString("label", ""),
				RootPass: req.GetString("root_pass", ""),
				Tags:     req.GetStringSlice("tags", nil),
			}, dry)
		})

	// ---- volumes ----
	r.add("volumes_create", "建立 block storage volume。",
		[]mcp.ToolOption{
			mcp.WithString("label", mcp.Required(), mcp.Description("名稱")),
			mcp.WithNumber("size", mcp.Required(), mcp.Description("大小 GB")),
			mcp.WithString("region", mcp.Description("region（與 linode_id 二選一）")),
			mcp.WithNumber("linode_id", mcp.Description("直接掛到某台機器")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			label, _ := req.RequireString("label")
			size, _ := req.RequireInt("size")
			return svc.CreateVolume(ctx, ops.CreateVolumeInput{
				Label:    label,
				Size:     size,
				Region:   req.GetString("region", ""),
				LinodeID: req.GetInt("linode_id", 0),
			}, dry)
		})

	r.add("volumes_attach", "把 volume 掛到機器。",
		[]mcp.ToolOption{
			mcp.WithNumber("volume_id", mcp.Required(), mcp.Description("Volume ID")),
			mcp.WithNumber("linode_id", mcp.Required(), mcp.Description("目標 Linode ID")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			vid, err := req.RequireInt("volume_id")
			if err != nil {
				return nil, fmt.Errorf("volume_id 必填")
			}
			lid, err := req.RequireInt("linode_id")
			if err != nil {
				return nil, fmt.Errorf("linode_id 必填")
			}
			return svc.AttachVolume(ctx, vid, lid, dry)
		})

	r.add("volumes_detach", "卸載 volume。",
		[]mcp.ToolOption{mcp.WithNumber("volume_id", mcp.Required(), mcp.Description("Volume ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			vid, err := req.RequireInt("volume_id")
			if err != nil {
				return nil, fmt.Errorf("volume_id 必填")
			}
			return svc.DetachVolume(ctx, vid, dry)
		})

	r.add("volumes_resize", "擴充 volume（只能變大）。",
		[]mcp.ToolOption{
			mcp.WithNumber("volume_id", mcp.Required(), mcp.Description("Volume ID")),
			mcp.WithNumber("size", mcp.Required(), mcp.Description("新大小 GB")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			vid, _ := req.RequireInt("volume_id")
			size, _ := req.RequireInt("size")
			return svc.ResizeVolume(ctx, vid, size, dry)
		})

	r.add("volumes_delete", "刪除 volume（不可逆）。",
		[]mcp.ToolOption{mcp.WithNumber("volume_id", mcp.Required(), mcp.Description("Volume ID"))},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			vid, err := req.RequireInt("volume_id")
			if err != nil {
				return nil, fmt.Errorf("volume_id 必填")
			}
			return svc.DeleteVolume(ctx, vid, dry)
		})

	// ---- images ----
	r.add("images_capture", "從磁碟建立 image（disk_id 可用 linodes_disks 查）。",
		[]mcp.ToolOption{
			mcp.WithNumber("disk_id", mcp.Required(), mcp.Description("來源磁碟 ID")),
			mcp.WithString("label", mcp.Required(), mcp.Description("image 名稱")),
			mcp.WithString("description", mcp.Description("描述")),
		},
		func(ctx context.Context, req mcp.CallToolRequest, dry bool) (*ops.Result, error) {
			diskID, err := req.RequireInt("disk_id")
			if err != nil {
				return nil, fmt.Errorf("disk_id 必填")
			}
			label, err := req.RequireString("label")
			if err != nil || label == "" {
				return nil, fmt.Errorf("label 必填")
			}
			return svc.CaptureImage(ctx, ops.CaptureImageInput{
				DiskID:      diskID,
				Label:       label,
				Description: req.GetString("description", ""),
			}, dry)
		})
}
