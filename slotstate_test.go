// slotstate_test.go - unit tests, concurrency tests, and microbenchmarks for slotState
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
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// =========================================================================
// Construction
// =========================================================================

func TestSlotState_NewK1(t *testing.T) {
	ss := newSlotState(100, 1)
	if ss.vbits != 1 {
		t.Fatalf("vbits=%d, want 1", ss.vbits)
	}
	if ss.vmask != 1 {
		t.Fatalf("vmask=%d, want 1", ss.vmask)
	}
	if ss.maxVisit != 1 {
		t.Fatalf("maxVisit=%d, want 1", ss.maxVisit)
	}
	if len(ss.words) != 100 {
		t.Fatalf("len(words)=%d, want 100", len(ss.words))
	}
}

func TestSlotState_NewKValues(t *testing.T) {
	tests := []struct {
		k        int
		wantBits uint
		wantMask uint64
		wantMax  uint64
	}{
		{1, 1, 1, 1},
		{2, 2, 3, 2}, // 2 bits, mask=3, but saturates at 2 not 3
		{3, 2, 3, 3},
		{4, 3, 7, 4},
		{7, 3, 7, 7},
		{8, 4, 15, 8},
		{15, 4, 15, 15},
		{63, 6, 63, 63},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("k=%d", tt.k), func(t *testing.T) {
			ss := newSlotState(4, tt.k)
			if ss.vbits != tt.wantBits {
				t.Errorf("vbits=%d, want %d", ss.vbits, tt.wantBits)
			}
			if ss.vmask != tt.wantMask {
				t.Errorf("vmask=%d, want %d", ss.vmask, tt.wantMask)
			}
			if ss.maxVisit != tt.wantMax {
				t.Errorf("maxVisit=%d, want %d", ss.maxVisit, tt.wantMax)
			}
		})
	}
}

func TestSlotState_NewKClamped(t *testing.T) {
	for _, k := range []int{0, -1, -100} {
		ss := newSlotState(8, k)
		if ss.maxVisit != 1 {
			t.Fatalf("k=%d: maxVisit=%d, want 1 (clamped)", k, ss.maxVisit)
		}
		if ss.vbits != 1 {
			t.Fatalf("k=%d: vbits=%d, want 1", k, ss.vbits)
		}
	}
}

// =========================================================================
// Lock / Unlock basics
// =========================================================================

func TestSlotState_LockUnlock(t *testing.T) {
	ss := newSlotState(4, 1)

	ss.Lock(0)
	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit == 0 {
		t.Fatal("Lock: lock bit not set")
	}

	ss.Unlock(0)
	w = atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("Unlock: lock bit still set")
	}
}

func TestSlotState_Lock_DoesNotAffectVisited(t *testing.T) {
	ss := newSlotState(4, 3)

	// Mark to counter=2
	ss.LockAndMark(0)
	ss.Unlock(0)
	ss.LockAndMark(0)
	ss.Unlock(0)

	// Plain Lock/Unlock should not change the counter
	ss.Lock(0)
	ss.Unlock(0)

	w := atomic.LoadUint64(&ss.words[0])
	counter := w & ss.vmask
	if counter != 2 {
		t.Fatalf("Lock/Unlock changed counter: got %d, want 2", counter)
	}
}

// =========================================================================
// LockAndMark
// =========================================================================

func TestSlotState_LockAndMark_K1(t *testing.T) {
	ss := newSlotState(4, 1)

	if ss.IsVisited(0) {
		t.Fatal("should not be visited initially")
	}

	ss.LockAndMark(0)

	// While locked: both bits set
	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit == 0 {
		t.Fatal("lock bit not set")
	}
	if w&1 == 0 {
		t.Fatal("visited bit not set")
	}

	ss.Unlock(0)

	// After unlock: visited preserved, lock cleared
	if !ss.IsVisited(0) {
		t.Fatal("Unlock should preserve visited bit")
	}
	w = atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("Unlock should clear lock bit")
	}
}

func TestSlotState_LockAndMark_K1_Idempotent(t *testing.T) {
	ss := newSlotState(4, 1)

	// Multiple marks on k=1: bit stays 1
	for i := 0; i < 20; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	w := atomic.LoadUint64(&ss.words[0])
	if w != 1 { // visited=1, lock=0
		t.Fatalf("k=1 after 20 marks: word=%#x, want 0x1", w)
	}
}

