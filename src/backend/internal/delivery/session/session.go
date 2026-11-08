// Package session seals the user session, including the Moodle token, into an opaque value kept in an HttpOnly cookie.
// The server stores no tokens. Workflows get their own sealed value, which only activities open: Temporal keeps it in
// its history, and it does not work as a cookie.
package session

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Session struct {
	UserID  int64  `json:"u"`
	Token   string `json:"t"`
	Expires int64  `json:"e"`
}

var ErrInvalid = errors.New("invalid or expired session")

type Sealer struct{ aead cipher.AEAD }

// NewSealer takes a 32-byte key (AES-256-GCM).
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("session key must be 32 bytes, got %d", len(key))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("session cipher: %w", err)
	}

	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("session cipher: %w", err)
	}

	return &Sealer{aead: aead}, nil
}

// forWork binds a sealed value to background work: it opens only with OpenWork.
var forWork = []byte("work")

// Seal seals the session for the cookie.
func (s *Sealer) Seal(sess Session) (string, error) { return s.seal(sess, nil) }

// Open opens the value of a cookie.
func (s *Sealer) Open(sealed string) (Session, error) { return s.open(sealed, nil) }

// SealForWork seals the session for a workflow.
func (s *Sealer) SealForWork(sess Session) (string, error) { return s.seal(sess, forWork) }

// OpenWork opens the value a workflow carries.
func (s *Sealer) OpenWork(sealed string) (Session, error) { return s.open(sealed, forWork) }

func (s *Sealer) seal(sess Session, purpose []byte) (string, error) {
	plain, err := json.Marshal(sess)
	if err != nil {
		return "", fmt.Errorf("encode session: %w", err)
	}

	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("session nonce: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(s.aead.Seal(nonce, nonce, plain, purpose)), nil
}

func (s *Sealer) open(sealed string, purpose []byte) (Session, error) {
	raw, err := base64.RawURLEncoding.DecodeString(sealed)
	if err != nil || len(raw) < s.aead.NonceSize() {
		return Session{}, ErrInvalid
	}

	plain, err := s.aead.Open(nil, raw[:s.aead.NonceSize()], raw[s.aead.NonceSize():], purpose)
	if err != nil {
		return Session{}, ErrInvalid
	}

	var sess Session
	if json.Unmarshal(plain, &sess) != nil || time.Now().Unix() > sess.Expires {
		return Session{}, ErrInvalid
	}

	return sess, nil
}
