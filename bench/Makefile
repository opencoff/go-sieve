# Makefile for the bench/ module (comparison benchmarks + trace replay).
#
# Targets:
#   bench   - synthetic comparison benchmarks (SIEVE vs LRU vs ARC)
#   trace   - trace replay benchmarks + miss-ratio + GC-pressure tests
#   test    - compile-check (bench module has no Test* without -tags=trace)
#   race    - compile-check under -race
#   clean   - remove generated result files
#
# Build tags cleanly separate the two worlds — no -bench=FILTER expression
# is used anywhere:
#   bench_test.go   has //go:build !trace  (synth only)
#   trace.go        has //go:build trace   (parser library)
#   trace_test.go   has //go:build trace   (parser smoke tests)
#   replay_test.go  has //go:build trace   (replay + miss-ratio + GC)
#
# So `go test -bench=.` picks up exactly one of the two sets depending on
# whether -tags=trace is passed.
#
# Trace data expected in DATADIR (default: ../data/). Fetch with:
#   ./fetch-traces.sh

SHELL       := /bin/bash
GOTEST      := go test
COUNT       := 3
TRACE_COUNT := 1
TIMEOUT     := 60m
DATADIR     := ../data

# Core counts for the scaling sweep; override with SWEEP_PROCS="1 4 16".
SWEEP_PROCS := 1 2 4 8 16 32
SWEEP_BENCH := Get_Parallel|Mixed_Parallel

OUT_BENCH := results/synthetic.txt
OUT_TRACE := results/trace.txt
OUT_SWEEP := results/sweep.txt

.DEFAULT_GOAL := help
.PHONY: help all bench trace sweep compare test race clean check-traces

help:
	@echo "bench/Makefile — comparison benchmarks and trace replay"
	@echo ""
	@echo "Targets:"
	@echo "  bench   - synthetic comparison benchmarks (SIEVE vs LRU vs ARC)"
	@echo "            -> $(OUT_BENCH)"
	@echo "  trace   - trace replay + miss-ratio + GC-pressure (needs $(DATADIR))"
	@echo "            -> $(OUT_TRACE)"
	@echo "  sweep   - core-scaling sweep: $(SWEEP_BENCH) at GOMAXPROCS=$(SWEEP_PROCS)"
	@echo "            -> $(OUT_SWEEP)"
	@echo "  compare - benchstat OLD=<file> NEW=<file> (defaults to $(OUT_BENCH))"
	@echo "  test    - go test (compile-check; no Test* without -tags=trace)"
	@echo "  race    - go test -race (compile-check under race detector)"
	@echo "  all     - bench + trace"
	@echo "  clean   - remove generated result files"
	@echo "  help    - this message (default)"

all: bench trace

# stamp writes a metadata header so every results file is self-describing.
# Lines start with '#' — benchstat and the table generator skip them.
define stamp
	( echo "# date: $$(date -u +%Y-%m-%dT%H:%M:%SZ)"; \
	  echo "# host: $$(uname -srm)"; \
	  echo "# cpu: $$(sysctl -n machdep.cpu.brand_string 2>/dev/null || grep -m1 'model name' /proc/cpuinfo | cut -d: -f2 | sed 's/^ //')"; \
	  echo "# ncpu: $$(sysctl -n hw.ncpu 2>/dev/null || nproc)"; \
	  echo "# go: $$(go version)"; \
	  echo "# commit: $$(git rev-parse --short HEAD)$$(git diff --quiet 2>/dev/null || echo -dirty)" ) > $(1)
endef

bench:
	@mkdir -p results
	$(call stamp,$(OUT_BENCH))
	$(GOTEST) -bench=. -benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee -a $(OUT_BENCH)

trace: check-traces
	@mkdir -p results
	$(call stamp,$(OUT_TRACE))
	$(GOTEST) -tags=trace -bench=. -benchmem -count=$(TRACE_COUNT) -v -timeout=$(TIMEOUT) | tee -a $(OUT_TRACE)

# Core-scaling sweep: substantiates the "scales with cores" claim by
# running the parallel benchmarks at increasing GOMAXPROCS.
sweep:
	@mkdir -p results
	$(call stamp,$(OUT_SWEEP))
	@for p in $(SWEEP_PROCS); do \
		echo "=== GOMAXPROCS=$$p ===" | tee -a $(OUT_SWEEP); \
		GOMAXPROCS=$$p $(GOTEST) -bench='$(SWEEP_BENCH)' -benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee -a $(OUT_SWEEP); \
	done

# Compare two benchmark result files (needs golang.org/x/perf/cmd/benchstat).
OLD ?= $(OUT_BENCH)
NEW ?=
compare:
	@if [ -z "$(NEW)" ]; then echo "usage: make compare OLD=<file> NEW=<file>"; exit 1; fi
	benchstat $(OLD) $(NEW)

test:
	$(GOTEST) -count=1

race:
	$(GOTEST) -race -count=1

check-traces:
	@if [ ! -d $(DATADIR) ]; then \
		echo "ERROR: trace data missing at $(DATADIR)."; \
		echo "Run ./fetch-traces.sh to download it."; \
		exit 1; \
	fi

clean:
	rm -f $(OUT_BENCH) $(OUT_TRACE) $(OUT_SWEEP)
