// Package web holds HTTP handlers and HTML rendering.
package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/religiosa1/dictionary/internal/auth"
	"github.com/religiosa1/dictionary/internal/reqlog"
	"github.com/religiosa1/dictionary/internal/store"
	"github.com/religiosa1/dictionary/internal/version"
)

//go:embed templates/*.tmpl
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

const pageSize = 30

type Server struct {
	db         *store.DB
	publicRead bool
	version    string
	pages      map[string]*template.Template
}

func New(db *store.DB, publicRead bool) (*Server, error) {
	pages, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &Server{db: db, publicRead: publicRead, version: version.String(), pages: pages}, nil
}

// Handler wraps mux, holding the app and auth routes, in the request
// middleware. Static assets are served outside it: they need neither sessions
// nor request logs.
func Handler(sessions *auth.Sessions, mux *http.ServeMux) http.Handler {
	root := http.NewServeMux()
	root.Handle("GET /static/", http.StripPrefix("/static/", staticHandler()))
	root.Handle("/", reqlog.Handler(slog.Default(), http.NewCrossOriginProtection().Handler(sessions.LoadUser(mux))))
	return root
}

// staticHandler serves the embedded assets with content-hash ETags. Embedded
// files have no modification time, so without an ETag browsers can't
// revalidate and re-download every asset on each page load.
func staticHandler() http.Handler {
	static, _ := fs.Sub(staticFS, "static")
	etags := map[string]string{}
	// Embedded files are always readable, so a walk error can't happen.
	_ = fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		etags[path] = `"` + hex.EncodeToString(sum[:8]) + `"`
		return nil
	})
	files := http.FileServerFS(static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if etag, ok := etags[r.URL.Path]; ok {
			w.Header().Set("ETag", etag)
			// Revalidate each time: cheap 304s, and a new release shows up at once.
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// Register adds the app routes to mux.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", s.requireReader(s.home))
	mux.HandleFunc("GET /tags", s.requireReader(s.tags))
	mux.HandleFunc("GET /phrases/{id}", s.requireReader(s.showPhrase))
	mux.HandleFunc("GET /phrases/new", s.requireEditor(s.newPhrase))
	mux.HandleFunc("POST /phrases", s.requireEditor(s.createPhrase))
	mux.HandleFunc("GET /phrases/{id}/edit", s.requireEditor(s.editPhrase))
	mux.HandleFunc("POST /phrases/{id}", s.requireEditor(s.updatePhrase))
	mux.HandleFunc("GET /phrases/{id}/delete", s.requireEditor(s.confirmDelete))
	mux.HandleFunc("POST /phrases/{id}/delete", s.requireEditor(s.deletePhrase))
	mux.HandleFunc("GET /meaning-row", s.requireEditor(s.meaningRow))
	mux.HandleFunc("DELETE /meaning-row", s.requireEditor(removeMeaningRow))
	mux.HandleFunc("/", s.notFound)
}

func (s *Server) requireReader(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.publicRead && auth.UserFrom(r.Context()) == nil {
			redirectToLogin(w, r)
			return
		}
		next(w, r)
	}
}

func (s *Server) requireEditor(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFrom(r.Context())
		if user == nil {
			redirectToLogin(w, r)
			return
		}
		if !user.IsEditor() {
			s.renderError(w, r, http.StatusForbidden, "Недостаточно прав для редактирования.")
			return
		}
		next(w, r)
	}
}

func redirectToLogin(w http.ResponseWriter, r *http.Request) {
	next := "/"
	if r.Method == http.MethodGet {
		next = r.URL.RequestURI()
	}
	loginURL := "/auth/login?next=" + url.QueryEscape(next)
	// htmx requests go through fetch(), which can't follow a redirect to the
	// external identity provider; ask htmx for a full navigation instead.
	if r.Header.Get("HX-Request") == "true" {
		w.Header().Set("HX-Redirect", loginURL)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusSeeOther)
}

// view is the data passed to every page template.
type view struct {
	User       *store.User
	CanEdit    bool
	CurrentURL string
	Version    string
	Page       any
}

func parseTemplates() (map[string]*template.Template, error) {
	funcs := template.FuncMap{
		"date":  func(t time.Time) string { return t.Local().Format("02.01.2006") },
		"lines": func(lines []string) string { return strings.Join(lines, "\n") },
		// Form limits, so the HTML maxlength matches server validation.
		"maxPhraseLen":      func() int { return maxPhraseLen },
		"maxTranslationLen": func() int { return maxTranslationLen },
	}
	layout, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/layout.tmpl")
	if err != nil {
		return nil, err
	}
	files, err := fs.Glob(templatesFS, "templates/*.tmpl")
	if err != nil {
		return nil, err
	}
	pages := map[string]*template.Template{}
	for _, file := range files {
		name := strings.TrimSuffix(strings.TrimPrefix(file, "templates/"), ".tmpl")
		if name == "layout" {
			continue
		}
		// Each page gets its own copy of the layout, so their blocks don't collide.
		page, err := template.Must(layout.Clone()).ParseFS(templatesFS, file)
		if err != nil {
			return nil, fmt.Errorf("parsing %s: %w", file, err)
		}
		pages[name] = page
	}
	return pages, nil
}

// render executes a page's named template; "layout" renders the full page.
// Output is buffered so a template error still yields a clean 500.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, page, name string, data any) {
	user := auth.UserFrom(r.Context())
	v := view{
		User:       user,
		CanEdit:    user.IsEditor(),
		CurrentURL: r.URL.RequestURI(),
		Version:    s.version,
		Page:       data,
	}
	var buf bytes.Buffer
	if err := s.pages[page].ExecuteTemplate(&buf, name, v); err != nil {
		reqlog.From(r.Context()).Error("error rendering template", "page", page, "err", err)
		http.Error(w, "Внутренняя ошибка", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

func (s *Server) renderPage(w http.ResponseWriter, r *http.Request, status int, page string, data any) {
	s.render(w, r, status, page, "layout", data)
}

type errorPage struct {
	Status  int
	Message string
}

func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, message string) {
	s.renderPage(w, r, status, "error", errorPage{Status: status, Message: message})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	reqlog.From(r.Context()).Error("request failed", "err", err)
	s.renderError(w, r, http.StatusInternalServerError, "Что-то пошло не так.")
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	s.renderError(w, r, http.StatusNotFound, "Страница не найдена.")
}
