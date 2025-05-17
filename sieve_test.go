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
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
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

	vals = shuffle(vals)

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

type timing struct {
	typ       string
	d         time.Duration
	hit, miss uint64
}

type barrier atomic.Uint64

func (b *barrier) Wait() {
	v := (*atomic.Uint64)(b)
	for {
		if v.Load() == 1 {
			return
		}
		runtime.Gosched()
	}
}

func (b *barrier) Signal() {
	v := (*atomic.Uint64)(b)
	v.Store(1)
}

func TestSpeed(t *testing.T) {
	size := 32768
	vals := randints(size * 3)
	//valr := shuffle(vals)

	// we will start 4 types of workers: add, get, del, probe
	// each worker will be working on a shuffled version of
	// the uint64 array.

	for ncpu := 2; ncpu <= 32; ncpu *= 2 {
		var wg sync.WaitGroup

		wg.Add(ncpu)
		s := sieve.New[uint64, uint64](size)

		var bar barrier

		// number of workers of each type
		m := ncpu / 2
		ch := make(chan timing, m)
		for i := 0; i < m; i++ {

			go func(ch chan timing, wg *sync.WaitGroup) {
				var hit, miss uint64

				bar.Wait()
				st := time.Now()

				// shuffled array
				for _, x := range vals {
					v := x % 16384
					if _, ok := s.Get(v); ok {
						hit++
					} else {
						miss++
					}
				}
				d := time.Now().Sub(st)
				ch <- timing{
					typ:  "get",
					d:    d,
					hit:  hit,
					miss: miss,
				}
				wg.Done()
			}(ch, &wg)

			go func(ch chan timing, wg *sync.WaitGroup) {
				var hit, miss uint64
				bar.Wait()
				st := time.Now()
				for _, x := range vals {
					v := x % 16384
					if _, ok := s.Probe(v, v); ok {
						hit++
					} else {
						miss++
					}
				}
				d := time.Now().Sub(st)
				ch <- timing{
					typ:  "probe",
					d:    d,
					hit:  hit,
					miss: miss,
				}
				wg.Done()
			}(ch, &wg)
		}

		bar.Signal()

		// wait for goroutines to end and close the chan
		go func() {
			wg.Wait()
			close(ch)
		}()

		// now harvest timing
		times := map[string]timing{}
		for tm := range ch {
			if v, ok := times[tm.typ]; ok {
				z := (int64(v.d) + int64(tm.d)) / 2
				v.d = time.Duration(z)
				v.hit = (v.hit + tm.hit) / 2
				v.miss = (v.miss + tm.miss) / 2
				times[tm.typ] = v
			} else {
				times[tm.typ] = tm
			}
		}

		var out strings.Builder
		fmt.Fprintf(&out, "Tot CPU %d, workers/type %d %d elems\n", ncpu, m, len(vals))
		for _, v := range times {
			var ratio string
			ns := toNs(int64(v.d), len(vals), m)
			ratio = hitRatio(v.hit, v.miss)
			fmt.Fprintf(&out, "%6s %4.2f ns/op%s\n", v.typ, ns, ratio)
		}
		t.Logf("%s", out.String())
	}
}

func dup[T ~[]E, E any](v T) []E {
	n := len(v)
	g := make([]E, n)
	copy(g, v)
	return g
}

func shuffle[T ~[]E, E any](v T) []E {
	i := len(v)
	for i--; i >= 0; i-- {
		j := rand.Intn(i + 1)
		v[i], v[j] = v[j], v[i]
	}
	return v
}

func toNs(tot int64, nvals, ncpu int) float64 {
	return (float64(tot) / float64(nvals)) / float64(ncpu)
}

func hitRatio(hit, miss uint64) string {
	r := float64(hit) / float64(hit+miss)
	return fmt.Sprintf("  hit-ratio %4.2f (hit %d, miss %d)", r, hit, miss)
}

func randints(sz int) []uint64 {
	var b [8]byte

	v := make([]uint64, sz)

	for i := 0; i < sz; i++ {
		n, err := rand.Read(b[:])
		if n != 8 || err != nil {
			panic("can't generate rand")
		}

		v[i] = binary.BigEndian.Uint64(b[:])
	}
	return v
}
