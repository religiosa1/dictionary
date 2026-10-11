package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/religiosa1/dictionary/internal/auth"
	"github.com/religiosa1/dictionary/internal/store"
)

type testApp struct {
	db      *store.DB
	handler http.Handler
}

// newTestApp wires the app like main does, minus the OIDC routes.
func newTestApp(t *testing.T, publicRead bool) testApp {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server, err := New(db, publicRead)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	server.Register(mux)
	return testApp{db: db, handler: http.NewCrossOriginProtection().Handler(auth.NewSessions(db, false).LoadUser(mux))}
}

// login creates a user with a session and returns its cookie.
func (a testApp) login(t *testing.T, name string, role store.Role) *http.Cookie {
	t.Helper()
	user, err := a.db.UpsertUser("sub-"+name, name, role)
	if err != nil {
		t.Fatal(err)
	}
	token, err := a.db.CreateSession(user.ID, "", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: "session", Value: token}
}

func (a testApp) do(req *http.Request, cookie *http.Cookie) *httptest.ResponseRecorder {
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	return rec
}

func (a testApp) get(path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	return a.do(httptest.NewRequest(http.MethodGet, path, nil), cookie)
}

func (a testApp) post(path string, form url.Values, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return a.do(req, cookie)
}

func phraseValues(phrase string, translations ...string) url.Values {
	form := url.Values{"phrase": {phrase}, "tags": {"Травы, архаизм"}}
	for _, translation := range translations {
		form.Add("translation", translation)
		form.Add("examples", "")
	}
	return form
}

func TestAnonymousAccess(t *testing.T) {
	private := newTestApp(t, false)
	rec := private.get("/?q=kot", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/auth/login?next=%2F%3Fq%3Dkot" {
		t.Errorf("private: got %d %q", rec.Code, rec.Header().Get("Location"))
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	if rec := private.do(req, nil); rec.Header().Get("HX-Redirect") == "" {
		t.Errorf("htmx request should get HX-Redirect, got %d", rec.Code)
	}

	public := newTestApp(t, true)
	if rec := public.get("/", nil); rec.Code != http.StatusOK {
		t.Errorf("public: got %d", rec.Code)
	}
	if rec := public.get("/phrases/new", nil); rec.Code != http.StatusSeeOther {
		t.Errorf("public anonymous editing: got %d", rec.Code)
	}
}

func TestReaderCannotEdit(t *testing.T) {
	app := newTestApp(t, false)
	reader := app.login(t, "reader", store.RoleReader)

	if rec := app.get("/", reader); rec.Code != http.StatusOK {
		t.Errorf("reader home: got %d", rec.Code)
	}
	if rec := app.get("/phrases/new", reader); rec.Code != http.StatusForbidden {
		t.Errorf("reader new form: got %d", rec.Code)
	}
	if rec := app.post("/phrases", phraseValues("kot", "кот"), reader); rec.Code != http.StatusForbidden {
		t.Errorf("reader create: got %d", rec.Code)
	}
}

func TestEditorFlow(t *testing.T) {
	app := newTestApp(t, false)
	editor := app.login(t, "Ala", store.RoleEditor)

	rec := app.get("/meaning-row", editor)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `name="translation"`) {
		t.Errorf("meaning row: got %d: %s", rec.Code, rec.Body)
	}

	rec = app.do(httptest.NewRequest(http.MethodDelete, "/meaning-row", nil), editor)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("remove meaning row: got %d: %s", rec.Code, rec.Body)
	}

	rec = app.post("/phrases", phraseValues("Pszczoła", "пчела", ""), editor)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("create: got %d: %s", rec.Code, rec.Body)
	}
	location := rec.Header().Get("Location")

	rec = app.get(location, editor)
	body := rec.Body.String()
	for _, want := range []string{"Pszczoła", "пчела", "травы", "архаизм", "Ala"} {
		if !strings.Contains(body, want) {
			t.Errorf("phrase page missing %q", want)
		}
	}

	rec = app.get("/?q=pszczola", editor)
	if !strings.Contains(rec.Body.String(), "Pszczoła") {
		t.Errorf("search didn't find the phrase")
	}

	rec = app.post("/phrases", phraseValues("pszczoła", "пчела"), editor)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), location) {
		t.Errorf("duplicate: got %d, want link to %s", rec.Code, location)
	}

	rec = app.post(location, phraseValues("Pszczoła", "пчёлка"), editor)
	if rec.Code != http.StatusSeeOther {
		t.Errorf("update: got %d: %s", rec.Code, rec.Body)
	}
	if body := app.get(location, editor).Body.String(); !strings.Contains(body, "пчёлка") {
		t.Errorf("update not visible")
	}

	if rec := app.post(location+"/delete", nil, editor); rec.Code != http.StatusSeeOther {
		t.Errorf("delete: got %d", rec.Code)
	}
	if rec := app.get(location, editor); rec.Code != http.StatusNotFound {
		t.Errorf("after delete: got %d", rec.Code)
	}
}

