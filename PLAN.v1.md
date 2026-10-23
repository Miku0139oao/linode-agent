# Linode CLI + MCP（Go）實作計劃 v1

目標：一支 Go 二進位檔，同時提供「人類用的 CLI」與「agent 用的 MCP server」，
讓 agent 能以受控、可審計、低 token 成本的方式操作 Linode API v4。

---

## 0. 目標與非目標

### 目標
- 單一 binary（`linode-agent`），`go build` 即可跨平台發佈，無 runtime 依賴
- CLI：覆蓋 Linode API v4 常用資源，`--json` 全量可腳本化
- MCP：stdio 傳輸為主，工具集小而穩（不是把 300+ endpoint 全灌進去）
- 安全：讀寫分離、破壞性操作需顯式確認、token 永不外洩到輸出或日誌
- agent 友善：回應預設精簡（欄位投影 + 截斷 + 游標），避免一次回 500KB JSON

### 非目標（v1 不做）
- 不做 GUI / TUI
- 不重寫官方 `linode-cli`（Python）的所有行為；相容常用旗標即可
- 不做多帳號同時連線（先支援多 profile，但一次只連一個）
- 不實作 OAuth 流程（v1 只支援 Personal Access Token）

---

## 1. 架構

```
┌─────────────┐        ┌──────────────┐
│  cobra CLI  │───────▶│ internal/api │──▶ https://api.linode.com/v4
└─────────────┘        │  (client)    │
┌─────────────┐        │ - paginate   │
│ MCP server  │───────▶│ - filter     │
│ (stdio)     │        │ - retry/429  │
└─────────────┘        │ - err map    │
        ▲              └──────────────┘
        │
   agent (hermes / claude / codex)
```

關鍵原則：**CLI 與 MCP 共用同一個 `internal/api` 與同一組 `internal/ops`（業務動作）**，
只有表現在外層不同。避免「CLI 能做的事 MCP 不能做」的分裂。

---

## 2. 套件結構

```
cmd/linode-agent/main.go        # 進入點，子命令分派
internal/config/                # profile / token 解析（env > config file > keyring）
internal/api/                   # HTTP client：pagination、X-Filter、retry、error mapping
internal/api/types.go            # 手寫核心型別（Linode、Volume、Domain…），其餘用 map
internal/ops/                   # 高階動作（list_linodes、reboot、attach_volume…）
internal/cli/                   # cobra 命令樹
internal/mcpserver/             # MCP tool 註冊與 handler
internal/render/                # table / json / yaml / fields 投影
internal/spec/                  # 內嵌 OpenAPI 索引（endpoint 探索用）
testdata/                       # golden files、mock 回應
```

依賴（少量、必要）：
- `github.com/spf13/cobra` — CLI
- `github.com/modelcontextprotocol/go-sdk`（或 `mark3labs/mcp-go`，M2 前定案）
- 標準庫 `net/http`、`encoding/json`、`log/slog`；不引入 ORM

---

## 3. `internal/api` 規格（整個專案的地基）

| 項目 | 決策 |
|---|---|
| Base URL | `https://api.linode.com/v4`，可用 `LINODE_API_URL` 覆寫（測試用） |
| 認證 | `Authorization: Bearer <token>`；token 只從 env / config / keyring 取 |
| 分頁 | `page`、`page_size`（預設 100，上限 500）；回傳 `data/pages/results/page` |
| 過濾 | `X-Filter` header（JSON）；client 提供 `Filter builder`，不讓呼叫端手拼字串 |
| 排序 | query `+field` / `-field` |
| 重試 | 429 / 5xx / 連線錯誤：指數退避 + jitter，尊重 `Retry-After`；上限 4 次 |
| 速率 | 主動節流（token bucket），實際值以回應標頭校準，寫死前先實測 |
| 逾時 | 每請求 30s context；長時操作改用 events 輪詢，不長佔連線 |
| 錯誤 | 統一映射為 `APIError{Status, Code, Reason, Field, DocsURL, RequestID}` |
| 追蹤 | 保留 `X-Correlation-ID` / `X-Spec-Version`，印在 `--verbose` |
| 日誌 | `slog`；**所有 header 與 token 一律 redact**，預設 info 以下不輸出 body |

