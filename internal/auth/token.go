package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const (
	sessionTokenBytes  = 32
	apiKeyRandomLength = 40
	apiKeyPrefix       = "otk_"
	magicTokenLength   = 32
)

var base58Alphabet = []byte("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz")

func generateSessionToken() (raw string, hash string, err error) {
	buf := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}

	raw = base64.RawURLEncoding.EncodeToString(buf)
	return raw, sha256Hex(raw), nil
}

func generateAPIKey() (raw string, hash string, prefix string, err error) {
	randomPart, err := generateBase58(apiKeyRandomLength)
	if err != nil {
		return "", "", "", err
	}

	raw = apiKeyPrefix + randomPart
	prefix = raw
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}

	return raw, sha256Hex(raw), prefix, nil
}

func generateMagicToken() (string, error) {
	token, err := generateBase58(magicTokenLength)
	if err != nil {
		return "", err
	}
	return "mlk_" + token, nil
}

func generateBase58(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("base58 length must be > 0")
	}

	var builder strings.Builder
	builder.Grow(length)

	max := big.NewInt(int64(len(base58Alphabet)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", fmt.Errorf("generate random base58: %w", err)
		}
		builder.WriteByte(base58Alphabet[n.Int64()])
	}

	return builder.String(), nil
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