func TestSlotState_LockAndMark_K3_Saturation(t *testing.T) {
	ss := newSlotState(4, 3)

	// Increment 10 times, should saturate at 3
	for i := 0; i < 10; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	w := atomic.LoadUint64(&ss.words[0])
	counter := w & ss.vmask
	if counter != 3 {
		t.Fatalf("k=3 after 10 marks: counter=%d, want 3", counter)
	}
}

// K=2 uses 2-bit field (vmask=3) but must saturate at 2, not 3.
func TestSlotState_LockAndMark_K2_SaturatesAtMaxVisitNotMask(t *testing.T) {
	ss := newSlotState(4, 2)

	for i := 0; i < 20; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	w := atomic.LoadUint64(&ss.words[0])
	counter := w & ss.vmask
	if counter != 2 {
		t.Fatalf("k=2: counter=%d, want 2 (maxVisit, not vmask=%d)", counter, ss.vmask)
	}
}

func TestSlotState_LockAndMark_K7_StepByStep(t *testing.T) {
	ss := newSlotState(4, 7)

	for want := uint64(1); want <= 7; want++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
		w := atomic.LoadUint64(&ss.words[0])
		got := w & ss.vmask
		if got != want {
			t.Fatalf("after %d marks: counter=%d, want %d", want, got, want)
		}
	}

	// One more: should stay at 7
	ss.LockAndMark(0)
	ss.Unlock(0)
	w := atomic.LoadUint64(&ss.words[0])
	if w&ss.vmask != 7 {
		t.Fatalf("after saturation: counter=%d, want 7", w&ss.vmask)
	}
}

// =========================================================================
// IsVisited
// =========================================================================

func TestSlotState_IsVisited(t *testing.T) {
	ss := newSlotState(4, 3)

	if ss.IsVisited(0) {
		t.Fatal("should not be visited initially")
	}

	ss.LockAndMark(0)
	ss.Unlock(0)
	if !ss.IsVisited(0) {
		t.Fatal("should be visited after mark")
	}
}

// =========================================================================
// Clear
// =========================================================================

func TestSlotState_Clear_K1(t *testing.T) {
	ss := newSlotState(4, 1)

	ss.LockAndMark(0)
	ss.Unlock(0)
	if !ss.IsVisited(0) {
		t.Fatal("expected visited after mark")
	}

	ss.Clear(0)
	if ss.IsVisited(0) {
		t.Fatal("expected not visited after clear")
	}

	// Word should be fully zero
	w := atomic.LoadUint64(&ss.words[0])
	if w != 0 {
		t.Fatalf("word=%#x after clear, want 0", w)
	}
}

func TestSlotState_Clear_K3_DecrementsByOne(t *testing.T) {
	ss := newSlotState(4, 3)

	// Saturate at 3
	for i := 0; i < 5; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	for want := uint64(2); ; want-- {
		ss.Clear(0)
		w := atomic.LoadUint64(&ss.words[0])
		got := w & ss.vmask
		if got != want {
			t.Fatalf("Clear: counter=%d, want %d", got, want)
		}
		if !ss.IsVisited(0) == (want > 0) {
			t.Fatalf("Clear: IsVisited=%v, want %v", ss.IsVisited(0), want > 0)
		}
		if want == 0 {
			break
		}
	}
}

func TestSlotState_Clear_AlreadyZero(t *testing.T) {
	ss := newSlotState(4, 3)

	// Clear on zero counter: no-op
	ss.Clear(0)
	w := atomic.LoadUint64(&ss.words[0])
	if w != 0 {
		t.Fatalf("Clear on zero: word=%#x, want 0", w)
	}
}

// Clear uses CAS; verify it works while the lock bit is held by someone else.
// The CAS includes the lock bit in the expected value, so it will succeed
// as long as the lock state doesn't change between Load and CAS.
func TestSlotState_Clear_WhileLocked(t *testing.T) {
	ss := newSlotState(4, 3)

	// Mark to 3
	for i := 0; i < 3; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	// Hold the lock, Clear from another goroutine
	ss.Lock(0)

	done := make(chan struct{})
	go func() {
		ss.Clear(0) // CAS on _LockBit|3 → _LockBit|2
		close(done)
	}()

	// Give the Clear goroutine time to execute
	runtime.Gosched()
	runtime.Gosched()

	ss.Unlock(0)
	<-done

	w := atomic.LoadUint64(&ss.words[0])
	counter := w & ss.vmask
	if counter != 2 {
		t.Fatalf("Clear while locked: counter=%d, want 2", counter)
	}
}

