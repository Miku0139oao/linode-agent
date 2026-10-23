# Linode CLI + TUI + MCP（Go）實作計劃 v2

目標：一支 Go 二進位檔，同時提供三種介面——
**TUI（給人看，好用好看）**、**CLI（給腳本）**、**MCP（給 agent）**。

三者共用同一層 client 與 ops，業務邏輯不重複實作。

**本輪交付範圍（已確認）：M0 → M3（唯讀）**，含 TUI 唯讀、MCP stdio + HTTP。

## 進度（2026-10-23）

- M0 ✅ api client（分頁／X-Filter／429 退避／錯誤映射）+ config + `auth whoami`，測試綠
- M1 ✅ 讀取命令（linodes/volumes/images/regions/events/tags）+ `api` 逃生口 + `--fields` 投影
  （`spec search` 暫緩：官方 `openapi.yaml` 404）
- M2 ✅ TUI 唯讀（Linodes/Volumes/Images/Events/Doctor、篩選、詳情、說明浮層、狀態符號＋顏色）
- M3 ✅ MCP stdio + HTTP，9 個工具，統一回應契約與 redaction；stdio/HTTP 皆已端到端驗證
- M4 ✅ Phase 2 寫入：linode create/boot/shutdown/reboot/update/resize/delete、volume create/attach/detach/resize/delete、image capture；dry-run 預設 + `--yes` + audit log
- M5 ✅ MCP 寫入工具（`--allow-write`）＋ `dry_run=false` 與 `confirm=true` 雙閘門
- M5+ ✅ `--wait` 事件收斂（CLI／MCP）、TUI 寫入動作（`b`/`s`/`R`/`x`）與確認浮層
- 尚未開始：Domains/Networking/NodeBalancers/VPCs、`spec search`

---

## 0. 目標與非目標

### 目標
- 單一 binary（`linode-agent`），`go build` 跨平台，無 runtime 依賴
- **TUI**：儀表板 + 資源瀏覽 + 操作確認；鍵盤驅動、有主題、能看能耗
- **CLI**：覆蓋常用資源，`--json` 全量可腳本化
- **MCP**：stdio 與 HTTP 兩種傳輸都提供；工具集小而穩（不把 300+ endpoint 全灌進去）
- 安全：讀寫分離、破壞性操作需顯式確認、token 永不外洩
- agent 友善：預設精簡輸出（欄位投影 + 截斷 + 游標）

### 非目標（v1 不做）
- 不做 Web / 桌面 GUI（TUI 取代）
- 不重寫官方 Python `linode-cli` 全部行為；相容常用旗標即可
- 不做多帳號同時連線（支援多 profile，一次連一個）
- 不實作 OAuth 流程（只支援 Personal Access Token）

---

## 1. 架構

```
┌────────────────┐
│ TUI (bubbletea)│──┐
└────────────────┘  │
┌────────────────┐  │      ┌──────────────┐     ┌───────────────────┐
│ CLI (cobra)    │──┼─────▶│ internal/api │────▶│ api.linode.com/v4 │
└────────────────┘  │      │ paginate     │     └───────────────────┘
┌────────────────┐  │      │ X-Filter     │
│ MCP (stdio/HTTP)│─┘      │ retry/429    │
└────────────────┘         │ err mapping  │
        ▲                  └──────────────┘
        │                          ▲
   agent (hermes)          internal/ops（三個介面共用）
```

原則：**業務動作只寫在 `internal/ops` 一次**，TUI / CLI / MCP 都只是呼叫端。
`ops` 回傳結構化結果（不是列印字串），呈現交給 `internal/render` 與 TUI 的 View。

---

## 2. 套件結構

```
cmd/linode-agent/main.go        # 進入點；TTY 且無子命令 → 直接開 TUI
internal/config/                # profile / token（env > 0600 檔案 > keyring）
internal/api/                   # HTTP client：pagination、X-Filter、retry、error mapping
internal/api/types.go           # 核心型別（Linode、Volume、Domain、Event…）
internal/ops/                   # 高階動作，回傳 struct，不印字
internal/cli/                   # cobra 命令樹
internal/tui/                   # bubbletea MVU：models / views / theme / keys
internal/mcpserver/             # MCP tool 註冊與 handler（stdio + HTTP）
internal/render/                # table / json / yaml / fields 投影
internal/spec/                  # 內嵌 OpenAPI 索引（endpoint 探索）
testdata/                       # golden files、mock 回應
```

依賴：
- `github.com/spf13/cobra`
- `github.com/charmbracelet/bubbletea` + `bubbles` + `lipgloss`
- MCP SDK：`modelcontextprotocol/go-sdk` 或 `mark3labs/mcp-go`（M3 前 spike 定案）
- 其餘走標準庫

---

## 3. `internal/api` 規格（地基）

