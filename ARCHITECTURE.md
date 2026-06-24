# 專案架構與檔案說明 (Project Architecture)

本專案是一個基於 Go 語言與 WebSockets 實作的畫圖猜謎 (Draw and Guess) 遊戲後端。為了提高程式碼的維護性與可讀性，我們將核心邏輯依照功能進行了拆分。

## 目錄結構

```text
GMAE/
├── main.go                   # 應用程式入口，負責伺服器初始化
├── Dockerfile                # Docker 容器化部署設定
├── server/                   # 遊戲伺服器核心邏輯
│   ├── types.go              # 資料結構定義
│   ├── state.go              # 房間狀態管理與共用工具函式
│   ├── room.go               # 房間成員管理輔助函式
│   ├── handlers.go           # 所有 WebSocket 事件的業務邏輯處理器
│   ├── game.go               # 遊戲回合流程控制（抽籤、計時、結算）
│   ├── score.go              # 分數計算、廣播與勝負判定
│   ├── questions.go          # 題庫載入與隨機出題
│   ├── filter.go             # 髒話過濾與訊息發送頻率限制
│   └── ws.go                 # WebSocket 生命週期設定與事件分發
├── static/                   # 前端靜態資源
│   ├── index.html            # 主頁面 HTML
│   ├── style.css             # 全域樣式
│   └── app.js                # 前端遊戲邏輯
├── assets/                   # 其他素材與資源
│   ├── img/                  # 圖片素材
│   └── json/                 # 題庫 JSON 檔案
│       ├── questions_set0_easy.json    # 簡單題庫
│       ├── questions_set1_medium.json  # 中等題庫
│       └── questions_set2_hard.json    # 困難題庫
└── go.mod / go.sum           # Go 模組依賴管理
```

## 檔案詳細說明

### `main.go`
- **職責**：專案的主程式入口 (Entry Point)。
- **功能**：
  - 讀取環境變數 (例如 `PORT`)。
  - 呼叫 `server.LoadQuestions()` 載入題庫。
  - 初始化 Melody WebSocket 實例。
  - 呼叫 `server.SetupWebSockets(m)` 來註冊所有 WebSocket 事件。
  - 設定 HTTP 路由 (`/ws`, `/ping`, `/`, `/assets/`)。
  - 啟動 HTTP 伺服器監聽請求。

### `Dockerfile`
- **職責**：容器化部署設定，採用多階段建置 (Multi-stage build)。
- **功能**：
  - **Builder 階段**：使用 `golang:1.21-alpine` 編譯 Go 二進位檔。
  - **Final 階段**：使用輕量的 `alpine:latest`，僅複製執行檔與必要靜態資源 (`static/`, `assets/`)，大幅縮小映像檔體積。
  - 預設暴露 `8080` 埠，可由環境變數 `PORT` 覆蓋（適用於 Render 等雲端平台）。

---

### `server` 套件 (Package)
這個套件封裝了所有與遊戲和 WebSocket 通訊相關的核心邏輯，依功能拆分為以下檔案：

#### 1. `server/types.go`
- **職責**：定義系統中用到的所有資料結構 (Structs)。
- **功能**：
  - `Message`: 定義前端與後端通訊的標準 JSON 格式（包含 `event`, `room_id`, `client_id`, `data`）。
  - `JoinData`: 定義玩家加入或建立房間時傳遞的資料（例如 `nickname`）。
  - `MemberInfo`: 定義廣播房間成員列表時，單個成員的狀態資料（暱稱、是否為房主、是否準備就緒）。

