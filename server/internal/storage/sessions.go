package storage

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"atomic-updater/server/internal/auth"
)

// --- admin_sessions (implements auth.SessionStore) ---

func (db *DB) CreateSession(ctx context.Context, tokenHash, username string, expiresAt time.Time) error {
	// Opportunistic cleanup keeps the table from accumulating dead rows.
	_, _ = db.Pool.Exec(ctx, `DELETE FROM admin_sessions WHERE expires_at < now()`)
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO admin_sessions (token_hash, username, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, username, expiresAt)
	return err
}

func (db *DB) GetSession(ctx context.Context, tokenHash string) (string, time.Time, error) {
	var username string
	var expires time.Time
	err := db.Pool.QueryRow(ctx,
		`SELECT username, expires_at FROM admin_sessions WHERE token_hash = $1`,
		tokenHash).Scan(&username, &expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, auth.ErrNoSession
	}
	return username, expires, err
}

func (db *DB) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM admin_sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// --- revoked_certs ---

func (db *DB) IsRevoked(ctx context.Context, certFingerprint string) (bool, error) {
	var revoked bool
	err := db.Pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM revoked_certs WHERE cert_fingerprint = $1)`,
		certFingerprint).Scan(&revoked)
	return revoked, err
}

func (db *DB) RevokeCert(ctx context.Context, certFingerprint, serial, reason string) error {
	_, err := db.Pool.Exec(ctx,
		`INSERT INTO revoked_certs (cert_fingerprint, serial, reason)
		 VALUES ($1, $2, $3) ON CONFLICT (cert_fingerprint) DO NOTHING`,
		certFingerprint, serial, reason)
	return err
}

// isUniqueViolation matches Postgres error 23505.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
