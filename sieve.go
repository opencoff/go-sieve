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
// a combined per-node lock+visited word (one uint64 per node), and xsync.MapOf
// for lock-free reads.
package sieve

import (
	"fmt"
	"strings"
	"sync"

	"github.com/puzpuzpuz/xsync/v3"
)

const (
	nullIdx     = int32(-1)
	sentinelIdx = int32(0) // index 0 is always the sentinel node
)

// node contains the <key, val> tuple as a node in a linked list.
// Synchronization is external: the per-node slotState word protects
// val reads/writes via its embedded spinlock.
type node[K comparable, V any] struct {
	key  K
	val  V
	next int32 // index into backing array
	prev int32
}

// allocator manages a fixed pool of pre-allocated nodes using bump allocation
// and an index-based freelist. Index 0 is reserved for the sentinel node and
// is never allocated or freed.
type allocator[K comparable, V any] struct {
	nodes []node[K, V] // the full backing array (never resliced), index 0 = sentinel
	cur   int32        // bump allocator cursor (starts at 1, skipping sentinel)
	next  int32        // head of freelist (nullIdx = empty)
	cap   int32        // user-requested capacity (excludes sentinel)
}

// initAllocator initializes an allocator with capacity usable nodes.
// Allocates capacity+1 slots (index 0 is the sentinel).
func initAllocator[K comparable, V any](a *allocator[K, V], capacity int) {
	a.nodes = make([]node[K, V], capacity+1) // +1 for sentinel
	a.cur = 1                                // skip sentinel at index 0
	a.next = nullIdx
	a.cap = int32(capacity) // #nosec G115 — capacity is user-provided positive int, bounded well below int32 max
	// Initialize sentinel: circular self-links
	a.nodes[sentinelIdx].next = sentinelIdx
	a.nodes[sentinelIdx].prev = sentinelIdx
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

	// Bump allocate (total array length = cap + 1 for sentinel)
	if a.cur > a.cap {
		return nullIdx
	}

	idx := a.cur
	a.cur++
	return idx
}

// free returns a node at idx to the freelist.
// Caller must have already zeroed key/val (done in remove() under slot lock).
func (a *allocator[K, V]) free(idx int32) {
	a.nodes[idx].next = a.next
	a.next = idx
}

// reset resets the allocator to its initial state and re-initializes
// the sentinel's circular self-links.
//
// Note: key/val fields are NOT zeroed here to avoid racing with concurrent
// Get() calls that may hold a stale index. Instead, newNode() overwrites
// key/val under the slot lock, and remove() zeroes them under the slot lock.
// After Purge, stale key/val references are retained until slots are reused;
// this is an acceptable GC trade-off for a rare operation.
func (a *allocator[K, V]) reset() {
	a.cur = 1 // skip sentinel
	a.next = nullIdx
	a.nodes[sentinelIdx].next = sentinelIdx
	a.nodes[sentinelIdx].prev = sentinelIdx
}

// capacity returns the user-visible capacity (excludes sentinel)
func (a *allocator[K, V]) capacity() int32 {
	return a.cap
}

// Evicted represents a key-value pair that was evicted from the cache.
type Evicted[K comparable, V any] struct {
	Key K
	Val V
}

// CacheResult is a bitmask indicating what happened during an Add or Probe operation.
type CacheResult uint8

const (
	// CacheHit indicates the key was already present in the cache.
	CacheHit CacheResult = 1 << iota

	// CacheEvict indicates an entry was evicted to make room for the new key.
	CacheEvict
)

// Hit reports whether the key was already present in the cache.
func (r CacheResult) Hit() bool { return r&CacheHit != 0 }

// Evicted reports whether an entry was evicted during the operation.
func (r CacheResult) Evicted() bool { return r&CacheEvict != 0 }

// Sieve represents a cache mapping the key of type 'K' with
// a value of type 'V'. The type 'K' must implement the
// comparable trait. An instance of Sieve has a fixed max capacity;
// new additions to the cache beyond the capacity will cause cache
// eviction of other entries - as determined by the SIEVE algorithm.
type Sieve[K comparable, V any] struct {
	mu    sync.Mutex
	cache *xsync.MapOf[K, int32]
	slots slotState // combined per-node lock + visited counter
	hand  int32     // eviction hand; sentinelIdx means "unset, start from tail"
	size  int

	allocator allocator[K, V] // embedded by value — one fewer GC-traced pointer
}

