# Polish–Russian family dictionary

A small web app where family members collect interesting Polish words and
phrases with their Russian meanings, to look them up and memorize them.

- Each entry: a Polish phrase, one or more meanings (translation + optional
  examples), tags, who added and who last changed it.
- Home page shows recently added entries, fuzzy search on Polish phrases
  (diacritic-insensitive and typo-tolerant), filters by tag, author and
  last editor.
- Login via OIDC (PocketID). Roles come from PocketID groups: editors can
  change everything, everyone else is a reader.
- UI is in Russian.

> **Early stage.** This is a first iteration, built for a single family's
> use. Expect breaking changes, including to the database schema.

Built to stay small: Go stdlib HTTP, SQLite (pure-Go driver, FTS5),
`html/template`, htmx 4 with no hand-written JS. Everything is embedded into
a single binary. See [SPEC.md](SPEC.md) for the design and its reasoning.

## Running locally

Requires Go 1.27+. No cgo or C toolchain needed.

### Dev server (no identity provider)

```sh
go run ./cmd/devserver
```

Open <http://127.0.0.1:8080>, click «Войти», and pick any name and role on the
dev login page. Log in again with a different name or role to test what
other users see.

- Data goes to `dev.db` in the current directory; delete it to start over.
- `DICT_LISTEN_ADDR`, `DICT_DB_PATH` and `DICT_PUBLIC_READ` work the same as
  in the real server (see [Configuration](#configuration)).

The dev server is a separate command that swaps the OIDC login for the dev
login page; the app itself is the same code. It refuses to listen on
anything except loopback and is never part of the production binary
(`go build .` builds only the real server). **Don't deploy it**: anyone who
can reach it can log in as anyone.

### Real server with PocketID

See [docs/pocket-id.md](docs/pocket-id.md) for all client and group
settings.

1. Create an OIDC client in PocketID with callback URL
   `http://localhost:8080/auth/callback`.
2. Create a group for editors (default name `dictionary-editors`) and add
   yourself to it.
3. Run:

   ```sh
   DICT_BASE_URL=http://localhost:8080 \
   DICT_OIDC_ISSUER=https://id.example.com \
   DICT_OIDC_CLIENT_ID=... \
   DICT_OIDC_CLIENT_SECRET=... \
   go run .
   ```

The issuer must be reachable at startup, because the server discovers its
endpoints then.

### Tests

```sh
go test ./...
```

Tests use temporary SQLite files and log in by creating sessions directly in
the database, so they need neither PocketID nor network access.

## Configuration

Environment variables:

| Variable                  | Default                | Notes                                              |
| ------------------------- | ---------------------- | -------------------------------------------------- |
| `DICT_LISTEN_ADDR`        | `127.0.0.1:8080`       |                                                    |
| `DICT_DB_PATH`            | `dictionary.db`        | created and migrated on startup                    |
| `DICT_BASE_URL`           | required               | public URL; `https://` turns on `Secure` cookies   |
| `DICT_OIDC_ISSUER`        | required               |                                                    |
| `DICT_OIDC_CLIENT_ID`     | required               |                                                    |
| `DICT_OIDC_CLIENT_SECRET` | required               |                                                    |
| `DICT_EDITOR_GROUP`       | `dictionary-editors`   | PocketID group whose members become editors        |
| `DICT_PUBLIC_READ`        | `false`                | `true` lets anonymous visitors read                |

A user's role is refreshed from their groups on each login.

## Deployment

```sh
CGO_ENABLED=0 go build -buildvcs=true -o dictionary .
```

`dictionary -version` prints the build version, which is also shown in the
page footer. Go stamps it from git automatically, so:

- build from a git checkout, with `git` installed, and build the package
  (`.`), not single files; `go run` never stamps it, showing `(devel)`;
- `-buildvcs=true` makes the build fail instead of silently omitting the
  version when it can't be stamped.

| Checkout state                         | Version                                 |
| -------------------------------------- | --------------------------------------- |
| HEAD exactly at tag `v0.1.0`           | `v0.1.0`                                |
| commits after the tag                  | `v0.1.1-0.<date>-<commit>`              |
| no tags                                | `v0.0.0-<date>-<commit>`                |
| uncommitted changes                    | any of the above with `+dirty`          |

To release: `git tag -a v0.1.0 -m v0.1.0 && git push --tags`, then build.
Check a binary without running it: `go version -m dictionary`.

Example files in [`deploy/`](deploy):

- `dictionary.service`: systemd unit; the database lives in
  `/var/lib/dictionary`.
- `env.example`: goes to `/etc/dictionary/env`, readable by root only.
  Client and group setup in Pocket ID: [docs/pocket-id.md](docs/pocket-id.md).
- `nginx.conf.example`: TLS reverse proxy. Keep `proxy_set_header Host $host`:
  the CSRF protection relies on it.

## License

[MIT](LICENSE)
