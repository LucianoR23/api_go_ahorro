package fxrates

// Characterization tests (item 0.21d) of the FX fetcher, refresh, cache
// hydration and the cold-cache path of Convert, against a real database and
// local stand-ins for DolarAPI and Bluelytics (plan 5.1).

import (
	"context"
	"errors"
	"github.com/shopspring/decimal"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LucianoR23/valuo_api/internal/domain"
	"github.com/LucianoR23/valuo_api/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

const bluelyticsJSON = `{
  "oficial":      {"value_avg": 1000.5, "value_sell": 1020, "value_buy": 981},
  "blue":         {"value_avg": 1200,   "value_sell": 1210, "value_buy": 1190},
  "oficial_euro": {"value_avg": 1100,   "value_sell": 1120, "value_buy": 1080},
  "blue_euro":    {"value_avg": 1300,   "value_sell": 1310, "value_buy": 1290},
  "last_update":  "2026-11-05T12:00:00-03:00"
}`

const dolaresJSON = `[
  {"moneda":"USD","casa":"oficial","compra":1520,"venta":1570,"fechaActualizacion":"2026-11-05T18:55:00.000Z"},
  {"moneda":"USD","casa":"blue","compra":1535,"venta":1555,"fechaActualizacion":"2026-11-09T20:58:00.000Z"},
  {"moneda":"USD","casa":"bolsa","compra":1560.2,"venta":1587.9,"fechaActualizacion":"2026-11-09T20:58:00.000Z"},
  {"moneda":"USD","casa":"contadoconliqui","compra":1658.4,"venta":1664.9,"fechaActualizacion":"2026-11-09T20:58:00.000Z"},
  {"moneda":"USD","casa":"mayorista","compra":1536.5,"venta":1545.5,"fechaActualizacion":"2026-11-05T18:06:00.000Z"},
  {"moneda":"USD","casa":"cripto","compra":1661.05,"venta":1666,"fechaActualizacion":"2026-11-09T20:58:00.000Z"},
  {"moneda":"USD","casa":"tarjeta","compra":1976,"venta":2041,"fechaActualizacion":"2026-11-05T18:55:00.000Z"},
  {"moneda":"USD","casa":"futuro","compra":1,"venta":2,"fechaActualizacion":"2026-11-05T18:55:00.000Z"}
]`

const cotizacionesJSON = `[
  {"moneda":"USD","casa":"oficial","compra":1520,"venta":1570,"fechaActualizacion":"2026-11-05T17:59:00.000Z"},
  {"moneda":"EUR","casa":"oficial","compra":1724.414,"venta":1738.3784,"fechaActualizacion":"2026-11-05T17:59:00.000Z"},
  {"moneda":"BRL","casa":"oficial","compra":281.3,"venta":281.46,"fechaActualizacion":"2026-11-05T18:02:00.000Z"}
]`

// upstream: one answer of a fake upstream.
type upstream struct {
	status int
	body   string
}

func ok(body string) upstream { return upstream{http.StatusOK, body} }

var allGood = map[string]upstream{
	"/v1/dolares":      ok(dolaresJSON),
	"/v1/cotizaciones": ok(cotizacionesJSON),
	"/bluelytics":      ok(bluelyticsJSON),
}

// fakeUpstreams serves DolarAPI and Bluelytics from one local server; a
// path missing from routes answers 502.
func fakeUpstreams(t *testing.T, routes map[string]upstream) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, found := routes[r.URL.Path]
		if !found {
			u = upstream{http.StatusBadGateway, "down"}
		}
		w.WriteHeader(u.status)
		_, _ = io.WriteString(w, u.body)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(srv.Client())
	f.dolarAPIURL = srv.URL
	f.bluelyticsURL = srv.URL + "/bluelytics"
	return f
}

// onlyBluelytics: DolarAPI down, Bluelytics answering body.
func onlyBluelytics(t *testing.T, status int, body string) *Fetcher {
	t.Helper()
	return fakeUpstreams(t, map[string]upstream{"/bluelytics": {status, body}})
}

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func find(rates []domain.ExchangeRate, currency string, source domain.RateSource) (domain.ExchangeRate, bool) {
	for _, r := range rates {
		if r.Currency == currency && r.Source == source {
			return r, true
		}
	}
	return domain.ExchangeRate{}, false
}

