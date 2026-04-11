// sieve_evict_test.go - eviction return value tests
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

package sieve_test

import (
	"sync"
	"testing"

	"github.com/opencoff/go-sieve"
)

// TestEvict_Basic verifies that a single eviction returns the correct
// key and value from Add.
func TestEvict_Basic(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, string](4))

	s.Add(1, "one")
	s.Add(2, "two")
	s.Add(3, "three")
	s.Add(4, "four")

	// Adding a 5th item triggers eviction of the tail (key 1, unvisited).
	ev, r := s.Add(5, "five")
	assert(r.Evicted(), "expected eviction on 5th add")
	assert(ev.Key == 1, "evicted key: got %d, want 1", ev.Key)
	assert(ev.Val == "one", "evicted val: got %q, want %q", ev.Val, "one")
}

// TestEvict_CaptureBeforeZero verifies that the eviction result contains
// the original values, not zero values.
func TestEvict_CaptureBeforeZero(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[string, int](2))

	s.Add("alpha", 42)
	s.Add("beta", 99)
	ev, r := s.Add("gamma", 7) // evicts "alpha"

	assert(r.Evicted(), "expected eviction")
	assert(ev.Key == "alpha", "evicted key: got %q, want %q", ev.Key, "alpha")
	assert(ev.Val == 42, "evicted val: got %d, want 42", ev.Val)
}

// TestEvict_Sequential verifies that overflowing the cache by N items
// produces exactly N eviction results with correct content.
func TestEvict_Sequential(t *testing.T) {
	assert := newAsserter(t)

	const cap = 4
	const overflow = 6
	s := sieve.Must(sieve.New[int, int](cap))

	// Fill to capacity — no evictions
	for i := 0; i < cap; i++ {
		_, r := s.Add(i, i*1000)
		assert(!r.Evicted(), "no eviction expected while filling, got one at i=%d", i)
	}

	// Overflow — each add evicts one item
	evictions := 0
	for i := cap; i < cap+overflow; i++ {
		ev, r := s.Add(i, i*1000)
		assert(r.Evicted(), "expected eviction at i=%d", i)
		assert(ev.Val == ev.Key*1000, "event %d: key=%d val=%d, want val=%d",
			evictions, ev.Key, ev.Val, ev.Key*1000)
		evictions++
	}
	assert(evictions == overflow, "expected %d evictions, got %d", overflow, evictions)
}

// TestEvict_VisitedSkipped verifies that visited items are skipped during
// eviction and the correct (unvisited) item is reported as evicted.
func TestEvict_VisitedSkipped(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, string](4))

	s.Add(1, "one")
	s.Add(2, "two")
	s.Add(3, "three")
	s.Add(4, "four")

	// Visit keys 1, 2, 3 — they get visited bits set.
	// Key 4 is at the head (last inserted), key 1 is at the tail.
	// Hand starts from tail. Key 1 is visited → clear, key 2 → clear,
	// key 3 → clear, key 4 (unvisited) → evict.
	s.Get(1)
	s.Get(2)
	s.Get(3)

	ev, r := s.Add(5, "five") // should evict key 4 (unvisited)
	assert(r.Evicted(), "expected eviction")
	assert(ev.Key == 4, "evicted key: got %d, want 4", ev.Key)
	assert(ev.Val == "four", "evicted val: got %q, want %q", ev.Val, "four")
}

// TestEvict_SieveK verifies eviction results work correctly with
// SIEVE-k (multi-level visit counters).
func TestEvict_SieveK(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[string, int](3, sieve.WithVisitClamp(3)))

	s.Add("A", 1)
	s.Add("B", 2)
	s.Add("C", 3)

	// Access A three times (saturates at k=3)
	s.Get("A")
	s.Get("A")
	s.Get("A")

	// Access B once
	s.Get("B")

	// C has no accesses → evicted first
	ev, r := s.Add("D", 4)
	assert(r.Evicted(), "expected eviction")
	assert(ev.Key == "C", "evicted key: got %q, want %q", ev.Key, "C")
	assert(ev.Val == 3, "evicted val: got %d, want 3", ev.Val)
}

// TestEvict_NoBelowCapacity verifies that no eviction occurs
// when the cache is not yet full.
func TestEvict_NoBelowCapacity(t *testing.T) {
	s := sieve.Must(sieve.New[int, int](8))

	for i := 0; i < 8; i++ {
		_, r := s.Add(i, i)
		if r.Evicted() {
			t.Fatalf("unexpected eviction at i=%d", i)
		}
	}
}

