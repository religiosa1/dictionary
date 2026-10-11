package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/religiosa1/dictionary/internal/reqlog"
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
// idToken is the raw OIDC ID token, empty for non-OIDC logins.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, user store.User, idToken, next string) {
	logger := reqlog.From(r.Context())
	if err := s.db.DeleteExpiredSessions(); err != nil {
		logger.Error("error deleting expired sessions", "err", err)
	}
	token, err := s.db.CreateSession(user.ID, idToken, sessionTTL)
	if err != nil {
		logger.Error("error creating session", "err", err)
		http.Error(w, "Не удалось войти", http.StatusInternalServerError)
		return
	}
	s.setCookie(w, sessionCookie, token, sessionTTL)
	logger.Info("user logged in", "user", user.Name, "role", user.Role)
	http.Redirect(w, r, safeRedirect(next), http.StatusFound)
}

// End deletes the session and clears its cookie. Returns the session's ID
// token, empty if there's none.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) (idToken string) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		idToken, err = s.db.DeleteSession(cookie.Value)
		if err != nil {
			reqlog.From(r.Context()).Error("error deleting session", "err", err)
		}
	}
	s.setCookie(w, sessionCookie, "", -1)
	return idToken
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
			reqlog.From(r.Context()).Error("error loading session", "err", err)
		default:
			reqlog.Add(r.Context(), "user", user.Name)
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
