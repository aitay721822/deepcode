// Package cache speeds up repeated reads. Nothing stored here is the only
// copy of any data: losing it only costs recomputation.
package cache

import (
	"bytes"
	"container/list"
	"sync"
	"time"
)

type entry struct {
	key     string
	value   []byte
	expires time.Time
}

// Cache is an in-memory LRU bounded by total key+value bytes, with a TTL per entry.
// Values are copied in and out so callers can never mutate shared bytes.
type Cache struct {
	mu       sync.Mutex
	maxBytes int
	size     int
	ll       *list.List // front = most recently used
	items    map[string]*list.Element
	now      func() time.Time

	flightMu sync.Mutex
	flights  map[string]*flight // loads in progress, for GetOrLoad
}

func New(maxBytes int) *Cache {
	return &Cache{maxBytes: maxBytes, ll: list.New(), items: map[string]*list.Element{}, now: time.Now, flights: map[string]*flight{}}
}

func (c *Cache) get(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	e := el.Value.(*entry)
	if !c.now().Before(e.expires) {
		c.remove(el)
		return nil, false
	}
	c.ll.MoveToFront(el)
	return bytes.Clone(e.value), true
}

func (c *Cache) set(key string, value []byte, ttl time.Duration) {
	n := len(key) + len(value)
	if ttl <= 0 || n > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.remove(el)
	}
	c.items[key] = c.ll.PushFront(&entry{key: key, value: bytes.Clone(value), expires: c.now().Add(ttl)})
	c.size += n
	for c.size > c.maxBytes {
		c.remove(c.ll.Back())
	}
}

func (c *Cache) remove(el *list.Element) {
	e := c.ll.Remove(el).(*entry)
	delete(c.items, e.key)
	c.size -= len(e.key) + len(e.value)
}
