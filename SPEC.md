# Polish–Russian family dictionary — SPEC

A small web app where family members record interesting Polish words and
phrases with their Russian meanings, to look them up and memorize them.

Ruling principle: **small, simple, easy to reason about.** When in doubt,
pick the option with less code and fewer moving parts.

## 1. Stack

| Concern        | Choice                                                                  |
| -------------- | ----------------------------------------------------------------------- |
| Language       | Go (current toolchain, 1.27)                                            |
| HTTP           | stdlib `net/http` `ServeMux` with method patterns, no framework         |
| DB             | SQLite via `modernc.org/sqlite` (pure Go, no cgo, FTS5 + JSON built in) |
| DB access      | `jmoiron/sqlx` over hand-written SQL, no ORM                            |
| Migrations     | `Migrator` copied from `git-webhook-receiver/internal/sqlhelpers`       |
| Templates      | `html/template`, embedded                                               |
| Frontend       | htmx 4 (vendored, pinned), simple.css (vendored), minimal JS (`app.js`) |
| Auth           | OIDC (PocketID) via `coreos/go-oidc/v3` + `golang.org/x/oauth2`, PKCE   |
| Sessions       | random token cookie, sessions table in SQLite                           |
| CSRF           | stdlib `http.CrossOriginProtection`                                     |
| Logging        | `log/slog`                                                              |
| Deployment     | single static binary (`CGO_ENABLED=0`), systemd unit, behind nginx     |

Everything (templates, static files, SQL migrations) is embedded with
`//go:embed`, so deployment is one binary plus an env file.

## 2. Project layout

```
main.go                      config from env, wiring, http.Server, graceful shutdown
cmd/devserver/               local server with a fake login page instead of OIDC
internal/
  store/                     everything SQL
    db.go                    Open(): DSN pragmas, run migrations
    migrator.go              copied Migrator (PRAGMA user_version, forward-only)
    migrations/001_init.sql
    phrases.go               CRUD, list/filter, search
    users.go                 upsert on login, list for filter dropdowns
    sessions.go
    tags.go
    normalize.go             Polish normalization + trigram query builder
  auth/                      OIDC login/callback/logout; Sessions: session cookie, user middleware
  web/                       handlers, form parsing, template rendering
    templates/*.tmpl
    static/                  htmx.min.js, simple.css, app.css (tiny overrides)
deploy/
  dictionary.service
  nginx.conf.example
  env.example
```

Three internal packages. `web` depends on `store` and `auth`; `auth` depends
on `store`.

## 3. Configuration (env)

| Variable                  | Example / default                      | Notes                                         |
| ------------------------- | -------------------------------------- | --------------------------------------------- |
| `DICT_LISTEN_ADDR`        | `127.0.0.1:8080`                       |                                               |
| `DICT_DB_PATH`            | `/var/lib/dictionary/dictionary.db`    |                                               |
| `DICT_BASE_URL`           | `https://slownik.example.com`          | builds OIDC redirect URL; `https` ⇒ `Secure` cookies |
| `DICT_OIDC_ISSUER`        | `https://id.example.com`               | PocketID                                      |
| `DICT_OIDC_CLIENT_ID`     |                                        |                                               |
| `DICT_OIDC_CLIENT_SECRET` |                                        |                                               |
| `DICT_EDITOR_GROUP`       | `dictionary-editors`                   | PocketID group granting editor role           |
| `DICT_PUBLIC_READ`        | `false`                                | `true` ⇒ anonymous visitors can read          |

Missing required values ⇒ refuse to start with a clear error.

## 4. Data model

