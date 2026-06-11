// mktables - regenerate markdown benchmark tables from raw results files.
//
// Usage:
//
//	go run ./cmd/mktables -synth results/synthetic.txt -trace results/trace.txt
//
// Parses `go test -bench` output (synthetic: count=N, medians reported) and
// the -v trace run (miss-ratio test logs, replay/parallel benchmarks,
// GC-pressure logs), then emits the markdown tables used in bench-results.md,
// README.md, and bench/README.md. Derived claims (e.g. "k=1 beats LRU on
// N of M traces") are computed here — never count them by hand.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// sample is one benchmark line's metrics, keyed by unit ("ns/op", "B/op", ...).
type sample map[string]float64

// benchData accumulates samples per benchmark name (count>1 → multiple).
type benchData map[string][]sample

// -N GOMAXPROCS suffix is absent when GOMAXPROCS=1 (sweep runs).
var benchLine = regexp.MustCompile(`^Benchmark(\S+?)(?:-\d+)?\s+(\d+)\s+(.*)$`)

func parseBenchFile(path string, into benchData) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		m := benchLine.FindStringSubmatch(sc.Text())
		if m == nil {
			continue
		}
		name := m[1]
		s := sample{}
		fields := strings.Fields(m[3])
		for i := 0; i+1 < len(fields); i += 2 {
			v, err := strconv.ParseFloat(fields[i], 64)
			if err != nil {
				continue
			}
			s[fields[i+1]] = v
		}
		into[name] = append(into[name], s)
	}
	return sc.Err()
}

// median of metric u across all samples of benchmark name; NaN-free: returns
// (0, false) when absent.
func (b benchData) median(name, u string) (float64, bool) {
	var vals []float64
	for _, s := range b[name] {
		if v, ok := s[u]; ok {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return 0, false
	}
	sort.Float64s(vals)
	return vals[len(vals)/2], true
}

func fmtNs(v float64, ok bool) string {
	if !ok {
		return "—"
	}
	switch {
	case v >= 1e6:
		return fmt.Sprintf("%.2f ms", v/1e6)
	case v >= 10_000:
		return fmt.Sprintf("%.1f µs", v/1000)
	case v >= 100:
		return fmt.Sprintf("%.1f", v)
	default:
		return fmt.Sprintf("%.2f", v)
	}
}

// --- trace -v log parsing -------------------------------------------------

var (
	runLine   = regexp.MustCompile(`^=== (?:RUN|CONT)\s+(\S+)`)
	missLine  = regexp.MustCompile(`^\s+replay_test\.go:\d+:\s+(sieve-k1|sieve-k2|sieve-k3|LRU|ARC)\s+miss ratio: ([0-9.]+)`)
	gcLine    = regexp.MustCompile(`^\s+replay_test\.go:\d+:\s+(sieve-k1|sieve-k3|LRU|ARC)\s+(\d+)\s+(\d+) us\s+(\d+) KB\s+(\d+)$`)
	gcTrcLine = regexp.MustCompile(`^\s+replay_test\.go:\d+:\s+Using trace: (\S+)`)
)

type traceLogs struct {
	missRatio map[string]map[string]float64 // trace -> variant -> ratio
	gcTrace   string
	gcRows    [][]string // variant, numGC, pause us, alloc KB, heap objs
	traces    []string   // insertion order
}

func parseTraceLogs(path string) (*traceLogs, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	tl := &traceLogs{missRatio: map[string]map[string]float64{}}
	cur := ""
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := runLine.FindStringSubmatch(line); m != nil {
			cur = m[1]
			continue
		}
		if m := missLine.FindStringSubmatch(line); m != nil {
			name := strings.TrimPrefix(cur, "TestMissRatio/")
			if name == cur || name == "" {
				continue // miss-ratio log outside a TestMissRatio subtest
			}
			if tl.missRatio[name] == nil {
				tl.missRatio[name] = map[string]float64{}
				tl.traces = append(tl.traces, name)
			}
			v, _ := strconv.ParseFloat(m[2], 64)
			tl.missRatio[name][m[1]] = v
			continue
		}
		if m := gcTrcLine.FindStringSubmatch(line); m != nil {
			tl.gcTrace = m[1]
			continue
		}
		if m := gcLine.FindStringSubmatch(line); m != nil {
			tl.gcRows = append(tl.gcRows, m[1:])
		}
	}
	return tl, sc.Err()
}

// --- table emitters --------------------------------------------------------

