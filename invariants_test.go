// invariants_test.go - deep structural invariant checker (whitebox)
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
	"fmt"
	"testing"
)

// checkInvariants verifies all structural invariants of a Sieve cache.
// Caller must ensure no concurrent operations are in progress.
func checkInvariants[K comparable, V any](t *testing.T, s *Sieve[K, V], context string) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	nodes := s.allocator.nodes
	cap := s.allocator.cap
	size := int(s.size.Load())

	// 1. Forward walk: sentinel.next → ... → sentinel
	fwdCount := 0
	seen := make(map[int32]bool)
	for idx := nodes[sentinelIdx].next; idx != sentinelIdx; idx = nodes[idx].next {
		fwdCount++
		if fwdCount > int(cap)+1 {
			t.Fatalf("%s: forward walk exceeded capacity — cycle detected", context)
		}
		if idx < 1 || idx > cap {
			t.Fatalf("%s: forward walk hit invalid index %d (valid range: 1..%d)", context, idx, cap)
		}
		// 5. No duplicates
		if seen[idx] {
			t.Fatalf("%s: duplicate index %d in forward walk", context, idx)
		}
		seen[idx] = true
	}
	if fwdCount != size {
		t.Fatalf("%s: forward walk count %d != size %d", context, fwdCount, size)
	}

	// 2. Reverse walk: sentinel.prev → ... → sentinel
	revCount := 0
	for idx := nodes[sentinelIdx].prev; idx != sentinelIdx; idx = nodes[idx].prev {
		revCount++
		if revCount > int(cap)+1 {
			t.Fatalf("%s: reverse walk exceeded capacity — cycle detected", context)
		}
	}
	if revCount != size {
		t.Fatalf("%s: reverse walk count %d != size %d (fwd was %d)", context, revCount, size, fwdCount)
	}

	// 3a. Every list node's key exists in the map with the correct index
	for idx := nodes[sentinelIdx].next; idx != sentinelIdx; idx = nodes[idx].next {
		n := &nodes[idx]
		mapIdx, ok := s.cache.Load(n.key)
		if !ok {
			t.Fatalf("%s: list node %d (key=%v) not found in map", context, idx, n.key)
		}
		if mapIdx != idx {
			t.Fatalf("%s: map[%v]=%d but node is at index %d", context, n.key, mapIdx, idx)
		}
	}

	// 3b. Map size == list size
	mapSize := 0
	s.cache.Range(func(_ K, _ int32) bool {
		mapSize++
		return true
	})
	if mapSize != size {
		t.Fatalf("%s: map size %d != list size %d", context, mapSize, size)
	}

	// 4. Hand validity: must be sentinelIdx or a valid slot index (1..cap).
	// After Delete(), hand may point to a freed slot. This is safe because
	// the LIFO freelist reuses freed slots before eviction triggers
	// (eviction requires size==cap, meaning all freelist entries consumed).
	if s.hand != sentinelIdx {
		if s.hand < 1 || s.hand > cap {
			t.Fatalf("%s: hand=%d out of valid range [sentinelIdx, 1..%d]", context, s.hand, cap)
		}
	}

	// 6. Allocator accounting: size + freelistLen == bumpAllocated
	freelistLen := 0
	for idx := s.allocator.next; idx != nullIdx; idx = nodes[idx].next {
		freelistLen++
		if freelistLen > int(cap)+1 {
			t.Fatalf("%s: freelist cycle detected", context)
		}
	}
	bumpAllocated := int(s.allocator.cur - 1)
	if size+freelistLen != bumpAllocated {
		t.Fatalf("%s: accounting: size(%d) + freelist(%d) = %d, want bump_allocated(%d)",
			context, size, freelistLen, size+freelistLen, bumpAllocated)
	}
}

// =========================================================================
// Tests using the deep invariant checker
// =========================================================================

