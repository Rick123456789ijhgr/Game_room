package server

import (
	"encoding/json"
)

// Message 對應 PRD 中定義的 JSON 通訊協定：
// {"event": "...", "room_id": "...", "data": {...}}
type Message struct {
	Event  string          `json:"event"`
	RoomID string          `json:"room_id"`
	Data   json.RawMessage `json:"data"`
}

// JoinData is the payload for join_room / create_room events
type JoinData struct {
	Nickname string `json:"nickname"`
}

// MemberInfo is a single member entry in the member list broadcast
type MemberInfo struct {
	Nickname string `json:"nickname"`
	IsHost   bool   `json:"is_host"`
	IsReady  bool   `json:"is_ready"`
}
