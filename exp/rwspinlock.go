// rwspinlock.go - high-performance reader-writer spinlock
//
// One uint64 per lock eliminates intra-word CAS contention that plagued
// the packed 8-slot design (8 independent locks serialized on the same
// atomic address). With 1M entries: 8 MB vs 1 MB — negligible compared
// to cached data.
//
// Bit layout per uint64:
//   Bit 63:    writer exclusive flag
//   Bits 0-62: reader count (max ~4.6×10¹⁸ — overflow impossible)
//
// Design:
//   RLock:   optimistic atomic Add — single atomic op on uncontended path.
//            If writer present: undo Add, spin, retry.
//   RUnlock: unconditional atomic Add (subtract 1). One op, no loop.
//   Lock:    two-phase acquire:
//            (1) atomic.OrUint64 to claim writer bit (one op, no CAS loop)
//            (2) spin until reader count drains to zero
//            New readers see writer bit and back off → no writer starvation.
//   Unlock:  atomic.AndUint64 to clear writer bit. One op.
//
// Spin strategy (matches Go runtime sync.Mutex tuning):
//   First 4 iterations:  runtime.procyield(30) — PAUSE on x86, YIELD on arm64
//   Then:                runtime.Gosched() — deschedule goroutine
//   For nanosecond critical sections (value update in cache slot), the lock
//   holder almost always releases during the PAUSE phase.

package sieve

import (
	"sync/atomic"
)

const (
	_WriterBit  = uint64(1) << 63
	_ReaderMask = _WriterBit - 1 // bits 0-62
)

// rwSpinlock is an array of reader-writer spinlocks, one uint64 per slot.
type rwSpinlock struct {
	words []uint64
}

// newRWSpinlock creates an RW spinlock array for the given capacity.
func newRWSpinlock(capacity int) *rwSpinlock {
	return &rwSpinlock{
		words: make([]uint64, capacity),
	}
}

// RLock acquires a shared read lock on slot idx.
//
// Uncontended fast path: one atomic.AddUint64 — no Load+CAS loop.
// The Add cannot overflow into bit 63 (would require 2⁶³ concurrent readers).
func (rw *rwSpinlock) RLock(idx int32) {
	word := &rw.words[idx]
	for i := 0; ; i++ {
		// Optimistic: bump reader count.
		nv := atomic.AddUint64(word, 1)
		if nv&_WriterBit == 0 {
			return // no writer — lock acquired
		}
		// Writer present: undo our increment and spin.
		// The writer will see readers drain and complete quickly for
		// nanosecond critical sections.
		atomic.AddUint64(word, ^uint64(0)) // subtract 1
		pause(i)
	}
}

// RUnlock releases a shared read lock on slot idx.
// Single atomic op, no loop.
func (rw *rwSpinlock) RUnlock(idx int32) {
	// Subtract 1 from reader count. Two's complement: ^uint64(0) == -1.
	// Safe: caller holds lock so reader count > 0; no underflow into adjacent
	// bits. Writer bit (63) is unaffected because no borrow propagates from
	// bit 62 when count > 0.
	atomic.AddUint64(&rw.words[idx], ^uint64(0))
}

// Lock acquires an exclusive write lock on slot idx.
//
// Two-phase design:
//
//	Phase 1: Claim writer bit via atomic.OrUint64 (returns old value).
//	         One atomic op on uncontended path — no Load+CAS loop.
//	Phase 2: Wait for pre-existing readers to drain.
//	         New readers see writer bit and back off, so only in-flight
//	         readers must finish — bounded by critical section duration.
func (rw *rwSpinlock) Lock(idx int32) {
	word := &rw.words[idx]

	// Phase 1: claim the writer bit.
	for i := 0; ; i++ {
		old := atomic.OrUint64(word, _WriterBit)
		if old&_WriterBit == 0 {
			break // we transitioned the bit 0→1
		}
		// Another writer holds it. Our Or was a no-op (bit already set).
		pause(i)
	}

	// Phase 2: drain pre-existing readers.
	for i := 0; atomic.LoadUint64(word)&_ReaderMask != 0; i++ {
		pause(i)
	}
}

// Unlock releases an exclusive write lock on slot idx.
// Single atomic op.
func (rw *rwSpinlock) Unlock(idx int32) {
	atomic.AndUint64(&rw.words[idx], ^_WriterBit)
}

// ResetAll clears all lock state. Must be called with external synchronization
// (e.g., the caller holds s.mu in Purge).
func (rw *rwSpinlock) ResetAll() {
	for i := range rw.words {
		atomic.StoreUint64(&rw.words[i], 0)
	}
}
