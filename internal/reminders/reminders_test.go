package reminders_test

// Daily logging nudge (plan 10.2).

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"github.com/LucianoR23/valuo_api/internal/config"
	"github.com/LucianoR23/valuo_api/internal/testutil/apitest"
)

func browserKeys(t *testing.T) map[string]string {
	t.Helper()
	priv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 16)
	_, _ = rand.Read(secret)
	return map[string]string{
		"p256dh": base64.RawURLEncoding.EncodeToString(priv.PublicKey().Bytes()),
		"auth":   base64.RawURLEncoding.EncodeToString(secret),
	}
}

func TestDailyReminders(t *testing.T) {
	t.Parallel()
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var hits []string // endpoint path of each push: the person's name
	pushSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, strings.TrimPrefix(r.URL.Path, "/"))
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(pushSrv.Close)
	got := func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := slices.Clone(hits)
		slices.Sort(out)
		hits = nil
		return out
	}

	ba, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		t.Fatal(err)
	}
	slot := time.Date(2026, 11, 11, 21, 0, 0, 0, ba)
	now := slot.Add(time.Minute)
	s := apitest.New(t,
		apitest.WithNow(func() time.Time { return now }),
		apitest.WithConfig(func(c *config.Config) {
			c.VAPIDPublicKey, c.VAPIDPrivateKey = pub, priv
			c.PushExtraHosts = []string{strings.TrimPrefix(pushSrv.URL, "http://")}
		}))

	// person registers, finishes onboarding, subscribes a device named
	// after them and saves prefs (reminder and quiet hours).
	person := func(name string, reminder map[string]any, quiet map[string]any) apitest.User {
		u := s.Register(t)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/me/onboarding/complete", Token: u.Token}).Status(t, http.StatusOK)
		s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/push/subscriptions", Token: u.Token,
			Body: map[string]any{"endpoint": pushSrv.URL + "/" + name, "keys": browserKeys(t)}}).Status(t, http.StatusCreated)
		if quiet == nil {
			quiet = map[string]any{"enabled": true, "start": "23:00", "end": "08:00"}
		}
		body := map[string]any{"kinds": []any{}, "quietHours": quiet}
		if reminder != nil {
			body["dailyReminder"] = reminder
		}
		s.Do(t, apitest.Req{Method: http.MethodPut, Path: "/me/notification-preferences", Token: u.Token, Body: body}).
			Status(t, http.StatusOK)
		return u
	}
	at := func(hhmm string) map[string]any { return map[string]any{"enabled": true, "at": hhmm} }

	person("idle", at("21:00"), nil)
	spent := person("spent", at("21:00"), nil)
	s.CreateExpense(t, spent, s.PersonalSpace(t, spent), map[string]any{
		"paymentMethodId": s.Cash(t, spent).ID, "amount": 4500, "spentAt": "2026-11-11"})
	paid := person("paid", at("21:00"), nil)
	s.Do(t, apitest.Req{Method: http.MethodPost, Path: "/incomes", Token: paid.Token, Household: s.PersonalSpace(t, paid), Body: map[string]any{
		"amount": 1200000, "currency": "ARS", "source": "salary", "description": "Sueldo", "receivedAt": "2026-11-11"}}).Status(t, http.StatusCreated)
	yesterday := person("yesterday", at("21:00"), nil)
	s.CreateExpense(t, yesterday, s.PersonalSpace(t, yesterday), map[string]any{
		"paymentMethodId": s.Cash(t, yesterday).ID, "amount": 900, "spentAt": "2026-11-10"})
	if _, err := s.Pool.Exec(context.Background(),
		`UPDATE expenses SET created_at = $1 WHERE created_by = $2`, slot.Add(-22*time.Hour), yesterday.ID); err != nil {
		t.Fatal(err)
	}
	person("off", nil, nil)                       // never turned it on
	person("later", at("21:15"), nil)             // next slot
	person("asleep", at("21:00"), map[string]any{ // quiet hours from 20:00
		"enabled": true, "start": "20:00", "end": "08:00"})

	ctx := context.Background()
	sent, err := s.Jobs.DailyReminders(ctx, slot)
	if err != nil {
		t.Fatal(err)
	}
	// "asleep" is due but quiet hours drop the push (not held for morning).
	if h, want := got(), []string{"idle", "yesterday"}; !slices.Equal(h, want) || sent != 3 {
		t.Errorf("pushed to %v (sent %d), want %v", h, sent, want)
	}
	var held int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE kind = 'push_delivery'`).Scan(&held); err != nil {
		t.Fatal(err)
	}
	if held != 0 {
		t.Errorf("%d reminders held for later, want none", held)
	}

	// The next slot reaches "later" only.
	now = slot.Add(16 * time.Minute)
	if _, err := s.Jobs.DailyReminders(ctx, slot.Add(15*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if h, want := got(), []string{"later"}; !slices.Equal(h, want) {
		t.Errorf("21:15 pushed to %v, want %v", h, want)
	}

	// A slot worked an hour late is skipped.
	now = slot.Add(time.Hour)
	if n, err := s.Jobs.DailyReminders(ctx, slot); err != nil || n != 0 {
		t.Errorf("stale slot: sent %d, err %v", n, err)
	}
	if h := got(); len(h) != 0 {
		t.Errorf("stale slot pushed to %v", h)
	}
}
