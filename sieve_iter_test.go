// sieve_iter_test.go - tests for the All() range iterator
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
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencoff/go-sieve"
)

// TestAll_Basic fills a cache below capacity and verifies that All()
// yields every inserted (key, value) pair exactly once.
func TestAll_Basic(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](64))
	want := make(map[int]int, 32)
	for i := 1; i <= 32; i++ {
		s.Add(i, i*10)
		want[i] = i * 10
	}

	got := make(map[int]int, 32)
	for k, v := range s.All() {
		_, dup := got[k]
		assert(!dup, "duplicate key %d yielded", k)
		got[k] = v
	}

	assert(len(got) == len(want), "yielded %d entries, want %d", len(got), len(want))
	for k, v := range want {
		gv, ok := got[k]
		assert(ok, "missing key %d", k)
		assert(gv == v, "key %d: got %d, want %d", k, gv, v)
	}
}

// TestAll_Empty verifies iteration over an empty cache yields nothing.
func TestAll_Empty(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](16))
	n := 0
	for range s.All() {
		n++
	}
	assert(n == 0, "empty cache yielded %d entries", n)
}

// TestAll_EarlyBreak verifies that breaking out of the range loop
// terminates iteration cleanly.
func TestAll_EarlyBreak(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](128))
	for i := 1; i <= 100; i++ {
		s.Add(i, i)
	}

	n := 0
	for k, v := range s.All() {
		assert(k == v, "k/v mismatch: %d != %d", k, v)
		n++
		if n == 3 {
			break
		}
	}
	assert(n == 3, "expected to break after 3 entries, got %d", n)
}

// TestAll_DoesNotPromote verifies that iterating over the cache does not
// set visited bits on entries. After a full walk, the next eviction
// should still target the SIEVE FIFO tail.
func TestAll_DoesNotPromote(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, string](4))
	s.Add(1, "one")
	s.Add(2, "two")
	s.Add(3, "three")
	s.Add(4, "four")

	// Full walk — must not mark any entry visited.
	n := 0
	for range s.All() {
		n++
	}
	assert(n == 4, "expected 4 entries, got %d", n)

	// Adding a 5th item should still evict the oldest (key 1).
	// If iteration had marked entries visited, the hand would skip key 1
	// and evict something else (or scan multiple times).
	ev, r := s.Add(5, "five")
	assert(r.Evicted(), "expected eviction on 5th add")
	assert(ev.Key == 1, "iteration leaked visited bit: evicted %d, want 1", ev.Key)
}

// TestAll_ReentrantOps verifies that Get/Add/Delete called from inside
// the iterator body do not deadlock and behave correctly.
func TestAll_ReentrantOps(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](128))
	for i := 1; i <= 50; i++ {
		s.Add(i, i)
	}

	seen := 0
	for k, v := range s.All() {
		assert(k == v, "k/v mismatch: %d != %d", k, v)
		seen++

		// Re-entrant Get — must not deadlock.
		gv, ok := s.Get(k)
		assert(ok, "Get(%d) missing during iteration", k)
		assert(gv == v, "Get(%d): got %d, want %d", k, gv, v)

		// Re-entrant Delete on an unrelated key.
		if k == 25 {
			s.Delete(1)
		}

		// Re-entrant Add of a new key.
		if k == 30 {
			s.Add(1000, 1000)
		}
	}
	// We expect to see at least the originals minus the one we deleted
	// before reaching it, but xsync's relaxed scan can surface new
	// inserts non-deterministically, so we just sanity-check the bound.
	assert(seen >= 49 && seen <= 52, "unexpected yield count: %d", seen)
}

// TestAll_ConcurrentReaders verifies that multiple concurrent iterators
// over the same cache do not deadlock or report data races.
func TestAll_ConcurrentReaders(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.Must(sieve.New[int, int](1024))
	for i := 1; i <= 500; i++ {
		s.Add(i, i*7)
	}

	const readers = 8
	var wg sync.WaitGroup
	wg.Add(readers)
	for r := 0; r < readers; r++ {
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				count := 0
				for k, v := range s.All() {
					if v != k*7 {
						t.Errorf("concurrent reader: bad pair (%d, %d)", k, v)
						return
					}
					count++
				}
				if count == 0 {
					t.Errorf("concurrent reader saw empty cache")
					return
				}
			}
		}()
	}
	wg.Wait()
	assert(true, "no deadlock")
}

