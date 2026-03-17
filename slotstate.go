// slotstate.go - combined per-node lock + visited counter in a single []uint64
//
// Each slot (one uint64 per node index) holds:
//
//	Bit 63:              exclusive spinlock
//	Bits [vbits-1 : 0]:  visited counter (saturates at maxVisit)
//
// For k=1 (classic SIEVE): 1 bit for visited, lock in bit 63.
// For k=3: 2 bits for counter (saturates at 3), lock in bit 63.
//
// This replaces the separate spinlock + visitor arrays with a single
// allocation, saving one cache miss per Get() on the read path.
//
// Memory: 8 bytes per node. At 1M entries: 8 MB.
//   - vs prior design ([]uint64 spinlock + []uint32 visitor): 12 MB.
//
// Lock uses atomic.OrUint64 as test-and-set (returns old value).
// Unlock uses atomic.AndUint64 to clear the lock bit.
// Both are single instructions on ARM64 with LSE (LDSETAL / LDCLRAL).
//
// Hot path (Get, k=1):
//
//	LockAndMark: 1 Or   (sets lock + visited bits, returns old to check)
//	read val
//	Unlock:      1 And  (clears lock bit, visitor bit untouched)
//
// Two atomic ops total, down from three (CAS + Store + Or/Store).

package sieve

import (
	"math/bits"
	"sync/atomic"
)

const _LockBit = uint64(1) << 63

// slotState manages per-node lock + visited counter in a single []uint64.
type slotState struct {
	words    []uint64
	maxVisit uint64 // k (saturation value)
	vmask    uint64 // (1 << vbits) - 1
	vbits    uint   // ceil(log2(k+1)) — number of bits per visitor counter
}

// newSlotState creates a slotState for the given capacity and visit level k.
// k=1 uses a single visited bit. k>1 uses ceil(log2(k+1)) bits for a
// saturating counter.
func newSlotState(capacity int, k int) slotState {
	if k < 1 {
		k = 1
	}
	vb := uint(bits.Len(uint(k))) // k=1→1, k=2..3→2, k=4..7→3
	return slotState{
		words:    make([]uint64, capacity),
		maxVisit: uint64(k),
		vmask:    (1 << vb) - 1,
		vbits:    vb,
	}
}

// LockAndMark acquires the exclusive lock and marks the node as visited.
//
// For k=1: single atomic.OrUint64 sets both lock bit and visited bit.
// Or is idempotent on the visited bit and returns the old value to test
// whether we acquired the lock. No CAS, no spurious retries from
// concurrent visitor bit changes.
//
// For k>1: acquires lock via Or, then saturating-increments the counter
// via CAS (only the CAS can fail spuriously, and only from concurrent
// Clear on the same word — probability 1/N).
func (ss *slotState) LockAndMark(idx int32) {
	word := &ss.words[idx]

	if ss.vbits == 1 {
		// k=1 fast path: Or sets both lock and visited bits.
		// One atomic instruction on ARM64 with LSE (LDSETAL).
		for i := 0; ; i++ {
			old := atomic.OrUint64(word, _LockBit|1)
			if old&_LockBit == 0 {
				return // we set the lock bit 0→1
			}
			pause(i)
		}
	}

	// k>1: two-step — lock via Or, then saturating increment via CAS.
	for i := 0; ; i++ {
		old := atomic.OrUint64(word, _LockBit)
		if old&_LockBit == 0 {
			break // lock acquired
		}
		pause(i)
	}
	// We hold the lock. Increment visitor counter (saturating).
	for i := 0; ; i++ {
		old := atomic.LoadUint64(word)
		if old&ss.vmask >= ss.maxVisit {
			return // saturated
		}
		if atomic.CompareAndSwapUint64(word, old, old+1) {
			return
		}
		pause(i)
	}
}

// Lock acquires the exclusive lock without marking visited.
// Used by remove() to serialize field zeroing with fast-path reads.
func (ss *slotState) Lock(idx int32) {
	word := &ss.words[idx]
	for i := 0; ; i++ {
		old := atomic.OrUint64(word, _LockBit)
		if old&_LockBit == 0 {
			return
		}
		pause(i)
	}
}

// LockAndReset acquires the exclusive lock and clears the visited counter.
// Used by newNode() when initializing a freshly allocated slot. Unlike
// Reset()+Lock(), this is safe against concurrent holders: it spins until
// the lock is acquired, then zeroes the visited bits while holding it.
func (ss *slotState) LockAndReset(idx int32) {
	word := &ss.words[idx]
	for i := 0; ; i++ {
		old := atomic.OrUint64(word, _LockBit)
		if old&_LockBit == 0 {
			// Lock acquired. Clear visited bits, keep lock.
			atomic.StoreUint64(word, _LockBit)
			return
		}
		pause(i)
	}
}

// Unlock releases the exclusive lock, leaving visitor bits intact.
// Single atomic instruction on ARM64 with LSE (LDCLRAL).
func (ss *slotState) Unlock(idx int32) {
	atomic.AndUint64(&ss.words[idx], ^_LockBit)
}

// IsVisited returns true if the visited counter is > 0.
// Single atomic load — no contention.
func (ss *slotState) IsVisited(idx int32) bool {
	return atomic.LoadUint64(&ss.words[idx])&ss.vmask != 0
}

// Clear decrements the visited counter, saturating at 0.
// Called during eviction under s.mu. A concurrent Get() may hold the
// lock on the same word, so we use CAS.
func (ss *slotState) Clear(idx int32) {
	word := &ss.words[idx]
	for i := 0; ; i++ {
		old := atomic.LoadUint64(word)
		if old&ss.vmask == 0 {
			return // already zero
		}
		if atomic.CompareAndSwapUint64(word, old, old-1) {
			return
		}
		pause(i)
	}
}

// Reset zeroes the entire slot (lock + visitor). Called from newNode()
// under s.mu when a node is freshly allocated — no concurrent access.
func (ss *slotState) Reset(idx int32) {
	atomic.StoreUint64(&ss.words[idx], 0)
}

// ResetAll zeroes all slots. Called from Purge() under s.mu.
// Uses atomic stores to avoid data races with concurrent Get() calls
// that may be in LockAndMark on the same word.
func (ss *slotState) ResetAll() {
	for i := range ss.words {
		atomic.StoreUint64(&ss.words[i], 0)
	}
}
