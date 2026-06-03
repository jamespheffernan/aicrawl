package textnorm

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

func Normalize(input string) string {
	if input == "" {
		return ""
	}
	input = strings.ToValidUTF8(input, "\uFFFD")
	var b strings.Builder
	b.Grow(len(input))
	lastSpace := false
	for _, r := range input {
		if isZeroWidth(r) {
			continue
		}
		if r == '\r' {
			continue
		}
		if r == '\n' {
			if b.Len() > 0 && !lastSpace {
				b.WriteByte('\n')
				lastSpace = true
			}
			continue
		}
		if unicode.IsSpace(r) {
			if b.Len() > 0 && !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func SearchTerms(query string) (wordTerms []string, literalTerms []string) {
	for _, term := range strings.Fields(Normalize(query)) {
		if term == "" {
			continue
		}
		if hasLetterOrDigit(term) {
			wordTerms = append(wordTerms, term)
		} else {
			literalTerms = append(literalTerms, term)
		}
	}
	return wordTerms, literalTerms
}

func FTS5Query(terms []string) string {
	quoted := make([]string, 0, len(terms))
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		quoted = append(quoted, `"`+strings.ReplaceAll(term, `"`, `""`)+`"`)
	}
	return strings.Join(quoted, " ")
}

func EscapeLike(term string) string {
	var b strings.Builder
	for _, r := range term {
		switch r {
		case '%', '_', '\\':
			b.WriteByte('\\')
		}
		if r == utf8.RuneError {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func isZeroWidth(r rune) bool {
	switch r {
	case '\u200B', '\u200C', '\u200D', '\u2060', '\uFEFF':
		return true
	default:
		return false
	}
}

func hasLetterOrDigit(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
