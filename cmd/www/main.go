package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	handler := newServerHandler("www", loadJoinConfigFromEnv())

	log.Printf("🦦 Otter Camp coming soon page on port %s", port)
	if err := http.ListenAndServe(":"+port, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

type joinConfig struct {
	InviteCodes map[string]struct{}
}

func loadJoinConfigFromEnv() joinConfig {
	codes := map[string]struct{}{}
	for _, raw := range strings.Split(os.Getenv("OTTER_JOIN_INVITE_CODES"), ",") {
		code := strings.ToLower(strings.TrimSpace(raw))
		if code == "" {
			continue
		}
		codes[code] = struct{}{}
	}

	return joinConfig{InviteCodes: codes}
}

func (c joinConfig) isValidInviteCode(code string) bool {
	normalized := strings.ToLower(strings.TrimSpace(code))
	if normalized == "" {
		return false
	}
	_, ok := c.InviteCodes[normalized]
	return ok
}

func newServerHandler(staticDir string, cfg joinConfig) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/join/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}

		code := strings.TrimPrefix(r.URL.Path, "/join/")
		if code == "" || strings.Contains(code, "/") || !cfg.isValidInviteCode(code) {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprint(w, renderJoinPage(code))
	})

	mux.Handle("/", http.FileServer(http.Dir(staticDir)))
	return mux
}

func renderJoinPage(code string) string {
	return `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Join Otter Camp</title>
  </head>
  <body>
    <h1>Join Otter Camp</h1>
    <p data-invite-code="` + code + `">Invite accepted.</p>
  </body>
</html>`
}
