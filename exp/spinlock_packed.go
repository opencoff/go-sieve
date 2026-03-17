// spinlock_packed.go - packed exclusive spinlock using atomic bit ops
//
// Column-oriented per-node synchronization: 1 bit per lock slot,
// 64 locks packed into each uint64 word. For 1M entries this is
// 16 KB (same layout as the visited bitfield).
//
// Every lock/unlock (both Get and Add paths) is exclusive.
// The critical section is a single field read or write (~1 ns),
// so reader-reader serialization is negligible compared to the
// CAS overhead of a reader-writer scheme.
//
// On the uncontended fast path:
//   Lock:   1 atomic.OrUint64 (test-and-set in one instruction)
//   Unlock: 1 atomic.AndUint64

package sieve

import (
	"runtime"
	"sync/atomic"
)

const (
	packedSlotsPerWord = 64
)

// packedSpinlock is a packed array of exclusive spinlocks.
// 64 locks per uint64 word, one lock per node index.
type packedSpinlock struct {
	words []uint64
}

// newPackedSpinlock creates a packed spinlock array for the given capacity.
func newPackedSpinlock(capacity int) *packedSpinlock {
	nwords := (capacity + packedSlotsPerWord - 1) / packedSlotsPerWord
	return &packedSpinlock{
		words: make([]uint64, nwords),
	}
}

// packedSlotPos returns the word index and bit mask for a given node index.
func packedSlotPos(idx int32) (w int32, mask uint64) {
	w = idx / packedSlotsPerWord
	mask = uint64(1) << uint(idx%packedSlotsPerWord)
	return
}

// Lock acquires an exclusive lock for the node at idx.
// Uses atomic.OrUint64 as a test-and-set: the returned old value
// tells us whether we won the race (bit was clear) or need to spin.
// OR of a 1 onto an existing 1 is idempotent — no harm if we lose.
func (sl *packedSpinlock) Lock(idx int32) {
	w, mask := packedSlotPos(idx)
	word := &sl.words[w]

	for {
		old := atomic.OrUint64(word, mask)
		if old&mask == 0 {
			// Bit was clear — we acquired it
			return
		}
		// Already locked — spin
		runtime.Gosched()
	}
}

// Unlock releases an exclusive lock for the node at idx.
func (sl *packedSpinlock) Unlock(idx int32) {
	w, mask := packedSlotPos(idx)
	atomic.AndUint64(&sl.words[w], ^mask)
}

// ResetAll clears all lock state. Called from Purge() under s.mu.
func (sl *packedSpinlock) ResetAll() {
	for i := range sl.words {
		sl.words[i] = 0
	}
}
