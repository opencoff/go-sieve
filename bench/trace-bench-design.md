# SIEVE-k Implementation & Benchmark Design

## Goal

Extend go-sieve to support SIEVE-k (multi-bit saturating counters instead of
single visited bit). Measure impact using real production trace replay across
three workload classes.

---

## 1. Repo Layout

```
$REPO_ROOT/
├── sieve.go                    # main implementation
├── atomic_bitfield.go          # packed visited bits → generalized counters
├── sieve_bench_test.go         # existing micro-benchmarks
├── sieve_bench_custom_test.go  # existing micro-benchmarks
├── data/                       # trace files (gitignored, user-prepared)
│   ├── twitter/
│   │   └── cluster52.csv
│   ├── meta_cdn/
│   │   └── *.csv
│   └── tencent_block/
│       └── *.oracleGeneral
└── bench/                      # sub-module (own go.mod, existing)
    ├── go.mod                  # existing; has hashicorp deps + replace ../
    ├── trace.go                # NEW: CSV + oracleGeneral parsers
    ├── trace_test.go           # NEW: verify parsers
    ├── replay_test.go          # NEW: trace-replay benchmarks
    ├── fetch-traces.sh         # NEW: download + decompress script
    ├── README.md               # NEW: data prep instructions
    └── ...                     # existing hashicorp comparison benchmarks
```

---

## 2. Trace Data

### Trace Selection

| Role | Dataset | Format | Why |
|------|---------|--------|-----|
| Regression (must not hurt) | Twitter cluster52 | CSV | Highly skewed Zipfian (α=1-2.5), almost no scans. k>1 should be neutral. |
| Mixed workload | Meta CDN 2023 | CSV | CDN edge traffic with crawlers/prefetch bursts. Some scan-like patterns. |
| Scan-heavy (k>1 should help) | Tencent CBS | oracleGeneral | Block I/O with sequential scans. Where vanilla SIEVE is weakest. |

### Data Files — Pre-downloaded & Decompressed

The `fetch-traces.sh` script (see Section 7) handles download and decompression.
All files below are assumed present and decompressed before running benchmarks.

**Twitter cluster52:**
```
Source:  https://ftp.pdl.cmu.edu/pub/datasets/twemcacheWorkload/open_source/cluster52.sort.zst
Format:  timestamp,anonymized_key,key_size,value_size,client_id,op,TTL
Placed:  data/twitter/cluster52.csv
```

**Meta CDN 2023 (one cluster — e.g., "nha"):**
```
Source:  https://s3.amazonaws.com/cache-datasets/cache_dataset_txt/2023_metaCDN/
Format:  timestamp,cacheKey,OpType,objectSize,responseSize,...
Placed:  data/meta_cdn/<filename>.csv
```

**Tencent CBS (one volume file):**
```
Source:  https://s3.amazonaws.com/cache-datasets/cache_dataset_oracleGeneral/2020_tencentBlock/
Format:  oracleGeneral binary (24 bytes/record)
Placed:  data/tencent_block/<filename>.oracleGeneral
```

**MSR Cambridge (optional, smaller block trace for quick iteration):**
```
Source:  https://s3.amazonaws.com/cache-datasets/cache_dataset_oracleGeneral/2007_msr/
Format:  oracleGeneral binary (24 bytes/record)
Placed:  data/msr/<filename>.oracleGeneral
```

---

## 3. Trace Parser: `bench/trace.go`

### Core Types

```go
type Request[T any] struct {
    T
}

type Trace[T any] struct {
    Requests []Request[T]
    Unique   int
}
```

### CSV Loader

```go
func LoadCSV[T any](path string, parse func(fields []string) (T, bool)) (*Trace[T], error)
```

- `parse` func: takes split CSV fields, returns typed value + ok. Returns
  `false` to skip line (malformed, wrong op, header row, etc.).