```sql
CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  oidc_sub      TEXT NOT NULL UNIQUE,
  name          TEXT NOT NULL,
  role          TEXT NOT NULL CHECK (role IN ('reader', 'editor')),
  created_at    TEXT NOT NULL,
  last_login_at TEXT NOT NULL
);

CREATE TABLE sessions (
  token_hash TEXT PRIMARY KEY,            -- sha256(cookie token), raw token never stored
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at TEXT NOT NULL,
  id_token   TEXT NOT NULL DEFAULT ''   -- raw OIDC ID token, for id_token_hint on logout
);

CREATE TABLE phrases (
  id          INTEGER PRIMARY KEY,
  phrase      TEXT NOT NULL,              -- as typed, e.g. "Łaska"
  phrase_key  TEXT NOT NULL UNIQUE,       -- Go strings.ToLower(trimmed phrase); uniqueness
  phrase_norm TEXT NOT NULL,              -- lowercase, diacritics stripped; search only
  meanings    TEXT NOT NULL CHECK (json_valid(meanings)),
  created_at  TEXT NOT NULL,
  updated_at  TEXT NOT NULL,
  created_by  INTEGER NOT NULL REFERENCES users(id),
  updated_by  INTEGER NOT NULL REFERENCES users(id)
);
CREATE INDEX phrases_created_at ON phrases(created_at);
CREATE INDEX phrases_created_by ON phrases(created_by);
CREATE INDEX phrases_updated_by ON phrases(updated_by);

CREATE TABLE tags (
  id   INTEGER PRIMARY KEY,
  name TEXT NOT NULL UNIQUE               -- lowercased in Go
);

CREATE TABLE phrase_tags (
  phrase_id INTEGER NOT NULL REFERENCES phrases(id) ON DELETE CASCADE,
  tag_id    INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (phrase_id, tag_id)
);
CREATE INDEX phrase_tags_tag ON phrase_tags(tag_id);

CREATE VIRTUAL TABLE phrases_fts USING fts5(
  phrase_norm, content='phrases', content_rowid='id', tokenize='trigram'
);
-- + AFTER INSERT/UPDATE/DELETE triggers on phrases keeping phrases_fts in sync
```

Notes:

- **Meanings JSON** — ordered array, at least one element:
  `[{"translation": "благодать", "examples": ["Łaska boża", "..."]}]`.
  `examples` may be empty. The first meaning's translation is the *preview*.
- **Timestamps** — UTC RFC 3339 text, set in Go. Sorts lexicographically.
- **Why `phrase_key` / lowercased tags in Go** — SQLite's `NOCASE` and `lower()`
  are ASCII-only; they don't fold `Ł` or Cyrillic. Folding in Go is the
  simplest correct option.
- **Uniqueness** is on `phrase_key`, *not* on `phrase_norm`: "łaska" and
  "laska" are different words. A word with several senses is one entry with
  several meanings. Duplicate on create ⇒ form error with a link to the
  existing entry.
- **Tags** — orphaned tags (no phrases) are deleted in the same transaction
  that removes their last use.
- **Users are never deleted**, so `created_by`/`updated_by` FKs stay valid.
- No revision history; only last modifier is recorded.

### DB connection

DSN pragmas: `journal_mode(WAL)`, `foreign_keys(1)`, `busy_timeout(5000)`,
`synchronous(NORMAL)`, and `_txlock=immediate` so write transactions take
the lock upfront instead of failing with `SQLITE_BUSY` on upgrade.

Migrations: `migrations/*.sql` embedded, read with `fs.ReadDir` (sorted by
name), passed as `[]string` to `Migrator.Migrate`.

## 5. Search

FTS5 alone isn't fuzzy, so:

1. **Normalize** (`normalize.go`): trim, `strings.ToLower`, map
   `ą→a ć→c ę→e ł→l ń→n ó→o ś→s ź→z ż→z`, collapse whitespace.
   Hand-written map — Unicode decomposition doesn't cover `ł`, and it avoids
   pulling in `x/text`. Applied both when saving (`phrase_norm`) and to the
   query.
2. **Index** `phrase_norm` with FTS5 `trigram` tokenizer.
3. **Query**: split the normalized query into overlapping trigrams and OR them:
   `"psz" OR "szc" OR "zcz" OR ...` (each quoted, `"` doubled).
   Typos break only a few trigrams, so the right entry still ranks high.
4. **Rank**: exact match (diacritics included) first, then prefix matches,
   then `bm25`:
   `ORDER BY p.phrase_key = :key DESC, (p.phrase_norm LIKE :q || '%') DESC, phrases_fts.rank`.
   So "laska" ranks above "łaska" and vice versa.
5. Query shorter than 3 chars ⇒ plain `phrase_norm LIKE :q || '%'`.

Search combines with filters (AND). Without a query, results are ordered by
`created_at DESC` — that's the "recently added" view.

Russian search: deferred. Would be a second FTS table fed from
`json_each(meanings)`.

## 6. Auth & permissions

### Login flow

- `GET /auth/login?next=/path` — generate state, nonce, PKCE verifier; store
  them plus `next` in a short-lived (10 min) HttpOnly cookie; redirect to
  PocketID. Scopes: `openid profile groups`.
- `GET /auth/callback` — check state, exchange code with verifier, verify ID
  token and nonce. Upsert user by `sub`:
  - `name` ← `name` claim, falling back to `preferred_username`;
  - `role` ← `editor` if `DICT_EDITOR_GROUP` ∈ `groups` claim, else `reader`.
  Create session, set cookie, redirect to `next` (only if it's a local path
  starting with `/` and not `//`; else `/`).
