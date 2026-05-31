// Package filestatecache provides an LRU cache of recently read file
// contents, inspired by claude-code's FileStateCache. It enables:
//
//   - Deduplication: avoid re-reading files already seen this session
//   - Staleness detection: reject writes when file changed since read
//   - Size-aware eviction: bounded memory use (default 25 MB)
package filestatecache

import (
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultMaxEntries = 100
	DefaultMaxBytes   = 25 * 1024 * 1024 // 25 MB
)

// FileState records a snapshot of a file at read time.
type FileState struct {
	Content       []byte
	Timestamp     time.Time
	Offset        int
	Limit         int
	IsPartialView bool
}

// Cache is a thread-safe LRU cache of recently read files. It tracks
// total bytes and evicts the least recently used entry when capacity
// is exceeded.
type Cache struct {
	mu       sync.Mutex
	entries  map[string]*cacheEntry // normalized path -> entry
	head     *cacheEntry            // most recently used
	tail     *cacheEntry            // least recently used
	maxEnt   int
	maxBytes int64
	totalB   int64
}

type cacheEntry struct {
	key   string
	state FileState
	size  int64
	prev  *cacheEntry
	next  *cacheEntry
}

// New creates a Cache with the given limits.
func New(maxEntries int, maxBytes int64) *Cache {
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBytes
	}
	return &Cache{
		entries:  make(map[string]*cacheEntry),
		maxEnt:   maxEntries,
		maxBytes: maxBytes,
	}
}

// normalize cleans a file path for use as a cache key.
func normalize(p string) string {
	return filepath.Clean(p)
}

// Get returns the cached state for path, or nil.
func (c *Cache) Get(path string) *FileState {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[normalize(path)]
	if !ok {
		return nil
	}
	c.moveToFront(e)
	// Return a copy so callers can't mutate the cache.
	fs := e.state
	return &fs
}

// Put stores a file's state in the cache.
func (c *Cache) Put(path string, state FileState) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := normalize(path)
	size := int64(len(state.Content))

	// If already present, update in place.
	if e, ok := c.entries[key]; ok {
		c.totalB -= e.size
		e.state = state
		e.size = size
		c.totalB += size
		c.moveToFront(e)
		c.evict()
		return
	}

	// Insert new entry.
	e := &cacheEntry{key: key, state: state, size: size}
	c.entries[key] = e
	c.totalB += size
	c.pushFront(e)
	c.evict()
}

// Invalidate removes a path from the cache.
func (c *Cache) Invalidate(path string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := normalize(path)
	e, ok := c.entries[key]
	if !ok {
		return
	}
	c.remove(e)
}

// Clear removes all entries.
func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	c.head = nil
	c.tail = nil
	c.totalB = 0
}

// Has reports whether path is in the cache.
func (c *Cache) Has(path string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.entries[normalize(path)]
	return ok
}

// IsStale reports whether the file at path has been modified since it
// was cached. A file not in the cache is not considered stale.
func (c *Cache) IsStale(path string) (bool, error) {
	c.mu.Lock()
	e, ok := c.entries[normalize(path)]
	c.mu.Unlock()

	if !ok {
		return false, nil
	}

	fi, err := os.Stat(path)
	if err != nil {
		return false, err
	}
	return fi.ModTime().After(e.state.Timestamp), nil
}

// Size returns the current count and total bytes.
func (c *Cache) Size() (entries int, bytes int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries), c.totalB
}

// Keys returns all cached paths.
func (c *Cache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	keys := make([]string, 0, len(c.entries))
	for k := range c.entries {
		keys = append(keys, k)
	}
	return keys
}

// moveToFront moves e to the head of the LRU list.
func (c *Cache) moveToFront(e *cacheEntry) {
	if c.head == e {
		return
	}
	c.remove(e)
	c.pushFront(e)
}

// pushFront inserts e at the head.
func (c *Cache) pushFront(e *cacheEntry) {
	e.prev = nil
	e.next = c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}

// remove unlinks e from the list and deletes from the map.
func (c *Cache) remove(e *cacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else {
		c.tail = e.prev
	}
	delete(c.entries, e.key)
	c.totalB -= e.size
}

// evict removes the LRU entry until limits are satisfied.
func (c *Cache) evict() {
	for c.tail != nil && (len(c.entries) > c.maxEnt || c.totalB > c.maxBytes) {
		c.remove(c.tail)
	}
}
