// Package monitoring reports errors to GlitchTip (Sentry compatible, plan
// 0.12). Events never carry personal data or money amounts (plan §8b): the
// slog bridge sends only the log message, the error chain and allowlisted
// tags, and every string goes through Scrub before it leaves the process.
package monitoring

import (
	"regexp"
	"strings"
)

var (
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)
	uuidRe  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	jwtRe   = regexp.MustCompile(`eyJ[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]+\.[A-Za-z0-9_\-]*`)
	// Long opaque strings: session tokens, hashes, API keys. Only those with
	// a digit, so constraint names like expense_installments_household_id_fkey
	// stay readable.
	tokenRe = regexp.MustCompile(`[A-Za-z0-9_\-]{32,}`)
	// Postgres constraint details: Key (email)=(ana@example.com).
	pgDetailRe = regexp.MustCompile(`\)=\([^)]*\)`)
	// Quoted values, as %q prints them: invalid input syntax for type uuid: "x".
	quotedRe = regexp.MustCompile(`"[^"]*"`)
	// SQLSTATE codes are kept: they say what failed and carry no data.
	sqlstateRe = regexp.MustCompile(`SQLSTATE [0-9A-Z]{5}`)
	// Any number left is treated as a possible amount.
	numberRe = regexp.MustCompile(`\d+(?:[.,]\d+)*`)
)

// Scrub removes personal data and amounts from a string bound for an error
// event: e-mails, IDs, tokens, quoted values, Postgres key details and every
// number except SQLSTATE codes.
func Scrub(s string) string {
	if s == "" {
		return s
	}
	codes := sqlstateRe.FindAllString(s, -1)
	if len(codes) > 0 {
		s = sqlstateRe.ReplaceAllString(s, "\x00")
	}
	s = emailRe.ReplaceAllString(s, "[email]")
	s = jwtRe.ReplaceAllString(s, "[token]")
	s = uuidRe.ReplaceAllString(s, "[id]")
	s = tokenRe.ReplaceAllStringFunc(s, func(m string) string {
		if strings.ContainsAny(m, "0123456789") {
			return "[token]"
		}
		return m
	})
	s = pgDetailRe.ReplaceAllString(s, ")=([redacted])")
	s = quotedRe.ReplaceAllString(s, `"[redacted]"`)
	s = numberRe.ReplaceAllString(s, "[n]")
	for _, c := range codes {
		s = strings.Replace(s, "\x00", c, 1)
	}
	return s
}
