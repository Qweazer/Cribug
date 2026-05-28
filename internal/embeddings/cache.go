package embeddings

import (
	"crypto/sha256"
	"fmt"
	"sync"
)

// lruEntry holds a key-value pair in the LRU cache.
type lruEntry struct {
	key   string
	value *EmbeddingResult
	prev  *lruEntry
	next  *lruEntry
}

// lruCache is a bounded thread-safe LRU cache.
// Cache key: SHA256(provider + ":" + model + ":" + text)
type lruCache struct {
	mu       sync.Mutex
	maxSize  int
	entries  map[string]*lruEntry
	head     *lruEntry // sentinel
	tail     *lruEntry // sentinel
}

// newLRUCache creates a new LRU cache with the given maximum size.
func newLRUCache(maxSize int) *lruCache {
	if maxSize <= 0 {
		maxSize = 1000
	}
	c := &lruCache{
		maxSize: maxSize,
		entries: make(map[string]*lruEntry),
		head:    &lruEntry{},
		tail:    &lruEntry{},
	}
	// Link sentinels
	c.head.next = c.tail
	c.tail.prev = c.head
	return c
}

// cacheKey generates a SHA256 hash of provider + ":" + model + ":" + text.
func cacheKey(provider, model, text string) string {
	h := sha256.Sum256([]byte(provider + ":" + model + ":" + text))
	return fmt.Sprintf("%x", h)
}

// Get retrieves a value from the cache, promoting the entry to MRU.
// Returns nil if not found.
func (c *lruCache) Get(key string) *EmbeddingResult {
	c.mu.Lock()
	defer c.mu.Unlock()

	entry, ok := c.entries[key]
	if !ok {
		return nil
	}
	// Move to front (MRU)
	c.moveToFront(entry)
	return entry.value
}

// Set adds or updates a value in the cache.
func (c *lruCache) Set(key string, value *EmbeddingResult) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// If key already exists, update it and move to front
	if entry, ok := c.entries[key]; ok {
		entry.value = value
		c.moveToFront(entry)
		return
	}

	// Evict if at capacity
	if len(c.entries) >= c.maxSize {
		c.evictLRU()
	}

	// Insert at front
	entry := &lruEntry{
		key:   key,
		value: value,
	}
	c.entries[key] = entry
	c.pushFront(entry)
}

// Len returns the number of entries in the cache.
func (c *lruCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// moveToFront moves an existing entry to the front (MRU position).
func (c *lruCache) moveToFront(entry *lruEntry) {
	c.removeNode(entry)
	c.pushFront(entry)
}

// pushFront inserts an entry at the front (MRU position), after head.
func (c *lruCache) pushFront(entry *lruEntry) {
	entry.next = c.head.next
	entry.prev = c.head
	c.head.next.prev = entry
	c.head.next = entry
}

// removeNode removes an entry from the linked list.
func (c *lruCache) removeNode(entry *lruEntry) {
	entry.prev.next = entry.next
	entry.next.prev = entry.prev
	entry.prev = nil
	entry.next = nil
}

// evictLRU removes the least recently used entry (just before tail sentinel).
func (c *lruCache) evictLRU() {
	if c.tail.prev == c.head {
		return // empty
	}
	lru := c.tail.prev
	c.removeNode(lru)
	delete(c.entries, lru.key)
}
