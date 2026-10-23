# linode-agent

一支 Go 二進位檔，同時提供三種介面操作 **Linode API v4**：

- **TUI** — 給人看的全螢幕儀表板（鍵盤操作、80×24 起可用）
- **CLI** — 給腳本，全部支援 `--json` / `--fields`
- **MCP** — 給 agent，stdio 與 Streamable HTTP 兩種傳輸

三者共用同一層 `internal/api`（HTTP 語意）與 `internal/ops`（業務動作），
所以「CLI 做得到、MCP 做不到」這種分裂不會發生。

---

## 安裝

```bash
# 直接安裝（需要 Go 1.25+）
go install github.com/Miku0139oao/linode-agent/cmd/linode-agent@latest

# 或從原始碼
git clone https://github.com/Miku0139oao/linode-agent && cd linode-agent
make build            # → ./bin/linode-agent
```

需求：Go 1.25+。

## 憑證

優先序：`LINODE_TOKEN` → `~/.config/linode-agent/credentials`（0600）→ 無。

```bash
export LINODE_TOKEN=...            # 一次性
linode-agent auth login            # 寫入 0600 設定檔（可多 profile）
linode-agent auth whoami --json    # 驗證
linode-agent auth login --profile work
linode-agent --profile work linodes list
```

建議給 agent 一把**只有讀取 scope** 的 token，寫入另給一把。

---

## CLI

```bash
linode-agent linodes list
linode-agent linodes list --fields id,label,status,region,ipv4.0
linode-agent linodes get 12345 --json
linode-agent volumes list
linode-agent images list
linode-agent regions list
linode-agent events list --limit 20
linode-agent tags list

# 逃生口：任何 endpoint
linode-agent api GET /account/events --query page_size=5
linode-agent api POST /linode/instances --body '{"region":"us-east","type":"g6-nanode-1"}'
```

慣例：

- `--json` 輸出原始陣列／物件；不加則是對齊表格 + 筆數頁碼
- `--fields a,b.c,list.0` 投影欄位（支援 dotted path 與陣列索引）
- `page_size` 會被夾在 **25–500**（實測：小於 25 會回 HTTP 400），`--limit` 是總筆數上限
- exit code：`0` 成功 / `1` 一般錯誤 / `2` 用法錯誤 / `3` 認證失敗 / `4` 速率限制 / `5` 找不到

## TUI

```bash
linode-agent          # 在 TTY 直接進 TUI
linode-agent tui
linode-agent tui --snapshot --width 100 --height 24   # 輸出一張純文字畫面（不需 TTY）
```

```
 Linode Agent   profile: default
 1 Linodes (4)  2 Volumes  3 Images  4 Events  5 Doctor
 ID      LABEL   STATUS     TYPE          REGION   IPV4
 1001    web1    ● running  g6-nanode-1   us-east  192.0.2.10
 1002    db1     ○ offline  g6-standard-2 eu-west  198.51.100.7
 ?:說明  tab:切換  /:篩選  r:刷新  enter:詳情  q:退出
```

鍵位：

- `j` / `k`（或方向鍵）移動，`tab` / `shift+tab` 或 `1`–`5` 切換資源頁
- `/` 篩選（`esc` 清除）、`r` 重新載入、`enter` 開詳情（`j`/`k` 捲動、`esc` 返回）
- `?` 說明、`q` / `ctrl+c` 離開
- 加了 `--allow-write` 後，Linodes 頁多出：`b` 開機、`s` 關機、`R` 重開、`x` 刪除
  （每個都會跳確認浮層，`y` 執行 / `n` 取消）

狀態用**符號＋顏色**雙編碼（`● running` / `○ offline` / `◐ provisioning` / `✕ failed`），
色盲或 `NO_COLOR` 環境仍可讀。

## MCP

```bash
linode-agent mcp serve                 # stdio（本機 agent）
linode-agent mcp serve --http :8080    # Streamable HTTP，endpoint /mcp
```

stdio 模式下 **stdout 只輸出 JSON-RPC**，診斷訊息一律走 stderr。

MCP 設定範例（Claude Code / Codex / 任何支援 MCP 的 client）：

```json
{
  "mcpServers": {
    "linode": {
      "command": "/absolute/path/to/bin/linode-agent",
      "args": ["mcp", "serve"],
      "env": { "LINODE_TOKEN": "..." }
    }
  }
}
```

遠端 agent 用：

```bash
linode-agent mcp serve --http 127.0.0.1:8080
# client 連 http://127.0.0.1:8080/mcp
```

### 工具（唯讀，9 個）

`linodes_list`、`linodes_get`、`volumes_list`、`images_list`、`regions_list`、
`events_list`、`tags_list`、`account_get`、`linode_api`（逃生口）。

輸出契約：

```json
{ "ok": true,
  "data": [ { "id": 1001, "label": "web1", "status": "running" } ],
  "meta": { "page": 1, "pages": 1, "results": 1, "rate_limit": 400, "rate_remaining": 399 } }
```

