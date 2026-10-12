package store

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

type Meaning struct {
	Translation string   `json:"translation"`
	Examples    []string `json:"examples"`
}

// Meanings is stored as a JSON array in a single column.
type Meanings []Meaning

func (m Meanings) Value() (driver.Value, error) {
	b, err := json.Marshal(m)
	return string(b), err
}

func (m *Meanings) Scan(src any) error {
	return scanJSON(src, m)
}

// StringList scans a JSON array of strings, e.g. from json_group_array.
type StringList []string

func (s *StringList) Scan(src any) error {
	return scanJSON(src, s)
}

func scanJSON(src any, dest any) error {
	switch v := src.(type) {
	case string:
		return json.Unmarshal([]byte(v), dest)
	case []byte:
		return json.Unmarshal(v, dest)
	case nil:
		return nil
	default:
		return fmt.Errorf("unexpected JSON column type %T", src)
	}
}

type Phrase struct {
	ID            int64      `db:"id"`
	Phrase        string     `db:"phrase"`
	Meanings      Meanings   `db:"meanings"`
	Notes         string     `db:"notes"`
	Tags          StringList `db:"tags"`
	CreatedAt     time.Time  `db:"created_at"`
	UpdatedAt     time.Time  `db:"updated_at"`
	CreatedBy     int64      `db:"created_by"`
	UpdatedBy     int64      `db:"updated_by"`
	CreatedByName string     `db:"created_by_name"`
	UpdatedByName string     `db:"updated_by_name"`
}

// Preview is the first translation, shown in lists.
func (p Phrase) Preview() string {
	if len(p.Meanings) == 0 {
		return ""
	}
	return p.Meanings[0].Translation
}

// PhraseInput is a validated create/update payload.
type PhraseInput struct {
	Phrase   string
	Meanings Meanings
	Notes    string
	Tags     []string
}

type Filter struct {
	Query     string
	Tag       string
	CreatedBy int64
	UpdatedBy int64
	Page      int // 1-based
}

const phraseSelect = `
	SELECT p.id, p.phrase, p.meanings, p.notes, p.created_at, p.updated_at, p.created_by, p.updated_by,
		cu.name AS created_by_name, uu.name AS updated_by_name,
		(SELECT json_group_array(t.name ORDER BY t.name)
			FROM phrase_tags pt JOIN tags t ON t.id = pt.tag_id
			WHERE pt.phrase_id = p.id) AS tags
	FROM phrases p
	JOIN users cu ON cu.id = p.created_by
	JOIN users uu ON uu.id = p.updated_by`

func (db *DB) GetPhrase(id int64) (Phrase, error) {
	var phrase Phrase
	err := db.Get(&phrase, phraseSelect+` WHERE p.id = ?`, id)
	if isNotFoundError(err) {
		return phrase, ErrNotFound
	}
	return phrase, err
}

// PhraseIDByText finds an entry with the same uniqueness key, e.g. to link
// to it after a duplicate error.
func (db *DB) PhraseIDByText(phrase string) (int64, error) {
	var id int64
	err := db.Get(&id, `SELECT id FROM phrases WHERE phrase_key = ?`, Key(phrase))
	if isNotFoundError(err) {
		return 0, ErrNotFound
	}
	return id, err
}

