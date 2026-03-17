// saturating_counter.go - packed multi-bit saturating counters using atomic CAS
//
// Generalizes atomicBitfield to k-bit counters per slot. For k=1 this is
// equivalent to a visited bitfield (64 slots per word). For k=3, each slot
// uses 2 bits (32 slots per word), and counters saturate at 3.
//
// Read() is a single atomic load. Increment()/Decrement() use CAS loops
// with saturation early-exit.

package sieve

import (
	"math/bits"
	"sync/atomic"
)

// atomicSaturatingCounter is a packed array of multi-bit counters.
// Each counter occupies bitsPerSlot bits within a uint64 word and
// saturates at maxVal (which equals k).
type atomicSaturatingCounter struct {
	words        []uint64
	bitsPerSlot  uint
	slotsPerWord uint
	maxVal       uint64
	mask         uint64
}

var _ visitor = &atomicSaturatingCounter{}

// newAtomicSaturatingCounter creates a counter array with enough words
// for capacity slots, where each counter saturates at k.
// k must be >= 1.
func newAtomicSaturatingCounter(capacity int, k int) *atomicSaturatingCounter {
	if k < 1 {
		k = 1
	}
	bps := uint(bits.Len(uint(k))) // k=1→1, k=2..3→2, k=4..7→3
	spw := 64 / bps
	nwords := (uint(capacity) + spw - 1) / spw

	return &atomicSaturatingCounter{
		words:        make([]uint64, nwords),
		bitsPerSlot:  bps,
		slotsPerWord: spw,
		maxVal:       uint64(k),
		mask:         (1 << bps) - 1,
	}
}

// Mark increments the counter for slot idx, saturating at maxVal (k).
// Since the saturation check prevents overflow into the adjacent slot,
// we can add 1<<shift directly instead of extract→clear→reinsert.
func (sc *atomicSaturatingCounter) Mark(idx int32) {
	w := uint(idx) / sc.slotsPerWord
	word := &sc.words[w]

	shift := (uint(idx) % sc.slotsPerWord) * sc.bitsPerSlot
	incr := uint64(1) << shift
	vmax := sc.maxVal << shift
	mask := sc.mask << shift

	for {
		z := atomic.LoadUint64(word)
		if z&mask >= vmax {
			return // saturated
		}
		if atomic.CompareAndSwapUint64(word, z, z+incr) {
			return
		}
	}
}

// Clear decrements the counter for slot idx, saturating at 0.
// Since the zero check prevents underflow/borrow from the adjacent slot,
// we can subtract 1<<shift directly.
func (sc *atomicSaturatingCounter) Clear(idx int32) {
	w := uint(idx) / sc.slotsPerWord
	word := &sc.words[w]

	shift := (uint(idx) % sc.slotsPerWord) * sc.bitsPerSlot
	decr := uint64(1) << shift
	mask := sc.mask << shift

	for {
		z := atomic.LoadUint64(word)
		if z&mask == 0 {
			return
		}
		if atomic.CompareAndSwapUint64(word, z, z-decr) {
			return
		}
	}
}

// Reset sets the counter for slot idx to 0.
func (sc *atomicSaturatingCounter) Reset(idx int32) {
	w := uint(idx) / sc.slotsPerWord
	word := &sc.words[w]

	shift := (uint(idx) % sc.slotsPerWord) * sc.bitsPerSlot
	mask := sc.mask << shift

	for {
		z := atomic.LoadUint64(word)
		if z&mask == 0 {
			return // already zero
		}
		if atomic.CompareAndSwapUint64(word, z, z&^mask) {
			return
		}
	}
}

// read returns the raw counter value for slot idx (used in tests).
func (sc *atomicSaturatingCounter) read(idx int32) uint64 {
	w := uint(idx) / sc.slotsPerWord
	shift := (uint(idx) % sc.slotsPerWord) * sc.bitsPerSlot
	word := atomic.LoadUint64(&sc.words[w])
	return (word >> shift) & sc.mask
}

// IsVisited returns true if the counter for slot idx is > 0.
// Masks in place to avoid the right-shift.
func (sc *atomicSaturatingCounter) IsVisited(idx int32) bool {
	w := uint(idx) / sc.slotsPerWord
	shift := (uint(idx) % sc.slotsPerWord) * sc.bitsPerSlot
	word := atomic.LoadUint64(&sc.words[w])
	return word&(sc.mask<<shift) != 0
}

// ResetAll clears all counters. Called from Purge() under lock — plain stores are fine.
func (sc *atomicSaturatingCounter) ResetAll() {
	for i := range sc.words {
		sc.words[i] = 0
	}
}
