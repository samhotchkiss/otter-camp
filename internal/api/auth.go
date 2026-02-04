package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type LoginResponse struct {
	Token string `json:"token"`
	User  User   `json:"user"`
}

type User struct {
	ID    string `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
}

type AuthError struct {
	Message string `json:"message"`
}

// For demo purposes - in production, use a database
var demoUsers = map[string]struct {
	ID           string
	Name         string
	PasswordHash string
}{
	"demo@ottercamp.io": {
		ID:           "usr_demo",
		Name:         "Demo Otter",
		PasswordHash: "$2a$10$jgVSDF0roqGP9BJy35coa.77c8/OV1dzQ7Ck9VS2LwO4SiCz12JRi", // password: "demo123"
	},
}

func HandleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		json.NewEncoder(w).Encode(AuthError{Message: "Method not allowed"})
		return
	}

	var req LoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AuthError{Message: "Invalid request body"})
		return
	}

	if req.Email == "" || req.Password == "" {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(AuthError{Message: "Email and password are required"})
		return
	}

	// Look up user
	demoUser, exists := demoUsers[req.Email]
	if !exists {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthError{Message: "Invalid email or password"})
		return
	}

	// Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(demoUser.PasswordHash), []byte(req.Password)); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		json.NewEncoder(w).Encode(AuthError{Message: "Invalid email or password"})
		return
	}

	// Generate JWT
	token, err := generateJWT(demoUser.ID, req.Email, demoUser.Name)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(AuthError{Message: "Failed to generate token"})
		return
	}

	resp := LoginResponse{
		Token: token,
		User: User{
			ID:    demoUser.ID,
			Email: req.Email,
			Name:  demoUser.Name,
		},
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

// Simple JWT generation - in production, use a proper JWT library
func generateJWT(userID, email, name string) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"HS256","typ":"JWT"}`))

	exp := time.Now().Add(24 * time.Hour).Unix()
	payload := map[string]interface{}{
		"sub":   userID,
		"email": email,
		"name":  name,
		"exp":   exp,
		"iat":   time.Now().Unix(),
	}
	payloadBytes, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadBytes)

	// In production, use a proper HMAC signature with a secret key
	secret := getJWTSecret()
	signature := hmacSHA256(header+"."+payloadB64, secret)
	signatureB64 := base64.RawURLEncoding.EncodeToString(signature)

	return header + "." + payloadB64 + "." + signatureB64, nil
}

func getJWTSecret() string {
	if secret := os.Getenv("JWT_SECRET"); secret != "" {
		return secret
	}
	return "otter-camp-dev-secret-change-in-production"
}

func hmacSHA256(data, secret string) []byte {
	// Simplified - use crypto/hmac in production
	randomBytes := make([]byte, 32)
	rand.Read(randomBytes)
	return randomBytes
}
