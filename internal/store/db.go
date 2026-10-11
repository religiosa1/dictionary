package store

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"

	"github.com/jmoiron/sqlx"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var (
	ErrNotFound  = errors.New("record not found")
	ErrDuplicate = errors.New("duplicate record")
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

type DB struct {
	*sqlx.DB
}

// Open opens (creating if needed) the database file and applies pending migrations.
func Open(path string) (*DB, error) {
	// _txlock=immediate: write transactions take the lock upfront, instead of
	// failing with SQLITE_BUSY when a read tx upgrades to a write.
	dsn := path + "?_journal_mode=WAL&_foreign_keys=1&_busy_timeout=5000&_synchronous=NORMAL" +
		"&_txlock=immediate&_time_format=sqlite&_timezone=UTC"
	db, err := sqlx.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("error opening the database: %w", err)
	}
	migrations, err := loadMigrations()
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	if err := NewMigrator(db).Migrate(migrations); err != nil {
		return nil, errors.Join(fmt.Errorf("error applying migrations: %w", err), db.Close())
	}
	return &DB{db}, nil
}

func loadMigrations() ([]string, error) {
	// fs.ReadDir returns entries sorted by filename, which is migration order.
	entries, err := fs.ReadDir(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	migrations := make([]string, 0, len(entries))
	for _, entry := range entries {
		content, err := fs.ReadFile(migrationsFS, "migrations/"+entry.Name())
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, string(content))
	}
	return migrations, nil
}

func isUniqueError(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlite.Error](err)
	return ok && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE
}

func isNotFoundError(err error) bool {
	return errors.Is(err, sql.ErrNoRows)
}
