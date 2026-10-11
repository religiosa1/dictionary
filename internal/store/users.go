package store

import (
	"time"
)

type Role string

const (
	RoleReader Role = "reader"
	RoleEditor Role = "editor"
)

type User struct {
	ID          int64     `db:"id"`
	OIDCSub     string    `db:"oidc_sub"`
	Name        string    `db:"name"`
	Role        Role      `db:"role"`
	CreatedAt   time.Time `db:"created_at"`
	LastLoginAt time.Time `db:"last_login_at"`
}

func (u *User) IsEditor() bool {
	return u != nil && u.Role == RoleEditor
}

// UpsertUser creates or refreshes a user on login; name and role always
// follow the latest OIDC claims.
func (db *DB) UpsertUser(sub, name string, role Role) (User, error) {
	now := time.Now().UTC()
	var user User
	err := db.Get(&user, `
		INSERT INTO users (oidc_sub, name, role, created_at, last_login_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (oidc_sub) DO UPDATE SET
			name = excluded.name,
			role = excluded.role,
			last_login_at = excluded.last_login_at
		RETURNING *`,
		sub, name, role, now, now,
	)
	return user, err
}

// ListUsers returns all users, for filter dropdowns.
func (db *DB) ListUsers() ([]User, error) {
	var users []User
	err := db.Select(&users, `SELECT * FROM users ORDER BY name`)
	return users, err
}
