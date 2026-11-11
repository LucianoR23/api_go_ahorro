package recurringexpenses

import (
	"context"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/dates"
	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/money"
)

// Subscription detection (plan 7.3): expenses loaded by hand that repeat
// every month (Netflix, the gym, a phone plan) are suggested as recurring
// expenses. A suggestion someone dismisses stops showing for the whole
// space.

const (
	// suggestionMonths: the window, counting the current month.
	suggestionMonths = 4
	// suggestionMinMonths: months of the window it has to show up in.
	suggestionMinMonths = 3
	// suggestionMaxKeyLen matches the table's CHECK.
	suggestionMaxKeyLen = 300
)

// suggestionMaxSpread: the biggest amount can be at most 25% over the
// smallest (a price increase still counts; a different purchase does not).
var suggestionMaxSpread = decimal.RequireFromString("1.25")

// Suggestion: an expense that looks recurring.
type Suggestion struct {
	// Key: normalized description and currency; what a dismissal stores.
	Key             string
	Description     string // as written the last time
	Currency        string
	Amount          decimal.Decimal // the last one
	MinAmount       decimal.Decimal
	MaxAmount       decimal.Decimal
	Months          int
	DayOfMonth      int // of the last one
	LastSpentAt     time.Time
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID
}

// candidate: a manual one-payment expense.
type candidate struct {
	Description     string
	Currency        string
	Amount          decimal.Decimal
	SpentAt         time.Time
	CategoryID      *uuid.UUID
	PaymentMethodID uuid.UUID
}

// normalizeDescription: lower case, no accents, only letters and digits
// separated by single spaces. "Netflix ", "NETFLIX" and "netflix." match.
func normalizeDescription(s string) string {
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

func suggestionKey(description, currency string) string {
	k := currency + ":" + normalizeDescription(description)
	if len(k) > suggestionMaxKeyLen {
		k = k[:suggestionMaxKeyLen]
	}
	return k
}

// suggestionWindow: the first day of the oldest month and of the month after
// the current one.
func suggestionWindow(today time.Time) (from, to time.Time) {
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, time.UTC)
	return first.AddDate(0, -(suggestionMonths - 1), 0), first.AddDate(0, 1, 0)
}

// detectSuggestions: the candidates (oldest first) that show up once a month
// in at least suggestionMinMonths months of the window, with amounts close
// to each other. Skips what is already a recurring expense (same
// description) and what was dismissed.
func detectSuggestions(cands []candidate, recurring []domain.RecurringExpense, dismissed map[string]bool) []Suggestion {
	existing := map[string]bool{}
	for _, r := range recurring {
		existing[normalizeDescription(r.Description)] = true
	}

	groups := map[string][]candidate{}
	var keys []string
	for _, c := range cands {
		norm := normalizeDescription(c.Description)
		if norm == "" || existing[norm] {
			continue
		}
		k := suggestionKey(c.Description, c.Currency)
		if dismissed[k] {
			continue
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], c)
	}

	var out []Suggestion
	for _, k := range keys {
		g := groups[k]
		months := map[string]bool{}
		once := true
		minA, maxA := g[0].Amount, g[0].Amount
		for _, c := range g {
			m := c.SpentAt.Format("2006-01")
			if months[m] {
				once = false
				break
			}
			months[m] = true
			minA, maxA = decimal.Min(minA, c.Amount), decimal.Max(maxA, c.Amount)
		}
		if !once || len(months) < suggestionMinMonths || maxA.GreaterThan(minA.Mul(suggestionMaxSpread)) {
			continue
		}
		last := g[len(g)-1]
		out = append(out, Suggestion{
			Key: k, Description: strings.TrimSpace(last.Description), Currency: last.Currency,
			Amount: last.Amount, MinAmount: minA, MaxAmount: maxA, Months: len(months),
			DayOfMonth: last.SpentAt.Day(), LastSpentAt: last.SpentAt,
			CategoryID: last.CategoryID, PaymentMethodID: last.PaymentMethodID,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].LastSpentAt.Equal(out[j].LastSpentAt) {
			return out[i].LastSpentAt.After(out[j].LastSpentAt)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ===================== service =====================

// Suggestions: what looks recurring in householdID, as of today.
func (s *Service) Suggestions(ctx context.Context, householdID uuid.UUID) ([]Suggestion, error) {
	from, to := suggestionWindow(dates.Today())
	cands, err := s.repo.SuggestionCandidates(ctx, householdID, from, to)
	if err != nil {
		return nil, err
	}
	recurring, err := s.repo.ListByHousehold(ctx, householdID)
	if err != nil {
		return nil, err
	}
	dismissed, err := s.repo.SuggestionDismissals(ctx, householdID)
	if err != nil {
		return nil, err
	}
	return detectSuggestions(cands, recurring, dismissed), nil
}

// DismissSuggestion: someone in the space says key is not recurring.
func (s *Service) DismissSuggestion(ctx context.Context, householdID, userID uuid.UUID, key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > suggestionMaxKeyLen {
		return domain.NewValidationError("key", "clave inválida")
	}
	return s.repo.DismissSuggestion(ctx, householdID, userID, key)
}

// ===================== repository =====================

func (r *Repository) SuggestionCandidates(ctx context.Context, householdID uuid.UUID, from, to time.Time) ([]candidate, error) {
	rows, err := r.q.ListRecurringSuggestionCandidates(ctx, sqlcgen.ListRecurringSuggestionCandidatesParams{
		HouseholdID: householdID,
		FromDate:    pgtype.Date{Time: from, Valid: true},
		ToDate:      pgtype.Date{Time: to, Valid: true},
	})
	if err != nil {
		return nil, err
	}
	out := make([]candidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, candidate{
			Description: row.Description, Currency: row.Currency, Amount: money.FromNumeric(row.Amount),
			SpentAt: row.SpentAt.Time, CategoryID: row.CategoryID, PaymentMethodID: row.PaymentMethodID,
		})
	}
	return out, nil
}

func (r *Repository) SuggestionDismissals(ctx context.Context, householdID uuid.UUID) (map[string]bool, error) {
	keys, err := r.q.ListRecurringSuggestionDismissals(ctx, householdID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

func (r *Repository) DismissSuggestion(ctx context.Context, householdID, userID uuid.UUID, key string) error {
	return r.q.DismissRecurringSuggestion(ctx, sqlcgen.DismissRecurringSuggestionParams{
		HouseholdID: householdID, Key: key, DismissedBy: &userID,
	})
}
