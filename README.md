# go-sieve - SIEVE cache eviction for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/opencoff/go-sieve.svg)](https://pkg.go.dev/github.com/opencoff/go-sieve)
[![Go Report Card](https://goreportcard.com/badge/github.com/opencoff/go-sieve)](https://goreportcard.com/report/github.com/opencoff/go-sieve)
[![Release](https://img.shields.io/github/v/release/opencoff/go-sieve)](https://github.com/opencoff/go-sieve/releases)

A high-performance, GC-friendly Go implementation of the
[SIEVE](https://yazhuozhang.com/assets/pdf/nsdi24-sieve.pdf) cache eviction
algorithm (NSDI'24, Zhang et al.). Generic over key and value types.

SIEVE uses a FIFO queue with a roving "hand" pointer: cache hits set a
visited bit (lazy promotion), and eviction scans from the hand clearing
visited bits until it finds an unvisited node (quick demotion). It matches
or exceeds LRU/ARC hit ratios with far less bookkeeping.

## Key Design Decisions

**Array-backed indexed list.** [Marc Brooker observed](https://brooker.co.za/blog/2023/12/15/sieve.html)
that SIEVE's mid-list removal prevents a simple circular buffer. Rather than
Tobin Baker's "swap tail into hole" workaround, this implementation uses a
doubly-linked list with `int32` indices into a pre-allocated backing array.
This preserves SIEVE's exact eviction semantics while eliminating all interior
pointers — the GC sees a flat `[]node` with no pointers to trace (for
non-pointer `K`, `V` types).

**Packed visited bitfield.** Per-node `atomic.Bool` (4 bytes each, aligned to
`uint32`) is replaced by a shared `[]uint64` bitfield. `Test()` is a single
`atomic.LoadUint64` — zero write contention on the read path. `Set()`/`Clear()`
use CAS loops with early exit when the bit is already in the desired state.
For a 1M-entry cache this is 16 KB instead of 4 MB.

**xsync.MapOf for concurrent access.** The key→index map uses
[puzpuzpuz/xsync.MapOf](https://github.com/puzpuzpuz/xsync) which stores
`int32` values inline in cache-line-padded buckets — no traced pointers per
entry. `Get()` is fully lock-free; only `Add()`/`Probe()` (on miss) and
`Delete()` take the global mutex.

**Pre-allocated node pool.** All nodes are allocated once at cache creation in
a contiguous array. A bump allocator + intrusive freelist (reusing `node.next`)
provides O(1) alloc/free with zero heap allocations during steady-state operation.

**TOCTOU-safe concurrent writes.** `Add()` and `Probe()` use a double-check
pattern: fast-path `Load()` outside the lock, re-check under `mu.Lock()` before
inserting. This prevents duplicate nodes from concurrent writers racing on the
same key.

## Benchmark Results

Measured on Apple M4, `GOMAXPROCS=10`, `go test -bench=. -benchmem -count=6`.

The baseline numbers below are from the original unoptimized code (pointer-based
list, `sync.Map`, per-node `atomic.Bool`) measured on Apple M2 Pro. The hardware
difference means the throughput comparison is directional, but the allocation
reductions are hardware-independent and show the real structural improvement.

| Benchmark | Baseline | Optimized | Improvement |
|-----------|----------|-----------|-------------|
| `Sieve_Get` | 31.4 ns/op | 16.5 ns/op | ~1.9x |
| `Sieve_Add` | 88.9 ns/op, 35 B, 1 alloc | 49.9 ns/op, 10 B, 0 allocs | ~1.8x, **3.5x less memory** |
| `SieveAdd/8192` | 128.3 ns/op, 84 B, 2 allocs | 59.6 ns/op, 16 B, 1 alloc | ~2.2x, **5x less memory** |
| `SieveConcurrency` | 167.0 ns/op, 10 B | 66.8 ns/op, 2 B | ~2.5x, **5x less memory** |
| `Get_Parallel` | 6.3 ns/op | 3.3 ns/op | **~1.9x faster** |
| Heap objects (1M cache) | 2.01M | 1.02M | **2x fewer** |
| Heap bytes (1M cache) | 179 MB | 83 MB | **2.2x less memory** |

## Comparison vs hashicorp/golang-lru

Benchmarked against [hashicorp/golang-lru v2.0.7](https://github.com/hashicorp/golang-lru)
(LRU and ARC) on Apple M4, `GOMAXPROCS=10`, `go test -bench=. -benchmem -count=6`.
Benchmarks live in `bench/` as a separate module to avoid polluting `go.mod`.

| Benchmark | Sieve | LRU | ARC |
|-----------|-------|-----|-----|
| `Get_Parallel` | **3.2 ns/op** | 127 ns/op | 140 ns/op |
| `Add_Parallel` | **122 ns/op, 8 B** | 194 ns/op, 40 B | 264 ns/op, 74 B |
| `Mixed_Parallel` | **67 ns/op, 2 B** | 200 ns/op, 12 B | 226 ns/op, 24 B |
| Memory @ 1M entries | **122 MB, 1.10M allocs** | 156 MB, 1.01M allocs | 156 MB, 1.01M allocs |
| GC impact @ 1M (mixed+GC) | **4.5 ms/op** | 9.1 ms/op | 9.4 ms/op |

Sieve's lock-free `Get()` is **~40x faster** than LRU/ARC under contention.
Write throughput is 1.6–2.2x better with 5–9x fewer bytes per op.

## Trace Replay Results

We validated SIEVE against real-world cache traces from the
[libCacheSim](https://cachelib.org/) trace repository — 14 MSR Cambridge
enterprise block I/O traces and 5 Meta Storage (Tectonic) block traces,
totalling ~300M requests. Each trace was replayed with a cache sized at
10% of unique keys, comparing SIEVE (k=1 and k=3) against
hashicorp/golang-lru (LRU and ARC).

**Miss ratio.** SIEVE k=1 beats LRU on nearly every trace (often by 2–7%)
and is competitive with ARC. On msr_prn_1, SIEVE k=3 outperforms all
others (0.3796 vs LRU 0.4341, ARC 0.4148). On msr_src1_1, SIEVE k=1
beats ARC (0.7939 vs 0.8231).

**Parallel Get throughput.** SIEVE's lock-free `Get()` is ~100x faster than
LRU/ARC under concurrent read load (1.7–2.1 ns/op vs 173–258 ns/op on
12 goroutines). The k=3 saturating counter adds <5% overhead.

**Memory.** During trace replay, SIEVE allocates 2.7x less than LRU and
6.5x less than ARC (154 MB vs 418 MB vs 997 MB on a 13M-request trace with
a 601K-entry cache).

| Metric | SIEVE k=1 | LRU | ARC |
|--------|-----------|-----|-----|
| Parallel Get (ns/op) | **1.7** | 190 | 232 |
| Sequential replay (ns/op) | **189** | 235 | 549 |
| Total alloc (13M replay) | **154 MB** | 418 MB | 997 MB |
| Miss ratio (msr_hm_0) | **0.299** | 0.319 | 0.292 |

Full results and methodology: [`bench/README.md`](bench/README.md).

## SIEVE-k

`WithVisitClamp(k)` creates a SIEVE-k cache where each entry uses a
saturating counter instead of a single visited bit. An item accessed k+1
times survives k eviction passes before being evicted. `k=1` is equivalent
to classic SIEVE (the default). Use `k=2` or `k=3` for workloads with
repeated access patterns where extra eviction resistance is beneficial.

```go
// Classic SIEVE (k=1, the default)
c := sieve.New[string, int](1000)

// SIEVE-k=3: items survive up to 3 eviction passes
c := sieve.New[string, int](1000, sieve.WithVisitClamp(3))
```

## Usage

```go
import "github.com/opencoff/go-sieve"

// Create a cache mapping string keys to int values, capacity 1000.
c := sieve.New[string, int](1000)

c.Add("foo", 42)

if val, ok := c.Get("foo"); ok {
    fmt.Println(val) // 42
}

// Probe inserts only if absent; returns the cached value if present.
val, _, r := c.Probe("foo", 99)
// val == 42, r.Hit() == true

c.Delete("foo")
c.Purge() // reset entire cache
```

### Eviction Handling

`Add()` and `Probe()` return the evicted entry (if any) along with a
`CacheResult` bitmask. This allows callers to handle evictions
synchronously without channels, goroutines, or lifecycle management.

```go
c := sieve.New[string, int](1000)

ev, r := c.Add("foo", 42)
if r.Evicted() {
    cleanupDisk(ev.Key, ev.Val)
}

// CacheResult bitmask:
//   CacheHit   — key was already present (value updated, no eviction)
//   CacheEvict — an entry was evicted to make room (mutually exclusive with CacheHit)
```

`Purge()` and `Delete()` do not report evictions.

## API

| Method | Description |
|--------|-------------|
| `New[K, V](capacity, ...Option)` | Create a cache with fixed capacity |
| `Get(key) (V, bool)` | Look up a key (lock-free) |
| `Add(key, val) (Evicted[K,V], CacheResult)` | Insert or update; returns evicted entry and result bitmask |
| `Probe(key, val) (V, Evicted[K,V], CacheResult)` | Insert-if-absent; returns cached/inserted value, evicted entry, and result bitmask |
| `Delete(key) bool` | Remove a key |
| `Purge()` | Clear the entire cache |
| `Len() int` | Current number of entries |
| `Cap() int` | Maximum capacity |

### Options

| Option | Description |
|--------|-------------|
| `WithVisitClamp(k)` | Use k-level saturating counters (default k=1, classic SIEVE) |

## GC Note

When `K` or `V` is a pointer type (including `string`, which contains an
internal pointer in Go), the node array will still contain GC-traced pointers.
The GC pressure reduction is most dramatic for scalar key/value types (`int`,
`[16]byte`, fixed-size structs).

## License

BSD-2-Clause. See the source files for the full license text.
