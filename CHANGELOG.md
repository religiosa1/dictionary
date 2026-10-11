# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Request logging: every request gets an ID, returned in the `X-Request-ID`
  header: the one nginx sends in `X-Request-ID` (see
  `deploy/nginx.conf.example`), else a generated UUIDv7. All log lines while
  handling a request, including errors and recovered panics, carry it, plus
  method, path and the logged-in user. Each request is logged on completion
  with status and duration. Static files aren't logged.

### Changed

- Logging out also signs out of Pocket ID (RP-initiated logout) and returns
  to the home page. Requires setting the client's Logout Callback URL in
  Pocket ID to `DICT_BASE_URL` + `/`, see docs/pocket-id.md. Sessions from
  before the upgrade still log out locally only.
- Static files (htmx, CSS, JS) are revalidated by content hash (ETag) instead
  of being downloaded again on every page load.

## [0.1.1] - 2026-10-11

### Added

- Clicking an existing tag under the tags input on the phrase form adds it to
  the input, if it's not there yet.

### Changed

- Header: the user name and login/logout button wrap to a second row as one
  block on narrow screens.
- Home page: on mobile (up to 720px) the filter selects are collapsed under
  «Фильтры»; expanded by default when a filter is active. Desktop is unchanged.

## [0.1.0] - 2026-10-11

Initial version.

### Added

- Phrases: Polish phrase with one or more meanings (translation and optional
  examples), tags, creation/update time and authors.
- Home page with recently added phrases, fuzzy search on Polish phrases
  (diacritic-insensitive, typo-tolerant) and filters by tag, author and last
  editor.
- Create, edit and delete phrases; adding and removing meanings in the form.
- Tags page with phrase counts.
- Login via OIDC (Pocket ID), reader/editor roles from Pocket ID groups,
  optional public read access.
- `-version` flag; version shown in the page footer.
- Dev server (`cmd/devserver`) with a fake login, for local testing.
- systemd unit, nginx and env examples; Pocket ID setup docs.

[Unreleased]: https://github.com/religiosa1/dictionary/compare/v0.1.1...HEAD
[0.1.1]: https://github.com/religiosa1/dictionary/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/religiosa1/dictionary/releases/tag/v0.1.0
