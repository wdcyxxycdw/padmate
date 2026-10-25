//go:build linux

package inputtest

import (
	"runtime"
	"testing"
	"time"

	"github.com/ebitengine/purego"
)

// TestLockedThreadExit guards the purego version. purego v0.10.0's amd64
// trampoline stored its argument pointer above its own frame, into the
// frame of mstart0 on the thread's g0 stack. In a cgo build a thread that
// had made a purego call then crashed when it exited, which happens when a
// goroutine locked to it returns (SIGSEGV, no Go traceback): the whole test
// binary died, as cmd/padmated's did after inputtest's failed dlopen calls.
// Whether the dlopen succeeds does not matter.
func TestLockedThreadExit(t *testing.T) {
	for range 8 {
		done := make(chan struct{})
		go func() {
			defer close(done)
			// Never unlocked: the thread exits with the goroutine.
			runtime.LockOSThread()
			if h, err := purego.Dlopen("libc.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL); err == nil {
				_ = purego.Dlclose(h)
			}
		}()
		<-done
	}
	// The threads exit after their goroutines return; a crash there would
	// otherwise hit a later test.
	time.Sleep(100 * time.Millisecond)
}
