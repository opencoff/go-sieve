// sieve_bench_test.go -- benchmark testing
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
	"math/rand"
	"sync/atomic"
	"testing"

	"github.com/opencoff/go-sieve"
)

func BenchmarkSieve_Add(b *testing.B) {
	c := sieve.New[int, int](8192)
	ent := make([]int, b.N)

	for i := 0; i < b.N; i++ {
		var k int
		if i%2 == 0 {
			k = int(rand.Int63() % 16384)
		} else {
			k = int(rand.Int63() % 32768)
		}
		ent[i] = k
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := ent[i]
		c.Add(k, k)
	}
}

func BenchmarkSieve_Get(b *testing.B) {
	c := sieve.New[int, int](8192)
	ent := make([]int, b.N)
	for i := 0; i < b.N; i++ {
		var k int
		if i%2 == 0 {
			k = int(rand.Int63() % 16384)
		} else {
			k = int(rand.Int63() % 32768)
		}
		c.Add(k, k)
		ent[i] = k
	}

	b.ResetTimer()

	var hit, miss int64
	for i := 0; i < b.N; i++ {
		if _, ok := c.Get(ent[i]); ok {
			atomic.AddInt64(&hit, 1)
		} else {
			atomic.AddInt64(&miss, 1)
		}
	}

	b.Logf("%d: hit %d, miss %d, ratio %4.2f", b.N, hit, miss, float64(hit)/float64(hit+miss))
}
