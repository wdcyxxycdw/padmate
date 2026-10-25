package output_test

import (
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

var mediaKeyNames = []string{"VolumeUp", "VolumeDown", "VolumeMute", "MediaPlayPause", "MediaNext", "MediaPrevious", "MediaStop"}

// A media key is an ordinary key to the Arbiter (OUT-20): it goes down with its
// first reference and up with its last, whatever it is sent by, and can be
// tapped, held, repeated and combined with modifiers.
func TestArbiterMediaKeys(t *testing.T) {
	s := newSim(t)
	s.tap(ownerA, "VolumeUp", 0)
	s.ExpectOutput("+VolumeUp@0 -VolumeUp@0")
	s.tap(ownerA, "Ctrl+Shift+MediaNext", 0)
	s.ExpectOutput("+LCtrl@0 +LShift@0 +MediaNext@0 -MediaNext@0 -LShift@0 -LCtrl@0")

	s.down(ownerA, "MediaPlayPause")
	s.ExpectOutput("+MediaPlayPause@0")
	s.repeat(ownerA, "MediaPlayPause")
	s.ExpectOutput("+MediaPlayPause@0")
	// Two owners hold it: pressed once, released with the last.
	s.down(ownerB, "MediaPlayPause")
	s.ExpectOutput("")
	s.up(ownerA, "MediaPlayPause")
	s.ExpectOutput("")
	s.up(ownerB, "MediaPlayPause")
	s.ExpectOutput("-MediaPlayPause@0")

	// The key and the raw scan code of the same physical key are two keys.
	s.down(ownerA, "VolumeMute")
	s.down(ownerB, "sc:0xE020")
	s.ExpectOutput("+VolumeMute@0 +sc:0xE020@0")
	s.up(ownerA, "VolumeMute")
	s.up(ownerB, "sc:0xE020")
	s.ExpectOutput("-VolumeMute@0 -sc:0xE020@0")
}

// Every media key is sent by VK with its E0 scan code, and released with the
// same event, whatever happened in between.
func TestArbiterMediaKeyEvents(t *testing.T) {
	for _, name := range mediaKeyNames {
		s := newSim(t)
		s.tap(ownerA, name, 0)
		recs := s.Out.Records()
		if len(recs) != 2 {
			t.Fatalf("%s: %d events, want 2", name, len(recs))
		}
		for i, rec := range recs {
			e := rec.Event
			if !e.ByVK || !e.Ext || e.VK == 0 || e.Scan == 0 || e.Key.String() != name || e.Up != (i == 1) || e.Unicode || e.Mouse != 0 {
				t.Errorf("%s event %d = %+v: want the VK event with an E0 scan code", name, i, e)
			}
		}
		if recs[0].Event.VK != recs[1].Event.VK || recs[0].Event.Scan != recs[1].Event.Scan {
			t.Errorf("%s: the release %+v is not the press %+v", name, recs[1].Event, recs[0].Event)
		}
	}
}

// The minimum press holds a media key as any key's.
func TestArbiterMediaKeyMinPress(t *testing.T) {
	s := newSim(t, simtest.WithArbiterOptions(output.ArbiterOptions{MinPress: 20 * ms}))
	s.tap(ownerA, "VolumeDown", 0)
	s.ExpectOutput("+VolumeDown@0")
	s.Advance(20 * ms)
	s.ExpectOutput("-VolumeDown@20")
}

// The held-key log records a media key like Pause, by its VK, and the startup
// sweep releases one the last run left down when the system has it down.
func TestLoopHeldLogMediaKeys(t *testing.T) {
	path := heldPath(t)
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: func(output.KeyEvent) bool { return false }}, nil, nil)
	if res := lt.l.Swept(); res.Err != nil || res.Off || len(res.Released) > 0 {
		t.Fatalf("Swept() = %+v, want nothing done", res)
	}
	lt.do(down(ownerA, "VolumeMute"))
	wantHeld(t, path, "VolumeMute")
	lt.do(down(ownerB, "Ctrl+MediaStop"))
	wantHeld(t, path, "VolumeMute", "LCtrl", "MediaStop")
	lt.do(tap(ownerA, "MediaNext", 0))
	wantHeld(t, path, "VolumeMute", "LCtrl", "MediaStop")
	lt.do(up(ownerB, "Ctrl+MediaStop"))
	lt.do(up(ownerA, "VolumeMute"))
	wantHeld(t, path)
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
}

func TestLoopSweepMediaKeys(t *testing.T) {
	path := heldPath(t)
	writeHeld(t, path, mediaKeyNames...)
	// Only the volume keys are still down in the system; the sweep releases them in
	// the order of their bits, which is that of their VKs.
	down := downSet(t, "VolumeUp", "VolumeDown", "VolumeMute")
	lt := startLoopWith(t, output.LoopOptions{HeldFile: path, KeyDown: down.down}, nil, nil)
	res := lt.l.Swept()
	if res.Err != nil {
		t.Fatalf("Swept().Err = %v", res.Err)
	}
	if got, want := eventsTrace(res.Released), "-VolumeMute -VolumeDown -VolumeUp"; got != want {
		t.Errorf("Swept().Released = %q, want %q", got, want)
	}
	for _, e := range res.Released {
		if !e.ByVK || !e.Ext {
			t.Errorf("%+v is not the VK release with its E0 scan code", e)
		}
	}
	wantHeld(t, path)
	if err := lt.stop(); err != nil {
		t.Fatal(err)
	}
	wantNoHeld(t, path)
}