// =========================================================================
// LockAndReset
// =========================================================================

func TestSlotState_LockAndReset(t *testing.T) {
	ss := newSlotState(4, 3)

	// Mark to 3
	for i := 0; i < 5; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}
	if !ss.IsVisited(0) {
		t.Fatal("should be visited before reset")
	}

	ss.LockAndReset(0)

	// Lock held, visited cleared
	w := atomic.LoadUint64(&ss.words[0])
	if w != _LockBit {
		t.Fatalf("LockAndReset: word=%#x, want %#x (lock bit only)", w, _LockBit)
	}

	ss.Unlock(0)

	if ss.IsVisited(0) {
		t.Fatal("visited should be cleared after LockAndReset")
	}
	w = atomic.LoadUint64(&ss.words[0])
	if w != 0 {
		t.Fatalf("after Unlock: word=%#x, want 0", w)
	}
}

// LockAndReset must wait if the lock is already held.
func TestSlotState_LockAndReset_WaitsForLock(t *testing.T) {
	ss := newSlotState(4, 1)

	ss.LockAndMark(0)
	// Lock is held. LockAndReset in another goroutine must block.

	var resetDone atomic.Bool
	go func() {
		ss.LockAndReset(0)
		resetDone.Store(true)
		ss.Unlock(0)
	}()

	// Yield a few times — reset goroutine should still be spinning
	for i := 0; i < 10; i++ {
		runtime.Gosched()
	}
	if resetDone.Load() {
		t.Fatal("LockAndReset returned while lock was held")
	}

	// Release the lock
	ss.Unlock(0)

	// Wait for the reset goroutine
	for i := 0; i < 1000; i++ {
		if resetDone.Load() {
			break
		}
		runtime.Gosched()
	}
	if !resetDone.Load() {
		t.Fatal("LockAndReset never completed")
	}

	// After LockAndReset + Unlock: visited should be cleared
	if ss.IsVisited(0) {
		t.Fatal("visited should be cleared after LockAndReset")
	}
}

// =========================================================================
// Reset / ResetAll
// =========================================================================

func TestSlotState_Reset(t *testing.T) {
	ss := newSlotState(4, 3)

	ss.LockAndMark(0)
	ss.Unlock(0)

	ss.Reset(0)
	w := atomic.LoadUint64(&ss.words[0])
	if w != 0 {
		t.Fatalf("Reset: word=%#x, want 0", w)
	}
}

func TestSlotState_ResetAll(t *testing.T) {
	ss := newSlotState(8, 3)

	for i := int32(0); i < 8; i++ {
		ss.LockAndMark(i)
		ss.Unlock(i)
	}

	ss.ResetAll()

	for i := int32(0); i < 8; i++ {
		w := atomic.LoadUint64(&ss.words[i])
		if w != 0 {
			t.Fatalf("ResetAll: word[%d]=%#x, want 0", i, w)
		}
	}
}

// =========================================================================
// Slot independence
// =========================================================================

func TestSlotState_SlotsAreIndependent(t *testing.T) {
	ss := newSlotState(4, 3)

	// Mark slot 0 to saturation
	for i := 0; i < 5; i++ {
		ss.LockAndMark(0)
		ss.Unlock(0)
	}

	// Mark slot 1 once
	ss.LockAndMark(1)
	ss.Unlock(1)

	// Slot 2 and 3 untouched
	if ss.IsVisited(2) || ss.IsVisited(3) {
		t.Fatal("untouched slots should not be visited")
	}

	w0 := atomic.LoadUint64(&ss.words[0])
	w1 := atomic.LoadUint64(&ss.words[1])
	if w0&ss.vmask != 3 {
		t.Fatalf("slot 0: counter=%d, want 3", w0&ss.vmask)
	}
	if w1&ss.vmask != 1 {
		t.Fatalf("slot 1: counter=%d, want 1", w1&ss.vmask)
	}

	// Clear slot 0; slot 1 unaffected
	ss.Clear(0)
	w1 = atomic.LoadUint64(&ss.words[1])
	if w1&ss.vmask != 1 {
		t.Fatalf("slot 1 changed after clearing slot 0: counter=%d", w1&ss.vmask)
	}
}

