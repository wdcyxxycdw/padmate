package input_test

import (
	"bytes"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// These tests need no SDL: the watch only sees Events.

func TestDefaultCombo(t *testing.T) {
	for _, tc := range []struct {
		typ      string
		vertical bool
		buttons  []string
	}{
		{"xboxone", false, []string{"back", "start"}},
		{"xboxone", true, []string{"back", "start"}},
		{"ps5", false, []string{"back", "start"}},
		{"unknown", false, []string{"back", "start"}},
		{"", true, []string{"back", "start"}},
		{"joyconpair", false, []string{"back", "start"}},
		{"joyconpair", true, []string{"back", "start"}},
		// SL+SR: ARCHITECTURE §7.1.
		{"joyconleft", false, []string{"leftshoulder", "rightshoulder"}},
		{"joyconright", false, []string{"leftshoulder", "rightshoulder"}},
		{"joyconleft", true, []string{"paddle2", "paddle4"}},
		{"joyconright", true, []string{"paddle3", "paddle1"}},
	} {
		c := input.DefaultCombo(input.DeviceInfo{Type: tc.typ}, tc.vertical)
		if !slices.Equal(c.Buttons, tc.buttons) || c.Hold != input.ComboHold {
			t.Errorf("DefaultCombo(%q, vertical %v) = %+v, want %q for %v", tc.typ, tc.vertical, c, tc.buttons, input.ComboHold)
		}
	}
	if input.ComboHold != 2*time.Second {
		t.Errorf("ComboHold = %v, want 2s", input.ComboHold)
	}
}

// TestMayHaveButton: only a gamepad whose controls are known can lack a
// button; a name that is not a button is lacked by every gamepad.
func TestMayHaveButton(t *testing.T) {
	const back, paddle1 = 1 << 4, 1 << 16 // SDL_GamepadButton values
	for _, tc := range []struct {
		name   string
		info   input.DeviceInfo
		button string
		want   bool
	}{
		{"unknown controls", input.DeviceInfo{}, "paddle1", true},
		{"unknown controls, not a button", input.DeviceInfo{}, "lefttrigger", false},
		{"known, has it", input.DeviceInfo{ButtonMask: back | paddle1, Capabilities: input.Capabilities{Known: true}}, "paddle1", true},
		{"known, lacks it", input.DeviceInfo{ButtonMask: back, Capabilities: input.Capabilities{Known: true}}, "paddle1", false},
		{"legacy mask, lacks it", input.DeviceInfo{ButtonMask: back}, "paddle1", false},
		{"known, no buttons", input.DeviceInfo{Capabilities: input.Capabilities{Known: true}}, "back", false},
	} {
		if got := tc.info.MayHaveButton(tc.button); got != tc.want {
			t.Errorf("%s: MayHaveButton(%q) = %v, want %v", tc.name, tc.button, got, tc.want)
		}
	}
}

// comboOp is one step of a watch script (parseComboScript).
type comboOp struct {
	ev    *input.Event
	due   *int64 // check Due: -1 for none, else the time in ms
	fire  int64  // call Fire at this time in ms, when ev and due are nil
	fired []string
}

// parseComboScript reads a watch script: tokens separated by spaces, each
// a device event, a Due check or a Fire call, with times in ms. Device
// events are "[D]+button@t" and "[D]-button@t", "[D]add@t" (an xboxone),
// "[D]add:type@t" and "[D]rm@t", where D is the device, 1 when left out.
// "due@t" checks that Due gives t, "due-" that it gives none.
// "fire@t>1@2000,2@2100" calls Fire(t) and checks the combos it fires, as
// device@due; "fire@t" checks that it fires none.
func parseComboScript(t *testing.T, script string) []comboOp {
	t.Helper()
	var ops []comboOp
	for _, tok := range strings.Fields(script) {
		switch {
		case tok == "due-":
			none := int64(-1)
			ops = append(ops, comboOp{due: &none})
		case strings.HasPrefix(tok, "due@"):
			ms := parseMS(t, tok, strings.TrimPrefix(tok, "due@"))
			ops = append(ops, comboOp{due: &ms})
		case strings.HasPrefix(tok, "fire@"):
			at, fired, _ := strings.Cut(strings.TrimPrefix(tok, "fire@"), ">")
			op := comboOp{fire: parseMS(t, tok, at)}
			if fired != "" {
				op.fired = strings.Split(fired, ",")
			}
			ops = append(ops, op)
		default:
			e := parseComboEvent(t, tok)
			ops = append(ops, comboOp{ev: &e})
		}
	}
	return ops
}

// parseComboEvents reads a script of device events only.
func parseComboEvents(t *testing.T, script string) []input.Event {
	t.Helper()
	var evs []input.Event
	for _, tok := range strings.Fields(script) {
		evs = append(evs, parseComboEvent(t, tok))
	}
	return evs
}

func parseComboEvent(t *testing.T, tok string) input.Event {
	t.Helper()
	body, at, ok := strings.Cut(tok, "@")
	if !ok {
		t.Fatalf("token %q: no @time", tok)
	}
	e := input.Event{Time: uint64(parseMS(t, tok, at)) * uint64(time.Millisecond), Device: 1}
	if i := strings.IndexFunc(body, func(r rune) bool { return r < '0' || r > '9' }); i > 0 {
		d, err := strconv.ParseUint(body[:i], 10, 32)
		if err != nil {
			t.Fatalf("token %q: %v", tok, err)
		}
		e.Device, body = input.DeviceID(d), body[i:]
	}
	switch {
	case strings.HasPrefix(body, "+"):
		e.Kind, e.Control = input.ButtonDown, body[1:]
	case strings.HasPrefix(body, "-"):
		e.Kind, e.Control = input.ButtonUp, body[1:]
	case body == "rm":
		e.Kind = input.DeviceRemoved
	case body == "add" || strings.HasPrefix(body, "add:"):
		typ := strings.TrimPrefix(strings.TrimPrefix(body, "add"), ":")
		if typ == "" {
			typ = "xboxone"
		}
		e.Kind, e.Info = input.DeviceAdded, &input.DeviceInfo{ID: e.Device, Type: typ, IsGamepad: true}
	default:
		t.Fatalf("token %q: unknown event", tok)
	}
	return e
}

func parseMS(t *testing.T, tok, s string) int64 {
	t.Helper()
	ms, err := strconv.ParseInt(s, 10, 64)
	if err != nil || ms < 0 {
		t.Fatalf("token %q: bad time %q", tok, s)
	}
	return ms
}

// formatCombo renders a ComboHeld as device@ms.
func formatCombo(t *testing.T, e input.Event) string {
	t.Helper()
	if e.Kind != input.ComboHeld || e.Control != "" || e.Info != nil || e.Value != 0 {
		t.Errorf("fired %+v, want a bare ComboHeld", e)
	}
	return fmt.Sprintf("%d@%s", e.Device, strconv.FormatFloat(float64(e.Time)/float64(time.Millisecond), 'f', -1, 64))
}

// horizontal is the pick of replay: the default combo, horizontal grip.
func horizontal(info input.DeviceInfo) input.Combo { return input.DefaultCombo(info, false) }

// TestComboWatch drives a watch with the default combos and the hold of
// 2 s through scripts (parseComboScript).
func TestComboWatch(t *testing.T) {
	for _, tc := range []struct{ name, script string }{
		{"start first", "add@0 +start@0 +back@100 due@2100 fire@2099 fire@2100>1@2100 due-"},
		{"back first", "add@0 +back@0 +start@50 due@2050 fire@2050>1@2050"},
		{"one member", "add@0 +back@0 due- fire@5000 +south@100 due- fire@5000"},
		{"released before the hold", "add@0 +back@0 +start@0 due@2000 -back@1999 due- fire@5000"},
		{"released at once", "add@0 +back@0 +start@0 -start@0 due- fire@5000"},
		{"a 6 s hold fires once", "add@0 +back@0 +start@0 fire@2000>1@2000 due- fire@4000 fire@6000 -back@6000 -start@6000 fire@9000"},
		{"a late Fire gives the due time", "add@0 +back@0 +start@10 fire@60000>1@2010 fire@60000"},
		{"re-pressing a member re-arms", "add@0 +back@0 +start@0 fire@2000>1@2000 -start@2500 due- +start@3000 due@5000 fire@5000>1@5000"},
		{"releasing both re-arms", "add@0 +back@0 +start@0 fire@2000>1@2000 -start@2500 -back@2600 +start@3000 +back@3100 fire@5100>1@5100"},
		{"a repeated down keeps the hold", "add@0 +back@0 +start@0 +start@1000 +back@1500 due@2000 fire@2000>1@2000 +back@2500 due-"},
		{"an orphan up of a member", "add@0 -back@0 +back@10 +start@20 fire@2020>1@2020"},
		{"other buttons held", "add@0 +south@0 +back@10 +east@20 +start@30 -east@40 +west@50 -south@60 fire@2030>1@2030"},
		{"other buttons while latched", "add@0 +back@0 +start@0 fire@2000>1@2000 +south@3000 -south@3100 due- fire@9000"},
		{"two devices", "add@0 2add@0 +back@0 2+back@100 +start@200 2+start@300 due@2200 -back@500 due@2300 fire@2299 fire@2300>2@2300 due-"},
		{"the other device's members", "add@0 2add@0 +back@0 2+start@0 due- fire@5000"},
		{"in due order", "add@0 2add@0 2+back@0 +back@0 +start@100 2+start@50 due@2050 fire@5000>2@2050,1@2100"},
		{"equal due times by device", "3add@0 add@0 3+back@0 3+start@0 +back@0 +start@0 fire@2000>1@2000,3@2000"},
		{"removal disarms", "add@0 +back@0 +start@0 rm@1000 due- fire@3000"},
		{"removal of the other device", "add@0 2add@0 +back@0 +start@0 2rm@1000 due@2000 fire@2000>1@2000"},
		{"a removal while latched", "add@0 +back@0 +start@0 fire@2000>1@2000 rm@3000 fire@9000"},
		{"a new device with the old id", "add@0 +back@0 +start@0 rm@1000 add@1500 +back@1600 due- +start@1700 due@3700"},
		// A device without its added event, as in a hand-written trace, gets
		// the combo of an unknown type.
		{"no added event", "+back@0 +start@0 fire@2000>1@2000"},
		// The Info of the added event picks the combo: a single Joy-Con
		// uses SL+SR, which it reports as the shoulders when held sideways.
		{"Joy-Con", "add:joyconleft@0 +back@0 +start@0 due- fire@3000 +leftshoulder@3000 +rightshoulder@3100 fire@5100>1@5100"},
		{"Joy-Con R", "add:joyconright@0 +rightshoulder@0 +leftshoulder@0 fire@2000>1@2000"},
		{"Joy-Con pair", "add:joyconpair@0 +leftshoulder@0 +rightshoulder@0 due- +back@0 +start@0 fire@2000>1@2000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := input.NewComboWatch(horizontal)
			runComboScript(t, w, tc.script)
		})
	}
}

