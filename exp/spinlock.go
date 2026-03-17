// spinlock.go - per-slot exclusive spinlock using uint32 atomics
//
// Column-oriented per-node synchronization: one uint32 per lock slot.
// 16 locks per cache line (64 bytes / 4 bytes), reducing false sharing
// compared to the packed 1-bit layout (512 locks per cache line).
//
// For 1M entries this is 4 MB. The tradeoff is more memory for less
// false sharing on concurrent atomic ops across different slots.
//
// On the uncontended fast path:
//   Lock:   1 atomic.CompareAndSwapUint64
//   Unlock: 1 atomic.StoreUint64

package sieve

import (
	"sync/atomic"
)

// spinlock is an array of per-slot exclusive spinlocks.
// One uint32 per node index: 0 = unlocked, 1 = locked.
type spinlock struct {
	slots []uint64
}

// newSpinlock creates a spinlock array for the given capacity.
func newSpinlock(capacity int) *spinlock {
	return &spinlock{
		slots: make([]uint64, capacity),
	}
}

// Lock acquires an exclusive lock for the node at idx.
func (sl *spinlock) Lock(idx int32) {
	slot := &sl.slots[idx]
	for i := 0; ; i++ {
		if atomic.CompareAndSwapUint64(slot, 0, 1) {
			return
		}
		pause(i)
	}
}

// Unlock releases an exclusive lock for the node at idx.
func (sl *spinlock) Unlock(idx int32) {
	atomic.StoreUint64(&sl.slots[idx], 0)
}

// ResetAll clears all lock state. Called from Purge() under s.mu.
func (sl *spinlock) ResetAll() {
	for i := range sl.slots {
		sl.slots[i] = 0
	}
}
