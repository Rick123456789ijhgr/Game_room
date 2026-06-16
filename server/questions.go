package server

import (
	"encoding/json"
	"log"
	"math/rand"
	"os"
)

var (
	easyQuestions   []string
	mediumQuestions []string
	hardQuestions   []string
)

type easySet struct {
	Easy []string `json:"easy"`
}

type mediumSet struct {
	Medium []string `json:"medium"`
}

type hardSet struct {
	Hard []string `json:"hard"`
}

func LoadQuestions() {
	// Load easy
	b, err := os.ReadFile("assets/json/questions_set0_easy.json")
	if err == nil {
		var s easySet
		if err := json.Unmarshal(b, &s); err == nil {
			easyQuestions = s.Easy
		} else {
			log.Printf("Failed to parse easy questions: %v", err)
		}
	} else {
		log.Printf("Failed to read easy questions: %v", err)
	}

	// Load medium
	b, err = os.ReadFile("assets/json/questions_set1_medium.json")
	if err == nil {
		var s mediumSet
		if err := json.Unmarshal(b, &s); err == nil {
			mediumQuestions = s.Medium
		} else {
			log.Printf("Failed to parse medium questions: %v", err)
		}
	} else {
		log.Printf("Failed to read medium questions: %v", err)
	}

	// Load hard
	b, err = os.ReadFile("assets/json/questions_set2_hard.json")
	if err == nil {
		var s hardSet
		if err := json.Unmarshal(b, &s); err == nil {
			hardQuestions = s.Hard
		} else {
			log.Printf("Failed to parse hard questions: %v", err)
		}
	} else {
		log.Printf("Failed to read hard questions: %v", err)
	}

	log.Printf("[Questions] Loaded: %d easy, %d medium, %d hard", len(easyQuestions), len(mediumQuestions), len(hardQuestions))
}

func GetRandomTopic() string {
	if len(easyQuestions) == 0 && len(mediumQuestions) == 0 && len(hardQuestions) == 0 {
		return "自由發揮"
	}

	for {
		r := rand.Float64()
		if r < 0.5 {
			if len(easyQuestions) > 0 {
				return easyQuestions[rand.Intn(len(easyQuestions))]
			}
		} else if r < 0.85 {
			if len(mediumQuestions) > 0 {
				return mediumQuestions[rand.Intn(len(mediumQuestions))]
			}
		} else {
			if len(hardQuestions) > 0 {
				return hardQuestions[rand.Intn(len(hardQuestions))]
			}
		}
	}
}
