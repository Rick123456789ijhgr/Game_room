package server

import (
	"encoding/json"
	"log"

	"github.com/olahol/melody"
)

// SetupWebSockets registers all Melody WebSocket lifecycle hooks and routes
// incoming events to their dedicated handler functions.
func SetupWebSockets(m *melody.Melody) {
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

		isHost := false
		if h, ok := s.Get("is_host"); ok {
			isHost = h.(bool)
		}

		if isHost {
			// Host left → cancel timer and close the entire room
			cancelRoomTimer(roomID)
			deleteRoomState(roomID)
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
			handleCreateRoom(s, msg, m)
		case "join_room":
			handleJoinRoom(s, msg, m)
		case "player_ready":
			handlePlayerReady(s, msg, m)
		case "set_room_settings":
			handleSetRoomSettings(s, msg, m)
		case "kick_player":
			handleKickPlayer(s, msg, m)
		case "start_game":
			handleStartGame(s, msg, m)
		case "next_round":
			handleNextRound(s, msg, m)
		case "guess":
			handleGuess(s, msg, m)
		case "draw":
			handleDraw(s, rawMsg, m)
		case "clear":
			handleClear(s, rawMsg, m)
		case "chat":
			handleChat(s, msg, m)
		default:
			log.Printf("[WS] Unhandled event: %q", msg.Event)
		}
	})
}
