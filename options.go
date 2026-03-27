// options.go - functional options for sieve cache construction
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

package sieve

// config holds internal configuration built from Options.
type config struct {
	k            int // visited counter saturation; 1 = classic SIEVE
	evictBufSize int // eviction channel buffer; 0 = disabled
}

// Option configures a Sieve cache at construction time.
type Option func(*config)

// WithVisitClamp creates a SIEVE-k cache where each entry can accumulate
// up to k visit counts before being considered "maximally visited".
// k=1 is equivalent to classic SIEVE (the default). k>1 uses multi-bit
// saturating counters: an item accessed k+1 times survives k eviction
// passes. Values less than 1 are clamped to 1.
func WithVisitClamp(k int) Option {
	return func(c *config) { c.k = k }
}

// WithOnEvict enables eviction notifications. When an entry is evicted,
// its key and value are sent on a buffered channel of size bufSize.
// Use Evictor() to obtain the receive end of the channel.
//
// The send is blocking but occurs outside the cache mutex, so the cache
// is not blocked while the consumer processes events. If the consumer
// cannot keep up, the sending goroutine (the caller of Add or Probe)
// blocks until space is available - this provides natural backpressure.
//
// The caller must call Close() when done with the cache to close the
// channel and prevent goroutine leaks in consumers.
func WithOnEvict(bufSize int) Option {
	return func(c *config) { c.evictBufSize = bufSize }
}