- `POST /auth/logout` — delete session row, clear cookie, then RP-initiated
  logout: redirect to PocketID's `end_session_endpoint` (from discovery) with
  `client_id`, `id_token_hint` and `post_logout_redirect_uri` = base URL +
  `/`. PocketID ends its own login and redirects back home. Without a stored
  ID token PocketID would ask to confirm, so such sessions log out locally
  only. Other apps using PocketID keep their sessions (no back-channel
  logout).

Who may log in at all is controlled in PocketID (allowed groups on the OIDC
client). The app trusts anyone PocketID lets through as at least a reader.

Role is refreshed on each login only, so a group change takes effect on the
user's next login (or when their session expires).

### Sessions

Cookie `session`: `crypto/rand.Text()` (≥128 bits); HttpOnly, SameSite=Lax,
`Secure` when base URL is https, 30-day fixed lifetime. DB stores the
sha256 hash. Expired sessions are purged on startup and on each login.

### Permission matrix

|                    | anonymous                       | reader | editor |
| ------------------ | ------------------------------- | ------ | ------ |
| read pages         | only if `DICT_PUBLIC_READ=true` | ✓      | ✓      |
| create/edit/delete | ✗ (redirect to login)           | ✗ 403  | ✓      |

Middleware loads the current user (if any) into the request context;
handlers are wrapped with `requireReader` / `requireEditor`.

### CSRF

`http.CrossOriginProtection` wraps the whole mux. nginx must pass
`Host` through (`proxy_set_header Host $host`) for the Origin fallback;
modern browsers send `Sec-Fetch-Site` anyway. SameSite=Lax is a second layer.

## 7. Routes

| Method | Path                       | Access | Purpose                                     |
| ------ | -------------------------- | ------ | ------------------------------------------- |
| GET    | `/`                        | read   | search + filters + results (recent by default) |
| GET    | `/phrases/{id}`            | read   | phrase page                                 |
| GET    | `/phrases/new`             | editor | create form                                 |
| POST   | `/phrases`                 | editor | create → redirect to phrase page            |
| GET    | `/phrases/{id}/edit`       | editor | edit form                                   |
| POST   | `/phrases/{id}`            | editor | update → redirect to phrase page            |
| GET    | `/phrases/{id}/delete`     | editor | confirmation page                           |
| POST   | `/phrases/{id}/delete`     | editor | delete → redirect to `/`                    |
| GET    | `/meaning-row`             | editor | empty meaning fieldset (htmx fragment)      |
| DELETE | `/meaning-row`             | editor | empty 200, lets htmx remove a fieldset      |
| GET    | `/tags`                    | read   | all tags with counts, links to `/?tag=`     |
| GET    | `/auth/login`, `/auth/callback` | — | OIDC                                        |
| POST   | `/auth/logout`             | —      | RP-initiated logout                         |
| GET    | `/static/...`              | —      | embedded assets                             |

Only GET/POST, so every form works without JS. Writes use
POST-redirect-GET.

### Query params on `/`

`q` (search), `tag` (single tag name), `created_by`, `updated_by`
(user ids), `page` (1-based). All combined with AND. Page size: 30
(constant, to be tuned). Pager: "← Назад / Далее →" links.

## 8. Frontend / htmx

Guiding idea: **the server always renders full pages**; htmx only decides
what part of the response to swap. No `HX-Request` branching in handlers.

- `<body hx-boost:inherited="true">` — links and forms become AJAX
  navigations with history, falling back to normal navigation without JS.
  (htmx 4 inheritance is explicit, hence `:inherited`.) The login link and
  the logout form have `hx-boost="false"`: fetch() can't follow a redirect
  to PocketID. For the
  same reason, auth redirects on htmx requests use the `HX-Redirect` header.
- htmx 4 swaps 4xx/5xx responses by default, so validation errors (422/409)
  and error pages render without extra config.
- **Live search**: the filter form is a plain `GET /` form with a submit
  button. htmx attributes on it:
  `hx-get="/" hx-trigger="submit, input target:input delay:300ms, change target:select"
  hx-select="#results" hx-target="#results" hx-swap="outerHTML"
  hx-push-url="true"` — server renders the whole page, htmx picks out the
  results block. Rendering a full page per keystroke is negligible at this
  scale.
