// validate_test.go - invariant checker for Phase 2 index-based list
package sieve_test

import (
	"fmt"
	"testing"

	"github.com/opencoff/go-sieve"
)

// validate checks all structural invariants of a Sieve cache.
// It uses the public API (Len, Cap, Dump) to verify consistency.
// For thorough internal validation, we exercise operations and check
// that Len() matches expected counts after every operation.
func validate(t *testing.T, s *sieve.Sieve[int, int], context string) {
	t.Helper()

	length := s.Len()
	capacity := s.Cap()

	if length < 0 {
		t.Fatalf("%s: negative Len()=%d", context, length)
	}
	if length > capacity {
		t.Fatalf("%s: Len()=%d exceeds Cap()=%d", context, length, capacity)
	}

	// Dump walks head→tail via next links. Count nodes in the dump.
	dump := s.Dump()
	nodeCount := 0
	for _, line := range splitLines(dump) {
		// Node lines start with "  " or ">>"
		if len(line) >= 2 && (line[:2] == "  " || line[:2] == ">>") {
			nodeCount++
		}
	}

	if nodeCount != length {
		t.Fatalf("%s: Dump node count %d != Len() %d\nDump:\n%s", context, nodeCount, length, dump)
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// TestInvariants exercises validate() after every operation.
func TestInvariants(t *testing.T) {
	const cap = 8
	s := sieve.New[int, int](cap)
	validate(t, s, "empty cache")

	// Add items up to capacity
	for i := 0; i < cap; i++ {
		s.Add(i, i*10)
		validate(t, s, fmt.Sprintf("after Add(%d)", i))

		if s.Len() != i+1 {
			t.Fatalf("after Add(%d): expected Len()=%d, got %d", i, i+1, s.Len())
		}
	}

	// Get each item (sets visited)
	for i := 0; i < cap; i++ {
		v, ok := s.Get(i)
		if !ok {
			t.Fatalf("Get(%d): expected hit", i)
		}
		if v != i*10 {
			t.Fatalf("Get(%d): expected %d, got %d", i, i*10, v)
		}
		validate(t, s, fmt.Sprintf("after Get(%d)", i))
	}

	// Add beyond capacity — triggers eviction
	for i := cap; i < cap*2; i++ {
		s.Add(i, i*10)
		validate(t, s, fmt.Sprintf("after Add(%d) with eviction", i))

		if s.Len() != cap {
			t.Fatalf("after Add(%d): expected Len()=%d, got %d", i, cap, s.Len())
		}
	}

	// Update existing keys
	for i := cap; i < cap*2; i++ {
		_, r := s.Add(i, i*100)
		if !r.Hit() {
			t.Fatalf("Add(%d) update: expected true (key exists)", i)
		}
		validate(t, s, fmt.Sprintf("after update Add(%d)", i))
	}

	// Delete some keys
	for i := cap; i < cap+4; i++ {
		ok := s.Delete(i)
		if !ok {
			t.Fatalf("Delete(%d): expected true", i)
		}
		validate(t, s, fmt.Sprintf("after Delete(%d)", i))
	}

	expectedLen := cap - 4
	if s.Len() != expectedLen {
		t.Fatalf("after deletions: expected Len()=%d, got %d", expectedLen, s.Len())
	}

	// Delete non-existent key
	ok := s.Delete(99999)
	if ok {
		t.Fatal("Delete(99999): expected false for non-existent key")
	}
	validate(t, s, "after Delete(non-existent)")

	// Probe existing and non-existing
	for i := cap + 4; i < cap*2; i++ {
		v, _, r := s.Probe(i, -1)
		if !r.Hit() {
			t.Fatalf("Probe(%d): expected hit", i)
		}
		if v != i*100 {
			t.Fatalf("Probe(%d): expected %d, got %d", i, i*100, v)
		}
		validate(t, s, fmt.Sprintf("after Probe(%d) hit", i))
	}

	// Probe new keys to fill back up
	for i := 0; i < 4; i++ {
		key := cap*2 + i
		v, _, r := s.Probe(key, key*10)
		if r.Hit() {
			t.Fatalf("Probe(%d): expected miss", key)
		}
		if v != key*10 {
			t.Fatalf("Probe(%d): expected val=%d, got %d", key, key*10, v)
		}
		validate(t, s, fmt.Sprintf("after Probe(%d) miss", key))
	}

	if s.Len() != cap {
		t.Fatalf("after refill: expected Len()=%d, got %d", cap, s.Len())
	}

	// Purge
	s.Purge()
	validate(t, s, "after Purge")
	if s.Len() != 0 {
		t.Fatalf("after Purge: expected Len()=0, got %d", s.Len())
	}

	// Re-add after purge
	for i := 0; i < cap; i++ {
		s.Add(i, i)
		validate(t, s, fmt.Sprintf("after re-Add(%d) post-purge", i))
	}

	// Force multiple rounds of eviction to exercise hand wrap-around
	for i := 0; i < cap*3; i++ {
		s.Add(cap+i, cap+i)
		validate(t, s, fmt.Sprintf("after churn Add(%d)", cap+i))
	}
}

// TestInvariants_LargerScale runs invariant checks at a larger scale.
func TestInvariants_LargerScale(t *testing.T) {
	const cap = 128
	s := sieve.New[int, int](cap)

	// Fill, evict, delete in various patterns
	for i := 0; i < cap*4; i++ {
		s.Add(i, i)
	}
	validate(t, s, "after bulk fill")
	if s.Len() != cap {
		t.Fatalf("expected Len()=%d, got %d", cap, s.Len())
	}

	// Delete every other key that exists
	deleted := 0
	for i := cap * 3; i < cap*4; i += 2 {
		if s.Delete(i) {
			deleted++
		}
	}
	validate(t, s, "after alternating deletes")
	if s.Len() != cap-deleted {
		t.Fatalf("expected Len()=%d, got %d", cap-deleted, s.Len())
	}

	// Re-add to fill back up
	for i := cap * 4; i < cap*5; i++ {
		s.Add(i, i)
	}
	validate(t, s, "after re-fill")
	if s.Len() != cap {
		t.Fatalf("expected Len()=%d, got %d", cap, s.Len())
	}
}
