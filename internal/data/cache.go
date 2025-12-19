package data

import "sync"

type Cache struct {
	mu sync.RWMutex
	m  map[string]int64
}

func New() *Cache {
	return &Cache{m: make(map[string]int64)}
}

func (c *Cache) IsPresent(key string) bool {
	c.mu.RLock()
	_, ok := c.m[key]
	c.mu.RUnlock()
	return ok
}

func (c *Cache) Add(key string, ts int64) {
	c.mu.Lock()
	c.m[key] = ts
	c.mu.Unlock()
}

// Fill swaps the backing map in O(1).
// The caller must treat entries as owned by the cache after this call:
// do not write to it from other goroutines (or at all) without going through Cache.
func (c *Cache) Fill(entries map[string]int64) {
	if entries == nil {
		entries = make(map[string]int64)
	}

	c.mu.Lock()
	c.m = entries
	c.mu.Unlock()
}
