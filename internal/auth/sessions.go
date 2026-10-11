package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/religiosa1/dictionary/internal/store"
)

// Sessions manages the session cookie. It's independent of OIDC, so other
// login methods (dev server, tests) can use it too.
type Sessions struct {
	db     *store.DB
	secure bool
}

func NewSessions(db *store.DB, secureCookies bool) *Sessions {
	return &Sessions{db: db, secure: secureCookies}
}

// Start creates a session for the user and redirects to next, if it's a local path.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, user store.User, next string) {
	if err := s.db.DeleteExpiredSessions(); err != nil {
		slog.Error("error deleting expired sessions", "err", err)
	}
	token, err := s.db.CreateSession(user.ID, sessionTTL)
	if err != nil {
		slog.Error("error creating session", "err", err)
		http.Error(w, "Не удалось войти", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, sessionCookie, token, sessionTTL)
	slog.Info("user logged in", "user", user.Name, "role", user.Role)
	http.Redirect(w, r, safeRedirect(next), http.StatusFound)
}

func (s *Sessions) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		if err := s.db.DeleteSession(cookie.Value); err != nil {
			slog.Error("error deleting session", "err", err)
		}
	}
	s.setCookie(w, sessionCookie, "", -1)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

type userKey struct{}

// LoadUser puts the session's user (if any) into the request context.
func (s *Sessions) LoadUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		user, err := s.db.SessionUser(cookie.Value)
		switch {
		case errors.Is(err, store.ErrNotFound):
			// Stale cookie, treat as anonymous.
		case err != nil:
			slog.Error("error loading session", "err", err)
		default:
			r = r.WithContext(context.WithValue(r.Context(), userKey{}, &user))
		}
		next.ServeHTTP(w, r)
	})
}

// UserFrom returns the logged-in user, or nil for anonymous requests.
func UserFrom(ctx context.Context) *store.User {
	user, _ := ctx.Value(userKey{}).(*store.User)
	return user
}

// setCookie sets an auth cookie; negative ttl deletes it.
func (s *Sessions) setCookie(w http.ResponseWriter, name, value string, ttl time.Duration) {
	maxAge := int(ttl.Seconds())
	if ttl < 0 {
		maxAge = -1
	}
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// safeRedirect only allows local paths, preventing open redirects.
func safeRedirect(next string) string {
	if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, `/\`) {
		return "/"
	}
	return next
}
