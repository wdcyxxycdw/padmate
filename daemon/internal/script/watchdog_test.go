package script

import (
	"errors"
	"runtime"
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

func TestWatchdogInterruptsAndClears(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	vm := goja.New()
	w, err := NewWatchdog(vm, WatchdogOptions{Poll: time.Millisecond, Budget: 5 * time.Millisecond, Wall: 200 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Begin(WatchdogCallHandler); err != nil {
		t.Fatal(err)
	}
	_, callErr := vm.RunString(`for (;;) {}`)
	firing := w.End()
	if firing == nil || firing.Reason == nil {
		t.Fatalf("watchdog firing = %+v, want budget firing (call err %v)", firing, callErr)
	}
	var interrupted *goja.InterruptedError
	if !errors.As(callErr, &interrupted) {
		t.Fatalf("call error = %v, want InterruptedError", callErr)
	}
	if _, err := vm.RunString("1 + 1"); err != nil {
		t.Fatalf("runtime not reusable after End: %v", err)
	}
	if err := w.Stop(); err != nil {
		t.Fatal(err)
	}
	if err := w.Stop(); err != nil {
		t.Fatalf("second Stop = %v, want nil", err)
	}
}

func TestWatchdogDefaults(t *testing.T) {
	for _, tc := range []struct {
		name string
		got  WatchdogOptions
		want WatchdogOptions
	}{
		{"handler", HandlerWatchdog, WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 100 * time.Millisecond, Wall: 2 * time.Second}},
		{"setup", SetupWatchdog, WatchdogOptions{Poll: 2 * time.Millisecond, Budget: time.Second, Wall: 2 * time.Second}},
		{"teardown", TeardownWatchdog, WatchdogOptions{Poll: 2 * time.Millisecond, Budget: 200 * time.Millisecond, Wall: 2 * time.Second}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Fatalf("options = %+v, want %+v", tc.got, tc.want)
			}
		})
	}
}

func TestRestartTrackerBackoffAndPermanentStop(t *testing.T) {
	var r RestartTracker
	var base clock.Time
	for _, want := range []time.Duration{time.Second, 10 * time.Second, 60 * time.Second} {
		got := r.Record(base)
		if got.Delay != want {
			t.Errorf("delay = %s, want %s", got.Delay, want)
		}
		base = base.Add(time.Second)
	}
	if !r.Permanent() {
		t.Fatal("tracker is not permanently stopped after three interruptions")
	}
	if got := r.Record(base); !got.Permanent || got.Delay != 0 {
		t.Fatalf("record after permanent = %+v, want permanent without delay", got)
	}

	var fresh RestartTracker
	if got := fresh.Record(0); got.Permanent {
		t.Fatal("first interruption permanently stopped tracker")
	}
	if got := fresh.Record(clock.Time(11 * time.Minute)); got.Count != 1 || got.Permanent {
		t.Fatalf("old interruption was not pruned: %+v", got)
	}
}
