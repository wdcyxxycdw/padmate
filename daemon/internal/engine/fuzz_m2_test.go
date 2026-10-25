package engine_test

import (
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// fuzzProfile has every kind of action the rules allow (ARCHITECTURE
// §7.7) on the eight controls of fuzzControls: tap-mode taps and
// multi-taps, with and without a hold, press in hold and tap mode, press
// and hold holding the same chord, a press action next to a timeOnly tap
// and hold (so its release, a tap too, has an edge decision), typematic
// repeat on press and on hold, release, a tap-mode sequence, a sequence action
// and a text action (they hold nothing, MAP-22, OUT-21), a latch, the
// three interrupt policies, and layer hold and toggle of the overlay fn,
// which rebinds some of the controls. The sets of the function keys
// (MAP-27) rebind more of them, with every kind of action they may hold.
const fuzzProfile = `{"schemaVersion": 0, "name": "fuzz", "layers": {
  "base": {
    "west": {"tap": {"type": "keys", "keys": "F13"}, "doubleTap": {"type": "keys", "keys": "F14"},
      "tripleTap": {"type": "keys", "keys": "F15"}, "quadTap": {"type": "keys", "keys": "F16"},
      "hold": {"type": "keys", "keys": "E"}},
    "north": {"press": {"type": "keys", "keys": "Shift"}, "tap": {"type": "keys", "keys": "B"},
      "hold": {"type": "keys", "keys": "Shift"}},
    "east": {"press": {"type": "keys", "keys": "A", "repeat": {"delayMs": 30, "rateHz": 50}}},
    "south": {"press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}, "doubleTap": {"type": "keys", "keys": "F24"},
      "release": {"type": "keys", "keys": "F17"}},
    "leftshoulder": {"press": {"type": "keys", "keys": ["Ctrl+K", "S"], "mode": "tap"}},
    "rightshoulder": {"latch": true, "press": {"type": "keys", "keys": "Ctrl"}, "release": {"type": "text", "text": "x"}},
    "dpad.up": {"policy": "timeOnly", "press": {"type": "keys", "keys": "F12"}, "tap": {"type": "keys", "keys": "F18"},
      "hold": {"type": "keys", "keys": "F19", "repeat": {"delayMs": 5, "rateHz": 100}}},
    "dpad.down": {"press": {"type": "sequence", "steps": [{"keys": "Ctrl+Y"}, {"wait": 5}, {"text": "ok"}]},
      "tap": {"type": "layer", "layer": "fn", "op": "toggle"}, "hold": {"type": "layer", "layer": "fn", "op": "hold"}}
  },
  "fn": {
    "west": {"press": {"type": "keys", "keys": "F20"}, "release": {"type": "keys", "keys": "F21"}},
    "south": {"doubleTap": {"type": "keys", "keys": "F22"}, "hold": {"type": "keys", "keys": "F23", "mode": "tap"}},
    "east": {"press": {"type": "keys", "keys": "A", "repeat": {"style": "tap", "delayMs": 0, "rateHz": 1000}},
      "doubleTap": {"type": "text", "text": "dt"}}
  }
}, "functionKeys": {
  "back": {
    "west": {"press": {"type": "keys", "keys": "F9"}, "release": {"type": "keys", "keys": "F10"}},
    "east": {"press": {"type": "keys", "keys": "A", "repeat": {"delayMs": 20, "rateHz": 60}}},
    "north": {"tap": {"type": "keys", "keys": "F11"}, "hold": {"type": "keys", "keys": "F12"},
      "tripleTap": {"type": "sequence", "steps": [{"text": "t"}, {"wait": 1}]}},
    "dpad.down": {"press": {"type": "layer", "layer": "fn", "op": "hold"}},
    "rightshoulder": {"block": true}
  },
  "start": {
    "south": {"press": {"type": "keys", "keys": ["Ctrl+K", "S"], "mode": "tap"}, "doubleTap": {"type": "keys", "keys": "F13"}},
    "dpad.up": {"policy": "timeOnly", "tap": {"type": "keys", "keys": "F18"}, "hold": {"type": "keys", "keys": "F19"}},
    "leftshoulder": {"press": {"type": "layer", "layer": "fn", "op": "toggle"}},
    "west": {"press": {"type": "keys", "keys": "Shift"}}
  }
}}`

// fuzzControls are the controls FuzzGestures presses, by operation code.
var fuzzControls = [8]control.Control{
	control.West, control.North, control.East, control.South,
	control.LeftShoulder, control.RightShoulder, control.DpadUp, control.DpadDown,
}

// fuzzLatched is the chord of the latch binding, the only one that stays
// held after every control is released.
var fuzzLatched = keys.MustParse("Ctrl")

// FuzzGestures drives an Engine over fuzzProfile with random edges, waits,
// pauses, cancels, resets and reloads, and checks with a ledger that it
// never leaves a key held (ENG-12, invariant 1). Each byte is one
// operation: its low 4 bits select it.
//
//	0–7   press the control fuzzControls[op] if it is up, else release it;
//	      with the high bit set, Options.Button consumes the edge, as a
//	      script does (SCR-05): a consumed press leaves the control up in
//	      the engine, so its release is stale, and a consumed release ends
//	      the cycle without its tap or release action; with bit 6 set
//	      (0x40) it is a function key instead: back for an even op, start
//	      for an odd one, pressed if up, else released (MAP-27)
//	8     wait max(1, next byte) ms
//	9     wait until the next deadline
//	10    wait until 1 ms before the next deadline
//	11    wait (byte>>4)+1 ms
//	12    Pause, or Resume when paused
//	13    CancelAll
//	14    Reset (all controls are up again)
//	15    Reload, switching between two programs
//
// Waits run every deadline on the way at its time, as slot.Replay does.
// The ledger fails at an Up without a Down, a Repeat of a chord that is not
// held, a Repeat of a cycle whose control is physically up (OUT-11: the
// control of a cycle comes from the decisions), a Down, Tap or Repeat while
// paused, and a negative tap hold. After CancelAll, Pause, Reset and Reload
// it forgets what is held, as the slot's ReleaseOwners does. It also fails
// when the decisions about the edges themselves, the press decision of
// each press and the up decision of each release, do not come in the order
// the edges were stepped (ARCHITECTURE §7.7, MAP-09): a release that goes
// ahead of an earlier buffered press lets the keys and layers its cycle
// holds go before that press replays. Only the gesture decisions (tap,
// hold, multi-tap and release actions) may come earlier; an edge the
// engine drops, such as a buffered one at CancelAll, has none. At the end
// every control is released and every deadline run: then no further
// command may come, only the latch may hold a chord, and after CancelAll
// nothing is held and no deadline is left.
func FuzzGestures(f *testing.F) {
	progs := [2]*engine.Program{compileProfile(f, fuzzProfile), compileProfile(f, strings.Replace(fuzzProfile, `"name": "fuzz",`, `"name": "fuzz", "defaultLayer": "fn",`, 1))}
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Add([]byte{7, 7, 1, 0, 2, 9, 3})
	f.Add([]byte{0, 8, 10, 0, 8, 240})                                    // the window of a single tap expires
	f.Add([]byte{0, 8, 10, 0, 8, 50, 0, 8, 10, 0, 8, 50, 0, 8, 10, 0, 9}) // triple tap, then its window
	f.Add([]byte{0, 8, 10, 0, 3, 8, 10, 3})                               // another press commits the tap
	f.Add([]byte{0, 8, 30, 3, 8, 20, 3, 8, 30, 0})                        // balanced: nested interrupt, replayed
	f.Add([]byte{0, 8, 30, 3, 8, 20, 0, 8, 30, 3})                        // balanced: rolling interrupt
	f.Add([]byte{1, 8, 10, 2, 8, 100, 2, 1})                              // holdPreferred: press Shift, hold Shift
	f.Add([]byte{6, 8, 10, 3, 3, 8, 50, 6, 9, 9})                         // timeOnly buffers both edges
	f.Add([]byte{6, 9, 9, 9, 9, 6})                                       // hold with repeat
	f.Add([]byte{5, 5, 8, 10, 5, 5, 5})                                   // latch, unlatch, latch
	f.Add([]byte{7, 9, 0, 0, 3, 9, 9, 3, 7, 7, 0, 0})                     // layer hold, then toggle
	f.Add([]byte{0, 8, 10, 12, 0, 12, 0, 0})                              // pause while pending
	f.Add([]byte{2, 8, 200, 13, 2, 2, 5, 5, 13})                          // cancel a repeat and a latch
	f.Add([]byte{2, 0, 14, 15, 2, 0, 15, 3, 3, 10, 9})                    // reset and reload
	f.Add([]byte("01&110"))                                               // a buffered release behind timeOnly
	f.Add([]byte{6, 0x81, 1, 1, 6, 1})                                    // consumed press: its stale release waits behind timeOnly
	f.Add([]byte{1, 8, 250, 6, 0x81, 9, 9, 6})                            // consumed release of a hold while timeOnly is pending
	f.Add([]byte{'0', '1', '&', 0xB1, '1', '0'})                          // the MAP-09 input, north's buffered release consumed
	f.Add([]byte{0x86, 1, 0x81, 1, 6})                                    // consumed press of the timeOnly control, consumed release of north
	f.Add([]byte{3, 6, 3, 3, 3, 9, 6})                                    // south's window waits for its press, buffered behind timeOnly
	f.Add([]byte{6, 3, 3, 8, 240, 3, 3, 9, 6})                            // a replayed release opens a window that is over already
	f.Add([]byte{6, 0, 8, 10, 0, 9, 6})                                   // a replayed press past its deadline, released before it
	f.Add([]byte{2, 8, 100, 0, 8, 5, 3, 8, 15, 2, 8, 250, 3, 0})          // east's release waits behind south's press: its repeat stops
	f.Add([]byte{6, 2, 8, 40, 0, 2, 8, 255, 6, 0})                        // east's press replayed with its release buffered: no repeat
	f.Add([]byte{0, 0x9B, 2, 0x9B, 0, 0x9B, 0, 0x9B, 0, 0x9B, 2, 9, 9})   // west's double tap across east's earlier press
	f.Add([]byte{0x40, 0, 0, 0x40, 2, 2})                                 // back held: west and east use its set
	f.Add([]byte{0, 0x40, 0, 0, 0x40})                                    // west goes down before back, is released under it
	f.Add([]byte{0x40, 0x41, 3, 3, 0x40, 3, 3, 0x41})                     // two function keys, the first let go under south
	f.Add([]byte{0x40, 7, 7, 0x40, 7, 7})                                 // back's set holds the layer fn
	f.Add([]byte{0x41, 4, 4, 0x41, 6, 8, 20, 6})                          // start's set toggles fn, dpad.up has a timeOnly pair
	f.Add([]byte{6, 3, 0x40, 3, 0x40, 6, 9})                              // a function key under a pending timeOnly gesture, south buffered
	f.Add([]byte{0, 3, 0x40, 3, 0x40, 0, 0x41, 3, 3, 9, 9, 0x41})         // west pending, south buffered under back
	f.Add([]byte{0x40, 2, 8, 100, 13, 2, 0x40, 2, 8, 50, 14, 2, 2})       // cancel and reset with back held and east repeating
	f.Add([]byte{0x40, 12, 0, 0, 12, 0x40, 0, 0, 15, 0x40, 0, 0})         // pause and reload with back held
	f.Add([]byte{6, 3, 6, 9, 3})                                          // dpad.up's tap is decided ahead of south's press, its release is not
	f.Add([]byte{0, 2, 0, 9, 2})                                          // west's window opens after east's earlier press replays
	f.Add([]byte{3, 3, 8, 240})                                           // south's single tap: its press action answered the cycle, so the window ends with nothing more
	f.Add([]byte{3, 3, 0, 9, 0})                                          // ... ended by the press of west, the same
	f.Add([]byte{'&', '1', '1', 0x81, '0'})                               // a consumed press is decided on arrival, ahead of an earlier press that waits in the buffer
	f.Add([]byte{7, 9, 3, 3, 9, 9})                                       // fn holds: its south has a double tap only, tapped once, and nothing answered the press
	f.Add([]byte{0x40, 1, 1, 1, 1, 9, 0x40})                              // back holds: its north has a tap and a triple tap, tapped twice, and nothing answered the press
	f.Add([]byte{0, 8, 10, 0x80, 9, 9})                                   // west's release is consumed: the waiting press is answered by the script
	f.Add([]byte{0, 8, 10, 0, 12, 12, 9})                                 // pause while west's window is open: it is canceled
	f.Add([]byte{0, 8, 10, 0, 13, 0, 8, 10, 0, 15, 9})                    // cancel and reload while west's window is open
	f.Fuzz(func(t *testing.T, data []byte) {
		l := &ledger{t: t, held: map[ledgerKey]int{}, ctl: map[uint64]control.Control{}}
		prog := 0
		consume := false
		e := engine.New(progs[prog], l, engine.Options{
			Trace: func(d engine.Decision) {
				l.decisions++
				l.unmatched(d)
				if d.Cycle != 0 {
					l.ctl[d.Cycle] = d.Control
				}
				l.decided(d)
			},
			Button: func(engine.ButtonEvent) bool { return consume },
		})
		l.e = e
		var now clock.Time
		var down [len(fuzzControls)]bool
		// fnDown are back and start as the operations left them, and fnFirst
		// is the one whose set is used: the one pressed first of those held,
		// which the engine must report as ActiveFunctionKey.
		fnKeys := [2]control.Control{control.Back, control.Start}
		var fnDown [2]bool
		fnFirst := control.Invalid
		advanceTo := func(target clock.Time) {
			for steps := 0; ; steps++ {
				d, ok := e.NextDeadline()
				if !ok || d > target {
					break
				}
				if steps > 10000 {
					t.Fatalf("deadline %v does not make progress at %v", d, now)
				}
				now = max(now, d)
				e.Apply(now)
			}
			now = max(now, target)
		}
		for i := 0; i < len(data); i++ {
			b := data[i]
			switch op := b & 0x0F; {
			case op < 8 && b&0x40 != 0:
				// A function key: no output and no deadline, whatever else is
				// going on.
				k := op & 1
				fnDown[k] = !fnDown[k]
				switch {
				case fnDown[k] && (fnFirst == control.Invalid || !fnDown[fnIndex(fnFirst)]):
					fnFirst = fnKeys[k]
				case !fnDown[k] && fnFirst == fnKeys[k]:
					fnFirst = control.Invalid
					if fnDown[1-k] {
						fnFirst = fnKeys[1-k]
					}
				}
				cmds := l.cmds
				deadline, deadlineSet := e.NextDeadline()
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: fnKeys[k], Down: fnDown[k]})
				if l.cmds != cmds {
					t.Fatalf("an edge of %s sent %d commands", fnKeys[k], l.cmds-cmds)
				}
				if d, ok := e.NextDeadline(); d != deadline || ok != deadlineSet {
					t.Fatalf("an edge of %s moved the next deadline from %v (%t) to %v (%t)", fnKeys[k], deadline, deadlineSet, d, ok)
				}
				if got := e.ActiveFunctionKey(); got != fnFirst {
					t.Fatalf("the active function key is %v after an edge of %s; want %v", got, fnKeys[k], fnFirst)
				}
			case op < 8:
				down[op] = !down[op]
				l.down[fuzzControls[op]] = down[op]
				l.stepped(fuzzControls[op], down[op])
				consume = b&0x80 != 0
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: fuzzControls[op], Down: down[op]})
				consume = false
			case op == 8:
				ms := 1
				if i+1 < len(data) {
					i++
					ms = max(1, int(data[i]))
				}
				advanceTo(now.Add(time.Duration(ms) * time.Millisecond))
			case op == 9:
				if d, ok := e.NextDeadline(); ok {
					advanceTo(d)
				}
			case op == 10:
				if d, ok := e.NextDeadline(); ok && d > now+clock.Time(time.Millisecond) {
					advanceTo(d - clock.Time(time.Millisecond))
				}
			case op == 11:
				advanceTo(now.Add(time.Duration(b>>4+1) * time.Millisecond))
			case op == 12:
				if e.Paused() {
					e.Resume()
				} else {
					e.Pause(now)
					l.releaseAll()
				}
			case op == 13:
				e.CancelAll(now)
				l.releaseAll()
			case op == 14:
				e.Reset(now)
				l.releaseAll()
				down = [len(fuzzControls)]bool{}
				fnDown, fnFirst = [2]bool{}, control.Invalid
				clear(l.down[:])
				if got := e.ActiveFunctionKey(); got != control.Invalid {
					t.Fatalf("the active function key is %v after Reset", got)
				}
			case op == 15:
				prog = 1 - prog
				e.Reload(now, progs[prog])
				l.releaseAll()
			}
		}
		for i, d := range down {
			if d {
				l.down[fuzzControls[i]] = false
				l.stepped(fuzzControls[i], false)
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: fuzzControls[i], Down: false})
			}
		}
		for k, d := range fnDown {
			if d {
				e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: fnKeys[k], Down: false})
			}
		}
		if got := e.ActiveFunctionKey(); got != control.Invalid {
			t.Fatalf("the active function key is %v with both released", got)
		}
		for steps := 0; ; steps++ {
			d, ok := e.NextDeadline()
			if !ok {
				break
			}
			if steps > 10000 {
				t.Fatalf("deadline %v does not make progress at %v", d, now)
			}
			now = max(now, d)
			e.Apply(now)
		}
		cmds := l.cmds
		e.Apply(now.Add(10 * time.Second))
		if l.cmds != cmds {
			t.Fatalf("%d commands after every control was released and every deadline ran", l.cmds-cmds)
		}
		for k, n := range l.held {
			if k.c != fuzzLatched || n != 1 || len(l.held) != 1 {
				t.Fatalf("every control is up, but %s holds %s %d times (%d held in all)", k.o, k.c, n, len(l.held))
			}
		}
		e.CancelAll(now)
		if len(l.held) != 0 {
			t.Fatalf("CancelAll left %v held", l.held)
		}
		if d, ok := e.NextDeadline(); ok {
			t.Fatalf("deadline %v after CancelAll", d)
		}
	})
}

