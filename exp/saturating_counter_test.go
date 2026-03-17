package sieve

import (
	"sync"
	"testing"
)

func TestSaturatingCounter_MarkAndClear(t *testing.T) {
	sc := newAtomicSaturatingCounter(128, 3)

	// Initially zero
	if v := sc.read(0); v != 0 {
		t.Fatalf("expected 0, got %d", v)
	}
	if sc.IsVisited(0) {
		t.Fatal("expected not visited")
	}

	// Mark (increment) to 1, 2, 3
	sc.Mark(0)
	if v := sc.read(0); v != 1 {
		t.Fatalf("expected 1, got %d", v)
	}
	sc.Mark(0)
	if v := sc.read(0); v != 2 {
		t.Fatalf("expected 2, got %d", v)
	}
	sc.Mark(0)
	if v := sc.read(0); v != 3 {
		t.Fatalf("expected 3, got %d", v)
	}

	// Saturates at 3
	sc.Mark(0)
	if v := sc.read(0); v != 3 {
		t.Fatalf("expected saturation at 3, got %d", v)
	}

	// Clear (decrement) to 2, 1, 0
	sc.Clear(0)
	if v := sc.read(0); v != 2 {
		t.Fatalf("expected 2 after clear, got %d", v)
	}
	sc.Clear(0)
	if v := sc.read(0); v != 1 {
		t.Fatalf("expected 1 after clear, got %d", v)
	}
	sc.Clear(0)
	if v := sc.read(0); v != 0 {
		t.Fatalf("expected 0 after clear, got %d", v)
	}

	// Saturates at 0
	sc.Clear(0)
	if v := sc.read(0); v != 0 {
		t.Fatalf("expected 0 after clear at zero, got %d", v)
	}
	if sc.IsVisited(0) {
		t.Fatal("expected not visited after clearing to 0")
	}
}

func TestSaturatingCounter_K1_MatchesBitfield(t *testing.T) {
	sc := newAtomicSaturatingCounter(256, 1)

	if sc.bitsPerSlot != 1 {
		t.Fatalf("expected bitsPerSlot=1, got %d", sc.bitsPerSlot)
	}
	if sc.slotsPerWord != 64 {
		t.Fatalf("expected slotsPerWord=64, got %d", sc.slotsPerWord)
	}

	sc.Mark(42)
	if !sc.IsVisited(42) {
		t.Fatal("expected visited after Mark")
	}
	// Saturates at 1
	sc.Mark(42)
	if v := sc.read(42); v != 1 {
		t.Fatalf("expected saturation at 1, got %d", v)
	}
	// Clear back to 0
	sc.Clear(42)
	if sc.IsVisited(42) {
		t.Fatal("expected not visited after Clear")
	}
}

func TestSaturatingCounter_AdjacentSlots(t *testing.T) {
	sc := newAtomicSaturatingCounter(128, 3)

	sc.Mark(0)
	sc.Mark(0)
	sc.Mark(0) // slot 0 = 3

	sc.Mark(1) // slot 1 = 1

	if v := sc.read(0); v != 3 {
		t.Fatalf("slot 0: expected 3, got %d", v)
	}
	if v := sc.read(1); v != 1 {
		t.Fatalf("slot 1: expected 1, got %d", v)
	}
	if v := sc.read(2); v != 0 {
		t.Fatalf("slot 2: expected 0, got %d", v)
	}
}

func TestSaturatingCounter_Reset(t *testing.T) {
	sc := newAtomicSaturatingCounter(64, 7)
	sc.Mark(5)
	sc.Mark(5)
	sc.Mark(5)
	sc.Reset(5)
	if v := sc.read(5); v != 0 {
		t.Fatalf("expected 0 after Reset, got %d", v)
	}
}

func TestSaturatingCounter_ResetAll(t *testing.T) {
	sc := newAtomicSaturatingCounter(128, 3)
	for i := int32(0); i < 128; i++ {
		sc.Mark(i)
	}
	sc.ResetAll()
	for i := int32(0); i < 128; i++ {
		if v := sc.read(i); v != 0 {
			t.Fatalf("slot %d: expected 0 after ResetAll, got %d", i, v)
		}
	}
}

func TestSaturatingCounter_Concurrent(t *testing.T) {
	sc := newAtomicSaturatingCounter(1024, 3)
	var wg sync.WaitGroup

	// Many goroutines marking the same slot
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				sc.Mark(42)
			}
		}()
	}
	wg.Wait()

	if v := sc.read(42); v != 3 {
		t.Fatalf("expected saturation at 3, got %d", v)
	}

	// Many goroutines clearing
	for g := 0; g < 100; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				sc.Clear(42)
			}
		}()
	}
	wg.Wait()

	if v := sc.read(42); v != 0 {
		t.Fatalf("expected 0 after mass clear, got %d", v)
	}
}

func BenchmarkSaturatingCounter_Mark(b *testing.B) {
	sc := newAtomicSaturatingCounter(1024, 3)
	for i := 0; i < b.N; i++ {
		sc.Mark(int32(i & 1023))
	}
}

func BenchmarkSaturatingCounter_Mark_Contended(b *testing.B) {
	sc := newAtomicSaturatingCounter(1024, 3)
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			sc.Mark(0)
		}
	})
}