func TestFetch(t *testing.T) {
	t.Parallel()

	t.Run("every DolarAPI dollar, its official euro and the Bluelytics blue euro", func(t *testing.T) {
		rates, err := fakeUpstreams(t, allGood).Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		// 7 dollars (the unknown "futuro" is dropped), the official euro from
		// DolarAPI and the blue euro from Bluelytics.
		if len(rates) != 9 {
			t.Fatalf("got %d rates: %+v", len(rates), rates)
		}
		mep, _ := find(rates, "USD", domain.RateSourceBolsa)
		if mep.RateSell != 1587.9 || mep.RateBuy != 1560.2 || math.Abs(mep.RateAvg-1574.05) > 1e-9 ||
			!mep.LastUpdate.Equal(time.Date(2026, 11, 9, 20, 58, 0, 0, time.UTC)) {
			t.Errorf("USD bolsa = %+v", mep)
		}
		if blue, _ := find(rates, "USD", domain.RateSourceBlue); blue.RateSell != 1555 {
			t.Errorf("USD blue = %+v, want DolarAPI's, not Bluelytics'", blue)
		}
		if eur, _ := find(rates, "EUR", domain.RateSourceOficial); eur.RateSell != 1738.3784 {
			t.Errorf("EUR oficial = %+v", eur)
		}
		if eur, _ := find(rates, "EUR", domain.RateSourceBlue); eur.RateSell != 1310 {
			t.Errorf("EUR blue = %+v", eur)
		}
	})

	t.Run("DolarAPI down: Bluelytics' four rates and the error to log", func(t *testing.T) {
		rates, err := onlyBluelytics(t, http.StatusOK, bluelyticsJSON).Fetch(context.Background())
		if err == nil || len(rates) != 4 {
			t.Fatalf("rates = %d, err = %v", len(rates), err)
		}
		want := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)
		usdBlue, _ := find(rates, "USD", domain.RateSourceBlue)
		if usdBlue.RateAvg != 1200 || usdBlue.RateBuy != 1190 || usdBlue.RateSell != 1210 || !usdBlue.LastUpdate.Equal(want) {
			t.Errorf("USD blue = %+v", usdBlue)
		}
	})

	t.Run("without the euro from DolarAPI, Bluelytics' official euro", func(t *testing.T) {
		rates, err := fakeUpstreams(t, map[string]upstream{"/v1/dolares": ok(dolaresJSON), "/bluelytics": ok(bluelyticsJSON)}).
			Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if eur, _ := find(rates, "EUR", domain.RateSourceOficial); eur.RateSell != 1120 {
			t.Errorf("EUR oficial = %+v", eur)
		}
	})

	t.Run("missing dates fall back to now", func(t *testing.T) {
		before := time.Now().Add(-time.Second)
		rates, _ := onlyBluelytics(t, http.StatusOK, `{"blue":{"value_avg":1}}`).Fetch(context.Background())
		if len(rates) == 0 || rates[0].LastUpdate.Before(before) {
			t.Errorf("lastUpdate = %+v", rates)
		}
	})

	t.Run("both upstreams down or broken", func(t *testing.T) {
		if rates, err := onlyBluelytics(t, http.StatusBadGateway, "down").Fetch(context.Background()); err == nil || rates != nil {
			t.Error("502 did not fail")
		}
		if _, err := onlyBluelytics(t, http.StatusOK, "{not json").Fetch(context.Background()); err == nil {
			t.Error("bad JSON did not fail")
		}
		if _, err := fakeUpstreams(t, map[string]upstream{"/v1/dolares": ok("[]")}).Fetch(context.Background()); err == nil {
			t.Error("an empty DolarAPI answer and Bluelytics down did not fail")
		}
	})
}