- Handles `.gz` (gzip.NewReader) based on file suffix.
- Reads entire file into `[]Request[T]`.
- Counts unique keys during load (needs `T comparable` or caller provides
  key-extraction — design decision for author).

**Twitter parse func:**
```go
func parseTwitter(fields []string) (string, bool) {
    if len(fields) < 2 { return "", false }
    return fields[1], true  // anonymized_key at index 1
}
```

**Meta CDN parse func:**
```go
func parseMetaCDN(fields []string) (string, bool) {
    if len(fields) < 2 { return "", false }
    return fields[1], true  // cacheKey at index 1
}
```

### oracleGeneral Loader

```go
func LoadOracleGeneral(path string) (*Trace[uint64], error)
```

Uses `github.com/opencoff/go-mmap` to mmap the decompressed file. Walks the
mapped region in 24-byte strides:

```
Offset 0:  uint32  timestamp
Offset 4:  uint64  obj_id          ← this is the key
Offset 12: uint32  obj_size
Offset 16: int64   next_access_vtime
```

All little-endian. Extract `obj_id` from each record as `Request[uint64]`.
Count unique keys via map pass. The mmap ensures zero-copy scan regardless of
file size.

### Parser Tests: `bench/trace_test.go`

- `TestLoadTwitterCSV`: load `data/twitter/cluster52.csv`, print request count,
  unique keys, first 5 entries.
- `TestLoadMetaCDNCSV`: same for Meta CDN.
- `TestLoadTencentOracleGeneral`: load a Tencent CBS oracleGeneral file, print
  stats.
- Small hand-crafted fixture tests for edge cases (empty lines, malformed rows).

---

## 4. Benchmark Harness: `bench/replay_test.go`

### Cache Size

**10% of unique keys.** Compute from `Trace.Unique`.

### A. Miss Ratio (Sanity Check)

Regular test, not a benchmark. Prints table of miss ratios per trace per variant.

```go
func TestMissRatio(t *testing.T)
    // For each trace (twitter, meta_cdn, tencent_block):
    //   For each variant (sieve k=1, k=2, k=3, hashicorp LRU, ARC):
    //     replay, tally, t.Logf()
```

### B. Throughput — Sequential Replay

Per-trace, per-variant benchmarks:

```go
func BenchmarkReplay_Twitter_SieveK1(b *testing.B)
func BenchmarkReplay_Twitter_SieveK3(b *testing.B)
func BenchmarkReplay_Twitter_LRU(b *testing.B)
func BenchmarkReplay_Twitter_ARC(b *testing.B)
// ... repeat for MetaCDN, TencentBlock
```

Each:
1. Pre-load trace via `sync.Once`
2. Create cache at 10% capacity
3. `b.ResetTimer()`
4. Replay: `Get()`, if miss `Add()`
5. `b.ReportAllocs()`
6. `b.ReportMetric(missRatio, "miss-ratio")`

For oracleGeneral traces, benchmark uses `Sieve[uint64, struct{}]`.
For CSV traces, benchmark uses `Sieve[string, struct{}]`.

### C. Throughput — Parallel Reads

Warm cache with full replay, then `b.RunParallel` on `Get()` only.

```go
func BenchmarkParallelGet_Twitter_SieveK1(b *testing.B)
func BenchmarkParallelGet_Twitter_SieveK3(b *testing.B)
func BenchmarkParallelGet_Twitter_LRU(b *testing.B)
func BenchmarkParallelGet_Twitter_ARC(b *testing.B)
```

### D. GC Pressure

```go
func TestGCPressure(t *testing.T)
    // runtime.GC(); ReadMemStats before
    // replay trace
    // ReadMemStats after
    // report: NumGC, PauseTotalNs, TotalAlloc, HeapObjects deltas
```

---

## 5. Workflow

### Step 1: Data Prep

```bash
cd bench && bash fetch-traces.sh
```

### Step 2: Trace Parser

```bash
cd bench
go test -run=TestLoad -v
```

### Step 3: Baseline Benchmarks (current k=1)