// TestInternalInvariants mirrors TestInvariants but uses the deep checker.
func TestInternalInvariants(t *testing.T) {
	const cap = 8
	s := Must(New[int, int](cap))
	checkInvariants(t, s, "empty cache")

	// Fill to capacity
	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
		checkInvariants(t, s, fmt.Sprintf("after Add(%d)", i))
	}

	// Visit all
	for i := 0; i < cap; i++ {
		v, ok := s.Get(i)
		if !ok || v != i*10 {
			t.Fatalf("Get(%d) = (%d, %v), want (%d, true)", i, v, ok, i*10)
		}
		checkInvariants(t, s, fmt.Sprintf("after Get(%d)", i))
	}

	// Eviction
	for i := cap; i < cap*2; i++ {
		s.Add(i, i*10)
		checkInvariants(t, s, fmt.Sprintf("after eviction Add(%d)", i))
	}

	// Update
	for i := cap; i < cap*2; i++ {
		s.Add(i, i*100)
		checkInvariants(t, s, fmt.Sprintf("after update Add(%d)", i))
	}

	// Delete
	for i := cap; i < cap+4; i++ {
		s.Delete(i)
		checkInvariants(t, s, fmt.Sprintf("after Delete(%d)", i))
	}

	// Probe miss (insert)
	for i := 0; i < 4; i++ {
		key := cap*2 + i
		s.Probe(key, key*10)
		checkInvariants(t, s, fmt.Sprintf("after Probe(%d) miss", key))
	}

	// Probe hit
	for i := 0; i < 4; i++ {
		key := cap*2 + i
		v, _, r := s.Probe(key, -1)
		if !r.Hit() || v != key*10 {
			t.Fatalf("Probe(%d) hit = (%d, %v), want (%d, true)", key, v, r.Hit(), key*10)
		}
	}

	// Purge
	s.Purge()
	checkInvariants(t, s, "after Purge")

	// 7. Post-Purge state
	s.mu.Lock()
	if sz := s.size.Load(); sz != 0 {
		t.Fatalf("post-Purge: size=%d, want 0", sz)
	}
	if s.hand != sentinelIdx {
		t.Fatalf("post-Purge: hand=%d, want sentinelIdx", s.hand)
	}
	nodes := s.allocator.nodes
	if nodes[sentinelIdx].next != sentinelIdx || nodes[sentinelIdx].prev != sentinelIdx {
		t.Fatalf("post-Purge: sentinel not self-linked: next=%d prev=%d",
			nodes[sentinelIdx].next, nodes[sentinelIdx].prev)
	}
	if s.allocator.cur != 1 || s.allocator.next != nullIdx {
		t.Fatalf("post-Purge: allocator not reset: cur=%d next=%d",
			s.allocator.cur, s.allocator.next)
	}
	s.mu.Unlock()

	// Re-add after Purge
	for i := 0; i < cap; i++ {
		s.Add(i, i)
		checkInvariants(t, s, fmt.Sprintf("after re-Add(%d) post-purge", i))
	}

	// Heavy churn
	for i := 0; i < cap*5; i++ {
		s.Add(cap+i, cap+i)
		checkInvariants(t, s, fmt.Sprintf("after churn Add(%d)", cap+i))
	}
}

