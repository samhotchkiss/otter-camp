package crypto

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := mustNewMasterKey(t)
	plaintext := []byte("super-secret-value")

	ciphertext, nonce, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if bytes.Equal(ciphertext, plaintext) {
		t.Fatal("ciphertext must not match plaintext")
	}
	if len(nonce) != 12 {
		t.Fatalf("nonce length = %d, want 12", len(nonce))
	}

	decrypted, err := Decrypt(key, ciphertext, nonce)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if !bytes.Equal(decrypted, plaintext) {
		t.Fatalf("decrypted plaintext = %q, want %q", decrypted, plaintext)
	}
}

func TestEncryptProducesUniqueCiphertextForSamePlaintext(t *testing.T) {
	key := mustNewMasterKey(t)
	plaintext := []byte("repeatable")

	ciphertextA, nonceA, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt first failed: %v", err)
	}
	ciphertextB, nonceB, err := Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt second failed: %v", err)
	}

	if bytes.Equal(nonceA, nonceB) {
		t.Fatal("expected different nonces")
	}
	if bytes.Equal(ciphertextA, ciphertextB) {
		t.Fatal("expected different ciphertexts")
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	key := mustNewMasterKey(t)
	ciphertext, nonce, err := Encrypt(key, []byte("verify-me"))
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	ciphertext[0] ^= 0xFF
	if _, err := Decrypt(key, ciphertext, nonce); err == nil {
		t.Fatal("Decrypt should fail for tampered ciphertext")
	}
}

func TestLoadMasterKeyFromEnv(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	t.Setenv("OTTERCAMP_MASTER_KEY", base64.StdEncoding.EncodeToString(raw))

	key, err := LoadMasterKey(1)
	if err != nil {
		t.Fatalf("LoadMasterKey failed: %v", err)
	}

	encrypted, nonce, err := Encrypt(key, []byte("ok"))
	if err != nil {
		t.Fatalf("Encrypt failed with loaded key: %v", err)
	}
	if _, err := Decrypt(key, encrypted, nonce); err != nil {
		t.Fatalf("Decrypt failed with loaded key: %v", err)
	}
}

func TestLoadMasterKeyFromFile(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	filePath := filepath.Join(t.TempDir(), "master-key.bin")
	fileBytes := make([]byte, 64)
	for i := range fileBytes {
		fileBytes[i] = byte(255 - i)
	}
	if err := os.WriteFile(filePath, fileBytes, 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", filePath)

	key, err := LoadMasterKey(1)
	if err != nil {
		t.Fatalf("LoadMasterKey failed: %v", err)
	}

	ciphertext, nonce, err := Encrypt(key, []byte("hello"))
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if _, err := Decrypt(key, ciphertext, nonce); err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
}

func TestLoadMasterKeyNoConfigReturnsError(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", "")
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "")

	_, err := LoadMasterKey(1)
	if !errors.Is(err, ErrNoMasterKey) {
		t.Fatalf("LoadMasterKey error = %v, want ErrNoMasterKey", err)
	}
}

func TestLoadMasterKeyKMSStub(t *testing.T) {
	t.Setenv("OTTERCAMP_MASTER_KEY", "")
	t.Setenv("OTTERCAMP_MASTER_KEY_FILE", "")
	t.Setenv("OTTERCAMP_KMS_KEY_ID", "kms-key-1")

	_, err := LoadMasterKey(1)
	if !errors.Is(err, ErrKMSNotImplemented) {
		t.Fatalf("LoadMasterKey error = %v, want ErrKMSNotImplemented", err)
	}
}

func mustNewMasterKey(t *testing.T) MasterKey {
	t.Helper()

	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	key, err := NewMasterKey(1, raw)
	if err != nil {
		t.Fatalf("NewMasterKey failed: %v", err)
	}
	return key
}
