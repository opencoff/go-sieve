// sieve_evict_test.go - eviction channel notification tests
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
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/opencoff/go-sieve"
)

// TestEvict_Basic verifies that a single eviction delivers the correct
// key and value on the eviction channel.
func TestEvict_Basic(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[int, string](4, sieve.WithOnEvict(8))
	defer s.Close()

	s.Add(1, "one")
	s.Add(2, "two")
	s.Add(3, "three")
	s.Add(4, "four")

	// Adding a 5th item triggers eviction of the tail (key 1, unvisited).
	s.Add(5, "five")

	ch := s.Evictor()
	select {
	case ev := <-ch:
		assert(ev.Key == 1, "evicted key: got %d, want 1", ev.Key)
		assert(ev.Val == "one", "evicted val: got %q, want %q", ev.Val, "one")
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eviction event")
	}
}

// TestEvict_CaptureBeforeZero verifies that the eviction event contains
// the original values, not zero values. This guards against a regression
// where evict() might zero the node before capturing.
func TestEvict_CaptureBeforeZero(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[string, int](2, sieve.WithOnEvict(4))
	defer s.Close()

	s.Add("alpha", 42)
	s.Add("beta", 99)
	s.Add("gamma", 7) // evicts "alpha"

	ev := <-s.Evictor()
	assert(ev.Key == "alpha", "evicted key: got %q, want %q", ev.Key, "alpha")
	assert(ev.Val == 42, "evicted val: got %d, want 42", ev.Val)
}

// TestEvict_Sequential verifies that overflowing the cache by N items
// produces exactly N eviction events with correct content.
func TestEvict_Sequential(t *testing.T) {
	assert := newAsserter(t)

	const cap = 4
	const overflow = 6
	s := sieve.New[int, int](cap, sieve.WithOnEvict(overflow+cap))
	defer s.Close()

	// Fill to capacity
	for i := 0; i < cap; i++ {
		s.Add(i, i*1000)
	}

	// Overflow — each add evicts one item
	for i := cap; i < cap+overflow; i++ {
		s.Add(i, i*1000)
	}

	ch := s.Evictor()
	received := 0
	for {
		select {
		case ev := <-ch:
			assert(ev.Val == ev.Key*1000, "event %d: key=%d val=%d, want val=%d",
				received, ev.Key, ev.Val, ev.Key*1000)
			received++
		default:
			goto done
		}
	}
done:
	assert(received == overflow, "expected %d eviction events, got %d", overflow, received)
}

// TestEvict_VisitedSkipped verifies that visited items are skipped during
// eviction and the correct (unvisited) item is reported as evicted.
func TestEvict_VisitedSkipped(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[int, string](4, sieve.WithOnEvict(8))
	defer s.Close()

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

	s.Add(5, "five") // should evict key 4 (unvisited)

	ev := <-s.Evictor()
	assert(ev.Key == 4, "evicted key: got %d, want 4", ev.Key)
	assert(ev.Val == "four", "evicted val: got %q, want %q", ev.Val, "four")
}

// TestEvict_SieveK verifies eviction notifications work correctly with
// SIEVE-k (multi-level visit counters).
func TestEvict_SieveK(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[string, int](3, sieve.WithVisitClamp(3), sieve.WithOnEvict(16))
	defer s.Close()

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
	s.Add("D", 4)

	ev := <-s.Evictor()
	assert(ev.Key == "C", "evicted key: got %q, want %q", ev.Key, "C")
	assert(ev.Val == 3, "evicted val: got %d, want 3", ev.Val)
}

// TestEvict_NoBelowCapacity verifies that no eviction events are produced
// when the cache is not yet full.
func TestEvict_NoBelowCapacity(t *testing.T) {
	s := sieve.New[int, int](8, sieve.WithOnEvict(8))
	defer s.Close()

	for i := 0; i < 8; i++ {
		s.Add(i, i)
	}

	ch := s.Evictor()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected eviction event: key=%d val=%d", ev.Key, ev.Val)
	default:
		// Good — no events
	}
}

// TestEvict_DeleteNoEvent verifies that Delete does not produce
// eviction events (only automatic evictions do).
func TestEvict_DeleteNoEvent(t *testing.T) {
	s := sieve.New[int, int](4, sieve.WithOnEvict(8))
	defer s.Close()

	s.Add(1, 10)
	s.Add(2, 20)
	s.Delete(1)

	ch := s.Evictor()
	select {
	case ev := <-ch:
		t.Fatalf("Delete should not produce eviction event: key=%d val=%d", ev.Key, ev.Val)
	default:
		// Good
	}
}

// TestEvict_PurgeNoEvent verifies that Purge does not produce eviction events.
func TestEvict_PurgeNoEvent(t *testing.T) {
	s := sieve.New[int, int](4, sieve.WithOnEvict(8))
	defer s.Close()

	for i := 0; i < 4; i++ {
		s.Add(i, i*10)
	}
	s.Purge()

	ch := s.Evictor()
	select {
	case ev := <-ch:
		t.Fatalf("Purge should not produce eviction event: key=%d val=%d", ev.Key, ev.Val)
	default:
		// Good
	}
}