- **Meanings editor**: each meaning is a `<fieldset>` with one
  `translation` input and one `examples` textarea (one example per line).
  The handler zips `r.Form["translation"]` and `r.Form["examples"]` by
  position — no indexed field names.
  - "Добавить значение": `hx-get="/meaning-row" hx-target="#meanings"
    hx-swap="beforeend"`. Only fragment endpoint in the app (renders the
    `meaning-row` template).
    A blank fieldset is rendered only when there are no meanings yet (new
    phrase); otherwise the blank one is inside `<noscript>`, for no-JS users.
  - "Удалить значение": `hx-delete="/meaning-row" hx-target="closest fieldset"
    hx-swap="delete"`; the endpoint returns an empty 200 (htmx skips swaps
    on 204). Without JS: clear the fields; fieldsets with empty translation
    are dropped on save.
- **Tags input**: single text input, comma-separated, with existing tags
  listed below as a hint. (`<datalist>` was dropped: it completes the whole
  input value, so it doesn't work for comma-separated lists.)
- **Delete**: separate confirmation page with a POST form. Works the same
  with or without JS.
- **htmx 4.0.0**, vendored at `internal/web/static/htmx.min.js`.

### Pages

- **Layout**: header (title, "Добавить" for editors, "Теги", user name +
  logout / login link), main, simple.css. `<html lang="ru">`; Polish text
  wrapped in `lang="pl"`.
- **Home**: filter form (search input, tag `<select>`, "Добавил" `<select>`,
  "Изменил" `<select>`), results list: phrase → first translation, tags as
  small links. Each item links to the phrase page.
- **Phrase page**: phrase; numbered meanings with examples; tags (links to
  `/?tag=`); "добавил X, 11.10.2026 · изменил Y, 12.10.2026";
  edit/delete links for editors.
- **Form**: phrase input, meanings fieldsets, tags input. Validation errors
  re-render the form with values kept and messages in Russian.
- **Error pages**: 403/404/500, minimal.

UI language: Russian only, strings inline in templates (no i18n layer).
Dates shown as `DD.MM.YYYY` in server local time.

### Templates

`layout.tmpl` + one file per page. The results list on home is its own
`{{define "results"}}` block, so fragment responses (§12) can be added later
without restructuring. Each page is parsed as its own template
set (layout cloned + page) to avoid block-name collisions. Parsed once at
startup.

## 9. Validation rules

- `phrase`: trimmed, non-empty, ≤ 200 chars; unique by `phrase_key`.
- Meanings: ≥ 1 with non-empty translation; translation ≤ 500 chars;
  examples: lines trimmed, blank lines dropped.
- Tags: split on `,`, trimmed, lowercased, empty dropped, deduplicated,
  each ≤ 50 chars.

## 10. Deployment

- Build: `CGO_ENABLED=0 go build -o dictionary .`
- `deploy/dictionary.service`: `DynamicUser=yes`, `StateDirectory=dictionary`,
  `EnvironmentFile=/etc/dictionary/env`, `Restart=on-failure`, standard
  hardening (`ProtectSystem=strict`, `ProtectHome=yes`, `NoNewPrivileges=yes`,
  `PrivateTmp=yes`).
- `deploy/nginx.conf.example`: TLS, `proxy_pass http://127.0.0.1:8080`,
  `proxy_set_header Host $host`, `X-Forwarded-Proto`.
- Graceful shutdown on SIGTERM.

## 11. Testing

- `store`: tests against a temp-file DB — migrations apply, CRUD, uniqueness,
  tag pruning, filters, search ranking on a small fixture set.
- `normalize`: table tests (diacritics, trigrams, quoting).
- `web`: `httptest` for the permission matrix and form parsing; OIDC
  bypassed by inserting a session directly.
- Manual testing: `go run ./cmd/devserver`. It wires the same app with a
  login page that signs in as any name/role. Session handling (`auth.Sessions`)
  is separate from the OIDC flow (`auth.Auth`), so both logins share it.
  Loopback-only, and not part of the production build.

## 12. Deferred / out of scope

- Styling (simple.css is a placeholder; revisit).
- Page size tuning (30).
- Search relevance threshold to cut weak trigram matches.
- Russian search (FTS on meanings).
- Backups (e.g. nightly `VACUUM INTO` systemd timer).
- Revision history, admin UI, JSON API, multi-tag filter, i18n.
- Back-channel logout: PocketID notifying the app when a user is disabled
  or loses group access, to drop their sessions before they expire.
- Fragment responses: render only the `results` block when
  `HX-Request-Type: partial` (drop `hx-select`, keep `hx-target`), and send
  `Vary: HX-Request-Type`. Saves ~1 KB gzipped per search request.
