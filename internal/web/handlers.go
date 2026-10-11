package web

import (
	"errors"
	"maps"
	"net/http"
	"net/url"
	"strconv"

	"github.com/religiosa1/dictionary/internal/auth"
	"github.com/religiosa1/dictionary/internal/store"
)

type homePage struct {
	Filter  store.Filter
	Phrases []store.Phrase
	Tags    []store.TagCount
	Users   []store.User
	PrevURL string
	NextURL string
}

// IsRecent tells whether it's the default "recently added" view.
func (p homePage) IsRecent() bool {
	return p.Filter.Query == "" && !p.HasFilters()
}

// HasFilters tells whether any of the select filters is set.
func (p homePage) HasFilters() bool {
	f := p.Filter
	return f.Tag != "" || f.CreatedBy != 0 || f.UpdatedBy != 0
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := store.Filter{
		Query:     query.Get("q"),
		Tag:       query.Get("tag"),
		CreatedBy: queryInt(query, "created_by"),
		UpdatedBy: queryInt(query, "updated_by"),
		Page:      max(int(queryInt(query, "page")), 1),
	}
	phrases, hasMore, err := s.db.ListPhrases(filter, pageSize)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	tags, err := s.db.ListTags()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	users, err := s.db.ListUsers()
	if err != nil {
		s.serverError(w, r, err)
		return
	}

	page := homePage{Filter: filter, Phrases: phrases, Tags: tags, Users: users}
	if filter.Page > 1 {
		page.PrevURL = pageURL(query, filter.Page-1)
	}
	if hasMore {
		page.NextURL = pageURL(query, filter.Page+1)
	}
	s.renderPage(w, r, http.StatusOK, "home", page)
}

// queryInt parses an integer query param, treating invalid values as unset.
func queryInt(query url.Values, key string) int64 {
	value, _ := strconv.ParseInt(query.Get(key), 10, 64)
	return value
}

func pageURL(query url.Values, page int) string {
	query = maps.Clone(query)
	query.Set("page", strconv.Itoa(page))
	return "/?" + query.Encode()
}

func (s *Server) tags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.db.ListTags()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	s.renderPage(w, r, http.StatusOK, "tags", tags)
}

// loadPhrase fetches the phrase from the {id} path value, rendering 404 or
// 500 on failure.
func (s *Server) loadPhrase(w http.ResponseWriter, r *http.Request) (store.Phrase, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return store.Phrase{}, false
	}
	phrase, err := s.db.GetPhrase(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return phrase, false
	}
	if err != nil {
		s.serverError(w, r, err)
		return phrase, false
	}
	return phrase, true
}

func (s *Server) showPhrase(w http.ResponseWriter, r *http.Request) {
	if phrase, ok := s.loadPhrase(w, r); ok {
		s.renderPage(w, r, http.StatusOK, "phrase", phrase)
	}
}

func (s *Server) newPhrase(w http.ResponseWriter, r *http.Request) {
	s.renderForm(w, r, http.StatusOK, phraseForm{})
}

func (s *Server) editPhrase(w http.ResponseWriter, r *http.Request) {
	if phrase, ok := s.loadPhrase(w, r); ok {
		s.renderForm(w, r, http.StatusOK, formFromPhrase(phrase))
	}
}

func (s *Server) createPhrase(w http.ResponseWriter, r *http.Request) {
	form, input := parsePhraseForm(r)
	if len(form.Errors) > 0 {
		s.renderForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	id, err := s.db.CreatePhrase(input, auth.UserFrom(r.Context()).ID)
	if s.handleSaveError(w, r, form, err) {
		return
	}
	http.Redirect(w, r, "/phrases/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (s *Server) updatePhrase(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	form, input := parsePhraseForm(r)
	form.ID = id
	if len(form.Errors) > 0 {
		s.renderForm(w, r, http.StatusUnprocessableEntity, form)
		return
	}
	err = s.db.UpdatePhrase(id, input, auth.UserFrom(r.Context()).ID)
	if s.handleSaveError(w, r, form, err) {
		return
	}
	http.Redirect(w, r, "/phrases/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

// handleSaveError renders the response for a failed save; false if err is nil.
func (s *Server) handleSaveError(w http.ResponseWriter, r *http.Request, form phraseForm, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrDuplicate):
		form.DuplicateID, _ = s.db.PhraseIDByText(form.Phrase)
		form.Errors = append(form.Errors, "Такая фраза уже есть в словаре.")
		s.renderForm(w, r, http.StatusConflict, form)
	case errors.Is(err, store.ErrNotFound):
		s.notFound(w, r)
	default:
		s.serverError(w, r, err)
	}
	return true
}

func (s *Server) renderForm(w http.ResponseWriter, r *http.Request, status int, form phraseForm) {
	tags, err := s.db.ListTags()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	form.AllTags = tags
	s.renderPage(w, r, status, "form", form)
}

func (s *Server) meaningRow(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "form", "meaning-row", store.Meaning{})
}

// removeMeaningRow pairs with meaningRow: the button swaps its fieldset out
// with an empty response. Nothing is stored server-side until the form is saved.
// Status must be 200: htmx doesn't swap 204 responses.
func removeMeaningRow(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (s *Server) confirmDelete(w http.ResponseWriter, r *http.Request) {
	if phrase, ok := s.loadPhrase(w, r); ok {
		s.renderPage(w, r, http.StatusOK, "delete", phrase)
	}
}

func (s *Server) deletePhrase(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	err = s.db.DeletePhrase(id)
	if errors.Is(err, store.ErrNotFound) {
		s.notFound(w, r)
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}