| 項目 | 決策 |
|---|---|
| Base URL | `https://api.linode.com/v4`，`LINODE_API_URL` 可覆寫（測試） |
| 認證 | `Authorization: Bearer <token>`；來源 env / config / keyring |
| 分頁 | `page`、`page_size`（預設 100、上限 500）；回 `data/pages/results/page` |
| 過濾 | `X-Filter` header（JSON）由 Filter builder 產生，禁止手拼 |
| 排序 | `+field` / `-field` |
| 重試 | 429 / 5xx / 連線錯誤：指數退避 + jitter，尊重 `Retry-After`，上限 4 次 |
| 速率 | token bucket 主動節流；數值以 `X-RateLimit-*` 校準 |
| 逾時 | 單請求 30s；長時操作改輪詢 events |
| 錯誤 | `APIError{Status, Code, Reason, Field, DocsURL, RequestID}` |
| 追蹤 | 保留 `X-Correlation-ID`、`X-Spec-Version` |
| 日誌 | `slog`，header 與 token 一律 redact |

先完成 client + 測試，再接任何命令。

---

## 4. CLI 設計

```
linode-agent                      # TTY 下直接進 TUI
linode-agent tui                  # 明確啟動 TUI
linode-agent auth whoami
linode-agent linodes list --fields id,label,status,region
linode-agent linodes get 12345 --json
linode-agent volumes list
linode-agent images list
linode-agent events list --limit 20
linode-agent api GET /account/events --query page_size=25     # 逃生口
linode-agent mcp serve                      # stdio（預設）
linode-agent mcp serve --http :8080         # HTTP
linode-agent spec search volume
```

慣例：
- 名詞複數 + 動詞（`linodes list`、`volumes attach`）
- 全命令支援 `--json` `--yaml` `--fields` `--no-color`
- 破壞性命令 `--dry-run` 預設開，需 `--yes` 執行（Phase 2）
- Exit code：0 成功 / 1 一般 / 2 用法 / 3 認證 / 4 429 / 5 資源不存在
- `--wait` 統一輪詢 events / status，帶 timeout

---

## 5. TUI 設計（人類用，好看＋好用）

### 技術選型
- `bubbletea`（MVU）+ `bubbles`（list / table / textinput / viewport / spinner）+ `lipgloss`
- 純 `Update/View` 模型 → 可用固定終端寬高做 golden 測試，不必真的開終端

### 版面
```
┌ Linode Agent ── profile: personal ── acct: xxx ── rl: 87/400 ┐
│ ▸ Linodes   ● 3 running  ○ 1 offline                          │
│ ▸ Volumes   (12)                                              │
│ ▸ Disk Images (8)                                             │
│ ▸ Events    (live)                                            │
├───────────────────────────────────────────────────────────────┤
│ ID       LABEL   STATUS    TYPE         REGION   IPV4         │
│ 12345 ●  web1    running   g6-nanode-1  us-east  1.2.3.4      │
│ 12346 ○  db1     offline   g6-standard-2 eu-west 5.6.7.8      │
├───────────────────────────────────────────────────────────────┤
│ /filter  enter:detail  r:refresh  ?:help  q:quit   ⠋ polling │
└───────────────────────────────────────────────────────────────┘
```

### 畫面
1. **Dashboard**：帳號摘要、running/offline 計數、最近 events、region 分佈
2. **Linodes**：表格（狀態色點、region、IP、規格）→ 詳情（規格、IP、標籤、events）
3. **Volumes / Images / Events**：同構表格 + 詳情
4. **Doctor**：token 有效性、scopes、profile、API 版本、速率標頭

### 操作
- 鍵位：`j/k` 移動、`g/G` 頭尾、`/` 模糊搜尋、`enter` 進入、`tab` 切面板、`r` 刷新、`?` 說明、`q` 退出
- `:` 命令面板（給不想記快捷鍵的人）
- 破壞性操作跳確認浮層：目標、變更摘要、dry-run 預覽（Phase 2 啟用）
- 錯誤：狀態列紅字 + `!` 開詳情（status / reason / field / request id）

### 資料層
- 共用 `internal/ops`；TUI 只負責 cache + 呈現
- 輪詢只抓可見資源，預設 5s；429 自動退避並在狀態列標示
- `r` 強制刷新；切頁不重抓未過期資料

### 外觀
- `lipgloss` AdaptiveColor（亮/暗終端），尊重 `NO_COLOR`
- 最少 80×24；窄版收欄位而非破版
- 狀態用符號 + 顏色雙編碼（色盲友善）
- 主題集中 `internal/tui/theme.go`

### 測試
- `Update` 餵鍵盤事件 → 斷言 state
- `View` 固定 100×30 → golden 比對
- ops 用 mock server，TUI 測試不打真 API

---

## 6. MCP 設計（不要 300 個工具）

### 傳輸
- `linode-agent mcp serve` → stdio（給本機 agent）
- `linode-agent mcp serve --http :8080` → Streamable HTTP（給遠端 agent）
- 兩者共用同一份 tool registry

