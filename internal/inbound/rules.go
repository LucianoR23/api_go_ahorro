package inbound

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/LucianoR23/valuo_api/internal/expenseparse"
)

// Senders the rules know (decided with the user), by the domain of the
// notification's From. Any other mail goes to the AI.
var sources = []struct {
	name    string
	domains []string
	// payment is what the person paid with when the mail does not name a
	// card ("mercado pago" for money in the account).
	payment string
	bank    string
}{
	{name: "mercadopago", domains: []string{"mercadopago.com", "mercadopago.com.ar", "mercadolibre.com"}, payment: "mercado pago"},
	{name: "galicia", domains: []string{"bancogalicia.com.ar", "galicia.ar", "bancogalicia.com"}, bank: "galicia"},
	{name: "santander", domains: []string{"santander.com.ar"}, bank: "santander"},
	{name: "bbva", domains: []string{"bbva.com.ar", "bbva.com"}, bank: "bbva"},
	{name: "naranjax", domains: []string{"naranjax.com", "naranja.com"}, payment: "naranja x", bank: "naranja"},
	{name: "brubank", domains: []string{"brubank.com", "brubank.com.ar"}, payment: "brubank", bank: "brubank"},
}

// Gmail's forwarding confirmation, which arrives before any forwarded mail.
const gmailForwarding = "forwarding-noreply@google.com"

func sourceOf(from string) (name, payment, bank string, ok bool) {
	at := strings.LastIndexByte(from, '@')
	if at < 0 {
		return "", "", "", false
	}
	domain := from[at+1:]
	for _, s := range sources {
		for _, d := range s.domains {
			if domain == d || strings.HasSuffix(domain, "."+d) {
				return s.name, s.payment, s.bank, true
			}
		}
	}
	return "", "", "", false
}

