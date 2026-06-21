package server

import (
	"encoding/json"

	"github.com/olahol/melody"
)

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

// broadcastScores sends the current scoreboard to all players in the room.
func broadcastScores(m *melody.Melody, roomID string) {
	scores := buildScores(m, roomID)
	data, _ := json.Marshal(scores)
	msg, _ := json.Marshal(Message{
		Event:  "score_update",
		RoomID: roomID,
		Data:   data,
	})
	broadcastToRoom(m, roomID, msg)
}

// checkWinCondition checks if any player has reached the target score.
// Returns (winner nickname, isTied).
// - winner != "" → someone won outright
// - isTied      → multiple players tied at the target
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
