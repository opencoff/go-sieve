//go:build trace

package bench

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"os"
	"strings"

	"github.com/opencoff/go-mmap"
)

// Request represents a single cache access from a trace.
type Request[T any] struct {
	Key T
}

// Trace holds the full sequence of requests and the count of unique keys.
type Trace[T any] struct {
	Requests []Request[T]
	Unique   int
}

// LoadCSV reads a CSV trace file and returns a Trace.
// The parse function receives split fields for each line and returns
// the key value and true, or false to skip the line.
// Unique counting requires T to be comparable; the caller provides
// a key-extraction identity (the parse func itself serves this role
// since T is the key).
func LoadCSV(path string, parse func(fields []string) (string, bool)) (*Trace[string], error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("LoadCSV: open %s: %w", path, err)
	}
	defer f.Close()

	var requests []Request[string]
	seen := make(map[string]struct{})

	scanner := bufio.NewScanner(f)
	// Increase buffer for long lines
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Split(line, ",")
		key, ok := parse(fields)
		if !ok {
			continue
		}
		requests = append(requests, Request[string]{Key: key})
		seen[key] = struct{}{}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("LoadCSV: scan %s: %w", path, err)
	}

	return &Trace[string]{
		Requests: requests,
		Unique:   len(seen),
	}, nil
}

// ParseTwitter extracts the anonymized key (field index 1) from a Twitter trace line.
func ParseTwitter(fields []string) (string, bool) {
	if len(fields) < 2 {
		return "", false
	}
	return fields[1], true
}

// ParseMetaCDN extracts the cacheKey (field index 1) from a Meta CDN trace line.
func ParseMetaCDN(fields []string) (string, bool) {
	if len(fields) < 2 {
		return "", false
	}
	return fields[1], true
}

// LoadOracleGeneral reads an oracleGeneral binary trace file.
// Each record is 24 bytes:
//
//	Offset 0:  uint32  timestamp
//	Offset 4:  uint64  obj_id (the key)
//	Offset 12: uint32  obj_size
//	Offset 16: int64   next_access_vtime
//
// All little-endian. The file is read entirely into memory.
func LoadOracleGeneral(path string) (*Trace[uint64], error) {
	fd, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("LoadOracleGeneral: read %s: %w", path, err)
	}

	defer fd.Close()

	mm := mmap.New(fd)

	mapping, err := mm.Map(0, 0, mmap.PROT_READ, mmap.F_READAHEAD)
	if err != nil {
		return nil, fmt.Errorf("LoadOracleGeneral: mmap %s: %w", path, err)
	}

	defer mm.Unmap(mapping)

	data := mapping.Bytes()

	const recordSize = 24
	if len(data)%recordSize != 0 {
		return nil, fmt.Errorf("LoadOracleGeneral: %s size %d not a multiple of %d", path, len(data), recordSize)
	}

	nRecords := len(data) / recordSize
	requests := make([]Request[uint64], 0, nRecords)
	seen := make(map[uint64]struct{}, nRecords/4)

	for i := 0; i < len(data); i += recordSize {
		objID := binary.LittleEndian.Uint64(data[i+4 : i+12])
		requests = append(requests, Request[uint64]{Key: objID})
		seen[objID] = struct{}{}
	}

	return &Trace[uint64]{
		Requests: requests,
		Unique:   len(seen),
	}, nil
}
