package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	masterKeySize = 32
	gcmNonceSize  = 12
)

var (
	ErrNoMasterKey       = errors.New("no master key configured")
	ErrKMSNotImplemented = errors.New("kms key loading is not implemented")
	ErrInvalidMasterKey  = errors.New("invalid master key")
)

type MasterKey struct {
	Version int
	key     [masterKeySize]byte
}

func NewMasterKey(version int, raw []byte) (MasterKey, error) {
	if version <= 0 {
		return MasterKey{}, fmt.Errorf("%w: version must be > 0", ErrInvalidMasterKey)
	}
	if len(raw) != masterKeySize {
		return MasterKey{}, fmt.Errorf("%w: expected %d bytes, got %d", ErrInvalidMasterKey, masterKeySize, len(raw))
	}

	var keyBytes [masterKeySize]byte
	copy(keyBytes[:], raw)
	return MasterKey{
		Version: version,
		key:     keyBytes,
	}, nil
}

func Encrypt(key MasterKey, plaintext []byte) (ciphertext, nonce []byte, err error) {
	if err := validateKey(key); err != nil {
		return nil, nil, err
	}

	block, err := aes.NewCipher(key.key[:])
	if err != nil {
		return nil, nil, fmt.Errorf("create aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("create gcm: %w", err)
	}

	nonce = make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("generate nonce: %w", err)
	}

	ciphertext = gcm.Seal(nil, nonce, plaintext, nil)
	return ciphertext, nonce, nil
}

func Decrypt(key MasterKey, ciphertext, nonce []byte) (plaintext []byte, err error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	if len(nonce) != gcmNonceSize {
		return nil, fmt.Errorf("invalid nonce length: got %d, want %d", len(nonce), gcmNonceSize)
	}

	block, err := aes.NewCipher(key.key[:])
	if err != nil {
		return nil, fmt.Errorf("create aes cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create gcm: %w", err)
	}

	plaintext, err = gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt ciphertext: %w", err)
	}
	return plaintext, nil
}

func LoadMasterKey(version int) (MasterKey, error) {
	if version <= 0 {
		return MasterKey{}, fmt.Errorf("%w: version must be > 0", ErrInvalidMasterKey)
	}

	if encoded := strings.TrimSpace(os.Getenv("OTTERCAMP_MASTER_KEY")); encoded != "" {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return MasterKey{}, fmt.Errorf("decode OTTERCAMP_MASTER_KEY as base64: %w", err)
		}
		return NewMasterKey(version, raw)
	}

	if path := strings.TrimSpace(os.Getenv("OTTERCAMP_MASTER_KEY_FILE")); path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return MasterKey{}, fmt.Errorf("read OTTERCAMP_MASTER_KEY_FILE: %w", err)
		}
		if len(content) < masterKeySize {
			return MasterKey{}, fmt.Errorf("%w: OTTERCAMP_MASTER_KEY_FILE must contain at least %d bytes", ErrInvalidMasterKey, masterKeySize)
		}
		return NewMasterKey(version, content[:masterKeySize])
	}

	if kmsKeyID := strings.TrimSpace(os.Getenv("OTTERCAMP_KMS_KEY_ID")); kmsKeyID != "" {
		return MasterKey{}, fmt.Errorf("%w: OTTERCAMP_KMS_KEY_ID=%q", ErrKMSNotImplemented, kmsKeyID)
	}

	return MasterKey{}, fmt.Errorf(
		"%w: set OTTERCAMP_MASTER_KEY (base64-encoded 32 bytes) or OTTERCAMP_MASTER_KEY_FILE",
		ErrNoMasterKey,
	)
}

func validateKey(key MasterKey) error {
	if key.Version <= 0 {
		return fmt.Errorf("%w: version must be > 0", ErrInvalidMasterKey)
	}
	if len(key.key) != masterKeySize {
		return fmt.Errorf("%w: expected %d-byte key", ErrInvalidMasterKey, masterKeySize)
	}
	return nil
}