// TestAll_ConcurrentWritersAndIterator runs writers (Add / Probe / Delete /
// Get) in parallel with an iterator goroutine and verifies invariants:
//   - no yielded pair has the zero key/value sentinel from an evicted slot
//   - the (k, v) pair satisfies our test invariant (v == k*1000)
//   - no panics, no -race report
//
// This is the primary race-detector test: run with `go test -race`.
func TestAll_ConcurrentWritersAndIterator(t *testing.T) {
	assert := newAsserter(t)

	const (
		capacity = 256
		keyspace = 4096
		workers  = 8
		duration = 750 * time.Millisecond
	)

	s := sieve.Must(sieve.New[int, int](capacity))

	// Pre-warm so the iterator has something to chew on immediately.
	for i := 1; i <= capacity/2; i++ {
		s.Add(i, i*1000)
	}

	stop := make(chan struct{})
	var wg sync.WaitGroup

	// Writers
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := 1 + rng.Intn(keyspace)
				switch rng.Intn(4) {
				case 0:
					s.Add(k, k*1000)
				case 1:
					s.Probe(k, k*1000)
				case 2:
					s.Get(k)
				case 3:
					s.Delete(k)
				}
			}
		}(int64(w) + 1)
	}

	// Iterator
	var iterations, yielded uint64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			n := 0
			for k, v := range s.All() {
				// Invariant 1: no zero sentinel — every inserted value is k*1000 with k >= 1.
				if k == 0 || v == 0 {
					t.Errorf("iterator yielded zero sentinel: (%d, %d)", k, v)
					return
				}
				// Invariant 2: (k, v) is internally consistent.
				if v != k*1000 {
					t.Errorf("iterator yielded inconsistent pair: (%d, %d)", k, v)
					return
				}
				n++
				// Bound iteration so we cycle quickly under churn.
				if n > capacity*4 {
					break
				}
			}
			atomic.AddUint64(&yielded, uint64(n))
			atomic.AddUint64(&iterations, 1)
			runtime.Gosched()
		}
	}()

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	assert(atomic.LoadUint64(&iterations) > 0, "iterator never ran")
	t.Logf("iterations=%d total-yielded=%d", iterations, yielded)
}

// TestAll_HighChurn drives extreme eviction pressure (tiny cache, large
// keyspace) while iterating, to maximize the chance of a stale-idx race.
func TestAll_HighChurn(t *testing.T) {
	assert := newAsserter(t)

	const (
		capacity = 32
		keyspace = 8192
		workers  = 8
		duration = 750 * time.Millisecond
	)

	s := sieve.Must(sieve.New[int, int](capacity))

	stop := make(chan struct{})
	var wg sync.WaitGroup

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k := 1 + rng.Intn(keyspace)
				s.Add(k, k*1000)
				if rng.Intn(8) == 0 {
					s.Delete(k)
				}
			}
		}(int64(w) + 100)
	}

	var iterations uint64
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for k, v := range s.All() {
				if k == 0 || v == 0 {
					t.Errorf("high-churn iterator yielded zero sentinel: (%d, %d)", k, v)
					return
				}
				if v != k*1000 {
					t.Errorf("high-churn iterator yielded inconsistent pair: (%d, %d)", k, v)
					return
				}
			}
			atomic.AddUint64(&iterations, 1)
		}
	}()

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	assert(atomic.LoadUint64(&iterations) > 0, "high-churn iterator never completed a pass")
	t.Logf("high-churn iterations=%d", iterations)
}

// TestAll_StringKeyValue exercises the iterator with pointer-typed K/V to
// confirm slot zeroing under remove() doesn't leak into yielded values.
func TestAll_StringKeyValue(t *testing.T) {
	assert := newAsserter(t)

	const (
		capacity = 64
		keyspace = 1024
		workers  = 4
		duration = 500 * time.Millisecond
	)

	s := sieve.Must(sieve.New[string, string](capacity))

	stop := make(chan struct{})
	var wg sync.WaitGroup

	mk := func(i int) (string, string) {
		k := "key-" + itoa(i)
		return k, "val-" + itoa(i)
	}

	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				k, v := mk(1 + rng.Intn(keyspace))
				s.Add(k, v)
			}
		}(int64(w) + 200)
	}

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			for k, v := range s.All() {
				// Invariant: yielded key must not be the empty string
				// (the zero-value sentinel that remove() writes), and
				// value's suffix must match key's suffix.
				if k == "" || v == "" {
					t.Errorf("yielded empty sentinel: (%q, %q)", k, v)
					return
				}
				if k[:4] != "key-" || v[:4] != "val-" || k[4:] != v[4:] {
					t.Errorf("inconsistent pair: (%q, %q)", k, v)
					return
				}
			}
		}
	}()

	time.Sleep(duration)
	close(stop)
	wg.Wait()
	assert(true, "no panic, no race")
}

// itoa is a tiny dependency-free int-to-string for test keys.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
