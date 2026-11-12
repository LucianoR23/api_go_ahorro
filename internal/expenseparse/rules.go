package expenseparse

import (
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/textnorm"
)

// Method is one of the person's active payment methods, with the words
// that name it.
type Method struct {
	ID    uuid.UUID
	Name  string
	Kind  string // cash, debit, credit, wallet, transfer
	Bank  string
	Alias string // credit card alias
	// Network is the card's network (visa, mastercard, amex, cabal,
	// naranja, maestro, other), if set.
	Network string
}

// Fields is what a sentence says. Zero values mean "not said".
type Fields struct {
	Amount       float64
	Currency     string // ARS, USD, EUR
	SpentAt      time.Time
	Installments int
	Description  string
	// PaymentMethodID is set when one method matches; Candidates when the
	// words fit several ("visa" with two Visa cards), and then nothing is
	// picked: the person chooses (decided with the user).
	PaymentMethodID uuid.UUID
	Candidates      []uuid.UUID
	Shared          bool
}

// ruleResult is Fields plus what the rules could not settle.
type ruleResult struct {
	Fields
	// amountClear is false when no amount, or more than one, was found:
	// then the AI is asked.
	amountClear bool
}

type token struct {
	raw  string // as typed, without surrounding punctuation
	norm string // lower case, no accents
	used bool
}

func tokenize(text string) []token {
	var out []token
	for _, f := range strings.Fields(text) {
		raw := strings.TrimFunc(f, func(r rune) bool {
			return strings.ContainsRune(".,;:!?¡¿\"'()", r)
		})
		if raw == "" {
			continue
		}
		out = append(out, token{raw: raw, norm: fold(raw)})
	}
	return out
}

