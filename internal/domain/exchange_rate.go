package domain

import (
	"time"
)

// RateSource identifica la fuente del tipo de cambio.
type RateSource string

// The DolarAPI "casas" (plan 5.1). Bolsa is the MEP dollar.
const (
	RateSourceOficial         RateSource = "oficial"
	RateSourceBlue            RateSource = "blue"
	RateSourceBolsa           RateSource = "bolsa"
	RateSourceContadoConLiqui RateSource = "contadoconliqui"
	RateSourceTarjeta         RateSource = "tarjeta"
	RateSourceCripto          RateSource = "cripto"
	RateSourceMayorista       RateSource = "mayorista"
)

// DefaultRateSource: what a new household converts with (MEP, decided with
// the user in plan 5.1).
const DefaultRateSource = RateSourceBolsa

// RateSources: every source, in the order the app lists them.
var RateSources = []RateSource{
	RateSourceBolsa, RateSourceOficial, RateSourceBlue, RateSourceTarjeta,
	RateSourceContadoConLiqui, RateSourceCripto, RateSourceMayorista,
}

// Valid reports whether s is a known source.
func (s RateSource) Valid() bool {
	for _, x := range RateSources {
		if s == x {
			return true
		}
	}
	return false
}

// ExchangeRate: una cotización ARS por unidad de moneda extranjera.
// Ejemplo: currency=USD, source=blue, rate_avg=1200 → 1 USD = 1200 ARS.
type ExchangeRate struct {
	Currency   string // USD, EUR, or a ViewCurrencies code (plan 5.6)
	Source     RateSource
	LastUpdate time.Time // reportado por el upstream (bluelytics)
	RateAvg    float64
	RateBuy    float64
	RateSell   float64
	FetchedAt  time.Time // last time an upstream returned it (plan 5.2)
}

// PickRateSource: the source a movement converts with: the one picked for
// it, or the household's (plan 5.1). An unknown pick is a validation error.
func PickRateSource(picked, household RateSource) (RateSource, error) {
	if picked == "" {
		picked = household
	}
	if picked == "" {
		picked = DefaultRateSource
	}
	if !picked.Valid() {
		return "", NewValidationError("rateSource", "cotización desconocida")
	}
	return picked, nil
}

// RateSourcePtr turns a nullable column into a *RateSource.
func RateSourcePtr(s string, valid bool) *RateSource {
	if !valid {
		return nil
	}
	v := RateSource(s)
	return &v
}

// RateStaleAfter: a rate no upstream confirmed for this long is stale and
// the app warns before using it (plan 5.2).
const RateStaleAfter = 24 * time.Hour

// Stale reports whether no upstream confirmed r in RateStaleAfter. A rate
// that is still the latest quote (the official dollar on a weekend) is not
// stale while an upstream keeps returning it.
func (r ExchangeRate) Stale(now time.Time) bool {
	return r.FetchedAt.IsZero() || now.Sub(r.FetchedAt) > RateStaleAfter
}

// ViewCurrency: a currency reports can be shown in besides pesos (plan 5.6).
// View only: movements still take ARS, USD or EUR.
type ViewCurrency struct {
	Code string
	// Scale: decimals an amount in it keeps.
	Scale int32
	// Crypto: priced in the crypto market (source cripto), not converted
	// through the household's dollar.
	Crypto bool
}

// ViewCurrencies, in the order the app lists them.
var ViewCurrencies = []ViewCurrency{
	{Code: "USD", Scale: 2},
	{Code: "EUR", Scale: 2},
	{Code: "BRL", Scale: 2},
	{Code: "CLP", Scale: 2},
	{Code: "UYU", Scale: 2},
	{Code: "USDT", Scale: 2, Crypto: true},
	{Code: "BTC", Scale: 8, Crypto: true},
	{Code: "ETH", Scale: 8, Crypto: true},
}

// LookupViewCurrency finds code in ViewCurrencies.
func LookupViewCurrency(code string) (ViewCurrency, bool) {
	for _, c := range ViewCurrencies {
		if c.Code == code {
			return c, true
		}
	}
	return ViewCurrency{}, false
}