// =========================================================================
// Concurrency correctness tests
// =========================================================================

// Verify that Lock provides mutual exclusion using a shared counter.
func TestSlotState_MutualExclusion_Lock(t *testing.T) {
	ss := newSlotState(4, 1)
	const goroutines = 50
	const iters = 10000

	var counter int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.Lock(0)
				c := counter
				c++
				counter = c
				ss.Unlock(0)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * iters)
	if counter != want {
		t.Fatalf("mutual exclusion violated: counter=%d, want %d", counter, want)
	}
}

// Verify that LockAndMark provides mutual exclusion.
func TestSlotState_MutualExclusion_LockAndMark(t *testing.T) {
	ss := newSlotState(4, 1)
	const goroutines = 50
	const iters = 10000

	var counter int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				c := counter
				c++
				counter = c
				ss.Unlock(0)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * iters)
	if counter != want {
		t.Fatalf("mutual exclusion violated: counter=%d, want %d", counter, want)
	}
}

// Verify that LockAndMark k>1 provides mutual exclusion. The k>1 path
// uses a CAS loop for the counter increment; verify it doesn't break
// the lock's exclusion guarantee.
func TestSlotState_MutualExclusion_LockAndMark_K3(t *testing.T) {
	ss := newSlotState(4, 3)
	const goroutines = 50
	const iters = 10000

	var counter int64
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				c := counter
				c++
				counter = c
				ss.Unlock(0)
			}
		}()
	}
	wg.Wait()

	want := int64(goroutines * iters)
	if counter != want {
		t.Fatalf("mutual exclusion violated: counter=%d, want %d", counter, want)
	}
}

// Concurrent LockAndMark on the same slot: final state must have
// lock cleared and counter at the correct saturation value.
func TestSlotState_ConcurrentMark_K1(t *testing.T) {
	ss := newSlotState(4, 1)
	const goroutines = 100
	const iters = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				ss.Unlock(0)
			}
		}()
	}
	wg.Wait()

	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("lock bit set after all goroutines finished")
	}
	if w&ss.vmask != 1 {
		t.Fatalf("k=1: counter=%d, want 1", w&ss.vmask)
	}
}

func TestSlotState_ConcurrentMark_K3(t *testing.T) {
	ss := newSlotState(4, 3)
	const goroutines = 100
	const iters = 5000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				ss.Unlock(0)
			}
		}()
	}
	wg.Wait()

	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("lock bit set after all goroutines finished")
	}
	if w&ss.vmask != 3 {
		t.Fatalf("k=3: counter=%d, want 3", w&ss.vmask)
	}
}

// LockAndMark racing with Clear on the same slot. This exercises:
// - The CAS loop in Clear (fails when LockAndMark flips the lock bit)
// - The CAS loop in LockAndMark k>1 (fails when Clear decrements the counter)
func TestSlotState_ConcurrentMarkAndClear_K3(t *testing.T) {
	ss := newSlotState(4, 3)
	const goroutines = 20
	const iters = 10000

	var wg sync.WaitGroup

	// Markers
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				ss.Unlock(0)
			}
		}()
	}

	// Clearers (as eviction would do)
	wg.Add(goroutines / 2)
	for g := 0; g < goroutines/2; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.Clear(0)
			}
		}()
	}

	wg.Wait()

	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("lock bit set after all goroutines finished")
	}
	counter := w & ss.vmask
	if counter > ss.maxVisit {
		t.Fatalf("counter=%d exceeds maxVisit=%d", counter, ss.maxVisit)
	}
}

// LockAndReset racing with LockAndMark. This is the newNode() + Get() race:
// a stale Get holds the lock while newNode calls LockAndReset.
func TestSlotState_ConcurrentLockAndReset_WithMark(t *testing.T) {
	ss := newSlotState(4, 3)
	const goroutines = 20
	const iters = 5000

	var wg sync.WaitGroup

	// Markers (simulating Get hot path)
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters; i++ {
				ss.LockAndMark(0)
				ss.Unlock(0)
			}
		}()
	}

	// Resetters (simulating newNode)
	resetCount := goroutines / 4
	wg.Add(resetCount)
	for g := 0; g < resetCount; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < iters/10; i++ {
				ss.LockAndReset(0)
				// While holding lock after reset: word must be _LockBit only
				w := atomic.LoadUint64(&ss.words[0])
				if w != _LockBit {
					t.Errorf("LockAndReset: word=%#x, want %#x", w, _LockBit)
					ss.Unlock(0)
					return
				}
				ss.Unlock(0)
			}
		}()
	}

	wg.Wait()

	w := atomic.LoadUint64(&ss.words[0])
	if w&_LockBit != 0 {
		t.Fatal("lock bit set after all goroutines finished")
	}
}

