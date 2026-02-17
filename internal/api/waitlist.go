package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type WaitlistRequest struct {
	Email string `json:"email"`
}

type WaitlistResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+-]+@[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$`)

type WaitlistHandler struct {
	DB               *sql.DB
	now              func() time.Time
	sendNotification func(signupEmail, timestamp string)
	limiter          *waitlistRateLimiter
}

type waitlistRateLimiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	entries map[string][]time.Time
}

func NewWaitlistHandler(db *sql.DB) *WaitlistHandler {
	return &WaitlistHandler{
		DB:               db,
		now:              time.Now,
		sendNotification: sendNotificationEmail,
		limiter:          newWaitlistRateLimiter(5, 10*time.Minute),
	}
}

// Handle handles POST /api/waitlist.
func (h *WaitlistHandler) Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nowFn := h.now
	if nowFn == nil {
		nowFn = time.Now
	}

	if h.limiter != nil && !h.limiter.allow(waitlistClientKey(r), nowFn()) {
		sendJSON(w, http.StatusTooManyRequests, WaitlistResponse{
			Success: false,
			Message: "Too many waitlist requests, please try again later.",
		})
		return
	}

	var req WaitlistRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, http.StatusBadRequest, WaitlistResponse{
			Success: false,
			Message: "Invalid request body",
		})
		return
	}

	email := strings.TrimSpace(strings.ToLower(req.Email))
	if !emailRegex.MatchString(email) {
		sendJSON(w, http.StatusBadRequest, WaitlistResponse{
			Success: false,
			Message: "Invalid email address",
		})
		return
	}

	if h.DB == nil {
		sendJSON(w, http.StatusServiceUnavailable, WaitlistResponse{
			Success: false,
			Message: "Waitlist is temporarily unavailable",
		})
		return
	}

	if _, err := h.DB.Exec(
		`INSERT INTO waitlist (email) VALUES ($1) ON CONFLICT (email) DO NOTHING`,
		email,
	); err != nil {
		sendJSON(w, http.StatusInternalServerError, WaitlistResponse{
			Success: false,
			Message: "Unable to save waitlist signup",
		})
		return
	}

	timestamp := nowFn().UTC().Format(time.RFC3339)
	fmt.Printf("🦦 Waitlist signup: %s at %s\n", email, timestamp)

	if h.sendNotification != nil {
		go h.sendNotification(email, timestamp)
	}

	sendJSON(w, http.StatusOK, WaitlistResponse{
		Success: true,
		Message: "You're on the list! We'll be in touch soon.",
	})
}

// HandleWaitlist handles POST /api/waitlist.
func HandleWaitlist(w http.ResponseWriter, r *http.Request) {
	NewWaitlistHandler(nil).Handle(w, r)
}

func newWaitlistRateLimiter(limit int, window time.Duration) *waitlistRateLimiter {
	if limit < 1 {
		limit = 1
	}
	if window <= 0 {
		window = time.Minute
	}
	return &waitlistRateLimiter{
		limit:   limit,
		window:  window,
		entries: make(map[string][]time.Time),
	}
}

func (l *waitlistRateLimiter) allow(client string, now time.Time) bool {
	if l == nil {
		return true
	}
	if client == "" {
		client = "unknown"
	}

	cutoff := now.Add(-l.window)

	l.mu.Lock()
	defer l.mu.Unlock()

	history := l.entries[client][:0]
	for _, ts := range l.entries[client] {
		if ts.After(cutoff) {
			history = append(history, ts)
		}
	}

	if len(history) >= l.limit {
		l.entries[client] = history
		return false
	}

	l.entries[client] = append(history, now)
	return true
}

func waitlistClientKey(r *http.Request) string {
	xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
	if xff != "" {
		first := strings.TrimSpace(strings.Split(xff, ",")[0])
		if first != "" {
			return first
		}
	}

	remote := strings.TrimSpace(r.RemoteAddr)
	if remote == "" {
		return "unknown"
	}
	host, _, err := net.SplitHostPort(remote)
	if err == nil && host != "" {
		return host
	}
	return remote
}

func sendNotificationEmail(signupEmail, timestamp string) {
	smtpHost := os.Getenv("SMTP_HOST")
	smtpPort := os.Getenv("SMTP_PORT")
	smtpUser := os.Getenv("SMTP_USER")
	smtpPass := os.Getenv("SMTP_PASS")
	notifyEmail := os.Getenv("NOTIFY_EMAIL")

	if smtpHost == "" || notifyEmail == "" {
		fmt.Printf("SMTP not configured, skipping notification for %s\n", signupEmail)
		return
	}

	if smtpPort == "" {
		smtpPort = "587"
	}

	from := smtpUser
	to := notifyEmail
	subject := "🦦 New Otter Camp Waitlist Signup"
	body := fmt.Sprintf("New signup!\n\nEmail: %s\nTime: %s", signupEmail, timestamp)

	msg := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\n\r\n%s",
		from, to, subject, body)

	auth := smtp.PlainAuth("", smtpUser, smtpPass, smtpHost)
	err := smtp.SendMail(smtpHost+":"+smtpPort, auth, from, []string{to}, []byte(msg))
	if err != nil {
		fmt.Printf("Failed to send notification email: %v\n", err)
		return
	}

	fmt.Printf("Notification sent to %s for signup %s\n", notifyEmail, signupEmail)
}

func sendJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}
