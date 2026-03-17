package sieve

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

func TestRWSpinlock_BasicReadLock(t *testing.T) {
	rw := newRWSpinlock(64)

	rw.RLock(0)
	rw.RUnlock(0)

	if rw.words[0] != 0 {
		t.Fatalf("slot not zero after RUnlock: %016x", rw.words[0])
	}
}

func TestRWSpinlock_BasicWriteLock(t *testing.T) {
	rw := newRWSpinlock(64)

	rw.Lock(0)

	// Writer bit (bit 63) should be set, no readers
	if rw.words[0] != _WriterBit {
		t.Fatalf("expected writer bit 0x%016x, got 0x%016x", _WriterBit, rw.words[0])
	}

	rw.Unlock(0)
	if rw.words[0] != 0 {
		t.Fatalf("slot not zero after Unlock: %016x", rw.words[0])
	}
}

func TestRWSpinlock_MultipleReaders(t *testing.T) {
	rw := newRWSpinlock(64)
	const idx = int32(5)
	const numReaders = 50

	for i := 0; i < numReaders; i++ {
		rw.RLock(idx)
	}

	// Reader count is stored directly in bits 0-62
	val := rw.words[idx]
	if val != uint64(numReaders) {
		t.Fatalf("expected reader count %d, got %d", numReaders, val)
	}

	for i := 0; i < numReaders; i++ {
		rw.RUnlock(idx)
	}

	if rw.words[idx] != 0 {
		t.Fatalf("expected 0 after all RUnlock, got %d", rw.words[idx])
	}
}

func TestRWSpinlock_WriterExcludesReaders(t *testing.T) {
	rw := newRWSpinlock(64)
	const idx = int32(3)

	rw.Lock(idx)

	acquired := make(chan struct{})
	go func() {
		rw.RLock(idx)
		close(acquired)
		rw.RUnlock(idx)
	}()

	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}

	select {
	case <-acquired:
		t.Fatal("reader acquired lock while writer held it")
	default:
	}

	rw.Unlock(idx)
	<-acquired
}

func TestRWSpinlock_ReaderExcludesWriter(t *testing.T) {
	rw := newRWSpinlock(64)
	const idx = int32(7)

	rw.RLock(idx)

	acquired := make(chan struct{})
	go func() {
		rw.Lock(idx)
		close(acquired)
		rw.Unlock(idx)
	}()

	for i := 0; i < 100; i++ {
		runtime.Gosched()
	}

	select {
	case <-acquired:
		t.Fatal("writer acquired lock while reader held it")
	default:
	}

	rw.RUnlock(idx)
	<-acquired
}

func TestRWSpinlock_DifferentSlotsDontInterfere(t *testing.T) {
	rw := newRWSpinlock(64)

	// Lock slot 0 for writing
	rw.Lock(0)

	// Other slots are fully independent (separate uint64 words)
	rw.RLock(1)
	rw.RUnlock(1)

	rw.Lock(1)
	rw.Unlock(1)

	rw.RLock(7)
	rw.RUnlock(7)

	rw.RLock(8)
	rw.RUnlock(8)

	rw.Unlock(0)
}

func TestRWSpinlock_ConcurrentReaders(t *testing.T) {
	rw := newRWSpinlock(1024)
	const idx = int32(42)
	const numGoroutines = 100
	const opsPerGoroutine = 10_000

	var totalOps atomic.Int64

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	for g := 0; g < numGoroutines; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				rw.RLock(idx)
				totalOps.Add(1)
				rw.RUnlock(idx)
			}
		}()
	}
	wg.Wait()

	expected := int64(numGoroutines) * int64(opsPerGoroutine)
	if totalOps.Load() != expected {
		t.Fatalf("expected %d ops, got %d", expected, totalOps.Load())
	}
}

func TestRWSpinlock_ConcurrentReadWrite(t *testing.T) {
	rw := newRWSpinlock(64)
	const idx = int32(0)
	const numReaders = 8
	const numWriters = 2
	const opsPerGoroutine = 50_000

	var field1, field2 int64

	var wg sync.WaitGroup
	var tornReads atomic.Int64

	wg.Add(numReaders)
	for g := 0; g < numReaders; g++ {
		go func() {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				rw.RLock(idx)
				f1 := atomic.LoadInt64(&field1)
				f2 := atomic.LoadInt64(&field2)
				rw.RUnlock(idx)
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
				rw.Lock(idx)
				atomic.StoreInt64(&field1, val)
				atomic.StoreInt64(&field2, val)
				rw.Unlock(idx)
			}
		}(g)
	}

	wg.Wait()

	if torn := tornReads.Load(); torn != 0 {
		t.Fatalf("detected %d torn reads", torn)
	}
}

func TestRWSpinlock_StressMultiSlot(t *testing.T) {
	rw := newRWSpinlock(64)
	const numSlots = 8
	const numGoroutines = 4
	const opsPerGoroutine = 20_000

	var wg sync.WaitGroup

	for slot := int32(0); slot < numSlots; slot++ {
		wg.Add(numGoroutines)
		for g := 0; g < numGoroutines-1; g++ {
			go func(s int32) {
				defer wg.Done()
				for i := 0; i < opsPerGoroutine; i++ {
					rw.RLock(s)
					runtime.Gosched()
					rw.RUnlock(s)
				}
			}(slot)
		}
		go func(s int32) {
			defer wg.Done()
			for i := 0; i < opsPerGoroutine; i++ {
				rw.Lock(s)
				rw.Unlock(s)
			}
		}(slot)
	}

	wg.Wait()

	// Each slot is its own word now; check all 8
	for i := int32(0); i < numSlots; i++ {
		if rw.words[i] != 0 {
			t.Fatalf("words[%d] not zero after stress: %016x", i, rw.words[i])
		}
	}
}

func TestRWSpinlock_ResetAll(t *testing.T) {
	rw := newRWSpinlock(64)

	rw.RLock(0)
	rw.RLock(1)
	rw.Lock(2)

	rw.ResetAll()

	for i, w := range rw.words {
		if w != 0 {
			t.Fatalf("word[%d] not zero after ResetAll: %016x", i, w)
		}
	}
}

// --- Benchmarks ---

func BenchmarkRWSpinlock_RLockUnlock_Uncontended(b *testing.B) {
	rw := newRWSpinlock(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.RLock(0)
		rw.RUnlock(0)
	}
}

func BenchmarkRWSpinlock_RLockUnlock_Parallel(b *testing.B) {
	rw := newRWSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rw.RLock(0)
			rw.RUnlock(0)
		}
	})
}

func BenchmarkRWSpinlock_RLockUnlock_DifferentSlots(b *testing.B) {
	rw := newRWSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		id := int32(runtime.GOMAXPROCS(0))
		slot := int32(0)
		for pb.Next() {
			rw.RLock(slot % id)
			rw.RUnlock(slot % id)
			slot++
		}
	})
}

func BenchmarkRWSpinlock_LockUnlock_Uncontended(b *testing.B) {
	rw := newRWSpinlock(1024)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rw.Lock(0)
		rw.Unlock(0)
	}
}

func BenchmarkRWSpinlock_Mixed_Parallel(b *testing.B) {
	rw := newRWSpinlock(1024)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%5 == 0 {
				rw.Lock(0)
				rw.Unlock(0)
			} else {
				rw.RLock(0)
				rw.RUnlock(0)
			}
			i++
		}
	})
}
