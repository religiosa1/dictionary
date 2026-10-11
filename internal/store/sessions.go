package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// Only the hash of a session token is stored, so a leaked DB doesn't leak
// live sessions.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// CreateSession returns a new random session token for the user. idToken is
// the raw OIDC ID token, kept for logout; empty for non-OIDC logins.
func (db *DB) CreateSession(userID int64, idToken string, ttl time.Duration) (string, error) {
	token := rand.Text()
	_, err := db.Exec(
		`INSERT INTO sessions (token_hash, user_id, id_token, expires_at) VALUES (?, ?, ?, ?)`,
		hashToken(token), userID, idToken, time.Now().UTC().Add(ttl),
	)
	if err != nil {
		return "", err
	}
	return token, nil
}

// SessionUser returns the user owning a non-expired session.
func (db *DB) SessionUser(token string) (User, error) {
	var user User
	err := db.Get(&user, `
		SELECT u.* FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = ? AND s.expires_at > ?`,
		hashToken(token), time.Now().UTC(),
	)
	if isNotFoundError(err) {
		return user, ErrNotFound
	}
	return user, err
}

// DeleteSession deletes the session and returns its ID token; empty if
// there's no such session.
func (db *DB) DeleteSession(token string) (idToken string, err error) {
	err = db.Get(&idToken, `DELETE FROM sessions WHERE token_hash = ? RETURNING id_token`, hashToken(token))
	if isNotFoundError(err) {
		return "", nil
	}
	return idToken, err
}

func (db *DB) DeleteExpiredSessions() error {
	_, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC())
	return err
}
