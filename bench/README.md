# bench — comparison benchmarks and trace replay

This directory is a separate Go module (`github.com/opencoff/go-sieve/bench`)
that benchmarks go-sieve against hashicorp/golang-lru (LRU and ARC). It uses
a `replace` directive to point at the parent directory, so changes to `../sieve.go`
are picked up immediately without publishing.

## Contents

| File | Build tag | Purpose |
|------|-----------|---------|
| `doc.go` | (none) | Stub `package bench` declaration; always present. |
| `bench_test.go` | `!trace` | Synthetic micro-benchmarks: parallel Get/Add/Probe/Delete/Mixed, memory footprint, GC impact. Compares Sieve vs LRU vs ARC. |
| `trace.go` | `trace` | Trace file parsers: `LoadCSV` (Twitter, Meta CDN) and `LoadOracleGeneral` (mmap-based binary parser). |
| `trace_test.go` | `trace` | Smoke tests that load each trace format and print request count / unique keys. |
| `replay_test.go` | `trace` | Trace-replay harness: `TestMissRatio`, `BenchmarkReplay`, `BenchmarkParallelGet`, `BenchmarkParallelReplay`, `TestGCPressure`. |
| `fetch-traces.sh` | — | Downloads and decompresses trace datasets (see below). |
| `trace-bench-design.md` | — | Design document for the SIEVE-k extension and trace benchmarks. |
| `results/` | — | Saved benchmark output for benchstat comparison. |

The `!trace` / `trace` tags are mutually exclusive: `go test -bench=.` without
`-tags=trace` picks up only the synth benchmarks; with `-tags=trace`, only the
trace benchmarks + tests. The Makefile leverages this so no `-bench=FILTER`
regex is needed anywhere.

## API asymmetry: SIEVE replay uses Probe, LRU/ARC use Get+Add

The trace-replay harness and `BenchmarkProbe_Parallel` use `sieve.Probe()`
for SIEVE — a single call that inserts on miss and marks the visited bit
on hit. It's the idiomatic API for the get-or-insert pattern a trace
replay exercises, and it preserves SIEVE's promotion semantics exactly
(both `Get` and `Probe` call `LockAndMark` on hit).

LRU and ARC use the `Get` + `Add`-on-miss idiom. Their superficial
lookalikes are **not** semantic equivalents:

| Method | Promotes recency on hit? |
|--------|:-:|
| `lru.Cache.ContainsOrAdd` | No — uses `Contains` |
| `lru.Cache.PeekOrAdd`     | No — uses `Peek` |
| `arc.ARCCache.*OrAdd`     | Does not exist |

Both LRU lookalikes deliberately skip recency promotion on the hit path.
Using them in a replay harness would corrupt LRU's eviction order (items
would never get re-promoted) and inflate its miss ratio. The honest
comparison therefore uses each library's idiomatic pattern:

- **SIEVE** — `Probe(k, v)` (one call, TOCTOU-safe)
- **LRU/ARC** — `Get(k)` fast path, fallback to `Add(k, v)` on miss

Miss ratios remain comparable because the sequence of nodes visited and
evicted is identical; only the SIEVE call path collapses into one
function invocation.

## Trace Datasets

All benchmarks replay real-world cache access traces from published research
datasets. Trace files live in `../data/` (gitignored) and are loaded at test
time via mmap (oracleGeneral) or buffered I/O (CSV). Benchmarks skip
gracefully when trace files are absent.

### Sources