### 兩層策略
1. **逃生口（1 個）**：`linode_api(method, path, query, body)`
   - 用內嵌 OpenAPI 索引白名單校驗 path/method
2. **精選工具（~20）**：機器生命週期、volumes、images、DNS、firewalls、events、account

### 契約
- 每個工具：`dry_run`（寫入類預設 true）、`confirm`（破壞性必填）、`fields`
- 輸入 schema 用 enum（型號清單取自 `/linodes/types`）
- 輸出 `{ok, data, meta:{page,pages,results}, warnings[]}`，`data` 投影 8–12 欄
- 錯誤 `{ok:false, error:{status, reason, field, retry_after}}`
- 不註冊帳號級破壞操作（`DELETE /account`、OAuth client 刪除）

### Agent 友善
- 列表預設 `page_size=25` + `next_page`
- 長文字截斷 500 字
- 長時操作回 `event_id` + `event_wait`
- 輸出不含 token、不回 request body 原樣

---

## 7. 覆蓋範圍分期

官方資源群組：Account、Beta programs、Databases、Domains、Identity and access、Images、
Linode instances、LKE、StackScripts、Longview、Maintenance、Managed、Monitor、
Network transfer prices、Networking、NodeBalancers、Object Storage、Placement groups、
Profile、Regions、Resource locking、Support、Tags、Volumes、VPCs。

- **Phase 1（唯讀）**：Account、Profile、Regions、Linodes、Volumes、Images、Tags、Events ← 本輪
- **Phase 2（寫入）**：Linodes 生命週期、Volume attach/detach/resize、Image capture
- **Phase 3（網路）**：Domains + records、Networking/防火牆、NodeBalancers、VPCs
- **Phase 4（可選）**：Object Storage、LKE、Databases、StackScripts、Placement groups、Resource locking、Longview、Managed

---

## 8. 安全模型

- Token 優先序：`LINODE_TOKEN` → `~/.config/linode-agent/credentials`（0600）→ keyring
- README 明示：agent 用最小 scope 讀取 token
- MCP 預設只註冊讀取工具，寫入需 `--allow-write`（Phase 2）
- 破壞性操作雙閘門：`dry_run` 預設 true + `confirm=true`
- 寫入寫 audit log：時間、工具、目標 id、變更摘要、correlation id
- Redaction：`token`、`secret`、`password`、`*_key` → `***`
- TUI 同規則：確認浮層必顯示目標與變更摘要

---

## 9. 測試策略

- `internal/api`：httptest 覆蓋分頁、429 退避、Filter 產生、錯誤映射
- `internal/ops`：table-driven + golden JSON
- `internal/tui`：Update state 測試 + 固定尺寸 View golden
- MCP：in-memory client，每個工具 happy path + 一個失敗路徑
- 契約測試：內嵌 spec 驗證工具 path/method 仍存在
- e2e（可選）：真實帳號 read-only，`-tags=e2e`，CI 預設跳過

---

## 10. 里程碑

| 里程碑 | 內容 | 驗收 |
|---|---|---|
| M0 | 骨架、config、api client + 測試、`auth whoami` | `whoami` 印帳號；client 測試綠 |
| M1 | Phase 1 CLI 讀取 + 內嵌 spec 索引 + `spec search` | 8 個群組 list/get，`--json` 一致 |
| M2 | **TUI 唯讀**（Dashboard/Linodes/Volumes/Images/Events/Doctor） | 鍵盤瀏覽順暢、錯誤處理正常、80×24 不破版 |
| M3 | MCP（stdio + HTTP）+ 讀取工具 + `linode_api` | agent 透過 MCP 查機器並回報規格 |
| M4 | Phase 2 寫入 + dry-run/confirm + events 輪詢 | CLI/TUI 能建機器、attach volume，可審計 |
| M5 | MCP 寫入工具（`--allow-write`）+ TUI 寫入動作 | agent 在授權下完成一次重開機 |
| M6 | Phase 3 網路/DNS + 文件 | 建 domain record、防火牆規則下發 |

本輪目標：M0 → M3。

---

## 11. 風險與待確認

- **MCP SDK 選擇**：M3 前 30 分鐘 spike 定案
- **OpenAPI 來源**：`api.linode.com/v4/openapi.yaml` 目前 404，需確認現行取得方式；否則 `spec search` 改手寫索引
- **TUI 依賴重量**：bubbletea 會讓 binary 大幾 MB，可接受但記錄
- **速率限制**：以 `X-RateLimit-*` 校準；TUI 輪詢是主要消耗來源
- **`--wait` 收斂判定**：不同操作收斂條件不同，需逐個定義
- **寫入 token 誤用**：靠 scope 分離 + 預設唯讀 + 確認閘門

---

## 12. 已定案

1. MCP 傳輸：**stdio 與 HTTP 都給**
2. 本輪範圍：**M3**（含 TUI 唯讀 + MCP 讀取）
3. 專案路徑：**`~/linode-agent`**
