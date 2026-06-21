package server

import (
	"encoding/json"
	"sync"

	"github.com/olahol/melody"
)

// ── Room state (per roomID) ───────────────────────────────────────────────────

type RoomState struct {
	UsedDrawers []string   // nicknames that have already drawn this cycle
	TimerCancel chan struct{}
	TargetScore int        // win condition (default 10)
	IsOvertime  bool       // true when we're in overtime mode
}

var (
	roomsMu sync.Mutex
	rooms   = map[string]*RoomState{}
)

// getRoomState returns (or creates) the RoomState for a room.
func getRoomState(roomID string) *RoomState {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	if rooms[roomID] == nil {
		rooms[roomID] = &RoomState{TargetScore: 10}
	}
	return rooms[roomID]
}

// deleteRoomState removes a room from the map.
func deleteRoomState(roomID string) {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	delete(rooms, roomID)
}

// cancelRoomTimer stops any active countdown for the room.
func cancelRoomTimer(roomID string) {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	if rs, ok := rooms[roomID]; ok && rs.TimerCancel != nil {
		close(rs.TimerCancel)
		rs.TimerCancel = nil
	}
}

// broadcastToRoom sends a message to all sessions in a room.
func broadcastToRoom(m *melody.Melody, roomID string, data []byte) {
	m.BroadcastFilter(data, func(s *melody.Session) bool {
		r, ok := s.Get("room")
		return ok && r == roomID
	})
}

// sendError is a small helper that writes a single-recipient error event.
func sendError(s *melody.Session, roomID, message string) {
	data, _ := json.Marshal(map[string]string{"message": message})
	resp, _ := json.Marshal(Message{
		Event:  "error",
		RoomID: roomID,
		Data:   data,
	})
	s.Write(resp)
}
