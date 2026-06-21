package server

import (
	"strings"
	"time"

	"github.com/olahol/melody"
)

// ── Profanity Filter ──────────────────────────────────────────────────────────

// profanityList is the blocklist for bad words.
// Add or remove words here to customize the filter.
var profanityList = []string{"幹", "靠杯", "沙小", "機掰", "屌", "媽的", "fuck", "shit", "bitch"}

// censorMessage replaces bad words with *** (case-insensitive).
func censorMessage(text string) string {
	lowerText := strings.ToLower(text)
	for _, word := range profanityList {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			text = strings.ReplaceAll(text, word, "***")
		}
	}
	return text
}

// containsProfanity checks if the text contains any word from profanityList.
func containsProfanity(text string) bool {
	lowerText := strings.ToLower(text)
	for _, word := range profanityList {
		if strings.Contains(lowerText, strings.ToLower(word)) {
			return true
		}
	}
	return false
}

// ── Rate Limiting ─────────────────────────────────────────────────────────────

// checkRateLimit returns true if the session may send a message now.
// It updates last_msg_time on success.
// Returns false (and does NOT update the timestamp) if the session is sending too fast.
func checkRateLimit(s *melody.Session) bool {
	now := time.Now()
	if lastMsgTime, ok := s.Get("last_msg_time"); ok {
		if now.Sub(lastMsgTime.(time.Time)) < 1*time.Second {
			return false // rate limited
		}
	}
	s.Set("last_msg_time", now)
	return true
}
