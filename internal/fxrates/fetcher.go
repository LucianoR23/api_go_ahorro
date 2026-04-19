package fxrates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// bluelyticsURL: endpoint público sin auth.
// Respuesta shape (resumido):
//   {
//     "oficial":         {"value_avg": 1050.0, "value_sell": 1060, "value_buy": 1040},
//     "blue":            {"value_avg": 1200.0, "value_sell": 1210, "value_buy": 1190},
//     "oficial_euro":    {...},
//     "blue_euro":       {...},
//     "last_update":     "2026-04-19T14:02:13.123Z"
//   }
const bluelyticsURL = "https://api.bluelytics.com.ar/v2/latest"

// Fetcher es un adapter http. Se inyecta un *http.Client para poder
// setear timeout desde main (y reemplazar en tests).
type Fetcher struct {
	client *http.Client
	url    string
}

func NewFetcher(client *http.Client) *Fetcher {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	return &Fetcher{client: client, url: bluelyticsURL}
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

// Fetch pega a bluelytics y devuelve 4 ExchangeRate: USD blue/oficial y EUR blue/oficial.
// Todas comparten el mismo last_update (es el de la respuesta, no por par).
func (f *Fetcher) Fetch(ctx context.Context) ([]domain.ExchangeRate, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, fmt.Errorf("fxrates.Fetch: build request: %w", err)
	}
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fxrates.Fetch: do: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("fxrates.Fetch: status %d: %s", resp.StatusCode, string(body))
	}

	var parsed bluelyticsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("fxrates.Fetch: decode: %w", err)
	}

	lu := parsed.LastUpdate
	if lu.IsZero() {
		lu = time.Now().UTC()
	}

	return []domain.ExchangeRate{
		{Currency: "USD", Source: domain.RateSourceBlue, LastUpdate: lu,
			RateAvg: parsed.Blue.ValueAvg, RateBuy: parsed.Blue.ValueBuy, RateSell: parsed.Blue.ValueSell},
		{Currency: "USD", Source: domain.RateSourceOficial, LastUpdate: lu,
			RateAvg: parsed.Oficial.ValueAvg, RateBuy: parsed.Oficial.ValueBuy, RateSell: parsed.Oficial.ValueSell},
		{Currency: "EUR", Source: domain.RateSourceBlue, LastUpdate: lu,
			RateAvg: parsed.BlueEuro.ValueAvg, RateBuy: parsed.BlueEuro.ValueBuy, RateSell: parsed.BlueEuro.ValueSell},
		{Currency: "EUR", Source: domain.RateSourceOficial, LastUpdate: lu,
			RateAvg: parsed.OficialEuro.ValueAvg, RateBuy: parsed.OficialEuro.ValueBuy, RateSell: parsed.OficialEuro.ValueSell},
	}, nil
}