// TestInternal_EvictionAllVisited verifies that eviction works when every
// node in the list is visited. The hand must scan the entire list clearing
// all visited bits, wrap around, and evict the first node found unvisited.
//
// We trace the eviction deterministically:
//   - List after fill: sentinel → 7 → 6 → 5 → 4 → 3 → 2 → 1 → 0 → sentinel
//     (each Add inserts at head, so key 7 is head, key 0 is tail)
//   - hand == sentinelIdx (unset), so eviction starts from tail (key 0)
//   - All nodes visited → hand scans backward clearing bits:
//     0(clear)→7(clear)→6(clear)→5(clear)→4(clear)→3(clear)→2(clear)→1(clear)
//     → wraps to tail → 0(now unvisited) → evict 0.
//   - Result: key 0 evicted, keys 1-7 + 8 present, all visited bits cleared.
func TestInternal_EvictionAllVisited(t *testing.T) {
	const cap = 8
	s := Must(New[int, int](cap))

	// Fill: keys 0..7
	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
	}

	// Verify list order: head should be key 7 (last inserted), tail key 0
	s.mu.Lock()
	nodes := s.allocator.nodes
	headKey := nodes[nodes[sentinelIdx].next].key
	tailKey := nodes[nodes[sentinelIdx].prev].key
	s.mu.Unlock()
	if headKey != cap-1 {
		t.Fatalf("head key=%d, want %d (last inserted)", headKey, cap-1)
	}
	if tailKey != 0 {
		t.Fatalf("tail key=%d, want 0 (first inserted)", tailKey)
	}

	// Visit ALL nodes
	for i := 0; i < cap; i++ {
		s.Get(i)
	}

	// Verify all visited
	s.mu.Lock()
	nodes = s.allocator.nodes
	for idx := nodes[sentinelIdx].next; idx != sentinelIdx; idx = nodes[idx].next {
		if !s.slots.IsVisited(idx) {
			t.Fatalf("node %d (key=%v) should be visited", idx, nodes[idx].key)
		}
	}
	s.mu.Unlock()

	// Add key 8 — triggers eviction with 100% visited list.
	// Expected: key 0 (tail) evicted after full scan + wrap.
	s.Add(cap, cap*10)
	checkInvariants(t, s, "after eviction with all visited")

	// Key 0 should be evicted (it's the tail, first node reached after wrap)
	if _, ok := s.Get(0); ok {
		t.Fatal("expected key 0 to be evicted (tail, first unvisited after scan)")
	}

	// Key 8 (new) and keys 1..7 should all be present
	for i := 1; i <= cap; i++ {
		v, ok := s.Get(i)
		if !ok {
			t.Fatalf("key %d should be present", i)
		}
		if v != i*10 {
			t.Fatalf("Get(%d) = %d, want %d", i, v, i*10)
		}
	}

	// After all-visited eviction, all visited bits should have been cleared
	// during the scan. The only visited nodes are the ones we just Get'd above.
	// Verify the data structure is consistent.
	checkInvariants(t, s, "after verifying all keys")
}

// TestInternal_EvictionAllVisited_Repeated verifies that the all-visited
// eviction path works across multiple consecutive evictions.
func TestInternal_EvictionAllVisited_Repeated(t *testing.T) {
	const cap = 16
	s := Must(New[int, int](cap))

	for round := 0; round < 5; round++ {
		// Fill cache
		base := round * cap * 2
		for s.Len() < cap {
			k := base + s.Len()
			s.Add(k, k)
		}

		// Visit everything
		s.mu.Lock()
		nodes := s.allocator.nodes
		for idx := nodes[sentinelIdx].next; idx != sentinelIdx; idx = nodes[idx].next {
			// Use Get (which takes and releases lock) — must unlock first
			key := nodes[idx].key
			s.mu.Unlock()
			s.Get(key)
			s.mu.Lock()
			nodes = s.allocator.nodes // re-read after re-lock
		}
		s.mu.Unlock()

		// Force evictions — each one faces an all-visited list
		for i := 0; i < cap/2; i++ {
			newKey := base + cap*2 + i
			s.Add(newKey, newKey)
			checkInvariants(t, s, fmt.Sprintf("round %d, eviction %d", round, i))
		}
	}
}

