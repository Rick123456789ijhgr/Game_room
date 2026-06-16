package main

import (
	"fmt"
	"log"
	"net/http"
	"os"

	"draw-and-guess/server"

	"github.com/olahol/melody"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	// Load questions
	server.LoadQuestions()

	// Initialise Melody WebSocket manager
	m := melody.New()

	// --- Melody event handlers ---
	server.SetupWebSockets(m)

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