// runComboScript runs a script on w.
func runComboScript(t *testing.T, w *input.ComboWatch, script string) {
	t.Helper()
	for i, op := range parseComboScript(t, script) {
		switch {
		case op.ev != nil:
			w.Observe(*op.ev)
		case op.due != nil:
			due, ok := w.Due()
			if *op.due < 0 {
				if ok {
					t.Errorf("op %d: Due() = %v, true; want none", i, due)
				}
			} else if want := uint64(*op.due) * uint64(time.Millisecond); !ok || due != want {
				t.Errorf("op %d: Due() = %v, %v; want %v, true", i, due, ok, want)
			}
		default:
			var got []string
			for _, e := range w.Fire(uint64(op.fire) * uint64(time.Millisecond)) {
				got = append(got, formatCombo(t, e))
			}
			if !slices.Equal(got, op.fired) {
				t.Errorf("op %d: Fire(%dms) fired %q, want %q", i, op.fire, got, op.fired)
			}
		}
	}
}

// TestComboWatchPick checks what the watch does with the combos that pick
// returns.
func TestComboWatchPick(t *testing.T) {
	var infos []input.DeviceInfo
	record := func(c input.Combo) func(input.DeviceInfo) input.Combo {
		return func(info input.DeviceInfo) input.Combo {
			infos = append(infos, info)
			return c
		}
	}
	for _, tc := range []struct {
		name   string
		combo  input.Combo
		script string
	}{
		{"one button, no hold", input.Combo{Buttons: []string{"south"}}, "add@0 +south@10 due@10 fire@10>1@10"},
		{"a negative hold", input.Combo{Buttons: []string{"south"}, Hold: -time.Second}, "add@0 +south@10 fire@10>1@10"},
		{"three buttons", input.Combo{Buttons: []string{"south", "east", "dpad.up"}, Hold: time.Second}, "add@0 +south@0 +east@0 due- +dpad.up@5 fire@1005>1@1005"},
		{"a repeated member", input.Combo{Buttons: []string{"south", "south"}, Hold: time.Second}, "add@0 +south@0 fire@1000>1@1000"},
		{"no buttons", input.Combo{Hold: time.Second}, "add@0 +south@0 +back@0 +start@0 due- fire@9000"},
		{"an unknown button", input.Combo{Buttons: []string{"south", "select"}, Hold: time.Second}, "add@0 +south@0 due- fire@9000"},
		{"an axis", input.Combo{Buttons: []string{"south", "lefttrigger"}, Hold: time.Second}, "add@0 +south@0 due- fire@9000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runComboScript(t, input.NewComboWatch(record(tc.combo)), tc.script)
		})
	}

	// pick gets the Info of the added event, or only the id of a device
	// seen without one; a new added event of the id picks again.
	infos = nil
	w := input.NewComboWatch(record(input.Combo{Buttons: []string{"south"}}))
	pad := &input.DeviceInfo{ID: 1, Name: "pad", Type: "ps5", IsGamepad: true}
	for _, e := range []input.Event{
		{Device: 1, Kind: input.DeviceAdded, Info: pad},
		{Device: 1, Kind: input.ButtonDown, Control: "east"},
		{Device: 2, Kind: input.ButtonDown, Control: "east"},
		{Device: 2, Kind: input.ButtonUp, Control: "east"},
		{Device: 3, Kind: input.DeviceAdded},
		{Device: 1, Kind: input.DeviceAdded, Info: pad},
		{Device: 4, Kind: input.AxisMotion, Control: "leftx", Value: 5},
		{Device: 5, Kind: input.DeviceRemoved},
	} {
		w.Observe(e)
	}
	want := []input.DeviceInfo{*pad, {ID: 2}, {ID: 3}, *pad}
	if !slices.Equal(infos, want) {
		t.Errorf("pick got %+v, want %+v", infos, want)
	}
}

