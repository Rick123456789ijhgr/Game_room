package server

import (
	"encoding/json"
	"log"
	"math/rand"
	"time"

	"github.com/olahol/melody"
)

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
		Data:   data,
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
		Data:   data,
	})
	broadcastToRoom(m, roomID, msg)
	log.Printf("[WS] Game over in room %q — winner: %q overtime: %v", roomID, winner, overtime)
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
			Data:   timerStartData,
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
			Data:   roundEndData,
		})
		broadcastToRoom(m, roomID, roundEndMsg)
		log.Printf("[WS] Round ended for room %q — answer: %q", roomID, topic)
	}()
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
	for _, targetSess := range sessions {
		if r, ok := targetSess.Get("room"); !ok || r != roomID {
			continue
		}
		isDrawer := (targetSess == drawer)
		targetSess.Set("is_drawer", isDrawer)
		targetSess.Set("current_topic", topic)
		targetSess.Set("is_playing", true)
		targetSess.Set("guessed_correctly", false)

		if isDrawer {
			targetSess.Set("drawer_awarded_first", false)
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
			Data:   dataBytes,
		})
		targetSess.Write(resp)
	}

	// 5 s role overlay + 1 s buffer = 6 s delay before countdown begins
	// Then 30 s for the round itself
	startRoundTimer(m, roomID, 30*time.Second, 6*time.Second)
	drawerName := "?"
	if n, ok := drawer.Get("nickname"); ok {
		drawerName = n.(string)
	}
	log.Printf("[WS] Round started in room %q — drawer: %s, topic: %s, overtime: %v", roomID, drawerName, topic, isOvertime)
}
