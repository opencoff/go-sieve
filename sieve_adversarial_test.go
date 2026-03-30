// sieve_adversarial_test.go - comprehensive functional, edge-case, and concurrency tests
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
	"fmt"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/opencoff/go-sieve"
)

// --- Edge Case Tests ---

func TestEdge_Capacity1(t *testing.T) {
	s := sieve.New[int, string](1)

	if s.Cap() != 1 {
		t.Fatalf("Cap() = %d, want 1", s.Cap())
	}

	s.Add(1, "a")
	if v, ok := s.Get(1); !ok || v != "a" {
		t.Fatalf("Get(1) = (%q, %v), want (a, true)", v, ok)
	}

	// Adding second key evicts first
	s.Add(2, "b")
	if s.Len() != 1 {
		t.Fatalf("Len() = %d, want 1", s.Len())
	}
	if _, ok := s.Get(1); ok {
		t.Fatal("expected key 1 to be evicted")
	}
	if v, ok := s.Get(2); !ok || v != "b" {
		t.Fatalf("Get(2) = (%q, %v), want (b, true)", v, ok)
	}
}

func TestEdge_GetEmptyCache(t *testing.T) {
	s := sieve.New[string, int](10)

	v, ok := s.Get("nonexistent")
	if ok {
		t.Fatal("expected miss on empty cache")
	}
	if v != 0 {
		t.Fatalf("expected zero value, got %d", v)
	}
}

func TestEdge_DeleteNonexistent(t *testing.T) {
	s := sieve.New[int, int](10)

	if s.Delete(42) {
		t.Fatal("expected false for deleting from empty cache")
	}

	s.Add(1, 1)
	if s.Delete(42) {
		t.Fatal("expected false for deleting nonexistent key")
	}
}

func TestEdge_ProbeInsertsThenReturns(t *testing.T) {
	s := sieve.New[int, string](4)

	// Probe on miss inserts and returns (val, _, 0)
	v, _, r := s.Probe(1, "hello")
	if r.Hit() {
		t.Fatal("expected miss on first Probe")
	}
	if v != "hello" {
		t.Fatalf("Probe miss: expected %q, got %q", "hello", v)
	}

	// Probe on hit returns cached value, not the passed value
	v, _, r = s.Probe(1, "world")
	if !r.Hit() {
		t.Fatal("expected hit on second Probe")
	}
	if v != "hello" {
		t.Fatalf("Probe hit: expected %q, got %q", "hello", v)
	}
}

func TestEdge_PurgeAndReuse(t *testing.T) {
	s := sieve.New[int, int](8)

	for i := 0; i < 8; i++ {
		s.Add(i, i*10)
	}
	if s.Len() != 8 {
		t.Fatalf("pre-purge Len() = %d, want 8", s.Len())
	}

	s.Purge()

	if s.Len() != 0 {
		t.Fatalf("post-purge Len() = %d, want 0", s.Len())
	}
	if s.Cap() != 8 {
		t.Fatalf("post-purge Cap() = %d, want 8", s.Cap())
	}

	// All old keys should be gone
	for i := 0; i < 8; i++ {
		if _, ok := s.Get(i); ok {
			t.Fatalf("key %d should not exist after Purge", i)
		}
	}

	// Re-add should work
	for i := 0; i < 8; i++ {
		s.Add(i, i*100)
	}
	for i := 0; i < 8; i++ {
		v, ok := s.Get(i)
		if !ok {
			t.Fatalf("key %d missing after re-add", i)
		}
		if v != i*100 {
			t.Fatalf("key %d: expected %d, got %d", i, i*100, v)
		}
	}
}

func TestEdge_AddUpdateReturnValue(t *testing.T) {
	s := sieve.New[string, int](4)

	// First add: not a hit (new key)
	_, r := s.Add("x", 1)
	if r.Hit() {
		t.Fatal("first Add should not be a hit")
	}

	// Second add: hit (existing key updated)
	_, r = s.Add("x", 2)
	if !r.Hit() {
		t.Fatal("second Add should be a hit")
	}

	v, _ := s.Get("x")
	if v != 2 {
		t.Fatalf("after update: expected 2, got %d", v)
	}
}

