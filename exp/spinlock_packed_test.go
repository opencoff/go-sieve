package sieve

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestPackedSpinlock_BasicLockUnlock(t *testing.T) {
	sl := newPackedSpinlock(64)

	sl.Lock(0)
	// Verify bit is set
	w, mask := packedSlotPos(0)
	if sl.words[w]&mask == 0 {
		t.Fatal("bit not set after Lock")
	}

	sl.Unlock(0)
	if sl.words[w]&mask != 0 {
		t.Fatal("bit not cleared after Unlock")
	}
}

func TestPackedSpinlock_ExcludesSecondLocker(t *testing.T) {
	sl := newPackedSpinlock(64)
	const idx = int32(3)

	sl.Lock(idx)

	acquired := make(chan struct{})
	go func() {
		sl.Lock(idx)
		close(acquired)
		sl.Unlock(idx)
	}()

	// Give the goroutine time to spin
	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}

	select {
	case <-acquired:
		t.Fatal("second locker acquired while first held it")
	default:
		// Expected: spinning
	}

	sl.Unlock(idx)
	<-acquired
}

func TestPackedSpinlock_DifferentSlotsDontInterfere(t *testing.T) {
	sl := newPackedSpinlock(128)

	// Lock slot 0
	sl.Lock(0)

	// Slot 1 (same word) should be independently lockable
	sl.Lock(1)
	sl.Unlock(1)

	// Slot 63 (same word, last bit)
	sl.Lock(63)
	sl.Unlock(63)

	// Slot 64 (different word)
	sl.Lock(64)
	sl.Unlock(64)

	sl.Unlock(0)
}

func TestPackedSpinlock_AdjacentBitIsolation(t *testing.T) {
	sl := newPackedSpinlock(64)

	// Lock slots 0 and 1
	sl.Lock(0)
	sl.Lock(1)

	// Unlock slot 0 — slot 1 must remain locked
	sl.Unlock(0)

	w0, mask0 := packedSlotPos(0)
	w1, mask1 := packedSlotPos(1)

	if sl.words[w0]&mask0 != 0 {
		t.Error("slot 0 still locked after Unlock")
	}
	if sl.words[w1]&mask1 == 0 {
		t.Error("slot 1 was cleared by slot 0's Unlock")
	}

	sl.Unlock(1)
}

func TestPackedSpinlock_AllBitsInWord(t *testing.T) {
	sl := newPackedSpinlock(64)

	// Lock all 64 bits in word 0
	for i := int32(0); i < 64; i++ {
		sl.Lock(i)
	}

	// Word should be all 1s
	if sl.words[0] != ^uint64(0) {
		t.Fatalf("expected all bits set, got %016x", sl.words[0])
	}

	// Unlock all
	for i := int32(0); i < 64; i++ {
		sl.Unlock(i)
	}

	if sl.words[0] != 0 {
		t.Fatalf("expected 0, got %016x", sl.words[0])
	}
}

func TestPackedSpinlock_ConcurrentExclusion(t *testing.T) {
	sl := newPackedSpinlock(64)
	const idx = int32(0)
	const numGoroutines = 50
	const opsPerGoroutine = 10_000

	var counter int64

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				sl.Lock(idx)
				counter++
				sl.Unlock(idx)
			}
		}()
	}
	wg.Wait()

	expected := int64(numGoroutines) * int64(opsPerGoroutine)
	if counter != expected {
		t.Fatalf("expected %d, got %d", expected, counter)
	}
}

func TestPackedSpinlock_ConcurrentTornReadDetection(t *testing.T) {
	sl := newPackedSpinlock(64)
	const idx = int32(0)
	const numReaders = 8
	const numWriters = 2
	const opsPerGoroutine = 50_000

	var field1, field2 int64
	var tornReads atomic.Int64

	var wg sync.WaitGroup

	// Readers
	wg.Add(numReaders)
	for g := 0; g < numReaders; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				sl.Lock(idx)
				f1 := field1
				f2 := field2
				sl.Unlock(idx)
				if f1 != f2 {
					tornReads.Add(1)
				}
			}
		}()
	}

	// Writers
	wg.Add(numWriters)
	for g := 0; g < numWriters; g++ {
		go func(id int) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				val := int64(id*opsPerGoroutine + i)
				sl.Lock(idx)
				field1 = val
				field2 = val
				sl.Unlock(idx)
			}
		}(g)
	}

	wg.Wait()

	if torn := tornReads.Load(); torn != 0 {
		t.Fatalf("detected %d torn reads", torn)
	}
}

func TestPackedSpinlock_StressMultiSlot(t *testing.T) {
	sl := newPackedSpinlock(64)
	const numSlots = 64 // all in same uint64 word
	const numGoroutines = 4
	const opsPerGoroutine = 20_000

	var wg sync.WaitGroup

	for slot := int32(0); slot < numSlots; slot++ {
		wg.Add(numGoroutines)
		for g := 0; g < numGoroutines; g++ {
			go func(s int32) {
				defer wg.Done()
				for i := 0; i < opsPerGoroutine; i++ {
					sl.Lock(s)
					runtime.Gosched()
					sl.Unlock(s)
				}
			}(slot)
		}
	}

	wg.Wait()

	if sl.words[0] != 0 {
		t.Fatalf("word not zero after stress: %016x", sl.words[0])
	}
}

func TestPackedSpinlock_ResetAll(t *testing.T) {
	sl := newPackedSpinlock(128)

	sl.Lock(0)
	sl.Lock(1)
	sl.Lock(64)

	sl.ResetAll()

	for i, w := range sl.words {
		if w != 0 {
			t.Fatalf("word[%d] not zero after ResetAll: %016x", i, w)
		}
	}
}

// --- Benchmarks ---

func BenchmarkPackedSpinlock_LockUnlock_Uncontended(b *testing.B) {
	sl := newPackedSpinlock(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.Lock(0)
		sl.Unlock(0)
	}
}

func BenchmarkPackedSpinlock_LockUnlock_Parallel(b *testing.B) {
	sl := newPackedSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sl.Lock(0)
			sl.Unlock(0)
		}
	})
}

func BenchmarkPackedSpinlock_LockUnlock_DifferentSlots(b *testing.B) {
	sl := newPackedSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := int32(runtime.GOMAXPROCS(0))
		slot := int32(0)
		for pb.Next() {
			sl.Lock(slot % id)
			sl.Unlock(slot % id)
			slot++
		}
	})
}
