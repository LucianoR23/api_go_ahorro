package fxrates

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/LucianoR23/valuo_api/internal/domain"
)

// Upstreams (plan 5.1, 5.6):
//   - DolarAPI, the main source: /v1/dolares has every dollar "casa"
//     (oficial, blue, bolsa = MEP, contadoconliqui, tarjeta, cripto,
//     mayorista); /v1/cotizaciones has the official euro, real, Chilean and
//     Uruguayan peso. Its crypto dollar is also stored as USDT.
//   - Bluelytics, the fallback: blue and official dollar and euro. It is the
//     only source of the blue euro, and covers the rest when DolarAPI fails.
//   - CoinGecko: bitcoin and ether in pesos. Needs a (free) demo key; without
//     one it is skipped. Asked at most once per cryptoEvery.
const (
	dolarAPIURL   = "https://dolarapi.com"
	bluelyticsURL = "https://api.bluelytics.com.ar/v2/latest"
	coinGeckoURL  = "https://api.coingecko.com/api/v3"
	cryptoEvery   = 55 * time.Minute
)

// Fetcher es un adapter http. Se inyecta un *http.Client para poder
// setear timeout desde main (y reemplazar en tests).
type Fetcher struct {
	client        *http.Client
	dolarAPIURL   string
	bluelyticsURL string
	coinGeckoURL  string
	coinGeckoKey  string
	now           func() time.Time

	mu         sync.Mutex
	lastCrypto time.Time // last time CoinGecko answered
}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Fetcher{
		client: client, dolarAPIURL: dolarAPIURL, bluelyticsURL: bluelyticsURL,
		coinGeckoURL: coinGeckoURL, now: time.Now,
	}
}

// WithCoinGecko sets CoinGecko's demo key; empty leaves bitcoin and ether out.
func (f *Fetcher) WithCoinGecko(key string) *Fetcher {
	f.coinGeckoKey = key
	return f
}

// Fetch returns the latest rates of the dollar upstreams: DolarAPI's, plus
// the Bluelytics pairs DolarAPI did not have, plus CoinGecko's when it is
// due. It fails only when neither dollar upstream answered; otherwise the
// rates come with the other errors so the caller can log them.
func (f *Fetcher) Fetch(ctx context.Context) ([]domain.ExchangeRate, error) {
	main, errMain := f.fetchDolarAPI(ctx)
	backup, errBackup := f.fetchBluelytics(ctx)
	if errMain != nil && errBackup != nil {
		return nil, errors.Join(errMain, errBackup)
	}
	crypto, errCrypto := f.fetchCoinGecko(ctx)
	out := main
	have := make(map[cacheKey]bool, len(main))
	for _, r := range main {
		have[cacheKey{r.Currency, r.Source}] = true
	}
	for _, r := range backup {
		if !have[cacheKey{r.Currency, r.Source}] {
			out = append(out, r)
		}
	}
	out = append(out, crypto...)
	return out, errors.Join(errMain, errBackup, errCrypto)
}

