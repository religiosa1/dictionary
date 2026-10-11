package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/religiosa1/dictionary/internal/store"
)

func TestSafeRedirect(t *testing.T) {
	cases := map[string]string{
		"":                     "/",
		"/phrases/1?x=y":       "/phrases/1?x=y",
		"//evil.example":       "/",
		`/\evil.example`:       "/",
		"https://evil.example": "/",
	}
	for in, want := range cases {
		if got := safeRedirect(in); got != want {
			t.Errorf("safeRedirect(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestLogout(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	user, err := db.UpsertUser("sub", "Alice", store.RoleReader)
	if err != nil {
		t.Fatal(err)
	}
	a := &Auth{
		db:         db,
		sessions:   NewSessions(db, false),
		oauth:      oauth2.Config{ClientID: "dict"},
		baseURL:    "https://dict.example.com",
		endSession: "https://id.example.com/api/oidc/end-session",
	}

	logout := func(idToken string) *url.URL {
		t.Helper()
		token, err := db.CreateSession(user.ID, idToken, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/auth/logout", nil)
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: token})
		rec := httptest.NewRecorder()
		a.Logout(rec, req)
		if _, err := db.SessionUser(token); err == nil {
			t.Error("session still exists after logout")
		}
		loc, err := url.Parse(rec.Header().Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		return loc
	}

	loc := logout("id-token")
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != a.endSession {
		t.Errorf("redirected to %q, want end session endpoint", got)
	}
	want := url.Values{
		"client_id":                {"dict"},
		"id_token_hint":            {"id-token"},
		"post_logout_redirect_uri": {"https://dict.example.com/"},
	}
	if loc.RawQuery != want.Encode() {
		t.Errorf("query = %q, want %q", loc.RawQuery, want.Encode())
	}

	if loc := logout(""); loc.String() != "/" {
		t.Errorf("without ID token redirected to %q, want local /", loc)
	}
}
