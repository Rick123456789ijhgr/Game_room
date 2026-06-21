package server

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/olahol/melody"
)

// ── Room management handlers ──────────────────────────────────────────────────

func handleCreateRoom(s *melody.Session, msg Message, m *melody.Melody) {
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

	broadcastMemberList(m, msg.RoomID)
	broadcastRoomSettings(m, msg.RoomID)
}

func handleJoinRoom(s *melody.Session, msg Message, m *melody.Melody) {
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
		sendError(s, msg.RoomID, "找不到該房間，請確認房號是否正確")
		return
	}
	if isPlaying {
		log.Printf("[WS] %s failed to join room %q: game in progress", s.Request.RemoteAddr, msg.RoomID)
		sendError(s, msg.RoomID, "遊戲已經開始，無法加入房間")
		return
	}

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

	// Notify everyone in room that a player joined
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

	broadcastMemberList(m, msg.RoomID)
	broadcastRoomSettings(m, msg.RoomID)
}

func handlePlayerReady(s *melody.Session, msg Message, m *melody.Melody) {
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
}

func handleSetRoomSettings(s *melody.Session, msg Message, m *melody.Melody) {
	if h, ok := s.Get("is_host"); !ok || !h.(bool) {
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
}

func handleKickPlayer(s *melody.Session, msg Message, m *melody.Melody) {
	if h, ok := s.Get("is_host"); !ok || !h.(bool) {
		return
	}
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}

	var kd struct {
		Nickname string `json:"nickname"`
	}
	json.Unmarshal(msg.Data, &kd)

	sessions, _ := m.Sessions()
	for _, target := range sessions {
		if target == s {
			continue
		}
		r, ok := target.Get("room")
		if !ok || r != senderRoom {
			continue
		}
		tn, ok := target.Get("nickname")
		if !ok || tn.(string) != kd.Nickname {
			continue
		}
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
}

// ── Game flow handlers ────────────────────────────────────────────────────────

func handleStartGame(s *melody.Session, msg Message, m *melody.Melody) {
	if h, ok := s.Get("is_host"); !ok || !h.(bool) {
		return
	}
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	roomID := senderRoom.(string)

	if !allReady(m, roomID) {
		sendError(s, roomID, "還有玩家尚未準備好")
		return
	}

	log.Printf("[WS] Game starting in room %q", roomID)

	// Reset scores for all players
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
}

func handleNextRound(s *melody.Session, msg Message, m *melody.Melody) {
	if h, ok := s.Get("is_host"); !ok || !h.(bool) {
		return
	}
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	roomID := senderRoom.(string)
	log.Printf("[WS] next_round requested for room %q", roomID)
	startRound(m, roomID)
}

// ── Gameplay handlers ─────────────────────────────────────────────────────────

func handleGuess(s *melody.Session, msg Message, m *melody.Melody) {
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	roomID := senderRoom.(string)

	// Prevent drawer or already-correct guessers from guessing
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
		sendError(s, roomID, "發送太快了，請稍後再試！")
		return
	}

	// ── Profanity Filter ──
	if containsProfanity(guessData.Guess) {
		sendError(s, roomID, "您的猜測包含不雅字詞，請重新輸入！")
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

	newScore := 0
	if correct {
		curScore := 0
		if sc, ok := s.Get("score"); ok {
			curScore = sc.(int)
		}
		newScore = curScore + 1
		s.Set("score", newScore)
		s.Set("guessed_correctly", true)
		log.Printf("[WS] %q scored! New score: %d in room %q", nick, newScore, roomID)
	}

	respData, _ := json.Marshal(map[string]interface{}{
		"correct":   correct,
		"guess":     guessData.Guess,
		"nickname":  nick,
		"new_score": newScore,
	})
	resp, _ := json.Marshal(Message{
		Event:  "guess_result",
		RoomID: roomID,
		Data:   json.RawMessage(respData),
	})

	// Send to sender
	s.Write(resp)
	// Broadcast to rest of room for guess history visibility
	m.BroadcastFilter(resp, func(other *melody.Session) bool {
		if other == s {
			return false
		}
		otherRoom, hasRoom := other.Get("room")
		return hasRoom && otherRoom == senderRoom
	})

	if !correct {
		return
	}

	// ── Post-correct: drawer scoring + early termination check ──
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
		// First correct guess → drawer gets 1 pt
		if awardedFirst, _ := drawerSess.Get("drawer_awarded_first"); awardedFirst != nil && !awardedFirst.(bool) {
			curDrawerScore := 0
			if sc, ok := drawerSess.Get("score"); ok {
				curDrawerScore = sc.(int)
			}
			drawerSess.Set("score", curDrawerScore+1)
			drawerSess.Set("drawer_awarded_first", true)
			log.Printf("[WS] Drawer awarded 1 pt for first correct guess in room %q", roomID)
		}

		// Perfect round → drawer gets 1 extra pt
		if correctGuessers == totalGuessers && totalGuessers > 0 {
			curDrawerScore := 0
			if sc, ok := drawerSess.Get("score"); ok {
				curDrawerScore = sc.(int)
			}
			drawerSess.Set("score", curDrawerScore+1)
			log.Printf("[WS] Drawer awarded 1 extra pt for PERFECT round in room %q", roomID)
		}
	}

	broadcastScores(m, roomID)

	// Check win condition
	winner, isTied := checkWinCondition(m, roomID)
	if winner != "" {
		cancelRoomTimer(roomID)
		broadcastGameOver(m, roomID, winner, false)
	} else if isTied {
		cancelRoomTimer(roomID)
		rs := getRoomState(roomID)
		roomsMu.Lock()
		rs.IsOvertime = true
		roomsMu.Unlock()
		broadcastGameOver(m, roomID, "", true)
	} else if correctGuessers == totalGuessers && totalGuessers > 0 {
		// Early termination: everyone guessed correctly
		cancelRoomTimer(roomID)
		scores := buildScores(m, roomID)
		roundEndData, _ := json.Marshal(map[string]interface{}{
			"answer": topic.(string),
			"scores": scores,
		})
		roundEndMsg, _ := json.Marshal(Message{
			Event:  "round_end",
			RoomID: roomID,
			Data:   json.RawMessage(roundEndData),
		})
		broadcastToRoom(m, roomID, roundEndMsg)
		log.Printf("[WS] Round ended early for room %q — all guessers correct!", roomID)
	}
}

