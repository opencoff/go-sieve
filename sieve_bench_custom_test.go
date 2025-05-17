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
