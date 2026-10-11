package web

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/religiosa1/dictionary/internal/store"
)

const (
	maxPhraseLen      = 200
	maxTranslationLen = 500
	maxTagLen         = 50
)

// phraseForm holds the submitted (or stored) values of the edit form.
type phraseForm struct {
	ID          int64 // 0 for a new phrase
	Phrase      string
	Meanings    []store.Meaning
	Tags        string
	AllTags     []store.TagCount
	Errors      []string
	DuplicateID int64
}

func formFromPhrase(phrase store.Phrase) phraseForm {
	return phraseForm{
		ID:       phrase.ID,
		Phrase:   phrase.Phrase,
		Meanings: phrase.Meanings,
		Tags:     strings.Join(phrase.Tags, ", "),
	}
}

// WithEmptyMeaning returns meanings plus one blank row, so a new meaning can
// be added even without JS.
func (f phraseForm) WithEmptyMeaning() []store.Meaning {
	return append(slices.Clone(f.Meanings), store.Meaning{})
}

// parsePhraseForm reads and validates the form. Translations and examples
// come as parallel lists, one entry per meaning fieldset.
func parsePhraseForm(r *http.Request) (phraseForm, store.PhraseInput) {
	var form phraseForm
	if err := r.ParseForm(); err != nil {
		form.Errors = append(form.Errors, "Не удалось прочитать форму.")
		return form, store.PhraseInput{}
	}
	form.Phrase = strings.TrimSpace(r.PostForm.Get("phrase"))
	form.Tags = r.PostForm.Get("tags")

	translations := r.PostForm["translation"]
	examples := r.PostForm["examples"]
	for i, translation := range translations {
		meaning := store.Meaning{Translation: strings.TrimSpace(translation)}
		if i < len(examples) {
			meaning.Examples = splitLines(examples[i])
		}
		if meaning.Translation == "" {
			if len(meaning.Examples) > 0 {
				form.Errors = append(form.Errors, "У значения с примерами не указан перевод.")
				form.Meanings = append(form.Meanings, meaning)
			}
			// Blank fieldsets are how meanings get removed.
			continue
		}
		if utf8.RuneCountInString(meaning.Translation) > maxTranslationLen {
			form.Errors = append(form.Errors, fmt.Sprintf("Перевод длиннее %d символов.", maxTranslationLen))
		}
		form.Meanings = append(form.Meanings, meaning)
	}

	switch {
	case form.Phrase == "":
		form.Errors = append(form.Errors, "Укажите фразу.")
	case utf8.RuneCountInString(form.Phrase) > maxPhraseLen:
		form.Errors = append(form.Errors, fmt.Sprintf("Фраза длиннее %d символов.", maxPhraseLen))
	}
	if len(form.Meanings) == 0 {
		form.Errors = append(form.Errors, "Добавьте хотя бы один перевод.")
	}

	tags := parseTags(form.Tags)
	for _, tag := range tags {
		if utf8.RuneCountInString(tag) > maxTagLen {
			form.Errors = append(form.Errors, fmt.Sprintf("Тег «%s» длиннее %d символов.", tag, maxTagLen))
		}
	}

	return form, store.PhraseInput{Phrase: form.Phrase, Meanings: form.Meanings, Tags: tags}
}

// splitLines returns trimmed non-empty lines.
func splitLines(text string) []string {
	var lines []string
	for line := range strings.Lines(text) {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// parseTags splits a comma-separated list into lowercase unique tags.
func parseTags(text string) []string {
	var tags []string
	for tag := range strings.SplitSeq(text, ",") {
		tag = strings.Join(strings.Fields(strings.ToLower(tag)), " ")
		if tag != "" && !slices.Contains(tags, tag) {
			tags = append(tags, tag)
		}
	}
	return tags
}
