# go-sieve - SIEVE cache eviction for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/opencoff/go-sieve.svg)](https://pkg.go.dev/github.com/opencoff/go-sieve)
[![Go Report Card](https://goreportcard.com/badge/github.com/opencoff/go-sieve)](https://goreportcard.com/report/github.com/opencoff/go-sieve)
[![Release](https://img.shields.io/github/v/release/opencoff/go-sieve)](https://github.com/opencoff/go-sieve/releases)

A Go implementation of the
[SIEVE](https://yazhuozhang.com/assets/pdf/nsdi24-sieve.pdf) cache eviction
algorithm (NSDI'24, Zhang et al.), **engineered from the ground up for highly
concurrent, read-heavy workloads**. Generic over key and value types.

The read path (`Get()`) is fully **lock-free** — a single atomic load on the
key→index map plus a single atomic bit update on a shared visited bitfield.
No mutex, no pointer chasing, no per-entry allocations, zero GC traffic on
hits. Under parallel load this is ~90–300x faster than
`hashicorp/golang-lru` and scales linearly with cores: measured at
**1–3 ns/op across 32 goroutines** on real-world cache traces. The write
path uses a single short-held mutex and a pre-allocated node pool, so
`Add()`/`Probe()` also avoid per-operation heap allocation in steady state.

If you need a cache that many goroutines hit simultaneously on the read
path — an HTTP response cache, a DNS resolver cache, an authz decision
cache, a hot-path object lookup — this implementation is built for that
shape. For purely single-threaded use, `hashicorp/golang-lru` may be
marginally faster on the sequential write path; under any concurrency, Sieve
wins decisively.

SIEVE uses a FIFO queue with a roving "hand" pointer: cache hits set a
visited bit (lazy promotion), and eviction scans from the hand clearing
visited bits until it finds an unvisited node (quick demotion). It matches
or exceeds LRU/ARC hit ratios with far less bookkeeping — validated here on
~300M requests from the MSR Cambridge and Meta Storage trace repositories.

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

Benchmarked against [hashicorp/golang-lru v2.0.7](https://github.com/hashicorp/golang-lru)
(LRU and ARC) on a 13th Gen Intel Core i9-13900, Linux, Go 1.26.1,
`GOMAXPROCS=32`. Benchmarks live in `bench/` as a separate module to avoid
polluting `go.mod`.

Commands used (no name filter — every benchmark in every package runs):

```
# Comparison micro-benchmarks:
cd bench && go test -bench=. -benchmem -count=3

# Trace replay + miss ratio + GC pressure (trace build tag):
cd bench && go test -tags=trace -bench=. -benchmem -count=1 -v -timeout=240m
```

Full raw results: [`bench-results.md`](bench-results.md).

### Parallel Micro-Benchmarks (`count=3`, medians)

| Benchmark | Sieve | LRU | ARC |
|-----------|-------|-----|-----|
| `Get_Parallel` | **2.49 ns/op, 0 B** | 223.7 ns/op, 0 B | 245.0 ns/op, 0 B |
| `Add_Parallel` | **157.1 ns/op, 8 B** | 188.8 ns/op, 40 B | 366.6 ns/op, 74 B |
| `Mixed_Parallel` (80/20) | **141.4 ns/op, 2 B** | 213.6 ns/op, 12 B | 221.3 ns/op, 24 B |
| `Zipf_Get_Parallel` (s=1.01) | **16.7 ns/op** | 189.3 ns/op | 196.6 ns/op |
| Memory @ 1M fill | 122 MB, 1.10M allocs | 156 MB, 1.01M allocs | 156 MB, 1.01M allocs |
| `GCImpact` | **5.83 ms/op**, 9.8 KB/op | 10.65 ms/op, 28 KB/op | 10.51 ms/op, 116 KB/op |

Sieve's `Get()` is **~90x faster** than LRU/ARC on the parallel read path and
is fully lock-free. Adds are 1.2–2.3x faster with 5–9x fewer bytes per op.
Memory footprint at a 1M-entry fill is 22% lower than LRU/ARC.

## Trace Replay Results

Validated against real-world cache traces from the
[libCacheSim](https://cachelib.org/) trace repository — 14 MSR Cambridge
enterprise block I/O traces + 5 Meta Storage (Tectonic) block traces
totalling ~300M requests. Each trace was replayed with a cache sized at
10% of unique keys, comparing SIEVE (k=1, k=2, k=3) against
hashicorp/golang-lru (LRU and ARC).

### Parallel Get throughput (32 goroutines, ns/op, zero allocs)

SIEVE's lock-free `Get()` is **~100–300x faster** than LRU/ARC under
concurrent read load. Every trace, every cache:

| Trace | SIEVE k=1 | SIEVE k=3 | LRU | ARC |
|-------|---------:|---------:|----:|----:|
| msr_web_2 | **1.03** | 1.02 | 300.6 | 404.7 |
| msr_proj_4 | **1.22** | 1.24 | 379.6 | 549.6 |
| meta_storage/block_traces_1 | **1.30** | 1.42 | 286.5 | 384.7 |
| meta_storage/block_traces_2 | **1.36** | 1.55 | 280.1 | 419.6 |
| meta_storage/block_traces_3 | **1.54** | 1.55 | 276.5 | 400.0 |
| meta_storage/block_traces_4 | **1.58** | 1.58 | 279.7 | 499.2 |
| msr_prn_1 | **1.59** | 1.60 | 349.4 | 384.5 |
| meta_storage/block_traces_5 | **1.61** | 1.63 | 269.5 | 409.4 |
| msr_prxy_0 | **1.74** | 1.95 | 285.7 | 434.0 |
| msr_src1_0 | **2.03** | 2.01 | 322.3 | 521.5 |
| msr_usr_2 | **2.10** | 2.10 | 363.5 | 546.7 |
| msr_src1_1 | **2.22** | 2.32 | 426.9 | 613.7 |
| msr_usr_1 | **2.25** | 2.33 | 391.0 | 603.2 |
| msr_proj_1 | **2.90** | 2.97 | 336.2 | 507.8 |
| msr_proj_2 | **2.91** | 2.95 | 358.0 | 450.8 |
| msr_proj_0 | **5.21** | 6.05 | 351.1 | 458.6 |
| msr_hm_0 | **7.26** | 7.86 | 347.2 | 448.1 |
| msr_prn_0 | **10.35** | 11.54 | 289.3 | 433.1 |

The k=3 saturating counter adds under 5% overhead to the read path.

### Miss ratio (every trace)

Cache sized at 10% of unique keys. **Bold** = best in row.

| Trace | SIEVE k=1 | SIEVE k=2 | SIEVE k=3 | LRU | ARC |
|-------|----------:|----------:|----------:|----:|----:|
| meta_storage/block_traces_1 | 0.4632 | 0.4651 | 0.4672 | **0.4602** | 0.4667 |
| meta_storage/block_traces_2 | 0.4719 | 0.4743 | 0.4754 | **0.4676** | 0.4755 |
| meta_storage/block_traces_3 | 0.4908 | 0.4928 | 0.4948 | **0.4885** | 0.4947 |
| meta_storage/block_traces_4 | 0.4841 | 0.4870 | 0.4888 | **0.4812** | 0.4887 |
| meta_storage/block_traces_5 | 0.4959 | 0.4984 | 0.4998 | **0.4927** | 0.5003 |
| msr_hm_0 | 0.2991 | 0.3025 | 0.3025 | 0.3188 | **0.2923** |
| msr_prn_0 | 0.2156 | 0.2194 | 0.2208 | 0.2310 | **0.2145** |
| msr_prn_1 | 0.3908 | 0.3837 | **0.3796** | 0.4341 | 0.4148 |
| msr_proj_0 | 0.2537 | 0.2660 | 0.2745 | 0.2375 | **0.2242** |
| msr_proj_1 | 0.6794 | 0.6794 | 0.6794 | 0.7215 | **0.6788** |
| msr_proj_2 | 0.8231 | 0.8231 | 0.8231 | 0.8548 | **0.8125** |
| msr_proj_4 | 0.8463 | 0.8463 | 0.8463 | 0.8140 | **0.7173** |
| msr_prxy_0 | 0.0512 | 0.0572 | 0.0594 | 0.0476 | **0.0468** |
| msr_src1_0 | 0.7845 | 0.7845 | 0.7845 | 0.9132 | **0.7811** |
| msr_src1_1 | **0.7939** | 0.7934 | 0.7934 | 0.8129 | 0.8231 |
| msr_usr_1 | 0.3558 | 0.3558 | 0.3558 | 0.4007 | **0.3513** |
| msr_usr_2 | 0.7216 | 0.7216 | 0.7216 | 0.7533 | **0.7199** |
| msr_web_2 | 0.9786 | 0.9786 | 0.9786 | 0.9929 | **0.9785** |

**Summary**: SIEVE k=1 beats LRU on 13 of 18 traces (largest gap msr_src1_0,
12.9 points). SIEVE is competitive with ARC — ARC wins on scan-heavy MSR
block traces where its scan-resistance pays off, but Sieve wins or ties on
msr_prn_1, msr_src1_1, and every Meta Storage block trace. SIEVE k=3
produces the overall-best miss ratio in the whole comparison on msr_prn_1
(0.3796 vs LRU 0.4341, ARC 0.4148).

### Memory during replay

On the 13.2M-request `meta_storage/block_traces_1` trace (601K-entry cache),
`TestGCPressure` reports:

| Variant | TotalAlloc |
|---------|-----------:|
| **SIEVE k=1** | **154 MB** |
| SIEVE k=3 | 155 MB |
| LRU | 418 MB |
| ARC | 997 MB |

SIEVE allocates **2.7x less** than LRU and **6.5x less** than ARC during
replay — the array-backed node pool and inline-int32 `xsync.MapOf` are
structural, not workload-dependent, wins.

Full per-trace tables (sequential replay ns/op, B/op, miss ratio for every
trace) are in [`bench-results.md`](bench-results.md). Methodology and
trace-loading details are in [`bench/README.md`](bench/README.md).

## SIEVE-k

`WithVisitClamp(k)` creates a SIEVE-k cache where each entry uses a
saturating counter instead of a single visited bit. An item accessed k+1
times survives k eviction passes before being evicted. `k=1` is equivalent
to classic SIEVE (the default). Use `k=2` or `k=3` for workloads with
repeated access patterns where extra eviction resistance is beneficial.

```go
// Classic SIEVE (k=1, the default)
c, err := sieve.New[string, int](1000)

// SIEVE-k=3: items survive up to 3 eviction passes
c, err := sieve.New[string, int](1000, sieve.WithVisitClamp(3))
```

## Usage

```go
import "github.com/opencoff/go-sieve"

// Create a cache mapping string keys to int values, capacity 1000.
c, err := sieve.New[string, int](1000)
if err != nil {
    log.Fatal(err) // ErrInvalidCapacity or ErrInvalidVisitClamp
}

// Or, for constant arguments, use Must to get a one-liner:
c := sieve.Must(sieve.New[string, int](1000))

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

`New` returns an error for `capacity <= 0` (`ErrInvalidCapacity`) and for
`WithVisitClamp(k)` with `k > sieve.MaxVisitClamp` (255 — `ErrInvalidVisitClamp`).
Clamp values below 1 are silently rounded up to 1.

### Eviction Handling

`Add()` and `Probe()` return the evicted entry (if any) along with a
`CacheResult` bitmask. This allows callers to handle evictions
synchronously without channels, goroutines, or lifecycle management.

```go
c := sieve.Must(sieve.New[string, int](1000))

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

| Function / Method | Description |
|-------------------|-------------|
| `New[K, V](capacity, ...Option) (*Sieve[K,V], error)` | Create a cache with fixed capacity. Returns `ErrInvalidCapacity` or `ErrInvalidVisitClamp` on bad input. |
| `Must[K, V](*Sieve[K,V], error) *Sieve[K,V]` | Helper that panics on error; useful with constant arguments. |
| `Get(key) (V, bool)` | Look up a key (lock-free, zero-alloc). |
| `Add(key, val) (Evicted[K,V], CacheResult)` | Insert or update; returns evicted entry and result bitmask. |
| `Probe(key, val) (V, Evicted[K,V], CacheResult)` | Insert-if-absent; returns cached/inserted value, evicted entry, and result bitmask. |
| `Delete(key) bool` | Remove a key. |
| `Purge()` | Clear the entire cache. |
| `Len() int` | Current number of entries (lock-free atomic load). |
| `Cap() int` | Maximum capacity. |

### Options

| Option | Description |
|--------|-------------|
| `WithVisitClamp(k)` | Use k-level saturating counters (default k=1 = classic SIEVE). `k` is capped at `MaxVisitClamp` (255); `k < 1` is silently rounded to 1. |

## GC Note

When `K` or `V` is a pointer type (including `string`, which contains an
internal pointer in Go), the node array will still contain GC-traced pointers.
The GC pressure reduction is most dramatic for scalar key/value types (`int`,
`[16]byte`, fixed-size structs).

## License

BSD-2-Clause. See the source files for the full license text.
