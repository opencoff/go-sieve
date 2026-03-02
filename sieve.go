// sieve.go - SIEVE - a simple and efficient cache
//
// (c) 2024 Sudhi Herle <sudhi@herle.net>
//
// Copyright 2024- Sudhi Herle <sw-at-herle-dot-net>
// License: BSD-2-Clause
//
// If you need a commercial license for this work, please contact
// the author.
//
// This software does not come with any express or implied
// warranty; it is provided "as is". No claim  is made to its
// suitability for any purpose.

// Package sieve implements the SIEVE cache eviction algorithm (NSDI'24, Zhang et al.).
// https://yazhuozhang.com/assets/pdf/nsdi24-sieve.pdf
//
// SIEVE uses a FIFO queue with a roving "hand" pointer. On cache hit, only a
// visited bit is set (lazy promotion). On miss, the hand scans toward the head,
// clearing visited bits until it finds an unvisited node to evict (quick demotion).
//
// This implementation is optimized for low GC overhead and high concurrency:
// an array-backed doubly-linked list with int32 indices (no interior pointers),
// a packed atomic bitfield for visited flags, and xsync.MapOf for lock-free reads.
package sieve

import (
	"fmt"
	"strings"
	"sync"

	"github.com/puzpuzpuz/xsync/v3"
)

const nullIdx = int32(-1)

// node contains the <key, val> tuple as a node in a linked list.
type node[K comparable, V any] struct {
	sync.Mutex
	key  K
	val  V
	next int32 // index into backing array, nullIdx = null
	prev int32
}

// allocator manages a fixed pool of pre-allocated nodes using bump allocation
// and an index-based freelist.
type allocator[K comparable, V any] struct {
	nodes []node[K, V] // the full backing array (never resliced)
	cur   int32        // bump allocator cursor
	next  int32        // head of freelist (nullIdx = empty)
}

// newAllocator creates a new allocator with capacity nodes
func newAllocator[K comparable, V any](capacity int) *allocator[K, V] {
	return &allocator[K, V]{
		nodes: make([]node[K, V], capacity),
		cur:   0,
		next:  nullIdx,
	}
}

// alloc retrieves a node index from the allocator.
// It first tries the freelist, then falls back to bump allocation.
// Returns nullIdx if no nodes are available.
func (a *allocator[K, V]) alloc() int32 {
	// Try freelist first
	if a.next != nullIdx {
		idx := a.next
		a.next = a.nodes[idx].next
		return idx
	}

	// Bump allocate
	if a.cur >= a.capacity() {
		return nullIdx
	}

	idx := a.cur
	a.cur++
	return idx
}

// free returns a node at idx to the freelist
func (a *allocator[K, V]) free(idx int32) {
	a.nodes[idx].next = a.next
	a.next = idx
}

// reset resets the allocator to its initial state
func (a *allocator[K, V]) reset() {
	a.cur = 0
	a.next = nullIdx
}

// capacity returns the total capacity as int32
func (a *allocator[K, V]) capacity() int32 {
	return int32(len(a.nodes))
}

// Sieve represents a cache mapping the key of type 'K' with
// a value of type 'V'. The type 'K' must implement the
// comparable trait. An instance of Sieve has a fixed max capacity;
// new additions to the cache beyond the capacity will cause cache
// eviction of other entries - as determined by the SIEVE algorithm.
type Sieve[K comparable, V any] struct {
	mu      sync.Mutex
	cache   *xsync.MapOf[K, int32]
	head    int32
	tail    int32
	hand    int32
	size    int
	visited atomicBitfield

	allocator *allocator[K, V]
}

// New creates a new cache of size 'capacity' mapping key 'K' to value 'V'
func New[K comparable, V any](capacity int) *Sieve[K, V] {
	s := &Sieve[K, V]{
		cache:     xsync.NewMapOf[K, int32](),
		head:      nullIdx,
		tail:      nullIdx,
		hand:      nullIdx,
		visited:   newAtomicBitfield(capacity),
		allocator: newAllocator[K, V](capacity),
	}
	return s
}

// Get fetches the value for a given key in the cache.
// It returns true if the key is in the cache, false otherwise.
// The zero value for 'V' is returned when key is not in the cache.
func (s *Sieve[K, V]) Get(key K) (V, bool) {
	if idx, ok := s.cache.Load(key); ok {
		s.visited.Set(idx)
		return s.allocator.nodes[idx].val, true
	}

	var x V
	return x, false
}

// Add adds a new element to the cache or overwrite one if it exists
// Return true if we replaced, false otherwise
func (s *Sieve[K, V]) Add(key K, val V) bool {
	nodes := s.allocator.nodes

	// Fast path: key exists, just update
	if idx, ok := s.cache.Load(key); ok {
		n := &nodes[idx]
		n.Lock()
		n.val = val
		n.Unlock()
		s.visited.Set(idx)
		return true
	}

	s.mu.Lock()
	// Re-check under lock to prevent double-insert (TOCTOU fix)
	if idx, ok := s.cache.Load(key); ok {
		n := &nodes[idx]
		n.Lock()
		n.val = val
		n.Unlock()
		s.visited.Set(idx)
		s.mu.Unlock()
		return true
	}
	s.add(key, val)
	s.mu.Unlock()
	return false
}

