package insights

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestTicketStore(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	s := NewTicketStore()
	s.now = func() time.Time { return now }
	user, hh := uuid.New(), uuid.New()

	a, err := s.Issue(user, hh)
	if err != nil || len(a) < 40 {
		t.Fatalf("ticket %q, err %v", a, err)
	}
	b, _ := s.Issue(user, hh)
	if a == b {
		t.Fatal("two tickets are equal")
	}

	gotUser, gotHH, ok := s.Redeem(a)
	if !ok || gotUser != user || gotHH != hh {
		t.Errorf("redeem = %v %v %v", gotUser, gotHH, ok)
	}
	if _, _, ok := s.Redeem(a); ok {
		t.Error("a ticket worked twice")
	}
	if _, _, ok := s.Redeem("made-up"); ok {
		t.Error("an unknown ticket worked")
	}

	now = now.Add(TicketTTL + time.Second)
	if _, _, ok := s.Redeem(b); ok {
		t.Error("an expired ticket worked")
	}

	// Issuing drops expired tickets, so the map does not grow forever.
	c, _ := s.Issue(user, hh)
	now = now.Add(TicketTTL + time.Second)
	if _, err := s.Issue(user, hh); err != nil {
		t.Fatal(err)
	}
	if _, kept := s.tickets[c]; kept || len(s.tickets) != 1 {
		t.Errorf("expired tickets kept: %d left", len(s.tickets))
	}
}
