// sieve_bench_custom_test.go - benchmarks for Sieve cache with custom memory allocator
//
// (c) 2024 Sudhi Herle <sudhi@herle.net>
//
// Copyright 2024- Sudhi Herle <sw-at-herle-dot-net>
// License: BSD-2-Clause

package sieve_test

import (
	"fmt"
	"math/rand"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"github.com/opencoff/go-sieve"
)

// BenchmarkSieveAdd benchmarks the Add operation
func BenchmarkSieveAdd(b *testing.B) {
	// Test with various cache sizes
	for _, cacheSize := range []int{1024, 8192, 32768} {
		b.Run(fmt.Sprintf("CacheSize_%d", cacheSize), func(b *testing.B) {
			cache := sieve.New[int, int](cacheSize)

			// Generate keys with some predictable access pattern
			keys := make([]int, b.N)
			for i := 0; i < b.N; i++ {
				if i%3 == 0 {
					// Reuse some keys for cache hits
					keys[i] = i % (cacheSize / 2)
				} else {
					// Use new keys for cache misses
					keys[i] = i + cacheSize
				}
			}

			b.ResetTimer()

			// Perform add operations that will cause evictions
			for i := 0; i < b.N; i++ {
				key := keys[i]
				cache.Add(key, key)

				// Occasionally delete some items to test the node recycling
				if i%5 == 0 && i > 0 {
					cache.Delete(keys[i-1])
				}
			}
		})
	}
}

// BenchmarkSieveGetHitMiss benchmarks Get operations with a mix of hits and misses
func BenchmarkSieveGetHitMiss(b *testing.B) {
	cacheSize := 8192
	cache := sieve.New[int, int](cacheSize)

	// Fill the cache with initial data
	for i := 0; i < cacheSize; i++ {
		cache.Add(i, i)
	}

	// Generate a mix of hit and miss patterns
	keys := make([]int, b.N)
	for i := 0; i < b.N; i++ {
		if i%2 == 0 {
			// Cache hit
			keys[i] = rand.Intn(cacheSize)
		} else {
			// Cache miss
			keys[i] = cacheSize + rand.Intn(cacheSize)
		}
	}

	b.ResetTimer()

	// Perform get operations
	var hit, miss int
	for i := 0; i < b.N; i++ {
		key := keys[i]
		if _, ok := cache.Get(key); ok {
			hit++
		} else {
			miss++
			// Add key that was a miss
			cache.Add(key, key)
		}
	}

	b.StopTimer()
	hitRatio := float64(hit) / float64(b.N)
	b.ReportMetric(hitRatio, "hit-ratio")
}

// BenchmarkSieveConcurrency benchmarks high concurrency operations
func BenchmarkSieveConcurrency(b *testing.B) {
	cacheSize := 16384
	cache := sieve.New[int, int](cacheSize)

	b.ResetTimer()

	// Run a highly concurrent benchmark with many goroutines
	b.RunParallel(func(pb *testing.PB) {
		// Each goroutine gets its own random seed
		r := rand.New(rand.NewSource(rand.Int63()))

		for pb.Next() {
			// Random operation: get, add, or delete
			op := r.Intn(10)
			key := r.Intn(cacheSize * 2) // Half will be misses

			if op < 6 { // 60% gets
				cache.Get(key)
			} else if op < 9 { // 30% adds
				cache.Add(key, key)
			} else { // 10% deletes
				cache.Delete(key)
			}
		}
	})
}

