//go:build trace

package bench_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	sieve "github.com/opencoff/go-sieve"

	"github.com/opencoff/go-sieve/bench"

	arc "github.com/hashicorp/golang-lru/arc/v2"
	lru "github.com/hashicorp/golang-lru/v2"
)

// --- Trace discovery and caching ---

// traceEntry holds a loaded trace keyed by its relative path.
type traceEntry struct {
	name  string // relative path from data/, e.g. "msr_2007/msr_hm_0"
	trace *bench.Trace[uint64]
}

var (
	traceOnce    sync.Once
	traceEntries []traceEntry
)

func dataDir() string {
	return filepath.Join("..", "data")
}

func isOracleGeneral(name string) bool {
	return strings.HasSuffix(name, ".oracleGeneral") || strings.HasSuffix(name, ".oracleGeneral.bin")
}

// discoverTraces finds and loads all oracleGeneral traces under data/.
// Results are cached via sync.Once.
func discoverTraces(tb testing.TB) []traceEntry {
	traceOnce.Do(func() {
		root := dataDir()
		filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if !isOracleGeneral(d.Name()) {
				return nil
			}
			// Skip very large files (>2GB) to keep benchmarks tractable
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.Size() > 2*1024*1024*1024 {
				return nil
			}

			trace, err := bench.LoadOracleGeneral(path)
			if err != nil {
				return nil
			}

			rel, _ := filepath.Rel(root, path)
			// Clean up the name: strip extension, use / as separator
			name := strings.TrimSuffix(rel, ".bin")
			name = strings.TrimSuffix(name, ".oracleGeneral")
			traceEntries = append(traceEntries, traceEntry{name: name, trace: trace})
			return nil
		})
	})
	if len(traceEntries) == 0 {
		tb.Skip("no oracleGeneral traces found in data/")
	}
	return traceEntries
}

// --- Miss Ratio Test ---

func TestMissRatio(t *testing.T) {
	entries := discoverTraces(t)

	for _, e := range entries {
		trace := e.trace
		capacity := trace.Unique / 10
		if capacity < 1 {
			capacity = 1
		}

		t.Run(e.name, func(t *testing.T) {
			t.Logf("%d requests, %d unique, cache size %d (10%%)", len(trace.Requests), trace.Unique, capacity)

			type variant struct {
				name string
				run  func() int
			}
			variants := []variant{
				{"sieve-k1", func() int {
					c := sieve.Must(sieve.New[uint64, struct{}](capacity))
					return replayMisses(c, trace)
				}},
				{"sieve-k2", func() int {
					c := sieve.Must(sieve.New[uint64, struct{}](capacity, sieve.WithVisitClamp(2)))
					return replayMisses(c, trace)
				}},
				{"sieve-k3", func() int {
					c := sieve.Must(sieve.New[uint64, struct{}](capacity, sieve.WithVisitClamp(3)))
					return replayMisses(c, trace)
				}},
				{"LRU", func() int {
					c, _ := lru.New[uint64, struct{}](capacity)
					return replayMissesLRU(c, trace)
				}},
				{"ARC", func() int {
					c, _ := arc.NewARC[uint64, struct{}](capacity)
					return replayMissesARC(c, trace)
				}},
			}

			for _, v := range variants {
				misses := v.run()
				ratio := float64(misses) / float64(len(trace.Requests))
				t.Logf("  %-12s miss ratio: %.4f (%d/%d)", v.name, ratio, misses, len(trace.Requests))
			}
		})
	}
}

// --- Sequential Replay Benchmarks ---

func BenchmarkReplay(b *testing.B) {
	entries := discoverTraces(b)

	for _, e := range entries {
		trace := e.trace
		capacity := trace.Unique / 10
		if capacity < 1 {
			capacity = 1
		}

		b.Run(e.name+"/SieveK1", func(b *testing.B) {
			for range b.N {
				c := sieve.Must(sieve.New[uint64, struct{}](capacity))
				misses := replayMisses(c, trace)
				b.ReportMetric(float64(misses)/float64(len(trace.Requests)), "miss-ratio")
			}
		})

		b.Run(e.name+"/SieveK3", func(b *testing.B) {
			for range b.N {
				c := sieve.Must(sieve.New[uint64, struct{}](capacity, sieve.WithVisitClamp(3)))
				misses := replayMisses(c, trace)
				b.ReportMetric(float64(misses)/float64(len(trace.Requests)), "miss-ratio")
			}
		})

		b.Run(e.name+"/LRU", func(b *testing.B) {
			for range b.N {
				c, _ := lru.New[uint64, struct{}](capacity)
				misses := replayMissesLRU(c, trace)
				b.ReportMetric(float64(misses)/float64(len(trace.Requests)), "miss-ratio")
			}
		})

		b.Run(e.name+"/ARC", func(b *testing.B) {
			for range b.N {
				c, _ := arc.NewARC[uint64, struct{}](capacity)
				misses := replayMissesARC(c, trace)
				b.ReportMetric(float64(misses)/float64(len(trace.Requests)), "miss-ratio")
			}
		})
	}
}

