// atomic_bitfield.go - packed visited bitfield using atomic bit ops
//
// Replaces per-node atomic.Bool with a shared []uint64 bitfield.
// For a 1M-entry cache, this uses 16KB instead of 4MB.
//
// Mark/Clear use atomic.OrUint64/AndUint64 (single locked instruction,
// no CAS retry loop) with a fast-path load check to skip the locked
// instruction when the bit is already in the desired state.
// IsVisited is a single atomic load — zero contention on the read path.

package sieve

import "sync/atomic"

// atomicBitfield is a packed bitfield for tracking visited status of cache nodes.
// Each bit corresponds to one node index. Thread-safe via atomic operations.
type atomicBitfield struct {
	words []uint64
}

var _ visitor = &atomicBitfield{}

// newAtomicBitfield creates an atomicBitfield with enough words for capacity bits.
func newAtomicBitfield(capacity int) *atomicBitfield {
	nwords := (capacity + 63) / 64
	return &atomicBitfield{
		words: make([]uint64, nwords),
	}
}

// Mark sets the visited bit for node at idx.
func (vb *atomicBitfield) Mark(idx int32) {
	w := idx / 64
	word := &vb.words[w]
	bit := uint64(1) << (idx % 64)
	if atomic.LoadUint64(word)&bit != 0 {
		return // already set — skip the locked instruction
	}
	atomic.OrUint64(word, bit)
}

// Clear clears the visited bit for node at idx.
func (vb *atomicBitfield) Clear(idx int32) {
	w := idx / 64
	word := &vb.words[w]
	bit := uint64(1) << (idx % 64)
	if atomic.LoadUint64(word)&bit == 0 {
		return
	}
	atomic.AndUint64(word, ^bit)
}

// Reset clears the visited bit for node at idx (same as Clear for a bitfield).
func (vb *atomicBitfield) Reset(idx int32) {
	vb.Clear(idx)
}

// IsVisited returns true if the visited bit for node at idx is set.
// Single atomic load — no CAS, no contention.
func (vb *atomicBitfield) IsVisited(idx int32) bool {
	w := idx / 64
	word := &vb.words[w]
	bit := uint64(1) << (idx % 64)
	return atomic.LoadUint64(word)&bit != 0
}

// ResetAll clears all visited bits. Called from Purge() under s.mu — plain stores are fine.
func (vb *atomicBitfield) ResetAll() {
	for i := range vb.words {
		vb.words[i] = 0
	}
}
