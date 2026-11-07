package waitlist

import (
	"net/mail"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// normalizeEmail trims and lowercases, and checks the contract's rules:
// <= 254 characters, local part <= 64, a valid address and nothing else
// (no display name, no angle brackets).
func normalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	bad := domain.NewValidationError("email", "ingresá un email válido")
	if e == "" || len(e) > 254 {
		return "", bad
	}
	at := strings.LastIndexByte(e, '@')
	if at < 1 || at > 64 || at == len(e)-1 {
		return "", bad
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || addr.Name != "" || !strings.Contains(e[at+1:], ".") {
		return "", bad
	}
	return e, nil
}

// sanitizeAttribution applies the contract's rule to source, medium and
// campaign (the landing applies the same one): NFKC, trim, lowercase; every
// run of characters outside [\p{Ll}\p{N}._-] becomes one "-"; no "-" at the
// ends; cut to 64 runes; empty means nil. It never rejects.
func sanitizeAttribution(raw *string) *string {
	if raw == nil {
		return nil
	}
	s := strings.ToLower(strings.TrimSpace(norm.NFKC.String(*raw)))
	var b strings.Builder
	dash := false
	for _, r := range s {
		if unicode.IsLower(r) || unicode.IsNumber(r) || r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash {
			b.WriteRune('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if utf8.RuneCountInString(out) > 64 {
		out = strings.TrimRight(string([]rune(out)[:64]), "-")
	}
	if out == "" {
		return nil
	}
	return &out
}

// validReferrerPath: optional, a path only (starts with "/", no query or
// fragment), <= 200 characters.
func validReferrerPath(p *string) (*string, error) {
	if p == nil || strings.TrimSpace(*p) == "" {
		return nil, nil
	}
	v := strings.TrimSpace(*p)
	if len(v) > 200 || !strings.HasPrefix(v, "/") || strings.ContainsAny(v, "?#") || strings.HasPrefix(v, "//") {
		return nil, domain.NewValidationError("referrer_path", "debe ser una ruta del sitio")
	}
	return &v, nil
}
