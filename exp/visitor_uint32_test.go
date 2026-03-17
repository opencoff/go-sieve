package sieve

import (
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

// --- visitedBool (k=1) tests ---

func TestVisitedBool_SetClearTest(t *testing.T) {
	v := newVisitedBool(256)

	for i := int32(0); i < 256; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear initially", i)
		}
	}

	for i := int32(0); i < 256; i += 2 {
		v.Mark(i)
	}

	for i := int32(0); i < 256; i++ {
		expected := i%2 == 0
		if v.IsVisited(i) != expected {
			t.Fatalf("slot %d: expected %v, got %v", i, expected, v.IsVisited(i))
		}
	}

	for i := int32(0); i < 256; i += 2 {
		v.Clear(i)
	}

	for i := int32(0); i < 256; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear after Clear", i)
		}
	}

	for i := int32(0); i < 256; i++ {
		v.Mark(i)
	}
	v.ResetAll()
	for i := int32(0); i < 256; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear after ResetAll", i)
		}
	}
}

func TestVisitedBool_MarkIdempotent(t *testing.T) {
	v := newVisitedBool(128)

	for i := 0; i < 100; i++ {
		v.Mark(42)
	}
	if !v.IsVisited(42) {
		t.Fatal("slot 42 should be set")
	}

	for i := 0; i < 100; i++ {
		v.Clear(42)
	}
	if v.IsVisited(42) {
		t.Fatal("slot 42 should be clear")
	}
}

func TestVisitedBool_Concurrent(t *testing.T) {
	const (
		capacity   = 1024
		goroutines = 64
		opsPerG    = 10000
	)

	v := newVisitedBool(capacity)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerG; i++ {
				idx := int32(r.Intn(capacity))
				switch r.Intn(3) {
				case 0:
					v.Mark(idx)
				case 1:
					v.Clear(idx)
				case 2:
					v.IsVisited(idx)
				}
			}
		}(int64(g))
	}
	wg.Wait()

	v.ResetAll()
	for i := int32(0); i < capacity; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear after ResetAll", i)
		}
	}
}

// --- visitedCounter (k>1) tests ---

func TestVisitedCounter_SaturatingK3(t *testing.T) {
	v := newVisitedCounter(64, 3)

	// Mark 5 times — should saturate at 3
	for i := 0; i < 5; i++ {
		v.Mark(0)
	}
	if atomic.LoadUint32(&v.slots[0]) != 3 {
		t.Fatalf("expected 3, got %d", atomic.LoadUint32(&v.slots[0]))
	}
	if !v.IsVisited(0) {
		t.Fatal("should be visited")
	}

	// Clear 3 times — should reach 0
	for i := 0; i < 3; i++ {
		v.Clear(0)
		if i < 2 && !v.IsVisited(0) {
			t.Fatalf("should still be visited after %d clears", i+1)
		}
	}
	if v.IsVisited(0) {
		t.Fatal("should not be visited after 3 clears")
	}

	// Clear again — idempotent at 0
	v.Clear(0)
	if atomic.LoadUint32(&v.slots[0]) != 0 {
		t.Fatalf("expected 0, got %d", atomic.LoadUint32(&v.slots[0]))
	}
}

func TestVisitedCounter_SetClear(t *testing.T) {
	v := newVisitedCounter(256, 3)

	for i := int32(0); i < 256; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear initially", i)
		}
	}

	// Mark each slot once
	for i := int32(0); i < 256; i++ {
		v.Mark(i)
	}
	for i := int32(0); i < 256; i++ {
		if !v.IsVisited(i) {
			t.Fatalf("slot %d should be visited after Mark", i)
		}
	}

	// Clear each slot once (counter goes from 1 to 0)
	for i := int32(0); i < 256; i++ {
		v.Clear(i)
	}
	for i := int32(0); i < 256; i++ {
		if v.IsVisited(i) {
			t.Fatalf("slot %d should be clear after Clear", i)
		}
	}
}

func TestVisitedCounter_Concurrent(t *testing.T) {
	const (
		capacity   = 1024
		goroutines = 64
		opsPerG    = 10000
	)

	v := newVisitedCounter(capacity, 3)

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < opsPerG; i++ {
				idx := int32(r.Intn(capacity))
				switch r.Intn(3) {
				case 0:
					v.Mark(idx)
				case 1:
					v.Clear(idx)
				case 2:
					v.IsVisited(idx)
				}
			}
		}(int64(g))
	}
	wg.Wait()

	for i := int32(0); i < capacity; i++ {
		if atomic.LoadUint32(&v.slots[i]) > 3 {
			t.Fatalf("slot %d overflowed: %d", i, atomic.LoadUint32(&v.slots[i]))
		}
	}
}

// --- Benchmarks ---

func BenchmarkVisitedBool_Mark(b *testing.B) {
	v := newVisitedBool(1 << 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Mark(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedBool_Mark_Contended(b *testing.B) {
	v := newVisitedBool(64)
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			v.Mark(int32(r.Intn(64)))
		}
	})
}

func BenchmarkVisitedBool_IsVisited(b *testing.B) {
	v := newVisitedBool(1 << 20)
	for i := int32(0); i < 1<<20; i += 2 {
		v.Mark(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.IsVisited(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedBool_Clear(b *testing.B) {
	v := newVisitedBool(1 << 20)
	for i := int32(0); i < 1<<20; i++ {
		v.Mark(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Clear(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedCounter_Mark(b *testing.B) {
	v := newVisitedCounter(1<<20, 3)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.Mark(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedCounter_Mark_Contended(b *testing.B) {
	v := newVisitedCounter(64, 3)
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			v.Mark(int32(r.Intn(64)))
		}
	})
}
