// pause.go - relax the cpu or schedule

package sieve

import (
	"runtime"
	_ "unsafe" // for go:linkname
)

const (
	// Spin tuning — mirrors Go runtime's sync.Mutex active spin constants.
	// 4 rounds of 30 PAUSE instructions ≈ 400-500ns on modern x86.
	_SpinPAUSE   = 4  // procyield iterations before falling back to Gosched
	_PauseCycles = 30 // PAUSE instructions per procyield call
)

// procyield emits N PAUSE instructions (x86) or YIELD (arm64).
// Used by Go's own sync.Mutex; unlikely to disappear.
//
//go:linkname procyield runtime.procyield
func procyield(cycles uint32)

// pause - relax the cpu if we haven't paused enough else yield the cpu
func pause(n int) {
	if n < _SpinPAUSE {
		procyield(_PauseCycles)
	} else {
		runtime.Gosched()
	}
}
