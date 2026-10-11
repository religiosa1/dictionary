package store

import (
	"github.com/jmoiron/sqlx"
)

type TagCount struct {
	Name  string `db:"name"`
	Count int    `db:"count"`
}

// ListTags returns all tags with their phrase counts.
func (db *DB) ListTags() ([]TagCount, error) {
	var tags []TagCount
	err := db.Select(&tags, `
		SELECT t.name, count(*) AS count
		FROM tags t JOIN phrase_tags pt ON pt.tag_id = t.id
		GROUP BY t.id
		ORDER BY t.name`)
	return tags, err
}

// setTags replaces phrase's tags, creating new ones and dropping unused.
func setTags(tx *sqlx.Tx, phraseID int64, tags []string) error {
	if _, err := tx.Exec(`DELETE FROM phrase_tags WHERE phrase_id = ?`, phraseID); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.Exec(`INSERT INTO tags (name) VALUES (?) ON CONFLICT (name) DO NOTHING`, tag); err != nil {
			return err
		}
		_, err := tx.Exec(`
			INSERT INTO phrase_tags (phrase_id, tag_id)
			SELECT ?, id FROM tags WHERE name = ?`,
			phraseID, tag,
		)
		if err != nil {
			return err
		}
	}
	return pruneTags(tx)
}

func pruneTags(tx *sqlx.Tx) error {
	_, err := tx.Exec(`DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM phrase_tags)`)
	return err
}
