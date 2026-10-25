package output_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestLoopHeldLogInUse: a Loop whose held-key log another process, or
// another Loop, has open keeps no log: it sweeps nothing, sends its keys
// as ever, and neither changes nor removes the other's log. A log that is
// open only for a moment is opened on a retry.
func TestLoopHeldLogInUse(t *testing.T) {
	t.Run("another Loop", func(t *testing.T) {
		output.SetHeldOpenWait(t, 100*ms)
		path := heldPath(t)
		a := startLoop(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }})
		a.l.Swept()
		a.do(down(ownerA, "Space"))
		wantHeld(t, path, "Space")

		b := startLoop(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return true }})
		res := b.l.Swept()
		if !res.Off || !errors.Is(res.Err, output.ErrHeldInUse) || len(res.Released) > 0 {
			t.Errorf("Swept() = %+v, want Off with ErrHeldInUse", res)
		}
		b.do(down(ownerA, "Z"))
		wantHeld(t, path, "Space")
		b.do(up(ownerA, "Z"))
		if got := b.rec.Trace(); got != "+Z@0 -Z@0" {
			t.Errorf("output of the second Loop: %s, want +Z@0 -Z@0", got)
		}
		if err := b.stop(); err != nil {
			t.Fatal(err)
		}
		wantHeld(t, path, "Space")

		a.do(up(ownerA, "Space"))
		if err := a.stop(); err != nil {
			t.Fatal(err)
		}
		wantNoHeld(t, path)
	})
	t.Run("retry", func(t *testing.T) {
		path := heldPath(t)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		name, err := windows.UTF16PtrFromString(path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil,
			windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err != nil {
			t.Fatal(err)
		}
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			time.Sleep(50 * ms)
			_ = windows.CloseHandle(h)
		}()
		lt := startLoop(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }})
		if res := lt.l.Swept(); res.Off || res.Err != nil {
			t.Errorf("Swept() = %+v, want the log opened on a retry", res)
		}
		<-closed
		lt.do(down(ownerA, "Space"))
		wantHeld(t, path, "Space")
		lt.do(up(ownerA, "Space"))
	})
}
