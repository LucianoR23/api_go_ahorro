package fxrates

// Characterization tests (item 0.21d) of the FX fetcher, refresh, cache
// hydration and the cold-cache path of Convert, against a real database and
// a local stand-in for Bluelytics.

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
	"github.com/LucianoR23/api_go_ahorra/internal/testutil/testdb"
)

func TestMain(m *testing.M) { os.Exit(testdb.Main(m)) }

const bluelyticsJSON = `{
  "oficial":      {"value_avg": 1000.5, "value_sell": 1020, "value_buy": 981},
  "blue":         {"value_avg": 1200,   "value_sell": 1210, "value_buy": 1190},
  "oficial_euro": {"value_avg": 1100,   "value_sell": 1120, "value_buy": 1080},
  "blue_euro":    {"value_avg": 1300,   "value_sell": 1310, "value_buy": 1290},
  "last_update":  "2026-11-05T12:00:00-03:00"
}`

func fakeBluelytics(t *testing.T, status int, body string) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(srv.Client())
	f.url = srv.URL
	return f
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

	t.Run("maps the four Bluelytics rates", func(t *testing.T) {
		rates, err := fakeBluelytics(t, http.StatusOK, bluelyticsJSON).Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(rates) != 4 {
			t.Fatalf("got %d rates", len(rates))
		}
		want := time.Date(2026, 11, 5, 15, 0, 0, 0, time.UTC)
		usdBlue, _ := find(rates, "USD", domain.RateSourceBlue)
		eurOf, _ := find(rates, "EUR", domain.RateSourceOficial)
		if usdBlue.RateAvg != 1200 || usdBlue.RateBuy != 1190 || usdBlue.RateSell != 1210 || !usdBlue.LastUpdate.Equal(want) {
			t.Errorf("USD blue = %+v", usdBlue)
		}
		if eurOf.RateAvg != 1100 {
			t.Errorf("EUR oficial = %+v", eurOf)
		}
	})

	t.Run("missing last_update falls back to now", func(t *testing.T) {
		before := time.Now().Add(-time.Second)
		rates, err := fakeBluelytics(t, http.StatusOK, `{"blue":{"value_avg":1}}`).Fetch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if rates[0].LastUpdate.Before(before) {
			t.Errorf("lastUpdate = %v", rates[0].LastUpdate)
		}
	})

	t.Run("upstream errors", func(t *testing.T) {
		if _, err := fakeBluelytics(t, http.StatusBadGateway, "down").Fetch(context.Background()); err == nil {
			t.Error("502 did not fail")
		}
		if _, err := fakeBluelytics(t, http.StatusOK, "{not json").Fetch(context.Background()); err == nil {
			t.Error("bad JSON did not fail")
		}
	})
}

func TestRefreshHydrateAndConvert(t *testing.T) {
	t.Parallel()
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	svc := NewService(repo, fakeBluelytics(t, http.StatusOK, bluelyticsJSON), quietLogger())
	if err := svc.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got := len(svc.Current()); got != 4 {
		t.Errorf("cache has %d rates after refresh", got)
	}
	stored, err := repo.ListLatest(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 4 {
		t.Errorf("db has %d latest rates, want 4", len(stored))
	}

	t.Run("refreshing the same snapshot is idempotent", func(t *testing.T) {
		if err := svc.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM exchange_rates`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 4 {
			t.Errorf("exchange_rates rows = %d, want 4", n)
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
		got, rate, err := cold.Convert(ctx, 10, "USD", "ARS")
		if err != nil || got != 12000 || rate != 1200 {
			t.Errorf("Convert = %v, %v, %v", got, rate, err)
		}
		if _, ok := find(cold.Current(), "USD", domain.RateSourceBlue); !ok {
			t.Errorf("rate read from the database was not cached")
		}
	})

	t.Run("convert without any rate fails with not found", func(t *testing.T) {
		empty := NewService(NewRepository(testdb.New(t)), nil, quietLogger())
		_, _, err := empty.Convert(ctx, 1, "USD", "ARS")
		if !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("a failed fetch keeps the previous cache", func(t *testing.T) {
		broken := NewService(repo, fakeBluelytics(t, http.StatusInternalServerError, "boom"), quietLogger())
		if err := broken.Hydrate(ctx); err != nil {
			t.Fatal(err)
		}
		if err := broken.Refresh(ctx); err == nil {
			t.Error("refresh against a failing upstream returned nil")
		}
		if len(broken.Current()) != 4 {
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
	good := NewService(repo, fakeBluelytics(t, http.StatusOK, bluelyticsJSON), quietLogger())
	if err := good.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	body := `{"blue":{"value_avg":1250,"value_buy":1240,"value_sell":1260},"blue_euro":{"value_avg":0},
	          "oficial":{"value_avg":1000},"oficial_euro":{"value_avg":1100},"last_update":"2026-11-06T12:00:00Z"}`
	good.fetcher = fakeBluelytics(t, http.StatusOK, body)
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
