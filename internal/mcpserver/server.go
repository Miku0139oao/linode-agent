package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/miku0139/linode-agent/internal/api"
	"github.com/miku0139/linode-agent/internal/ops"
	"github.com/miku0139/linode-agent/internal/render"
)

// Options configures what the MCP server exposes.
type Options struct {
	Version    string
	AllowWrite bool // Phase 2: registers mutating tools
}

// New builds the MCP server with the read-only tool set (plus the escape hatch).
func New(svc *ops.Service, opts Options) *server.MCPServer {
	version := opts.Version
	if version == "" {
		version = "dev"
	}
	s := server.NewMCPServer("linode-agent", version,
		server.WithToolCapabilities(false),
		server.WithRecovery(),
	)
	registerReadTools(s, svc)
	registerEscapeHatch(s, svc, opts.AllowWrite)
	if opts.AllowWrite {
		registerWriteTools(s, svc)
	}
	return s
}

// ---- response contract ----

type metaOut struct {
	Page          int    `json:"page,omitempty"`
	Pages         int    `json:"pages,omitempty"`
	Results       int    `json:"results,omitempty"`
	RateLimit     int    `json:"rate_limit,omitempty"`
	RateRemaining int    `json:"rate_remaining,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
}

type envelope struct {
	OK    bool     `json:"ok"`
	Data  any      `json:"data,omitempty"`
	Meta  *metaOut `json:"meta,omitempty"`
	Error *errOut  `json:"error,omitempty"`
}

type errOut struct {
	Status     int    `json:"status,omitempty"`
	Reason     string `json:"reason"`
	Field      string `json:"field,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

func toMeta(m api.Meta) *metaOut {
	out := &metaOut{Page: m.Page, Pages: m.Pages, Results: m.Results,
		RateLimit: m.RateLimit, RateRemaining: m.RateRemaining, RequestID: m.RequestID}
	if *out == (metaOut{}) {
		return nil
	}
	return out
}

func okResult(data any, meta api.Meta) *mcp.CallToolResult {
	payload := envelope{OK: true, Data: sanitize(data), Meta: toMeta(meta)}
	b, err := json.Marshal(payload)
	if err != nil {
		return mcp.NewToolResultError("failed to encode result: " + err.Error())
	}
	return mcp.NewToolResultText(string(b))
}

func errResult(err error) *mcp.CallToolResult {
	e := &errOut{Reason: err.Error()}
	if ae, ok := api.AsAPIError(err); ok {
		e.Status = ae.Status
		e.Reason = ae.Reason
		e.Field = ae.Field
		e.RetryAfter = int(ae.RetryAfter.Seconds())
		switch {
		case ae.Status == 401 || ae.Status == 403:
			e.Hint = "token 無效或 scope 不足"
		case ae.Status == 404:
			e.Hint = "資源不存在或已被刪除"
		case ae.Status == 429:
			e.Hint = "被速率限制，稍後重試"
		}
	}
	b, _ := json.Marshal(envelope{OK: false, Error: e})
	return mcp.NewToolResultError(string(b))
}

// sanitize truncates long strings and redacts secret-looking keys.
func sanitize(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, item := range val {
			if isSecretKey(k) {
				out[k] = "***"
				continue
			}
			out[k] = sanitize(item)
		}
		return out
	case []map[string]any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitize(item)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = sanitize(item)
		}
		return out
	case string:
		if len(val) > 500 {
			return val[:500] + "…"
		}
		return val
	default:
		return val
	}
}

func isSecretKey(k string) bool {
	l := strings.ToLower(k)
	return strings.Contains(l, "token") || strings.Contains(l, "secret") ||
		strings.Contains(l, "password") || strings.HasSuffix(l, "_key")
}

// ---- shared tool plumbing ----

type listFunc func(context.Context, ops.ListOpts) ([]ops.Object, api.Meta, error)

func listOptions(req mcp.CallToolRequest) ops.ListOpts {
	pageSize := req.GetInt("page_size", 25)
	if pageSize <= 0 {
		pageSize = 25
	}
	if pageSize < 25 {
		pageSize = 25 // the API rejects page_size < 25
	}
	if pageSize > 500 {
		pageSize = 500
	}
	page := req.GetInt("page", 0)
	o := ops.ListOpts{Page: page, PageSize: pageSize}
	if page == 0 {
		// No explicit page: walk up to page_size items only (one page).
		o.Page = 1
	}
	return o
}

func project(rows []ops.Object, req mcp.CallToolRequest) []ops.Object {
	fields := splitFields(req.GetString("fields", ""))
	if len(fields) == 0 {
		return rows
	}
	return render.Project(rows, fields)
}

func splitFields(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

var commonListOptions = []mcp.ToolOption{
	mcp.WithNumber("page", mcp.Description("頁碼（從 1 開始，預設 1）")),
	mcp.WithNumber("page_size", mcp.Description("每頁筆數，預設 25，上限 500")),
	mcp.WithString("fields", mcp.Description("逗號分隔的欄位投影，例如 id,label,status")),
}

func addListTool(s *server.MCPServer, name, desc string, load listFunc, writeFilter bool) {
	opts := []mcp.ToolOption{mcp.WithDescription(desc)}
	opts = append(opts, commonListOptions...)
	if writeFilter {
		opts = append(opts,
			mcp.WithString("status", mcp.Description("以 status 精確過濾（例如 running）")),
			mcp.WithString("label_contains", mcp.Description("label 包含此字串（模糊比對）")),
		)
	}
	tool := mcp.NewTool(name, opts...)
	s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		o := listOptions(req)
		if writeFilter {
			filter := map[string]any{}
			if v := req.GetString("status", ""); v != "" {
				filter["status"] = v
			}
			if v := req.GetString("label_contains", ""); v != "" {
				filter["label"] = map[string]any{"+contains": v}
			}
			if len(filter) > 0 {
				o.Filter = filter
			}
		}
		rows, meta, err := load(ctx, o)
		if err != nil {
			return errResult(err), nil
		}
		return okResult(project(rows, req), meta), nil
	})
}

