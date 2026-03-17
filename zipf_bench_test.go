// zipf_bench_test.go — Zipfian synthetic benchmarks for slotState and Sieve
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
	"math/rand"
	"testing"
)

// =========================================================================
// Helpers
// =========================================================================

const (
	_ZipfSeqLen    = 256 << 10 // 256K samples per sequence
	_ZipfCacheSize = 8192
	_ZipfKeyRange  = _ZipfCacheSize * 2 // half the keys won't fit in cache
)

var zipfSkews = []struct {
	name string
	s    float64
}{
	{"s=1.01", 1.01},
	{"s=1.20", 1.2},
	{"s=1.50", 1.5},
}

// zipfIndices generates n indices from Zipf(s, v=1) over [0, keySpace).
// Requires s > 1 (Go's rand.NewZipf constraint).
func zipfIndices(n, keySpace int, s float64, seed int64) []int32 {
	r := rand.New(rand.NewSource(seed))
	z := rand.NewZipf(r, s, 1.0, uint64(keySpace-1))
	out := make([]int32, n)
	for i := range out {
		out[i] = int32(z.Uint64())
	}
	return out
}

// shuffledCopy returns a new slice with the same elements as src, shuffled.
func shuffledCopy(src []int32, seed int64) []int32 {
	dst := make([]int32, len(src))
	copy(dst, src)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(dst), func(i, j int) {
		dst[i], dst[j] = dst[j], dst[i]
	})
	return dst
}

// =========================================================================
// slotState — Zipfian contention benchmarks
// =========================================================================

// The Get() hot path: LockAndMark + Unlock under Zipfian contention, k=1.
func BenchmarkSlotState_Zipf_K1(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			ss := newSlotState(_ZipfCacheSize, 1)
			seq := zipfIndices(_ZipfSeqLen, _ZipfCacheSize, sk.s, 42)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					idx := local[i%n]
					ss.LockAndMark(idx)
					ss.Unlock(idx)
					i++
				}
			})
		})
	}
}

// Same as above but k=3 (Or + CAS path).
func BenchmarkSlotState_Zipf_K3(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			ss := newSlotState(_ZipfCacheSize, 3)
			seq := zipfIndices(_ZipfSeqLen, _ZipfCacheSize, sk.s, 42)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					idx := local[i%n]
					ss.LockAndMark(idx)
					ss.Unlock(idx)
					i++
				}
			})
		})
	}
}

// 90% LockAndMark+Unlock (Get path), 10% Clear (eviction scan).
func BenchmarkSlotState_Zipf_Mixed(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			ss := newSlotState(_ZipfCacheSize, 1)
			seq := zipfIndices(_ZipfSeqLen, _ZipfCacheSize, sk.s, 42)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				r := rand.New(rand.NewSource(rand.Int63()))
				n := len(local)
				i := 0
				for pb.Next() {
					idx := local[i%n]
					if r.Intn(10) < 9 {
						ss.LockAndMark(idx)
						ss.Unlock(idx)
					} else {
						ss.Clear(idx)
					}
					i++
				}
			})
		})
	}
}

// =========================================================================
// Sieve — Zipfian benchmarks
// =========================================================================

// Parallel Get on a warm cache. The cache is pre-warmed by replaying
// the Zipfian sequence (Get-or-Add), so the working set is established
// before timing starts.
func BenchmarkSieve_Zipf_Get(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			c := New[int, int](_ZipfCacheSize)
			seq := zipfIndices(_ZipfSeqLen, _ZipfKeyRange, sk.s, 42)

			// Warm up: establish working set
			for _, idx := range seq {
				k := int(idx)
				if _, ok := c.Get(k); !ok {
					c.Add(k, k)
				}
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					c.Get(int(local[i%n]))
					i++
				}
			})
		})
	}
}

// Parallel Get-or-Add: the steady-state cache pattern.
// On miss, the key is added (possibly triggering eviction).
func BenchmarkSieve_Zipf_GetOrAdd(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			c := New[int, int](_ZipfCacheSize)
			seq := zipfIndices(_ZipfSeqLen, _ZipfKeyRange, sk.s, 42)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					k := int(local[i%n])
					if _, ok := c.Get(k); !ok {
						c.Add(k, k)
					}
					i++
				}
			})
		})
	}
}

// Parallel Probe (insert-if-absent). Exercises the Probe-specific
// code path which is distinct from Get+Add.
func BenchmarkSieve_Zipf_Probe(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			c := New[int, int](_ZipfCacheSize)
			seq := zipfIndices(_ZipfSeqLen, _ZipfKeyRange, sk.s, 42)

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					k := int(local[i%n])
					c.Probe(k, k)
					i++
				}
			})
		})
	}
}

// 60% Get, 30% Add, 10% Delete — the standard mixed workload.
func BenchmarkSieve_Zipf_Mixed(b *testing.B) {
	for _, sk := range zipfSkews {
		b.Run(sk.name, func(b *testing.B) {
			c := New[int, int](_ZipfCacheSize)
			seq := zipfIndices(_ZipfSeqLen, _ZipfKeyRange, sk.s, 42)

			// Pre-fill so deletes have something to hit
			for i := 0; i < _ZipfCacheSize; i++ {
				c.Add(i, i)
			}

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffledCopy(seq, rand.Int63())
				r := rand.New(rand.NewSource(rand.Int63()))
				n := len(local)
				i := 0
				for pb.Next() {
					k := int(local[i%n])
					op := r.Intn(10)
					switch {
					case op < 6:
						c.Get(k)
					case op < 9:
						c.Add(k, k)
					default:
						c.Delete(k)
					}
					i++
				}
			})
		})
	}
}