失敗：

```json
{ "ok": false, "error": { "status": 404, "reason": "Not found", "field": "id", "hint": "資源不存在或已被刪除" } }
```

- 列表預設 `page_size=25`（上限 500），可用 `fields` 投影
- 長字串截斷 500 字；`token` / `secret` / `password` / `*_key` 一律 `***`
- 逃生口白名單：唯讀模式只允許 `GET`，並封鎖 `/account/oauth-clients` 與帳號刪除

---

## 開發

```bash
make test     # go test ./...
make vet      # go vet ./...
make fmt      # gofmt -l -w .
make build
```

測試策略：

- `internal/api`：`httptest` 覆蓋分頁、429 退避、`X-Filter` 產生、錯誤映射
- `internal/cli`：以 mock API 跑真實命令，驗證表格／JSON／exit code
- `internal/tui`：`Update` 餵鍵盤事件斷言 state；`View` 固定 100×30 golden 比對
  （`TUI_PREVIEW=/tmp/out.txt go test ./internal/tui -run TestPreviewSnapshot` 可輸出純文字畫面）
- `internal/mcpserver`：`tools/list` 註冊檢查、逃生口白名單、redaction／截斷

## 寫入操作（Phase 2）

所有變更類命令**預設 dry-run**：只印出將送出的請求，不碰 API。要真的執行必須加 `--yes`。

```bash
linode-agent linodes create --region jp-osa --type g6-nanode-1 \
    --image linode/ubuntu24.04 --label web2 --root-pass-stdin        # 先看計劃
linode-agent linodes create ... --yes                                 # 真的建
linode-agent linodes boot|shutdown|reboot <id> [--yes]
linode-agent linodes update <id> --label new-name [--yes]
linode-agent linodes resize <id> --type g6-standard-1 [--yes]
linode-agent linodes delete <id> [--yes]
linode-agent linodes disks <id>              # image capture 需要 disk_id

linode-agent volumes create --label data --size 100 --region jp-osa [--yes]
linode-agent volumes attach <volume-id> --linode <id> [--yes]
linode-agent volumes detach <volume-id> [--yes]
linode-agent volumes resize <volume-id> --size 200 [--yes]
linode-agent volumes delete <volume-id> [--yes]

linode-agent images capture --disk <disk-id> --label my-image [--yes]
```

安全機制：

- *dry-run 預設開*，`--yes` 是唯一的執行開關
- `--wait` 會在送出後輪詢對應事件，直到 `finished`／`failed`（最多 180 秒）才結束，適合腳本串接
- 每個動作都寫 audit log：`~/.local/state/linode-agent/audit.jsonl`（0600），含時間、profile、action、target、dry_run、成功與否
- `root_pass` 不會被寫進 audit 或輸出（只出現在送往 API 的請求）
- MCP 的寫入工具只在 `--allow-write` 時註冊，且必須 `dry_run=false` **加** `confirm=true` 才會執行；再帶 `wait=true` 可同步等事件

## 已知 API 怪癖

實測踩到的、官方文件寫錯或沒寫的地方（`linode-agent` 內部已處理，但自己打 API 時要注意）：

- **`/linode/instances/{id}/transfer` 的單位是混的**：`used` 是 **bytes**，但
  `quota`／`billable` 是 **GB**（文件把三個都寫成 GB）。`/account/transfer` 則全部是 GB。
  `linode-agent network usage` 已代為換算。
- **`page_size` 只接受 25–500**：小於 25 會直接回 `HTTP 400 Must be 25-500`（文件未載明）。
  client 會自動夾到合法區間。
- **`X-RateLimit-Limit` 實際值跟文件不同**：實測是 `1840`，不是文件常見的 400。
  一律以回應標頭為準，不要寫死。
- **`openapi.yaml` 已 404**：文件站上的 spec 連結失效，所以 `spec search` 目前沒有做。

## 目前狀態

已完成：

- `internal/api` 客戶端 + 測試、`internal/ops`、CLI 讀取命令、`--fields` 投影、exit code 映射
- TUI 唯讀：Linodes / Volumes / Images / Events / Doctor，含篩選、詳情、說明浮層、`--snapshot`
- MCP：stdio + HTTP，讀取工具 + 逃生口，統一輸出契約與 redaction
- *Phase 2 寫入*：機器生命週期、volumes、image capture，全部 dry-run 預設 + audit log
- MCP 寫入工具（`--allow-write`）＋ `confirm` 雙閘門
- `--wait` 事件收斂（CLI 與 MCP 都支援）
- TUI 寫入動作（`b`/`s`/`R`/`x`）＋ 確認浮層

待做：

- Domains / Networking / NodeBalancers / VPCs
- `spec search`：官方 `openapi.yaml` 目前 404，需先確認現行 spec 來源