func registerReadTools(s *server.MCPServer, svc *ops.Service) {
	addListTool(s, "linodes_list", "列出 Linode 執行個體（預設 25 筆）。",
		svc.ListLinodes, true)
	addListTool(s, "volumes_list", "列出區塊儲存 volumes。", svc.ListVolumes, false)
	addListTool(s, "images_list", "列出磁碟 images。", svc.ListImages, false)
	addListTool(s, "regions_list", "列出可用 regions。", svc.ListRegions, false)
	addListTool(s, "events_list", "列出帳號事件（最近的在最前）。", svc.ListEvents, false)
	addListTool(s, "tags_list", "列出 tags。", svc.ListTags, false)

	// linodes_disks — needed before image capture
	s.AddTool(
		mcp.NewTool("linodes_disks",
			mcp.WithDescription("列出某台機器的磁碟（image capture 需要 disk_id）。"),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return errResult(fmt.Errorf("id 必填且必須是整數")), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			rows, meta, err := svc.ListDisks(ctx, id)
			if err != nil {
				return errResult(err), nil
			}
			return okResult(project(rows, req), meta), nil
		},
	)

	// linodes_get
	s.AddTool(
		mcp.NewTool("linodes_get",
			mcp.WithDescription("取得單一 Linode 的完整規格。"),
			mcp.WithNumber("id", mcp.Required(), mcp.Description("Linode ID")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			id, err := req.RequireInt("id")
			if err != nil {
				return errResult(fmt.Errorf("id 必填且必須是整數")), nil
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			obj, meta, err := svc.GetLinode(ctx, id)
			if err != nil {
				return errResult(err), nil
			}
			return okResult(obj, meta), nil
		},
	)

	// account_get
	s.AddTool(
		mcp.NewTool("account_get",
			mcp.WithDescription("取得目前 token 所屬帳號摘要（驗證 token 是否可用）。"),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			obj, meta, err := svc.WhoAmI(ctx)
			if err != nil {
				return errResult(err), nil
			}
			if f := splitFields(req.GetString("fields", "")); len(f) > 0 {
				obj = render.Project([]ops.Object{obj}, f)[0]
			}
			return okResult(obj, meta), nil
		},
	)
}

// ---- escape hatch ----

func registerEscapeHatch(s *server.MCPServer, svc *ops.Service, allowWrite bool) {
	allowed := "GET"
	if allowWrite {
		allowed = "GET, POST, PUT, DELETE"
	}
	s.AddTool(
		mcp.NewTool("linode_api",
			mcp.WithDescription("任意 Linode API v4 呼叫（逃生口）。未列在專屬工具的操作可用這個。"+
				"目前允許的 method："+allowed+"。"),
			mcp.WithString("method", mcp.Required(), mcp.Description("HTTP method")),
			mcp.WithString("path", mcp.Required(), mcp.Description("API 路徑，例如 /linode/instances")),
			mcp.WithString("query", mcp.Description("查詢字串，例如 page_size=5&page=2")),
			mcp.WithString("body", mcp.Description("JSON body（寫入操作用）")),
		),
		func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			method, err := req.RequireString("method")
			if err != nil {
				return errResult(fmt.Errorf("method 必填")), nil
			}
			path, err := req.RequireString("path")
			if err != nil {
				return errResult(fmt.Errorf("path 必填")), nil
			}
			method = strings.ToUpper(strings.TrimSpace(method))
			if !pathAllowed(method, path, allowWrite) {
				return errResult(fmt.Errorf("拒絕：%s %s 不在白名單內（allowWrite=%v）", method, path, allowWrite)), nil
			}
			q, err := url.ParseQuery(req.GetString("query", ""))
			if err != nil {
				return errResult(fmt.Errorf("query 格式錯誤：%w", err)), nil
			}
			var body any
			if raw := req.GetString("body", ""); strings.TrimSpace(raw) != "" {
				if err := json.Unmarshal([]byte(raw), &body); err != nil {
					return errResult(fmt.Errorf("body 不是合法 JSON：%w", err)), nil
				}
			}
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			out, meta, err := svc.Raw(ctx, method, path, q, body)
			if err != nil {
				return errResult(err), nil
			}
			return okResult(out, meta), nil
		},
	)
}

// pathAllowed enforces the read/write split and blocks account-level destruction.
func pathAllowed(method, path string, allowWrite bool) bool {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return false
	}
	blockedPrefixes := []string{"/account/oauth-clients"}
	for _, p := range blockedPrefixes {
		if strings.HasPrefix(path, p) {
			return false
		}
	}
	if strings.HasPrefix(path, "/account") && (method == "DELETE") {
		return false
	}
	switch method {
	case "GET":
		return true
	case "POST", "PUT", "DELETE":
		if !allowWrite {
			return false
		}
		return !strings.Contains(strings.ToUpper(path), "/ACCOUNT/TOKENS")
	default:
		return false
	}
}
