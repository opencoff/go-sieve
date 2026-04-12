# Root Makefile for go-sieve.
#
# Each target runs the root-module operation first, then cascades into
# bench/ via `$(MAKE) -C bench $@` so a single `make bench` (etc.) covers
# both the parent module and the comparison module.
#
# Targets:
#   test   - go test on parent module + bench module
#   race   - go test -race on parent module + bench module
#   bench  - internal SIEVE micro-benchmarks + SIEVE-vs-LRU-vs-ARC synth
#   trace  - trace replay benchmarks (requires bench/fetch-traces.sh data)
#   all    - everything above, in order
#   clean  - clean results
#
# NOTE: per project guidance we only test `.` and `./bench`; never
# `./...` — exp/ deadlocks under -race, and bench is a separate module
# that must be invoked through its own Makefile.

SHELL  := /bin/bash
GOTEST := go test
COUNT  := 3

.DEFAULT_GOAL := help
.PHONY: help all test race bench trace clean

help:
	@echo "go-sieve — root Makefile"
	@echo ""
	@echo "Each target runs the parent module operation then cascades into bench/."
	@echo ""
	@echo "Targets:"
	@echo "  test   - go test on parent module + bench compile-check"
	@echo "  race   - go test -race on parent module + bench race compile-check"
	@echo "  bench  - SIEVE internal micro-benches + SIEVE-vs-LRU-vs-ARC synth"
	@echo "  trace  - trace replay (delegates to bench/; needs bench/data/)"
	@echo "  all    - test + race + bench + trace"
	@echo "  clean  - remove generated result files"
	@echo "  help   - this message (default)"

all: test race bench trace

test:
	$(GOTEST) . -count=1
	$(MAKE) -C bench $@

race:
	$(GOTEST) -race . -count=1
	$(MAKE) -C bench $@

bench:
	$(GOTEST) -bench=. -benchmem -count=$(COUNT) .
	$(MAKE) -C bench $@

trace:
	$(MAKE) -C bench $@

clean:
	$(MAKE) -C bench $@