var missVariants = []string{"sieve-k1", "sieve-k2", "sieve-k3", "LRU", "ARC"}

func emitMissRatio(tl *traceLogs) {
	fmt.Println("### Miss ratio (every trace)")
	fmt.Println()
	fmt.Println("Cache sized at 10% of unique keys. **Bold** = best in row.")
	fmt.Println()
	fmt.Println("| Trace | SIEVE k=1 | SIEVE k=2 | SIEVE k=3 | LRU | ARC |")
	fmt.Println("|-------|----------:|----------:|----------:|----:|----:|")

	bestCount := map[string]int{}
	k1Wins, lruWins, ties := 0, 0, 0
	traces := append([]string(nil), tl.traces...)
	sort.Strings(traces)
	for _, tr := range traces {
		row := tl.missRatio[tr]
		best := ""
		for _, v := range missVariants {
			if best == "" || row[v] < row[best] {
				best = v
			}
		}
		bestCount[best]++
		switch {
		case row["sieve-k1"] < row["LRU"]:
			k1Wins++
		case row["LRU"] < row["sieve-k1"]:
			lruWins++
		default:
			ties++
		}
		fmt.Printf("| %s |", tr)
		for _, v := range missVariants {
			cell := fmt.Sprintf("%.4f", row[v])
			if v == best {
				cell = "**" + cell + "**"
			}
			fmt.Printf(" %s |", cell)
		}
		fmt.Println()
	}
	fmt.Println()
	fmt.Printf("Derived (computed by mktables, n=%d traces):\n", len(traces))
	fmt.Printf("- Best-in-row counts: ")
	var parts []string
	for _, v := range missVariants {
		if bestCount[v] > 0 {
			parts = append(parts, fmt.Sprintf("%s: %d", v, bestCount[v]))
		}
	}
	fmt.Println(strings.Join(parts, ", "))
	fmt.Printf("- Head-to-head SIEVE k=1 vs LRU: k=1 lower on %d, LRU lower on %d, tied on %d\n",
		k1Wins, lruWins, ties)
	fmt.Println()
}

var traceVariants = []string{"SieveK1", "SieveK3", "LRU", "ARC"}

func emitTraceBench(bd benchData, prefix, title, note string) {
	// Collect trace names present under this prefix.
	set := map[string]bool{}
	for name := range bd {
		if !strings.HasPrefix(name, prefix+"/") {
			continue
		}
		rest := strings.TrimPrefix(name, prefix+"/")
		i := strings.LastIndex(rest, "/")
		if i < 0 {
			continue
		}
		set[rest[:i]] = true
	}
	if len(set) == 0 {
		return
	}
	var traces []string
	for t := range set {
		traces = append(traces, t)
	}
	// Sort by SieveK1 ns/op ascending, like the README tables.
	sort.Slice(traces, func(a, b int) bool {
		va, _ := bd.median(prefix+"/"+traces[a]+"/SieveK1", "ns/op")
		vb, _ := bd.median(prefix+"/"+traces[b]+"/SieveK1", "ns/op")
		return va < vb
	})

	fmt.Printf("### %s\n\n", title)
	if note != "" {
		fmt.Println(note)
		fmt.Println()
	}
	fmt.Println("| Trace | SIEVE k=1 | SIEVE k=3 | LRU | ARC |")
	fmt.Println("|-------|---------:|---------:|----:|----:|")
	for _, tr := range traces {
		fmt.Printf("| %s |", tr)
		for i, v := range traceVariants {
			val, ok := bd.median(prefix+"/"+tr+"/"+v, "ns/op")
			cell := fmtNs(val, ok)
			if i == 0 {
				cell = "**" + cell + "**"
			}
			fmt.Printf(" %s |", cell)
		}
		fmt.Println()
	}
	fmt.Println()
}

func emitGCPressure(tl *traceLogs) {
	if len(tl.gcRows) == 0 {
		return
	}
	fmt.Printf("### GC pressure during replay (trace: %s)\n\n", tl.gcTrace)
	fmt.Println("| Variant | NumGC | PauseTotal (µs) | TotalAlloc (KB) | HeapObjects |")
	fmt.Println("|---------|------:|----------------:|----------------:|------------:|")
	for _, r := range tl.gcRows {
		fmt.Printf("| %s | %s | %s | %s | %s |\n", r[0], r[1], r[2], r[3], r[4])
	}
	fmt.Println()
}

type synthRow struct {
	label string // display label
	base  string // benchmark name up to the variant segment
}