// BenchmarkSieveGCPressure specifically measures the impact on garbage collection
func BenchmarkSieveGCPressure(b *testing.B) {
	// Run with different cache sizes
	for _, cacheSize := range []int{1000, 10000, 50000} {
		b.Run(fmt.Sprintf("CacheSize_%d", cacheSize), func(b *testing.B) {
			// Fixed number of operations for consistent measurement
			operations := 1000000

			// Force GC before test
			runtime.GC()

			// Capture GC stats before
			var statsBefore debug.GCStats
			debug.ReadGCStats(&statsBefore)
			var memStatsBefore runtime.MemStats
			runtime.ReadMemStats(&memStatsBefore)

			startTime := time.Now()

			// Create cache with custom allocator
			cache := sieve.New[int, int](cacheSize)

			// Run the workload
			runSieveWorkload(cache, operations)

			elapsedTime := time.Since(startTime)

			// Force GC to get accurate stats
			runtime.GC()

			// Capture GC stats after
			var statsAfter debug.GCStats
			debug.ReadGCStats(&statsAfter)
			var memStatsAfter runtime.MemStats
			runtime.ReadMemStats(&memStatsAfter)

			// Report metrics
			gcCount := statsAfter.NumGC - statsBefore.NumGC
			pauseTotal := statsAfter.PauseTotal - statsBefore.PauseTotal

			b.ReportMetric(float64(gcCount), "GC-cycles")
			b.ReportMetric(float64(pauseTotal.Nanoseconds())/float64(operations), "GC-pause-ns/op")
			b.ReportMetric(float64(memStatsAfter.HeapObjects)/float64(operations), "heap-objs/op")
			b.ReportMetric(float64(operations)/elapsedTime.Seconds(), "ops/sec")
		})
	}
}

// BenchmarkEviction_LargeCache measures eviction scan time with 1M entries.
func BenchmarkEviction_LargeCache(b *testing.B) {
	const cacheSize = 1_000_000
	cache := sieve.New[int, int](cacheSize)

	// Fill the cache completely
	for i := 0; i < cacheSize; i++ {
		cache.Add(i, i)
	}
	// Mark ~50% as visited so eviction has to scan
	for i := 0; i < cacheSize; i += 2 {
		cache.Get(i)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Each Add beyond capacity triggers an eviction
		key := cacheSize + i
		cache.Add(key, key)
	}
}

// BenchmarkGCPause_Comparison measures GC pause times at various cache sizes.
func BenchmarkGCPause_Comparison(b *testing.B) {
	for _, cacheSize := range []int{100_000, 500_000, 1_000_000} {
		b.Run(fmt.Sprintf("Size_%d", cacheSize), func(b *testing.B) {
			cache := sieve.New[int, int](cacheSize)

			// Fill the cache
			for i := 0; i < cacheSize; i++ {
				cache.Add(i, i)
			}

			// Force GC, measure pause
			runtime.GC()

			var stats runtime.MemStats
			runtime.ReadMemStats(&stats)

			// Run some operations during the benchmark
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := i % (cacheSize * 2)
				op := i % 10
				if op < 6 {
					cache.Get(key)
				} else if op < 9 {
					cache.Add(key, key)
				} else {
					cache.Delete(key)
				}
			}
			b.StopTimer()

			// Force GC and measure
			runtime.GC()
			runtime.ReadMemStats(&stats)
			b.ReportMetric(float64(stats.PauseTotalNs)/float64(stats.NumGC), "avg-gc-pause-ns")
			b.ReportMetric(float64(stats.HeapObjects), "heap-objects")
		})
	}
}

// BenchmarkMemoryOverhead measures HeapObjects and HeapAlloc at various sizes.
func BenchmarkMemoryOverhead(b *testing.B) {
	for _, cacheSize := range []int{100_000, 500_000, 1_000_000} {
		b.Run(fmt.Sprintf("Size_%d", cacheSize), func(b *testing.B) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			cache := sieve.New[int, int](cacheSize)
			for i := 0; i < cacheSize; i++ {
				cache.Add(i, i)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc), "heap-bytes-delta")
			b.ReportMetric(float64(after.HeapObjects-before.HeapObjects), "heap-objects-delta")

			// Run dummy operations so the benchmark doesn't report 0 ns/op
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cache.Get(i % cacheSize)
			}

			// Keep cache alive
			runtime.KeepAlive(cache)
		})
	}
}

