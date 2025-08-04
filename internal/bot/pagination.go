package bot

import (
	"sync"
	"time"

	lru "github.com/hashicorp/golang-lru/v2"
)

const (
	paginationCacheSize = 1000
	paginationTTL       = 5 * time.Minute
)

type PaginatedContent struct {
	Pages     [][]string
	Timestamp time.Time
}

type PaginationCache struct {
	cache *lru.Cache[string, PaginatedContent]
	mu    sync.RWMutex
}

func NewPaginationCache() *PaginationCache {
	cache, _ := lru.New[string, PaginatedContent](paginationCacheSize)
	return &PaginationCache{
		cache: cache,
	}
}

func (c *PaginationCache) Set(key string, pages [][]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache.Add(key, PaginatedContent{
		Pages:     pages,
		Timestamp: time.Now(),
	})
}

func (c *PaginationCache) Get(key string) ([][]string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if content, ok := c.cache.Get(key); ok {
		if time.Since(content.Timestamp) > paginationTTL {
			c.cache.Remove(key)
			return nil, false
		}
		return content.Pages, true
	}
	return nil, false
}