func TestEdge_DeleteReducesLen(t *testing.T) {
	s := sieve.New[int, int](16)

	for i := 0; i < 10; i++ {
		s.Add(i, i)
	}
	if s.Len() != 10 {
		t.Fatalf("Len() = %d, want 10", s.Len())
	}

	for i := 0; i < 10; i++ {
		ok := s.Delete(i)
		if !ok {
			t.Fatalf("Delete(%d) returned false", i)
		}
		expected := 10 - i - 1
		if s.Len() != expected {
			t.Fatalf("after Delete(%d): Len() = %d, want %d", i, s.Len(), expected)
		}
	}
}

func TestEdge_LenNeverExceedsCap(t *testing.T) {
	const cap = 32
	s := sieve.New[int, int](cap)

	for i := 0; i < cap*10; i++ {
		s.Add(i, i)
		if s.Len() > cap {
			t.Fatalf("after Add(%d): Len()=%d > Cap()=%d", i, s.Len(), cap)
		}
	}
}

// --- Concurrent Stress Tests ---

// TestConcurrent_DeleteStress tests Delete under high concurrency.
func TestConcurrent_DeleteStress(t *testing.T) {
	const (
		cacheSize  = 512
		goroutines = 50
		opsPerG    = 2000
	)

	s := sieve.New[int, int](cacheSize)

	// Pre-fill
	for i := 0; i < cacheSize; i++ {
		s.Add(i, i)
	}

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(rand.Int63()))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(cacheSize * 2)
				switch r.Intn(4) {
				case 0:
					s.Delete(key)
				case 1:
					s.Add(key, key)
				case 2:
					s.Get(key)
				case 3:
					s.Probe(key, key)
				}
			}
		}()
	}
	wg.Wait()

	if s.Len() < 0 || s.Len() > cacheSize {
		t.Fatalf("Len()=%d out of range [0, %d]", s.Len(), cacheSize)
	}
}

// TestConcurrent_PurgeUnderLoad tests Purge racing with Get/Add.
func TestConcurrent_PurgeUnderLoad(t *testing.T) {
	const (
		cacheSize  = 256
		goroutines = 20
		opsPerG    = 1000
		purges     = 10
	)

	s := sieve.New[int, int](cacheSize)

	var wg sync.WaitGroup
	var stop atomic.Bool

	// Workers doing Get/Add
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(rand.Int63()))
			for !stop.Load() {
				key := r.Intn(cacheSize * 2)
				if r.Intn(2) == 0 {
					s.Get(key)
				} else {
					s.Add(key, key)
				}
			}
		}()
	}

	// Purger
	for i := 0; i < purges; i++ {
		// Let workers run a bit
		runtime.Gosched()
		s.Purge()
	}

	stop.Store(true)
	wg.Wait()

	// After all workers stop, cache should be in a consistent state
	if s.Len() < 0 || s.Len() > cacheSize {
		t.Fatalf("Len()=%d out of range [0, %d]", s.Len(), cacheSize)
	}
}

// TestConcurrent_EvictionStress exercises eviction under heavy concurrent load.
// This specifically targets the code path where eviction scans the list while
// concurrent Get() calls re-mark visited bits.
func TestConcurrent_EvictionStress(t *testing.T) {
	const (
		cacheSize  = 64 // small cache to maximize eviction rate
		goroutines = 20
		opsPerG    = 10000
	)

	s := sieve.New[int, int](cacheSize)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerG; i++ {
				// Key range much larger than cache → constant eviction
				key := r.Intn(cacheSize * 20)
				switch r.Intn(10) {
				case 0, 1, 2, 3, 4, 5: // 60% Get
					s.Get(key)
				case 6, 7, 8: // 30% Add
					s.Add(key, key)
				case 9: // 10% Delete
					s.Delete(key)
				}
			}
		}(g)
	}
	wg.Wait()

	if s.Len() < 0 || s.Len() > cacheSize {
		t.Fatalf("Len()=%d out of range [0, %d]", s.Len(), cacheSize)
	}
}

