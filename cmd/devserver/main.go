// Command devserver runs the app locally without an OIDC provider.
// Its login page lets you sign in as any name with any role, so it refuses
// to listen on anything but loopback. Never deploy it.
package main

import (
	"cmp"
	"fmt"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/religiosa1/dictionary/internal/auth"
	"github.com/religiosa1/dictionary/internal/reqlog"
	"github.com/religiosa1/dictionary/internal/store"
	"github.com/religiosa1/dictionary/internal/web"
)

var loginPage = template.Must(template.New("login").Parse(`<!doctype html>
<html lang="ru">
<head>
  <meta charset="utf-8">
  <title>Dev login</title>
  <link rel="stylesheet" href="/static/simple.min.css">
</head>
<body>
<main>
  <h1>Dev login</h1>
  <p>Development server: no real authentication.</p>
  <form method="post" action="/auth/login">
    <input type="hidden" name="next" value="{{.}}">
    <label for="name">Name</label>
    <input id="name" name="name" value="Dev" required>
    <label for="role">Role</label>
    <select id="role" name="role">
      <option value="editor">editor</option>
      <option value="reader">reader</option>
    </select>
    <button type="submit">Log in</button>
  </form>
</main>
</body>
</html>`))

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	listenAddr := cmp.Or(os.Getenv("DICT_LISTEN_ADDR"), "127.0.0.1:8080")
	dbPath := cmp.Or(os.Getenv("DICT_DB_PATH"), "dev.db")
	publicRead, _ := strconv.ParseBool(os.Getenv("DICT_PUBLIC_READ"))

	if !isLoopback(listenAddr) {
		return fmt.Errorf("devserver only listens on loopback, got %q", listenAddr)
	}

	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	sessions := auth.NewSessions(db, false)
	app, err := web.New(db, publicRead)
	if err != nil {
		return err
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/login", func(w http.ResponseWriter, r *http.Request) {
		if err := loginPage.Execute(w, r.URL.Query().Get("next")); err != nil {
			reqlog.From(r.Context()).Error("error rendering login page", "err", err)
		}
	})
	mux.HandleFunc("POST /auth/login", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.PostFormValue("name"))
		role := store.Role(r.PostFormValue("role"))
		if name == "" || (role != store.RoleEditor && role != store.RoleReader) {
			http.Error(w, "name and a valid role are required", http.StatusBadRequest)
			return
		}
		user, err := db.UpsertUser("dev:"+name, name, role)
		if err != nil {
			reqlog.From(r.Context()).Error("error creating dev user", "err", err)
			http.Error(w, "error creating user", http.StatusInternalServerError)
			return
		}
		sessions.Start(w, r, user, "", r.PostFormValue("next"))
	})
	mux.HandleFunc("POST /auth/logout", func(w http.ResponseWriter, r *http.Request) {
		sessions.End(w, r)
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	app.Register(mux)

	slog.Warn("DEV SERVER: anyone can log in as anyone", "addr", "http://"+listenAddr, "db", dbPath)
	return http.ListenAndServe(listenAddr, web.Handler(sessions, mux))
}

func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
