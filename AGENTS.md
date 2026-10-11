# Agent notes

Small family Polish–Russian dictionary. Ruling principle: simplicity, YAGNI.
Design and its reasoning: SPEC.md — update it when a decision changes.

## Workflow

- Commit directly on `master`, only when asked.
- Before committing: `go test ./...` and `golangci-lint run ./...`.
- UI changes: check in a browser via `go run ./cmd/devserver` (fake login,
  pick any name/role), both desktop and ≤720px width.

## Changelog and releases

- CHANGELOG.md follows Keep a Changelog 1.1.0. Every user-visible change goes
  under `## [Unreleased]` in the same commit (Added/Changed/Fixed/Removed).
- Release X.Y.Z:
  1. Move entries to `## [X.Y.Z] - YYYY-MM-DD`, leave `[Unreleased]` empty.
  2. Update compare links at the bottom.
  3. Commit `Release X.Y.Z: <summary>`, annotated tag `vX.Y.Z`
     (`v` prefix is required for Go to pick the version), push commit and tag.
- The version comes from git via Go build info: no ldflags. Build with
  `CGO_ENABLED=0 go build -buildvcs=true -o dictionary .`

## Frontend

- Server always renders full pages; htmx picks parts with `hx-select`.
  Only fragment endpoints: `GET`/`DELETE /meaning-row`.
- Everything must work without JS (progressive enhancement); htmx and
  `static/app.js` only improve it. JS stays minimal, delegated on `document`
  (htmx swaps the body, so per-element listeners get lost).
- htmx 4 gotchas: inheritance is explicit (`hx-boost:inherited`); 4xx/5xx
  are swapped; 204 is NOT swapped (return empty 200 instead); fetch can't
  follow redirects to the identity provider (login link has `hx-boost="false"`,
  auth redirects use `HX-Redirect`).
- Styling: simple.css (vendored) + overrides in `static/app.css`. Mobile
  breakpoint is 720px, matching simple.css.
- UI language: Russian only, strings inline in templates. Polish text gets
  `lang="pl"`.
- Vendored assets (htmx, simple.css) are pinned: update by downloading an
  exact version, mention it in the changelog.

## Data

- Migrations: append a new numbered file in `internal/store/migrations/`;
  never edit an applied one (`PRAGMA user_version` counts files).
- Case folding/normalization happens in Go: SQLite's NOCASE and lower() are
  ASCII-only, they don't handle Ł or Cyrillic.
- Timestamps are written in UTC.

## Logging

- Inside request handling, log with `reqlog.From(r.Context())`, not `slog`
  directly: it tags lines with the request ID.