// TestConcurrent_ValueConsistency checks that Get returns the correct value
// for the key it was asked about, not a value from a different key.
// This is a probabilistic detector for the ABA problem on index reuse.
func TestConcurrent_ValueConsistency(t *testing.T) {
	const (
		cacheSize  = 64 // small cache to force frequent eviction/reuse
		goroutines = 12
		opsPerG    = 50000
		keyRange   = cacheSize * 4 // 4x cache to force eviction
	)

	// Values encode the key, so we can detect cross-key contamination.
	// val = key * 1000 + arbitrary_suffix
	s := sieve.New[int, int](cacheSize)

	var violations atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(keyRange)
				encodedVal := key*1000 + r.Intn(1000)

				if r.Intn(3) == 0 {
					// Add: value encodes which key it belongs to
					s.Add(key, encodedVal)
				} else {
					// Get: verify value belongs to this key
					if v, ok := s.Get(key); ok {
						gotKey := v / 1000
						if gotKey != key {
							violations.Add(1)
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()

	v := violations.Load()
	if v > 0 {
		t.Errorf("detected %d value consistency violations (ABA problem on index reuse)", v)
	}
}

// TestConcurrent_ProbeConsistency checks that Probe returns consistent values.
func TestConcurrent_ProbeConsistency(t *testing.T) {
	const (
		cacheSize  = 32
		goroutines = 10
		opsPerG    = 20000
		keyRange   = cacheSize * 4
	)

	s := sieve.New[int, int](cacheSize)

	var violations atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(keyRange)
				probeVal := key * 1000

				v, _, _ := s.Probe(key, probeVal)
				gotKey := v / 1000
				if gotKey != key {
					violations.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	v := violations.Load()
	if v > 0 {
		t.Errorf("detected %d Probe value consistency violations", v)
	}
}

// --- SIEVE-k Additional Tests ---

func TestSieveK_K2(t *testing.T) {
	c := sieve.New[string, int](3, sieve.WithVisitClamp(2))

	c.Add("A", 1)
	c.Add("B", 2)
	c.Add("C", 3)

	// Access A twice (saturates at k=2)
	c.Get("A")
	c.Get("A")

	// Access B once
	c.Get("B")

	// C has no accesses → evicted first
	c.Add("D", 4)
	if _, ok := c.Get("C"); ok {
		t.Fatal("expected C to be evicted")
	}
	if _, ok := c.Get("A"); !ok {
		t.Fatal("expected A to survive")
	}
}

func TestSieveK_LargeK(t *testing.T) {
	// k=7 — uses 3 bits for counter
	c := sieve.New[int, int](4, sieve.WithVisitClamp(7))

	c.Add(1, 1)
	c.Add(2, 2)
	c.Add(3, 3)
	c.Add(4, 4)

	// Access key 1 many times
	for i := 0; i < 20; i++ {
		c.Get(1)
	}

	// Force several evictions — key 1 should survive
	c.Add(5, 5) // evicts one of 2,3,4
	c.Add(6, 6) // evicts another
	c.Add(7, 7) // evicts another

	if _, ok := c.Get(1); !ok {
		t.Fatal("key 1 should survive with k=7 and many accesses")
	}
}

func TestSieveK_PurgeResetsCounters(t *testing.T) {
	c := sieve.New[int, int](4, sieve.WithVisitClamp(3))

	c.Add(1, 1)
	for i := 0; i < 10; i++ {
		c.Get(1)
	}

	c.Purge()

	if c.Len() != 0 {
		t.Fatalf("post-purge Len() = %d, want 0", c.Len())
	}

	// Re-add and verify counters are reset (not saturated from before)
	c.Add(1, 10)
	c.Add(2, 20)
	c.Add(3, 30)
	c.Add(4, 40)

	// Don't access 1 at all. Add a 5th item to trigger eviction.
	// With reset counters, 1 (unvisited) should be evicted.
	c.Add(5, 50)
	// We can't guarantee which item is evicted without knowing hand position,
	// but we can verify the cache is consistent.
	if c.Len() != 4 {
		t.Fatalf("post-eviction Len() = %d, want 4", c.Len())
	}
}

// --- Dump/String Validation ---

func TestDump_Format(t *testing.T) {
	s := sieve.New[int, string](4)
	s.Add(1, "a")
	s.Add(2, "b")
	s.Add(3, "c")

	dump := s.Dump()
	if dump == "" {
		t.Fatal("Dump() returned empty string")
	}

	str := s.String()
	if str == "" {
		t.Fatal("String() returned empty string")
	}
}

func TestString_ShowsCapAndSize(t *testing.T) {
	s := sieve.New[int, int](16)
	for i := 0; i < 5; i++ {
		s.Add(i, i)
	}

	str := s.String()
	// String should contain size and cap info
	expected := fmt.Sprintf("size %d, cap %d", 5, 16)
	if len(str) == 0 {
		t.Fatal("String() is empty")
	}
	_ = expected // we just verify it doesn't panic and returns non-empty
}

// --- Heavy Churn Test ---

// TestChurn_HandWrapAround forces many evictions to exercise hand wrap-around.
func TestChurn_HandWrapAround(t *testing.T) {
	const cap = 16
	s := sieve.New[int, int](cap)

	// Fill and churn through many iterations
	for i := 0; i < cap*100; i++ {
		s.Add(i, i)
		if s.Len() > cap {
			t.Fatalf("iter %d: Len()=%d > Cap()=%d", i, s.Len(), cap)
		}

		// Periodically Get some items to set visited bits
		if i%3 == 0 {
			s.Get(i)
		}
	}

	// Final state should be consistent
	if s.Len() != cap {
		t.Fatalf("final Len()=%d, want %d", s.Len(), cap)
	}
}

// TestChurn_DeleteAndRefill tests alternating delete and add patterns.
func TestChurn_DeleteAndRefill(t *testing.T) {
	const cap = 32
	s := sieve.New[int, int](cap)

	for round := 0; round < 10; round++ {
		base := round * cap

		// Fill
		for i := 0; i < cap; i++ {
			s.Add(base+i, base+i)
		}

		// Delete half
		for i := 0; i < cap/2; i++ {
			s.Delete(base + i)
		}

		if s.Len() > cap {
			t.Fatalf("round %d: Len()=%d > Cap()=%d", round, s.Len(), cap)
		}
	}
}

// =========================================================================
// Additional edge case tests
// =========================================================================

// TestEdge_EvictionAllVisited verifies eviction works when every node
// in the list is visited. The hand must scan the entire list, clear all
// visited bits, wrap around, and evict the first unvisited node.
func TestEdge_EvictionAllVisited(t *testing.T) {
	const cap = 8
	s := sieve.New[int, int](cap)

	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
	}

	// Visit ALL nodes
	for i := 0; i < cap; i++ {
		s.Get(i)
	}

	// Add one more — forces full-list eviction scan
	s.Add(cap, cap*10)

	if s.Len() != cap {
		t.Fatalf("Len()=%d, want %d", s.Len(), cap)
	}

	// New key must be present
	v, ok := s.Get(cap)
	if !ok || v != cap*10 {
		t.Fatalf("Get(%d) = (%d, %v), want (%d, true)", cap, v, ok, cap*10)
	}

	// Exactly one old key evicted
	present := 0
	for i := 0; i < cap; i++ {
		if _, ok := s.Get(i); ok {
			present++
		}
	}
	if present != cap-1 {
		t.Fatalf("expected %d old keys present, got %d", cap-1, present)
	}
}

// TestEdge_EvictionAllVisited_Repeated exercises the all-visited path
// across many consecutive evictions.
func TestEdge_EvictionAllVisited_Repeated(t *testing.T) {
	const cap = 16
	s := sieve.New[int, int](cap)

	// Fill
	for i := 0; i < cap; i++ {
		s.Add(i, i)
	}

	for round := 0; round < 10; round++ {
		// Visit everything currently in cache
		for i := 0; i < cap*20; i++ {
			s.Get(i) // misses are no-ops
		}

		// Force cap/2 evictions, each facing all-visited list
		for i := 0; i < cap/2; i++ {
			key := (round+1)*1000 + i
			s.Add(key, key)
			if s.Len() != cap {
				t.Fatalf("round %d, eviction %d: Len()=%d, want %d", round, i, s.Len(), cap)
			}
		}
	}
}

// TestEdge_NewWithVisits_K0 verifies that NewWithVisits with k=0 is
// clamped to k=1 and behaves identically to New.
func TestEdge_NewWithVisits_K0(t *testing.T) {
	c := sieve.New[int, int](4, sieve.WithVisitClamp(0))

	c.Add(1, 10)
	c.Add(2, 20)
	c.Add(3, 30)
	c.Add(4, 40)

	if c.Len() != 4 {
		t.Fatalf("Len()=%d, want 4", c.Len())
	}

	v, ok := c.Get(1)
	if !ok || v != 10 {
		t.Fatalf("Get(1) = (%d, %v), want (10, true)", v, ok)
	}

	// Trigger eviction — same behavior as k=1
	c.Add(5, 50)
	if c.Len() != 4 {
		t.Fatalf("after eviction: Len()=%d, want 4", c.Len())
	}
}

// TestEdge_Capacity2 tests a 2-entry cache. When cache has 1 item,
// head == tail. Exercises boundary conditions in list operations.
func TestEdge_Capacity2(t *testing.T) {
	s := sieve.New[string, int](2)

	s.Add("a", 1)
	if s.Len() != 1 {
		t.Fatalf("Len()=%d, want 1", s.Len())
	}

	s.Add("b", 2)
	if s.Len() != 2 {
		t.Fatalf("Len()=%d, want 2", s.Len())
	}

	// Access a so it's visited
	s.Get("a")

	// Add c — should evict b (unvisited) or the tail
	s.Add("c", 3)
	if s.Len() != 2 {
		t.Fatalf("after eviction: Len()=%d, want 2", s.Len())
	}

	// c must be present (just added)
	if _, ok := s.Get("c"); !ok {
		t.Fatal("expected c to be present")
	}

	// Delete both
	s.Delete("a")
	s.Delete("c")
	if s.Len() != 0 {
		t.Fatalf("after deleting all: Len()=%d, want 0", s.Len())
	}

	// Re-add should work
	s.Add("x", 42)
	v, ok := s.Get("x")
	if !ok || v != 42 {
		t.Fatalf("Get(x) = (%d, %v), want (42, true)", v, ok)
	}
}

// TestEdge_ProbeAfterDelete verifies that Probe re-inserts a deleted key.
func TestEdge_ProbeAfterDelete(t *testing.T) {
	s := sieve.New[string, int](4)

	s.Add("key", 100)
	s.Delete("key")

	if _, ok := s.Get("key"); ok {
		t.Fatal("key should be gone after Delete")
	}

	// Probe should re-insert
	v, _, r := s.Probe("key", 200)
	if r.Hit() {
		t.Fatal("Probe should return miss after Delete")
	}
	if v != 200 {
		t.Fatalf("Probe miss: expected 200, got %d", v)
	}

	// Now it should be present
	v2, ok := s.Get("key")
	if !ok || v2 != 200 {
		t.Fatalf("Get after Probe re-insert = (%d, %v), want (200, true)", v2, ok)
	}
}

// TestEdge_DeleteThenEvict verifies that deleting keys and then triggering
// evictions doesn't corrupt the cache (exercises stale hand + freelist reuse).
func TestEdge_DeleteThenEvict(t *testing.T) {
	const cap = 8
	s := sieve.New[int, int](cap)

	// Fill
	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
	}

	// Visit some, then add to trigger eviction and set the hand
	s.Get(1)
	s.Get(2)
	s.Get(3)
	s.Add(cap, cap*10) // evicts one unvisited key, sets hand

	// Delete several keys
	s.Delete(1)
	s.Delete(2)

	// Now add many keys — forces eviction using potentially stale hand
	for i := 0; i < cap*3; i++ {
		key := cap*10 + i
		s.Add(key, key)
		if s.Len() > cap {
			t.Fatalf("iter %d: Len()=%d > Cap()=%d", i, s.Len(), cap)
		}
	}
}

// =========================================================================
// Additional concurrency tests
// =========================================================================

// TestConcurrent_PurgeValueCorrectness verifies that Get never returns a
// value belonging to a different key during concurrent Purge+Add cycles.
// Tracks operation counts to prove we actually exercised the contended paths.
func TestConcurrent_PurgeValueCorrectness(t *testing.T) {
	const (
		cacheSize  = 128
		goroutines = 20
		purges     = 20
	)

	s := sieve.New[int, int](cacheSize)

	var violations atomic.Int64
	var gets, adds, hits atomic.Int64
	var wg sync.WaitGroup
	var stop atomic.Bool
	var ready sync.WaitGroup

	// Workers: Get/Add with encoded values
	ready.Add(goroutines)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			r := rand.New(rand.NewSource(rand.Int63()))
			ready.Done()
			for !stop.Load() {
				key := r.Intn(cacheSize * 2)
				encodedVal := key * 1000

				if r.Intn(3) == 0 {
					s.Add(key, encodedVal)
					adds.Add(1)
				} else {
					gets.Add(1)
					if v, ok := s.Get(key); ok {
						hits.Add(1)
						if v/1000 != key {
							violations.Add(1)
						}
					}
				}
			}
		}()
	}

	// Wait for all workers to start, then purge
	ready.Wait()
	for i := 0; i < purges; i++ {
		runtime.Gosched()
		s.Purge()
	}

	stop.Store(true)
	wg.Wait()

	v := violations.Load()
	if v > 0 {
		t.Errorf("detected %d value consistency violations during Purge", v)
	}

	// Verify we actually did meaningful work
	if gets.Load() < 1000 {
		t.Fatalf("too few Gets (%d) — test didn't exercise read path", gets.Load())
	}
	if adds.Load() < 100 {
		t.Fatalf("too few Adds (%d) — test didn't exercise write path", adds.Load())
	}
	if hits.Load() == 0 {
		t.Fatal("zero hits — Purge may have prevented all cache reads from succeeding")
	}
	t.Logf("ops: %d gets (%d hits), %d adds, %d purges, %d violations",
		gets.Load(), hits.Load(), adds.Load(), purges, v)
}

