package server

import (
	"encoding/json"
	"log"
	"math/rand"
	"sync"
	"time"

	"github.com/olahol/melody"
)

// ── Room state (per roomID) ───────────────────────────────────────────────────

type RoomState struct {
	UsedDrawers []string // nicknames that have already drawn this cycle
	TimerCancel chan struct{}
}

var (
	roomsMu sync.Mutex
	rooms   = map[string]*RoomState{}
)

func getRoomState(roomID string) *RoomState {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	if rooms[roomID] == nil {
		rooms[roomID] = &RoomState{}
	}
	return rooms[roomID]
}

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

// startRoundTimer launches a goroutine that:
//  1. Waits `delay` seconds (role-overlay + buffer)
//  2. Broadcasts `round_timer_start` so clients can begin visual countdown
//  3. Counts down `duration` seconds then broadcasts `round_end`
func startRoundTimer(m *melody.Melody, roomID string, duration time.Duration, delay time.Duration) {
	rs := getRoomState(roomID)

	roomsMu.Lock()
	if rs.TimerCancel != nil {
		close(rs.TimerCancel)
	}
	cancel := make(chan struct{})
	rs.TimerCancel = cancel
	roomsMu.Unlock()

	go func() {
		// ── Phase 1: delay before countdown begins ────────────────
		select {
		case <-cancel:
			return
		case <-time.After(delay):
		}

		// Notify clients to start their visual countdown
		timerStartData, _ := json.Marshal(map[string]int{"seconds": int(duration.Seconds())})
		timerStartMsg, _ := json.Marshal(Message{
			Event:  "round_timer_start",
			RoomID: roomID,
			Data:   json.RawMessage(timerStartData),
		})
		broadcastToRoom(m, roomID, timerStartMsg)

		// ── Phase 2: actual countdown ─────────────────────────────
		select {
		case <-cancel:
			return
		case <-time.After(duration):
		}

		// Collect the topic for this round (from any session in the room)
		topic := ""
		sessions, _ := m.Sessions()
		for _, s := range sessions {
			if r, ok := s.Get("room"); !ok || r != roomID {
				continue
			}
			if t, ok := s.Get("current_topic"); ok {
				topic = t.(string)
				break
			}
		}

		roundEndData, _ := json.Marshal(map[string]string{"answer": topic})
		roundEndMsg, _ := json.Marshal(Message{
			Event:  "round_end",
			RoomID: roomID,
			Data:   json.RawMessage(roundEndData),
		})
		broadcastToRoom(m, roomID, roundEndMsg)
		log.Printf("[WS] Round ended for room %q — answer: %q", roomID, topic)
	}()
}

// broadcastToRoom sends a message to all sessions in a room.
func broadcastToRoom(m *melody.Melody, roomID string, data []byte) {
	m.BroadcastFilter(data, func(s *melody.Session) bool {
		r, ok := s.Get("room")
		return ok && r == roomID
	})
}

// pickNextDrawer selects a drawer for the next round, avoiding repeats until
// everyone has had a turn.
func pickNextDrawer(m *melody.Melody, roomID string) *melody.Session {
	rs := getRoomState(roomID)

	sessions, _ := m.Sessions()
	var inRoom []*melody.Session
	for _, s := range sessions {
		if r, ok := s.Get("room"); ok && r == roomID {
			inRoom = append(inRoom, s)
		}
	}
	if len(inRoom) == 0 {
		return nil
	}

	// Build set of already-used drawers
	used := map[string]bool{}
	roomsMu.Lock()
	for _, n := range rs.UsedDrawers {
		used[n] = true
	}
	roomsMu.Unlock()

	// Candidates: players NOT yet used
	var candidates []*melody.Session
	for _, s := range inRoom {
		nick := ""
		if n, ok := s.Get("nickname"); ok {
			nick = n.(string)
		}
		if !used[nick] {
			candidates = append(candidates, s)
		}
	}

	// If everyone has drawn, reset the queue
	if len(candidates) == 0 {
		roomsMu.Lock()
		rs.UsedDrawers = nil
		roomsMu.Unlock()
		candidates = inRoom
	}

	chosen := candidates[rand.Intn(len(candidates))]

	// Record chosen drawer
	chosenNick := ""
	if n, ok := chosen.Get("nickname"); ok {
		chosenNick = n.(string)
	}
	roomsMu.Lock()
	rs.UsedDrawers = append(rs.UsedDrawers, chosenNick)
	roomsMu.Unlock()

	return chosen
}