// Probe adds <key, val> if not present in the cache.
// Returns:
//
//	<cached-val, true> when key is present in the cache
//	<val, false> when key is not present in the cache
func (s *Sieve[K, V]) Probe(key K, val V) (V, bool) {
	nodes := s.allocator.nodes

	// Fast path: key exists
	if idx, ok := s.cache.Load(key); ok {
		s.visited.Set(idx)
		return nodes[idx].val, true
	}

	s.mu.Lock()
	// Re-check under lock to prevent double-insert (TOCTOU fix)
	if idx, ok := s.cache.Load(key); ok {
		s.visited.Set(idx)
		s.mu.Unlock()
		return nodes[idx].val, true
	}
	s.add(key, val)
	s.mu.Unlock()
	return val, false
}

// Delete deletes the named key from the cache
// It returns true if the item was in the cache and false otherwise
func (s *Sieve[K, V]) Delete(key K) bool {
	s.mu.Lock()
	if idx, ok := s.cache.LoadAndDelete(key); ok {
		s.remove(idx)
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	return false
}

// Purge resets the cache
func (s *Sieve[K, V]) Purge() {
	s.mu.Lock()
	s.cache = xsync.NewMapOf[K, int32]()
	s.head = nullIdx
	s.tail = nullIdx
	s.hand = nullIdx

	// Reset the allocator and visited bits
	s.allocator.reset()
	s.visited.Reset()
	s.size = 0
	s.mu.Unlock()
}

// Len returns the current cache utilization
func (s *Sieve[K, V]) Len() int {
	return s.size
}

// Cap returns the max cache capacity
func (s *Sieve[K, V]) Cap() int {
	return int(s.allocator.capacity())
}

// String returns a string description of the sieve cache
func (s *Sieve[K, V]) String() string {
	s.mu.Lock()
	m := s.desc()
	s.mu.Unlock()
	return m
}

// Dump dumps all the cache contents as a newline delimited
// string.
func (s *Sieve[K, V]) Dump() string {
	var b strings.Builder

	s.mu.Lock()
	b.WriteString(s.desc())
	b.WriteRune('\n')
	nodes := s.allocator.nodes
	for idx := s.head; idx != nullIdx; idx = nodes[idx].next {
		h := "  "
		if idx == s.hand {
			h = ">>"
		}
		n := &nodes[idx]
		b.WriteString(fmt.Sprintf("%svisited=%v, key=%v, val=%v\n", h, s.visited.Test(idx), n.key, n.val))
	}
	s.mu.Unlock()
	return b.String()
}

// -- internal methods --

// add a new tuple to the cache and evict as necessary
// caller must hold lock.
func (s *Sieve[K, V]) add(key K, val V) {
	// cache miss; we evict and find a new node
	if int32(s.size) == s.allocator.capacity() {
		s.evict()
	}

	idx := s.newNode(key, val)

	// Eviction is guaranteed to remove one node; so this should never happen.
	if idx == nullIdx {
		msg := fmt.Sprintf("%T: add <%v>: objpool empty after eviction", s, key)
		panic(msg)
	}

	s.cache.Store(key, idx)

	nodes := s.allocator.nodes

	// insert at the head of the list
	nodes[idx].next = s.head
	nodes[idx].prev = nullIdx
	if s.head != nullIdx {
		nodes[s.head].prev = idx
	}
	s.head = idx
	if s.tail == nullIdx {
		s.tail = idx
	}

	s.size += 1
}

// evict an item from the cache.
// NB: Caller must hold the lock
func (s *Sieve[K, V]) evict() {
	hand := s.hand
	if hand == nullIdx {
		hand = s.tail
	}

	nodes := s.allocator.nodes
	for hand != nullIdx {
		if !s.visited.Test(hand) {
			s.cache.Delete(nodes[hand].key)
			// Critical: save prev before remove() clobbers next for freelist
			prev := nodes[hand].prev
			s.remove(hand)
			s.hand = prev
			return
		}
		s.visited.Clear(hand)
		hand = nodes[hand].prev
		// wrap around and start again
		if hand == nullIdx {
			hand = s.tail
		}
	}
	s.hand = hand
}

// remove removes the node at idx from the linked list and frees it.
// Caller must hold lock.
func (s *Sieve[K, V]) remove(idx int32) {
	s.size -= 1

	nodes := s.allocator.nodes
	n := &nodes[idx]

	// remove node from list
	if n.prev != nullIdx {
		nodes[n.prev].next = n.next
	} else {
		s.head = n.next
	}
	if n.next != nullIdx {
		nodes[n.next].prev = n.prev
	} else {
		s.tail = n.prev
	}

	// Return the node to the allocator's freelist
	s.allocator.free(idx)
}

// newNode allocates a node and initializes it with key and val.
// Returns nullIdx if no nodes are available.
func (s *Sieve[K, V]) newNode(key K, val V) int32 {
	idx := s.allocator.alloc()
	if idx == nullIdx {
		return nullIdx
	}

	n := &s.allocator.nodes[idx]
	n.key = key
	n.val = val
	n.next = nullIdx
	n.prev = nullIdx
	s.visited.Clear(idx)

	return idx
}

// desc describes the properties of the sieve
func (s *Sieve[K, V]) desc() string {
	m := fmt.Sprintf("cache<%T>: size %d, cap %d, head=%d, tail=%d, hand=%d",
		s, s.size, int(s.allocator.capacity()), s.head, s.tail, s.hand)
	return m
}
