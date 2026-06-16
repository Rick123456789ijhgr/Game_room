package server

import (
	"encoding/json"
	"log"
	"math/rand"

	"github.com/olahol/melody"
)

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
			s.Set("is_ready", true) // Host is always ready
			s.Set("client_id", msg.ClientID)
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
			s.Set("is_ready", false) // Non-host starts as not ready
			s.Set("client_id", msg.ClientID)
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

		case "player_ready":
			// Toggle is_ready for sender
			senderRoom, ok := s.Get("room")
			if !ok {
				return
			}
			// Host cannot toggle (always ready)
			if h, ok := s.Get("is_host"); ok && h.(bool) {
				return
			}
			current := false
			if rd, ok := s.Get("is_ready"); ok {
				current = rd.(bool)
			}
			s.Set("is_ready", !current)
			log.Printf("[WS] %s toggled ready → %v in room %q", s.Request.RemoteAddr, !current, senderRoom)
			broadcastMemberList(m, senderRoom.(string))

		case "kick_player":
			// Only host can kick
			isHost := false
			if h, ok := s.Get("is_host"); ok {
				isHost = h.(bool)
			}
			if !isHost {
				return
			}
			senderRoom, ok := s.Get("room")
			if !ok {
				return
			}
			// Parse target nickname
			var kd struct {
				Nickname string `json:"nickname"`
			}
			json.Unmarshal(msg.Data, &kd)

			// Find and close the target session
			sessions, _ := m.Sessions()
			for _, target := range sessions {
				if target == s {
					continue // host cannot kick themselves
				}
				r, ok := target.Get("room")
				if !ok || r != senderRoom {
					continue
				}
				tn, ok := target.Get("nickname")
				if !ok || tn.(string) != kd.Nickname {
					continue
				}
				// Send kicked event to target
				kickedMsg, _ := json.Marshal(Message{
					Event:  "kicked",
					RoomID: senderRoom.(string),
					Data:   json.RawMessage(`{}`),
				})
				target.Write(kickedMsg)
				target.Close()
				log.Printf("[WS] Host kicked %q from room %q", kd.Nickname, senderRoom)
				break
			}
			broadcastMemberList(m, senderRoom.(string))

		case "start_game":
			// Only host can start
			isHost := false
			if h, ok := s.Get("is_host"); ok {
				isHost = h.(bool)
			}
			if !isHost {
				return
			}
			senderRoom, ok := s.Get("room")
			if !ok {
				return
			}
			roomID := senderRoom.(string)

			// Verify all players are ready
			if !allReady(m, roomID) {
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: roomID,
					Data:   json.RawMessage(`{"message":"還有玩家尚未準備好"}`),
				})
				s.Write(resp)
				return
			}

			log.Printf("[WS] Game starting in room %q", roomID)
			
			// Find all sessions in the room
			sessionsInRoom := []*melody.Session{}
			sessions, _ := m.Sessions()
			for _, other := range sessions {
				if r, ok := other.Get("room"); ok && r == roomID {
					sessionsInRoom = append(sessionsInRoom, other)
				}
			}

			if len(sessionsInRoom) == 0 {
				return
			}

			// Select a random drawer and topic
			drawerIdx := rand.Intn(len(sessionsInRoom))
			topic := GetRandomTopic()

			for i, target := range sessionsInRoom {
				isDrawer := (i == drawerIdx)
				target.Set("is_drawer", isDrawer)
				target.Set("current_topic", topic)
				
				role := "guesser"
				sentTopic := ""
				if isDrawer {
					role = "drawer"
					sentTopic = topic
				}

				dataBytes, _ := json.Marshal(map[string]string{
					"role":  role,
					"topic": sentTopic,
				})
				resp, _ := json.Marshal(Message{
					Event:  "game_start",
					RoomID: roomID,
					Data:   json.RawMessage(dataBytes),
				})
				target.Write(resp)
			}

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

		case "chat":
			// Relay chat message to everyone in the same room (including sender)
			senderRoomChat, okChat := s.Get("room")
			if !okChat {
				return
			}
			// Inject sender's nickname into the message so recipients know who sent it
			nick := "匿名玩家"
			if n, ok := s.Get("nickname"); ok && n != "" {
				nick = n.(string)
			}
			// Rebuild message with nickname embedded in data
			type ChatData struct {
				Text     string `json:"text"`
				Nickname string `json:"nickname"`
			}
			var cd ChatData
			json.Unmarshal(msg.Data, &cd)
			cd.Nickname = nick
			newData, _ := json.Marshal(cd)
			outMsg, _ := json.Marshal(Message{
				Event:  "chat",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(newData),
			})
			m.BroadcastFilter(outMsg, func(other *melody.Session) bool {
				otherRoom, exists := other.Get("room")
				return exists && otherRoom == senderRoomChat
			})

		default:
			log.Printf("[WS] Unhandled event: %q", msg.Event)
		}
	})
}
