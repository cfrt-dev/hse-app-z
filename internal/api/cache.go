package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Cache stores response bodies with their ETags so requests can be made
// conditional (like the official app) and so the UI can fall back to the
// last known data when offline or rate limited. It is best effort: all I/O
// errors are ignored. A nil *Cache is valid and caches nothing.
type Cache struct {
	dir string
	mu  sync.Mutex
	mem map[string]*cacheEntry
}

type cacheEntry struct {
	ETag   string    `json:"etag"`
	Body   []byte    `json:"body"`
	Stored time.Time `json:"stored"`
}

// NewCache returns a cache persisted under dir. An empty dir keeps the
// cache in memory only.
func NewCache(dir string) *Cache {
	if dir != "" {
		_ = os.MkdirAll(dir, 0o700)
	}
	return &Cache{dir: dir, mem: map[string]*cacheEntry{}}
}

func cacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (c *Cache) get(key string) *cacheEntry {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.mem[key]; ok {
		return e
	}
	if c.dir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(c.dir, key+".json"))
	if err != nil {
		return nil
	}
	var e cacheEntry
	if json.Unmarshal(b, &e) != nil || e.Body == nil {
		return nil
	}
	c.mem[key] = &e
	return &e
}

func (c *Cache) put(key, etag string, body []byte) {
	if c == nil {
		return
	}
	e := &cacheEntry{ETag: etag, Body: body, Stored: time.Now()}
	c.mu.Lock()
	c.mem[key] = e
	c.mu.Unlock()
	c.persist(key, e)
}

func (c *Cache) persist(key string, e *cacheEntry) {
	if c.dir == "" {
		return
	}
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(c.dir, "tmp-*")
	if err != nil {
		return
	}
	_, werr := tmp.Write(b)
	cerr := tmp.Close()
	if werr != nil || cerr != nil {
		os.Remove(tmp.Name())
		return
	}
	_ = os.Chmod(tmp.Name(), 0o600)
	if os.Rename(tmp.Name(), filepath.Join(c.dir, key+".json")) != nil {
		os.Remove(tmp.Name())
	}
}

// touch marks an entry as freshly validated (after a 304). Entries are
// replaced, never mutated: concurrent requests may still be reading the old
// one (see Client.do's fallback).
func (c *Cache) touch(key string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	e, ok := c.mem[key]
	if ok {
		e = &cacheEntry{ETag: e.ETag, Body: e.Body, Stored: time.Now()}
		c.mem[key] = e
	}
	c.mu.Unlock()
	if ok {
		c.persist(key, e)
	}
}

// Clear drops every cached response (memory and disk).
func (c *Cache) Clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mem = map[string]*cacheEntry{}
	if c.dir == "" {
		return
	}
	entries, _ := os.ReadDir(c.dir)
	for _, e := range entries {
		_ = os.Remove(filepath.Join(c.dir, e.Name()))
	}
}
