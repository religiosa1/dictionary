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

// CreateSession returns a new random session token for the user.
func (db *DB) CreateSession(userID int64, ttl time.Duration) (string, error) {
	token := rand.Text()
	_, err := db.Exec(
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(token), userID, time.Now().UTC().Add(ttl),
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

func (db *DB) DeleteSession(token string) error {
	_, err := db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
	return err
}

func (db *DB) DeleteExpiredSessions() error {
	_, err := db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UTC())
	return err
}