實作順序：先寫 client + httptest 單元測試，再接任何業務命令（避免邊寫命令邊改 client）。

---

## 4. CLI 設計

```
linode-agent auth whoami                       # 驗證 token、顯示帳號與 scopes
linode-agent linodes list --fields id,label,status,region
linode-agent linodes get 12345 --json
linode-agent linodes create --type g6-nanode-1 --region us-east --image linode/debian12 --label web1
linode-agent linodes reboot 12345 --wait
linode-agent volumes list | attach | detach | resize
linode-agent domains list | records-create
linode-agent firewall rules sync --from file.yaml
linode-agent api GET /account/events --query page_size=25     # 逃生口（通用呼叫）
linode-agent mcp serve [--stdio|--http :8080]
linode-agent spec search volume                                 # 從內嵌 OpenAPI 搜尋 endpoint
```

慣例：
- 名詞用複數資源名，動詞為第二段（`linodes list`、`volumes attach`）
- 所有命令支援 `--json`、`--yaml`、`--fields a,b,c`、`--no-color`
- 破壞性命令支援 `--dry-run`，需要 `--yes` 才真的執行
- Exit code：0 成功、1 一般錯誤、2 用法錯誤、3 認證失敗、4 速率限制、5 資源不存在
- `--wait` 統一輪詢 `/account/events`（或資源 `status`）直到收斂，有 timeout

---

## 5. MCP 設計（重點：不要 300 個工具）

### 兩層策略
1. **逃生口工具**（1 個）：`linode_api`
   - 輸入：`method`、`path`、`query`、`body`
   - 用內嵌 OpenAPI 索引做**白名單校驗**（path 必須存在於 spec，method 必須對應）
   - 讓 agent 在沒有專屬工具時仍能做事，但不至於亂打
2. **精選工具**（約 20 個，覆蓋 90% 工作流）
   - `linodes_list` / `linodes_get` / `linodes_create` / `linode_boot` / `linode_shutdown` / `linode_reboot` / `linode_resize`
   - `volumes_list` / `volume_attach` / `volume_detach`
   - `images_list` / `image_capture`
   - `domains_list` / `dns_record_create`
   - `firewalls_list` / `firewall_rules_get`
   - `stackscripts_list` / `stackscript_create`
   - `events_list` / `event_wait`
   - `account_get` / `object_storage_keys_list`

### 工具契約規則
- 每個工具都有：`dry_run`（預設 true 於寫入類）、`confirm`（破壞性必填 true）、`fields`（投影）
- 輸入 schema 用 JSON Schema 明確 enum（例如 `--type` 的值從 `/linodes/types` 取當前清單）
- 輸出：`{ok, data, meta:{page,pages,results}, warnings[]}`，`data` 預設投影到 8–12 個關鍵欄位
- 錯誤：`{ok:false, error:{status, reason, field, retry_after}}`，不回 stack trace
- 禁止：`DELETE /account`、`/account/oauth-clients` 的刪除等帳號級破壞操作（直接不註冊）

### Agent 友善細節
- 列表工具預設 `page_size=25`，並回 `next_page`；不要一次抓 500 筆
- 文字/描述欄位截斷（例 500 字），完整內容用 `--fields` 或 `get` 取
- 長時操作（reboot、resize、image capture）回 `event_id`，另給 `event_wait` 工具
- 所有工具輸出不含 token、不含 request body 原樣（避免 secret echo）

---

## 6. 覆蓋範圍分期（對應官方 API 的 25 個資源群組）

官方參考頁的群組：Account、Beta programs、Databases、Domains、Identity and access、
Images、Linode instances、LKE、StackScripts、Longview、Maintenance、Managed、Monitor、
Network transfer prices、Networking、NodeBalancers、Object Storage、Placement groups、
Profile、Regions、Resource locking、Support、Tags、Volumes、VPCs。