// TestComboWatchRepick checks that Repick gives every device the combo
// pick gives it now, with the Info it had, and that a hold under way
// fires neither combo until the new members are pressed again (UI-19).
func TestComboWatchRepick(t *testing.T) {
	standard := input.Combo{Buttons: []string{"back", "start"}, Hold: time.Second}
	joycon := input.Combo{Buttons: []string{"leftshoulder", "rightshoulder"}, Hold: time.Second}
	var infos []input.DeviceInfo
	pick := func(info input.DeviceInfo) input.Combo {
		infos = append(infos, info)
		if info.Type == "joyconleft" {
			return joycon
		}
		return standard
	}
	w := input.NewComboWatch(pick)
	// 1 holds the old combo, armed; 2 (a Joy-Con) holds one member; 3 has
	// no added event.
	runComboScript(t, w, "add@0 2add:joyconleft@0 +back@0 +start@100 due@1100 2+leftshoulder@200 3+west@300")
	standard = input.Combo{Buttons: []string{"west", "east"}, Hold: 2 * time.Second}
	joycon = input.Combo{Buttons: []string{"leftshoulder", "south"}, Hold: 500 * time.Millisecond}
	infos = nil
	w.Repick()
	want := []input.DeviceInfo{{ID: 1, Type: "xboxone", IsGamepad: true}, {ID: 2, Type: "joyconleft", IsGamepad: true}, {ID: 3}}
	slices.SortFunc(infos, func(a, b input.DeviceInfo) int { return int(a.ID) - int(b.ID) })
	if !slices.Equal(infos, want) {
		t.Fatalf("Repick picked for %+v, want %+v", infos, want)
	}
	runComboScript(t, w, strings.Join([]string{
		// The armed old combo of 1 was disarmed and never fires.
		"due- fire@9000",
		// The new members pressed after Repick arm it.
		"+west@1200 +east@1300 due@3300",
		// 2 held leftshoulder before Repick: pressing south arms nothing
		// until leftshoulder is pressed again.
		"2+south@1400 due@3300",
		"2-leftshoulder@1500 2+leftshoulder@1600 due@2100",
		// 3 held west, then no member, before Repick.
		"3+east@1700 due@2100",
		"3-west@1800 3+west@1900 due@2100",
		"fire@2100>2@2100 due@3300",
		"fire@4000>1@3300,3@3900 due-",
		// The old members are no members now.
		"-back@4100 +back@4200 -start@4300 +start@4400 due- fire@9000",
	}, " "))
}