// fold lowers and strips accents but keeps symbols ("u$s", "15/3").
func fold(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if k := textnorm.Key(string(r)); k != "" {
			b.WriteString(k)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

var numberWords = map[string]int{
	"un": 1, "una": 1, "uno": 1, "dos": 2, "tres": 3, "cuatro": 4, "cinco": 5,
	"seis": 6, "siete": 7, "ocho": 8, "nueve": 9, "diez": 10, "once": 11,
	"doce": 12, "dieciocho": 18, "veinticuatro": 24,
}

var currencyWords = map[string]string{
	"$": "ARS", "ars": "ARS", "peso": "ARS", "pesos": "ARS",
	"usd": "USD", "us$": "USD", "u$s": "USD", "u$d": "USD", "uss": "USD", "dolar": "USD",
	"dolares": "USD", "dls": "USD", "verdes": "USD",
	"eur": "EUR", "€": "EUR", "euro": "EUR", "euros": "EUR",
}

var multiplierWords = map[string]float64{
	"mil": 1e3, "k": 1e3, "luca": 1e3, "lucas": 1e3,
	"palo": 1e6, "palos": 1e6, "millon": 1e6, "millones": 1e6,
}

var weekdays = map[string]time.Weekday{
	"domingo": time.Sunday, "lunes": time.Monday, "martes": time.Tuesday, "miercoles": time.Wednesday,
	"jueves": time.Thursday, "viernes": time.Friday, "sabado": time.Saturday,
}

var months = map[string]time.Month{
	"enero": 1, "febrero": 2, "marzo": 3, "abril": 4, "mayo": 5, "junio": 6, "julio": 7,
	"agosto": 8, "septiembre": 9, "setiembre": 9, "octubre": 10, "noviembre": 11, "diciembre": 12,
}

// connectors are dropped at the edges of the description and before a
// recognized piece ("en 6 cuotas", "con visa").
var connectors = map[string]bool{
	"en": true, "con": true, "de": true, "por": true, "el": true, "la": true, "los": true, "las": true,
	"a": true, "al": true, "del": true, "y": true, "para": true, "un": true, "una": true,
	"pague": true, "gaste": true, "compre": true, "pagado": true, "pagada": true, "me": true, "salio": true,
}

// parseRules reads the sentence without AI. today is the person's date.
func parseRules(text string, today time.Time, methods []Method) ruleResult {
	toks := tokenize(text)
	var r ruleResult
	findInstallments(toks, &r)
	findDate(toks, today, &r)
	findShared(toks, &r)
	findAmount(toks, &r)
	findPayment(toks, methods, &r)
	r.Description = describe(toks)
	return r
}

// useBefore marks the connector right before i, if any.
func useBefore(toks []token, i int) {
	if i > 0 && !toks[i-1].used && connectors[toks[i-1].norm] {
		toks[i-1].used = true
	}
}

func findInstallments(toks []token, r *ruleResult) {
	for i, t := range toks {
		if t.used || (t.norm != "cuota" && t.norm != "cuotas" && t.norm != "pago" && t.norm != "pagos") || i == 0 {
			continue
		}
		prev := toks[i-1]
		n, ok := numberWords[prev.norm]
		if !ok {
			v, err := strconv.Atoi(prev.norm)
			if err != nil {
				continue
			}
			n = v
		}
		// "un pago" is one installment; "3 pagos" too, but "pago" alone is
		// a word of the description ("pago luz").
		if n < 1 || n > 60 || prev.used {
			continue
		}
		r.Installments = n
		toks[i].used, toks[i-1].used = true, true
		useBefore(toks, i-1)
		// "sin interés" after the installments says nothing new.
		if i+2 < len(toks) && toks[i+1].norm == "sin" && strings.HasPrefix(toks[i+2].norm, "interes") {
			toks[i+1].used, toks[i+2].used = true, true
		}
		return
	}
}

var dateRe = regexp.MustCompile(`^(\d{1,2})[/-](\d{1,2})(?:[/-](\d{2}|\d{4}))?$`)

func findDate(toks []token, today time.Time, r *ruleResult) {
	set := func(d time.Time, from, to int) {
		r.SpentAt = d
		for k := from; k <= to; k++ {
			toks[k].used = true
		}
		useBefore(toks, from)
	}
	for i, t := range toks {
		if t.used {
			continue
		}
		switch t.norm {
		case "hoy":
			set(today, i, i)
			return
		case "ayer":
			// "antes de ayer"
			if i >= 2 && toks[i-2].norm == "antes" && toks[i-1].norm == "de" {
				set(today.AddDate(0, 0, -2), i-2, i)
				return
			}
			set(today.AddDate(0, 0, -1), i, i)
			return
		case "anteayer", "antier":
			set(today.AddDate(0, 0, -2), i, i)
			return
		}
		if wd, ok := weekdays[t.norm]; ok {
			// The last such day before today: "el lunes" said on a Monday is
			// a week ago, since today is "hoy".
			back := (int(today.Weekday()) - int(wd) + 7) % 7
			if back == 0 {
				back = 7
			}
			set(today.AddDate(0, 0, -back), i, i)
			return
		}
		if m := dateRe.FindStringSubmatch(t.norm); m != nil {
			day, _ := strconv.Atoi(m[1])
			mon, _ := strconv.Atoi(m[2])
			year := today.Year()
			if m[3] != "" {
				year, _ = strconv.Atoi(m[3])
				if year < 100 {
					year += 2000
				}
			}
			if d, ok := pastDate(year, time.Month(mon), day, today, m[3] == ""); ok {
				set(d, i, i)
				return
			}
		}
		// "15 de marzo"
		if n, err := strconv.Atoi(t.norm); err == nil && i+2 < len(toks) && toks[i+1].norm == "de" {
			if mon, ok := months[toks[i+2].norm]; ok {
				if d, ok := pastDate(today.Year(), mon, n, today, true); ok {
					set(d, i, i+2)
					return
				}
			}
		}
		// "el 15": a day of this month, or of the last one if still ahead.
		if t.norm == "el" && i+1 < len(toks) {
			n, err := strconv.Atoi(toks[i+1].norm)
			next := ""
			if i+2 < len(toks) {
				next = toks[i+2].norm
			}
			if err == nil && n >= 1 && n <= 31 && multiplierWords[next] == 0 && currencyWords[next] == "" &&
				next != "cuotas" && next != "cuota" && next != "de" {
				y, mo, _ := today.Date()
				d := time.Date(y, mo, n, 0, 0, 0, 0, today.Location())
				if d.After(today) {
					d = time.Date(y, mo-1, n, 0, 0, 0, 0, today.Location())
				}
				if d.Day() == n {
					set(d, i, i+1)
					return
				}
			}
		}
	}
}

// pastDate builds a date that is not in the future. Without a year, a date
// still ahead is last year's.
func pastDate(year int, mon time.Month, day int, today time.Time, guessYear bool) (time.Time, bool) {
	if mon < 1 || mon > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	d := time.Date(year, mon, day, 0, 0, 0, 0, today.Location())
	if d.Day() != day {
		return time.Time{}, false
	}
	if d.After(today) {
		if !guessYear {
			return time.Time{}, false
		}
		d = d.AddDate(-1, 0, 0)
	}
	return d, true
}

func findShared(toks []token, r *ruleResult) {
	for i, t := range toks {
		if t.used {
			continue
		}
		if t.norm == "compartido" || t.norm == "compartida" || t.norm == "hogar" {
			r.Shared = true
			toks[i].used = true
			useBefore(toks, i)
			return
		}
	}
}

// amountRe reads "2500", "2.500", "2500,50", "1.5", "$2500", "120k",
// "u$s100" and "100usd".
var amountRe = regexp.MustCompile(`^(\$|u\$s|us\$|u\$d|usd|ars|eur|€)?(\d{1,3}(?:\.\d{3})+|\d+)(?:([.,])(\d{1,2}))?(k|mil|m)?(\$|usd|ars|eur|€)?$`)

type amountHit struct {
	value    float64
	currency string
	from, to int
	explicit bool // had a currency or a multiplier next to it
}

func findAmount(toks []token, r *ruleResult) {
	var hits []amountHit
	for i, t := range toks {
		if t.used {
			continue
		}
		m := amountRe.FindStringSubmatch(t.norm)
		if m == nil {
			continue
		}
		v, err := strconv.ParseFloat(strings.ReplaceAll(m[2], ".", ""), 64)
		if err != nil {
			continue
		}
		if m[4] != "" {
			frac, _ := strconv.ParseFloat("0."+m[4], 64)
			v += frac
		}
		h := amountHit{value: v, from: i, to: i}
		cur := currencyWords[m[1]]
		if cur == "" {
			cur = currencyWords[m[6]]
		}
		mult := 1.0
		switch m[5] {
		case "k", "mil":
			mult = 1e3
		case "m":
			mult = 1e6
		}
		if i+1 < len(toks) && !toks[i+1].used && mult == 1 {
			if x := multiplierWords[toks[i+1].norm]; x > 0 {
				mult, h.to = x, i+1
			}
		}
		if cur == "" && h.to+1 < len(toks) && !toks[h.to+1].used {
			if c := currencyWords[toks[h.to+1].norm]; c != "" {
				cur, h.to = c, h.to+1
			}
		}
		if cur == "" && i > 0 && !toks[i-1].used {
			if c := currencyWords[toks[i-1].norm]; c != "" {
				cur, h.from = c, i-1
			}
		}
		h.value = v * mult
		h.currency = cur
		h.explicit = cur != "" || mult != 1
		if h.value > 0 {
			hits = append(hits, h)
		}
	}
	pick := -1
	switch {
	case len(hits) == 1:
		pick = 0
	case len(hits) > 1:
		for k, h := range hits {
			if h.explicit {
				if pick >= 0 {
					pick = -1
					break
				}
				pick = k
			}
		}
	}
	if pick < 0 {
		return
	}
	h := hits[pick]
	r.Amount, r.Currency, r.amountClear = h.value, h.currency, true
	for k := h.from; k <= h.to; k++ {
		toks[k].used = true
	}
	useBefore(toks, h.from)
	if r.Currency == "" {
		// A currency said elsewhere: "en dólares 50".
		for i, t := range toks {
			if c := currencyWords[t.norm]; c != "" && !t.used {
				r.Currency = c
				toks[i].used = true
				break
			}
		}
	}
}

var kindWords = map[string][]string{
	"cash":     {"efectivo", "cash", "billete", "billetes"},
	"debit":    {"debito", "debit", "tarjeta"},
	"credit":   {"credito", "credit", "tarjeta"},
	"transfer": {"transferencia", "transferi", "transf"},
	"wallet":   {"billetera"},
}

// genericMethodWords appear in method names but also in descriptions
// ("Mercado Pago" and "pago luz", "Cuenta DNI" and "cuenta del gas").
var genericMethodWords = map[string]bool{
	"banco": true, "cuenta": true, "caja": true, "ahorro": true, "pago": true, "corriente": true,
	"sueldo": true, "pesos": true, "dolares": true, "tarjeta": true, "de": true, "del": true, "la": true,
}

// methodWords is every word that names m: its name, bank and alias, the
// usual short forms, and words for its kind.
func methodWords(m Method) map[string]bool {
	words := map[string]bool{}
	network := m.Network
	if network == "other" {
		network = ""
	}
	for _, s := range []string{m.Name, m.Bank, m.Alias, network} {
		for _, w := range strings.Fields(textnorm.Key(s)) {
			if !genericMethodWords[w] {
				words[w] = true
			}
		}
	}
	key := " " + textnorm.Key(m.Name+" "+m.Alias) + " "
	if words["mastercard"] {
		words["master"] = true
	}
	if words["amex"] || strings.Contains(key, " american express ") {
		words["amex"], words["american"] = true, true
	}
	if words["mercadopago"] || strings.Contains(key, " mercado pago ") {
		words["mp"], words["mercadopago"] = true, true
	}
	for _, w := range kindWords[m.Kind] {
		words[w] = true
	}
	return words
}

// matches says whether an input word names a method word: the same word,
// or its start ("galic", "santand").
func matches(w string, words map[string]bool) bool {
	if words[w] {
		return true
	}
	if utf8.RuneCountInString(w) < 4 {
		return false
	}
	for m := range words {
		if strings.HasPrefix(m, w) {
			return true
		}
	}
	return false
}

func isWord(s string) bool {
	if s == "mp" {
		return true
	}
	if utf8.RuneCountInString(s) < 3 || connectors[s] {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}

func findPayment(toks []token, methods []Method, r *ruleResult) {
	if len(methods) == 0 {
		return
	}
	type scored struct {
		m    Method
		hits map[int]bool
	}
	all := make([]scored, len(methods))
	best := 0
	for k, m := range methods {
		words := methodWords(m)
		all[k] = scored{m: m, hits: map[int]bool{}}
		for i, t := range toks {
			if !t.used && isWord(t.norm) && matches(t.norm, words) {
				all[k].hits[i] = true
			}
		}
		best = max(best, len(all[k].hits))
	}
	if best == 0 {
		return
	}
	var top []scored
	for _, s := range all {
		if len(s.hits) == best {
			top = append(top, s)
		}
	}
	if len(top) == 1 {
		r.PaymentMethodID = top[0].m.ID
	} else {
		for _, s := range top {
			r.Candidates = append(r.Candidates, s.m.ID)
		}
	}
	for _, s := range top {
		for i := range s.hits {
			toks[i].used = true
		}
	}
	for _, s := range top {
		for i := range s.hits {
			useBefore(toks, i)
		}
	}
}

// maxDescriptionRunes caps the description the rules leave.
const maxDescriptionRunes = 120

// describe is what is left, as typed, without connectors at the edges and
// with a capital first letter ("Zapatillas").
func describe(toks []token) string {
	var left []token
	for _, t := range toks {
		if !t.used {
			left = append(left, t)
		}
	}
	for len(left) > 0 && connectors[left[0].norm] {
		left = left[1:]
	}
	for len(left) > 0 && connectors[left[len(left)-1].norm] {
		left = left[:len(left)-1]
	}
	words := make([]string, len(left))
	for i, t := range left {
		words[i] = t.raw
	}
	return capitalize(truncate(strings.Join(words, " "), maxDescriptionRunes))
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n]))
}