// Multiple slots under concurrent access: verify no cross-slot interference.
func TestSlotState_ConcurrentMultipleSlots(t *testing.T) {
	const nslots = 16
	ss := newSlotState(nslots, 1)
	const goroutines = 64
	const iters = 5000

	counters := make([]int64, nslots)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			slot := int32(id % nslots)
			for i := 0; i < iters; i++ {
				ss.Lock(slot)
				counters[slot]++
				ss.Unlock(slot)
			}
		}(g)
	}
	wg.Wait()

	for slot := 0; slot < nslots; slot++ {
		assigned := 0
		for g := 0; g < goroutines; g++ {
			if g%nslots == slot {
				assigned++
			}
		}
		want := int64(assigned * iters)
		if counters[slot] != want {
			t.Errorf("slot %d: counter=%d, want %d", slot, counters[slot], want)
		}
	}
}

// Realistic mixed workload: mostly LockAndMark+Unlock with occasional
// Clear and LockAndReset, across many slots.
func TestSlotState_ConcurrentMixedWorkload(t *testing.T) {
	const nslots = 64
	ss := newSlotState(nslots, 3)
	const goroutines = 32
	const iters = 20000

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(id int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(id)))
			for i := 0; i < iters; i++ {
				idx := int32(r.Intn(nslots))
				op := r.Intn(20)
				switch {
				case op < 14: // 70% LockAndMark (Get path)
					ss.LockAndMark(idx)
					ss.Unlock(idx)
				case op < 17: // 15% Clear (eviction scan)
					ss.Clear(idx)
				case op < 19: // 10% Lock+Unlock (remove path)
					ss.Lock(idx)
					ss.Unlock(idx)
				default: // 5% LockAndReset (newNode path)
					ss.LockAndReset(idx)
					ss.Unlock(idx)
				}
			}
		}(g)
	}
	wg.Wait()

	// All locks should be released
	for i := int32(0); i < nslots; i++ {
		w := atomic.LoadUint64(&ss.words[i])
		if w&_LockBit != 0 {
			t.Errorf("slot %d: lock bit still set", i)
		}
		if w&ss.vmask > ss.maxVisit {
			t.Errorf("slot %d: counter=%d exceeds maxVisit=%d", i, w&ss.vmask, ss.maxVisit)
		}
	}
}

// =========================================================================
// Benchmarks — uncontended (single-thread baseline)
// =========================================================================

// The hot Get() path: LockAndMark + Unlock, k=1.
func BenchmarkSlotState_LockAndMark_Uncontended_K1(b *testing.B) {
	ss := newSlotState(1024, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := int32(i & 1023)
		ss.LockAndMark(idx)
		ss.Unlock(idx)
	}
}

// The hot Get() path for SIEVE-k: LockAndMark + Unlock, k=3.
func BenchmarkSlotState_LockAndMark_Uncontended_K3(b *testing.B) {
	ss := newSlotState(1024, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := int32(i & 1023)
		ss.LockAndMark(idx)
		ss.Unlock(idx)
	}
}

// Plain Lock + Unlock (the remove() path).
func BenchmarkSlotState_Lock_Uncontended(b *testing.B) {
	ss := newSlotState(1024, 1)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := int32(i & 1023)
		ss.Lock(idx)
		ss.Unlock(idx)
	}
}