#### 2. `server/state.go`
- **職責**：管理房間的持久化狀態，並提供跨檔案共用的工具函式。
- **功能**：
  - `RoomState` 結構體：儲存每個房間的執行時狀態，包含：
    - `UsedDrawers`：本輪循環中已抽到過的畫者名單（用於確保輪流出場）。
    - `TimerCancel`：當前回合計時器的取消 channel。
    - `TargetScore`：勝利目標分數（預設 10 分）。
    - `IsOvertime`：是否進入延長賽模式。
  - `getRoomState` / `deleteRoomState`：房間狀態的 CRUD。
  - `cancelRoomTimer`：安全地取消當前房間的回合計時器。
  - `broadcastToRoom`：向指定房間內所有連線廣播訊息的共用工具。
  - `sendError`：向單一玩家發送錯誤訊息的小工具。

#### 3. `server/room.go`
- **職責**：處理房間成員清單管理的輔助函式。
- **功能**：
  - `buildMemberList`: 遍歷 Melody 的連線 Session，找出同一個房間的所有玩家並整理成清單。
  - `broadcastMemberList`: 將最新的房間成員清單廣播給該房間內所有玩家，確保畫面同步。
  - `allReady`: 檢查除了房主以外，房間內所有玩家是否都已按下「準備」按鈕。

#### 4. `server/handlers.go`
- **職責**：集中實作所有 WebSocket 事件的業務邏輯，為 `ws.go` 的事件分發提供具體處理函式。
- **功能（依事件分類）**：
  - **房間管理**：
    - `handleCreateRoom`: 建立新房間，設定房主狀態、初始化房間設定並廣播成員列表。
    - `handleJoinRoom`: 加入現有房間，檢查房間是否存在及遊戲是否已開始。
    - `handlePlayerReady`: 切換玩家的準備狀態（房主永遠為準備好）。
    - `handleSetRoomSettings`: 房主調整目標分數（範圍 1～100）。
    - `handleKickPlayer`: 房主將指定玩家踢出房間。
  - **遊戲流程**：
    - `handleStartGame`: 房主開始遊戲，驗證所有人已準備後重置分數並啟動第一回合。
    - `handleNextRound`: 房主觸發下一回合（僅在 `round_end` 後使用）。
  - **遊戲進行中**：
    - `handleGuess`: 處理玩家猜測，包含正確性驗證、分數計算、畫者獎分、早終止（全員猜對）及勝負判定。
    - `handleDraw`: 將畫布事件即時轉發給同房間其他玩家。
    - `handleClear`: 將清除畫布事件轉發給同房間其他玩家。
    - `handleChat`: 處理聊天訊息，包含答案洩漏防護、髒話過濾及頻率限制。

#### 5. `server/game.go`
- **職責**：控制遊戲回合的完整生命週期。
- **功能**：
  - `pickNextDrawer`: 從房間玩家中選出下一位畫者，確保每位玩家輪流上場；所有人都畫過後重新開始新循環。
  - `startRoundTimer`: 以 Goroutine 啟動回合計時器，包含兩個階段：
    1. **Delay 階段（6 秒）**：等待角色公告動畫播放完畢。
    2. **Countdown 階段（30 秒）**：倒數計時，結束後廣播 `round_end`（含本回合答案與分數快照）。
    - 支援透過 `cancel` channel 提前終止（例如全員猜對時）。
  - `startRound`: 協調一輪遊戲的完整啟動，包含抽選畫者、取得隨機題目、廣播 `game_start`（畫者收到題目，猜題者不收）並啟動計時器。
  - `broadcastRoomSettings`: 廣播當前房間設定（目標分數）給所有玩家。
  - `broadcastGameOver`: 廣播 `game_over` 事件，包含勝者名稱、是否延長賽及最終分數。

#### 6. `server/score.go`
- **職責**：分數計算、廣播與勝負判定。
- **功能**：
  - `buildScores`: 彙整房間內所有玩家的當前分數，回傳 `nickname → score` 的 Map。
  - `broadcastScores`: 將即時分數排行廣播給房間所有玩家（`score_update` 事件）。
  - `checkWinCondition`: 判斷是否有玩家達到目標分數，回傳勝者名稱或是否進入延長賽（同分平手時）。

