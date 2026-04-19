package domain

import (
	"time"
)

// RateSource identifica la fuente del tipo de cambio.
type RateSource string

const (
	RateSourceBlue     RateSource = "blue"
	RateSourceOficial  RateSource = "oficial"
)

// ExchangeRate: una cotización ARS por unidad de moneda extranjera.
// Ejemplo: currency=USD, source=blue, rate_avg=1200 → 1 USD = 1200 ARS.
type ExchangeRate struct {
	Currency   string    // USD o EUR
	Source     RateSource
	LastUpdate time.Time // reportado por el upstream (bluelytics)
	RateAvg    float64
	RateBuy    float64
	RateSell   float64
	FetchedAt  time.Time // cuándo lo escribimos en DB
}
