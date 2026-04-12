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

OUT_BENCH := results/synthetic.txt
OUT_TRACE := results/trace.txt

.DEFAULT_GOAL := help
.PHONY: help all bench trace test race clean check-traces

help:
	@echo "bench/Makefile — comparison benchmarks and trace replay"
	@echo ""
	@echo "Targets:"
	@echo "  bench  - synthetic comparison benchmarks (SIEVE vs LRU vs ARC)"
	@echo "           -> $(OUT_BENCH)"
	@echo "  trace  - trace replay + miss-ratio + GC-pressure (needs $(DATADIR))"
	@echo "           -> $(OUT_TRACE)"
	@echo "  test   - go test (compile-check; no Test* without -tags=trace)"
	@echo "  race   - go test -race (compile-check under race detector)"
	@echo "  all    - bench + trace"
	@echo "  clean  - remove generated result files"
	@echo "  help   - this message (default)"

all: bench trace

bench:
	@mkdir -p results
	$(GOTEST) -bench=. -benchmem -count=$(COUNT) -timeout=$(TIMEOUT) | tee $(OUT_BENCH)

trace: check-traces
	@mkdir -p results
	$(GOTEST) -tags=trace -bench=. -benchmem -count=$(TRACE_COUNT) -v -timeout=$(TIMEOUT) | tee $(OUT_TRACE)

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
	rm -f $(OUT_BENCH) $(OUT_TRACE)
