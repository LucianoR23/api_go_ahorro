package inbound

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	sqlcgen "github.com/LucianoR23/valuo_api/internal/db/sqlc"
	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/expenseparse"
)

// Retention (plan §8b): Gmail codes are only useful for a moment; resolved
// mails are kept a while so a forward that arrives twice is still ignored.
const (
	CodeRetention     = 24 * time.Hour
	ResolvedRetention = 30 * 24 * time.Hour
	// MaxSenderPending caps what mails from unknown senders can pile up.
	MaxSenderPending = 20
)

// Reader reads a mail the rules do not know (expenseparse.Parser).
type Reader interface {
	ReadEmail(ctx context.Context, userID, householdID uuid.UUID, subject, text string) (r expenseparse.Reading, finance, ok bool)
}

type Service struct {
	q      *sqlcgen.Queries
	keys   *Keys
	reader Reader
	domain string
	prefix string
	loc    *time.Location
	now    func() time.Time
	logger *slog.Logger
}

// NewService: domain is INBOUND_DOMAIN ("in-next.valuo.ar"); prefix is
// INBOUND_LOCAL_PREFIX ("in+"). Cloudflare has no catch-all on a subdomain,
// so one rule for in@<domain> sends every in+<token>@<domain> to the Worker
// through subaddressing (decided with the user on 2026-11-14).
func NewService(q *sqlcgen.Queries, keys *Keys, reader Reader, domain, prefix string, loc *time.Location, now func() time.Time, logger *slog.Logger) *Service {
	return &Service{q: q, keys: keys, reader: reader, domain: strings.ToLower(domain), prefix: strings.ToLower(prefix),
		loc: loc, now: now, logger: logger}
}

// Keys is the key set, used by the handler to check signatures.
func (s *Service) Keys() *Keys { return s.keys }

// Address returns the user's address, creating it the first time.
func (s *Service) Address(ctx context.Context, userID uuid.UUID) (string, error) {
	row, err := s.q.GetInboundAddress(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		local := newLocal()
		row, err = s.q.CreateInboundAddress(ctx, sqlcgen.CreateInboundAddressParams{
			UserID: userID, LocalHash: s.keys.Hash(local), LocalEnc: s.keys.Seal(local), KeyID: keyID,
		})
	}
	if err != nil {
		return "", err
	}
	local, err := s.keys.Open(row.LocalEnc)
	if err != nil {
		return "", err
	}
	return s.prefix + local + "@" + s.domain, nil
}

var lowerBase32 = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// newLocal is "u_" and 16 random characters (80 bits): not guessable.
func newLocal() string {
	b := make([]byte, 10)
	if _, err := rand.Read(b); err != nil {
		panic("inbound: no randomness: " + err.Error())
	}
	return "u_" + lowerBase32.EncodeToString(b)
}

// normalizeSender makes the forwarding address comparable to the account
// email: lower case, no "+tag" (Gmail forwards as
// "ana+caf_=u_x=in.example@gmail.com"), and no dots for Gmail.
func normalizeSender(addr string) string {
	addr = strings.ToLower(strings.TrimSpace(addr))
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 {
		return addr
	}
	local, domain := addr[:at], addr[at+1:]
	if i := strings.IndexByte(local, '+'); i > 0 {
		local = local[:i]
	}
	if domain == "gmail.com" || domain == "googlemail.com" {
		local = strings.ReplaceAll(local, ".", "")
		domain = "gmail.com"
	}
	return local + "@" + domain
}