// --- Parallel Get Benchmarks ---

func BenchmarkParallelGet(b *testing.B) {
	entries := discoverTraces(b)

	for _, e := range entries {
		trace := e.trace
		capacity := trace.Unique / 10
		if capacity < 1 {
			capacity = 1
		}

		b.Run(e.name+"/SieveK1", func(b *testing.B) {
			c := sieve.Must(sieve.New[uint64, struct{}](capacity))
			warmup(c, trace)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					c.Get(trace.Requests[i%len(trace.Requests)].Key)
					i++
				}
			})
		})

		b.Run(e.name+"/SieveK3", func(b *testing.B) {
			c := sieve.Must(sieve.New[uint64, struct{}](capacity, sieve.WithVisitClamp(3)))
			warmup(c, trace)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					c.Get(trace.Requests[i%len(trace.Requests)].Key)
					i++
				}
			})
		})

		b.Run(e.name+"/LRU", func(b *testing.B) {
			c, _ := lru.New[uint64, struct{}](capacity)
			warmupLRU(c, trace)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					c.Get(trace.Requests[i%len(trace.Requests)].Key)
					i++
				}
			})
		})

		b.Run(e.name+"/ARC", func(b *testing.B) {
			c, _ := arc.NewARC[uint64, struct{}](capacity)
			warmupARC(c, trace)
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				i := 0
				for pb.Next() {
					c.Get(trace.Requests[i%len(trace.Requests)].Key)
					i++
				}
			})
		})
	}
}

// --- GC Pressure Test ---

func TestGCPressure(t *testing.T) {
	entries := discoverTraces(t)
	// Use first trace for GC test
	trace := entries[0].trace
	capacity := trace.Unique / 10
	if capacity < 1 {
		capacity = 1
	}

	type result struct {
		name        string
		numGC       uint32
		pauseNs     uint64
		totalAlloc  uint64
		heapObjects uint64
	}

	runGCTest := func(name string, replay func()) result {
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)
		replay()
		runtime.GC()
		var after runtime.MemStats
		runtime.ReadMemStats(&after)
		return result{
			name:        name,
			numGC:       after.NumGC - before.NumGC,
			pauseNs:     after.PauseTotalNs - before.PauseTotalNs,
			totalAlloc:  after.TotalAlloc - before.TotalAlloc,
			heapObjects: after.HeapObjects,
		}
	}

	t.Logf("Using trace: %s (%d requests, %d unique, cache %d)",
		entries[0].name, len(trace.Requests), trace.Unique, capacity)

	results := []result{
		runGCTest("sieve-k1", func() {
			c := sieve.Must(sieve.New[uint64, struct{}](capacity))
			replayMisses(c, trace)
		}),
		runGCTest("sieve-k3", func() {
			c := sieve.Must(sieve.New[uint64, struct{}](capacity, sieve.WithVisitClamp(3)))
			replayMisses(c, trace)
		}),
		runGCTest("LRU", func() {
			c, _ := lru.New[uint64, struct{}](capacity)
			replayMissesLRU(c, trace)
		}),
		runGCTest("ARC", func() {
			c, _ := arc.NewARC[uint64, struct{}](capacity)
			replayMissesARC(c, trace)
		}),
	}

	t.Logf("%-12s %8s %12s %14s %12s", "Variant", "NumGC", "PauseTotal", "TotalAlloc", "HeapObjects")
	for _, r := range results {
		t.Logf("%-12s %8d %10d us %12d KB %12d",
			r.name, r.numGC, r.pauseNs/1000, r.totalAlloc/1024, r.heapObjects)
	}
}

// --- Helpers ---

type sieveCache interface {
	Get(uint64) (struct{}, bool)
	Add(uint64, struct{}) (sieve.Evicted[uint64, struct{}], sieve.CacheResult)
}

func replayMisses(c sieveCache, trace *bench.Trace[uint64]) int {
	misses := 0
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
			misses++
		}
	}
	return misses
}

func replayMissesLRU(c *lru.Cache[uint64, struct{}], trace *bench.Trace[uint64]) int {
	misses := 0
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
			misses++
		}
	}
	return misses
}

func replayMissesARC(c *arc.ARCCache[uint64, struct{}], trace *bench.Trace[uint64]) int {
	misses := 0
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
			misses++
		}
	}
	return misses
}

func warmup(c sieveCache, trace *bench.Trace[uint64]) {
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
		}
	}
}

func warmupLRU(c *lru.Cache[uint64, struct{}], trace *bench.Trace[uint64]) {
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
		}
	}
}

func warmupARC(c *arc.ARCCache[uint64, struct{}], trace *bench.Trace[uint64]) {
	for _, r := range trace.Requests {
		if _, ok := c.Get(r.Key); !ok {
			c.Add(r.Key, struct{}{})
		}
	}
}
