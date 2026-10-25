package engine_test

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// runM2 advances every deadline before the next input, as slot.Replay does.
func runM2(t testing.TB, e *engine.Engine, r *recorder, script string) {
	t.Helper()
	for tok := range strings.FieldsSeq(script) {
		op, at, ok := strings.Cut(tok, "@")
		n, err := strconv.Atoi(at)
		if !ok || err != nil {
			t.Fatalf("bad step %q", tok)
		}
		now := clock.Time(time.Duration(n) * time.Millisecond)
		for steps := 0; ; steps++ {
			d, due := e.NextDeadline()
			if !due || d > now {
				break
			}
			if steps > 10000 {
				t.Fatal("deadline did not make progress")
			}
			r.now = d
			e.Apply(d)
		}
		r.now = now
		switch op {
		case "wait":
		case "cancel":
			e.CancelAll(now)
		case "pause":
			e.Pause(now)
		case "resume":
			e.Resume()
		case "reset":
			e.Reset(now)
		default:
			c, ok := control.Parse(op[1:])
			if !ok {
				t.Fatalf("bad control %q", op)
			}
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: op[0] == '+'})
		}
	}
}

func TestM2Gestures(t *testing.T) {
	const pair = `"west":{"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"E"}}`
	const south = `"south":{"press":{"type":"keys","keys":"Ctrl+Z","mode":"tap"}}`
	const (
		westPair      = `"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}}`
		northPair     = `"north":{"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"F15"}}`
		southDouble   = `"south":{"tap":{"type":"keys","keys":"Ctrl+Z"},"doubleTap":{"type":"keys","keys":"F16"}}`
		timeOnlyUp    = `"dpad.up":{"policy":"timeOnly","tap":{"type":"keys","keys":"F18"},"hold":{"type":"keys","keys":"F19"}}`
		timeOnlyUp400 = `"dpad.up":{"policy":"timeOnly","timing":{"holdMs":400},"tap":{"type":"keys","keys":"F18"},"hold":{"type":"keys","keys":"F19"}}`
		westMulti     = `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}}`
		westMultiHold = `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`
		eastA         = `"east":{"press":{"type":"keys","keys":"A"}}`
		// The first repeat is at 30 (10) ms after the press, then one every
		// max(1 s / 50, tapPressMs + tapGapMs) = 40 ms.
		eastRepeat30 = `"east":{"press":{"type":"keys","keys":"F13","mode":"hold","repeat":{"delayMs":30,"rateHz":50}}}`
		eastRepeat10 = `"east":{"press":{"type":"keys","keys":"F13","mode":"hold","repeat":{"delayMs":10,"rateHz":50}}}`
		// A press action (hold mode by default) next to tap and hold.
		westPressLCtrl = `"west":{"press":{"type":"keys","keys":"LCtrl"},"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}}`
		westPressShift = `"west":{"press":{"type":"keys","keys":"Shift"},"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"Shift"}}`
		westMultiLCtrl = `"west":{"press":{"type":"keys","keys":"LCtrl"},"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`
		southZ         = `"south":{"press":{"type":"keys","keys":"Z","mode":"tap"}}`
	)
	for _, tc := range []struct{ name, bindings, steps, want string }{
		{"press release", `"east":{"press":{"type":"keys","keys":"A","mode":"tap"},"release":{"type":"keys","keys":"B"}}`, "+east@0 -east@10", "@0 tap decl/1 A 20ms, @10 tap decl/1 B 20ms"},
		{"release only", `"east":{"release":{"type":"keys","keys":"B"}}`, "+east@0 -east@10", "@10 tap decl/1 B 20ms"},
		{"tap only waits for release", `"west":{"tap":{"type":"keys","keys":"B"}}`, "+west@0 wait@300 -west@310", "@310 tap decl/1 B 20ms"},
		{"tap only does not block another control", `"west":{"tap":{"type":"keys","keys":"B"}},` + south, "+west@0 +south@10 -south@20 -west@30", "@10 tap decl/2 Ctrl+Z 20ms, @30 tap decl/1 B 20ms"},
		{"hold only immediate", `"west":{"hold":{"type":"keys","keys":"E"}}`, "+west@0 -west@10", "@0 down decl/1 E, @10 up decl/1 E"},
		{"tap before threshold", pair, "+west@0 -west@249", "@249 tap decl/1 B 20ms"},
		{"hold at threshold", pair, "+west@0 -west@250", "@250 down decl/1 E, @250 up decl/1 E"},
		{"balanced nested", pair + "," + south, "+west@0 +south@30 -south@50 -west@80", "@50 down decl/1 E, @50 tap decl/2 Ctrl+Z 20ms, @80 up decl/1 E"},
		{"balanced rolling", pair + "," + south, "+west@0 +south@30 -west@50 -south@80", "@50 tap decl/1 B 20ms, @50 tap decl/2 Ctrl+Z 20ms"},
		{"timeOnly buffers both edges", strings.Replace(pair, `"west":{`, `"west":{"policy":"timeOnly",`, 1) + "," + south, "+west@0 +south@30 -south@50 -west@80", "@80 tap decl/1 B 20ms, @80 tap decl/2 Ctrl+Z 20ms"},
		{"auto modifier prefers hold", strings.Replace(pair, `"keys":"E"`, `"keys":"Shift"`, 1) + "," + south, "+west@0 +south@30 -south@50 -west@80", "@30 down decl/1 Shift, @30 tap decl/2 Ctrl+Z 20ms, @80 up decl/1 Shift"},
		{"other release is not interrupt", pair + "," + south, "+south@0 +west@30 -south@50 -west@80", "@0 tap decl/1 Ctrl+Z 20ms, @80 tap decl/2 B 20ms"},
		{"pause cancels buffered edges", pair + "," + south, "+west@0 +south@30 pause@40 -west@50 -south@60 resume@70 wait@500", ""},
		{"press plus hold releases both", `"west":{"press":{"type":"keys","keys":"Shift"},"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"E"}}`, "+west@0 wait@250 -west@300", "@0 down decl/1 Shift, @250 down decl/1 E, @300 up decl/1 E, @300 up decl/1 Shift"},
		{"press plus short tap releases press", `"west":{"press":{"type":"keys","keys":"Shift"},"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"E"}}`, "+west@0 -west@10", "@0 down decl/1 Shift, @10 tap decl/1 B 20ms, @10 up decl/1 Shift"},
		{"multi hold first hold does not tap", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`, "+west@0 -west@300 wait@1000", "@250 down decl/1 E, @300 up decl/1 E"},
		{"multi hold second short press", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`, "+west@0 -west@10 +west@100 -west@110 wait@1000", "@110 tap decl/1 F14 20ms"},
		{"multi timer cannot fire during next press", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}}`, "+west@0 -west@10 +west@100 -west@400 wait@1000", "@400 tap decl/1 F14 20ms"},
		{"invalid press cannot interrupt", pair, "+west@0 -west@30", "@30 tap decl/1 B 20ms"},
		{"missing lower tap consumes", `"west":{"doubleTap":{"type":"keys","keys":"F14"}}`, "+west@0 -west@10 wait@300", ""},
		{"double completes immediately", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}}`, "+west@0 -west@10 +west@100 -west@110 wait@600", "@110 tap decl/1 F14 20ms"},
		{"other press commits waiting tap", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}},` + south, "+west@0 -west@10 +south@40 -south@50 wait@600", "@40 tap decl/1 F13 20ms, @40 tap decl/2 Ctrl+Z 20ms"},
		{"multi hold has no delayed tap", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"hold":{"type":"keys","keys":"E"}}`, "+west@0 -west@10 +west@100 wait@350 -west@400 wait@900", "@350 down decl/1 E, @400 up decl/1 E"},
		{"pause cancels waiting tap", `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"}}`, "+west@0 -west@10 pause@30 resume@40 wait@900", ""},
		// north's first release waits in the buffer behind dpad.up's
		// pending gesture after north's press was replayed (MAP-09). It
		// stays in order: it waits until dpad.up's tap at 70, and so does
		// west's release, which comes after it; the second press and
		// release of north follow it. That release decides north's tap,
		// ahead of west's release, which came before it, but lets Shift up
		// only after west's E, in order.
		{"buffered release of a dispatched control", `"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}},` +
			`"north":{"press":{"type":"keys","keys":"Shift"},"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"Shift"}},` +
			timeOnlyUp,
			"+west@0 +north@10 +dpad.up@20 -north@30 +north@40 -west@50 -north@60 -dpad.up@70 wait@1000",
			"@30 down decl/1 E, @30 down decl/2 Shift, @30 down decl/2 Shift, @70 tap decl/3 F18 20ms, @70 up decl/2 Shift, @70 up decl/2 Shift, @70 down decl/4 Shift, @70 tap decl/4 B 20ms, @70 up decl/1 E, @70 up decl/4 Shift"},
		// A release goes at once only when nothing waits in the buffer.
		// Here leftshoulder's release comes after south's press, which
		// waits for north's gesture: it waits too, and Z taps with LCtrl
		// down, as the edges came.
		{"buffered release keeps a modifier for an earlier press", westPair + "," + northPair + `,"leftshoulder":{"press":{"type":"keys","keys":"LCtrl"}},"south":{"press":{"type":"keys","keys":"Z","mode":"tap"}}`,
			"+west@0 +leftshoulder@10 +north@20 +south@25 -leftshoulder@28 -south@40 -north@50 -west@60",
			"@28 down decl/1 E, @28 down decl/2 LCtrl, @40 down decl/3 F15, @40 tap decl/4 Z 20ms, @40 up decl/2 LCtrl, @50 up decl/3 F15, @60 up decl/1 E"},
		{"release with nothing buffered does not wait", westPair + `,"leftshoulder":{"press":{"type":"keys","keys":"LCtrl"}}`,
			"+leftshoulder@0 +west@10 -leftshoulder@20 -west@30",
			"@0 down decl/1 LCtrl, @20 up decl/1 LCtrl, @30 tap decl/2 F13 20ms"},
		// Two taps of south 10 ms apart are a double tap when their edges
		// wait in the buffer. south's first release stays behind the edges
		// before it, so it does not open the window early: not while its
		// second press waits behind dpad.up's timeOnly gesture (the window
		// would expire first), and not ahead of east's press, which came
		// before it (its replay would commit the window).
		{"double tap behind timeOnly", westPair + "," + timeOnlyUp + "," + southDouble,
			"+west@0 +south@10 +dpad.up@20 -south@30 +south@40 -south@60 -west@500 -dpad.up@510 wait@1000",
			"@30 down decl/1 E, @270 down decl/3 F19, @270 tap decl/2 F16 20ms, @500 up decl/1 E, @510 up decl/3 F19"},
		{"double tap behind balanced", westPair + "," + northPair + "," + southDouble + `,"east":{"press":{"type":"keys","keys":"A"}}`,
			"+west@0 +south@10 +north@20 +east@25 -south@30 +south@40 -south@50 -east@60 -north@70 -west@80 wait@1000",
			"@30 down decl/1 E, @50 down decl/3 F15, @50 down decl/4 A, @50 tap decl/2 F16 20ms, @60 up decl/4 A, @70 up decl/3 F15, @80 up decl/1 E"},
		// south's first release goes at once (nothing is buffered), and its
		// window expires at 240 while its second press waits behind
		// dpad.up: the window waits for that press, which came at 30.
		{"double tap across a pending gesture", timeOnlyUp + "," + southDouble,
			"+south@0 +dpad.up@10 -south@20 +south@30 -south@40 -dpad.up@300 wait@1000",
			"@260 down decl/2 F19, @260 tap decl/1 F16 20ms, @300 up decl/2 F19"},
		// The window of a replayed release counts from its original time,
		// not from the replay: a press 290 ms after the release is another
		// tap, and so is one 225 ms after it that is replayed too.
		{"replayed window counts from the release", timeOnlyUp + "," + southDouble,
			"+dpad.up@0 +south@10 -south@20 -dpad.up@300 +south@310 -south@320 wait@1000",
			"@250 down decl/1 F19, @250 tap decl/2 Ctrl+Z 20ms, @300 up decl/1 F19, @540 tap decl/3 Ctrl+Z 20ms"},
		{"replayed press after the window", timeOnlyUp + "," + southDouble,
			"+dpad.up@0 +south@10 -south@20 +south@245 -south@248 -dpad.up@300 wait@1000",
			"@250 down decl/1 F19, @250 tap decl/2 Ctrl+Z 20ms, @300 up decl/1 F19, @468 tap decl/3 Ctrl+Z 20ms"},
		// The window is over before leftshoulder's release is replayed, so
		// Z taps with LCtrl still down, as the edges came.
		{"replayed window over before a later edge", timeOnlyUp + `,"leftshoulder":{"press":{"type":"keys","keys":"LCtrl"}},"south":{"tap":{"type":"keys","keys":"Z"},"doubleTap":{"type":"keys","keys":"F16"}}`,
			"+leftshoulder@0 +dpad.up@5 +south@10 -south@20 -leftshoulder@245 -dpad.up@300 wait@1000",
			"@0 down decl/1 LCtrl, @255 down decl/2 F19, @255 tap decl/3 Z 20ms, @255 up decl/1 LCtrl, @300 up decl/2 F19"},
		// A replayed press whose hold deadline, from its original time, has
		// passed is a tap when its release, also buffered, came before the
		// deadline; interrupts before that release count as they came.
		{"replayed press released before its deadline", timeOnlyUp400 + "," + westPair,
			"+dpad.up@0 +west@10 -west@100 -dpad.up@500",
			"@400 down decl/1 F19, @400 tap decl/2 F13 20ms, @500 up decl/1 F19"},
		{"replayed press released after its deadline", timeOnlyUp400 + "," + westPair,
			"+dpad.up@0 +west@10 -west@300 -dpad.up@500",
			"@400 down decl/1 F19, @400 down decl/2 E, @400 up decl/2 E, @500 up decl/1 F19"},
		{"replayed press interrupted before its release", timeOnlyUp400 + "," + westPair + "," + south,
			"+dpad.up@0 +west@10 +south@20 -south@30 -west@100 -dpad.up@500",
			"@400 down decl/1 F19, @400 down decl/2 E, @400 tap decl/3 Ctrl+Z 20ms, @400 up decl/2 E, @500 up decl/1 F19"},
		{"replayed holdPreferred press released before its deadline", timeOnlyUp400 + `,"north":{"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"Shift"}},` + south,
			"+dpad.up@0 +north@10 -north@100 +south@150 -south@160 -dpad.up@500",
			"@400 down decl/1 F19, @400 tap decl/2 B 20ms, @400 tap decl/3 Ctrl+Z 20ms, @500 up decl/1 F19"},
		{"latch consumes next press and release", `"west":{"latch":true,"press":{"type":"keys","keys":"Shift"},"release":{"type":"keys","keys":"F13"}}`, "+west@0 -west@10 +west@20 -west@30", "@0 down decl/1 Shift, @10 tap decl/1 F13 20ms, @20 up decl/1 Shift"},
		// east's release waits behind south's press, which waits for west's
		// gesture until 350, but its repeat stops at the release (OUT-11):
		// only the Up waits, in order.
		{"buffered release stops the repeat", eastRepeat30 + "," + westPair + "," + south,
			"+east@0 +west@100 +south@105 -east@120 -south@400 -west@410",
			"@0 down decl/1 F13, @30 repeat decl/1 F13, @70 repeat decl/1 F13, @110 repeat decl/1 F13, @350 down decl/2 E, @350 tap decl/3 Ctrl+Z 20ms, @350 up decl/1 F13, @410 up decl/2 E"},
		// east's press is replayed at 250, when dpad.up's gesture resolves;
		// its release, at 30, is still buffered behind west's gesture, so it
		// does not start repeating.
		{"replayed press released in the buffer does not repeat", timeOnlyUp + "," + eastRepeat10 + "," + westPair,
			"+dpad.up@0 +east@10 +west@20 -east@30 -dpad.up@300 -west@310",
			"@250 down decl/1 F19, @250 down decl/2 F13, @270 down decl/3 E, @270 up decl/2 F13, @300 up decl/1 F19, @310 up decl/3 E"},
		// A press of another control commits a multi-tap window only when it
		// came after the release that opened it, in input order, as when
		// west has no hold: here east's press came before west's first
		// release, which decides west's tap, and replays before the release
		// opens the window.
		{"double tap with a hold across another press", westMultiHold + "," + eastA,
			"+west@0 +east@10 -west@20 +west@30 -west@40 -east@50 wait@1000",
			"@20 down decl/2 A, @40 tap decl/1 F14 20ms, @50 up decl/2 A"},
		// The same when both edges have one time, as the edges of one HID
		// report do: the order they came in decides.
		{"double tap with a hold across a press at the release's time", westMultiHold + "," + eastA,
			"+west@0 +east@20 -west@20 +west@30 -west@40 -east@50 wait@1000",
			"@20 down decl/2 A, @40 tap decl/1 F14 20ms, @50 up decl/2 A"},
		{"double tap without a hold across another press", westMulti + "," + eastA,
			"+west@0 +east@10 -west@20 +west@30 -west@40 -east@50 wait@1000",
			"@10 down decl/2 A, @40 tap decl/1 F14 20ms, @50 up decl/2 A"},
		{"timeOnly double tap across another tap", strings.Replace(westMultiHold, `"west":{`, `"west":{"policy":"timeOnly",`, 1) + "," + eastA,
			"+west@0 +east@10 -east@20 -west@30 +west@40 -west@50 wait@1000",
			"@30 down decl/2 A, @30 up decl/2 A, @50 tap decl/1 F14 20ms"},
		// west's window expires at 240 while its second press waits behind
		// dpad.up with east's press, which came before west's first
		// release: the window waits for west's press.
		{"double tap with a hold behind an earlier press", westMultiHold + "," + timeOnlyUp400 + "," + eastA,
			"+west@0 +dpad.up@5 +east@10 -west@20 +west@30 -west@40 -east@50 -dpad.up@500 wait@1000",
			"@405 down decl/2 F19, @405 down decl/3 A, @405 tap decl/1 F14 20ms, @405 up decl/3 A, @500 up decl/2 F19"},
		// west's release decides its tap, which comes before the edges it
		// captured; the rest of the release stays in order. south's press
		// came while west's press action held the modifier, so Z taps with
		// it down, whatever the policy.
		{"tap keeps the press action for a captured press", westPressLCtrl + "," + southZ,
			"+west@0 +south@10 -west@20 -south@30",
			"@0 down decl/1 LCtrl, @20 tap decl/1 F13 20ms, @20 tap decl/2 Z 20ms, @20 up decl/1 LCtrl"},
		{"timeOnly tap keeps the press action for a captured press", strings.Replace(westPressShift, `"west":{`, `"west":{"policy":"timeOnly",`, 1) + "," + southZ,
			"+west@0 +south@10 -west@20 -south@30",
			"@0 down decl/1 Shift, @20 tap decl/1 B 20ms, @20 tap decl/2 Z 20ms, @20 up decl/1 Shift"},
		{"balanced tap keeps the press action for a captured press", strings.Replace(westPressShift, `"west":{`, `"west":{"policy":"balanced",`, 1) + "," + southZ,
			"+west@0 +south@10 -west@20 -south@30",
			"@0 down decl/1 Shift, @20 tap decl/1 B 20ms, @20 tap decl/2 Z 20ms, @20 up decl/1 Shift"},
		// So does west's release action: it taps after south's press,
		// which came before the release.
		{"release action after a captured press", `"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"},"release":{"type":"keys","keys":"F17"}},` + southZ,
			"+west@0 +south@10 -west@20 -south@30",
			"@20 tap decl/1 F13 20ms, @20 tap decl/2 Z 20ms, @20 tap decl/1 F17 20ms"},
		// west's release ends its pending gesture, but opens the window only
		// when it replays, after south's press, which came before it: that
		// press does not commit the window, and LCtrl goes up in order,
		// before the window expires.
		{"window keeps the press action for a captured press", westMultiLCtrl + "," + southZ,
			"+west@0 +south@10 -west@20 -south@30 wait@1000",
			"@0 down decl/1 LCtrl, @20 tap decl/2 Z 20ms, @20 up decl/1 LCtrl, @240 tap decl/1 F13 20ms"},
		// west's window expires at 240, while its release still waits
		// behind dpad.up's press, which came before it: the window waits for
		// the release, as when nothing is buffered, where LCtrl goes up
		// before the window's tap.
		{"window waits for its buffered release", westMultiLCtrl + "," + timeOnlyUp,
			"+west@0 +dpad.up@10 -west@20 -dpad.up@500 wait@1000",
			"@0 down decl/1 LCtrl, @260 down decl/2 F19, @260 up decl/1 LCtrl, @260 tap decl/1 F13 20ms, @500 up decl/2 F19"},
		{"second tap after a buffered release", westMultiLCtrl + "," + timeOnlyUp,
			"+west@0 +dpad.up@10 -west@20 +west@30 -west@40 -dpad.up@500 wait@1000",
			"@0 down decl/1 LCtrl, @260 down decl/2 F19, @260 up decl/1 LCtrl, @260 down decl/1 LCtrl, @260 tap decl/1 F14 20ms, @260 up decl/1 LCtrl, @500 up decl/2 F19"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compile(t, tc.bindings), r, engine.Options{})
			runM2(t, e, r, tc.steps)
			if r.String() != tc.want {
				t.Fatalf("got  %s\nwant %s", r.String(), tc.want)
			}
			e.CancelAll(r.now)
			if d, ok := e.NextDeadline(); ok {
				t.Fatalf("deadline %v after cancel", d)
			}
		})
	}
}

