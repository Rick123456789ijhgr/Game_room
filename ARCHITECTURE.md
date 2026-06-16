# 專案架構與檔案說明 (Project Architecture)

本專案是一個基於 Go 語言與 WebSockets 實作的畫圖猜謎 (Draw and Guess) 遊戲後端。為了提高程式碼的維護性與可讀性，我們將核心邏輯依照功能進行了拆分。

## 目錄結構

```text
GMAE/
├── main.go             # 應用程式入口，負責伺服器初始化
├── server/             # 遊戲伺服器核心邏輯
│   ├── types.go        # 資料結構定義
│   ├── room.go         # 房間與玩家管理邏輯
│   └── ws.go           # WebSocket 事件處理與廣播
├── static/             # 前端靜態資源 (HTML, CSS, JS)
├── assets/             # 其他素材與資源
└── go.mod / go.sum     # Go 模組依賴管理
```

## 檔案詳細說明

### `main.go`
- **職責**：專案的主程式入口 (Entry Point)。
- **功能**：
  - 讀取環境變數 (例如 `PORT`)。
  - 初始化 Melody WebSocket 實例。
  - 呼叫 `server.SetupWebSockets(m)` 來註冊所有 WebSocket 事件。
  - 設定 HTTP 路由 (`/ws`, `/ping`, `/`, `/assets/`)。
  - 啟動 HTTP 伺服器監聽請求。

### `server` 套件 (Package)
這個套件封裝了所有與遊戲和 WebSocket 通訊相關的核心邏輯。

#### 1. `server/types.go`
- **職責**：定義系統中用到的所有資料結構 (Structs)。
- **功能**：
  - `Message`: 定義了前端與後端通訊的標準 JSON 格式（包含 `event`, `room_id`, `data`）。
  - `JoinData`: 定義了玩家加入或建立房間時傳遞的資料（例如 `nickname`）。
  - `MemberInfo`: 定義了廣播房間成員列表時，單個成員的狀態資料（暱稱、是否為房主、是否準備就緒）。

#### 2. `server/room.go`
- **職責**：處理房間狀態與成員管理的輔助函式。
- **功能**：
  - `buildMemberList`: 遍歷 Melody 的連線 Session，找出同一個房間的所有玩家，並整理成清單。
  - `broadcastMemberList`: 負責將最新的房間成員清單廣播給該房間內的所有玩家，確保大家的畫面同步。
  - `allReady`: 檢查除了房主以外，房間內的所有玩家是否都已經按下「準備」按鈕。

#### 3. `server/ws.go`
- **職責**：集中處理所有的 WebSocket 事件。
- **功能**：
  - 提供了 `SetupWebSockets` 函式供 `main.go` 呼叫。
  - **連線與斷線處理**：管理玩家的連線與中斷，當房主斷線時關閉房間，當一般玩家斷線時更新成員列表。
  - **事件分發 (Event Routing)**：解析從前端收到的 `Message`，並根據不同的 `event` 執行對應邏輯：
    - `create_room` / `join_room`: 建立與加入房間。
    - `player_ready` / `start_game`: 遊戲準備與開始邏輯。
    - `kick_player`: 房主踢人功能。
    - `draw` / `clear`: 畫布繪製與清除的即時同步。
    - `chat`: 聊天室訊息廣播。

## 擴充指南 (How to Extend)
如果未來需要新增功能：
1. **新增通訊事件**：在 `server/ws.go` 的 `switch msg.Event` 中加入新的 case。
2. **新增資料模型**：如果新功能需要複雜的 JSON 負載，請在 `server/types.go` 裡面定義新的 Struct。
3. **新增遊戲邏輯**：如果邏輯與房間管理有關，寫在 `server/room.go` 中；如果屬於全新的邏輯（例如題庫管理、分數計算），建議在 `server` 目錄下新增對應的檔案（例如 `game.go` 或 `score.go`）來保持程式碼整潔。
