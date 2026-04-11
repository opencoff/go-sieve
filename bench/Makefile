# Makefile for go-sieve benchmarks
#
# Targets:
#   all         - run synthetic + trace benchmarks (no miss ratio / GC tests)
#   synthetic   - all synthetic micro-benchmarks (no trace data needed)
#   traces      - trace replay throughput + parallel get (requires trace data)
#   miss-ratio  - miss ratio comparison across all traces
#   gc-pressure - GC pressure test across all traces
#   clean       - remove generated result files
#
# Trace data expected in DATADIR (default: ../data/).
# Fetch with: ./fetch-traces.sh
#
# Trace-driven tests and benchmarks are guarded by the `trace` build tag;
# the relevant targets below pass -tags=trace automatically.

SHELL       := /bin/bash
GOTEST      := go test
TRACETAGS   := -tags=trace
COUNT       := 3
TRACE_COUNT := 1
TIMEOUT     := 60m
DATADIR     := ../data

# Output files
OUT_SYNTHETIC  := results/synthetic.txt
OUT_REPLAY     := results/replay.txt
OUT_MISSRATIO  := results/miss_ratio.txt
OUT_GCPRESSURE := results/gc_pressure.txt

.PHONY: all synthetic traces miss-ratio gc-pressure clean check-traces

all: synthetic traces miss-ratio gc-pressure

synthetic:
	@mkdir -p results
	$(GOTEST) -bench=. -benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee $(OUT_SYNTHETIC)

traces: check-traces
	@mkdir -p results
	$(GOTEST) $(TRACETAGS) -run='^$$' \
		-bench='BenchmarkReplay|BenchmarkParallelGet' \
		-benchmem -count=$(TRACE_COUNT) -timeout=$(TIMEOUT) | tee $(OUT_REPLAY)

miss-ratio: check-traces
	@mkdir -p results
	$(GOTEST) $(TRACETAGS) -run=TestMissRatio -v -timeout=$(TIMEOUT) | tee $(OUT_MISSRATIO)

gc-pressure: check-traces
	@mkdir -p results
	$(GOTEST) $(TRACETAGS) -run=TestGCPressure -v -timeout=$(TIMEOUT) | tee $(OUT_GCPRESSURE)

check-traces:
	@if [ ! -d $(DATADIR) ]; then \
		echo "ERROR: Trace data not found at $(DATADIR)"; \
		echo "Run ./fetch-traces.sh to download trace files."; \
		exit 1; \
	fi

clean:
	rm -f $(OUT_SYNTHETIC) $(OUT_REPLAY) $(OUT_MISSRATIO) $(OUT_GCPRESSURE)
