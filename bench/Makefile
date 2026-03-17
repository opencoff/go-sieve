# Makefile for go-sieve benchmarks
#
# Targets:
#   all         - run synthetic + trace benchmarks (no miss ratio / GC tests)
#   synthetic   - parallel Get/Add/Mixed, memory footprint, GC impact
#   traces      - trace replay throughput + parallel get (requires trace data)
#   miss-ratio  - miss ratio comparison across all traces (slow, ~30min)
#   gc-pressure - GC pressure test across all traces
#   compare     - benchstat comparison: results.txt (old) vs results_optimized.txt (new)
#   clean       - remove generated result files
#
# Trace data expected in DATADIR (default: ../data/).
# Fetch with: ./fetch-traces.sh

SHELL       := /bin/bash
BENCHSTAT   := ~/go/bin/benchstat
GOTEST      := go test
COUNT       := 6
TIMEOUT     := 30m
DATADIR     := ../data

# Output files
OUT_SYNTHETIC  := results/synthetic.txt
OUT_REPLAY     := results/replay.txt
OUT_MISSRATIO  := results/miss_ratio_new.txt
OUT_GCPRESSURE := results/gc_pressure_new.txt

.PHONY: all synthetic traces miss-ratio gc-pressure compare clean check-traces

all: synthetic traces

synthetic:
	@mkdir -p results
	$(GOTEST) -bench='BenchmarkGet_Parallel|BenchmarkAdd_Parallel|BenchmarkMixed_Parallel|BenchmarkMemoryFootprint|BenchmarkGCImpact' \
		-benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee $(OUT_SYNTHETIC)

traces: check-traces
	@mkdir -p results
	$(GOTEST) -bench='BenchmarkReplay|BenchmarkParallelGet' \
		-benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee $(OUT_REPLAY)

miss-ratio: check-traces
	@mkdir -p results
	$(GOTEST) -run=TestMissRatio -v -timeout=$(TIMEOUT) | tee $(OUT_MISSRATIO)

gc-pressure: check-traces
	@mkdir -p results
	$(GOTEST) -run=TestGCPressure -v -timeout=$(TIMEOUT) | tee $(OUT_GCPRESSURE)

# Compare old results (results.txt) vs new synthetic results
compare: $(OUT_SYNTHETIC)
	@echo "=== Synthetic: old vs new ==="
	$(BENCHSTAT) results.txt $(OUT_SYNTHETIC)

# Compare old trace replay vs new
compare-traces: $(OUT_REPLAY)
	@echo "=== Trace replay: old vs new ==="
	@if [ -f results/baseline_replay.txt ]; then \
		$(BENCHSTAT) results/baseline_replay.txt $(OUT_REPLAY); \
	else \
		echo "No baseline_replay.txt found. Run traces with old code first."; \
	fi

check-traces:
	@if [ ! -d $(DATADIR) ]; then \
		echo "ERROR: Trace data not found at $(DATADIR)"; \
		echo "Run ./fetch-traces.sh to download trace files."; \
		exit 1; \
	fi

clean:
	rm -f $(OUT_SYNTHETIC) $(OUT_REPLAY) $(OUT_MISSRATIO) $(OUT_GCPRESSURE)