// get decodes a JSON GET into dst. header, when set, goes with the request.
func (f *Fetcher) get(ctx context.Context, url string, dst any, header ...string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("fxrates.Fetch %s: build request: %w", url, err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return fmt.Errorf("fxrates.Fetch %s: do: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("fxrates.Fetch %s: status %d: %s", url, resp.StatusCode, string(body))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(dst); err != nil {
		return fmt.Errorf("fxrates.Fetch %s: decode: %w", url, err)
	}
	return nil
}

// dolarAPIRate: one entry of /v1/dolares or /v1/cotizaciones.
type dolarAPIRate struct {
	Moneda             string    `json:"moneda"`
	Casa               string    `json:"casa"`
	Compra             float64   `json:"compra"`
	Venta              float64   `json:"venta"`
	FechaActualizacion time.Time `json:"fechaActualizacion"`
}

func (f *Fetcher) fetchDolarAPI(ctx context.Context) ([]domain.ExchangeRate, error) {
	var dolares, cotizaciones []dolarAPIRate
	if err := f.get(ctx, f.dolarAPIURL+"/v1/dolares", &dolares); err != nil {
		return nil, err
	}
	// The euro is a nice-to-have: without it Bluelytics' official euro is used.
	_ = f.get(ctx, f.dolarAPIURL+"/v1/cotizaciones", &cotizaciones)

	var out []domain.ExchangeRate
	add := func(r dolarAPIRate) {
		src := domain.RateSource(r.Casa)
		if !src.Valid() {
			return
		}
		lu := r.FechaActualizacion
		if lu.IsZero() {
			lu = time.Now().UTC()
		}
		avg := (r.Compra + r.Venta) / 2
		if r.Compra <= 0 {
			avg = r.Venta
		}
		out = append(out, domain.ExchangeRate{
			Currency: r.Moneda, Source: src, LastUpdate: lu,
			RateAvg: avg, RateBuy: r.Compra, RateSell: r.Venta,
		})
	}
	for _, r := range dolares {
		if r.Moneda != "USD" {
			continue
		}
		add(r)
		// USDT trades at the crypto dollar: DolarAPI's "cripto" is its price.
		if r.Casa == string(domain.RateSourceCripto) {
			r.Moneda = "USDT"
			add(r)
		}
	}
	for _, r := range cotizaciones {
		if r.Casa != string(domain.RateSourceOficial) {
			continue
		}
		switch r.Moneda {
		case "EUR", "BRL", "CLP", "UYU":
			add(r)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fxrates.Fetch %s: no rates", f.dolarAPIURL)
	}
	return out, nil
}

// coinGeckoIDs: CoinGecko's id of each coin stored.
var coinGeckoIDs = map[string]string{"bitcoin": "BTC", "ethereum": "ETH"}

// fetchCoinGecko: bitcoin and ether in pesos, stored with source cripto. It
// returns nothing without a key, or when it answered less than cryptoEvery
// ago (the demo plan has a monthly cap).
func (f *Fetcher) fetchCoinGecko(ctx context.Context) ([]domain.ExchangeRate, error) {
	if f.coinGeckoKey == "" {
		return nil, nil
	}
	now := f.now()
	f.mu.Lock()
	due := f.lastCrypto.IsZero() || now.Sub(f.lastCrypto) >= cryptoEvery
	f.mu.Unlock()
	if !due {
		return nil, nil
	}
	var parsed map[string]struct {
		ARS       float64 `json:"ars"`
		UpdatedAt int64   `json:"last_updated_at"`
	}
	url := f.coinGeckoURL + "/simple/price?ids=bitcoin,ethereum&vs_currencies=ars&include_last_updated_at=true"
	if err := f.get(ctx, url, &parsed, "x-cg-demo-api-key", f.coinGeckoKey); err != nil {
		return nil, err
	}
	var out []domain.ExchangeRate
	for id, code := range coinGeckoIDs {
		p, ok := parsed[id]
		if !ok || p.ARS <= 0 {
			continue
		}
		lu := now.UTC()
		if p.UpdatedAt > 0 {
			lu = time.Unix(p.UpdatedAt, 0).UTC()
		}
		out = append(out, domain.ExchangeRate{
			Currency: code, Source: domain.RateSourceCripto, LastUpdate: lu,
			RateAvg: p.ARS, RateBuy: p.ARS, RateSell: p.ARS,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("fxrates.Fetch %s: no crypto prices", f.coinGeckoURL)
	}
	f.mu.Lock()
	f.lastCrypto = now
	f.mu.Unlock()
	return out, nil
}

type bluelyticsRate struct {
	ValueAvg  float64 `json:"value_avg"`
	ValueSell float64 `json:"value_sell"`
	ValueBuy  float64 `json:"value_buy"`
}

type bluelyticsResponse struct {
	Oficial     bluelyticsRate `json:"oficial"`
	Blue        bluelyticsRate `json:"blue"`
	OficialEuro bluelyticsRate `json:"oficial_euro"`
	BlueEuro    bluelyticsRate `json:"blue_euro"`
	LastUpdate  time.Time      `json:"last_update"`
}

// fetchBluelytics: USD and EUR, blue and official. They share the
// response's last_update.
func (f *Fetcher) fetchBluelytics(ctx context.Context) ([]domain.ExchangeRate, error) {
	var parsed bluelyticsResponse
	if err := f.get(ctx, f.bluelyticsURL, &parsed); err != nil {
		return nil, err
	}
	lu := parsed.LastUpdate
	if lu.IsZero() {
		lu = time.Now().UTC()
	}
	rate := func(cur string, src domain.RateSource, r bluelyticsRate) domain.ExchangeRate {
		return domain.ExchangeRate{Currency: cur, Source: src, LastUpdate: lu, RateAvg: r.ValueAvg, RateBuy: r.ValueBuy, RateSell: r.ValueSell}
	}
	return []domain.ExchangeRate{
		rate("USD", domain.RateSourceBlue, parsed.Blue),
		rate("USD", domain.RateSourceOficial, parsed.Oficial),
		rate("EUR", domain.RateSourceBlue, parsed.BlueEuro),
		rate("EUR", domain.RateSourceOficial, parsed.OficialEuro),
	}, nil
}
