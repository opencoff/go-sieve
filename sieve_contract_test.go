// sieve_contract_test.go - construction error paths, hand fidelity after
// Delete, and eviction progress under sustained read load (whitebox)
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

package sieve

import (
	"errors"
	"sync"
	"testing"
	"time"
)

// TestNew_Errors exercises every construction error path.
func TestNew_Errors(t *testing.T) {
	cases := []struct {
		name    string
		cap     int
		opts    []Option
		wantErr error
	}{
		{"zero capacity", 0, nil, ErrInvalidCapacity},
		{"negative capacity", -1, nil, ErrInvalidCapacity},
		{"capacity above max", MaxCapacity + 1, nil, ErrInvalidCapacity},
		{"clamp above max", 8, []Option{WithVisitClamp(MaxVisitClamp + 1)}, ErrInvalidVisitClamp},
		{"clamp far above max", 8, []Option{WithVisitClamp(255)}, ErrInvalidVisitClamp},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New[int, int](tc.cap, tc.opts...)
			if s != nil {
				t.Fatalf("expected nil cache, got %v", s)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestNew_ValidBoundaries exercises the accepted edges of the input range.
func TestNew_ValidBoundaries(t *testing.T) {
	if _, err := New[int, int](1); err != nil {
		t.Fatalf("capacity 1: unexpected error %v", err)
	}
	if _, err := New[int, int](8, WithVisitClamp(MaxVisitClamp)); err != nil {
		t.Fatalf("clamp at MaxVisitClamp: unexpected error %v", err)
	}
	// Values below 1 are silently rounded up to 1.
	if _, err := New[int, int](8, WithVisitClamp(0)); err != nil {
		t.Fatalf("clamp 0: unexpected error %v", err)
	}
	if _, err := New[int, int](8, WithVisitClamp(-3)); err != nil {
		t.Fatalf("clamp -3: unexpected error %v", err)
	}
}

// TestMust_PanicsOnError verifies the Must helper contract.
func TestMust_PanicsOnError(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("Must did not panic on construction error")
		}
	}()
	Must(New[int, int](0))
}

// TestInternal_HandFidelity_DeleteHand verifies that Delete() of the node
// the eviction hand points to advances the hand to its predecessor instead
// of leaving it dangling on a freed slot. Without the fix, the freed slot
// is reused at the head of the list and the hand teleports there, evicting
// the newest entry instead of continuing the scan from where it stopped.
//
// Keys are 1-based: the zero key is reserved (see Sieve doc).
func TestInternal_HandFidelity_DeleteHand(t *testing.T) {
	const capacity = 4
	s := Must(New[int, int](capacity))

	// Fill: list head→tail is 4,3,2,1.
	for i := 1; i <= capacity; i++ {
		s.Add(i, i*10)
	}

	// First eviction: nothing visited, so the tail (key 1) is evicted and
	// the hand lands on key 2's node. Key 5 reuses the freed slot at head.
	ev, r := s.Add(5, 50)
	if !r.Evicted() || ev.Key != 1 {
		t.Fatalf("expected eviction of key 1, got evicted=%v key=%v", r.Evicted(), ev.Key)
	}
	if s.hand == sentinelIdx {
		t.Fatal("hand not set after eviction")
	}
	handKey := s.allocator.nodes[s.hand].key
	if handKey != 2 {
		t.Fatalf("expected hand on key 2, got key %v", handKey)
	}

	// Delete the hand node. The hand must advance to key 2's predecessor
	// (key 3) rather than dangle on the freed slot.
	if !s.Delete(2) {
		t.Fatal("Delete(2) failed")
	}
	checkInvariants(t, s, "after Delete of hand node")
	if got := s.allocator.nodes[s.hand].key; got != 3 {
		t.Fatalf("expected hand on key 3 after Delete of hand node, got key %v", got)
	}

	// Refill (key 6 reuses key 2's slot, inserted at head) and force an
	// eviction. The scan must continue at key 3 — not at the newest entry.
	s.Add(6, 60)
	ev, r = s.Add(7, 70)
	if !r.Evicted() {
		t.Fatal("expected eviction when adding key 7")
	}
	if ev.Key != 3 {
		t.Fatalf("expected eviction of key 3 (hand position), got key %v", ev.Key)
	}
	if _, ok := s.Get(6); !ok {
		t.Fatal("newest entry (key 6) was evicted — hand teleported to head")
	}
	checkInvariants(t, s, "after eviction past deleted hand")
}

// TestEvictionProgress_UnderGetLoad verifies that the eviction scan makes
// progress while concurrent lock-free Gets continuously re-mark visited
// bits behind the hand (theoretical livelock scenario).
func TestEvictionProgress_UnderGetLoad(t *testing.T) {
	const (
		capacity = 64
		readers  = 8
		adds     = 5000
	)
	s := Must(New[int, int](capacity))
	for i := 1; i <= capacity; i++ {
		s.Add(i, i)
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				for k := 1; k <= capacity; k++ {
					s.Get(k)
				}
			}
		}()
	}

	start := time.Now()
	deadline := start.Add(30 * time.Second)
	for i := 0; i < adds; i++ {
		s.Add(capacity+1+i, i)
		if time.Now().After(deadline) {
			close(done)
			wg.Wait()
			t.Fatalf("eviction stalled: only %d/%d Adds completed in 30s", i, adds)
		}
	}
	close(done)
	wg.Wait()

	if got := s.Len(); got != capacity {
		t.Fatalf("expected size %d after churn, got %d", capacity, got)
	}
}
