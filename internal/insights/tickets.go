package insights

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/google/uuid"
)

// TicketTTL is how long a stream ticket is valid.
const TicketTTL = 60 * time.Second

// streamTicket: who may open the stream, for which household, until when.
type streamTicket struct {
	userID      uuid.UUID
	householdID uuid.UUID
	expiresAt   time.Time
}

// TicketStore issues single-use stream tickets (plan 0.9). EventSource cannot
// send an Authorization header, and putting the access token in the URL
// leaks a 15-minute credential into any log that records URLs. A ticket is a
// random value that opens one stream, once, within a minute, and grants
// nothing else.
//
// Tickets live in memory: valid with one API instance. With several, move
// them to the database (insert on issue, DELETE ... RETURNING on use).
type TicketStore struct {
	mu      sync.Mutex
	tickets map[string]streamTicket
	now     func() time.Time
}

func NewTicketStore() *TicketStore {
	return &TicketStore{tickets: map[string]streamTicket{}, now: time.Now}
}

// Issue returns a new ticket for the user and household, and drops the
// expired ones (no background goroutine needed).
func (s *TicketStore) Issue(userID, householdID uuid.UUID) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for k, t := range s.tickets {
		if now.After(t.expiresAt) {
			delete(s.tickets, k)
		}
	}
	s.tickets[ticket] = streamTicket{userID: userID, householdID: householdID, expiresAt: now.Add(TicketTTL)}
	return ticket, nil
}

// Redeem consumes the ticket: it works once, and only before it expires.
func (s *TicketStore) Redeem(ticket string) (userID, householdID uuid.UUID, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, found := s.tickets[ticket]
	if !found {
		return uuid.Nil, uuid.Nil, false
	}
	delete(s.tickets, ticket)
	if s.now().After(t.expiresAt) {
		return uuid.Nil, uuid.Nil, false
	}
	return t.userID, t.householdID, true
}