// BenchmarkGCPause_Final measures GC pause at 1M entries — the headline number for Phase 4.
func BenchmarkGCPause_Final(b *testing.B) {
	const cacheSize = 1_000_000
	cache := sieve.New[int, int](cacheSize)

	// Fill the cache
	for i := 0; i < cacheSize; i++ {
		cache.Add(i, i)
	}

	// Trigger a GC to stabilize
	runtime.GC()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		key := i % (cacheSize * 2)
		op := i % 10
		if op < 6 {
			cache.Get(key)
		} else if op < 9 {
			cache.Add(key, key)
		} else {
			cache.Delete(key)
		}
	}
	b.StopTimer()

	runtime.GC()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	b.ReportMetric(float64(stats.PauseTotalNs)/float64(stats.NumGC), "avg-gc-pause-ns")
	b.ReportMetric(float64(stats.HeapObjects), "heap-objects")
	b.ReportMetric(float64(stats.HeapAlloc), "heap-bytes")

	runtime.KeepAlive(cache)
}

// BenchmarkMemoryTotal measures total memory footprint at various cache sizes.
func BenchmarkMemoryTotal(b *testing.B) {
	for _, cacheSize := range []int{100_000, 500_000, 1_000_000} {
		b.Run(fmt.Sprintf("Size_%d", cacheSize), func(b *testing.B) {
			runtime.GC()
			var before runtime.MemStats
			runtime.ReadMemStats(&before)

			cache := sieve.New[int, int](cacheSize)
			for i := 0; i < cacheSize; i++ {
				cache.Add(i, i)
			}

			runtime.GC()
			var after runtime.MemStats
			runtime.ReadMemStats(&after)

			heapDelta := after.HeapAlloc - before.HeapAlloc
			b.ReportMetric(float64(heapDelta), "total-heap-bytes")
			b.ReportMetric(float64(heapDelta)/float64(cacheSize), "bytes-per-entry")
			b.ReportMetric(float64(after.HeapObjects-before.HeapObjects), "heap-objects-delta")

			// Run dummy ops so benchmark doesn't report 0 ns/op
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				cache.Get(i % cacheSize)
			}

			runtime.KeepAlive(cache)
		})
	}
}

// BenchmarkEviction_VaryingVisited measures eviction scan cost sensitivity to
// the fraction of visited nodes. 100% visited is worst case (full wrap-around).
func BenchmarkEviction_VaryingVisited(b *testing.B) {
	const cacheSize = 100_000

	for _, pctVisited := range []int{0, 50, 90, 100} {
		b.Run(fmt.Sprintf("Visited_%d%%", pctVisited), func(b *testing.B) {
			cache := sieve.New[int, int](cacheSize)

			// Fill the cache
			for i := 0; i < cacheSize; i++ {
				cache.Add(i, i)
			}
			// Mark pctVisited% as visited
			visitCount := cacheSize * pctVisited / 100
			for i := 0; i < visitCount; i++ {
				cache.Get(i)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				key := cacheSize + i
				cache.Add(key, key)
			}
		})
	}
}

// runWorkload performs a consistent workload that stresses node allocation/deallocation
func runSieveWorkload(cache *sieve.Sieve[int, int], operations int) {
	capacity := cache.Cap()

	// Create a workload that ensures significant cache churn
	for i := 0; i < operations; i++ {
		key := i % (capacity * 2) // Ensure we cycle through keys causing evictions

		// Mix of operations: 70% adds, 20% gets, 10% deletes
		op := i % 10
		if op < 7 {
			// Add - heavy on adds to stress allocation
			cache.Add(key, i)
		} else if op < 9 {
			// Get
			cache.Get(key)
		} else {
			// Delete - to trigger freelist recycling
			cache.Delete(key)
		}

		// Every so often, add a burst of new entries to trigger evictions
		if i > 0 && i%10000 == 0 {
			for j := 0; j < capacity/10; j++ {
				cache.Add(i+j+capacity, i+j)
			}
		}
	}
}
