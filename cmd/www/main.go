package main

import (
	"log"
	"net/http"
	"os"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	// Serve static files from www directory
	fs := http.FileServer(http.Dir("www"))
	http.Handle("/", fs)

	log.Printf("🦦 Otter Camp coming soon page on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