// TestM2BufferedLayerRelease: leftshoulder holds the layer nav. Its release
// comes after south's press, which waits for north's gesture, so it waits
// too, and south's press finds nav's binding, as the edges came.
func TestM2BufferedLayerRelease(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"t","layers":{"base":{`+
		`"west":{"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}},`+
		`"north":{"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"F15"}},`+
		`"leftshoulder":{"press":{"type":"layer","layer":"nav","op":"hold"}},`+
		`"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}},`+
		`"nav":{"south":{"press":{"type":"keys","keys":"C","mode":"tap"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runM2(t, e, r, "+west@0 +leftshoulder@10 +north@20 +south@25 -leftshoulder@28 -south@40 -north@50 -west@60")
	if got, want := r.String(), "@28 down decl/1 E, @40 down decl/3 F15, @40 tap decl/4 C 20ms, @50 up decl/3 F15, @60 up decl/1 E"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestM2TapKeepsPressLayer: west's press action holds the layer nav. Its
// release decides the tap ahead of south's press, which it captured, but
// releases nav only in order: south's press came while nav was active, so
// it finds nav's binding.
func TestM2TapKeepsPressLayer(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":0,"name":"t","layers":{"base":{`+
		`"west":{"press":{"type":"layer","layer":"nav","op":"hold"},"tap":{"type":"keys","keys":"F13"},"hold":{"type":"keys","keys":"E"}},`+
		`"south":{"press":{"type":"keys","keys":"A","mode":"tap"}}},`+
		`"nav":{"south":{"press":{"type":"keys","keys":"C","mode":"tap"}}}}}`)
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runM2(t, e, r, "+west@0 +south@10 -west@20 -south@30")
	if got, want := r.String(), "@20 tap decl/1 F13 20ms, @20 tap decl/2 C 20ms"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

// TestM2LateStepAfterWindow: a press whose original time is at or after the
// end of the multi-tap window starts a new count, even when no Apply ran at
// the window's deadline (a late slot).
func TestM2LateStepAfterWindow(t *testing.T) {
	r := &recorder{}
	e := engine.New(compile(t, `"south":{"tap":{"type":"keys","keys":"Ctrl+Z"},"doubleTap":{"type":"keys","keys":"F16"}}`), r, engine.Options{})
	for _, in := range []struct {
		ms   int
		down bool
	}{{0, true}, {10, false}, {300, true}, {310, false}} {
		r.now = clock.Time(time.Duration(in.ms) * time.Millisecond)
		e.Step(engine.Input{T: r.now, Kind: engine.InputButton, Control: control.South, Down: in.down})
	}
	r.now = clock.Time(530 * time.Millisecond)
	e.Apply(r.now)
	if got, want := r.String(), "@300 tap decl/1 Ctrl+Z 20ms, @530 tap decl/2 Ctrl+Z 20ms"; got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestM2TripleQuad(t *testing.T) {
	for count := 1; count <= 4; count++ {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			p := compile(t, `"west":{"tap":{"type":"keys","keys":"F13"},"doubleTap":{"type":"keys","keys":"F14"},"tripleTap":{"type":"keys","keys":"F15"},"quadTap":{"type":"keys","keys":"F16"}}`)
			r := &recorder{}
			e := engine.New(p, r, engine.Options{})
			var script strings.Builder
			for n := 0; n < count; n++ {
				fmt.Fprintf(&script, "+west@%d -west@%d ", n*50, n*50+10)
			}
			script.WriteString("wait@1000")
			runM2(t, e, r, script.String())
			at := (count-1)*50 + 10
			if count < 4 {
				at += 220
			}
			want := fmt.Sprintf("@%d tap decl/1 F%d 20ms", at, 12+count)
			if r.String() != want {
				t.Fatalf("got %s; want %s", r.String(), want)
			}
		})
	}
}

// setAction sets b's action for gesture g.
func setAction(b *profile.Binding, g engine.Gesture, a *profile.Action) {
	switch g {
	case engine.GesturePress:
		b.Press = a
	case engine.GestureRelease:
		b.Release = a
	case engine.GestureTap:
		b.Tap = a
	case engine.GestureDoubleTap:
		b.DoubleTap = a
	case engine.GestureTripleTap:
		b.TripleTap = a
	case engine.GestureQuadTap:
		b.QuadTap = a
	case engine.GestureHold:
		b.Hold = a
	default:
		panic("setAction: gesture " + g.String())
	}
}

// TestM2CompileRejectsUnsafeModes checks the action rules on models built
// by hand, which Parse never returns: a gesture that runs after the control
// is released (profile.Gesture.Momentary) cannot hold keys or a layer, nor
// repeat; a key sequence needs tap mode and cannot repeat; repeat needs hold
// mode. commitMulti runs its action on a copy of the control's state, so a
// hold it started would never be released (invariant 1).
func TestM2CompileRejectsUnsafeModes(t *testing.T) {
	a := keys.MustParse("A")
	seq := []keys.Chord{keys.MustParse("Ctrl+K"), keys.MustParse("S")}
	keysAction := func(m profile.Mode, seq []keys.Chord, r *profile.Repeat) *profile.Action {
		return &profile.Action{ID: "/a", Type: profile.ActionKeys, Mode: m, Keys: a, Sequence: seq, Repeat: r}
	}
	layer := func(op string) *profile.Action {
		return &profile.Action{ID: "/a", Type: profile.ActionLayer, Layer: "base", Op: op}
	}
	momentary := []engine.Gesture{engine.GestureRelease, engine.GestureTap, engine.GestureDoubleTap, engine.GestureTripleTap, engine.GestureQuadTap}
	type tc struct {
		g  engine.Gesture
		a  *profile.Action
		ok bool
	}
	var cases []tc
	for _, g := range momentary {
		cases = append(cases,
			tc{g, keysAction(profile.ModeTap, nil, nil), true},
			tc{g, keysAction(profile.ModeTap, seq, nil), true},
			tc{g, layer("toggle"), true},
			tc{g, keysAction(profile.ModeHold, nil, nil), false},
			tc{g, keysAction(profile.ModeTap, nil, &profile.Repeat{}), false},
			tc{g, keysAction(profile.ModeHold, nil, &profile.Repeat{}), false},
			tc{g, keysAction(profile.ModeHold, seq, nil), false},
			tc{g, layer("hold"), false},
		)
	}
	for _, g := range []engine.Gesture{engine.GesturePress, engine.GestureHold} {
		cases = append(cases,
			tc{g, keysAction(profile.ModeHold, nil, nil), true},
			tc{g, keysAction(profile.ModeHold, nil, &profile.Repeat{DelayMs: 10, RateHz: 5}), true},
			tc{g, keysAction(profile.ModeTap, nil, nil), true},
			tc{g, keysAction(profile.ModeTap, seq, nil), true},
			tc{g, layer("hold"), true},
			tc{g, keysAction(profile.ModeHold, seq, nil), false},
			tc{g, keysAction(profile.ModeHold, seq[:1], nil), false},
			tc{g, keysAction(profile.ModeTap, seq, &profile.Repeat{}), false},
			tc{g, keysAction(profile.ModeHold, seq, &profile.Repeat{}), false},
			tc{g, keysAction(profile.ModeTap, nil, &profile.Repeat{}), true},
		)
	}
	for _, c := range cases {
		b := &profile.Binding{ID: "/b", Control: control.West}
		setAction(b, c.g, c.a)
		_, err := engine.Compile(&profile.Profile{Layers: []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{b}}}})
		if (err == nil) != c.ok {
			t.Errorf("%s %s (repeat %t): Compile error %v; want ok %t", c.g, c.a, c.a.Repeat != nil, err, c.ok)
		}
	}
}

