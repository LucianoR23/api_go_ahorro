package push_test

// Pushes follow the person's preferences and wait out quiet hours (plan 10.1).

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/google/uuid"

	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func TestPushPreferences(t *testing.T) {
	t.Parallel()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pushSrv.Close)

	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	var clock atomic.Value
	clock.Store(time.Date(2026, 11, 12, 2, 0, 0, 0, ba))
	s := apitest.New(t,
		apitest.WithNow(func() time.Time { return clock.Load().(time.Time) }),
		apitest.WithConfig(func(c *config.Config) {
			c.VAPIDPublicKey, c.VAPIDPrivateKey = pub, priv
			c.PushExtraHosts = []string{strings.TrimPrefix(pushSrv.URL, "http://")}
		}))

	owner := s.Register(t)
	hh := s.CreateHousehold(t, owner, "Casa")
	member := s.AddMember(t, owner, hh.ID)
	cash := s.Cash(t, owner)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: member.Token,
		Body: map[string]any{"endpoint": pushSrv.URL + "/m", "keys": browserKeys(t)}}).Status(t, http.StatusCreated)
	share := func(amount int) {
		s.CreateExpense(t, owner, hh.ID, map[string]any{"paymentMethodId": cash.ID, "amount": amount,
			"spentAt": "2026-11-11", "isShared": true})
	}
	type held struct {
		Args        json.RawMessage
		ScheduledAt time.Time
	}
	heldPushes := func() []held {
		rows, err := s.Pool.Query(context.Background(),
			`SELECT args, scheduled_at FROM river_job WHERE kind = 'push_delivery' AND args->>'kind' = 'shared_expense' ORDER BY id`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []held
		for rows.Next() {
			var h held
			if err := rows.Scan(&h.Args, &h.ScheduledAt); err != nil {
				t.Fatal(err)
			}
			out = append(out, h)
		}
		return out
	}

	// 02:00, inside the default quiet hours: the push waits until 08:00.
	// (The owner's "they accepted your invite" push waits too; not counted.)
	share(1000)
	eventually(t, "the push to be held", func() bool { return len(heldPushes()) == 1 })
	h := heldPushes()[0]
	if want := time.Date(2026, 11, 12, 8, 0, 0, 0, ba); !h.ScheduledAt.Equal(want) {
		t.Errorf("scheduled at %v, want %v", h.ScheduledAt, want)
	}
	if hits.Load() != 0 {
		t.Fatalf("pushed during quiet hours (%d)", hits.Load())
	}
	var args struct {
		UserID  uuid.UUID       `json:"user_id"`
		Kind    string          `json:"kind"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(h.Args, &args); err != nil {
		t.Fatal(err)
	}
	if args.UserID.String() != member.ID || args.Kind != "shared_expense" || !strings.Contains(string(args.Payload), "Nuevo gasto compartido") {
		t.Errorf("held push = %s", h.Args)
	}
	// At 08:00 the job delivers it.
	if err := s.Jobs.DeliverPush(context.Background(), args.UserID, args.Kind, args.Payload); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Fatalf("held push delivered %d times, want 1", hits.Load())
	}

	// During the day it goes right away.
	clock.Store(time.Date(2026, 11, 12, 15, 0, 0, 0, ba))
	share(2000)
	eventually(t, "the daytime push", func() bool { return hits.Load() == 2 })

	// Muted: neither sent nor held. A held one muted meanwhile is dropped too.
	s.Do(t, apitest.Req{Method: http.MethodPut, Path: "/me/notification-preferences", Token: member.Token, Body: map[string]any{
		"kinds":      []map[string]any{{"kind": "shared_expense", "channels": map[string]bool{"push": false}}},
		"quietHours": map[string]any{"enabled": true, "start": "23:00", "end": "08:00"},
	}}).Status(t, http.StatusOK)
	share(3000)
	if err := s.Jobs.DeliverPush(context.Background(), args.UserID, args.Kind, args.Payload); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // delivery is async: give a wrong push time to land
	if hits.Load() != 2 || len(heldPushes()) != 1 {
		t.Errorf("muted: %d hits, %d held", hits.Load(), len(heldPushes()))
	}

	// The test push the person asks for skips both.
	clock.Store(time.Date(2026, 11, 12, 3, 0, 0, 0, ba))
	s.MakeSuperadmin(t, member)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/admin/push/test", Token: member.Token}).Status(t, http.StatusOK)
	eventually(t, "the test push", func() bool { return hits.Load() == 3 })
}