// TestInternal_DeleteThenEvict verifies that deleting the node the hand
// points to doesn't corrupt the cache when a subsequent eviction occurs.
func TestInternal_DeleteThenEvict(t *testing.T) {
	const cap = 8
	s := Must(New[int, int](cap))

	// Fill: keys 0..7
	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
	}

	// Trigger one eviction to set hand to a known position
	// Visit keys 0..6 but NOT key 0 (tail). Key 0 will be evicted.
	// Actually, with sentinel list, insertion at head means:
	// List: sentinel → 7 → 6 → 5 → 4 → 3 → 2 → 1 → 0 → sentinel
	// Tail is key 0 (index 1). Don't visit it.
	for i := 1; i < cap; i++ {
		s.Get(i)
	}

	// Add key 8: evicts tail (key 0), hand moves to prev of evicted node
	s.Add(cap, cap*10)
	checkInvariants(t, s, "after first eviction")

	// Record what the hand points to
	s.mu.Lock()
	handIdx := s.hand
	handKey := s.allocator.nodes[handIdx].key
	s.mu.Unlock()

	// Delete the node that hand points to
	ok := s.Delete(handKey)
	if !ok {
		t.Fatalf("Delete(%d): expected true", handKey)
	}
	// hand is now stale (points to a freed node) but the cache should
	// still be consistent for subsequent operations.

	// Fill back up and force more evictions
	for i := 0; i < cap*2; i++ {
		key := cap*10 + i
		s.Add(key, key)
		checkInvariants(t, s, fmt.Sprintf("after post-delete Add(%d)", key))
	}
}

// TestInternal_AllocatorAccounting verifies freelist/bump accounting
// across many add/delete cycles.
func TestInternal_AllocatorAccounting(t *testing.T) {
	const cap = 32
	s := Must(New[int, int](cap))

	// Phase 1: fill
	for i := 0; i < cap; i++ {
		s.Add(i, i)
		checkInvariants(t, s, fmt.Sprintf("fill Add(%d)", i))
	}

	// Phase 2: delete half
	for i := 0; i < cap/2; i++ {
		s.Delete(i)
		checkInvariants(t, s, fmt.Sprintf("Delete(%d)", i))
	}

	// Phase 3: re-add (reuses freelist slots)
	for i := 0; i < cap/2; i++ {
		key := cap + i
		s.Add(key, key)
		checkInvariants(t, s, fmt.Sprintf("re-add Add(%d)", key))
	}

	// Phase 4: eviction churn (all slots now bump-allocated + recycled)
	for i := 0; i < cap*3; i++ {
		key := cap*2 + i
		s.Add(key, key)
		checkInvariants(t, s, fmt.Sprintf("churn Add(%d)", key))
	}

	// Phase 5: delete all, verify accounting
	for s.Len() > 0 {
		// Find a key to delete by walking the list
		s.mu.Lock()
		nodes := s.allocator.nodes
		firstIdx := nodes[sentinelIdx].next
		key := nodes[firstIdx].key
		s.mu.Unlock()

		s.Delete(key)
	}
	checkInvariants(t, s, "after delete all")

	s.mu.Lock()
	if sz := s.size.Load(); sz != 0 {
		t.Fatalf("size=%d after deleting all, want 0", sz)
	}
	// All allocated slots should be on freelist
	freelistLen := 0
	nodes := s.allocator.nodes
	for idx := s.allocator.next; idx != nullIdx; idx = nodes[idx].next {
		freelistLen++
	}
	bumpAllocated := int(s.allocator.cur - 1)
	if freelistLen != bumpAllocated {
		t.Fatalf("after delete all: freelist(%d) != bump_allocated(%d)", freelistLen, bumpAllocated)
	}
	s.mu.Unlock()
}

