-- Raw OIDC ID token, sent back as id_token_hint on logout. Empty for sessions
-- created before this migration or by non-OIDC logins (dev server).
ALTER TABLE sessions ADD COLUMN id_token TEXT NOT NULL DEFAULT '';