// TestExpandCombos checks the ComboHeld events that ExpandCombos inserts,
// each at its due time.
func TestExpandCombos(t *testing.T) {
	for _, tc := range []struct {
		name, in string
		want     []string // "@ms" marks where a ComboHeld of device 1 goes
	}{
		{"none", "add@0 +back@0 +start@0 -back@1999 -start@2500", nil},
		{"inserted", "add@0 +back@0 +start@100 +south@500 -back@3000 -start@3000", []string{"", "", "", "", "@2100"}},
		// A combo due at an event's time fires before the event.
		{"due at the next event", "add@0 +back@0 +start@0 -back@2000", []string{"", "", "", "@2000"}},
		{"due at the next event of another device", "add@0 2add@0 +back@0 +start@0 2+south@2000", []string{"", "", "", "", "@2000"}},
		// A combo still held after the last event fires at the end.
		{"at the end", "add@0 +back@0 +start@0 +south@500", []string{"", "", "", "", "@2000"}},
		{"twice", "add@0 +back@0 +start@0 -start@2500 +start@3000 -back@6000", []string{"", "", "", "@2000", "", "@5000"}},
		{"removed", "add@0 +back@0 +start@0 rm@1999", nil},
		{"Joy-Con, horizontal", "add:joyconleft@0 +leftshoulder@0 +rightshoulder@0", []string{"", "", "", "@2000"}},
		{"Joy-Con, not back+start", "add:joyconright@0 +back@0 +start@0 -back@3000", nil},
		{"no events", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := parseComboEvents(t, tc.in)
			orig := slices.Clone(in)
			var want []input.Event
			for i, e := range in {
				if i < len(tc.want) && tc.want[i] != "" {
					want = append(want, comboAt(t, tc.want[i]))
				}
				want = append(want, e)
			}
			if len(tc.want) > len(in) {
				want = append(want, comboAt(t, tc.want[len(in)]))
			}
			got := input.ExpandCombos(in, horizontal)
			if !slices.EqualFunc(got, want, eventEqual) {
				t.Errorf("ExpandCombos:\n got %v\nwant %v", got, want)
			}
			if !slices.EqualFunc(in, orig, eventEqual) {
				t.Errorf("ExpandCombos changed its input to %v", in)
			}
		})
	}
}

