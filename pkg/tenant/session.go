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

const createSessionSQL = `INSERT INTO devpulse_session (id, tenant_id, expires_at) VALUES ($1, $2, NOW() + $3::interval)`

const validateSessionSQL = `
	SELECT t.id, t.github_id, t.username, t.email, t.avatar_url,
	       COALESCE(t.name, ''), COALESCE(t.company, ''), COALESCE(t.location, ''), COALESCE(t.bio, ''),
	       t.max_repos, t.max_events_per_week, t.plan, t.tos_accepted_at, t.upgrade_requested_at, t.created_at, t.updated_at
	FROM devpulse_session s
	JOIN devpulse_tenant t ON t.id = s.tenant_id
	WHERE s.id = $1 AND s.expires_at > NOW()`

const destroySessionSQL = `DELETE FROM devpulse_session WHERE id = $1`

const cleanExpiredSessionsSQL = `DELETE FROM devpulse_session WHERE expires_at <= NOW()`

// CreateSession generates a random 256-bit token, stores its SHA-256 hash
// in the database, and returns the raw token for the cookie.
//
// The INSERT runs inside a transaction that clears app.tenant_id
// (transaction-local) so the RLS bypass policy on devpulse_session allows
// the write. Without this, a pooled connection carrying a stale
// app.tenant_id from a previous scoped request would cause the INSERT to
// be rejected by RLS (surfaced as an FK-constraint error).
func CreateSession(ctx context.Context, db *sql.DB, tenantID string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	rawToken := hex.EncodeToString(raw)
	hashed := HashToken(rawToken)

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("beginning session tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		"SELECT set_config('app.tenant_id', '', true)"); err != nil {
		return "", fmt.Errorf("clearing tenant scope for session: %w", err)
	}

	if _, err := tx.ExecContext(ctx, createSessionSQL, hashed, tenantID, ttl.String()); err != nil {
		return "", fmt.Errorf("creating session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("committing session: %w", err)
	}
	return rawToken, nil
}

// ValidateSession checks the session token and returns the associated tenant.
// Uses a dedicated connection with cleared app.tenant_id so the RLS bypass
// policy allows the JOIN across devpulse_session and devpulse_tenant.
func ValidateSession(ctx context.Context, db *sql.DB, rawToken string) (*Tenant, error) {
	hashed := HashToken(rawToken)

	conn, err := db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquiring connection for session validation: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx,
		"SELECT set_config('app.tenant_id', '', false)"); err != nil {
		return nil, fmt.Errorf("clearing tenant scope for session validation: %w", err)
	}

	t, scanErr := scanTenant(conn.QueryRowContext(ctx, validateSessionSQL, hashed))
	if errors.Is(scanErr, sql.ErrNoRows) {
		return nil, ErrSessionInvalid
	}
	if scanErr != nil {
		return nil, fmt.Errorf("validating session: %w", scanErr)
	}
	return t, nil
}

// DestroySession removes a session by its raw token.
// Uses a transaction with cleared app.tenant_id so the RLS bypass policy
// allows the DELETE regardless of stale connection state.
func DestroySession(ctx context.Context, db *sql.DB, rawToken string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning destroy session tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		"SELECT set_config('app.tenant_id', '', true)"); err != nil {
		return fmt.Errorf("clearing tenant scope for session destroy: %w", err)
	}

	if _, err := tx.ExecContext(ctx, destroySessionSQL, HashToken(rawToken)); err != nil {
		return fmt.Errorf("destroying session: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing session destroy: %w", err)
	}
	return nil
}

const lastSignInSQL = `SELECT MAX(created_at) FROM devpulse_session WHERE tenant_id = $1`

// GetLastSignIn returns the most recent session creation time for a tenant.
// Uses a dedicated connection with cleared app.tenant_id so RLS bypass
// allows the query regardless of stale connection state.
func GetLastSignIn(ctx context.Context, db *sql.DB, tenantID string) *time.Time {
	conn, err := db.Conn(ctx)
	if err != nil {
		return nil
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx,
		"SELECT set_config('app.tenant_id', '', false)"); err != nil {
		return nil
	}

	var t sql.NullTime
	if err := conn.QueryRowContext(ctx, lastSignInSQL, tenantID).Scan(&t); err != nil || !t.Valid {
		return nil
	}
	return &t.Time
}

// CleanExpiredSessions removes all expired sessions.
// Uses a transaction with cleared app.tenant_id so the RLS bypass policy
// allows the DELETE across all tenants.
func CleanExpiredSessions(ctx context.Context, db *sql.DB) (int64, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("beginning clean sessions tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.ExecContext(ctx,
		"SELECT set_config('app.tenant_id', '', true)"); err != nil {
		return 0, fmt.Errorf("clearing tenant scope for session cleanup: %w", err)
	}

	res, err := tx.ExecContext(ctx, cleanExpiredSessionsSQL)
	if err != nil {
		return 0, fmt.Errorf("cleaning expired sessions: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("getting rows affected: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("committing session cleanup: %w", err)
	}
	return n, nil
}

// HashToken returns the hex-encoded SHA-256 hash of a raw token.
func HashToken(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
