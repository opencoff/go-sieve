module github.com/opencoff/go-sieve/bench

go 1.26.1

replace github.com/opencoff/go-sieve => ..

require (
	github.com/hashicorp/golang-lru/arc/v2 v2.0.7
	github.com/hashicorp/golang-lru/v2 v2.0.7
	github.com/opencoff/go-mmap v0.1.7
	github.com/opencoff/go-sieve v0.0.0-00010101000000-000000000000
)

require (
	github.com/puzpuzpuz/xsync/v3 v3.5.1 // indirect
	golang.org/x/sys v0.33.0 // indirect
)