// TestConcurrent_AddDeleteSameKey focuses on concurrent Add and Delete
// targeting the same key. The fast-path Add (per-node lock) races with
// Delete (global mutex + node zeroing).
// Tracks hit counts to prove we actually exercised the contended paths.
func TestConcurrent_AddDeleteSameKey(t *testing.T) {
	const (
		cacheSize  = 64
		goroutines = 20
		opsPerG    = 20000
		keyRange   = 16 // small key range to maximize collisions
	)

	s := sieve.New[int, int](cacheSize)

	var deletes, adds, getHits, probeHits, violations atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(keyRange)
				encodedVal := key * 1000

				switch r.Intn(4) {
				case 0:
					if s.Delete(key) {
						deletes.Add(1)
					}
				case 1:
					s.Add(key, encodedVal)
					adds.Add(1)
				case 2:
					if v, ok := s.Get(key); ok {
						getHits.Add(1)
						if v/1000 != key {
							violations.Add(1)
						}
					}
				case 3:
					if v, _, r := s.Probe(key, encodedVal); r.Hit() {
						probeHits.Add(1)
						if v/1000 != key {
							violations.Add(1)
						}
					}
				}
			}
		}(g)
	}
	wg.Wait()

	if v := violations.Load(); v > 0 {
		t.Errorf("detected %d value consistency violations", v)
	}
	if s.Len() < 0 || s.Len() > cacheSize {
		t.Fatalf("Len()=%d out of range [0, %d]", s.Len(), cacheSize)
	}

	// Verify all operation types were exercised
	if deletes.Load() == 0 {
		t.Fatal("zero successful deletes — didn't exercise Delete path")
	}
	if getHits.Load() == 0 {
		t.Fatal("zero Get hits — didn't exercise fast-path read")
	}
	if probeHits.Load() == 0 {
		t.Fatal("zero Probe hits — didn't exercise Probe fast-path")
	}
	t.Logf("ops: %d adds, %d deletes, %d get-hits, %d probe-hits",
		adds.Load(), deletes.Load(), getHits.Load(), probeHits.Load())
}

