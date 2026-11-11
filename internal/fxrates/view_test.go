package fxrates

// Plan 5.6: the currencies "Ver en" shows besides pesos. DolarAPI's real,
// Chilean and Uruguayan peso, USDT at the crypto dollar, and bitcoin and
// ether from CoinGecko. No database needed.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

const viewCotizacionesJSON = `[
  {"moneda":"EUR","casa":"oficial","compra":1724.414,"venta":1738.3784,"fechaActualizacion":"2026-11-10T17:56:00.000Z"},
  {"moneda":"BRL","casa":"oficial","compra":276.7888,"venta":276.9358,"fechaActualizacion":"2026-11-10T17:57:00.000Z"},
  {"moneda":"CLP","casa":"oficial","compra":1.5709,"venta":1.5709,"fechaActualizacion":"2026-11-10T17:56:00.000Z"},
  {"moneda":"UYU","casa":"oficial","compra":38.1984,"venta":38.1987,"fechaActualizacion":"2026-11-10T17:57:00.000Z"},
  {"moneda":"BRL","casa":"blue","compra":1,"venta":2,"fechaActualizacion":"2026-11-10T17:57:00.000Z"},
  {"moneda":"ARS","casa":"oficial","compra":1,"venta":1,"fechaActualizacion":"2026-11-10T17:57:00.000Z"}
]`

const coinGeckoJSON = `{
  "bitcoin":  {"ars": 113583538, "last_updated_at": 1794362796},
  "ethereum": {"ars": 3171144.5, "last_updated_at": 1794362790}
}`

func TestFetchViewCurrencies(t *testing.T) {
	t.Parallel()
	rates, err := fakeUpstreams(t, map[string]upstream{
		"/v1/dolares":      ok(dolaresJSON),
		"/v1/cotizaciones": ok(viewCotizacionesJSON),
		"/bluelytics":      ok(bluelyticsJSON),
	}).Fetch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for cur, sell := range map[string]float64{"BRL": 276.9358, "CLP": 1.5709, "UYU": 38.1987} {
		if r, found := find(rates, cur, domain.RateSourceOficial); !found || r.RateSell != sell {
			t.Errorf("%s oficial = %+v, want sell %v", cur, r, sell)
		}
	}
	if _, found := find(rates, "BRL", domain.RateSourceBlue); found {
		t.Error("a non official real was stored")
	}
	if _, found := find(rates, "ARS", domain.RateSourceOficial); found {
		t.Error("pesos were stored as a rate")
	}
	usdt, found := find(rates, "USDT", domain.RateSourceCripto)
	if !found || usdt.RateSell != 1666 || usdt.RateBuy != 1661.05 {
		t.Errorf("USDT = %+v, want the crypto dollar", usdt)
	}
	if _, found := find(rates, "USDT", domain.RateSourceBolsa); found {
		t.Error("USDT stored at a dollar other than the crypto one")
	}
}

// coinGecko serves DolarAPI and CoinGecko; hits counts CoinGecko calls and
// key records the header of the last one.
type coinGecko struct {
	status int
	body   string
	hits   atomic.Int32
	key    atomic.Value
}

func (c *coinGecko) fetcher(t *testing.T, apiKey string) *Fetcher {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/dolares":
			_, _ = w.Write([]byte(dolaresJSON))
		case "/simple/price":
			c.hits.Add(1)
			c.key.Store(r.Header.Get("x-cg-demo-api-key"))
			if q := r.URL.Query(); q.Get("ids") != "bitcoin,ethereum" || q.Get("vs_currencies") != "ars" {
				t.Errorf("CoinGecko query = %s", r.URL.RawQuery)
			}
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(c.body))
		default:
			w.WriteHeader(http.StatusBadGateway)
		}
	}))
	t.Cleanup(srv.Close)
	f := NewFetcher(srv.Client()).WithCoinGecko(apiKey)
	f.dolarAPIURL = srv.URL
	f.bluelyticsURL = srv.URL + "/bluelytics"
	f.coinGeckoURL = srv.URL
	return f
}