var (
	// Money that came in is not an expense.
	received = regexp.MustCompile(`(?i)\b(recibiste|te (transfiri[oó]|envi[oó]|enviaron|pag[oó])|ingres[oó] (dinero|plata)|acreditamos|reintegro|devoluci[oó]n|te devolvimos)\b`)
	// What is not a single payment: summaries, due dates, codes, offers.
	notPayment = regexp.MustCompile(`(?i)\b(resumen de cuenta|tu resumen|vencimiento|vence el|c[oó]digo de (seguridad|verificaci[oó]n)|token|inicio de sesi[oó]n|promoci[oó]n|beneficio)\b`)

	amountRe = regexp.MustCompile(`(?i)(US\$|U\$S|USD|EUR|€|\$)\s?(\d{1,3}(?:\.\d{3})+(?:,\d{1,2})?|\d+(?:,\d{1,2})?)`)

	merchantRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)(?:le pagaste|pagaste|le transferiste|transferiste|le enviaste|enviaste|compraste)\b[^\n]{0,40}?\s(?:a|en)\s+([^\n]{2,60})`),
		regexp.MustCompile(`(?i)(?:compra|consumo|pago)\s+(?:de|por)\s+(?:US\$|U\$S|\$)\s?[\d.,]+\s+en\s+([^\n]{2,60})`),
		regexp.MustCompile(`(?im)^(?:comercio|establecimiento|destinatario|para|a)\s*:\s*([^\n]{2,60})$`),
	}
	merchantEnd = regexp.MustCompile(`(?i)\s+(?:con (?:tu|la|el)\b|el d[ií]a\b|por (?:US\$|U\$S|\$)|en \d+ cuotas|[.,;:!]\s|\(|$).*`)

	cardRe        = regexp.MustCompile(`(?i)\b(visa|mastercard|master|american express|amex|cabal|maestro)\b(?:\s+(d[eé]bito|cr[eé]dito))?`)
	installmentRe = regexp.MustCompile(`(?i)\b(\d{1,2})\s+cuotas\b`)
	dateRe        = regexp.MustCompile(`(?i)fecha[^\n:]{0,25}:?\s*(\d{1,2})/(\d{1,2})/(\d{2,4})`)
	legalSuffix   = regexp.MustCompile(`(?i)\s+(s\.?\s?a\.?|s\.?\s?r\.?\s?l\.?|s\.?a\.?s\.?)$`)
)

// verdict of the rules for one mail.
type verdict int

const (
	verdictUnknown  verdict = iota // not a known sender, or unclear: ask the AI
	verdictExpense                 // read
	verdictNotSpent                // money in, a summary, a code: ignored
)

// readRules reads a mail from a known sender. loc is the app's timezone,
// used for the mail's Date when the text has no date.
func readRules(m Mail, loc *time.Location) (expenseparse.Reading, string, verdict) {
	name, payment, bank, ok := sourceOf(m.From)
	if !ok {
		return expenseparse.Reading{}, "", verdictUnknown
	}
	head := m.Subject + "\n" + firstRunes(m.Text, 3000)
	if received.MatchString(head) || notPayment.MatchString(m.Subject) {
		return expenseparse.Reading{}, name, verdictNotSpent
	}
	am := amountRe.FindStringSubmatch(head)
	if am == nil {
		return expenseparse.Reading{}, name, verdictUnknown
	}
	amount, ok := parseAmount(am[2])
	if !ok {
		return expenseparse.Reading{}, name, verdictUnknown
	}
	r := expenseparse.Reading{Amount: amount, Currency: currencyOf(am[1]), Payment: payment}
	for _, re := range merchantRes {
		if mm := re.FindStringSubmatch(head); mm != nil {
			if d := cleanMerchant(mm[1]); d != "" {
				r.Description = d
				break
			}
		}
	}
	if c := cardRe.FindStringSubmatch(head); c != nil {
		r.Payment = cardWords(c[1], c[2], bank)
	}
	if n := installmentRe.FindStringSubmatch(head); n != nil {
		if k, err := strconv.Atoi(n[1]); err == nil && k > 1 && k <= 60 {
			r.Installments = k
		}
	}
	if d := dateRe.FindStringSubmatch(head); d != nil {
		r.Date = isoDate(d[1], d[2], d[3])
	}
	if r.Date == "" && !m.Date.IsZero() {
		r.Date = m.Date.In(loc).Format(time.DateOnly)
	}
	return r, name, verdictExpense
}

func firstRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	return string(rs[:n])
}

// parseAmount reads "15.430,50", "15430" or "15430,5".
func parseAmount(s string) (float64, bool) {
	s = strings.ReplaceAll(s, ".", "")
	s = strings.Replace(s, ",", ".", 1)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || f >= 1e10 {
		return 0, false
	}
	return f, true
}

func currencyOf(sym string) string {
	switch strings.ToUpper(sym) {
	case "US$", "U$S", "USD":
		return "USD"
	case "EUR", "€":
		return "EUR"
	}
	return "ARS"
}

func cleanMerchant(s string) string {
	s = merchantEnd.ReplaceAllString(strings.TrimSpace(s), "")
	s = legalSuffix.ReplaceAllString(strings.Trim(s, " -*\"'"), "")
	if amountRe.MatchString(s) || len(strings.Fields(s)) > 6 {
		return ""
	}
	words := strings.Fields(s)
	if len(words) > 5 {
		words = words[:5]
	}
	return strings.Join(words, " ")
}

func cardWords(brand, kind, bank string) string {
	b := strings.ToLower(brand)
	switch b {
	case "master":
		b = "mastercard"
	case "american express":
		b = "amex"
	}
	k := strings.ToLower(kind)
	switch {
	case strings.HasPrefix(k, "d"):
		return b + " débito"
	case bank != "":
		return b + " " + bank
	}
	return b
}

func isoDate(d, m, y string) string {
	if len(y) == 2 {
		y = "20" + y
	}
	t, err := time.Parse("2/1/2006", d+"/"+m+"/"+y)
	if err != nil {
		return ""
	}
	return t.Format(time.DateOnly)
}

var (
	gmailCodeRe     = regexp.MustCompile(`#(\d{5,20})`)
	gmailTextCodeRe = regexp.MustCompile(`(?i)(?:c[oó]digo de confirmaci[oó]n|confirmation code):?\s*(\d{5,20})`)
)

// gmailCode is the code of Gmail's forwarding confirmation, from the subject
// ("(#123456789) Gmail Forwarding Confirmation") or the text.
func gmailCode(m Mail) string {
	if c := gmailCodeRe.FindStringSubmatch(m.Subject); c != nil {
		return c[1]
	}
	if c := gmailTextCodeRe.FindStringSubmatch(m.Text); c != nil {
		return c[1]
	}
	return ""
}