func TestM2RepeatStopsAndHonorsGap(t *testing.T) {
	p := compile(t, `"east":{"press":{"type":"keys","keys":"A","mode":"hold","repeat":{"delayMs":10,"rateHz":1000}},"timing":{"tapGapMs":20}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+east@0 wait@10 wait@30 wait@50 -east@60 wait@500")
	if got, want := r.String(), "@0 down decl/1 A, @10 repeat decl/1 A, @50 repeat decl/1 A, @60 up decl/1 A"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
}

func TestM2LatchClearsOnCancel(t *testing.T) {
	p := compile(t, `"east":{"latch":true,"press":{"type":"keys","keys":"Shift"}}`)
	r := &recorder{}
	e := engine.New(p, r, engine.Options{})
	runM2(t, e, r, "+east@0 -east@1 cancel@2 wait@500")
	if got, want := r.String(), "@0 down decl/1 Shift, @2 up decl/1 Shift"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
	if d, ok := e.NextDeadline(); ok {
		t.Fatalf("deadline %v after cancel", d)
	}
}

func TestM2TimingScopesAndExplicitZero(t *testing.T) {
	src := `{"schemaVersion":0,"name":"timing","timing":{"holdMs":7,"multiTapMs":9,"tapPressMs":3,"tapGapMs":4},"layers":{"base":{"west":{"timing":{"holdMs":11,"multiTapMs":13,"tapPressMs":5,"tapGapMs":6},"tap":{"type":"keys","keys":"B"},"hold":{"type":"keys","keys":"E"}},"east":{"timing":{"holdMs":0,"multiTapMs":0,"tapPressMs":0,"tapGapMs":0},"tap":{"type":"keys","keys":"A"},"hold":{"type":"keys","keys":"D"}}}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil || ds.HasErrors() {
		t.Fatalf("Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatal(err)
	}
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{})
	runM2(t, e, r, "+west@0 wait@10 wait@11 -west@12 +east@20 wait@20 -east@21")
	if got, want := r.String(), "@11 down decl/1 E 5ms, @12 up decl/1 E, @20 down decl/2 D, @21 up decl/2 D"; got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
	bs := prog.Bindings()
	if len(bs) != 2 || bs[0].AddedLatency != 0 || bs[1].AddedLatency != 11*time.Millisecond {
		t.Fatalf("binding latencies = %+v; want east 0 and west 11ms", bs)
	}
}