// New creates a new cache of size 'capacity' mapping key 'K' to value 'V'.
// Without options, this creates a classic SIEVE with a single visited bit (k=1).
// Use WithVisitClamp to create a SIEVE-k cache.
func New[K comparable, V any](capacity int, opts ...Option) *Sieve[K, V] {
	cfg := config{k: 1}
	for _, o := range opts {
		o(&cfg)
	}
	if cfg.k < 1 {
		cfg.k = 1
	}

	// +1 for sentinel in slot array to keep indexing aligned
	total := capacity + 1
	s := &Sieve[K, V]{
		cache: xsync.NewMapOf[K, int32](),
		hand:  sentinelIdx,
		slots: newSlotState(total, cfg.k),
	}
	initAllocator(&s.allocator, capacity)
	return s
}

// Get fetches the value for a given key in the cache.
// It returns true if the key is in the cache, false otherwise.
// The zero value for 'V' is returned when key is not in the cache.
func (s *Sieve[K, V]) Get(key K) (V, bool) {
	if idx, ok := s.cache.Load(key); ok {
		slots := &s.slots
		slots.LockAndMark(idx)
		n := &s.allocator.nodes[idx]
		if n.key == key {
			val := n.val
			slots.Unlock(idx)
			return val, true
		}
		// Stale idx: node was evicted and reused for a different key.
		slots.Unlock(idx)
	}

	var x V
	return x, false
}

// Add adds a new element to the cache or overwrites one if it exists.
// Returns the evicted entry (if any) and a CacheResult bitmask:
//   - CacheHit is set when the key was already present (value updated).
//   - CacheEvict is set when an entry was evicted to make room.
//
// CacheHit and CacheEvict are mutually exclusive: updating an existing
// key never triggers eviction.
func (s *Sieve[K, V]) Add(key K, val V) (Evicted[K, V], CacheResult) {
	nodes := s.allocator.nodes
	slots := &s.slots

	// Fast path: key exists, just update
	if idx, ok := s.cache.Load(key); ok {
		n := &nodes[idx]
		slots.LockAndMark(idx)
		if n.key == key {
			n.val = val
			slots.Unlock(idx)
			return Evicted[K, V]{}, CacheHit
		}
		// Stale idx: node was evicted and reused. Fall through to slow path.
		slots.Unlock(idx)
	}

	mu := &s.mu
	mu.Lock()
	// Re-check under lock to prevent double-insert (TOCTOU fix)
	if idx, ok := s.cache.Load(key); ok {
		slots.LockAndMark(idx)
		nodes[idx].val = val
		slots.Unlock(idx)
		mu.Unlock()
		return Evicted[K, V]{}, CacheHit
	}
	ev, evicted := s.add(key, val)
	mu.Unlock()

	if evicted {
		return ev, CacheEvict
	}
	return Evicted[K, V]{}, 0
}