func emitSynth(bd benchData) {
	rows := []synthRow{
		{"`Get_Parallel`", "Get_Parallel"},
		{"`Add_Parallel`", "Add_Parallel"},
		{"`Probe_Parallel`", "Probe_Parallel"},
		{"`Delete_Parallel`", "Delete_Parallel"},
		{"`Mixed_Parallel` (60/30/10)", "Mixed_Parallel"},
		{"`Zipf_Get_Parallel` (s=1.01)", "Zipf_Get_Parallel/s=1.01"},
		{"`Zipf_Get_Parallel` (s=1.20)", "Zipf_Get_Parallel/s=1.20"},
		{"`Zipf_Get_Parallel` (s=1.50)", "Zipf_Get_Parallel/s=1.50"},
	}
	fmt.Println("### Synthetic parallel micro-benchmarks (medians)")
	fmt.Println()
	fmt.Println("| Benchmark | Sieve | LRU | ARC |")
	fmt.Println("|-----------|-------|-----|-----|")
	for _, r := range rows {
		fmt.Printf("| %s |", r.label)
		for _, v := range []string{"Sieve", "LRU", "ARC"} {
			ns, ok := bd.median(r.base+"/"+v, "ns/op")
			if !ok {
				fmt.Printf(" — |")
				continue
			}
			cell := fmtNs(ns, ok) + " ns/op"
			if b, ok2 := bd.median(r.base+"/"+v, "B/op"); ok2 {
				cell += fmt.Sprintf(", %.0f B", b)
			}
			fmt.Printf(" %s |", cell)
		}
		fmt.Println()
	}
	fmt.Println()

	// Memory footprint + GC impact, if present.
	fmt.Println("### Memory footprint at fill (median heap-bytes)")
	fmt.Println()
	fmt.Println("| Entries | Sieve | LRU | ARC |")
	fmt.Println("|---------|------:|----:|----:|")
	for _, size := range []string{"100K", "500K", "1M"} {
		fmt.Printf("| %s |", size)
		for _, v := range []string{"Sieve", "LRU", "ARC"} {
			hb, ok := bd.median("MemoryFootprint/"+size+"/"+v, "heap-bytes")
			if !ok {
				fmt.Printf(" — |")
				continue
			}
			fmt.Printf(" %.1f MB |", hb/(1<<20))
		}
		fmt.Println()
	}
	fmt.Println()

	fmt.Println("### GC impact at 1M entries (median)")
	fmt.Println()
	fmt.Println("| Variant | wall/iter | avg GC pause |")
	fmt.Println("|---------|----------:|-------------:|")
	for _, v := range []string{"Sieve", "LRU", "ARC"} {
		ns, ok1 := bd.median("GCImpact/"+v, "ns/op")
		p, ok2 := bd.median("GCImpact/"+v, "avg-gc-pause-ns")
		if !ok1 && !ok2 {
			continue
		}
		fmt.Printf("| %s | %s | %s |\n", v, fmtNs(ns, ok1), fmtNs(p, ok2))
	}
	fmt.Println()
}

func emitHeadlines(bd benchData) {
	fmt.Println("### Headlines (computed)")
	fmt.Println()

	// Per-trace speedup: min(LRU, ARC) / SieveK1 for each trace under a
	// prefix; report the min..max of those per-row ratios. Cross-trace
	// lo/hi ratios would overstate the spread.
	type span struct{ lo, hi float64 }
	rowRatios := func(prefix string) (sieve, ratio span, n int) {
		sieve.lo, ratio.lo = -1, -1
		for name := range bd {
			if !strings.HasPrefix(name, prefix+"/") || !strings.HasSuffix(name, "/SieveK1") {
				continue
			}
			tr := strings.TrimSuffix(strings.TrimPrefix(name, prefix+"/"), "/SieveK1")
			s, ok := bd.median(name, "ns/op")
			if !ok || s == 0 {
				continue
			}
			l, okL := bd.median(prefix+"/"+tr+"/LRU", "ns/op")
			a, okA := bd.median(prefix+"/"+tr+"/ARC", "ns/op")
			if !okL && !okA {
				continue
			}
			best := l
			if !okL || (okA && a < l) {
				best = a
			}
			r := best / s
			if sieve.lo < 0 || s < sieve.lo {
				sieve.lo = s
			}
			if s > sieve.hi {
				sieve.hi = s
			}
			if ratio.lo < 0 || r < ratio.lo {
				ratio.lo = r
			}
			if r > ratio.hi {
				ratio.hi = r
			}
			n++
		}
		return sieve, ratio, n
	}
	if s, r, n := rowRatios("ParallelGet"); n > 0 {
		fmt.Printf("- Warm parallel Get (SIEVE k=1): %.2f–%.2f ns/op across %d traces; %.0fx–%.0fx faster than the better of LRU/ARC per trace\n",
			s.lo, s.hi, n, r.lo, r.hi)
	}
	if s, r, n := rowRatios("ParallelReplay"); n > 0 {
		fmt.Printf("- Cold parallel replay (SIEVE k=1): %.1f–%.1f ns/op across %d traces; %.1fx–%.1fx faster than the better of LRU/ARC per trace\n",
			s.lo, s.hi, n, r.lo, r.hi)
	}
	fmt.Println()
}

