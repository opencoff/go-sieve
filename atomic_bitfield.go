// visited_bits.go - packed visited bitfield using atomic CAS
//
// Replaces per-node atomic.Bool with a shared []uint64 bitfield.
// For a 1M-entry cache, this uses 16KB instead of 4MB.
//
// Set/Clear use CAS loops with early exit (idempotent).
// Test is a single atomic load — zero contention on the read path.

package sieve

import "sync/atomic"

// atomicBitfield is a packed bitfield for tracking visited status of cache nodes.
// Each bit corresponds to one node index. Thread-safe via atomic operations.
type atomicBitfield struct {
	words []uint64
}

// newAtomicBitfield creates a atomicBitfield with enough words for capacity bits.
func newAtomicBitfield(capacity int) atomicBitfield {
	nwords := (capacity + 63) / 64
	return atomicBitfield{
		words: make([]uint64, nwords),
	}
}

// Set sets the visited bit for node at idx. CAS loop with early exit if already set.
func (vb *atomicBitfield) Set(idx int32) {
	word := idx / 64
	bit := uint64(1) << (idx % 64)
	for {
		old := atomic.LoadUint64(&vb.words[word])
		if old&bit != 0 {
			return // already set
		}
		if atomic.CompareAndSwapUint64(&vb.words[word], old, old|bit) {
			return
		}
	}
}

// Clear clears the visited bit for node at idx. CAS loop with early exit if already clear.
func (vb *atomicBitfield) Clear(idx int32) {
	word := idx / 64
	bit := uint64(1) << (idx % 64)
	for {
		old := atomic.LoadUint64(&vb.words[word])
		if old&bit == 0 {
			return // already clear
		}
		if atomic.CompareAndSwapUint64(&vb.words[word], old, old&^bit) {
			return
		}
	}
}

// Test returns true if the visited bit for node at idx is set.
// Single atomic load — no CAS, no contention.
func (vb *atomicBitfield) Test(idx int32) bool {
	word := idx / 64
	bit := uint64(1) << (idx % 64)
	return atomic.LoadUint64(&vb.words[word])&bit != 0
}

// Reset clears all visited bits. Called from Purge() under s.mu — plain stores are fine.
func (vb *atomicBitfield) Reset() {
	for i := range vb.words {
		vb.words[i] = 0
	}
}