We use traces from the [CacheLib / libCacheSim](https://cachelib.org/) trace
repository, hosted on S3 at `s3://cache-datasets/`.

| Dataset | Format | Records | Source |
|---------|--------|---------|--------|
| MSR Cambridge 2007 | oracleGeneral | 14 volumes, 3.9M–45.7M requests each | Enterprise block I/O (file servers, web, proxy, print) |
| Meta Storage 2022 (Tectonic) | oracleGeneral | 5 block traces, 13–14M requests each | Distributed storage block I/O |

**oracleGeneral** is a packed binary format (24 bytes/record, little-endian):

| Offset | Type | Field |
|--------|------|-------|
| 0 | uint32 | timestamp |
| 4 | uint64 | obj_id (cache key) |
| 12 | uint32 | obj_size |
| 16 | int64 | next_access_vtime |

The parser (`LoadOracleGeneral` in `trace.go`) mmaps the file and extracts
`obj_id` from each record. Unique key count is computed during load.

### Downloading Traces

```bash
cd bench
bash fetch-traces.sh
```

The script downloads from the S3 bucket, decompresses `.zst` files with
`zstd`, and places them under `../data/`:

```
../data/
├── meta_storage/
│   └── block_traces_{1..5}.oracleGeneral.bin
└── msr_2007/
    ├── msr_hm_0.oracleGeneral
    ├── msr_prn_{0,1}.oracleGeneral
    ├── msr_proj_{0,1,2,4}.oracleGeneral
    ├── msr_prxy_{0,1}.oracleGeneral
    ├── msr_src1_{0,1}.oracleGeneral
    ├── msr_usr_{1,2}.oracleGeneral
    └── msr_web_2.oracleGeneral
```

Total disk: ~11 GB decompressed. `msr_prxy_1` (3.8 GB) is the largest single
file and is skipped by default in benchmarks (>2 GB threshold).

Prerequisites: `zstd` (`brew install zstd`), `curl` or `wget`.

## What We Measured

### Trace-driven (`-tags=trace`, requires `../data/`)

#### 1. Miss Ratio (`TestMissRatio`)

For each trace, we create a cache sized at **10% of unique keys** and replay
every request sequentially. SIEVE uses `Probe()`; LRU/ARC use `Get+Add`.
We compare five cache variants:

- **SIEVE k=1** — classic single-bit visited flag
- **SIEVE k=2** — 2-bit saturating counter (survives 2 eviction passes)
- **SIEVE k=3** — 3-level saturating counter
- **LRU** — hashicorp/golang-lru
- **ARC** — hashicorp/golang-lru/arc (adaptive replacement cache)

#### 2. Sequential Replay Throughput (`BenchmarkReplay`)

Same replay loop as miss ratio, but measured as a Go benchmark with
`-benchmem`. Reports ns/op, bytes/op, allocs/op, and miss ratio per
iteration. Exercises the full get-or-insert + eviction path.

#### 3. Parallel Get Throughput (`BenchmarkParallelGet`)

Pre-warms the cache with a full replay, then hammers `Get()` from
`GOMAXPROCS` goroutines using `b.RunParallel`. Isolates the
lock-free read path — the headline number for concurrent read-heavy
workloads where the cache is already warm.

#### 4. Parallel Replay Throughput (`BenchmarkParallelReplay`)

Starts with a cold cache, no warmup. Goroutines hammer the trace
through `Probe()` (SIEVE) or `Get+Add` (LRU/ARC) in parallel. This is
the complement to `BenchmarkParallelGet`: together they bracket the
steady-state workload. `BenchmarkParallelGet` shows the warm-read
ceiling; `BenchmarkParallelReplay` shows throughput when misses, writes,
and evictions are still happening alongside reads.

#### 5. GC Pressure (`TestGCPressure`)

Replays a trace and measures `runtime.MemStats` deltas: NumGC,
PauseTotalNs, TotalAlloc, HeapObjects. Shows the memory efficiency
advantage of the array-backed design.

### Synthetic (no trace tag, no data required)

| Benchmark | SIEVE | LRU | ARC | Notes |
|---|:-:|:-:|:-:|---|
| `BenchmarkGet_Parallel` | yes | yes | yes | Warm cache, uniform random Get |
| `BenchmarkAdd_Parallel` | yes | yes | yes | Random Add over 2x cache-size key range |
| `BenchmarkProbe_Parallel` | yes | – | – | SIEVE only — see API asymmetry section |
| `BenchmarkDelete_Parallel` | yes | yes | yes | Pre-fill 2x cache-size, then parallel Delete |
| `BenchmarkMixed_Parallel` | yes | yes | yes | 60% Get / 30% Add / 10% Delete |
| `BenchmarkZipf_Get_Parallel` | yes | yes | yes | Zipfian distribution, three skews |
| `BenchmarkMemoryFootprint` | yes | yes | yes | HeapAlloc delta at 100k / 500k / 1M fill |
| `BenchmarkGCImpact` | yes | yes | yes | GC pause at 1M entries under mixed workload |

`BenchmarkProbe_Parallel` is SIEVE-only because LRU's `PeekOrAdd` /
`ContainsOrAdd` skip recency promotion and are not semantic
equivalents (see the asymmetry section above). ARC has no Probe-like
method at all.

## Running

The Makefile is the canonical entry point — it uses the build-tag
separation described above to run the right benchmark set for each
target, with no `-bench=FILTER` regex anywhere.

```bash
cd bench

# Synthetic comparison benchmarks (no trace data needed).
# Writes results/synthetic.txt.
make bench

# Trace replay + miss ratio + GC pressure (requires ../data/).
# Writes results/trace.txt.
make trace

# Compile-check (no Test* functions without trace tag, so effectively a
# type-check pass).
make test
make race

# Clean results.
make clean
```

From the repo root, there's also a top-level `Makefile` that cascades
into `bench/`:

```bash
# From repo root:
make test    # parent tests + bench compile-check
make race    # parent race tests + bench race compile-check
make bench   # parent SIEVE regression benches + bench comparison benches
make trace   # trace replay (delegates to bench/)
make all     # everything
```

### Running a subset

If you want to filter to a single trace or benchmark for faster
iteration, invoke `go test` directly — the Makefile deliberately does
not offer filter flags:

```bash
cd bench

# One trace:
go test -tags=trace -bench='BenchmarkReplay/msr_2007/msr_hm_0/' \
    -benchmem -count=3

# One synth benchmark:
go test -bench=BenchmarkProbe_Parallel -benchmem -count=6
```

### benchstat

```bash
# Save a baseline, make changes, compare:
cp results/synthetic.txt results/baseline.txt
# ... edit code ...
make bench
benchstat results/baseline.txt results/synthetic.txt
```

## Running With Your Own Traces

The benchmark harness auto-discovers all `.oracleGeneral` and
`.oracleGeneral.bin` files under `../data/`, recursively. To add your
own trace:

1. Convert to oracleGeneral format (24 bytes/record, little-endian — see
   table above). Many traces from the libCacheSim project are already in
   this format.
2. Place the file anywhere under `../data/`, e.g. `../data/my_traces/workload.oracleGeneral`.
3. Run benchmarks — it will appear automatically as a subtest named after
   its path relative to `data/`.

For CSV traces, add a parse function in `trace.go` (see `ParseTwitter` for
the pattern) and wire it into `replay_test.go`.

## Results

Full results (machine config, per-trace tables for every trace, raw
benchmark output files, reproduction commands) live in
[`../bench-results.md`](../bench-results.md) at the repo root. The tables
there are regenerated from the current hardware with a single unfiltered
`go test -bench=. -benchmem` invocation — no selected subsets, no hand-
curated numbers. Raw output files are committed under `results/`.

Headline numbers from the current run:

- **Parallel `Get()`**: 1.0–9.2 ns/op across all 18 replayed traces vs
  ~180–590 ns/op for LRU/ARC. ~100–300x faster. This is the warm-cache
  read ceiling from `BenchmarkParallelGet`.
- **Parallel Replay**: 6.4–25 ns/op for SIEVE vs ~400–550 ns/op for
  LRU/ARC (~18–62x faster). This is the cold-cache steady-state
  workload from `BenchmarkParallelReplay`.
- **Miss ratio**: SIEVE k=1 beats LRU on 13 of 18 traces, ties or beats
  ARC on 7 of 18. SIEVE k=3 produces the best overall miss ratio on
  msr_prn_1 (0.3796 vs LRU 0.4341, ARC 0.4148).
- **Memory during replay**: 2.7x less than LRU, 6.5x less than ARC on
  the 13.2M-request meta_storage/block_traces_1 trace.

### Understanding the parallel ns/op numbers

The sub-2 ns/op numbers from `BenchmarkParallelGet` are real but need
context: they are **aggregate throughput**, not per-operation latency.

Go's `b.RunParallel` distributes `b.N` total operations across
`GOMAXPROCS` goroutines and reports `ns/op = wall_clock / b.N`. When
32 goroutines complete 1 billion Get()s in ~1 second, the reported
number is ~1.0 ns/op — meaning "the system produces one completed Get
every ~1 ns." The **per-core latency** is ~32 ns (1.0 × 32 cores),
which is consistent with two L1/L2-hot atomic operations on a 5 GHz
CPU.

This works because SIEVE's `Get()` has no shared serialization point:
one atomic `Load` on an xsync.MapOf bucket, one atomic CAS on the
per-slot visited bit — three cache lines, no mutex. Thirty-two cores
operate independently; throughput scales linearly with core count.

LRU/ARC report ~200–600 ns/op under the same conditions — not because a
single Get is 200x slower, but because every Get takes a mutex. The 32
goroutines serialize through one lock, so throughput is flat regardless
of core count. On a single goroutine (see `BenchmarkReplay`,
sequential), SIEVE and LRU are within 15% of each other — the
**~100–300x gap is a concurrency-scaling story, not a raw-speed story**.

After warmup, the working data structures (map buckets, node array,
visited bitfield) are resident in CPU cache — L1/L2 for small traces,
L3 for larger ones. Real workloads with lower temporal locality will
see higher per-core latency, but the relative advantage over
mutex-bound LRU/ARC holds whenever there is any parallelism at all.