// Lock-free read path (eviction scan check).
func BenchmarkSlotState_IsVisited_Uncontended(b *testing.B) {
	ss := newSlotState(1024, 1)
	// Mark half the slots
	for i := int32(0); i < 512; i++ {
		ss.LockAndMark(i)
		ss.Unlock(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ss.IsVisited(int32(i & 1023))
	}
}

// Clear CAS loop (eviction scan decrement).
func BenchmarkSlotState_Clear_Uncontended(b *testing.B) {
	ss := newSlotState(1024, 3)
	// Pre-mark everything to counter=3
	for i := int32(0); i < 1024; i++ {
		for j := 0; j < 3; j++ {
			ss.LockAndMark(i)
			ss.Unlock(i)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := int32(i & 1023)
		ss.Clear(idx)
		// Re-mark so we don't drain the counter
		ss.LockAndMark(idx)
		ss.Unlock(idx)
	}
}

// LockAndReset + Unlock (the newNode() path).
func BenchmarkSlotState_LockAndReset_Uncontended(b *testing.B) {
	ss := newSlotState(1024, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		idx := int32(i & 1023)
		ss.LockAndReset(idx)
		ss.Unlock(idx)
	}
}

// =========================================================================
// Benchmarks — contended (parallel)
// =========================================================================

// Worst case: all goroutines hammer the same slot. k=1.
func BenchmarkSlotState_LockAndMark_Contended_K1_SameSlot(b *testing.B) {
	ss := newSlotState(1024, 1)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ss.LockAndMark(0)
			ss.Unlock(0)
		}
	})
}

// Worst case: same slot, k=3 (Or + CAS path).
func BenchmarkSlotState_LockAndMark_Contended_K3_SameSlot(b *testing.B) {
	ss := newSlotState(1024, 3)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ss.LockAndMark(0)
			ss.Unlock(0)
		}
	})
}

// Plain Lock contention on same slot.
func BenchmarkSlotState_Lock_Contended_SameSlot(b *testing.B) {
	ss := newSlotState(1024, 1)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			ss.Lock(0)
			ss.Unlock(0)
		}
	})
}

// Contention scaling: vary the number of hot slots.
// Contention probability = 1/hotSlots per pair of goroutines.
func BenchmarkSlotState_LockAndMark_ContendedScaling_K1(b *testing.B) {
	for _, hotSlots := range []int{1, 4, 16, 64, 256, 1024, 8192} {
		b.Run(fmt.Sprintf("Slots_%d", hotSlots), func(b *testing.B) {
			ss := newSlotState(8192, 1)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				r := rand.New(rand.NewSource(rand.Int63()))
				for pb.Next() {
					idx := int32(r.Intn(hotSlots))
					ss.LockAndMark(idx)
					ss.Unlock(idx)
				}
			})
		})
	}
}

// Same scaling test for k=3 to show the CAS overhead under contention.
func BenchmarkSlotState_LockAndMark_ContendedScaling_K3(b *testing.B) {
	for _, hotSlots := range []int{1, 4, 16, 64, 256, 1024, 8192} {
		b.Run(fmt.Sprintf("Slots_%d", hotSlots), func(b *testing.B) {
			ss := newSlotState(8192, 3)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				r := rand.New(rand.NewSource(rand.Int63()))
				for pb.Next() {
					idx := int32(r.Intn(hotSlots))
					ss.LockAndMark(idx)
					ss.Unlock(idx)
				}
			})
		})
	}
}

// IsVisited is lock-free; verify it scales linearly.
func BenchmarkSlotState_IsVisited_Parallel(b *testing.B) {
	const nslots = 8192
	ss := newSlotState(nslots, 1)
	for i := int32(0); i < int32(nslots); i += 2 {
		ss.LockAndMark(i)
		ss.Unlock(i)
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			ss.IsVisited(int32(r.Intn(nslots)))
		}
	})
}

// Clear under contention with concurrent LockAndMark (eviction vs Get).
func BenchmarkSlotState_Clear_Contended_K3(b *testing.B) {
	const nslots = 1024
	ss := newSlotState(nslots, 3)
	// Pre-fill
	for i := int32(0); i < nslots; i++ {
		for j := 0; j < 3; j++ {
			ss.LockAndMark(i)
			ss.Unlock(i)
		}
	}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			idx := int32(r.Intn(nslots))
			if r.Intn(2) == 0 {
				ss.LockAndMark(idx)
				ss.Unlock(idx)
			} else {
				ss.Clear(idx)
			}
		}
	})
}

// Realistic mixed workload: 90% LockAndMark, 10% Clear.
func BenchmarkSlotState_MixedWorkload_Parallel(b *testing.B) {
	const nslots = 8192
	ss := newSlotState(nslots, 1)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			idx := int32(r.Intn(nslots))
			if r.Intn(10) < 9 {
				ss.LockAndMark(idx)
				ss.Unlock(idx)
			} else {
				ss.Clear(idx)
			}
		}
	})
}
