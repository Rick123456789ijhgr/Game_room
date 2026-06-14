package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/olahol/melody"
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
}

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
		members = append(members, MemberInfo{Nickname: nick, IsHost: isHost})
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

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Initialise Melody WebSocket manager
	m := melody.New()

	// --- Melody event handlers ---

	m.HandleConnect(func(s *melody.Session) {
		log.Printf("[WS] Client connected: %s", s.Request.RemoteAddr)
	})

	m.HandleDisconnect(func(s *melody.Session) {
		log.Printf("[WS] Client disconnected: %s", s.Request.RemoteAddr)
		r, hasRoom := s.Get("room")
		if !hasRoom {
			return
		}
		roomID := r.(string)

		// Check if the disconnected session was the host
		isHost := false
		if h, ok := s.Get("is_host"); ok {
			isHost = h.(bool)
		}

		if isHost {
			// Host left → close the entire room for all remaining members
			log.Printf("[WS] Host left room %q — broadcasting room_closed", roomID)
			resp, _ := json.Marshal(Message{
				Event:  "room_closed",
				RoomID: roomID,
				Data:   json.RawMessage(`{}`),
			})
			m.BroadcastFilter(resp, func(other *melody.Session) bool {
				if other == s {
					return false
				}
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == roomID
			})
		} else {
			// Non-host left → just update the member list
			broadcastMemberList(m, roomID)
		}
	})

	m.HandleMessage(func(s *melody.Session, rawMsg []byte) {
		var msg Message
		if err := json.Unmarshal(rawMsg, &msg); err != nil {
			log.Printf("[WS] Bad JSON from %s: %v", s.Request.RemoteAddr, err)
			return
		}
		log.Printf("[WS] event=%q room=%q from=%s", msg.Event, msg.RoomID, s.Request.RemoteAddr)

		switch msg.Event {
		case "create_room":
			// Parse nickname
			var d JoinData
			json.Unmarshal(msg.Data, &d)
			nick := d.Nickname
			if nick == "" {
				nick = "匿名玩家"
			}

			s.Set("room", msg.RoomID)
			s.Set("nickname", nick)
			s.Set("is_host", true)
			log.Printf("[WS] %s created room %q as %q (host)", s.Request.RemoteAddr, msg.RoomID, nick)

			resp, _ := json.Marshal(Message{
				Event:  "room_created",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(`{}`),
			})
			s.Write(resp)

			// Broadcast member list (just self)
			broadcastMemberList(m, msg.RoomID)

		case "join_room":
			// Check if room exists
			exists := false
			sessions, _ := m.Sessions()
			for _, other := range sessions {
				if r, ok := other.Get("room"); ok && r == msg.RoomID {
					exists = true
					break
				}
			}

			if !exists {
				log.Printf("[WS] %s failed to join room %q: room not found", s.Request.RemoteAddr, msg.RoomID)
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: msg.RoomID,
					Data:   json.RawMessage(`{"message":"找不到該房間，請確認房號是否正確"}`),
				})
				s.Write(resp)
				return
			}

			// Parse nickname
			var d JoinData
			json.Unmarshal(msg.Data, &d)
			nick := d.Nickname
			if nick == "" {
				nick = "匿名玩家"
			}

			s.Set("room", msg.RoomID)
			s.Set("nickname", nick)
			s.Set("is_host", false)
			log.Printf("[WS] %s joined room %q as %q", s.Request.RemoteAddr, msg.RoomID, nick)

			// Notify everyone in room that a player joined (with nickname)
			joinData, _ := json.Marshal(map[string]string{"nickname": nick})
			joinResp, _ := json.Marshal(Message{
				Event:  "player_joined",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(joinData),
			})
			m.BroadcastFilter(joinResp, func(other *melody.Session) bool {
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == msg.RoomID
			})

			// Then send updated member list to all
			broadcastMemberList(m, msg.RoomID)

		case "draw":
			// Relay draw event to everyone in the same room, excluding the sender
			senderRoom, ok := s.Get("room")
			if !ok {
				return // sender hasn't joined a room yet
			}
			m.BroadcastFilter(rawMsg, func(other *melody.Session) bool {
				if other == s {
					return false // exclude sender
				}
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == senderRoom
			})

		case "clear":
			// Relay clear-canvas event to same-room peers, excluding sender
			senderRoomC, okC := s.Get("room")
			if !okC {
				return
			}
			m.BroadcastFilter(rawMsg, func(other *melody.Session) bool {
				if other == s {
					return false
				}
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == senderRoomC
			})

		default:
			log.Printf("[WS] Unhandled event: %q", msg.Event)
		}
	})

	// --- HTTP routes ---

	// /ws — WebSocket upgrade endpoint
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if err := m.HandleRequest(w, r); err != nil {
			log.Printf("[WS] HandleRequest error: %v", err)
		}
	})

	// /ping — keep-alive for Render cron job
	http.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "pong")
	})

	// / — serve static files from ./static
	fs := http.FileServer(http.Dir("./static"))
	http.Handle("/", fs)

	// /assets/ — serve assets files from ./assets
	assetsFs := http.FileServer(http.Dir("./assets"))
	http.Handle("/assets/", http.StripPrefix("/assets/", assetsFs))

	addr := ":" + port
	log.Printf("Server starting on http://localhost%s", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