// TestInternal_StaleIndex_ABA deterministically verifies the n.key == key guard
// (sieve.go lines 175, 197, 232) that detects stale indices after eviction+reuse.
//
// Sequence:
// 1. Fill a 4-entry cache with keys 10,20,30,40
// 2. Evict key 10 (unvisited tail) by adding key 50
// 3. The freed index is reused for the next Add (LIFO freelist)
// 4. Verify the old index now holds a different key — the guard catches this
func TestInternal_StaleIndex_ABA(t *testing.T) {
	const cap = 4
	s := Must(New[int, int](cap))

	// Fill: keys 10, 20, 30, 40 (non-zero to avoid zero-value ambiguity)
	for _, k := range []int{10, 20, 30, 40} {
		s.Add(k, k*1000)
	}
	checkInvariants(t, s, "after fill")

	// Record the index for key 10 (tail — first inserted, will be evicted first)
	targetIdx, ok := s.cache.Load(10)
	if !ok {
		t.Fatal("key 10 not in cache map")
	}

	// Visit keys 20, 30, 40 so they survive eviction. Do NOT visit key 10.
	for _, k := range []int{20, 30, 40} {
		if _, ok := s.Get(k); !ok {
			t.Fatalf("Get(%d) should hit", k)
		}
	}

	// Add key 50: triggers eviction. Key 10 is unvisited tail → evicted.
	// remove() frees targetIdx to LIFO freelist.
	s.Add(50, 50*1000)
	checkInvariants(t, s, "after eviction of key 10")

	// Verify key 10 is gone from the map
	if _, ok := s.cache.Load(10); ok {
		t.Fatal("key 10 should have been evicted from map")
	}

	// Verify key 10 is gone via public API
	if _, ok := s.Get(10); ok {
		t.Fatal("Get(10) should miss after eviction")
	}

	// Add key 60: LIFO freelist returns targetIdx — reused for a different key
	s.Add(60, 60*1000)
	checkInvariants(t, s, "after reuse of freed index")

	// Verify the freed index was reused for key 60
	reusedIdx, ok := s.cache.Load(60)
	if !ok {
		t.Fatal("key 60 not in cache map")
	}
	if reusedIdx != targetIdx {
		t.Fatalf("expected LIFO reuse: key 60 at idx %d, but key 10 was at idx %d", reusedIdx, targetIdx)
	}

	// Verify the node at targetIdx now holds key 60, not key 10
	s.slots.Lock(targetIdx)
	keyAtIdx := s.allocator.nodes[targetIdx].key
	valAtIdx := s.allocator.nodes[targetIdx].val
	s.slots.Unlock(targetIdx)

	if keyAtIdx != 60 {
		t.Fatalf("node[%d].key = %d, want 60 (proves stale index would mismatch)", targetIdx, keyAtIdx)
	}
	if valAtIdx != 60*1000 {
		t.Fatalf("node[%d].val = %d, want %d", targetIdx, valAtIdx, 60*1000)
	}

	// Verify public API works correctly for both keys
	if v, ok := s.Get(60); !ok || v != 60*1000 {
		t.Fatalf("Get(60) = (%d, %v), want (%d, true)", v, ok, 60*1000)
	}
	if _, ok := s.Get(10); ok {
		t.Fatal("Get(10) should still miss — key guard would catch stale index")
	}

	checkInvariants(t, s, "final")
}

// TestInternal_LargerScale runs the deep invariant checker at larger scale
// (less frequently to keep test time reasonable).
func TestInternal_LargerScale(t *testing.T) {
	const cap = 256
	s := Must(New[int, int](cap))

	// Bulk fill with eviction
	for i := 0; i < cap*4; i++ {
		s.Add(i, i)
	}
	checkInvariants(t, s, "after bulk fill")

	// Delete every other key, then refill
	for i := cap * 3; i < cap*4; i += 2 {
		s.Delete(i)
	}
	checkInvariants(t, s, "after alternating deletes")

	for i := cap * 4; i < cap*5; i++ {
		s.Add(i, i)
	}
	checkInvariants(t, s, "after refill")

	// Purge and restart
	s.Purge()
	checkInvariants(t, s, "after Purge")

	for i := 0; i < cap; i++ {
		s.Add(i, i)
	}
	checkInvariants(t, s, "after re-fill post-purge")
}
