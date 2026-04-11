# bench — comparison benchmarks and trace replay

This directory is a separate Go module (`github.com/opencoff/go-sieve/bench`)
that benchmarks go-sieve against hashicorp/golang-lru (LRU and ARC). It uses
a `replace` directive to point at the parent directory, so changes to `../sieve.go`
are picked up immediately without publishing.

## Contents

| File | Purpose |
|------|---------|
| `bench_test.go` | Synthetic micro-benchmarks: parallel Get/Add/Mixed, memory footprint, GC impact. Compares Sieve vs LRU vs ARC. |
| `trace.go` | Trace file parsers: `LoadCSV` (Twitter, Meta CDN) and `LoadOracleGeneral` (mmap-based binary parser). |
| `trace_test.go` | Smoke tests that load each trace format and print request count / unique keys. |
| `replay_test.go` | Trace-replay harness: `TestMissRatio`, `BenchmarkReplay`, `BenchmarkParallelGet`, `TestGCPressure`. |
| `fetch-traces.sh` | Downloads and decompresses trace datasets (see below). |
| `trace-bench-design.md` | Design document for the SIEVE-k extension and trace benchmarks. |
| `results/` | Saved benchmark output for benchstat comparison. |

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

### 1. Miss Ratio (`TestMissRatio`)

For each trace, we create a cache sized at **10% of unique keys** and replay
every request sequentially: `Get()`, on miss `Add()`. We compare five cache
variants:

- **SIEVE k=1** — classic single-bit visited flag
- **SIEVE k=2** — 2-bit saturating counter (survives 2 eviction passes)
- **SIEVE k=3** — 3-level saturating counter
- **LRU** — hashicorp/golang-lru
- **ARC** — hashicorp/golang-lru/arc (adaptive replacement cache)

### 2. Sequential Replay Throughput (`BenchmarkReplay`)

Same replay loop as miss ratio, but measured as a Go benchmark with
`-benchmem`. Reports ns/op, bytes/op, allocs/op, and miss ratio per
iteration. Exercises the full Add+Get+eviction path.

### 3. Parallel Get Throughput (`BenchmarkParallelGet`)

Warms the cache with a full replay, then hammers `Get()` from
`GOMAXPROCS` goroutines using `b.RunParallel`. This isolates the
lock-free read path — the headline number for concurrent read-heavy
workloads.

### 4. GC Pressure (`TestGCPressure`)

Replays a trace and measures `runtime.MemStats` deltas: NumGC,
PauseTotalNs, TotalAlloc, HeapObjects. Shows the memory efficiency
advantage of the array-backed design.

## Running

```bash
cd bench

# --- Trace replay ---

# Miss ratios (prints table, ~15 min for all traces)
go test -run=TestMissRatio -v -timeout=30m

# Sequential throughput (use -run='^$' to skip tests)
go test -run='^$' -bench=BenchmarkReplay -benchmem -count=6 -timeout=60m \
    > results/baseline.txt

# Parallel Get throughput
go test -run='^$' -bench=BenchmarkParallelGet -benchmem -count=6 -timeout=30m \
    >> results/baseline.txt

# GC pressure
go test -run=TestGCPressure -v -timeout=10m

# --- Subset of traces (faster iteration) ---
# Use -bench regex to filter by trace name:
go test -run='^$' -bench='BenchmarkReplay/msr_2007/msr_hm_0/' \
    -benchmem -count=6

# --- Synthetic micro-benchmarks (no trace data needed) ---
go test -bench='Benchmark(Get|Add|Mixed)_' -benchmem -count=6

# --- Compare before/after with benchstat ---
benchstat results/baseline.txt results/after.txt
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

- **Parallel `Get()`**: 1.0–10 ns/op across all 18 replayed traces vs
  ~270–620 ns/op for LRU/ARC. ~100–300x faster.
- **Miss ratio**: SIEVE k=1 beats LRU on 13 of 18 traces, ties or beats
  ARC on 7 of 18. SIEVE k=3 produces the best overall miss ratio on
  msr_prn_1 (0.3796 vs LRU 0.4341, ARC 0.4148).
- **Memory during replay**: 2.7x less than LRU, 6.5x less than ARC on
  the 13.2M-request meta_storage/block_traces_1 trace.