// fnIndex is 0 for back and 1 for start.
func fnIndex(c control.Control) int {
	if c == control.Start {
		return 1
	}
	return 0
}

// compileProfile parses and compiles the profile src. Warnings are fine;
// errors fail the test.
func compileProfile(t testing.TB, src string) *engine.Program {
	t.Helper()
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

type ledgerKey struct {
	o engine.OwnerID
	c keys.Chord
}

// ledger is an engine.Output that counts the Downs of each owner and chord
// that no Up has matched yet, a multiset: a binding whose press and hold
// both hold the same chord sends two Downs and two Ups, which the Arbiter
// takes as one key held twice.
type ledger struct {
	t         *testing.T
	e         *engine.Engine
	held      map[ledgerKey]int
	cmds      int
	decisions int
	// ctl is the control of each cycle, from the decisions, and down the
	// physical state of each control, as FuzzGestures steps it.
	ctl  map[uint64]control.Control
	down [control.Count]bool
	// edges are the button edges stepped so far, in order; next is the
	// index after the edge that the last edge decision matched.
	edges []ledgerEdge
	next  int
}

type ledgerEdge struct {
	c    control.Control
	down bool
}

func (l *ledger) stepped(c control.Control, down bool) {
	l.edges = append(l.edges, ledgerEdge{c, down})
}

// decided checks that d, when it is the decision about an edge itself,
// comes after the edge decisions of every earlier edge: it matches the
// first edge of its control and direction after the last one matched.
func (l *ledger) decided(d engine.Decision) {
	l.t.Helper()
	edge := d.Edge == engine.EdgeUp || d.Edge == engine.EdgeDown && d.Gesture == engine.GesturePress
	if !edge || d.Reason == engine.ReasonSpurious || d.Reason == engine.ReasonFunction {
		return
	}
	want := ledgerEdge{d.Control, d.Edge == engine.EdgeDown}
	if d.Reason == engine.ReasonScript {
		// A press that the script's button handler consumed is decided on
		// arrival: it starts no cycle and never waits in the buffer behind a
		// pending gesture, and it sends and holds nothing, so it need not keep
		// the order of the edges (ADR-0071). Its edge has no other decision,
		// and is the one being stepped, the last.
		if n := len(l.edges); n > l.next && l.edges[n-1] == want {
			l.edges = l.edges[:n-1]
			return
		}
		l.t.Fatalf("decision %s answers no edge being stepped", d)
	}
	for i := l.next; i < len(l.edges); i++ {
		if l.edges[i] == want {
			l.next = i + 1
			return
		}
	}
	l.t.Fatalf("decision %s comes after the decision of a later edge", d)
}

// unmatched checks that d, when it is the end of a multi-tap window with no
// action for its taps (an unbound decision of a gesture that is not the
// press, ADR-0071), is the answer to a press of the same control that came
// earlier: it has that cycle, and it carries no action or owner.
func (l *ledger) unmatched(d engine.Decision) {
	l.t.Helper()
	if d.Reason != engine.ReasonUnbound || d.Gesture == engine.GesturePress || d.Gesture == engine.GestureNone {
		return
	}
	if c, ok := l.ctl[d.Cycle]; !ok || c != d.Control || d.Edge != engine.EdgeDown || d.Action != nil || d.Owner != (engine.OwnerID{}) {
		l.t.Fatalf("decision %s is no answer to an earlier press of %s in cycle %d", d, d.Control, d.Cycle)
	}
}

func (l *ledger) command(what string, o engine.OwnerID, c keys.Chord) {
	l.t.Helper()
	l.cmds++
	if what != "Up" && what != "StopRepeat" && l.e.Paused() {
		l.t.Fatalf("%s %s %s while paused", what, o, c)
	}
}

func (l *ledger) Down(o engine.OwnerID, c keys.Chord, _ time.Duration) {
	l.command("Down", o, c)
	l.held[ledgerKey{o, c}]++
}

func (l *ledger) Up(o engine.OwnerID, c keys.Chord) {
	l.command("Up", o, c)
	k := ledgerKey{o, c}
	switch n := l.held[k]; n {
	case 0:
		l.t.Fatalf("Up %s %s, which is not held", o, c)
	case 1:
		delete(l.held, k)
	default:
		l.held[k] = n - 1
	}
}

func (l *ledger) Tap(o engine.OwnerID, c keys.Chord, hold time.Duration) {
	l.command("Tap", o, c)
	if hold < 0 {
		l.t.Fatalf("Tap %s %s for %v", o, c, hold)
	}
}

func (l *ledger) Repeat(o engine.OwnerID, c keys.Chord) {
	l.command("Repeat", o, c)
	if l.held[ledgerKey{o, c}] == 0 {
		l.t.Fatalf("Repeat %s %s, which is not held", o, c)
	}
	if ctl, ok := l.ctl[o.Cycle]; ok && !l.down[ctl] {
		l.t.Fatalf("Repeat %s %s after %s was released", o, c, ctl)
	}
}

func (l *ledger) TapSequence(o engine.OwnerID, items []keys.Chord, hold, _ time.Duration) {
	for _, c := range items {
		l.Tap(o, c, hold)
	}
}

// A sequence and a text hold nothing: the ledger only counts them.
func (l *ledger) Sequence(engine.OwnerID, []keys.Step, time.Duration, time.Duration) { l.cmds++ }
func (l *ledger) TypeText(engine.OwnerID, string, time.Duration)                     { l.cmds++ }
func (l *ledger) RepeatTap(o engine.OwnerID, c keys.Chord, hold time.Duration) {
	l.command("RepeatTap", o, c)
	if hold < 0 {
		l.t.Fatal("negative repeat tap hold")
	}
	if ctl, ok := l.ctl[o.Cycle]; ok && !l.down[ctl] {
		l.t.Fatalf("repeat tap after %s released", ctl)
	}
}
func (l *ledger) StopRepeat(o engine.OwnerID, c keys.Chord) { l.command("StopRepeat", o, c) }

// A pointer move or jump holds nothing: the ledger only counts it as a command.
func (l *ledger) Move(engine.OwnerID, int32, int32) { l.cmds++ }
func (l *ledger) Jump(engine.OwnerID, int32, int32) { l.cmds++ }

// releaseAll forgets what is held, as the slot's ReleaseOwners does after
// CancelAll.
func (l *ledger) releaseAll() { clear(l.held) }
