package fxrates

import (
	"sync"

	"github.com/LucianoR23/api_go_ahorra/internal/domain"
)

// cacheKey: combina moneda y fuente. "USD|blue", "EUR|oficial", etc.
type cacheKey struct {
	currency string
	source   domain.RateSource
}

// cache: snapshot en memoria de la última cotización por (currency, source).
// Se actualiza desde el worker y desde el bootstrap al arrancar.
// RWMutex porque el 99% del uso es lectura (Convert) y las escrituras
// son del worker cada 15 min.
type cache struct {
	mu    sync.RWMutex
	rates map[cacheKey]domain.ExchangeRate
}

func newCache() *cache {
	return &cache{rates: make(map[cacheKey]domain.ExchangeRate)}
}

func (c *cache) set(r domain.ExchangeRate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.rates[cacheKey{r.Currency, r.Source}] = r
}

func (c *cache) setMany(rs []domain.ExchangeRate) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range rs {
		c.rates[cacheKey{r.Currency, r.Source}] = r
	}
}

func (c *cache) get(currency string, source domain.RateSource) (domain.ExchangeRate, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	r, ok := c.rates[cacheKey{currency, source}]
	return r, ok
}

// snapshot copia el map actual. Lo usa el endpoint /current.
func (c *cache) snapshot() []domain.ExchangeRate {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]domain.ExchangeRate, 0, len(c.rates))
	for _, r := range c.rates {
		out = append(out, r)
	}
	return out
}
