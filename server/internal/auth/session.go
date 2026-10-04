package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"time"

	"golang.org/x/crypto/bcrypt"
)

var (
	ErrBadCredentials = errors.New("invalid username or password")
	ErrNoSession      = errors.New("no such session")
)

// SessionStore is implemented by *storage.DB (admin_sessions table).
type SessionStore interface {
	CreateSession(ctx context.Context, tokenHash, username string, expiresAt time.Time) error
	// GetSession returns ErrNoSession when the token is unknown.
	GetSession(ctx context.Context, tokenHash string) (username string, expiresAt time.Time, err error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

// Sessions implements the single-operator admin login (docs/api.md §2.1).
type Sessions struct {
	Store        SessionStore
	User         string
	PasswordHash []byte // bcrypt
	TTL          time.Duration
}

// Login verifies credentials and mints a session token (returned raw; only
// its SHA-256 is persisted).
func (s *Sessions) Login(ctx context.Context, username, password string) (string, error) {
	userOK := subtle.ConstantTimeCompare([]byte(username), []byte(s.User)) == 1
	// Always run the bcrypt comparison so unknown usernames cost the same.
	passErr := bcrypt.CompareHashAndPassword(s.PasswordHash, []byte(password))
	if !userOK || passErr != nil {
		return "", ErrBadCredentials
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	if err := s.Store.CreateSession(ctx, HashToken(token), username, time.Now().Add(s.TTL)); err != nil {
		return "", err
	}
	return token, nil
}

// Validate reports whether the token belongs to a live session.
func (s *Sessions) Validate(ctx context.Context, token string) (bool, error) {
	if token == "" {
		return false, nil
	}
	_, expires, err := s.Store.GetSession(ctx, HashToken(token))
	if errors.Is(err, ErrNoSession) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if time.Now().After(expires) {
		_ = s.Store.DeleteSession(ctx, HashToken(token))
		return false, nil
	}
	return true, nil
}

func (s *Sessions) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return s.Store.DeleteSession(ctx, HashToken(token))
}

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
