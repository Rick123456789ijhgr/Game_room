# 專案名稱：網頁版多人連線「你畫我猜」遊戲廳 (Draw & Guess Web Lobby)

## 1. 專案目標
開發一個輕量級的網頁多人連線繪圖遊戲。玩家可以輸入房號加入特定房間，並在 HTML5 Canvas 上繪圖，繪圖軌跡需透過 WebSocket 即時同步給同房間內的其他玩家。專案最終將部署至 Render。

## 2. 技術棧限制 (CRITICAL)
* **後端 (Backend):** Go 語言 (Golang 1.21+)。
* **後端路由與靜態檔案:** 使用 Go 原生 `net/http`。
* **WebSocket 核心 (極度重要):** 必須使用 `github.com/olahol/melody`。絕對禁止使用 Socket.io。
* **前端 (Frontend):** 原生 HTML5, CSS3, Vanilla JavaScript。不可使用 React/Vue 等任何框架。
* **前端通訊 (極度重要):** 絕對禁止引入任何外部通訊函式庫。必須 100% 使用瀏覽器原生的 `WebSocket` API (`new WebSocket()`)。

## 3. 目錄架構規劃
project_root/
├── main.go               # Go 後端進入點與 Melody 邏輯
├── go.mod                
├── go.sum                
└── static/               # 前端靜態檔案 (由 Go 伺服)
    ├── index.html        # 遊戲 UI 與 Canvas
    ├── style.css         
    └── app.js            # 前端 WebSocket 連線與 Canvas 繪圖邏輯

## 4. 核心功能規格與通訊協定
1. **HTTP 服務:** Go 需在 `/` 伺服 `static/` 目錄下的靜態檔案，並在 `/ws` 開放 WebSocket 升級端點。
2. **保活機制:** 需提供一個 `/ping` 路由，回傳 HTTP 200 "pong"，供外部 Cron Job 定期喚醒 Render 伺服器。
3. **動態 Port:** 監聽的 Port 必須從環境變數 `os.Getenv("PORT")` 取得，若無則預設為 8080。
4. **JSON 通訊協定:** 前後端所有 WebSocket 訊息必須使用 JSON 格式傳遞，統一結構為：`{"event": "事件名稱", "room_id": "房間號", "data": {任意資料}}`。
5. **房間機制:** 由於 Melody 沒有內建 Room，後端需在玩家發送 `join_room` 事件時，使用 `session.Set("room", room_id)` 將該連線標記。
6. **繪圖同步與廣播:** 前端發送 `draw` 事件時，後端透過 Melody 的 `BroadcastFilter` 功能，只將訊息轉發給 `session.Get("room")` 相同的其他玩家。