// Probe adds <key, val> if not present in the cache.
// Returns:
//   - The cached value (on hit) or val (on miss)
//   - The evicted entry, if any
//   - A CacheResult bitmask: CacheHit if key was present, CacheEvict if an
//     entry was evicted. CacheHit and CacheEvict are mutually exclusive.
func (s *Sieve[K, V]) Probe(key K, val V) (V, Evicted[K, V], CacheResult) {
	nodes := s.allocator.nodes
	slots := &s.slots

	// Fast path: key exists
	if idx, ok := s.cache.Load(key); ok {
		n := &nodes[idx]
		slots.LockAndMark(idx)
		if n.key == key {
			v := n.val
			slots.Unlock(idx)
			return v, Evicted[K, V]{}, CacheHit
		}
		// Stale idx: node was evicted and reused. Fall through to slow path.
		slots.Unlock(idx)
	}

	mu := &s.mu
	mu.Lock()
	// Re-check under lock to prevent double-insert (TOCTOU fix)
	if idx, ok := s.cache.Load(key); ok {
		slots.LockAndMark(idx)
		v := nodes[idx].val
		slots.Unlock(idx)
		mu.Unlock()
		return v, Evicted[K, V]{}, CacheHit
	}
	ev, evicted := s.add(key, val)
	mu.Unlock()

	if evicted {
		return val, ev, CacheEvict
	}
	return val, Evicted[K, V]{}, 0
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

// Purge resets the cache. Concurrent Get/Add/Probe calls that loaded
// an index before Purge may return a stale result; this is inherent to
// any concurrent purge operation.
//
// We intentionally do NOT call slots.ResetAll() here. Visited bits for
// reused slots are cleared by newNode() via LockAndReset(), which safely
// spins until any concurrent fast-path holder releases the slot lock.
// An unconditional ResetAll(Store→0) would destroy locks held by stale
// fast-path goroutines, causing two goroutines to "hold" the same lock.
func (s *Sieve[K, V]) Purge() {
	s.mu.Lock()
	s.hand = sentinelIdx
	s.cache.Clear()
	s.allocator.reset()
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
	for idx := nodes[sentinelIdx].next; idx != sentinelIdx; idx = nodes[idx].next {
		h := "  "
		if idx == s.hand {
			h = ">>"
		}
		n := &nodes[idx]
		b.WriteString(fmt.Sprintf("%svisited=%v, key=%v, val=%v\n", h, s.slots.IsVisited(idx), n.key, n.val))
	}
	s.mu.Unlock()
	return b.String()
}

// -- internal methods --

// add a new tuple to the cache and evict as necessary.
// Returns the evicted entry (if any) so the caller can return it.
// Caller must hold lock.
func (s *Sieve[K, V]) add(key K, val V) (Evicted[K, V], bool) {
	var ev Evicted[K, V]
	var evicted bool

	// cache miss; we evict and find a new node
	if int32(s.size) == s.allocator.capacity() { // #nosec G115 — size never exceeds capacity (int32)
		ev, evicted = s.evict()
	}

	idx := s.newNode(key, val)

	// Eviction is guaranteed to remove one node; so this should never happen.
	if idx == nullIdx {
		msg := fmt.Sprintf("%T: add <%v>: objpool empty after eviction", s, key)
		panic(msg)
	}

	s.cache.Store(key, idx)

	nodes := s.allocator.nodes

	// Insert after sentinel (at head of list). Branch-free.
	n := &nodes[idx]
	sen := &nodes[sentinelIdx]
	head := sen.next

	n.next, n.prev = head, sentinelIdx
	sen.next, nodes[head].prev = idx, idx

	s.size += 1
	return ev, evicted
}

// evict removes one item from the cache and returns its key/value.
// Caller must hold the lock.
func (s *Sieve[K, V]) evict() (Evicted[K, V], bool) {
	hand := s.hand
	nodes := s.allocator.nodes
	sen := &nodes[sentinelIdx]

	if hand == sentinelIdx {
		// Start from tail (sentinel.prev)
		hand = sen.prev
	}

	for hand != sentinelIdx {
		n := &nodes[hand]
		if !s.slots.IsVisited(hand) {
			s.cache.Delete(n.key)
			s.hand = n.prev
			ev := s.remove(hand)
			return ev, true
		}
		s.slots.Clear(hand)
		hand = n.prev
		// Wrap around: if we hit sentinel, go to tail
		if hand == sentinelIdx {
			hand = sen.prev
		}
	}
	s.hand = hand
	var ev Evicted[K, V]
	return ev, false
}

// remove removes the node at idx from the linked list and frees it.
// It captures and returns the node's key/val before zeroing them,
// so callers (evict) can return eviction info.
// Caller must hold s.mu. Key/val are captured and zeroed under the
// slot lock to serialize with concurrent fast-path reads and to
// release GC references. Branch-free: sentinel eliminates null checks.
func (s *Sieve[K, V]) remove(idx int32) Evicted[K, V] {
	s.size -= 1

	nodes := s.allocator.nodes
	n := &nodes[idx]

	// Unlink — no branches needed thanks to sentinel
	nodes[n.prev].next = n.next
	nodes[n.next].prev = n.prev

	// Capture key/val and zero them under the slot lock to serialize
	// with concurrent fast-path reads (which write val under slot lock)
	// and allow GC to collect pointed-to objects.
	s.slots.Lock(idx)
	ev := Evicted[K, V]{Key: n.key, Val: n.val}
	var zk K
	var zv V
	n.key = zk
	n.val = zv
	s.slots.Unlock(idx)

	// Return the node to the allocator's freelist
	s.allocator.free(idx)
	return ev
}

// newNode allocates a node and initializes it with key and val.
// Returns nullIdx if no nodes are available.
//
// Field writes are performed under the slot lock to serialize with
// concurrent fast-path reads (Get/Add/Probe) that may hold a stale
// index from before eviction. The Lock/Unlock on the slot establishes
// a happens-before edge so the fast path sees the new key/val.
func (s *Sieve[K, V]) newNode(key K, val V) int32 {
	idx := s.allocator.alloc()
	if idx == nullIdx {
		return nullIdx
	}

	s.slots.LockAndReset(idx)

	n := &s.allocator.nodes[idx]
	n.key = key
	n.val = val
	n.next = nullIdx
	n.prev = nullIdx

	s.slots.Unlock(idx)
	return idx
}

// desc describes the properties of the sieve
func (s *Sieve[K, V]) desc() string {
	nodes := s.allocator.nodes
	m := fmt.Sprintf("cache<%T>: size %d, cap %d, head=%d, tail=%d, hand=%d",
		s, s.size, int(s.allocator.capacity()),
		nodes[sentinelIdx].next, nodes[sentinelIdx].prev, s.hand)
	return m
}