// ── WebSocket handler setup ───────────────────────────────────────────────────

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
			// Check if room exists and if game is in progress
			exists := false
			isPlaying := false
			sessions, _ := m.Sessions()
			for _, other := range sessions {
				if r, ok := other.Get("room"); ok && r == msg.RoomID {
					exists = true
					if p, ok := other.Get("is_playing"); ok && p.(bool) {
						isPlaying = true
					}
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

			if isPlaying {
				log.Printf("[WS] %s failed to join room %q: game in progress", s.Request.RemoteAddr, msg.RoomID)
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: msg.RoomID,
					Data:   json.RawMessage(`{"message":"遊戲已經開始，無法加入房間"}`),
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

			// Reset room state (new game, fresh drawer queue)
			roomsMu.Lock()
			rooms[roomID] = &RoomState{}
			roomsMu.Unlock()

			startRound(m, roomID)

		case "next_round":
			// Only host triggers this (after round_end overlay)
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
			log.Printf("[WS] next_round requested for room %q", roomID)
			startRound(m, roomID)

		case "guess":
			senderRoom, ok := s.Get("room")
			if !ok {
				return
			}

			var guessData struct {
				Guess string `json:"guess"`
			}
			if err := json.Unmarshal(msg.Data, &guessData); err != nil {
				return
			}

			topic, ok := s.Get("current_topic")
			if !ok {
				return
			}

			correct := (guessData.Guess == topic.(string))

			nick := "匿名玩家"
			if n, ok := s.Get("nickname"); ok && n != "" {
				nick = n.(string)
			}

			respData, _ := json.Marshal(map[string]interface{}{
				"correct":  correct,
				"guess":    guessData.Guess,
				"nickname": nick,
			})

			resp, _ := json.Marshal(Message{
				Event:  "guess_result",
				RoomID: senderRoom.(string),
				Data:   json.RawMessage(respData),
			})

			// Send to sender (guesser)
			s.Write(resp)

			// Send to the drawer in the same room
			m.BroadcastFilter(resp, func(other *melody.Session) bool {
				if other == s {
					return false
				}
				otherRoom, hasRoom := other.Get("room")
				if !hasRoom || otherRoom != senderRoom {
					return false
				}
				isDrawer, hasRole := other.Get("is_drawer")
				return hasRole && isDrawer.(bool)
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

// startRound picks the next drawer, broadcasts game_start to all, then
// launches the countdown timer (5 s delay + 30 s round).
func startRound(m *melody.Melody, roomID string) {
	drawer := pickNextDrawer(m, roomID)
	if drawer == nil {
		return
	}

	topic := GetRandomTopic()

	sessions, _ := m.Sessions()
	for _, target := range sessions {
		if r, ok := target.Get("room"); !ok || r != roomID {
			continue
		}
		isDrawer := (target == drawer)
		target.Set("is_drawer", isDrawer)
		target.Set("current_topic", topic)
		target.Set("is_playing", true)

		role := "guesser"
		sentTopic := ""
		drawerNick := ""
		if n, ok := drawer.Get("nickname"); ok {
			drawerNick = n.(string)
		}
		if isDrawer {
			role = "drawer"
			sentTopic = topic
		}

		dataBytes, _ := json.Marshal(map[string]string{
			"role":        role,
			"topic":       sentTopic,
			"drawer_nick": drawerNick,
		})
		resp, _ := json.Marshal(Message{
			Event:  "game_start",
			RoomID: roomID,
			Data:   json.RawMessage(dataBytes),
		})
		target.Write(resp)
	}

	// 5 s role overlay + 1 s buffer = 6 s delay before countdown begins
	// Then 30 s for the round itself
	startRoundTimer(m, roomID, 30*time.Second, 6*time.Second)
	log.Printf("[WS] Round started in room %q — drawer: %s, topic: %s", roomID, func() string {
		if n, ok := drawer.Get("nickname"); ok {
			return n.(string)
		}
		return "?"
	}(), topic)
}