#### 7. `server/questions.go`
- **職責**：管理遊戲題庫的載入與隨機出題。
- **功能**：
  - `LoadQuestions`: 從 `assets/json/` 目錄讀取三個難度等級的 JSON 題庫（簡單、中等、困難）並解析到記憶體中。若檔案不存在或解析失敗會記錄錯誤但不中斷程式。
  - `GetRandomTopic`: 以加權機率（簡單 50%、中等 35%、困難 15%）隨機抽取一道題目；若題庫為空則回傳預設值「自由發揮」。

#### 8. `server/filter.go`
- **職責**：保護遊戲環境的安全性與公平性工具函式。
- **功能**：
  - **髒話過濾**：
    - `profanityList`：可自訂的黑名單詞庫（預設含中英文常見不雅字詞）。
    - `censorMessage`: 將訊息中的不雅字詞替換為 `***`（大小寫不敏感）。
    - `containsProfanity`: 檢查訊息是否包含不雅字詞（用於猜測輸入的快速拒絕）。
  - **訊息頻率限制 (Rate Limiting)**：
    - `checkRateLimit`: 以每個 Session 為單位，強制每則訊息間隔至少 1 秒，防止玩家刷訊息。

#### 9. `server/ws.go`
- **職責**：WebSocket 生命週期設定與事件分發路由。
- **功能**：
  - 提供 `SetupWebSockets` 函式供 `main.go` 呼叫。
  - **連線與斷線處理**：管理玩家連線與中斷；房主斷線時關閉整個房間並通知所有成員，一般玩家斷線時更新成員列表。
  - **事件分發 (Event Routing)**：解析從前端收到的 `Message`，並根據 `event` 欄位呼叫 `handlers.go` 中對應的處理函式：
    - `create_room` / `join_room`: 建立與加入房間。
    - `player_ready` / `start_game` / `next_round`: 遊戲準備、開始與回合推進。
    - `set_room_settings`: 調整房間設定。
    - `kick_player`: 房主踢人。
    - `guess`: 玩家猜題。
    - `draw` / `clear`: 畫布即時同步。
    - `chat`: 聊天室訊息廣播。

---

### `static/` 前端靜態資源
- **`index.html`**：主頁面 HTML，包含大廳、等待室、遊戲畫面等各階段的 UI 結構。
- **`style.css`**：全域 CSS 樣式，定義頁面排版、動畫效果與視覺風格。
- **`app.js`**：前端遊戲核心邏輯，處理 WebSocket 連線、畫布繪製、UI 狀態切換與所有使用者互動事件。

### `assets/` 素材資源
- **`img/`**：遊戲使用的圖片素材。
- **`json/`**：題庫資料，分為三個難度等級的 JSON 檔案：
  - `questions_set0_easy.json`：簡單題目（出現機率約 50%）。
  - `questions_set1_medium.json`：中等題目（出現機率約 35%）。
  - `questions_set2_hard.json`：困難題目（出現機率約 15%）。

---

## 擴充指南 (How to Extend)
如果未來需要新增功能：
1. **新增通訊事件**：在 `server/ws.go` 的 `switch msg.Event` 中加入新的 case，並在 `server/handlers.go` 中實作對應的 handler 函式。
2. **新增資料模型**：如果新功能需要複雜的 JSON 負載，請在 `server/types.go` 裡面定義新的 Struct。
3. **新增遊戲邏輯**：
   - 與分數相關 → 修改或擴充 `server/score.go`。
   - 與回合流程相關 → 修改或擴充 `server/game.go`。
   - 全新的獨立功能 → 在 `server/` 目錄下新增對應檔案，保持程式碼整潔。
4. **新增題庫**：在 `assets/json/` 目錄下新增 JSON 檔案，並在 `server/questions.go` 的 `LoadQuestions` 函式中加入載入邏輯。
5. **調整安全規則**：修改 `server/filter.go` 中的 `profanityList` 黑名單，或調整 `checkRateLimit` 的時間間隔。