// TestEvict_NilWithoutOption verifies that Evictor() returns nil when
// the cache is created without WithOnEvict.
func TestEvict_NilWithoutOption(t *testing.T) {
	s := sieve.New[int, int](4)
	if s.Evictor() != nil {
		t.Fatal("Evictor() should return nil without WithOnEvict")
	}
}

// TestEvict_Close verifies that Close() closes the eviction channel,
// allowing range loops to exit.
func TestEvict_Close(t *testing.T) {
	s := sieve.New[int, int](4, sieve.WithOnEvict(8))

	s.Add(1, 10)
	s.Add(2, 20)
	s.Add(3, 30)
	s.Add(4, 40)
	s.Add(5, 50) // evicts 1

	// Drain any pending events
	ch := s.Evictor()
drain:
	for {
		select {
		case <-ch:
		default:
			break drain
		}
	}

	done := make(chan bool, 1)
	go func() {
		// range should exit when channel is closed
		for range ch {
		}
		done <- true
	}()

	s.Close()

	select {
	case <-done:
		// Good — range exited after Close
	case <-time.After(time.Second):
		t.Fatal("range over Evictor() did not exit after Close")
	}
}

// TestEvict_UseAfterClose verifies that calling Add after Close panics.
func TestEvict_UseAfterClose(t *testing.T) {
	s := sieve.New[int, int](4, sieve.WithOnEvict(8))
	s.Add(1, 10)
	s.Close()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on Add after Close")
		}
	}()

	s.Add(2, 20)
}

// TestEvict_Concurrent verifies that all eviction events are received
// when multiple goroutines add items concurrently. Each goroutine uses
// a non-overlapping key range so every Add is a new insertion.
func TestEvict_Concurrent(t *testing.T) {
	assert := newAsserter(t)

	const (
		cacheSize = 64
		nWorkers  = 10
		keysPerW  = 100
	)
	totalKeys := nWorkers * keysPerW
	expectedEvictions := totalKeys - cacheSize

	s := sieve.New[int, int](cacheSize, sieve.WithOnEvict(expectedEvictions+128))
	defer s.Close()

	var wg sync.WaitGroup
	wg.Add(nWorkers)
	for g := 0; g < nWorkers; g++ {
		go func(id int) {
			defer wg.Done()
			base := id * keysPerW
			for i := 0; i < keysPerW; i++ {
				key := base + i
				s.Add(key, key*1000)
			}
		}(g)
	}
	wg.Wait()

	ch := s.Evictor()
	count := 0
	for {
		select {
		case ev := <-ch:
			assert(ev.Val == ev.Key*1000,
				"event %d: key=%d val=%d, want val=%d", count, ev.Key, ev.Val, ev.Key*1000)
			count++
		default:
			goto done
		}
	}
done:
	assert(count == expectedEvictions,
		"expected %d eviction events, got %d", expectedEvictions, count)
}

// TestEvict_Backpressure verifies that Add blocks (rather than dropping)
// when the eviction channel buffer is full.
func TestEvict_Backpressure(t *testing.T) {
	s := sieve.New[int, int](2, sieve.WithOnEvict(1))
	defer s.Close()

	s.Add(1, 100)
	s.Add(2, 200)

	// This Add triggers eviction; the single-slot buffer absorbs it.
	s.Add(3, 300)

	// Buffer is now full. Next eviction-triggering Add should block.
	done := make(chan bool, 1)
	go func() {
		s.Add(4, 400) // triggers eviction, but channel full → blocks
		done <- true
	}()

	// Give the goroutine time to start and block
	runtime.Gosched()
	time.Sleep(50 * time.Millisecond)

	select {
	case <-done:
		t.Fatal("Add should have blocked (eviction channel buffer full)")
	default:
		// Good — Add is blocked
	}

	// Drain one event — this should unblock the goroutine
	<-s.Evictor()

	select {
	case <-done:
		// Good — Add unblocked after drain
	case <-time.After(time.Second):
		t.Fatal("Add should have unblocked after draining eviction channel")
	}

	// Drain the second event
	<-s.Evictor()
}

// TestEvict_Probe verifies that Probe triggers eviction events when
// inserting a new key into a full cache.
func TestEvict_Probe(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[int, int](3, sieve.WithOnEvict(8))
	defer s.Close()

	s.Add(1, 100)
	s.Add(2, 200)
	s.Add(3, 300)

	// Probe with a new key triggers eviction
	v, existed := s.Probe(4, 400)
	assert(!existed, "Probe should return false for new key")
	assert(v == 400, "Probe should return the inserted value, got %d", v)

	ch := s.Evictor()
	select {
	case ev := <-ch:
		// Key 1 is the tail (first inserted, unvisited) → evicted
		assert(ev.Key == 1, "evicted key: got %d, want 1", ev.Key)
		assert(ev.Val == 100, "evicted val: got %d, want 100", ev.Val)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for eviction event from Probe")
	}
}