// --- sweep parsing ----------------------------------------------------------

var sweepHdr = regexp.MustCompile(`^=== GOMAXPROCS=(\d+) ===$`)

// emitSweep parses a sweep file (sections per GOMAXPROCS) and emits a
// scaling table for the given benchmark bases.
func emitSweep(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	perProc := map[int]benchData{}
	var procs []int
	cur := -1
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if m := sweepHdr.FindStringSubmatch(line); m != nil {
			cur, _ = strconv.Atoi(m[1])
			if perProc[cur] == nil {
				perProc[cur] = benchData{}
				procs = append(procs, cur)
			}
			continue
		}
		if cur < 0 {
			continue
		}
		if m := benchLine.FindStringSubmatch(line); m != nil {
			s := sample{}
			fields := strings.Fields(m[3])
			for i := 0; i+1 < len(fields); i += 2 {
				if v, err := strconv.ParseFloat(fields[i], 64); err == nil {
					s[fields[i+1]] = v
				}
			}
			perProc[cur][m[1]] = append(perProc[cur][m[1]], s)
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	sort.Ints(procs)

	for _, base := range []string{"Get_Parallel", "Mixed_Parallel", "Zipf_Get_Parallel/s=1.01"} {
		fmt.Printf("### Core scaling: %s (median ns/op)\n\n", base)
		fmt.Println("| GOMAXPROCS | Sieve | LRU | ARC |")
		fmt.Println("|-----------:|------:|----:|----:|")
		for _, p := range procs {
			fmt.Printf("| %d |", p)
			for _, v := range []string{"Sieve", "LRU", "ARC"} {
				val, ok := perProc[p].median(base+"/"+v, "ns/op")
				fmt.Printf(" %s |", fmtNs(val, ok))
			}
			fmt.Println()
		}
		fmt.Println()
	}
	return nil
}

func main() {
	synth := flag.String("synth", "", "synthetic results file (results/synthetic.txt)")
	trace := flag.String("trace", "", "trace results file (results/trace.txt)")
	sweep := flag.String("sweep", "", "core-scaling sweep file (results/sweep.txt)")
	flag.Parse()

	bd := benchData{}
	var tl *traceLogs

	if *synth != "" {
		if err := parseBenchFile(*synth, bd); err != nil {
			fmt.Fprintf(os.Stderr, "mktables: %v\n", err)
			os.Exit(1)
		}
	}
	if *trace != "" {
		if err := parseBenchFile(*trace, bd); err != nil {
			fmt.Fprintf(os.Stderr, "mktables: %v\n", err)
			os.Exit(1)
		}
		var err error
		if tl, err = parseTraceLogs(*trace); err != nil {
			fmt.Fprintf(os.Stderr, "mktables: %v\n", err)
			os.Exit(1)
		}
	}

	if tl != nil {
		emitTraceBench(bd, "ParallelGet",
			"Parallel Get throughput (warm cache, ns/op)",
			"Cache pre-warmed with a full replay; goroutines start at staggered trace offsets.")
		emitTraceBench(bd, "ParallelReplay",
			"Parallel Replay throughput (cold cache, mixed read+write, ns/op)",
			"Probe() for SIEVE, Get+Add for LRU/ARC; no warmup; staggered trace offsets.")
		emitTraceBench(bd, "Replay",
			"Sequential replay (full trace per iteration, wall time)", "")
		emitMissRatio(tl)
		emitGCPressure(tl)
	}
	if *synth != "" {
		emitSynth(bd)
	}
	if *sweep != "" {
		if err := emitSweep(*sweep); err != nil {
			fmt.Fprintf(os.Stderr, "mktables: %v\n", err)
			os.Exit(1)
		}
	}
	if tl != nil {
		emitHeadlines(bd)
	}
}