1. Record existing micro-benchmark baseline:

```bash
cd $REPO_ROOT
go test -bench=. -benchmem -count=6 > bench/results/micro-baseline.txt
```

2. Run trace-replay benchmarks (k=1 + hashicorp only — k>1 constructors
   default to k=1 until Step 5):
```bash
cd bench
go test -run=TestMissRatio -v
go test -bench=BenchmarkReplay -benchmem -count=6 -timeout=30m > results/baseline.txt
go test -bench=BenchmarkParallelGet -benchmem -count=6 -timeout=10m >> results/baseline.txt
go test -run=TestGCPressure -v
```

### Step 4: Commit

```bash
git add bench/trace.go bench/trace_test.go bench/replay_test.go \
        bench/fetch-traces.sh bench/README.md
git commit -m "bench: add trace-replay harness with baseline results"
```

### Step 5: SIEVE-k Implementation

Changes in `$REPO_ROOT`:

**`atomic_bitfield.go`** — generalize to multi-bit counters:
- `Test(i) bool` → `Read(i) uint64`
- `Set(i)` → `Increment(i)` (CAS, saturate at k)
- `Clear(i)` → `Decrement(i)` (CAS, saturate at 0, return new value)
- New fields: `bitsPerSlot`, `slotsPerWord`, `maxVal`, `mask`
- Index: `wordIdx = i / slotsPerWord`, `shift = (i % slotsPerWord) * bitsPerSlot`

