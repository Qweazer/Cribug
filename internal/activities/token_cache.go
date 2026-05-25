package activities

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	redisclient "cribug/internal/redis"
)

// ── L1: LocalLRU ──────────────────────────────────────────────

type lruEntry struct {
	key      string
	count    int
	expireAt time.Time
}

// LocalLRU is a mutex-protected in-process LRU cache for token counts.
type LocalLRU struct {
	mu       sync.Mutex
	capacity int
	ttl      time.Duration
	items    map[string]*list.Element
	order    *list.List
}

func NewLocalLRU(capacity int, ttl time.Duration) *LocalLRU {
	if capacity <= 0 {
		capacity = 10000
	}
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &LocalLRU{
		capacity: capacity,
		ttl:      ttl,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

// Get returns (count, true) if the key exists and is not expired.
func (c *LocalLRU) Get(key string) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.items[key]
	if !ok {
		return 0, false
	}
	entry := elem.Value.(*lruEntry)
	if time.Now().After(entry.expireAt) {
		c.order.Remove(elem)
		delete(c.items, key)
		return 0, false
	}
	c.order.MoveToFront(elem)
	return entry.count, true
}

// Set stores a token count with TTL. Evicts LRU tail if at capacity.
func (c *LocalLRU) Set(key string, count int) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.items[key]; ok {
		entry := elem.Value.(*lruEntry)
		entry.count = count
		entry.expireAt = time.Now().Add(c.ttl)
		c.order.MoveToFront(elem)
		return
	}

	// Evict if at capacity
	for c.order.Len() >= c.capacity {
		tail := c.order.Back()
		if tail == nil {
			break
		}
		entry := tail.Value.(*lruEntry)
		delete(c.items, entry.key)
		c.order.Remove(tail)
	}

	entry := &lruEntry{key: key, count: count, expireAt: time.Now().Add(c.ttl)}
	elem := c.order.PushFront(entry)
	c.items[key] = elem
}

func (c *LocalLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// ── L2: RedisTokenCache ───────────────────────────────────────

// RedisTokenCache wraps Redis String/KV for shared L2 cache.
type RedisTokenCache struct {
	client *redisclient.Client
	ttl    time.Duration
}

func NewRedisTokenCache(client *redisclient.Client, ttl time.Duration) *RedisTokenCache {
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}
	return &RedisTokenCache{client: client, ttl: ttl}
}

func cacheKey(model, text string) string {
	h := sha256.New()
	h.Write([]byte(model + "|" + text))
	hash := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("lru:tiktoken:%s:%s", model, hash)
}

// Get returns (count, true) on Redis hit. Returns (0, false) on miss or error.
func (r *RedisTokenCache) Get(ctx context.Context, model, text string) (int, bool) {
	if r.client == nil {
		return 0, false
	}
	key := cacheKey(model, text)
	val, err := r.client.Get(ctx, key)
	if err != nil || val == "" {
		return 0, false
	}
	count, err := strconv.Atoi(val)
	if err != nil {
		return 0, false
	}
	return count, true
}

// Set stores a token count in Redis with TTL. Errors are logged, not returned.
func (r *RedisTokenCache) Set(ctx context.Context, model, text string, count int) {
	if r.client == nil {
		return
	}
	key := cacheKey(model, text)
	if err := r.client.Set(ctx, key, strconv.Itoa(count), r.ttl); err != nil {
		log.Printf("[WARN] RedisTokenCache.Set: key=%s err=%v", key, err)
	}
}

func (r *RedisTokenCache) KeyFor(model, text string) string {
	return cacheKey(model, text)
}

// ── TwoLevelTokenCache ────────────────────────────────────────

// TokenCacheResult holds the result of a cache lookup.
type TokenCacheResult struct {
	Count  int    `json:"count"`
	Cached bool   `json:"cached"`
	Source string `json:"source"` // local_lru, redis, python_service, fallback
}

// TwoLevelTokenCache combines LocalLRU + Redis L2 with stats.
type TwoLevelTokenCache struct {
	l1     *LocalLRU
	l2     *RedisTokenCache
	client *redisclient.Client
}

func NewTwoLevelTokenCache(client *redisclient.Client, l1Capacity int, l1TTL, l2TTL time.Duration) *TwoLevelTokenCache {
	return &TwoLevelTokenCache{
		l1:     NewLocalLRU(l1Capacity, l1TTL),
		l2:     NewRedisTokenCache(client, l2TTL),
		client: client,
	}
}

// Get follows: L1 → L2 → miss.
// L2 hit backfills L1.
func (c *TwoLevelTokenCache) Get(ctx context.Context, model, text string) TokenCacheResult {
	key := cacheKey(model, text)

	// 1. L1
	if count, ok := c.l1.Get(key); ok {
		c.incrStat(ctx, "local_hits")
		c.incrStat(ctx, "hits")
		return TokenCacheResult{Count: count, Cached: true, Source: "local_lru"}
	}

	// 2. L2
	if count, ok := c.l2.Get(ctx, model, text); ok {
		c.l1.Set(key, count) // backfill L1
		c.incrStat(ctx, "redis_hits")
		c.incrStat(ctx, "hits")
		return TokenCacheResult{Count: count, Cached: true, Source: "redis"}
	}

	// 3. Miss
	c.incrStat(ctx, "misses")
	return TokenCacheResult{Count: 0, Cached: false, Source: ""}
}

// Set writes count to L1 + L2.
func (c *TwoLevelTokenCache) Set(ctx context.Context, model, text string, count int) {
	key := cacheKey(model, text)
	c.l1.Set(key, count)
	c.l2.Set(ctx, model, text, count)
}

// RecordPythonCall increments python_calls stat.
func (c *TwoLevelTokenCache) RecordPythonCall(ctx context.Context) {
	c.incrStat(ctx, "python_calls")
}

// RecordFallback increments fallback_calls stat.
func (c *TwoLevelTokenCache) RecordFallback(ctx context.Context) {
	c.incrStat(ctx, "fallback_calls")
}

func (c *TwoLevelTokenCache) incrStat(ctx context.Context, field string) {
	if c.client == nil {
		return
	}
	if _, err := c.client.HIncrBy(ctx, "lru:stats", field, 1); err != nil {
		log.Printf("[WARN] TwoLevelTokenCache: incrStat %s: %v", field, err)
	}
}

// GetStats reads the lru:stats hash from Redis.
func (c *TwoLevelTokenCache) GetStats(ctx context.Context) map[string]string {
	if c.client == nil {
		return nil
	}
	stats, err := c.client.HGetAll(ctx, "lru:stats")
	if err != nil {
		log.Printf("[WARN] TwoLevelTokenCache: GetStats: %v", err)
		return nil
	}
	return stats
}

// L1Len returns the number of entries in L1.
func (c *TwoLevelTokenCache) L1Len() int {
	return c.l1.Len()
}
