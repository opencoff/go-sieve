// sieve_test.go - test harness for sieve cache


package sieve

import (
	"fmt"
	"testing"
	"runtime"
)
 
func newAsserter(t *testing.T) func(cond bool, msg string, args ...interface{}) {
	return func(cond bool, msg string, args ...interface{}) {
		if cond {
			return
		}

		_, file, line, ok := runtime.Caller(1)
		if !ok {
			file = "???"
			line = 0
		}

		s := fmt.Sprintf(msg, args...)
		t.Fatalf("%s: %d: assertion failed: %s\n", file, line, s)
	}
}

func TestBasic(t *testing.T) {
	assert := newAsserter(t)

	s := NewSieveCache[int, string](4)
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