**`sieve.go`** — wire in k:
- Constructor accepts k (API shape is author's choice)
- `Get()`: `Increment()` instead of `Set()`
- Eviction loop: `Read() > 0` → `Decrement()`, advance; `== 0` → evict

**Validation:**
```bash
# All existing tests green
go test -v ./...
# Micro-benchmarks must not regress at k=1
go test -bench=. -benchmem -count=6 > bench/results/micro-sievek.txt
benchstat bench/results/micro-baseline.txt bench/results/micro-sievek.txt
```

If k=1 regresses: STOP and present results. Get human guidance on
next steps.

### Step 6: SIEVE-k Benchmarks + benchstat

```bash
cd bench
go test -run=TestMissRatio -v
go test -bench=BenchmarkReplay -benchmem -count=6 -timeout=30m > results/sievek.txt
go test -bench=BenchmarkParallelGet -benchmem -count=6 -timeout=10m >> results/sievek.txt
benchstat results/baseline.txt results/sievek.txt
go test -run=TestGCPressure -v
```

---

## 6. SIEVE-k Implementation Detail

### Bitfield Generalization

```
bitsPerSlot  = bits.Len(uint(k))    // k=1→1, k=2..3→2, k=4..7→3
slotsPerWord = 64 / bitsPerSlot
mask         = (1 << bitsPerSlot) - 1
```

k=1: `slotsPerWord=64`, `mask=1` — identical to current packing.
k=3: `slotsPerWord=32`, `mask=0x3`, 32KB per 1M entries (vs 16KB).

**Increment(i):**
```
word := atomic.LoadUint64(&words[wordIdx])
val  := (word >> shift) & mask
if val >= maxVal { return }           // saturation early-exit
new  := (word & ^(mask << shift)) | ((val+1) << shift)
CAS(&words[wordIdx], word, new)       // retry on failure
```

**Decrement(i) → uint64:**
```
word := atomic.LoadUint64(&words[wordIdx])
val  := (word >> shift) & mask
if val == 0 { return 0 }
new  := (word & ^(mask << shift)) | ((val-1) << shift)
CAS(&words[wordIdx], word, new)
return val - 1
```

### New Unit Tests (in $REPO_ROOT)

- Item accessed k+1 times survives k eviction passes
- Item accessed once evicted on first pass
- Counter saturates (100 accesses with k=3 → 3 passes to evict)

---

## 7. Scripts & Docs

### `bench/fetch-traces.sh`

Downloads and decompresses all traces. Uses curl or wget (whichever is
available). Requires `zstd` for decompression.

```bash
#!/usr/bin/env bash
set -euo pipefail

DATADIR="$(cd "$(dirname "$0")/.." && pwd)/data"

# -- helpers --
fetch() {
    local url="$1" dest="$2"
    if [ -f "$dest" ]; then
        echo "  SKIP $dest (exists)"
        return
    fi
    echo "  GET  $url"
    if command -v curl &>/dev/null; then
        curl -fSL --create-dirs -o "$dest" "$url"
    elif command -v wget &>/dev/null; then
        mkdir -p "$(dirname "$dest")"
        wget -q -O "$dest" "$url"
    else
        echo "ERROR: need curl or wget" >&2; exit 1
    fi
}

decompress_zst() {
    local src="$1"
    local dst="${src%.zst}"
    if [ -f "$dst" ]; then
        echo "  SKIP $dst (exists)"
        return
    fi
    echo "  ZSTD $src"
    zstd -d "$src" -o "$dst"
}

# -- Twitter cluster52 --
echo "=== Twitter cluster52 ==="
mkdir -p "$DATADIR/twitter"
fetch "https://ftp.pdl.cmu.edu/pub/datasets/twemcacheWorkload/open_source/cluster52.sort.zst" \
      "$DATADIR/twitter/cluster52.sort.zst"
decompress_zst "$DATADIR/twitter/cluster52.sort.zst"
# Rename to .csv for clarity (it is CSV, just no .csv extension)
[ -f "$DATADIR/twitter/cluster52.csv" ] || \
    mv "$DATADIR/twitter/cluster52.sort" "$DATADIR/twitter/cluster52.csv"

# -- Meta CDN 2023 (nha cluster, day 1) --
# NOTE: The S3 bucket serves a JS-rendered index. You may need to browse
#   https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_txt/2023_metaCDN/
# to find exact filenames. Adjust the URL below once you identify the file.
echo "=== Meta CDN ==="
mkdir -p "$DATADIR/meta_cdn"
echo "  Meta CDN traces require browsing the S3 index to find exact filenames."
echo "  Visit: https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_txt/2023_metaCDN/"
echo "  Download one cluster file (e.g., nha day 1) and place in $DATADIR/meta_cdn/"
echo "  Then decompress: zstd -d <file>.zst"

# -- Tencent CBS (oracleGeneral, one file) --
echo "=== Tencent CBS ==="
mkdir -p "$DATADIR/tencent_block"
echo "  Tencent CBS traces require browsing the S3 index to find exact filenames."
echo "  Visit: https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_oracleGeneral/2020_tencentBlock/"
echo "  Download one trace file and place in $DATADIR/tencent_block/"
echo "  Then decompress: zstd -d <file>.zst"

# -- MSR Cambridge (oracleGeneral, optional, smaller) --
echo "=== MSR Cambridge (optional) ==="
mkdir -p "$DATADIR/msr"
echo "  Visit: https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_oracleGeneral/2007_msr/"
echo "  Download one trace file and place in $DATADIR/msr/"
echo "  Then decompress: zstd -d <file>.zst"

echo ""
echo "Done. Verify files in $DATADIR/"
echo "Twitter cluster52 should be fully downloaded."
echo "Meta CDN, Tencent CBS, MSR require manual file selection from S3 index."
```

### `bench/README.md`

```markdown
# Benchmark Data Preparation

## Prerequisites

- `zstd` (for decompression): `brew install zstd` / `apt install zstd`
- `curl` or `wget`
- ~10-20 GB disk space for decompressed traces

## Quick Start

    bash fetch-traces.sh

This downloads Twitter cluster52 automatically. Meta CDN, Tencent CBS, and
MSR Cambridge require browsing the S3 index to select specific files (the
bucket uses a JS-rendered directory listing).

## Traces

### Twitter cluster52 (CSV)

- **Role:** Regression test — highly skewed Zipfian, no scans
- **Format:** `timestamp,key,key_size,value_size,client_id,op,TTL`
- **Column used:** index 1 (anonymized key)
- **Location:** `data/twitter/cluster52.csv`

### Meta CDN 2023 (CSV)

- **Role:** Mixed workload — CDN edge with crawler/prefetch bursts
- **Format:** `timestamp,cacheKey,OpType,objectSize,responseSize,...`
- **Column used:** index 1 (cacheKey)
- **Location:** `data/meta_cdn/<cluster>_<day>.csv`
- **Source:** https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_txt/2023_metaCDN/

### Tencent CBS (oracleGeneral binary)

- **Role:** Scan-heavy — block I/O with sequential access
- **Format:** 24-byte packed structs (uint32 ts, uint64 obj_id, uint32 size, int64 next_vtime)
- **Key:** obj_id (uint64)
- **Location:** `data/tencent_block/<file>.oracleGeneral`
- **Source:** https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_oracleGeneral/2020_tencentBlock/

### MSR Cambridge (oracleGeneral binary, optional)

- **Role:** Smaller block trace for quick iteration
- **Format:** same oracleGeneral
- **Location:** `data/msr/<file>.oracleGeneral`
- **Source:** https://s3.amazonaws.com/cache-datasets/index.html#cache_dataset_oracleGeneral/2007_msr/

## Data Directory Structure

    data/
    ├── twitter/
    │   └── cluster52.csv
    ├── meta_cdn/
    │   └── <cluster>_<day>.csv
    ├── tencent_block/
    │   └── <file>.oracleGeneral
    └── msr/                        # optional
        └── <file>.oracleGeneral

All files must be decompressed (no .zst suffix) before running benchmarks.
The Go benchmark harness mmaps oracleGeneral files directly and reads CSVs
into memory. Warm the page cache by running once before timing:

    cat data/twitter/cluster52.csv > /dev/null
    cat data/tencent_block/*.oracleGeneral > /dev/null

## Running Benchmarks

    cd bench

    # Sanity check — miss ratios
    go test -run=TestMissRatio -v

    # Full throughput benchmarks (save for benchstat)
    go test -bench=BenchmarkReplay -benchmem -count=6 -timeout=30m > results/baseline.txt

    # Parallel read benchmarks
    go test -bench=BenchmarkParallelGet -benchmem -count=6 -timeout=10m >> results/baseline.txt

    # GC pressure
    go test -run=TestGCPressure -v

    # After SIEVE-k changes, compare:
    go test -bench=BenchmarkReplay -benchmem -count=6 -timeout=30m > results/sievek.txt
    benchstat results/baseline.txt results/sievek.txt
```

---

## 8. Constraints

- **k=1 must not regress.** Existing micro-benchmarks are the gate.
- **Cache is slot-counted, not byte-budgeted.** Matches go-sieve's model.
- **Trace must fit in RAM** (CSV) or be mmap'd (oracleGeneral).
- **IBM ARC patent.** Hashicorp ARC fine for benchmarking. Flag for legal
  review before TernStack production use.

---

## 9. Expected Outcomes

| Metric | Expectation | Rationale |
|--------|------------|-----------|
| Miss ratio k=1 vs k=3 (Twitter) | No change | Zipfian, no scans |
| Miss ratio k=1 vs k=3 (Tencent CBS) | k=3 better | Counter survives scan bursts |
| Miss ratio k=3 vs ARC (all) | Comparable or better | SIEVE already beats ARC on web traces |
| Throughput k=1 vs k=3 | Within 5-10% | Saturation early-exit limits extra CAS |
| Throughput k=1 vs ARC | 2-4x faster | Lock-free Get vs mutex-gated Get |
| GC pressure k=1 vs k=3 | Negligible | 16KB → 32KB bitfield for 1M cache |

The Tencent CBS result is the one that matters for SIEVE-k. If k=3 closes the
gap with ARC on block traces while maintaining SIEVE's advantage on web traces,
the generalization is justified.