func TestFetchCoinGecko(t *testing.T) {
	t.Parallel()

	t.Run("without a key it is never asked", func(t *testing.T) {
		cg := &coinGecko{status: http.StatusOK, body: coinGeckoJSON}
		rates, _ := cg.fetcher(t, "").Fetch(context.Background())
		if cg.hits.Load() != 0 {
			t.Errorf("CoinGecko asked %d times without a key", cg.hits.Load())
		}
		if _, found := find(rates, "BTC", domain.RateSourceCripto); found {
			t.Error("bitcoin without a key")
		}
	})

	t.Run("bitcoin and ether in pesos, at most once per hour", func(t *testing.T) {
		cg := &coinGecko{status: http.StatusOK, body: coinGeckoJSON}
		f := cg.fetcher(t, "demo-key")
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		f.now = func() time.Time { return now }

		rates, err := f.Fetch(context.Background())
		if err == nil {
			t.Fatal("Bluelytics is down in this server: its error should come back to log")
		}
		if got := cg.key.Load(); got != "demo-key" {
			t.Errorf("key header = %v", got)
		}
		btc, found := find(rates, "BTC", domain.RateSourceCripto)
		if !found || btc.RateSell != 113583538 || btc.RateAvg != 113583538 ||
			!btc.LastUpdate.Equal(time.Unix(1794362796, 0)) {
			t.Errorf("BTC = %+v", btc)
		}
		if eth, found := find(rates, "ETH", domain.RateSourceCripto); !found || eth.RateSell != 3171144.5 {
			t.Errorf("ETH = %+v", eth)
		}

		now = now.Add(30 * time.Minute)
		rates, _ = f.Fetch(context.Background())
		if cg.hits.Load() != 1 {
			t.Errorf("CoinGecko asked %d times in 30 minutes", cg.hits.Load())
		}
		if _, found := find(rates, "BTC", domain.RateSourceCripto); found {
			t.Error("bitcoin came back without asking CoinGecko")
		}
		if _, found := find(rates, "USD", domain.RateSourceBolsa); !found {
			t.Error("the dollars stopped while CoinGecko rests")
		}

		now = now.Add(cryptoEvery)
		_, _ = f.Fetch(context.Background())
		if cg.hits.Load() != 2 {
			t.Errorf("CoinGecko asked %d times after an hour, want 2", cg.hits.Load())
		}
	})

	t.Run("CoinGecko down keeps the dollars and retries on the next run", func(t *testing.T) {
		cg := &coinGecko{status: http.StatusTooManyRequests, body: `{"status":{"error_code":429}}`}
		f := cg.fetcher(t, "demo-key")
		rates, err := f.Fetch(context.Background())
		if err == nil {
			t.Error("CoinGecko's error was swallowed")
		}
		if _, found := find(rates, "USD", domain.RateSourceBolsa); !found {
			t.Error("a CoinGecko failure dropped the dollars")
		}
		_, _ = f.Fetch(context.Background())
		if cg.hits.Load() != 2 {
			t.Errorf("CoinGecko asked %d times, want a retry after the failure", cg.hits.Load())
		}
	})

	t.Run("an answer without prices is an error", func(t *testing.T) {
		cg := &coinGecko{status: http.StatusOK, body: `{"bitcoin":{"ars":0}}`}
		rates, err := cg.fetcher(t, "demo-key").Fetch(context.Background())
		if err == nil {
			t.Error("no error for an empty answer")
		}
		if _, found := find(rates, "BTC", domain.RateSourceCripto); found {
			t.Error("a zero bitcoin was kept")
		}
	})
}

func TestViewRate(t *testing.T) {
	s := warmService(
		usd(domain.RateSourceOficial, 1000),
		usd(domain.RateSourceBolsa, 1200),
		usd(domain.RateSourceCripto, 1300),
		domain.ExchangeRate{Currency: "EUR", Source: domain.RateSourceOficial, RateSell: 1100},
		domain.ExchangeRate{Currency: "BRL", Source: domain.RateSourceOficial, RateSell: 200},
		domain.ExchangeRate{Currency: "CLP", Source: domain.RateSourceOficial, RateSell: 1.5709},
		domain.ExchangeRate{Currency: "USDT", Source: domain.RateSourceCripto, RateSell: 1300},
		domain.ExchangeRate{Currency: "BTC", Source: domain.RateSourceCripto, RateSell: 113583538},
	)
	ctx := context.Background()
	tests := []struct {
		currency string
		source   domain.RateSource
		want     string
	}{
		{"USD", domain.RateSourceBolsa, "1200"},
		{"EUR", domain.RateSourceOficial, "1100"},
		// No MEP real: the official one moved by MEP / official.
		{"BRL", domain.RateSourceBolsa, "240"},
		{"CLP", domain.RateSourceOficial, "1.5709"},
		{"CLP", domain.RateSourceBolsa, "1.8851"},
		// A crypto has one market price, whatever the household's dollar.
		{"USDT", domain.RateSourceBolsa, "1300"},
		{"BTC", domain.RateSourceOficial, "113583538"},
	}
	for _, tt := range tests {
		got, err := s.ViewRate(ctx, tt.currency, tt.source)
		if err != nil {
			t.Errorf("%s %s: %v", tt.currency, tt.source, err)
			continue
		}
		if !got.Equal(decimal.RequireFromString(tt.want)) {
			t.Errorf("%s %s = %s, want %s", tt.currency, tt.source, got, tt.want)
		}
	}

	if _, err := s.ViewRate(ctx, "ETH", domain.RateSourceBolsa); !errors.Is(err, domain.ErrNotFound) {
		t.Errorf("ether without a quote: err = %v, want not found", err)
	}
	if _, err := s.ViewRate(ctx, "ARS", domain.RateSourceBolsa); !isValidation(err) {
		t.Errorf("pesos as a view currency: err = %v", err)
	}
	if _, err := s.ViewRate(ctx, "EUR", "nope"); !isValidation(err) {
		t.Errorf("unknown source: err = %v", err)
	}
}

func isValidation(err error) bool {
	var ve *domain.ValidationError
	return errors.As(err, &ve)
}
