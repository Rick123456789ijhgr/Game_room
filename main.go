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
			// User explicitly wants to create a room.
			s.Set("room", msg.RoomID)
			log.Printf("[WS] %s created room %q", s.Request.RemoteAddr, msg.RoomID)
			
			resp, _ := json.Marshal(Message{
				Event:  "room_created",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(`{}`),
			})
			s.Write(resp)

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

			// Bind this session to the given room
			s.Set("room", msg.RoomID)
			log.Printf("[WS] %s joined room %q", s.Request.RemoteAddr, msg.RoomID)

			// Broadcast player_joined to everyone in the same room (including self)
			resp, _ := json.Marshal(Message{
				Event:  "player_joined",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(`{}`),
			})
			m.BroadcastFilter(resp, func(other *melody.Session) bool {
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == msg.RoomID
			})

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
