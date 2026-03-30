package tenant

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

// ErrSessionInvalid is returned when a session is expired or not found.
var ErrSessionInvalid = errors.New("session expired or not found")

const createSessionSQL = `INSERT INTO session (id, tenant_id, expires_at) VALUES ($1, $2, $3)`

const validateSessionSQL = `
	SELECT t.id, t.github_id, t.username, t.email, t.avatar_url,
	       t.max_repos, t.max_events_per_week, t.plan, t.tos_accepted_at, t.upgrade_requested_at, t.created_at, t.updated_at
	FROM session s
	JOIN tenant t ON t.id = s.tenant_id
	WHERE s.id = $1 AND s.expires_at > NOW()`

const destroySessionSQL = `DELETE FROM session WHERE id = $1`

const cleanExpiredSessionsSQL = `DELETE FROM session WHERE expires_at <= NOW()`

// CreateSession generates a random 256-bit token, stores its SHA-256 hash
// in the database, and returns the raw token for the cookie.
func CreateSession(ctx context.Context, db *sql.DB, tenantID string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	rawToken := hex.EncodeToString(raw)
	hashed := HashToken(rawToken)

	_, err := db.ExecContext(ctx, createSessionSQL, hashed, tenantID, time.Now().Add(ttl))
	if err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}
	return rawToken, nil
}

// ValidateSession checks the session token and returns the associated tenant.
func ValidateSession(ctx context.Context, db *sql.DB, rawToken string) (*Tenant, error) {
	hashed := HashToken(rawToken)
	t, err := scanTenant(db.QueryRowContext(ctx, validateSessionSQL, hashed))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if err != nil {
		return nil, fmt.Errorf("validating session: %w", err)
	}
	return t, nil
}

// DestroySession removes a session by its raw token.
func DestroySession(ctx context.Context, db *sql.DB, rawToken string) error {
	_, err := db.ExecContext(ctx, destroySessionSQL, HashToken(rawToken))
	if err != nil {
		return fmt.Errorf("destroying session: %w", err)
	}
	return nil
}

// CleanExpiredSessions removes all expired sessions.
func CleanExpiredSessions(ctx context.Context, db *sql.DB) (int64, error) {
	res, err := db.ExecContext(ctx, cleanExpiredSessionsSQL)
	if err != nil {
		return 0, fmt.Errorf("cleaning expired sessions: %w", err)
	}
	return res.RowsAffected()
}

// HashToken returns the hex-encoded SHA-256 hash of a raw token.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
