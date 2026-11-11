// Package textnorm compares names the way people type them: "Súper",
// "super" and "SUPER " are the same word.
package textnorm

import (
	"strings"
	"unicode"
)

// Key is s in lower case, without accents, with only letters and digits
// separated by single spaces. "Netflix ", "NETFLIX" and "netflix." match.
func Key(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		r = unaccent(r)
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
			space = false
		} else {
			space = true
		}
	}
	return b.String()
}

func unaccent(r rune) rune {
	switch r {
	case 'á', 'à', 'ä', 'â':
		return 'a'
	case 'é', 'è', 'ë', 'ê':
		return 'e'
	case 'í', 'ì', 'ï', 'î':
		return 'i'
	case 'ó', 'ò', 'ö', 'ô':
		return 'o'
	case 'ú', 'ù', 'ü', 'û':
		return 'u'
	}
	return r
}