// comboAt returns a ComboHeld of device 1 at "@ms".
func comboAt(t *testing.T, at string) input.Event {
	t.Helper()
	ms := parseMS(t, at, strings.TrimPrefix(at, "@"))
	return input.Event{Kind: input.ComboHeld, Device: 1, Time: uint64(ms) * uint64(time.Millisecond)}
}

func eventEqual(a, b input.Event) bool {
	if (a.Info == nil) != (b.Info == nil) || a.Info != nil && *a.Info != *b.Info {
		return false
	}
	a.Info, b.Info = nil, nil
	return a == b
}

// TestComboNotInTraces: ComboHeld is an event of a Source only; a trace
// cannot carry it, in either direction.
func TestComboNotInTraces(t *testing.T) {
	if got := input.ComboHeld.String(); got != "combo" {
		t.Errorf("ComboHeld.String() = %q, want combo", got)
	}
	for _, k := range []input.EventKind{input.ComboHeld, 0, input.ComboHeld + 1, input.DevicePower, input.DevicePower + 1} {
		var b bytes.Buffer
		err := input.WriteEvent(&b, input.Event{Time: 5, Device: 1, Kind: k})
		if want := fmt.Sprintf("input: event kind %s is not written to traces", k); err == nil || err.Error() != want {
			t.Errorf("WriteEvent(%s) = %v, want %q", k, err, want)
		}
		if b.Len() != 0 {
			t.Errorf("WriteEvent(%s) wrote %q", k, b.String())
		}
	}
	const line = `{"timeNs":0,"kind":"combo","device":1}`
	evs, err := input.ReadEvents(strings.NewReader(line))
	if want := `line 1: unknown kind "combo"; want added, removed, down, up or axis`; err == nil || err.Error() != want || evs != nil {
		t.Errorf("ReadEvents(%s) = %v, %v; want the error %q", line, evs, err, want)
	}
	found := false
	for _, bl := range badLines {
		found = found || bl.text == line
	}
	if !found {
		t.Errorf("badLines, the seeds of FuzzReadEvents, lack %s", line)
	}
}

