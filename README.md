# go-sieve - SIEVE cache for Go

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

**Packed visited bitfield.** We use a packed `[]uint64` to represent
visitied bits of all nodes in the cache.  `Test()` is a single
`atomic.LoadUint64` — zero write contention on the read path. `Set()`/`Clear()`
use CAS loops with early exit when the bit is already in the desired state.
For a 1M-entry cache this is 16 KB instead of 4 MB.

**xsync.MapOf for concurrent access.** The key→index map uses
[puzpuzpuz/xsync.MapOf](https://github.com/puzpuzpuz/xsync) which stores
`int32` values inline in cache-line-padded buckets — no traced pointers per
entry. `Get()` is fully lock-free; only `Add()` (on miss) and `Delete()` take
the global mutex.

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
| `Get_Parallel` | — | 3.3 ns/op | lock-free read path |
| GC cycles (10K cache) | 6 | 5 | fewer GC pauses |
| GC pause (1M cache) | — | 34 µs avg | near-invisible to GC |

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
val, existed := c.Probe("foo", 99)
// val == 42, existed == true

c.Delete("foo")
c.Purge() // reset entire cache
```

## API

| Method | Description |
|--------|-------------|
| `New[K, V](capacity)` | Create a cache with fixed capacity |
| `Get(key) (V, bool)` | Look up a key (lock-free) |
| `Add(key, val) bool` | Insert or update; returns true if key existed |
| `Probe(key, val) (V, bool)` | Insert-if-absent; returns cached value if present |
| `Delete(key) bool` | Remove a key |
| `Purge()` | Clear the entire cache |
| `Len() int` | Current number of entries |
| `Cap() int` | Maximum capacity |

## GC Note

When `K` or `V` is a pointer type (including `string`, which contains an
internal pointer in Go), the node array will still contain GC-traced pointers.
The GC pressure reduction is most dramatic for scalar key/value types (`int`,
`[16]byte`, fixed-size structs).

## License

BSD-2-Clause. See the source files for the full license text.
