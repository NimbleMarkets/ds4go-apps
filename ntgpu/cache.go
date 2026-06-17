package ntgpu

import (
	"crypto/sha256"
	"encoding/hex"
	"sync"
)

// HashSource returns a stable hash suitable for generated shader/cache keys.
func HashSource(src string) string {
	sum := sha256.Sum256([]byte(src))
	return hex.EncodeToString(sum[:])
}

type cacheEntry[T any] struct {
	hash string
	val  T
}

// PipelineCache stores GPU resources keyed by logical object name and source
// hash. It is generic so callers can cache package-specific pipeline structs
// while reusing the same safe replacement/invalidation behavior.
type PipelineCache[T any] struct {
	mu      sync.Mutex
	items   map[string]cacheEntry[T]
	exec    *Executor
	release func(T) error

	misses int
}

// NewPipelineCache creates a cache. If exec is non-nil, Clear and Invalidate
// release resources on that executor; otherwise they call release synchronously.
func NewPipelineCache[T any](exec *Executor, release func(T) error) *PipelineCache[T] {
	return &PipelineCache[T]{
		items:   make(map[string]cacheEntry[T]),
		exec:    exec,
		release: release,
	}
}

// Lookup returns the cached value for key when its hash matches. A miss records
// one compile/build attempt for test and diagnostic counters.
func (c *PipelineCache[T]) Lookup(key, hash string) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if ent, ok := c.items[key]; ok && ent.hash == hash {
		return ent.val, true
	}
	c.misses++
	var zero T
	return zero, false
}

// Store publishes val for key/hash and returns any displaced old value.
func (c *PipelineCache[T]) Store(key, hash string, val T) (T, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var old T
	var hadOld bool
	if ent, ok := c.items[key]; ok {
		old = ent.val
		hadOld = true
	}
	c.items[key] = cacheEntry[T]{hash: hash, val: val}
	return old, hadOld
}

// Invalidate removes key and releases the removed value, if present.
func (c *PipelineCache[T]) Invalidate(key string) error {
	c.mu.Lock()
	ent, ok := c.items[key]
	if ok {
		delete(c.items, key)
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	return c.releaseAll([]T{ent.val})
}

// Clear removes and releases all cached values.
func (c *PipelineCache[T]) Clear() error {
	c.mu.Lock()
	old := make([]T, 0, len(c.items))
	for _, ent := range c.items {
		old = append(old, ent.val)
	}
	c.items = make(map[string]cacheEntry[T])
	c.mu.Unlock()
	return c.releaseAll(old)
}

func (c *PipelineCache[T]) releaseAll(vals []T) error {
	if len(vals) == 0 || c.release == nil {
		return nil
	}
	release := func() error {
		for _, v := range vals {
			if err := c.release(v); err != nil {
				return err
			}
		}
		return nil
	}
	if c.exec == nil {
		return release()
	}
	return c.exec.DoFunc(release)
}

// Len returns the number of cached entries.
func (c *PipelineCache[T]) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Misses returns the number of Lookup misses.
func (c *PipelineCache[T]) Misses() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.misses
}
