//go:build !trace

package bench_test

import (
	"fmt"
	"math/rand"
	"runtime"
	"testing"

	sieve "github.com/opencoff/go-sieve"

	arc "github.com/hashicorp/golang-lru/arc/v2"
	lru "github.com/hashicorp/golang-lru/v2"
)

// BenchmarkGet_Parallel measures concurrent read throughput.
func BenchmarkGet_Parallel(b *testing.B) {
	const cacheSize = 8192

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](cacheSize))
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Get(r.Intn(cacheSize))
			}
		})
	})

	b.Run("LRU", func(b *testing.B) {
		c, _ := lru.New[int, int](cacheSize)
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Get(r.Intn(cacheSize))
			}
		})
	})

	b.Run("ARC", func(b *testing.B) {
		c, _ := arc.NewARC[int, int](cacheSize)
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Get(r.Intn(cacheSize))
			}
		})
	})
}

// BenchmarkAdd_Parallel measures concurrent write throughput with eviction.
func BenchmarkAdd_Parallel(b *testing.B) {
	const cacheSize = 8192
	const keyRange = cacheSize * 2

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](cacheSize))
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				c.Add(k, k)
			}
		})
	})

	b.Run("LRU", func(b *testing.B) {
		c, _ := lru.New[int, int](cacheSize)
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				c.Add(k, k)
			}
		})
	})

	b.Run("ARC", func(b *testing.B) {
		c, _ := arc.NewARC[int, int](cacheSize)
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				c.Add(k, k)
			}
		})
	})
}

// BenchmarkProbe_Parallel measures concurrent Probe (get-or-insert)
// throughput. SIEVE only: LRU and ARC have no semantically-equivalent
// method — their PeekOrAdd/ContainsOrAdd skip recency promotion, which
// would degrade eviction quality. See bench/README.md for details.
func BenchmarkProbe_Parallel(b *testing.B) {
	const cacheSize = 8192
	const keyRange = cacheSize * 2

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](cacheSize))
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				c.Probe(k, k)
			}
		})
	})
}

// BenchmarkDelete_Parallel measures concurrent Delete/Remove throughput.
// The cache is pre-filled with keyRange entries (keyRange > cacheSize so
// eviction fires during pre-fill). Goroutines then delete random keys
// from [0, keyRange); the hit/miss mix shifts toward "not present" as
// the cache drains, which is representative of delete traffic under
// churn.
func BenchmarkDelete_Parallel(b *testing.B) {
	const cacheSize = 8192
	const keyRange = cacheSize * 2

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](cacheSize))
		for i := 0; i < keyRange; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Delete(r.Intn(keyRange))
			}
		})
	})

	b.Run("LRU", func(b *testing.B) {
		c, _ := lru.New[int, int](cacheSize)
		for i := 0; i < keyRange; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Remove(r.Intn(keyRange))
			}
		})
	})

	b.Run("ARC", func(b *testing.B) {
		c, _ := arc.NewARC[int, int](cacheSize)
		for i := 0; i < keyRange; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				c.Remove(r.Intn(keyRange))
			}
		})
	})
}

// BenchmarkMixed_Parallel measures 60% Get / 30% Add / 10% Delete.
func BenchmarkMixed_Parallel(b *testing.B) {
	const cacheSize = 8192
	const keyRange = cacheSize * 2

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](cacheSize))
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				op := r.Intn(10)
				switch {
				case op < 6: // 60% Get
					c.Get(k)
				case op < 9: // 30% Add
					c.Add(k, k)
				default: // 10% Delete
					c.Delete(k)
				}
			}
		})
	})

	b.Run("LRU", func(b *testing.B) {
		c, _ := lru.New[int, int](cacheSize)
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				op := r.Intn(10)
				switch {
				case op < 6:
					c.Get(k)
				case op < 9:
					c.Add(k, k)
				default:
					c.Remove(k)
				}
			}
		})
	})

	b.Run("ARC", func(b *testing.B) {
		c, _ := arc.NewARC[int, int](cacheSize)
		for i := 0; i < cacheSize; i++ {
			c.Add(i, i)
		}
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			r := rand.New(rand.NewSource(rand.Int63()))
			for pb.Next() {
				k := r.Intn(keyRange)
				op := r.Intn(10)
				switch {
				case op < 6:
					c.Get(k)
				case op < 9:
					c.Add(k, k)
				default:
					c.Remove(k)
				}
			}
		})
	})
}

// BenchmarkMemoryFootprint measures heap allocation delta at various cache sizes.
func BenchmarkMemoryFootprint(b *testing.B) {
	for _, size := range []int{100_000, 500_000, 1_000_000} {
		name := formatSize(size)

		b.Run(name+"/Sieve", func(b *testing.B) {
			for range b.N {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)

				c := sieve.Must(sieve.New[int, int](size))
				for i := 0; i < size; i++ {
					c.Add(i, i)
				}

				runtime.GC()
				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "heap-bytes")
				b.ReportMetric(float64(after.HeapObjects-before.HeapObjects), "heap-objects")
			}
		})

		b.Run(name+"/LRU", func(b *testing.B) {
			for range b.N {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)

				c, _ := lru.New[int, int](size)
				for i := 0; i < size; i++ {
					c.Add(i, i)
				}

				runtime.GC()
				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "heap-bytes")
				b.ReportMetric(float64(after.HeapObjects-before.HeapObjects), "heap-objects")
			}
		})

		b.Run(name+"/ARC", func(b *testing.B) {
			for range b.N {
				var before, after runtime.MemStats
				runtime.GC()
				runtime.ReadMemStats(&before)

				c, _ := arc.NewARC[int, int](size)
				for i := 0; i < size; i++ {
					c.Add(i, i)
				}

				runtime.GC()
				runtime.ReadMemStats(&after)
				b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "heap-bytes")
				b.ReportMetric(float64(after.HeapObjects-before.HeapObjects), "heap-objects")
			}
		})
	}
}

