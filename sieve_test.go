// sieve_test.go - test harness for sieve cache
//
// (c) 2024 Sudhi Herle <sudhi@herle.net>
//
// Copyright 2024- Sudhi Herle <sw-at-herle-dot-net>
// License: BSD-2-Clause
//
// If you need a commercial license for this work, please contact
// the author.
//
// This software does not come with any express or implied
// warranty; it is provided "as is". No claim  is made to its
// suitability for any purpose.

package sieve_test

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/opencoff/go-sieve"
)

func TestBasic(t *testing.T) {
	assert := newAsserter(t)

	s := sieve.New[int, string](4)
	ok := s.Add(1, "hello")
	assert(!ok, "empty cache: expected clean add of 1")

	ok = s.Add(2, "foo")
	assert(!ok, "empty cache: expected clean add of 2")
	ok = s.Add(3, "bar")
	assert(!ok, "empty cache: expected clean add of 3")
	ok = s.Add(4, "gah")
	assert(!ok, "empty cache: expected clean add of 4")

	ok = s.Add(1, "world")
	assert(ok, "key 1: expected to replace")

	ok = s.Add(5, "boo")
	assert(!ok, "adding 5: expected to be new add")

	_, ok = s.Get(2)
	assert(!ok, "evict: expected 2 to be evicted")

}

func TestEvictAll(t *testing.T) {
	assert := newAsserter(t)

	size := 128
	s := sieve.New[int, string](size)

	for i := 0; i < size*2; i++ {
		val := fmt.Sprintf("val %d", i)
		_, ok := s.Probe(i, val)
		assert(!ok, "%d: exp new add", i)
	}

	// the first half should've been all evicted
	for i := 0; i < size; i++ {
		_, ok := s.Get(i)
		assert(!ok, "%d: exp to be evicted", i)
	}

	// leaving the second half intact
	for i := size; i < size*2; i++ {
		ok := s.Delete(i)
		assert(ok, "%d: exp del on existing cache elem")
	}
}

func TestAllOps(t *testing.T) {
	size := 8192
	vals := randints(size * 3)

	s := sieve.New[uint64, uint64](size)

	for i := range vals {
		k := vals[i]
		s.Add(k, k)
	}

	var hit, miss int
	for i := range vals {
		k := vals[i]
		_, ok := s.Get(k)
		if ok {
			hit++
		} else {
			miss++
		}
	}

	t.Logf("%d items: hit %d, miss %d, ratio %4.2f\n", len(vals), hit, miss, float64(hit)/float64(hit+miss))
}

func TestSpeed(t *testing.T) {
	size := 8192
	vals := randints(size * 3)
	nvals := len(vals)

	s := sieve.New[uint64, uint64](size)

	for cpu := 1; cpu <= 32; cpu *= 2 {
		add := doFunc(cpu, func() {
			for _, v := range vals {
				s.Add(v, v)
			}
		})

		nsAdd := toNs(add, nvals, cpu)

		var hit, miss uint64
		get := doFunc(cpu, func() {
			for _, v := range vals {
				_, ok := s.Get(v)
				if ok {
					atomic.AddUint64(&hit, 1)
				} else {
					atomic.AddUint64(&miss, 1)
				}
			}
		})

		nsGet := toNs(get, nvals, cpu)
		getRatio := hitRatio(hit, miss)

		del := doFunc(cpu, func() {
			for _, v := range vals {
				s.Delete(v)
			}
		})
		nsDel := toNs(del, nvals, cpu)

		s.Purge()

		hit = 0
		miss = 0
		probe := doFunc(cpu, func() {
			for _, v := range vals {
				_, ok := s.Probe(v, v)
				if ok {
					atomic.AddUint64(&hit, 1)
				} else {
					atomic.AddUint64(&miss, 1)
				}
			}
		})
		nsProbe := toNs(probe, nvals, cpu)
		probeRatio := hitRatio(hit, miss)

		t.Logf(`nCPU: %d
	add   %4.2f ns/op
	get   %4.2f ns/op  %s
	del   %4.2f ns/op
	probe %4.2f ns/op  %s`,
			cpu,
			nsAdd,
			nsGet, getRatio,
			nsDel,
			nsProbe, probeRatio)
	}
}

func doFunc(ncpu int, fp func()) int64 {
	var wg sync.WaitGroup

	times := make([]time.Duration, ncpu)

	wg.Add(ncpu)
	for j := 0; j < ncpu; j++ {
		go func(idx int, wg *sync.WaitGroup) {
			st := time.Now()
			fp()
			end := time.Now()
			times[idx] = end.Sub(st)
			wg.Done()
		}(j, &wg)
	}

	wg.Wait()

	var tot int64
	for i := range times {
		tm := times[i]
		tot += int64(tm)
	}
	return tot
}

func toNs(tot int64, nvals, ncpu int) float64 {
	return (float64(tot) / float64(nvals)) / float64(ncpu)
}

func hitRatio(hit, miss uint64) string {
	r := float64(hit) / float64(hit+miss)
	return fmt.Sprintf("hit-ratio %4.2f (hit %d, miss %d)", r, hit, miss)
}

func randints(sz int) []uint64 {
	var b [8]byte

	v := make([]uint64, sz)

	for i := 0; i < sz; i++ {
		n, err := rand.Read(b[:])
		if n != 8 || err != nil {
			panic("can't generate rand")
		}

		v[i] = binary.BigEndian.Uint64(b[:]) % 16384
	}
	return v
}