func TestRefreshHydrateAndConvert(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	svc := NewService(repo, fakeUpstreams(t, allGood), quietLogger())
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(svc.Current()); got != 9 {
		t.Errorf("cache has %d rates after refresh", got)
	}
	stored, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 9 {
		t.Errorf("db has %d latest rates, want 9", len(stored))
	}

	t.Run("refreshing the same snapshot is idempotent", func(t *testing.T) {
		if err := svc.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM exchange_rates`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 9 {
			t.Errorf("exchange_rates rows = %d, want 9", n)
		}
	})

	t.Run("a fresh service hydrates its cache from the database", func(t *testing.T) {
		cold := NewService(repo, nil, quietLogger())
		if len(cold.Current()) != 0 {
			t.Fatal("cache not empty before hydrate")
		}
		if err := cold.Hydrate(ctx); err != nil {
			t.Fatal(err)
		}
		r, ok := find(cold.Current(), "EUR", domain.RateSourceBlue)
		if !ok || r.RateAvg != 1300 {
			t.Errorf("hydrated EUR blue = %+v", r)
		}
	})

	t.Run("convert with a cold cache reads the database", func(t *testing.T) {
		cold := NewService(repo, nil, quietLogger())
		got, rate, err := cold.Convert(ctx, decimal.NewFromInt(10), "USD", "ARS", domain.RateSourceBolsa)
		if err != nil || !got.Equal(decimal.NewFromInt(15879)) || !rate.Equal(decimal.RequireFromString("1587.9")) {
			t.Errorf("Convert = %v, %v, %v", got, rate, err)
		}
		if _, ok := find(cold.Current(), "USD", domain.RateSourceBolsa); !ok {
			t.Errorf("rate read from the database was not cached")
		}
	})

	t.Run("convert without any rate fails with not found", func(t *testing.T) {
		empty := NewService(NewRepository(testdb.New(t)), nil, quietLogger())
		_, _, err := empty.Convert(ctx, decimal.NewFromInt(1), "USD", "ARS", domain.RateSourceBolsa)
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a failed fetch keeps the previous cache", func(t *testing.T) {
		broken := NewService(repo, onlyBluelytics(t, http.StatusInternalServerError, "boom"), quietLogger())
		if err := broken.Hydrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := broken.Refresh(ctx); err == nil {
			t.Error("refresh against a failing upstream returned nil")
		}
		if len(broken.Current()) != 9 {
			t.Errorf("cache lost after a failed refresh: %d", len(broken.Current()))
		}
	})
}

func TestRefreshDiscardsZeroRates(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	// A good snapshot first, then one where EUR blue comes back as 0.
	good := NewService(repo, onlyBluelytics(t, http.StatusOK, bluelyticsJSON), quietLogger())
	if err := good.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	body := `{"blue":{"value_avg":1250,"value_buy":1240,"value_sell":1260},"blue_euro":{"value_avg":0},
	          "oficial":{"value_avg":1000},"oficial_euro":{"value_avg":1100},"last_update":"2026-11-06T12:00:00Z"}`
	good.fetcher = onlyBluelytics(t, http.StatusOK, body)
	if err := good.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	var zeros int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exchange_rates WHERE rate_avg <= 0`).Scan(&zeros); err != nil {
		t.Fatal(err)
	}
	if zeros != 0 {
		t.Errorf("zero rate stored in the database")
	}

	// Regression (found in 0.21d): the discarded 0 used to replace the cached
	// rate and reach GET /exchange-rates/current.
	if r, ok := find(good.Current(), "EUR", domain.RateSourceBlue); !ok || r.RateAvg != 1300 {
		t.Errorf("EUR blue = %+v, want the last good rate 1300", r)
	}
	if r, _ := find(good.Current(), "USD", domain.RateSourceBlue); r.RateAvg != 1250 {
		t.Errorf("USD blue = %v, want the new 1250", r.RateAvg)
	}
}

// Plan 5.2: a rate is stale when no upstream confirmed it for a day, not
// when its quote is old (the official dollar does not move on weekends).
func TestStaleRates(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	now := time.Now()

	if (domain.ExchangeRate{FetchedAt: now.Add(-23 * time.Hour)}).Stale(now) {
		t.Error("confirmed 23 h ago is stale")
	}
	if !(domain.ExchangeRate{FetchedAt: now.Add(-25 * time.Hour)}).Stale(now) || !(domain.ExchangeRate{}).Stale(now) {
		t.Error("confirmed 25 h ago, or never, is not stale")
	}

	svc := NewService(repo, fakeUpstreams(t, allGood), quietLogger())
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	for _, r := range svc.Current() {
		if r.Stale(time.Now()) {
			t.Errorf("fresh rate is stale: %+v", r)
		}
	}

	// The same quote returned again a day later is confirmed again, so a
	// fresh service reading the database does not see it as stale.
	if _, err := pool.Exec(ctx, `UPDATE exchange_rates SET fetched_at = now() - interval '2 days'`); err != nil {
		t.Fatal(err)
	}
	cold := NewService(repo, nil, quietLogger())
	if err := cold.Hydrate(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := find(cold.Current(), "USD", domain.RateSourceOficial); !r.Stale(time.Now()) {
		t.Errorf("rate not confirmed for 2 days is not stale: %+v", r)
	}
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := cold.Hydrate(ctx); err != nil {
		t.Fatal(err)
	}
	if r, _ := find(cold.Current(), "USD", domain.RateSourceOficial); r.Stale(time.Now()) {
		t.Errorf("rate confirmed again is still stale: %+v", r)
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM exchange_rates`).Scan(&n); err != nil || n != 9 {
		t.Errorf("rows = %d, %v; confirming must not duplicate quotes", n, err)
	}
}