// BenchmarkGCImpact measures GC pause times at 1M entries under mixed workload.
func BenchmarkGCImpact(b *testing.B) {
	const size = 1_000_000
	const keyRange = size * 2

	b.Run("Sieve", func(b *testing.B) {
		c := sieve.Must(sieve.New[int, int](size))
		for i := 0; i < size; i++ {
			c.Add(i, i)
		}

		b.ResetTimer()
		for range b.N {
			r := rand.New(rand.NewSource(rand.Int63()))
			// Do some mixed ops to keep the cache active
			for j := 0; j < 1000; j++ {
				k := r.Intn(keyRange)
				if r.Intn(2) == 0 {
					c.Get(k)
				} else {
					c.Add(k, k)
				}
			}
			// Force GC and measure
			var stats runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&stats)
			b.ReportMetric(float64(stats.PauseTotalNs)/float64(stats.NumGC), "avg-gc-pause-ns")
		}
	})

	b.Run("LRU", func(b *testing.B) {
		c, _ := lru.New[int, int](size)
		for i := 0; i < size; i++ {
			c.Add(i, i)
		}

		b.ResetTimer()
		for range b.N {
			r := rand.New(rand.NewSource(rand.Int63()))
			for j := 0; j < 1000; j++ {
				k := r.Intn(keyRange)
				if r.Intn(2) == 0 {
					c.Get(k)
				} else {
					c.Add(k, k)
				}
			}
			var stats runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&stats)
			b.ReportMetric(float64(stats.PauseTotalNs)/float64(stats.NumGC), "avg-gc-pause-ns")
		}
	})

	b.Run("ARC", func(b *testing.B) {
		c, _ := arc.NewARC[int, int](size)
		for i := 0; i < size; i++ {
			c.Add(i, i)
		}

		b.ResetTimer()
		for range b.N {
			r := rand.New(rand.NewSource(rand.Int63()))
			for j := 0; j < 1000; j++ {
				k := r.Intn(keyRange)
				if r.Intn(2) == 0 {
					c.Get(k)
				} else {
					c.Add(k, k)
				}
			}
			var stats runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&stats)
			b.ReportMetric(float64(stats.PauseTotalNs)/float64(stats.NumGC), "avg-gc-pause-ns")
		}
	})
}

// BenchmarkZipf_Get_Parallel measures concurrent read throughput under Zipfian
// access distribution, comparing Sieve vs LRU vs ARC.
func BenchmarkZipf_Get_Parallel(b *testing.B) {
	const cacheSize = 8192
	const keyRange = cacheSize * 2
	const seqLen = 256 << 10 // 256K samples

	for _, skew := range []float64{1.01, 1.20, 1.50} {
		name := fmt.Sprintf("s=%.2f", skew)

		b.Run(name+"/Sieve", func(b *testing.B) {
			c := sieve.Must(sieve.New[int, int](cacheSize))
			seq := zipfSequence(seqLen, keyRange, skew, 42)
			for _, k := range seq {
				if _, ok := c.Get(k); !ok {
					c.Add(k, k)
				}
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffled(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					c.Get(local[i%n])
					i++
				}
			})
		})

		b.Run(name+"/LRU", func(b *testing.B) {
			c, _ := lru.New[int, int](cacheSize)
			seq := zipfSequence(seqLen, keyRange, skew, 42)
			for _, k := range seq {
				if _, ok := c.Get(k); !ok {
					c.Add(k, k)
				}
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffled(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					c.Get(local[i%n])
					i++
				}
			})
		})

		b.Run(name+"/ARC", func(b *testing.B) {
			c, _ := arc.NewARC[int, int](cacheSize)
			seq := zipfSequence(seqLen, keyRange, skew, 42)
			for _, k := range seq {
				if _, ok := c.Get(k); !ok {
					c.Add(k, k)
				}
			}
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				local := shuffled(seq, rand.Int63())
				n := len(local)
				i := 0
				for pb.Next() {
					c.Get(local[i%n])
					i++
				}
			})
		})
	}
}

func zipfSequence(n, keySpace int, s float64, seed int64) []int {
	r := rand.New(rand.NewSource(seed))
	z := rand.NewZipf(r, s, 1.0, uint64(keySpace-1))
	out := make([]int, n)
	for i := range out {
		out[i] = int(z.Uint64())
	}
	return out
}

func shuffled(src []int, seed int64) []int {
	dst := make([]int, len(src))
	copy(dst, src)
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(dst), func(i, j int) {
		dst[i], dst[j] = dst[j], dst[i]
	})
	return dst
}

func formatSize(n int) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%dK", n/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}