func TestEditFormHasNoBlankMeaning(t *testing.T) {
	app := newTestApp(t, false)
	editor := app.login(t, "Ala", store.RoleEditor)

	if body := app.get("/phrases/new", editor).Body.String(); strings.Count(body, `name="translation"`) != 1 {
		t.Errorf("new form should have one blank meaning")
	}
	location := app.post("/phrases", phraseValues("kot", "кот", "котик"), editor).Header().Get("Location")
	body := app.get(location+"/edit", editor).Body.String()
	// 2 meanings + a blank row inside <noscript>.
	if got := strings.Count(body, `name="translation"`); got != 3 || !strings.Contains(body, "<noscript>") {
		t.Errorf("edit form has %d meaning rows, want 2 and a <noscript> one", got)
	}
}

func TestFiltersOpenWhenActive(t *testing.T) {
	app := newTestApp(t, true)
	if body := app.get("/", nil).Body.String(); strings.Contains(body, `class="filter-selects" open`) {
		t.Errorf("filters should be collapsed without active filters")
	}
	if body := app.get("/?tag=x", nil).Body.String(); !strings.Contains(body, `class="filter-selects" open`) {
		t.Errorf("filters should be open with an active filter")
	}
}

func TestValidationErrors(t *testing.T) {
	app := newTestApp(t, false)
	editor := app.login(t, "Ala", store.RoleEditor)

	form := url.Values{"phrase": {"  "}, "translation": {""}, "examples": {"przykład"}}
	rec := app.post("/phrases", form, editor)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("got %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{"Укажите фразу", "не указан перевод", "przykład"} {
		if !strings.Contains(body, want) {
			t.Errorf("response missing %q", want)
		}
	}
}

func TestCrossOriginPostRejected(t *testing.T) {
	app := newTestApp(t, false)
	editor := app.login(t, "Ala", store.RoleEditor)

	req := httptest.NewRequest(http.MethodPost, "/phrases", strings.NewReader(phraseValues("kot", "кот").Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := app.do(req, editor); rec.Code != http.StatusForbidden {
		t.Errorf("got %d", rec.Code)
	}
}

func TestParsePhraseForm(t *testing.T) {
	form := url.Values{
		"phrase":      {" kot "},
		"translation": {"кот", "", "котик"},
		"examples":    {"Kot śpi.\n\n  Mój kot  ", "", ""},
		"tags":        {"Zwierzęta, , zwierzęta,  dom  "},
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	parsed, input := parsePhraseForm(req)
	if len(parsed.Errors) > 0 {
		t.Fatalf("unexpected errors: %v", parsed.Errors)
	}
	if input.Phrase != "kot" || len(input.Meanings) != 2 || input.Meanings[1].Translation != "котик" {
		t.Errorf("bad input: %+v", input)
	}
	if got := input.Meanings[0].Examples; len(got) != 2 || got[1] != "Mój kot" {
		t.Errorf("bad examples: %q", got)
	}
	if got := strings.Join(input.Tags, "|"); got != "zwierzęta|dom" {
		t.Errorf("bad tags: %q", got)
	}
}
