package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/smtp"
	"os"
	"regexp"
	"strings"
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

// HandleWaitlist handles POST /api/waitlist
func HandleWaitlist(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
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

	// TODO: Store in Postgres once DB is wired up
	// For now, just log and notify
	timestamp := time.Now().UTC().Format(time.RFC3339)
	fmt.Printf("🦦 Waitlist signup: %s at %s\n", email, timestamp)

	// Send notification email to Sam
	go sendNotificationEmail(email, timestamp)

	sendJSON(w, http.StatusOK, WaitlistResponse{
		Success: true,
		Message: "You're on the list! We'll be in touch soon.",
	})
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
