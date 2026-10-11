package store

import (
	"strings"
)

// Hand-written instead of Unicode decomposition: NFD doesn't decompose "ł".
var polishFolding = strings.NewReplacer(
	"ą", "a", "ć", "c", "ę", "e", "ł", "l", "ń", "n",
	"ó", "o", "ś", "s", "ź", "z", "ż", "z",
)

// Key folds a phrase for uniqueness checks: trimmed, lowercased, inner
// whitespace collapsed. Diacritics are kept, as "łaska" and "laska" differ.
func Key(phrase string) string {
	return strings.Join(strings.Fields(strings.ToLower(phrase)), " ")
}

// Normalize folds a phrase for search: Key plus stripped Polish diacritics.
func Normalize(phrase string) string {
	return polishFolding.Replace(Key(phrase))
}

// trigramQuery builds an FTS5 query matching any trigram of the normalized
// query, so a typo only breaks a few trigrams instead of the whole match.
func trigramQuery(normalized string) string {
	runes := []rune(normalized)
	seen := map[string]bool{}
	var terms []string
	for i := 0; i+3 <= len(runes); i++ {
		trigram := string(runes[i : i+3])
		if seen[trigram] {
			continue
		}
		seen[trigram] = true
		terms = append(terms, `"`+strings.ReplaceAll(trigram, `"`, `""`)+`"`)
	}
	return strings.Join(terms, " OR ")
}