// ListPhrases returns a page of phrases matching the filter, and whether
// there are more pages. Without a search query it's the "recently added" view.
func (db *DB) ListPhrases(filter Filter, pageSize int) ([]Phrase, bool, error) {
	var (
		query     = phraseSelect
		where     []string
		args      []any
		orderBy   = `p.created_at DESC, p.id DESC`
		orderArgs []any
	)

	if normalized := Normalize(filter.Query); normalized != "" {
		prefix := likePrefix(normalized)
		if len([]rune(normalized)) < 3 {
			// Trigram index can't match less than 3 chars.
			where = append(where, `p.phrase_norm LIKE ? ESCAPE '\'`)
			args = append(args, prefix)
			orderBy = `p.phrase_norm`
		} else {
			query += ` JOIN phrases_fts ON phrases_fts.rowid = p.id`
			where = append(where, `phrases_fts MATCH ?`)
			args = append(args, trigramQuery(normalized))
			// Exact match (diacritics included) first, then prefix matches, then fuzzy.
			orderBy = `p.phrase_key = ? DESC, (p.phrase_norm LIKE ? ESCAPE '\') DESC, phrases_fts.rank, p.id`
			orderArgs = []any{Key(filter.Query), prefix}
		}
	}
	if filter.Tag != "" {
		where = append(where, `EXISTS (SELECT 1 FROM phrase_tags pt JOIN tags t ON t.id = pt.tag_id
			WHERE pt.phrase_id = p.id AND t.name = ?)`)
		args = append(args, filter.Tag)
	}
	if filter.CreatedBy != 0 {
		where = append(where, `p.created_by = ?`)
		args = append(args, filter.CreatedBy)
	}
	if filter.UpdatedBy != 0 {
		where = append(where, `p.updated_by = ?`)
		args = append(args, filter.UpdatedBy)
	}

	if len(where) > 0 {
		query += ` WHERE ` + strings.Join(where, ` AND `)
	}
	query += ` ORDER BY ` + orderBy + ` LIMIT ? OFFSET ?`
	args = append(args, orderArgs...)
	page := max(filter.Page, 1)
	// One extra row tells whether there's a next page.
	args = append(args, pageSize+1, (page-1)*pageSize)

	var phrases []Phrase
	if err := db.Select(&phrases, query, args...); err != nil {
		return nil, false, err
	}
	hasMore := len(phrases) > pageSize
	if hasMore {
		phrases = phrases[:pageSize]
	}
	return phrases, hasMore, nil
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

func likePrefix(s string) string {
	return likeEscaper.Replace(s) + "%"
}

func (db *DB) CreatePhrase(in PhraseInput, userID int64) (int64, error) {
	var id int64
	err := db.inTx(func(tx *sqlx.Tx) error {
		now := time.Now().UTC()
		err := tx.Get(&id, `
			INSERT INTO phrases (phrase, phrase_key, phrase_norm, meanings, notes,
				created_at, updated_at, created_by, updated_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
			RETURNING id`,
			in.Phrase, Key(in.Phrase), Normalize(in.Phrase), in.Meanings, in.Notes,
			now, now, userID, userID,
		)
		if err != nil {
			return err
		}
		return setTags(tx, id, in.Tags)
	})
	if isUniqueError(err) {
		return 0, ErrDuplicate
	}
	return id, err
}

func (db *DB) UpdatePhrase(id int64, in PhraseInput, userID int64) error {
	err := db.inTx(func(tx *sqlx.Tx) error {
		err := requireAffected(tx.Exec(`
			UPDATE phrases SET phrase = ?, phrase_key = ?, phrase_norm = ?, meanings = ?, notes = ?,
				updated_at = ?, updated_by = ?
			WHERE id = ?`,
			in.Phrase, Key(in.Phrase), Normalize(in.Phrase), in.Meanings, in.Notes,
			time.Now().UTC(), userID, id,
		))
		if err != nil {
			return err
		}
		return setTags(tx, id, in.Tags)
	})
	if isUniqueError(err) {
		return ErrDuplicate
	}
	return err
}

func (db *DB) DeletePhrase(id int64) error {
	return db.inTx(func(tx *sqlx.Tx) error {
		if err := requireAffected(tx.Exec(`DELETE FROM phrases WHERE id = ?`, id)); err != nil {
			return err
		}
		return pruneTags(tx)
	})
}

// requireAffected turns an update or delete that matched no rows into ErrNotFound.
func requireAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (db *DB) inTx(fn func(tx *sqlx.Tx) error) (err error) {
	tx, err := db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
			return
		}
		err = tx.Commit()
	}()
	return fn(tx)
}
