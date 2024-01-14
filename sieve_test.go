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
	"fmt"
	"testing"

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