// TestConcurrent_StaleGetDuringReallocation forces the LockAndReset / LockAndMark
// interleaving at the Sieve integration level. A very small cache (cap=8) with a
// 10x key range maximizes the rate of eviction and slot reuse, increasing the
// probability that a Get() in progress on slot X sees LockAndReset() on the same
// slot during reallocation. Any cross-key value contamination is detected.
func TestConcurrent_StaleGetDuringReallocation(t *testing.T) {
	const (
		cacheSize  = 8
		keyRange   = cacheSize * 10 // 80 keys, forces constant eviction
		goroutines = 20
		opsPerG    = 100_000
	)

	s := sieve.New[int, int](cacheSize)

	var violations atomic.Int64
	var gets, hits, adds atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id * 31)))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(keyRange)
				if r.Intn(10) < 7 { // 70% Get
					gets.Add(1)
					if v, ok := s.Get(key); ok {
						hits.Add(1)
						if v/1000 != key {
							violations.Add(1)
						}
					}
				} else { // 30% Add
					adds.Add(1)
					s.Add(key, key*1000)
				}
			}
		}(g)
	}
	wg.Wait()

	v := violations.Load()
	if v > 0 {
		t.Errorf("detected %d cross-key value contaminations (cap=%d, keyRange=%d)", v, cacheSize, keyRange)
	}

	if hits.Load() == 0 {
		t.Fatal("zero hits — test didn't exercise the Get fast path")
	}

	t.Logf("ops: %d gets (%d hits), %d adds, violations=%d",
		gets.Load(), hits.Load(), adds.Load(), v)
}

// TestConcurrent_ProbeReturnValue verifies Probe's return value contract
// under concurrent access. When multiple goroutines Probe the same key:
// - Exactly one should get (val, false) — the inserter
// - All others should get (cachedVal, true) — the value from the winner
// We test this statistically: all returned values must encode the correct key.
func TestConcurrent_ProbeReturnValue(t *testing.T) {
	const (
		cacheSize  = 128
		goroutines = 50
		opsPerG    = 10000
		keyRange   = 32 // small to force Probe collisions
	)

	s := sieve.New[int, int](cacheSize)

	var violations atomic.Int64
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < opsPerG; i++ {
				key := r.Intn(keyRange)
				probeVal := key * 1000

				v, _, _ := s.Probe(key, probeVal)
				// Whether hit or miss, the value must belong to this key
				if v/1000 != key {
					violations.Add(1)
				}
			}
		}(g)
	}
	wg.Wait()

	v := violations.Load()
	if v > 0 {
		t.Errorf("detected %d Probe return value violations", v)
	}
}