- **Phase 1（讀取為主）**：Account、Profile、Regions、Linode instances、Volumes、Images、Tags、Events
- **Phase 2（寫入 + 安全網）**：Linodes 生命週期（create/boot/shutdown/reboot/resize/delete）、Volumes attach/detach/resize、Image capture
- **Phase 3（網路與 DNS）**：Domains + records、Networking（IP 指派、防火牆）、NodeBalancers、VPCs
- **Phase 4（進階/可選）**：Object Storage、LKE、Databases、StackScripts、Placement groups、Resource locking、Longview、Managed

Phase 1 結束就要能 demo：agent 透過 MCP 查機器、看狀態、列 volume，全部走 `--json` 精簡輸出。

---

## 7. 安全模型

- Token 來源優先序：`LINODE_TOKEN` env → `~/.config/linode-agent/credentials`（0600）→ OS keyring
- 建議 README 明示：agent 專用 token 請開**最小 scope**，寫入類另給一個
- MCP server 預設**只註冊讀取工具**；寫入類要 `--allow-write` 啟動旗標才註冊
- 破壞性工具雙閘門：`dry_run` 預設 true + `confirm=true` 必填
- 所有寫入操作印審計行（`slog`）：時間、工具、目標 id、變更摘要、correlation id
- 輸出 redaction：`token`、`secret`、`password`、`*_key` 欄位一律 `***`

---

## 8. 測試策略

- `internal/api`：`httptest` mock server，覆蓋分頁、429 退避、X-Filter 產生、錯誤映射
- `internal/ops`：table-driven，用 golden JSON 比對投影後輸出
- MCP：用官方 SDK 的 in-memory client 跑每個工具的 happy path + 一個失敗路徑
- 契約測試：內嵌 OpenAPI 索引 → 驗證每個工具的 `path/method` 仍存在（防 API 升版破壞）
- 端到端（可選）：對真實帳號跑 read-only 清單，標記 `-tags=e2e`，CI 預設跳過

---

## 9. 里程碑與驗收

| 里程碑 | 內容 | 驗收 |
|---|---|---|
| M0 | repo 骨架、cobra、`auth whoami`、api client + 測試 | `whoami` 能印帳號；client 單元測試綠 |
| M1 | Phase 1 讀取命令 + 內嵌 OpenAPI 索引 + `spec search` | 8 個資源群組可 list/get，`--json` 輸出一致 |
| M2 | MCP stdio server + 讀取類工具 + `linode_api` 逃生口 | agent 可用 MCP 完成「查一台機器並回報規格」 |
| M3 | Phase 2 寫入 + dry-run/confirm + events 輪詢 | 能建機器、attach volume、capture image，全程可審計 |
| M4 | Phase 3 網路/DNS + 工具輸出精簡化 | 建立 domain record、防火牆規則下發 |
| M5 | 文件、README、範例 prompts、`go install` 流程 | 新機器 5 分鐘內可跑起來 |

---

## 10. 風險與待確認

- **MCP SDK 選擇**：`modelcontextprotocol/go-sdk` 與 `mark3labs/mcp-go` 成熟度不同，M2 前做 30 分鐘 spike 定案
- **OpenAPI 來源**：官方 `api.linode.com/v4/openapi.yaml` 已 404，需確認 spec 的現行取得方式（techdocs 頁或 GitHub 上的 docs repo），否則 Phase 1 的 `spec search` 要改成手寫索引
- **速率限制數值**：文件寫的數字與實際標頭可能不同，實作時以 `X-RateLimit-*` 校準
- **`--wait` 的收斂判定**：不同操作收斂條件不同（status vs event status），要逐個定義
- **token 誤用風險**：agent 拿到寫入 token 可能誤刪資源 → 用 scope 分離 + 預設唯讀

---

## 11. 待你決定的三件事

1. MCP 只做 stdio，還是同時要 HTTP（給遠端 agent）？
2. v1 是否就先支援寫入（M3），還是先交付唯讀版本讓你試？
3. 專案放哪：`~/linode-agent`（目前 plan 所在）還是你要指定 repo 名稱/路徑？
