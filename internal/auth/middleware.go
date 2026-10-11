package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/religiosa1/dictionary/internal/store"
)

type userKey struct{}

// LoadUser puts the session's user (if any) into the request context.
// Independent of OIDC, so it works without a reachable provider (e.g. tests).
func LoadUser(db *store.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie(sessionCookie)
			if err != nil {
				next.ServeHTTP(w, r)
				return
			}
			user, err := db.SessionUser(cookie.Value)
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
}

// UserFrom returns the logged-in user, or nil for anonymous requests.
func UserFrom(ctx context.Context) *store.User {
	user, _ := ctx.Value(userKey{}).(*store.User)
	return user
}