func handleDraw(s *melody.Session, rawMsg []byte, m *melody.Melody) {
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	m.BroadcastFilter(rawMsg, func(other *melody.Session) bool {
		if other == s {
			return false
		}
		otherRoom, exists := other.Get("room")
		return exists && otherRoom == senderRoom
	})
}

func handleClear(s *melody.Session, rawMsg []byte, m *melody.Melody) {
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	m.BroadcastFilter(rawMsg, func(other *melody.Session) bool {
		if other == s {
			return false
		}
		otherRoom, exists := other.Get("room")
		return exists && otherRoom == senderRoom
	})
}

func handleChat(s *melody.Session, msg Message, m *melody.Melody) {
	senderRoom, ok := s.Get("room")
	if !ok {
		return
	}
	roomID := senderRoom.(string)

	// ── Rate Limiting ──
	if !checkRateLimit(s) {
		sendError(s, roomID, "發送太快了，請稍後再試！")
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
	if topic, ok := s.Get("current_topic"); ok && topic != nil && topic != "" {
		if strings.Contains(strings.ToLower(cd.Text), strings.ToLower(topic.(string))) {
			sendError(s, roomID, "🤫 請不要在聊天室洩露答案！")
			return
		}
	}

	// ── Profanity Filter ──
	cd.Text = censorMessage(cd.Text)

	// Inject sender's nickname
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
		return exists && otherRoom == senderRoom
	})
}
