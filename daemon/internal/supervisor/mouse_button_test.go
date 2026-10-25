package supervisor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
)

// A mouse button (ADR-0062) held by a hold binding is let go of on the same
// exit paths of Run as a key: the context ending Run, the end of the events,
// the removal of the pad, the pause and a reload (TestHoldReleasedOnCancel and
// its relatives). The pause combo and the system's suspension have golden
// cases of their own (mouse-button-panic-combo, mouse-button-session-lock).
func TestMouseButtonReleasedByRun(t *testing.T) {
	const eastLeft = `"east": {"press": {"type": "keys", "keys": "MouseLeft", "mode": "hold"}}`
	const westShiftRight = `"west": {"press": {"type": "keys", "keys": "Shift+RButton", "mode": "hold"}}`
	prog, err := compileBase(eastLeft, westShiftRight)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		end  func(t *testing.T, h *harness)
		out  string
	}{
		{"canceled", func(t *testing.T, h *harness) {
			h.cancel()
			if err := h.wait(); err != nil {
				t.Errorf("Run = %v, want nil", err)
			}
			if err := h.sup.Sync(context.Background()); !errors.Is(err, supervisor.ErrStopped) {
				t.Errorf("Sync after Run = %v, want ErrStopped", err)
			}
		}, "+MouseLeft@0 +LShift@10 +MouseRight@10 -MouseRight@50 -MouseLeft@50 -LShift@50"},
		{"events closed", func(t *testing.T, h *harness) {
			close(h.events)
			if err := h.wait(); err != nil {
				t.Errorf("Run = %v, want nil", err)
			}
		}, "+MouseLeft@0 +LShift@10 +MouseRight@10 -MouseRight@50 -MouseLeft@50 -LShift@50"},
		{"removed", func(t *testing.T, h *harness) {
			h.send(removed(1))
			h.sync()
			checkAttrs(t, h.waitLog("slot freed", 1)[0], map[string]string{"slot": "0", "device": "03ab"})
		}, "+MouseLeft@0 +LShift@10 +MouseRight@10 -MouseRight@50 -MouseLeft@50 -LShift@50"},
		{"paused", func(t *testing.T, h *harness) {
			h.pause(true)
			h.checkPaused(true)
		}, "+MouseLeft@0 +LShift@10 +MouseRight@10 -MouseRight@50 -MouseLeft@50 -LShift@50"},
		{"reloaded", func(t *testing.T, h *harness) {
			h.reload(compile2(t), 2)
		}, "+MouseLeft@0 +LShift@10 +MouseRight@10 -MouseRight@50 -MouseLeft@50 -LShift@50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := start(t, opts{program: prog})
			h.send(added(padA))
			h.send(button(1, "east", true))
			h.set(10)
			h.send(button(1, "west", true))
			h.set(50)
			h.expect("+MouseLeft@0 +LShift@10 +MouseRight@10")
			tc.end(t, h)
			h.expect(tc.out)
		})
	}
}