// TestEvict_HitNoEviction verifies that updating an existing key
// never triggers eviction (CacheHit and CacheEvict are mutually exclusive).
func TestEvict_HitNoEviction(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](4))

	// Fill to capacity
	for i := 0; i < 4; i++ {
		s.Add(i, i*10)
	}

	// Update existing keys — should be CacheHit, never CacheEvict
	for i := 0; i < 4; i++ {
		_, r := s.Add(i, i*100)
		assert(r.Hit(), "expected hit for existing key %d", i)
		assert(!r.Evicted(), "update should not trigger eviction for key %d", i)
	}
}

// TestEvict_Probe verifies that Probe triggers eviction when
// inserting a new key into a full cache.
func TestEvict_Probe(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](3))

	s.Add(1, 100)
	s.Add(2, 200)
	s.Add(3, 300)

	// Probe with a new key triggers eviction
	v, ev, r := s.Probe(4, 400)
	assert(!r.Hit(), "Probe should return miss for new key")
	assert(r.Evicted(), "Probe should trigger eviction")
	assert(v == 400, "Probe should return the inserted value, got %d", v)
	// Key 1 is the tail (first inserted, unvisited) → evicted
	assert(ev.Key == 1, "evicted key: got %d, want 1", ev.Key)
	assert(ev.Val == 100, "evicted val: got %d, want 100", ev.Val)
}

// TestEvict_ProbeHitNoEviction verifies that Probe on an existing key
// returns CacheHit with no eviction.
func TestEvict_ProbeHitNoEviction(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](4))

	for i := 0; i < 4; i++ {
		s.Add(i, i*10)
	}

	v, _, r := s.Probe(2, 999)
	assert(r.Hit(), "expected hit on existing key")
	assert(!r.Evicted(), "hit should not trigger eviction")
	assert(v == 20, "expected cached value 20, got %d", v)
}

// TestEvict_Concurrent verifies that eviction results are consistent
// when multiple goroutines add items concurrently.
func TestEvict_Concurrent(t *testing.T) {
	assert := newAsserter(t)

	const (
		cacheSize = 64
		nWorkers  = 10
		keysPerW  = 100
	)

	s := sieve.Must(sieve.New[int, int](cacheSize))

	var mu sync.Mutex
	var evictions []sieve.Evicted[int, int]

	var wg sync.WaitGroup
	wg.Add(nWorkers)
	for g := 0; g < nWorkers; g++ {
		go func(id int) {
			defer wg.Done()
			base := id * keysPerW
			for i := 0; i < keysPerW; i++ {
				key := base + i
				ev, r := s.Add(key, key*1000)
				if r.Evicted() {
					mu.Lock()
					evictions = append(evictions, ev)
					mu.Unlock()
				}
			}
		}(g)
	}
	wg.Wait()

	totalKeys := nWorkers * keysPerW
	expectedEvictions := totalKeys - cacheSize
	assert(len(evictions) == expectedEvictions,
		"expected %d eviction events, got %d", expectedEvictions, len(evictions))

	// Verify all evicted values are consistent
	for i, ev := range evictions {
		assert(ev.Val == ev.Key*1000,
			"event %d: key=%d val=%d, want val=%d", i, ev.Key, ev.Val, ev.Key*1000)
	}
}

// TestEvict_CacheResultBitmask verifies the CacheResult bitmask values.
func TestEvict_CacheResultBitmask(t *testing.T) {
	s := sieve.Must(sieve.New[int, int](2))

	// Case 1: new add, no eviction → result is 0
	_, r := s.Add(1, 10)
	if r != 0 {
		t.Fatalf("new add: expected result 0, got %d", r)
	}
	if r.Hit() || r.Evicted() {
		t.Fatal("new add: neither Hit nor Evicted should be set")
	}

	// Case 2: update existing → CacheHit
	_, r = s.Add(1, 20)
	if !r.Hit() {
		t.Fatal("update: expected CacheHit")
	}
	if r.Evicted() {
		t.Fatal("update: CacheEvict should not be set")
	}

	// Case 3: new add with eviction → CacheEvict
	s.Add(2, 20)
	_, r = s.Add(3, 30) // full cache, triggers eviction
	if r.Hit() {
		t.Fatal("eviction: CacheHit should not be set")
	}
	if !r.Evicted() {
		t.Fatal("eviction: expected CacheEvict")
	}
}
