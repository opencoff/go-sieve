// visited_bits_test.go - unit tests and benchmarks for packed visited bitfield
package sieve

import (
	"math/rand"
	"sync"
	"testing"
)

func TestVisitedBits_SetClearTest(t *testing.T) {
	vb := newAtomicBitfield(256)

	// All bits should start clear
	for i := int32(0); i < 256; i++ {
		if vb.IsVisited(i) {
			t.Fatalf("bit %d should be clear initially", i)
		}
	}

	// Set every other bit
	for i := int32(0); i < 256; i += 2 {
		vb.Mark(i)
	}

	// Verify pattern
	for i := int32(0); i < 256; i++ {
		expected := i%2 == 0
		if vb.IsVisited(i) != expected {
			t.Fatalf("bit %d: expected %v, got %v", i, expected, vb.IsVisited(i))
		}
	}

	// Clear the even bits
	for i := int32(0); i < 256; i += 2 {
		vb.Clear(i)
	}

	// All should be clear again
	for i := int32(0); i < 256; i++ {
		if vb.IsVisited(i) {
			t.Fatalf("bit %d should be clear after Clear", i)
		}
	}

	// Set all, then Reset
	for i := int32(0); i < 256; i++ {
		vb.Mark(i)
	}
	vb.ResetAll()
	for i := int32(0); i < 256; i++ {
		if vb.IsVisited(i) {
			t.Fatalf("bit %d should be clear after Reset", i)
		}
	}
}

func TestVisitedBits_SetIdempotent(t *testing.T) {
	vb := newAtomicBitfield(128)

	// Setting the same bit multiple times should be fine
	for i := 0; i < 100; i++ {
		vb.Mark(42)
	}
	if !vb.IsVisited(42) {
		t.Fatal("bit 42 should be set")
	}

	// Clearing the same bit multiple times should be fine
	for i := 0; i < 100; i++ {
		vb.Clear(42)
	}
	if vb.IsVisited(42) {
		t.Fatal("bit 42 should be clear")
	}
}

func TestVisitedBits_WordBoundaries(t *testing.T) {
	vb := newAtomicBitfield(256)

	// Test bits at word boundaries (63, 64, 127, 128)
	boundaries := []int32{0, 1, 62, 63, 64, 65, 126, 127, 128, 129, 255}
	for _, idx := range boundaries {
		vb.Mark(idx)
		if !vb.IsVisited(idx) {
			t.Fatalf("bit %d should be set", idx)
		}
	}

	// Verify only boundary bits are set
	for i := int32(0); i < 256; i++ {
		isBoundary := false
		for _, b := range boundaries {
			if i == b {
				isBoundary = true
				break
			}
		}
		if vb.IsVisited(i) != isBoundary {
			t.Fatalf("bit %d: expected %v, got %v", i, isBoundary, vb.IsVisited(i))
		}
	}
}

func TestVisitedBits_Concurrent(t *testing.T) {
	const (
		capacity   = 1024
		goroutines = 64
		opsPerG    = 10000
	)

	vb := newAtomicBitfield(capacity)

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
					vb.Mark(idx)
				case 1:
					vb.Clear(idx)
				case 2:
					vb.IsVisited(idx)
				}
			}
		}(int64(g))
	}
	wg.Wait()

	// No crash or race detector complaint = pass
	// Verify Reset works after concurrent abuse
	vb.ResetAll()
	for i := int32(0); i < capacity; i++ {
		if vb.IsVisited(i) {
			t.Fatalf("bit %d should be clear after Reset", i)
		}
	}
}

func BenchmarkVisitedBits_Set(b *testing.B) {
	vb := newAtomicBitfield(1 << 20) // 1M bits
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vb.Mark(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedBits_Set_Contended(b *testing.B) {
	vb := newAtomicBitfield(64) // single word — maximum contention
	b.RunParallel(func(pb *testing.PB) {
		r := rand.New(rand.NewSource(rand.Int63()))
		for pb.Next() {
			idx := int32(r.Intn(64))
			vb.Mark(idx)
		}
	})
}

func BenchmarkVisitedBits_Test(b *testing.B) {
	vb := newAtomicBitfield(1 << 20)
	// Set half the bits
	for i := int32(0); i < 1<<20; i += 2 {
		vb.Mark(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vb.IsVisited(int32(i % (1 << 20)))
	}
}

func BenchmarkVisitedBits_Clear(b *testing.B) {
	vb := newAtomicBitfield(1 << 20)
	// Set all bits first
	for i := int32(0); i < 1<<20; i++ {
		vb.Mark(i)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vb.Clear(int32(i % (1 << 20)))
	}
}