// TestComboWatchForgetsHeldMembersOnRemap: SDL does not release what is held
// when a mapping changes (IN-18), so back held before the remap may never be
// released: the hold of the combo must start again with both pressed anew.
func TestComboWatchForgetsHeldMembersOnRemap(t *testing.T) {
	w := input.NewComboWatch(func(info input.DeviceInfo) input.Combo { return input.DefaultCombo(info, false) })
	const dev = input.DeviceID(7)
	info := &input.DeviceInfo{ID: dev, Name: "pad", IsGamepad: true}
	w.Observe(input.Event{Time: 0, Device: dev, Kind: input.DeviceAdded, Info: info})
	w.Observe(input.Event{Time: 1, Device: dev, Kind: input.ButtonDown, Control: "back"})
	w.Observe(input.Event{Time: 2, Device: dev, Kind: input.DeviceRemapped, Info: info})
	w.Observe(input.Event{Time: 3, Device: dev, Kind: input.ButtonDown, Control: "start"})
	if _, armed := w.Due(); armed {
		t.Error("the combo is armed with back held from before the remap")
	}
	w.Observe(input.Event{Time: 4, Device: dev, Kind: input.ButtonDown, Control: "back"})
	if _, armed := w.Due(); !armed {
		t.Error("the combo does not arm once both are pressed after the remap")
	}
}