// Receive handles one mail the Worker posted. to and from are the envelope
// recipient and sender. A mail for no known address is dropped without
// saying so. The raw mail is never stored.
func (s *Service) Receive(ctx context.Context, to, from string, raw []byte) error {
	to = strings.ToLower(strings.TrimSpace(to))
	at := strings.LastIndexByte(to, '@')
	if at <= 0 || to[at+1:] != s.domain {
		return nil
	}
	// The token is what follows the prefix; a bare token is taken too, so
	// the address format can change without losing anyone's address.
	token := strings.TrimPrefix(to[:at], s.prefix)
	if i := strings.IndexByte(token, '+'); i >= 0 {
		token = token[i+1:]
	}
	if i := strings.IndexByte(token, '+'); i >= 0 {
		token = token[:i]
	}
	rcpt, err := s.q.InboundRecipient(ctx, s.keys.Hash(token))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	m, perr := ParseMail(raw)
	msg := sqlcgen.InsertInboundMessageParams{UserID: rcpt.UserID, DedupeHash: s.dedupe(m.MessageID, raw)}
	if perr != nil {
		msg.Kind, msg.Status = "expense", "unreadable"
		return s.insert(ctx, msg)
	}

	// Gmail asks to confirm a new forwarding address with a code sent
	// straight from Google, before any forwarded mail.
	if m.From == gmailForwarding && strings.HasSuffix(strings.ToLower(from), "@google.com") {
		code := gmailCode(m)
		if code == "" {
			return nil
		}
		msg.Kind, msg.Status, msg.GmailCode = "gmail_confirmation", "pending", text(code)
		return s.insert(ctx, msg)
	}

	sender := normalizeSender(from)
	accepted := sender == normalizeSender(rcpt.Email)
	for _, h := range rcpt.SenderHashes {
		if string(h) == string(s.keys.Hash(sender)) {
			accepted = true
		}
	}
	if !accepted {
		n, err := s.q.CountSenderPending(ctx, rcpt.UserID)
		if err != nil {
			return err
		}
		if n >= MaxSenderPending {
			return nil
		}
		msg.Status, msg.SenderEnc = "sender_pending", s.keys.Seal(sender)
	}

	reading, source, v := readRules(m, s.loc)
	// The AI only reads mails from accepted senders: an unknown one must not
	// spend the person's AI quota.
	if v == verdictUnknown && accepted {
		hh, err := s.q.GetPersonalHouseholdForUser(ctx, rcpt.UserID)
		if err == nil {
			r, finance, ok := s.reader.ReadEmail(ctx, rcpt.UserID, hh.ID, m.Subject, m.Text)
			switch {
			case ok && finance && r.Amount > 0:
				reading, source, v = r, "ai", verdictExpense
				if reading.Date == "" && !m.Date.IsZero() {
					reading.Date = m.Date.In(s.loc).Format(time.DateOnly)
				}
			case ok && !finance:
				v = verdictNotSpent
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}

	switch v {
	case verdictNotSpent:
		// Kept only to ignore a repeat; nothing to show.
		msg.Kind, msg.Status, msg.SenderEnc = "other", "dismissed", nil
	case verdictExpense:
		msg.Kind = "expense"
		if msg.Status == "" {
			msg.Status = "pending"
		}
		b, _ := json.Marshal(reading)
		msg.Parsed = b
		msg.Source = text(source)
	default:
		msg.Kind = "expense"
		if msg.Status == "" {
			msg.Status = "unreadable"
		}
		if source != "" {
			msg.Source = text(source)
		}
	}
	return s.insert(ctx, msg)
}

func (s *Service) dedupe(messageID string, raw []byte) []byte {
	if messageID != "" {
		return s.keys.Hash("message-id:" + messageID)
	}
	sum := sha256.Sum256(raw)
	return s.keys.Hash("raw:" + string(sum[:]))
}

func (s *Service) insert(ctx context.Context, p sqlcgen.InsertInboundMessageParams) error {
	_, err := s.q.InsertInboundMessage(ctx, p)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // the same mail again
	}
	return err
}

func text(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// Message is one open item for the app.
type Message struct {
	ID     uuid.UUID
	Kind   string
	Status string
	Source string
	// Reading and Sentence are set for expense mails that were read;
	// Sentence goes into the "Contalo como te salga" box.
	Reading   *expenseparse.Reading
	Sentence  string
	Sender    string // only while sender_pending
	GmailCode string
	Received  time.Time
}

// List returns open expense mails and Gmail codes of the last day.
func (s *Service) List(ctx context.Context, userID uuid.UUID) ([]Message, error) {
	now := s.now()
	rows, err := s.q.ListOpenInboundMessages(ctx, sqlcgen.ListOpenInboundMessagesParams{
		UserID: userID, CodesSince: pgtype.Timestamptz{Time: now.Add(-CodeRetention), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	y, mo, d := now.In(s.loc).Date()
	today := time.Date(y, mo, d, 0, 0, 0, 0, s.loc)
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		m := Message{ID: r.ID, Kind: r.Kind, Status: r.Status, Source: r.Source.String,
			GmailCode: r.GmailCode.String, Received: r.ReceivedAt.Time}
		if len(r.Parsed) > 0 {
			var rd expenseparse.Reading
			if json.Unmarshal(r.Parsed, &rd) == nil {
				m.Reading = &rd
				m.Sentence = expenseparse.Sentence(rd, today)
			}
		}
		if r.SenderEnc != nil {
			if sender, err := s.keys.Open(r.SenderEnc); err == nil {
				m.Sender = sender
			}
		}
		out = append(out, m)
	}
	return out, nil
}

// AcceptSender trusts the sender of a sender_pending mail from now on.
func (s *Service) AcceptSender(ctx context.Context, userID, id uuid.UUID) error {
	row, err := s.q.GetInboundMessage(ctx, sqlcgen.GetInboundMessageParams{ID: id, UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.Status != "sender_pending" {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	sender, err := s.keys.Open(row.SenderEnc)
	if err != nil {
		return err
	}
	if err := s.q.AcceptInboundSender(ctx, sqlcgen.AcceptInboundSenderParams{UserID: userID, SenderHash: s.keys.Hash(sender)}); err != nil {
		return err
	}
	_, err = s.q.AcceptInboundMessageSender(ctx, sqlcgen.AcceptInboundMessageSenderParams{ID: id, UserID: userID})
	return err
}

// Dismiss closes an open mail without an expense.
func (s *Service) Dismiss(ctx context.Context, userID, id uuid.UUID) error {
	return s.resolve(ctx, userID, id, "dismissed", nil)
}

// Confirm links an open mail to the expense saved from it.
func (s *Service) Confirm(ctx context.Context, userID, id, expenseID uuid.UUID) error {
	e, err := s.q.GetExpenseByID(ctx, expenseID)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && e.CreatedBy != userID {
		return domain.NewValidationError("expenseId", "el gasto no existe")
	}
	if err != nil {
		return err
	}
	return s.resolve(ctx, userID, id, "confirmed", &expenseID)
}

func (s *Service) resolve(ctx context.Context, userID, id uuid.UUID, status string, expenseID *uuid.UUID) error {
	n, err := s.q.ResolveInboundMessage(ctx, sqlcgen.ResolveInboundMessageParams{
		Status: status, ExpenseID: expenseID, ID: id, UserID: userID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// Cleanup deletes old codes and resolved mails (daily job).
func (s *Service) Cleanup(ctx context.Context) (int64, error) {
	now := s.now()
	return s.q.DeleteOldInboundMessages(ctx, sqlcgen.DeleteOldInboundMessagesParams{
		CodesBefore:    pgtype.Timestamptz{Time: now.Add(-CodeRetention), Valid: true},
		ResolvedBefore: pgtype.Timestamptz{Time: now.Add(-ResolvedRetention), Valid: true},
	})
}
