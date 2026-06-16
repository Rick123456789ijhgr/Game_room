package server

import (
	"encoding/json"
	"github.com/olahol/melody"
)

// buildMemberList collects all sessions in a given room and returns their member info.
func buildMemberList(m *melody.Melody, roomID string) []MemberInfo {
	sessions, _ := m.Sessions()
	var members []MemberInfo
	for _, s := range sessions {
		r, ok := s.Get("room")
		if !ok || r != roomID {
			continue
		}
		nick := "匿名玩家"
		if n, ok := s.Get("nickname"); ok && n != "" {
			nick = n.(string)
		}
		isHost := false
		if h, ok := s.Get("is_host"); ok {
			isHost = h.(bool)
		}
		isReady := false
		if rd, ok := s.Get("is_ready"); ok {
			isReady = rd.(bool)
		}
		clientID := ""
		if cid, ok := s.Get("client_id"); ok {
			clientID = cid.(string)
		}
		members = append(members, MemberInfo{Nickname: nick, IsHost: isHost, IsReady: isReady, ClientID: clientID})
	}
	return members
}

// broadcastMemberList sends the current member list to everyone in the room.
func broadcastMemberList(m *melody.Melody, roomID string) {
	members := buildMemberList(m, roomID)
	data, _ := json.Marshal(members)
	resp, _ := json.Marshal(Message{
		Event:  "member_list",
		RoomID: roomID,
		Data:   json.RawMessage(data),
	})
	m.BroadcastFilter(resp, func(other *melody.Session) bool {
		r, ok := other.Get("room")
		return ok && r == roomID
	})
}

// allReady returns true if every non-host session in the room has is_ready = true.
func allReady(m *melody.Melody, roomID string) bool {
	sessions, _ := m.Sessions()
	for _, s := range sessions {
		r, ok := s.Get("room")
		if !ok || r != roomID {
			continue
		}
		// Host is always considered ready
		if h, ok := s.Get("is_host"); ok && h.(bool) {
			continue
		}
		rd, ok := s.Get("is_ready")
		if !ok || !rd.(bool) {
			return false
		}
	}
	return true
}
