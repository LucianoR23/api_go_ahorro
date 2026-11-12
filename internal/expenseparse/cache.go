package expenseparse

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// The AI answers for identical sentences are kept for 24 hours (plan §8.3),
// in memory only: no table holds what people typed or what it meant
// (decided with the user). A deploy empties it; at worst a sentence is paid
// again.
const (
	cacheTTL  = 24 * time.Hour
	cacheSize = 5000
)

// cacheKey is per person and per day, since "ayer" changes with the date.
func cacheKey(userID uuid.UUID, day, text string) string {
	norm := strings.Join(strings.Fields(strings.ToLower(text)), " ")
	sum := sha256.Sum256([]byte(userID.String() + "|" + day + "|" + norm))
	return hex.EncodeToString(sum[:])
}

type cacheEntry struct {
	a       answer
	expires time.Time
}

type cache struct {
	mu   sync.Mutex
	ttl  time.Duration
	size int
	now  func() time.Time
	m    map[string]cacheEntry
}

func newCache(ttl time.Duration, size int, now func() time.Time) *cache {
	return &cache{ttl: ttl, size: size, now: now, m: map[string]cacheEntry{}}
}

func (c *cache) get(key string) (answer, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[key]
	if !ok {
		return answer{}, false
	}
	if !c.now().Before(e.expires) {
		delete(c.m, key)
		return answer{}, false
	}
	return e.a, true
}

// put drops expired entries when full, and then the one closest to
// expiring.
func (c *cache) put(key string, a answer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if _, ok := c.m[key]; !ok && len(c.m) >= c.size {
		var oldest string
		var oldestAt time.Time
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
				continue
			}
			if oldest == "" || e.expires.Before(oldestAt) {
				oldest, oldestAt = k, e.expires
			}
		}
		if len(c.m) >= c.size {
			delete(c.m, oldest)
		}
	}
	c.m[key] = cacheEntry{a: a, expires: now.Add(c.ttl)}
}
