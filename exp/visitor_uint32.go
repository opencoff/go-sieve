// visitor_uint32.go - per-slot uint32 visitor implementations
//
// Two concrete types, zero branches on the hot path:
//
//   visitedBool (k=1):  slot is 0 or 1. Mark/Clear are plain stores.
//   visitedCounter (k>1): slot is a saturating counter 0..k. CAS loops.
//
// Both use one uint32 per node (16 entries per cache line), reducing
// false sharing vs the packed bitfield (512 entries per cache line).
// For 1M entries: 4 MB vs 16 KB.
//
// The visitor interface dispatches at construction time (New vs
// NewWithVisits), so the hot path is a direct method call with no
// runtime branching.

package sieve

import "sync/atomic"

// visitor is the legacy interface for tracking visited status of cache nodes.
// Retained for the standalone visitedBool/visitedCounter types and their tests.
// The Sieve struct itself uses slotState (combined lock+visitor).
type visitor interface {
	Mark(idx int32)
	Clear(idx int32)
	Reset(idx int32)
	IsVisited(idx int32) bool
	ResetAll()
}

// --- k=1: boolean visited flag ---

// visitedBool tracks visited status with one uint32 per node (0 or 1).
// All operations are branchless on the hot path (single atomic op).
type visitedBool struct {
	slots []uint32
}

var _ visitor = &visitedBool{}

func newVisitedBool(capacity int) *visitedBool {
	return &visitedBool{
		slots: make([]uint32, capacity),
	}
}

// Mark sets the visited flag. Fast-path load skips the store if already set.
func (v *visitedBool) Mark(idx int32) {
	if atomic.LoadUint32(&v.slots[idx]) != 0 {
		return
	}
	atomic.StoreUint32(&v.slots[idx], 1)
}

// Clear clears the visited flag. Single store, no CAS.
func (v *visitedBool) Clear(idx int32) {
	atomic.StoreUint32(&v.slots[idx], 0)
}

// Reset clears the visited flag (same as Clear for k=1).
func (v *visitedBool) Reset(idx int32) {
	atomic.StoreUint32(&v.slots[idx], 0)
}

// IsVisited returns true if the visited flag is set. Single load.
func (v *visitedBool) IsVisited(idx int32) bool {
	return atomic.LoadUint32(&v.slots[idx]) != 0
}

// ResetAll clears all flags. Called from Purge() under s.mu.
func (v *visitedBool) ResetAll() {
	for i := range v.slots {
		v.slots[i] = 0
	}
}

// --- k>1: saturating counter ---

// visitedCounter tracks visited status with a saturating counter per node.
// Counter values range from 0 to maxVal (= k). Mark increments, Clear
// decrements, both saturate at the boundary.
type visitedCounter struct {
	slots  []uint32
	maxVal uint32
}

var _ visitor = &visitedCounter{}

func newVisitedCounter(capacity int, k int) *visitedCounter {
	return &visitedCounter{
		slots:  make([]uint32, capacity),
		maxVal: uint32(k),
	}
}

// Mark increments the counter, saturating at maxVal.
func (v *visitedCounter) Mark(idx int32) {
	w := &v.slots[idx]
	for {
		old := atomic.LoadUint32(w)
		if old >= v.maxVal {
			return
		}
		if atomic.CompareAndSwapUint32(w, old, old+1) {
			return
		}
	}
}

// Clear decrements the counter, saturating at 0.
func (v *visitedCounter) Clear(idx int32) {
	w := &v.slots[idx]
	for {
		old := atomic.LoadUint32(w)
		if old == 0 {
			return
		}
		if atomic.CompareAndSwapUint32(w, old, old-1) {
			return
		}
	}
}

// Reset sets the counter to 0.
func (v *visitedCounter) Reset(idx int32) {
	atomic.StoreUint32(&v.slots[idx], 0)
}

// IsVisited returns true if the counter is > 0.
func (v *visitedCounter) IsVisited(idx int32) bool {
	return atomic.LoadUint32(&v.slots[idx]) != 0
}

// ResetAll clears all counters. Called from Purge() under s.mu.
func (v *visitedCounter) ResetAll() {
	for i := range v.slots {
		v.slots[i] = 0
	}
}
