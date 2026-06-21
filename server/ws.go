package server

import (
	"encoding/json"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/olahol/melody"
)

// ── Room state (per roomID) ───────────────────────────────────────────────────

type RoomState struct {
	UsedDrawers []string       // nicknames that have already drawn this cycle
	TimerCancel chan struct{}
	TargetScore int            // win condition (default 10)
	IsOvertime  bool           // true when we're in overtime mode
}

var (
	roomsMu sync.Mutex
	rooms   = map[string]*RoomState{}
)

func getRoomState(roomID string) *RoomState {
	roomsMu.Lock()
	defer roomsMu.Unlock()
	if rooms[roomID] == nil {
		rooms[roomID] = &RoomState{TargetScore: 10}
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

		// Build scores snapshot to include in round_end
		scores := buildScores(m, roomID)
		roundEndData, _ := json.Marshal(map[string]interface{}{
			"answer": topic,
			"scores": scores,
		})
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

// buildScores returns a map of nickname → score for all players in the room.
func buildScores(m *melody.Melody, roomID string) map[string]int {
	result := map[string]int{}
	sessions, _ := m.Sessions()
	for _, s := range sessions {
		if r, ok := s.Get("room"); !ok || r != roomID {
			continue
		}
		nick := "匿名玩家"
		if n, ok := s.Get("nickname"); ok && n != "" {
			nick = n.(string)
		}
		score := 0
		if sc, ok := s.Get("score"); ok {
			score = sc.(int)
		}
		result[nick] = score
	}
	return result
}

// checkWinCondition checks if any player has reached the target score.
// Returns (winner nickname, isOvertime, shouldContinue).
// - winner != "" → someone won
// - shouldContinue → tied or no winner yet
func checkWinCondition(m *melody.Melody, roomID string) (winner string, isTied bool) {
	rs := getRoomState(roomID)

	roomsMu.Lock()
	target := rs.TargetScore
	roomsMu.Unlock()

	scores := buildScores(m, roomID)

	maxScore := 0
	for _, sc := range scores {
		if sc > maxScore {
			maxScore = sc
		}
	}

	if maxScore < target {
		return "", false // no one reached target yet
	}

	// Find all players at max score
	var leaders []string
	for nick, sc := range scores {
		if sc == maxScore {
			leaders = append(leaders, nick)
		}
	}

	if len(leaders) == 1 {
		return leaders[0], false // clear winner
	}
	// Multiple players tied at the target → overtime
	return "", true
}

// broadcastScores sends the current scoreboard to all players in the room.
func broadcastScores(m *melody.Melody, roomID string) {
	scores := buildScores(m, roomID)
	data, _ := json.Marshal(scores)
	msg, _ := json.Marshal(Message{
		Event:  "score_update",
		RoomID: roomID,
		Data:   json.RawMessage(data),
	})
	broadcastToRoom(m, roomID, msg)
}

// ── Rate Limiting & Profanity Filter ─────────────────────────────────────────

var profanityList = []string{"幹", "靠杯", "沙小", "機掰", "屌", "媽的", "fuck", "shit", "bitch"}

// censorMessage replaces bad words with ***
func censorMessage(text string) string {
	lowerText := strings.ToLower(text)
	for _, word := range profanityList {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			// Basic case-insensitive replace by looking up the lowercased word
			// Since Go's strings.ReplaceAll is case-sensitive, we can do a naive 
			// case-insensitive replace by using a regex, or simply replace the original text using strings.ReplaceAll for the exact word.
			// Let's use simple ReplaceAll for exact matched words for simplicity.
			text = strings.ReplaceAll(text, word, "***")
		}
	}
	return text
}

// containsProfanity checks if the text contains any bad words
func containsProfanity(text string) bool {
	lowerText := strings.ToLower(text)
	for _, word := range profanityList {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

// checkRateLimit returns true if the user is sending messages too fast
func checkRateLimit(s *melody.Session) bool {
	lastMsgTime, ok := s.Get("last_msg_time")
	now := time.Now()
	if ok {
		if now.Sub(lastMsgTime.(time.Time)) < 1*time.Second {
			return false // Rate limited
		}
	}
	s.Set("last_msg_time", now)
	return true
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
			s.Set("score", 0)
			log.Printf("[WS] %s created room %q as %q (host)", s.Request.RemoteAddr, msg.RoomID, nick)

			// Ensure room state exists with default target score
			getRoomState(msg.RoomID)

			resp, _ := json.Marshal(Message{
				Event:  "room_created",
				RoomID: msg.RoomID,
				Data:   json.RawMessage(`{}`),
			})
			s.Write(resp)

			// Broadcast member list (just self)
			broadcastMemberList(m, msg.RoomID)
			// Send current room settings to the creator
			broadcastRoomSettings(m, msg.RoomID)

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
			s.Set("score", 0)
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
			// Send current room settings to the new joiner
			broadcastRoomSettings(m, msg.RoomID)

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

		case "set_room_settings":
			// Only host can change settings
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

			var settingsData struct {
				TargetScore int `json:"target_score"`
			}
			if err := json.Unmarshal(msg.Data, &settingsData); err != nil {
				return
			}
			if settingsData.TargetScore < 1 {
				settingsData.TargetScore = 1
			}
			if settingsData.TargetScore > 100 {
				settingsData.TargetScore = 100
			}

			rs := getRoomState(roomID)
			roomsMu.Lock()
			rs.TargetScore = settingsData.TargetScore
			roomsMu.Unlock()

			log.Printf("[WS] Room %q target score set to %d", roomID, settingsData.TargetScore)
			broadcastRoomSettings(m, roomID)

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

			// Reset scores for all players in the room
			sessions, _ := m.Sessions()
			for _, sess := range sessions {
				if r, ok := sess.Get("room"); ok && r == roomID {
					sess.Set("score", 0)
				}
			}

			// Reset room state but preserve target score
			rs := getRoomState(roomID)
			roomsMu.Lock()
			target := rs.TargetScore
			rooms[roomID] = &RoomState{TargetScore: target}
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

			// Prevent drawer or already correct guessers from guessing
			if isDrawer, ok := s.Get("is_drawer"); ok && isDrawer.(bool) {
				return
			}
			if guessed, ok := s.Get("guessed_correctly"); ok && guessed.(bool) {
				return
			}

			var guessData struct {
				Guess string `json:"guess"`
			}
			if err := json.Unmarshal(msg.Data, &guessData); err != nil {
				return
			}

			// ── Rate Limiting ──
			if !checkRateLimit(s) {
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: senderRoom.(string),
					Data:   json.RawMessage(`{"message":"發送太快了，請稍後再試！"}`),
				})
				s.Write(resp)
				return
			}

			// ── Profanity Filter ──
			if containsProfanity(guessData.Guess) {
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: senderRoom.(string),
					Data:   json.RawMessage(`{"message":"您的猜測包含不雅字詞，請重新輸入！"}`),
				})
				s.Write(resp)
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

			// Update score if correct
			newScore := 0
			if correct {
				curScore := 0
				if sc, ok := s.Get("score"); ok {
					curScore = sc.(int)
				}
				newScore = curScore + 1
				s.Set("score", newScore)
				s.Set("guessed_correctly", true)
				log.Printf("[WS] %q scored! New score: %d in room %q", nick, newScore, senderRoom)
			}

			respData, _ := json.Marshal(map[string]interface{}{
				"correct":   correct,
				"guess":     guessData.Guess,
				"nickname":  nick,
				"new_score": newScore,
			})

			resp, _ := json.Marshal(Message{
				Event:  "guess_result",
				RoomID: senderRoom.(string),
				Data:   json.RawMessage(respData),
			})

			// Send to sender (guesser)
			s.Write(resp)

			// Send to everyone else in room (for guess history visibility)
			m.BroadcastFilter(resp, func(other *melody.Session) bool {
				if other == s {
					return false
				}
				otherRoom, hasRoom := other.Get("room")
				return hasRoom && otherRoom == senderRoom
			})

			// Broadcast updated scores to all
			if correct {
				// Handle drawer scoring & early termination
				sessions, _ := m.Sessions()
				var drawerSess *melody.Session
				totalGuessers := 0
				correctGuessers := 0

				for _, sess := range sessions {
					if r, ok := sess.Get("room"); ok && r == senderRoom {
						if isD, _ := sess.Get("is_drawer"); isD != nil && isD.(bool) {
							drawerSess = sess
						} else {
							totalGuessers++
							if gc, _ := sess.Get("guessed_correctly"); gc != nil && gc.(bool) {
								correctGuessers++
							}
						}
					}
				}

				if drawerSess != nil {
					awardedFirst, _ := drawerSess.Get("drawer_awarded_first")
					if awardedFirst != nil && !awardedFirst.(bool) {
						// Drawer gets 1 pt for first correct guess
						curDrawerScore := 0
						if sc, ok := drawerSess.Get("score"); ok {
							curDrawerScore = sc.(int)
						}
						drawerSess.Set("score", curDrawerScore+1)
						drawerSess.Set("drawer_awarded_first", true)
						log.Printf("[WS] Drawer awarded 1 pt for first correct guess in room %q", senderRoom)
					}

					// Perfect score check
					if correctGuessers == totalGuessers && totalGuessers > 0 {
						// Drawer gets another point
						curDrawerScore := 0
						if sc, ok := drawerSess.Get("score"); ok {
							curDrawerScore = sc.(int)
						}
						drawerSess.Set("score", curDrawerScore+1)
						log.Printf("[WS] Drawer awarded 1 extra pt for PERFECT round in room %q", senderRoom)
					}
				}

				broadcastScores(m, senderRoom.(string))

				// Check win condition
				winner, isTied := checkWinCondition(m, senderRoom.(string))
				if winner != "" {
					// Clear winner found → game over
					cancelRoomTimer(senderRoom.(string))
					broadcastGameOver(m, senderRoom.(string), winner, false)
				} else if isTied {
					// Tied → cancel current timer and signal overtime
					cancelRoomTimer(senderRoom.(string))
					rs := getRoomState(senderRoom.(string))
					roomsMu.Lock()
					rs.IsOvertime = true
					roomsMu.Unlock()
					broadcastGameOver(m, senderRoom.(string), "", true)
				} else if correctGuessers == totalGuessers && totalGuessers > 0 {
					// Early termination
					cancelRoomTimer(senderRoom.(string))
					// Broadcast round_end manually
					scores := buildScores(m, senderRoom.(string))
					roundEndData, _ := json.Marshal(map[string]interface{}{
						"answer": topic.(string),
						"scores": scores,
					})
					roundEndMsg, _ := json.Marshal(Message{
						Event:  "round_end",
						RoomID: senderRoom.(string),
						Data:   json.RawMessage(roundEndData),
					})
					broadcastToRoom(m, senderRoom.(string), roundEndMsg)
					log.Printf("[WS] Round ended early for room %q — all guessers correct!", senderRoom)
				}
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

			// ── Rate Limiting ──
			if !checkRateLimit(s) {
				resp, _ := json.Marshal(Message{
					Event:  "error",
					RoomID: senderRoomChat.(string),
					Data:   json.RawMessage(`{"message":"發送太快了，請稍後再試！"}`),
				})
				s.Write(resp)
				return
			}

			type ChatData struct {
				Text     string `json:"text"`
				Nickname string `json:"nickname"`
			}
			var cd ChatData
			if err := json.Unmarshal(msg.Data, &cd); err != nil {
				return
			}

			// ── Answer Leakage Prevention ──
			if topic, ok := s.Get("current_topic"); ok && topic != "" && topic != nil {
				if strings.Contains(strings.ToLower(cd.Text), strings.ToLower(topic.(string))) {
					resp, _ := json.Marshal(Message{
						Event:  "error",
						RoomID: senderRoomChat.(string),
						Data:   json.RawMessage(`{"message":"🤫 請不要在聊天室洩露答案！"}`),
					})
					s.Write(resp)
					return
				}
			}

			// ── Profanity Filter ──
			cd.Text = censorMessage(cd.Text)

			// Inject sender's nickname into the message so recipients know who sent it
			nick := "匿名玩家"
			if n, ok := s.Get("nickname"); ok && n != "" {
				nick = n.(string)
			}

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

// broadcastRoomSettings sends current room settings to all players in the room.
func broadcastRoomSettings(m *melody.Melody, roomID string) {
	rs := getRoomState(roomID)
	roomsMu.Lock()
	target := rs.TargetScore
	roomsMu.Unlock()

	data, _ := json.Marshal(map[string]int{"target_score": target})
	msg, _ := json.Marshal(Message{
		Event:  "room_settings",
		RoomID: roomID,
		Data:   json.RawMessage(data),
	})
	broadcastToRoom(m, roomID, msg)
}

// broadcastGameOver sends a game_over event to all players.
// winner="" + overtime=true means tied overtime round.
func broadcastGameOver(m *melody.Melody, roomID string, winner string, overtime bool) {
	scores := buildScores(m, roomID)
	data, _ := json.Marshal(map[string]interface{}{
		"winner":   winner,
		"overtime": overtime,
		"scores":   scores,
	})
	msg, _ := json.Marshal(Message{
		Event:  "game_over",
		RoomID: roomID,
		Data:   json.RawMessage(data),
	})
	broadcastToRoom(m, roomID, msg)
	log.Printf("[WS] Game over in room %q — winner: %q overtime: %v", roomID, winner, overtime)
}

// startRound picks the next drawer, broadcasts game_start to all, then
// launches the countdown timer (5 s delay + 30 s round).
func startRound(m *melody.Melody, roomID string) {
	drawer := pickNextDrawer(m, roomID)
	if drawer == nil {
		return
	}

	topic := GetRandomTopic()

	// Fetch current scores to include in game_start (so clients can display scoreboard)
	scores := buildScores(m, roomID)

	// Check if this is overtime
	rs := getRoomState(roomID)
	roomsMu.Lock()
	isOvertime := rs.IsOvertime
	target := rs.TargetScore
	roomsMu.Unlock()

	sessions, _ := m.Sessions()
	for _, target_sess := range sessions {
		if r, ok := target_sess.Get("room"); !ok || r != roomID {
			continue
		}
		isDrawer := (target_sess == drawer)
		target_sess.Set("is_drawer", isDrawer)
		target_sess.Set("current_topic", topic)
		target_sess.Set("is_playing", true)
		target_sess.Set("guessed_correctly", false)

		if isDrawer {
			target_sess.Set("drawer_awarded_first", false)
		}

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

		dataBytes, _ := json.Marshal(map[string]interface{}{
			"role":         role,
			"topic":        sentTopic,
			"drawer_nick":  drawerNick,
			"scores":       scores,
			"target_score": target,
			"overtime":     isOvertime,
		})
		resp, _ := json.Marshal(Message{
			Event:  "game_start",
			RoomID: roomID,
			Data:   json.RawMessage(dataBytes),
		})
		target_sess.Write(resp)
	}

	// 5 s role overlay + 1 s buffer = 6 s delay before countdown begins
	// Then 30 s for the round itself
	startRoundTimer(m, roomID, 30*time.Second, 6*time.Second)
	log.Printf("[WS] Round started in room %q — drawer: %s, topic: %s, overtime: %v", roomID, func() string {
		if n, ok := drawer.Get("nickname"); ok {
			return n.(string)
		}
		return "?"
	}(), topic, isOvertime)
}
