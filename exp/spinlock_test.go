package sieve

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSpinlock_BasicLockUnlock(t *testing.T) {
	sl := newSpinlock(64)

	sl.Lock(0)
	if atomic.LoadUint64(&sl.slots[0]) != 1 {
		t.Fatal("slot not 1 after Lock")
	}

	sl.Unlock(0)
	if atomic.LoadUint64(&sl.slots[0]) != 0 {
		t.Fatal("slot not 0 after Unlock")
	}
}

func TestSpinlock_ExcludesSecondLocker(t *testing.T) {
	sl := newSpinlock(64)
	const idx = int32(3)

	sl.Lock(idx)

	acquired := make(chan struct{})
	go func() {
		sl.Lock(idx)
		close(acquired)
		sl.Unlock(idx)
	}()

	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}

	select {
	case <-acquired:
		t.Fatal("second locker acquired while first held it")
	default:
	}

	sl.Unlock(idx)
	<-acquired
}

func TestSpinlock_DifferentSlotsDontInterfere(t *testing.T) {
	sl := newSpinlock(128)

	sl.Lock(0)

	// Other slots independently lockable
	sl.Lock(1)
	sl.Unlock(1)

	sl.Lock(15) // same cache line
	sl.Unlock(15)

	sl.Lock(16) // different cache line
	sl.Unlock(16)

	sl.Unlock(0)
}

func TestSpinlock_AdjacentSlotIsolation(t *testing.T) {
	sl := newSpinlock(64)

	sl.Lock(0)
	sl.Lock(1)

	sl.Unlock(0)

	if atomic.LoadUint64(&sl.slots[0]) != 0 {
		t.Error("slot 0 still locked after Unlock")
	}
	if atomic.LoadUint64(&sl.slots[1]) != 1 {
		t.Error("slot 1 was cleared by slot 0's Unlock")
	}

	sl.Unlock(1)
}

func TestSpinlock_ConcurrentExclusion(t *testing.T) {
	sl := newSpinlock(64)
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

func TestSpinlock_ConcurrentTornReadDetection(t *testing.T) {
	sl := newSpinlock(64)
	const idx = int32(0)
	const numReaders = 8
	const numWriters = 2
	const opsPerGoroutine = 50_000

	var field1, field2 int64
	var tornReads atomic.Int64

	var wg sync.WaitGroup

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

func TestSpinlock_StressMultiSlot(t *testing.T) {
	sl := newSpinlock(64)
	const numSlots = 16 // one cache line worth
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

	for i := int32(0); i < numSlots; i++ {
		if atomic.LoadUint64(&sl.slots[i]) != 0 {
			t.Fatalf("slot[%d] not zero after stress", i)
		}
	}
}

func TestSpinlock_ResetAll(t *testing.T) {
	sl := newSpinlock(128)

	sl.Lock(0)
	sl.Lock(1)
	sl.Lock(64)

	sl.ResetAll()

	for i, s := range sl.slots {
		if s != 0 {
			t.Fatalf("slot[%d] not zero after ResetAll: %d", i, s)
		}
	}
}

// --- Benchmarks ---

func BenchmarkSpinlock_LockUnlock_Uncontended(b *testing.B) {
	sl := newSpinlock(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sl.Lock(0)
		sl.Unlock(0)
	}
}

func BenchmarkSpinlock_LockUnlock_Parallel(b *testing.B) {
	sl := newSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sl.Lock(0)
			sl.Unlock(0)
		}
	})
}

func BenchmarkSpinlock_LockUnlock_DifferentSlots(b *testing.B) {
	sl := newSpinlock(1024)
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
