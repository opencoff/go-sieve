// sieve.go - SIEVE - a simple and efficient cache


// golang impl of:
// https://cachemon.github.io/SIEVE-website/blog/2023/12/17/sieve-is-simpler-than-lru

package sieve

import (
	"io"
	"fmt"
	"sync"
	"strings"
)


type node[K comparable, V any] struct {
	key K
	val V
	visited bool
	next *node[K, V]
	prev *node[K, V]
}

type Sieve[K comparable, V any] struct {
	sync.Mutex
	cache map[K]*node[K, V]
	head  *node[K, V]
	tail  *node[K, V]
	hand  *node[K, V]
	size	int
	capacity int
}


func NewSieveCache[K comparable, V any](capacity int) *Sieve[K, V] {
	s := &Sieve[K, V]{
		cache: map[K]*node[K, V]{},
		capacity: capacity,
	}
	return s
}


func  (s *Sieve[K, V]) Get(key K) (V, bool) {
	s.Lock()

	if v, ok := s.cache[key]; ok {
		v.visited = true
		s.Unlock()
		return v.val, true
	}

	s.Unlock()
	var v V
	return v, false
}

// Add adds a new element to the cache or overwrite one if it exists
// Return true if we replaced, false otherwise
func (s *Sieve[K, V])  Add(key K, val V) bool {
	s.Lock()

	if v, ok := s.cache[key]; ok {
		v.visited = true
		v.val = val
		s.Unlock()
		return true
	}

	s.add(key, val)
	s.Unlock()
	return false
}

func (s *Sieve[K, V]) add(key K, val V) {
	// cache miss; we evict and fnd a new node
	if s.size == s.capacity {
		s.evict()
	}

	n := s.newNode(key, val)
	s.cache[key] = n

	// insert at the head of the list
	n.next = s.head
	n.prev = nil
	if s.head != nil {
		s.head.prev = n
	}
	s.head = n
	if s.tail == nil {
		s.tail = n
	}

	s.size += 1
}

// Probe adds <key, val> if not present in the cache.
// Returns:
//	V, true -- when key is present in the cache (V is not replaced)
//	V, false -- when key is not present and V is added to the cache
func (s *Sieve[K, V]) Probe(key K, val V) (V, bool) {
	s.Lock()

	if v, ok := s.cache[key]; ok {
		v.visited = true
		s.Unlock()
		return v.val, true
	}
	s.add(key, val)
	s.Unlock()
	return val, false
}


func (s *Sieve[K, V]) Delete(key K) bool {
	s.Lock()

	if v, ok := s.cache[key]; ok {
		s.remove(v)
		s.Unlock()
		return true
	}

	s.Unlock()
	return false
}

func (s *Sieve[K, V]) Purge() {
	clear(s.cache)
	s.head = nil
	s.tail = nil
	s.cache = map[K]*node[K, V]{}
}

func (s *Sieve[K, V]) Len() int {
	return s.size
}

func (s *Sieve[K, V]) Cap() int {
	return s.capacity
}

// evict an item from the cache.
// NB: Caller must hold the lock
func (s *Sieve[K, V]) evict() {
	hand := s.hand
	if hand == nil {
		hand = s.tail
	}

	for hand != nil {
		if !hand.visited {
			s.remove(hand)
			return
		}
		hand.visited = false
		hand = hand.prev
		// wrap around and start again
		if hand == nil {
			hand = s.tail
		}
	}
}

func (s *Sieve[K, V]) remove(n *node[K, V]) {
	delete(s.cache, n.key)
	s.size -= 1

	// remove node from list
	if n.prev != nil {
		n.prev.next = n.next
	} else {
		s.head = n.next
	}
	if n.next != nil {
		n.next.prev = n.prev
	} else {
		s.tail = n.prev
	}
}

func (s *Sieve[K, V]) newNode(key K, val V) *node[K, V] {
	// XXX sync.pool
	n := &node[K, V]{
		val: val,
		key: key,
	}
	return n
}


func (s *Sieve[K, V]) Dump(wr io.Writer) {
	var b strings.Builder

	s.Lock()
	b.WriteString(fmt.Sprintf("cache<%T>: size %d, cap %d\n", s, s.size, s.capacity))
	for n := s.head; n != nil; n = n.next {
		b.WriteString(fmt.Sprintf("  visited=%v, key=%v, val=%v\n", n.visited, n.key, n.val))
	}
	wr.Write([]byte(b.String()))
	s.Unlock()
}
