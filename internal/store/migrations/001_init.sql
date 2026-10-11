CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  oidc_sub      TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('reader', 'editor')),
  created_at    DATETIME NOT NULL,
  last_login_at DATETIME NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE phrases (
  id          INTEGER PRIMARY KEY,
  phrase      TEXT NOT NULL,
  phrase_key  TEXT NOT NULL UNIQUE,
  phrase_norm TEXT NOT NULL,
  meanings    TEXT NOT NULL CHECK (json_valid(meanings)),
  created_at  DATETIME NOT NULL,
  updated_at  DATETIME NOT NULL,
  created_by  INTEGER NOT NULL REFERENCES users(id),
  updated_by  INTEGER NOT NULL REFERENCES users(id)
);
CREATE INDEX phrases_created_at ON phrases(created_at);
CREATE INDEX phrases_created_by ON phrases(created_by);
CREATE INDEX phrases_updated_by ON phrases(updated_by);

CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE
);

CREATE TABLE phrase_tags (
  phrase_id INTEGER NOT NULL REFERENCES phrases(id) ON DELETE CASCADE,
  tag_id    INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (phrase_id, tag_id)
);
CREATE INDEX phrase_tags_tag ON phrase_tags(tag_id);

-- External-content FTS index over phrases.phrase_norm, kept in sync by triggers.
CREATE VIRTUAL TABLE phrases_fts USING fts5(
  phrase_norm, content='phrases', content_rowid='id', tokenize='trigram'
);

CREATE TRIGGER phrases_fts_insert AFTER INSERT ON phrases BEGIN
  INSERT INTO phrases_fts(rowid, phrase_norm) VALUES (new.id, new.phrase_norm);
END;

CREATE TRIGGER phrases_fts_delete AFTER DELETE ON phrases BEGIN
  INSERT INTO phrases_fts(phrases_fts, rowid, phrase_norm) VALUES ('delete', old.id, old.phrase_norm);
END;

CREATE TRIGGER phrases_fts_update AFTER UPDATE OF phrase_norm ON phrases BEGIN
  INSERT INTO phrases_fts(phrases_fts, rowid, phrase_norm) VALUES ('delete', old.id, old.phrase_norm);
  INSERT INTO phrases_fts(rowid, phrase_norm) VALUES (new.id, new.phrase_norm);
END;
