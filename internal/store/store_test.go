package store

import (
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func newTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func newTestUser(t *testing.T, db *DB, name string) User {
	t.Helper()
	user, err := db.UpsertUser("sub-"+name, name, RoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func mustCreate(t *testing.T, db *DB, user User, phrase string, tags ...string) int64 {
	t.Helper()
	id, err := db.CreatePhrase(PhraseInput{
		Phrase:   phrase,
		Meanings: Meanings{{Translation: "перевод " + phrase}},
		Tags:     tags,
	}, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func phraseTexts(phrases []Phrase) []string {
	var texts []string
	for _, p := range phrases {
		texts = append(texts, p.Phrase)
	}
	return texts
}

func TestOpenTwiceKeepsSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	for range 2 {
		db, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		_ = db.Close()
	}
}

func TestPhraseCRUD(t *testing.T) {
	db := newTestDB(t)
	alice := newTestUser(t, db, "alice")
	bob := newTestUser(t, db, "bob")

	id, err := db.CreatePhrase(PhraseInput{
		Phrase: "Łaska",
		Meanings: Meanings{
			{Translation: "милость", Examples: []string{"Łaska boża"}},
			{Translation: "благодать"},
		},
		Notes: "Книжное.",
		Tags:  []string{"религия", "архаизм"},
	}, alice.ID)
	if err != nil {
		t.Fatal(err)
	}

	got, err := db.GetPhrase(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Phrase != "Łaska" || got.Preview() != "милость" || len(got.Meanings) != 2 || got.Notes != "Книжное." {
		t.Errorf("unexpected phrase: %+v", got)
	}
	if !slices.Equal(got.Tags, []string{"архаизм", "религия"}) {
		t.Errorf("tags = %v", got.Tags)
	}
	if got.CreatedByName != "alice" || time.Since(got.CreatedAt) > time.Minute {
		t.Errorf("bad audit fields: %+v", got)
	}

	err = db.UpdatePhrase(id, PhraseInput{
		Phrase:   "Łaska",
		Meanings: Meanings{{Translation: "милость"}},
		Tags:     []string{"религия"},
	}, bob.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ = db.GetPhrase(id)
	if got.UpdatedByName != "bob" || got.CreatedByName != "alice" {
		t.Errorf("bad audit fields after update: %+v", got)
	}
	if got.Notes != "" {
		t.Errorf("notes not cleared: %q", got.Notes)
	}
	tags, _ := db.ListTags()
	if len(tags) != 1 || tags[0].Name != "религия" {
		t.Errorf("unused tag not pruned: %v", tags)
	}

	if err := db.DeletePhrase(id); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetPhrase(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected not found, got %v", err)
	}
	if tags, _ := db.ListTags(); len(tags) != 0 {
		t.Errorf("tags not pruned on delete: %v", tags)
	}
	if err := db.DeletePhrase(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected not found on second delete, got %v", err)
	}
}

func TestPhraseUniqueness(t *testing.T) {
	db := newTestDB(t)
	user := newTestUser(t, db, "alice")
	id := mustCreate(t, db, user, "Łaska")

	_, err := db.CreatePhrase(PhraseInput{Phrase: "łaska", Meanings: Meanings{{Translation: "x"}}}, user.ID)
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("expected duplicate for case variant, got %v", err)
	}
	if found, _ := db.PhraseIDByText("ŁASKA"); found != id {
		t.Errorf("PhraseIDByText = %d, want %d", found, id)
	}
	// Different word, only differs in diacritics.
	mustCreate(t, db, user, "laska")
}

func TestListPhrasesFilters(t *testing.T) {
	db := newTestDB(t)
	alice := newTestUser(t, db, "alice")
	bob := newTestUser(t, db, "bob")
	mustCreate(t, db, alice, "pszczoła", "насекомые")
	mustCreate(t, db, bob, "mięta", "травы")
	mustCreate(t, db, alice, "rumianek", "травы")

	cases := []struct {
		name   string
		filter Filter
		want   []string
	}{
		{"recent first", Filter{}, []string{"rumianek", "mięta", "pszczoła"}},
		{"by tag", Filter{Tag: "травы"}, []string{"rumianek", "mięta"}},
		{"by author", Filter{CreatedBy: alice.ID}, []string{"rumianek", "pszczoła"}},
		{"tag and author", Filter{Tag: "травы", CreatedBy: bob.ID}, []string{"mięta"}},
		{"by modifier", Filter{UpdatedBy: bob.ID}, []string{"mięta"}},
		{"search without diacritics", Filter{Query: "pszczola"}, []string{"pszczoła"}},
		{"search with typo", Filter{Query: "pszczla"}, []string{"pszczoła"}},
		{"short query prefix", Filter{Query: "mi"}, []string{"mięta"}},
		{"search and tag", Filter{Query: "mieta", Tag: "насекомые"}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := db.ListPhrases(tc.filter, 30)
			if err != nil {
				t.Fatal(err)
			}
			if texts := phraseTexts(got); !slices.Equal(texts, tc.want) {
				t.Errorf("got %v, want %v", texts, tc.want)
			}
		})
	}
}

func TestListPhrasesSearchRanksPrefixFirst(t *testing.T) {
	db := newTestDB(t)
	user := newTestUser(t, db, "alice")
	mustCreate(t, db, user, "przykład kota")
	mustCreate(t, db, user, "kot")

	got, _, err := db.ListPhrases(Filter{Query: "kot"}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if texts := phraseTexts(got); len(texts) == 0 || texts[0] != "kot" {
		t.Errorf("got %v, want kot first", texts)
	}
}

func TestListPhrasesSearchRanksExactFirst(t *testing.T) {
	db := newTestDB(t)
	user := newTestUser(t, db, "alice")
	mustCreate(t, db, user, "łaska")
	mustCreate(t, db, user, "laska")

	for query, want := range map[string]string{"laska": "laska", "Łaska": "łaska"} {
		got, _, err := db.ListPhrases(Filter{Query: query}, 30)
		if err != nil {
			t.Fatal(err)
		}
		if texts := phraseTexts(got); len(texts) != 2 || texts[0] != want {
			t.Errorf("query %q: got %v, want %s first", query, texts, want)
		}
	}
}

func TestListPhrasesPagination(t *testing.T) {
	db := newTestDB(t)
	user := newTestUser(t, db, "alice")
	for _, p := range []string{"a1", "a2", "a3"} {
		mustCreate(t, db, user, p)
	}

	page1, more, _ := db.ListPhrases(Filter{Page: 1}, 2)
	if len(page1) != 2 || !more {
		t.Errorf("page 1: %v more=%v", phraseTexts(page1), more)
	}
	page2, more, _ := db.ListPhrases(Filter{Page: 2}, 2)
	if len(page2) != 1 || more {
		t.Errorf("page 2: %v more=%v", phraseTexts(page2), more)
	}
}

func TestSessions(t *testing.T) {
	db := newTestDB(t)
	user := newTestUser(t, db, "alice")

	token, err := db.CreateSession(user.ID, "id-token", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.SessionUser(token)
	if err != nil || got.ID != user.ID || got.Role != RoleEditor {
		t.Errorf("SessionUser = %+v, %v", got, err)
	}

	idToken, err := db.DeleteSession(token)
	if err != nil || idToken != "id-token" {
		t.Errorf("DeleteSession = %q, %v", idToken, err)
	}
	if idToken, err := db.DeleteSession(token); idToken != "" || err != nil {
		t.Errorf("second delete = %q, %v", idToken, err)
	}
	if _, err := db.SessionUser(token); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected not found after delete, got %v", err)
	}

	expired, _ := db.CreateSession(user.ID, "", -time.Minute)
	if _, err := db.SessionUser(expired); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected expired session to be rejected, got %v", err)
	}
}

func TestUpsertUserRefreshesRole(t *testing.T) {
	db := newTestDB(t)
	first, _ := db.UpsertUser("sub", "Alice", RoleEditor)
	second, err := db.UpsertUser("sub", "Alice K", RoleReader)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || second.Role != RoleReader || second.Name != "Alice K" {
		t.Errorf("upsert didn't refresh: %+v", second)
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]string{
		"  Łaska  Boża ": "laska boza",
		"ŻÓŁĆ":           "zolc",
		"zażółć gęślą":   "zazolc gesla",
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTrigramQuery(t *testing.T) {
	if got, want := trigramQuery(`kot"a`), `"kot" OR "ot""" OR "t""a"`; got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if got := trigramQuery("aaaa"); got != `"aaa"` {
		t.Errorf("duplicates not removed: %s", got)
	}
}
