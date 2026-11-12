// Package cache is for non-sensitive aggregate/public data, never identity or permissions.
package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type entry struct {
	Value   []byte    `json:"value"`
	Expires time.Time `json:"expires"`
}
type Stats struct {
	Hits    uint64 `json:"hits"`
	Misses  uint64 `json:"misses"`
	Entries int    `json:"entries"`
	Bytes   int    `json:"bytes"`
}
type pending struct {
	done chan struct{}
}
type Cache struct {
	mu                          sync.Mutex
	items                       map[string]entry
	maxEntries, maxBytes, bytes int
	dir                         string
	hits, misses                atomic.Uint64
	pending                     map[string]*pending
	generation                  uint64
}

func New(maxEntries, maxBytes int, dir string) (*Cache, error) {
	if maxEntries < 1 || maxBytes < 1 {
		return nil, errors.New("invalid cache budget")
	}
	if dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, err
		}
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("cache directory must be private")
		}
	}
	return &Cache{items: map[string]entry{}, pending: map[string]*pending{}, maxEntries: maxEntries, maxBytes: maxBytes, dir: dir}, nil
}
func (c *Cache) lock() (func(), error) {
	if c.dir == "" {
		return func() {}, nil
	}
	f, e := os.OpenFile(filepath.Join(c.dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		f.Close()
		return nil, e
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}
func (c *Cache) Get(ctx context.Context, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) ([]byte, error) {
	if ttl <= 0 {
		return load(ctx)
	}
	if c.dir == "" {
		return c.getMemory(ctx, key, ttl, load)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	unlock, e := c.lock()
	if e != nil {
		return nil, e
	}
	defer unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Disk is authoritative when enabled; this also invalidates other processes' L2.
	item, ok := c.items[key]
	sum := sha256.Sum256([]byte(key))
	file := hex.EncodeToString(sum[:]) + ".json"
	if c.dir != "" {
		f, e := os.Open(filepath.Join(c.dir, file))
		var raw []byte
		if e == nil {
			raw, e = io.ReadAll(io.LimitReader(f, int64(c.maxBytes*2+1)))
			f.Close()
		}
		ok = e == nil && len(raw) <= c.maxBytes*2 && json.Unmarshal(raw, &item) == nil
	}
	if ok && time.Now().Before(item.Expires) {
		c.hits.Add(1)
		return append([]byte(nil), item.Value...), nil
	}
	c.misses.Add(1)
	value, e := load(ctx)
	if e != nil {
		return nil, e
	}
	if len(value) > c.maxBytes {
		return value, nil
	}
	item = entry{append([]byte(nil), value...), time.Now().Add(ttl)}
	if len(c.items) >= c.maxEntries || c.bytes+len(value) > c.maxBytes {
		c.items = map[string]entry{}
		c.bytes = 0
	}
	if prior, ok := c.items[key]; ok {
		c.bytes -= len(prior.Value)
	}
	c.items[key] = item
	c.bytes += len(value)
	if c.dir != "" {
		files, _ := filepath.Glob(filepath.Join(c.dir, "*.json"))
		var total int64
		for _, p := range files {
			if i, e := os.Stat(p); e == nil {
				total += i.Size()
			}
		}
		if len(files) >= c.maxEntries || total+int64(len(value)*2) > int64(c.maxBytes*2) {
			for _, p := range files {
				if e = os.Remove(p); e != nil {
					return nil, e
				}
			}
		}
		raw, _ := json.Marshal(item)
		tmp, e := os.CreateTemp(c.dir, ".cache-")
		if e != nil {
			return nil, e
		}
		name := tmp.Name()
		defer os.Remove(name)
		if _, e = tmp.Write(raw); e == nil {
			e = tmp.Close()
		} else {
			tmp.Close()
		}
		if e == nil {
			e = os.Rename(name, filepath.Join(c.dir, file))
		}
		if e != nil {
			return nil, e
		}
	}
	return append([]byte(nil), value...), nil
}

// Coalesce one key's misses without holding the cache mutex during its loader.
func (c *Cache) getMemory(ctx context.Context, key string, ttl time.Duration, load func(context.Context) ([]byte, error)) ([]byte, error) {
	for {
		c.mu.Lock()
		if err := ctx.Err(); err != nil {
			c.mu.Unlock()
			return nil, err
		}
		if item, ok := c.items[key]; ok && time.Now().Before(item.Expires) {
			c.hits.Add(1)
			c.mu.Unlock()
			return append([]byte(nil), item.Value...), nil
		}
		if call, ok := c.pending[key]; ok {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-call.done:
				continue
			}
		}
		call := &pending{done: make(chan struct{})}
		c.pending[key] = call
		generation := c.generation
		c.misses.Add(1)
		c.mu.Unlock()
		var value []byte
		var err error
		func() {
			// Wake waiters even if the loader panics; each retains its own context.
			defer func() {
				c.mu.Lock()
				if c.pending[key] == call {
					delete(c.pending, key)
				}
				close(call.done)
				c.mu.Unlock()
			}()
			value, err = load(ctx)
			c.mu.Lock()
			defer c.mu.Unlock()
			if err == nil && len(value) <= c.maxBytes && generation == c.generation {
				if prior, ok := c.items[key]; ok {
					c.bytes -= len(prior.Value)
					delete(c.items, key)
				}
				if len(c.items) >= c.maxEntries || c.bytes+len(value) > c.maxBytes {
					c.items = map[string]entry{}
					c.bytes = 0
				}
				c.items[key] = entry{append([]byte(nil), value...), time.Now().Add(ttl)}
				c.bytes += len(value)
			}
		}()
		return append([]byte(nil), value...), err
	}
}

func (c *Cache) Clear() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	unlock, e := c.lock()
	if e != nil {
		return e
	}
	defer unlock()
	c.items = map[string]entry{}
	c.bytes = 0
	c.pending = map[string]*pending{}
	c.generation++
	if c.dir != "" {
		files, _ := filepath.Glob(filepath.Join(c.dir, "*.json"))
		for _, p := range files {
			if e = os.Remove(p); e != nil {
				return e
			}
		}
	}
	return nil
}
func (c *Cache) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return Stats{c.hits.Load(), c.misses.Load(), len(c.items), c.bytes}
}

type requestKey struct{}

func Request(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestKey{}, &sync.Map{})
}

type requestEntry struct {
	once  sync.Once
	value []byte
	err   error
}

func RequestGet(ctx context.Context, key string, load func() ([]byte, error)) ([]byte, error) {
	m, ok := ctx.Value(requestKey{}).(*sync.Map)
	if !ok {
		return load()
	}
	value, _ := m.LoadOrStore(key, &requestEntry{})
	slot := value.(*requestEntry)
	slot.once.Do(func() { slot.value, slot.err = load(); slot.value = append([]byte(nil), slot.value...) })
	return append([]byte(nil), slot.value...), slot.err
}
