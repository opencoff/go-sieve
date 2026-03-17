//go:build trace

package bench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dataDir() string {
	// data/ is at repo root, bench/ is one level down
	return filepath.Join("..", "data")
}

func TestLoadTwitterCSV(t *testing.T) {
	path := filepath.Join(dataDir(), "twitter", "cluster52.csv")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skipf("trace file not found: %s (run fetch-traces.sh)", path)
	}

	trace, err := LoadCSV(path, ParseTwitter)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("Twitter cluster52: %d requests, %d unique keys", len(trace.Requests), trace.Unique)
	for i := 0; i < min(5, len(trace.Requests)); i++ {
		t.Logf("  [%d] key=%s", i, trace.Requests[i].Key)
	}
}

func TestLoadMetaCDNCSV(t *testing.T) {
	dir := filepath.Join(dataDir(), "meta_cdn")
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		t.Skipf("no Meta CDN trace files in %s", dir)
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		trace, err := LoadCSV(path, ParseMetaCDN)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Meta CDN %s: %d requests, %d unique keys", e.Name(), len(trace.Requests), trace.Unique)
		for i := 0; i < min(5, len(trace.Requests)); i++ {
			t.Logf("  [%d] key=%s", i, trace.Requests[i].Key)
		}
		return
	}
	t.Skip("no CSV files in meta_cdn/")
}

// TestLoadOracleGeneral_All discovers every oracleGeneral file under data/
// and verifies the parser can load each one.
func TestLoadOracleGeneral_All(t *testing.T) {
	root := dataDir()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		t.Skipf("data directory not found: %s", root)
	}

	var files []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if isOracleGeneral(d.Name()) {
			files = append(files, path)
		}
		return nil
	})

	if len(files) == 0 {
		t.Skip("no oracleGeneral files found under data/")
	}

	for _, path := range files {
		rel, _ := filepath.Rel(root, path)
		t.Run(rel, func(t *testing.T) {
			trace, err := LoadOracleGeneral(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("%d requests, %d unique keys", len(trace.Requests), trace.Unique)
			for i := 0; i < min(5, len(trace.Requests)); i++ {
				t.Logf("  [%d] obj_id=%d", i, trace.Requests[i].Key)
			}
		})
	}
}

// isOracleGeneral returns true for files with .oracleGeneral or .oracleGeneral.bin extension.
func isOracleGeneral(name string) bool {
	return strings.HasSuffix(name, ".oracleGeneral") || strings.HasSuffix(name, ".oracleGeneral.bin")
}
