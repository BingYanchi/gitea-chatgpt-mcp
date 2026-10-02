package auth

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
)

type sealer struct {
	aead cipher.AEAD
}

func newSealer(encodedKey string) (*sealer, error) {
	key, err := base64.StdEncoding.DecodeString(encodedKey)
	if err != nil {
		return nil, fmt.Errorf("OAUTH_ENCRYPTION_KEY must be base64: %w", err)
	}
	if len(key) != 32 {
		return nil, fmt.Errorf("OAUTH_ENCRYPTION_KEY must decode to exactly 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &sealer{aead: aead}, nil
}

func (s *sealer) seal(purpose string, value any) (string, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := s.aead.Seal(nil, nonce, plain, []byte(purpose))
	raw := append(nonce, ciphertext...)
	return "v1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

func (s *sealer) open(purpose, token string, dst any) error {
	const prefix = "v1."
	if len(token) <= len(prefix) || token[:len(prefix)] != prefix {
		return fmt.Errorf("unsupported token format")
	}
	raw, err := base64.RawURLEncoding.DecodeString(token[len(prefix):])
	if err != nil {
		return fmt.Errorf("decode sealed token: %w", err)
	}
	if len(raw) < s.aead.NonceSize() {
		return fmt.Errorf("sealed token is too short")
	}
	nonce := raw[:s.aead.NonceSize()]
	ciphertext := raw[s.aead.NonceSize():]
	plain, err := s.aead.Open(nil, nonce, ciphertext, []byte(purpose))
	if err != nil {
		return fmt.Errorf("open sealed token: %w", err)
	}
	if err := json.Unmarshal(plain, dst); err != nil {
		return fmt.Errorf("decode sealed payload: %w", err)
	}
	return nil
}
