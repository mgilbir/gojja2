// Copyright 2026 The gojja2 Authors
// SPDX-License-Identifier: Apache-2.0

package gojja2

import "sync"

// defaultCacheSize is how many compiled templates an Environment keeps, and is
// jinja2's own default.
//
// The cache used to be an unbounded map with no eviction and no way to clear
// it, so every distinct name ever loaded was retained for the life of the
// process. With `{% include %}` over a name a template can influence, and a
// loader able to serve many, that grows without limit -- and it retains the
// constants folded into each tree along with it.
const defaultCacheSize = 400

// templateCache is a bounded LRU of compiled templates.
//
// Least-recently-used rather than plain eviction because template use is
// strongly skewed: a site has a handful of layouts touched on every request and
// a long tail touched rarely, and evicting the layouts would recompile them
// constantly.
type templateCache struct {
	mu    sync.Mutex
	limit int
	// entries maps a name to its node in the recency list.
	entries map[string]*cacheEntry
	// head is the most recently used, tail the least. A doubly-linked list
	// keeps both promotion and eviction O(1), which matters because every
	// GetTemplate promotes.
	head, tail *cacheEntry
}

type cacheEntry struct {
	name       string
	tmpl       *Template
	prev, next *cacheEntry
}

func newTemplateCache(limit int) *templateCache {
	return &templateCache{limit: limit, entries: make(map[string]*cacheEntry)}
}

// get returns a cached template and marks it most recently used.
func (c *templateCache) get(name string) (*Template, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[name]
	if !ok {
		return nil, false
	}
	c.detach(e)
	c.pushFront(e)
	return e.tmpl, true
}

// put stores a template, evicting the least recently used if that takes the
// cache past its limit.
func (c *templateCache) put(name string, tmpl *Template) {
	if c == nil || c.limit == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok {
		e.tmpl = tmpl
		c.detach(e)
		c.pushFront(e)
		return
	}
	e := &cacheEntry{name: name, tmpl: tmpl}
	c.entries[name] = e
	c.pushFront(e)
	for c.limit > 0 && len(c.entries) > c.limit {
		victim := c.tail
		if victim == nil {
			break
		}
		c.detach(victim)
		delete(c.entries, victim.name)
	}
}

// clear drops every cached template.
func (c *templateCache) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = make(map[string]*cacheEntry)
	c.head, c.tail = nil, nil
}

// forget drops one cached template.
func (c *templateCache) forget(name string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[name]; ok {
		c.detach(e)
		delete(c.entries, name)
	}
}

func (c *templateCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func (c *templateCache) detach(e *cacheEntry) {
	if e.prev != nil {
		e.prev.next = e.next
	} else if c.head == e {
		c.head = e.next
	}
	if e.next != nil {
		e.next.prev = e.prev
	} else if c.tail == e {
		c.tail = e.prev
	}
	e.prev, e.next = nil, nil
}

func (c *templateCache) pushFront(e *cacheEntry) {
	e.prev, e.next = nil, c.head
	if c.head != nil {
		c.head.prev = e
	}
	c.head = e
	if c.tail == nil {
		c.tail = e
	}
}
