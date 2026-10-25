package amgp

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// Tests for the review of the importer: each one builds a small file and
// checks that what AntiMicroX does is either carried over or reported.

const (
	heldTo2 = "<setselect>2</setselect><setselectcondition>while-held</setselectcondition>"
	heldTo3 = "<setselect>3</setselect><setselectcondition>while-held</setselectcondition>"
	heldTo4 = "<setselect>4</setselect><setselectcondition>while-held</setselectcondition>"
)

// linesOf returns the report lines with code.
func linesOf(report []ReportLine, code string) []ReportLine {
	var out []ReportLine
	for _, l := range report {
		if l.Code == code {
			out = append(out, l)
		}
	}
	return out
}

// The sticks' rings are controls like any other: a set that AntiMicroX would
// have replaced completely must not let the ring bindings below it fire.
func TestHeldLayerBlocksTheStickRings(t *testing.T) {
	ring := func(extra string) string {
		return `<stick index="1"><modifierZone>16000</modifierZone>` + extra + `<stickmodifierbutton><slots>` + xmlSlot("keyboard", 0x1000020) + `</slots></stickmodifierbutton></stick>`
	}
	doc := func(extra string) string {
		return xmlDoc("",
			xmlSet(1, "", ring(extra), xmlButton(10, heldTo2), xmlButton(5, heldTo3), xmlButton(7, heldTo4)),
			xmlSet(2, "Held", xmlButton(2, "", xmlSlot("keyboard", 'B'))),
			xmlSet(3, "OnView", xmlButton(2, "", xmlSlot("keyboard", 'C'))),
			xmlSet(4, "OnMenu", xmlButton(2, "", xmlSlot("keyboard", 'D'))))
	}
	for _, tc := range []struct{ name, extra, bound, free string }{
		{"outer", "", "ls.outer", "ls.inner"},
		{"inner", "<modifierZoneInverted>1</modifierZoneInverted>", "ls.inner", "ls.outer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, tree, _ := imported(t, doc(tc.extra))
			if !has(tree, "layers", "base", tc.bound, "press") {
				t.Fatalf("base: %v", at(t, tree, "layers", "base"))
			}
			for _, where := range [][]string{{"layers", "Held"}, {"functionKeys", "back"}, {"functionKeys", "start"}} {
				if !has(tree, append(where, tc.bound, "block")...) {
					t.Errorf("%v does not block %s: %v", where, tc.bound, at(t, tree, where...))
				}
				// A ring that nothing binds has nothing to block, and neither
				// has the right stick, whose rings are not bound at all.
				for _, c := range []string{tc.free, "rs.inner", "rs.outer"} {
					if has(tree, append(where, c)...) {
						t.Errorf("%v blocks %s that nothing binds", where, c)
					}
				}
			}
		})
	}
	// A ring bound only in a layer is still one a held set has to block.
	only := xmlDoc("",
		xmlSet(1, "", xmlButton(10, heldTo2)),
		xmlSet(2, "Held", xmlButton(2, "", xmlSlot("keyboard", 'B'))),
		xmlSet(3, "Other", ring("")))
	if _, tree, _ := imported(t, only); !has(tree, "layers", "Held", "ls.outer", "block") {
		t.Errorf("held: %v", at(t, tree, "layers", "Held"))
	}
}

// AntiMicroX writes the way back itself when a button holds a set; for a
// function key that is not a binding that is lost.
func TestFunctionKeyBackLinkIsNotAnError(t *testing.T) {
	doc := xmlDoc("",
		xmlSet(1, "", xmlButton(5, heldTo2)),
		xmlSet(2, "On view",
			xmlButton(1, "", xmlSlot("keyboard", 'A')),
			// What AntiMicroX adds: back to set 1 while held, no slots.
			xmlButton(5, "<setselect>1</setselect><setselectcondition>while-held</setselectcondition>"),
			// A real binding on the function key still is lost.
			xmlButton(7, "", xmlSlot("keyboard", 'Z'))))
	_, _, report := imported(t, doc)
	lost := linesOf(report, "function.key")
	var dropped []ReportLine
	for _, l := range lost {
		if l.Severity == SeverityDropped {
			dropped = append(dropped, l)
		}
	}
	if len(dropped) != 1 || dropped[0].Set != 2 || dropped[0].Control != "start" {
		t.Errorf("dropped lines: %v", dropped)
	}
	if len(lost) != 2 {
		t.Errorf("want the line for the working key and the one for Menu, got %v", lost)
	}
	// A way back that carries a key, or goes somewhere else, is a binding.
	for name, button := range map[string]string{
		"with a key":     xmlButton(5, "<setselect>1</setselect><setselectcondition>while-held</setselectcondition>", xmlSlot("keyboard", 'Q')),
		"to another set": xmlButton(5, heldTo3),
	} {
		_, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(5, heldTo2)), xmlSet(2, "On view", button), xmlSet(3, "")))
		found := false
		for _, l := range linesOf(report, "function.key") {
			found = found || l.Severity == SeverityDropped && l.Set == 2
		}
		if !found {
			t.Errorf("%s: no dropped line in %v", name, report)
		}
	}
	// Nor is it a way back when set 1 does not hold this set with that key.
	stray := xmlDoc("", xmlSet(1, "", xmlButton(5, heldTo2)), xmlSet(2, ""),
		xmlSet(3, "", xmlButton(5, "<setselect>1</setselect><setselectcondition>while-held</setselectcondition>")))
	_, _, report = imported(t, stray)
	found := false
	for _, l := range linesOf(report, "function.key") {
		found = found || l.Severity == SeverityDropped && l.Set == 3
	}
	if !found {
		t.Errorf("a stray way back is not reported: %v", report)
	}
}

// The time bound that an empty release zone puts on a tap has no counterpart,
// and the report says so.
func TestReleaseBandEndIsReported(t *testing.T) {
	multi := "<cycleresetactive>true</cycleresetactive><cycleresetinterval>500</cycleresetinterval>"
	for _, tc := range []struct {
		name     string
		settings string
		slots    []string
		want     int // milliseconds in the line, 0 for no line
	}{
		{"trailing zone", "", []string{xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 490)}, 500},
		{"empty hold zone", "", []string{xmlSlot("hold", 500), xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 490)}, 500},
		{"empty hold zone only", "", []string{xmlSlot("hold", 500), xmlSlot("release", 10), xmlSlot("keyboard", 'A')}, 500},
		{"double tap", multi, []string{xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 490), xmlSlot("cycle", 0), xmlSlot("release", 10), xmlSlot("keyboard", 'B'), xmlSlot("release", 490)}, 500},
		{"no bound", "", []string{xmlSlot("release", 10), xmlSlot("keyboard", 'A')}, 0},
		{"the hold is the bound", "", []string{xmlSlot("hold", 400), xmlSlot("keyboard", 'K'), xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 390)}, 0},
		{"the hold is later", "", []string{xmlSlot("hold", 800), xmlSlot("keyboard", 'K'), xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 490)}, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, tc.settings, tc.slots...))))
			lines := linesOf(report, "zone.release.bound")
			if tc.want == 0 {
				if len(lines) != 0 {
					t.Errorf("unexpected %v", lines)
				}
				return
			}
			if len(lines) == 0 || lines[0].Severity != SeverityApproximate || !strings.Contains(lines[0].Message, fmt.Sprint(tc.want)) || lines[0].Slot == 0 {
				t.Errorf("got %v", report)
			}
		})
	}
}

// A key of the press zone is held in AntiMicroX for as long as the button is
// down; the tap that a multi-tap partition becomes does not hold it.
func TestMultiTapPressKeysAreReported(t *testing.T) {
	settings := "<cycleresetactive>true</cycleresetactive><cycleresetinterval>500</cycleresetinterval>"
	p, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, settings,
		xmlSlot("keyboard", 0x1000020), xmlSlot("cycle", 0), xmlSlot("keyboard", 0x1000021)))))
	b := p.Layers[0].Bindings[0]
	if b.Tap == nil || b.DoubleTap == nil {
		t.Fatalf("binding %+v", b)
	}
	lines := linesOf(report, "multitap.held")
	if len(lines) != 1 || lines[0].Severity != SeverityApproximate || lines[0].Slot != 1 {
		t.Errorf("want one line for the binding, got %v", lines)
	}
	// A partition that fires on the release, and one that is a sequence, has
	// nothing held to lose.
	_, _, report = imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, settings,
		xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("cycle", 0),
		xmlSlot("keyboard", 'B'), xmlSlot("pause", 50), xmlSlot("keyboard", 'C')))))
	if lines := linesOf(report, "multitap.held"); len(lines) != 0 {
		t.Errorf("unexpected %v", lines)
	}
	// Alone on a button it is held, and says nothing.
	_, _, report = imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 0x1000020)))))
	if lines := linesOf(report, "multitap.held"); len(lines) != 0 {
		t.Errorf("unexpected %v", lines)
	}
}

// A setting of the mouse that has no counterpart is reported, on the stick that
// is imported as a mouse stick.
func TestMouseSettingsWithoutCounterpartAreReported(t *testing.T) {
	dir := func(i, code int, extra string) string {
		return fmt.Sprintf(`<stickbutton index="%d">%s<slots>%s</slots></stickbutton>`, i, extra, xmlSlot("mousemovement", code))
	}
	stick := func(extra string) string {
		return xmlDoc("", xmlSet(1, "", `<stick index="1">`+dir(1, 1, extra)+dir(5, 2, "")+dir(7, 3, "")+dir(3, 4, extra)+`</stick>`))
	}
	p, _, report := imported(t, stick("<extraacceleration>true</extraacceleration><mousesensitivity>3.5</mousesensitivity><easingduration>0.5</easingduration>"))
	if p.Analog.LS.Mode != "mouse" {
		t.Fatalf("ls %+v", p.Analog.LS)
	}
	lines := linesOf(report, "mouse.setting")
	if len(lines) != 3 {
		t.Fatalf("want a line for each setting, once, got %v", report)
	}
	for _, l := range lines {
		if l.Severity != SeverityDropped || l.Set != 1 || l.Control != "ls" {
			t.Errorf("line %+v", l)
		}
	}
	for _, word := range []string{"extraacceleration", "mousesensitivity", "easingduration"} {
		found := false
		for _, l := range lines {
			found = found || strings.Contains(l.Message, word)
		}
		if !found {
			t.Errorf("no line names %s: %v", word, lines)
		}
	}
	// The curve is read, and is not one of them; a stick with none of them
	// has no such line.
	p, _, report = imported(t, stick("<mouseacceleration>cubic</mouseacceleration>"))
	if p.Analog.LS.Mode != "mouse" || len(linesOf(report, "mouse.setting")) != 0 {
		t.Errorf("report %v", report)
	}
}

// A pause that fills the sequence leaves nothing of the zone, which the
// report has to say.
func TestSequenceTooLongToHoldAnythingIsReported(t *testing.T) {
	for _, tc := range []struct {
		name  string
		slots []string
		line  bool
	}{
		{"waits only, then a key", []string{xmlSlot("pause", 700000), xmlSlot("keyboard", 'A')}, true},
		{"a key, then a long wait, then a key", []string{xmlSlot("keyboard", 'A'), xmlSlot("pause", 700000), xmlSlot("keyboard", 'B')}, true},
		{"a trailing wait is dropped anyway", []string{xmlSlot("keyboard", 'A'), xmlSlot("pause", 20000)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", tc.slots...))))
			if got := len(linesOf(report, "sequence.long")) > 0; got != tc.line {
				t.Errorf("sequence.long: got %v, want %v: %v", got, tc.line, report)
			}
		})
	}
}

// Whatever the interval, a turbo must not make a profile that holds a key for
// longer than a key press time can be (10 seconds).
func TestTurboIntervalIsBounded(t *testing.T) {
	for _, interval := range []int{20001, 100000, 1 << 30, 1<<31 - 1} {
		doc := xmlDoc("", xmlSet(1, "", xmlButton(1, fmt.Sprintf("<useturbo>true</useturbo><turbointerval>%d</turbointerval>", interval), xmlSlot("keyboard", 'A'))))
		p, _, report := imported(t, doc)
		b := p.Layers[0].Bindings[0]
		if b.Press == nil || b.Press.Repeat == nil {
			t.Fatalf("interval %d: %+v", interval, b)
		}
		if !timingOf(b).TapPressSet || timingOf(b).TapPressMs != 10000 || !timingOf(b).TapGapSet || timingOf(b).TapGapMs != 10000 {
			t.Errorf("interval %d: timing %+v", interval, timingOf(b))
		}
		if lines := linesOf(report, "turbo.long"); len(lines) != 1 || lines[0].Severity != SeverityApproximate {
			t.Errorf("interval %d: report %v", interval, report)
		}
	}
	// The longest interval that is exact.
	p, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "<useturbo>true</useturbo><turbointerval>20000</turbointerval>", xmlSlot("keyboard", 'A')))))
	if tm := timingOf(p.Layers[0].Bindings[0]); tm.TapPressMs != 10000 || tm.TapGapMs != 10000 {
		t.Errorf("timing %+v", tm)
	}
	if len(linesOf(report, "turbo.long")) != 0 {
		t.Errorf("report %v", report)
	}
}

// A key press time only times the keys that are tapped.
func TestKeyPressTimeOnlyTimesTappedKeys(t *testing.T) {
	press := xmlSlot("keypress", 10000)
	// Held keys: no timing, no line.
	p, _, report := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 'A'), press))))
	if b := p.Layers[0].Bindings[0]; timingOf(b).TapPressSet {
		t.Errorf("a held key got a press time: %+v", timingOf(b))
	}
	if len(linesOf(report, "keypress.held")) != 0 {
		t.Errorf("report %v", report)
	}
	// Tapped keys: the time is theirs.
	p, _, report = imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 'A'), xmlSlot("pause", 50), xmlSlot("keyboard", 'B'), press))))
	if b := p.Layers[0].Bindings[0]; !timingOf(b).TapPressSet || timingOf(b).TapPressMs != 10000 {
		t.Errorf("timing %+v", timingOf(b))
	}
	if len(linesOf(report, "keypress.held")) != 0 {
		t.Errorf("report %v", report)
	}
	// A binding that also holds a key holds it for the time as well.
	p, _, report = imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 'A'), press, xmlSlot("release", 10), xmlSlot("keyboard", 'B')))))
	if b := p.Layers[0].Bindings[0]; !timingOf(b).TapPressSet || b.Press == nil || b.Release == nil {
		t.Errorf("binding %+v", b)
	}
	if lines := linesOf(report, "keypress.held"); len(lines) != 1 || lines[0].Severity != SeverityApproximate {
		t.Errorf("report %v", report)
	}
	// The key press time of the file is the profile's, and the report says
	// that held keys wait for it as well.
	p, _, report = imported(t, xmlDoc("<keyPressTime>200</keyPressTime>", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 'A')))))
	if p.Timing.TapPressMs != 200 {
		t.Errorf("timing %+v", p.Timing)
	}
	if lines := linesOf(report, "keypress.profile"); len(lines) != 1 || lines[0].Severity != SeverityApproximate || lines[0].Set != 0 {
		t.Errorf("report %v", report)
	}
	_, _, report = imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("keyboard", 'A')))))
	if len(linesOf(report, "keypress.profile")) != 0 {
		t.Errorf("report %v", report)
	}
}

// What the file says is quoted in the report only after it is cleaned and cut,
// so that a file cannot write lines of its own.
func TestReportQuotesAreSanitized(t *testing.T) {
	forged := "pulse\n信息  set 1：全部正常，没有丢弃任何东西 " + strings.Repeat("x", 4000)
	turbo := xmlDoc("", xmlSet(1, "", xmlButton(1, "<useturbo>true</useturbo><turbointerval>100</turbointerval><turbomode>"+forged+"</turbomode>", xmlSlot("keyboard", 'A'))))
	dir := func(i, code int, extra string) string {
		return fmt.Sprintf(`<stickbutton index="%d">%s<slots>%s</slots></stickbutton>`, i, extra, xmlSlot("mousemovement", code))
	}
	curve := "<mouseacceleration>" + strings.ReplaceAll(forged, "pulse", "weird") + "</mouseacceleration>"
	mouse := xmlDoc("", xmlSet(1, "", `<stick index="1">`+dir(1, 1, curve)+dir(5, 2, "")+dir(7, 3, "")+dir(3, 4, "")+`</stick>`))
	for name, doc := range map[string]string{"turbomode": turbo, "mouseacceleration": mouse} {
		_, _, report := imported(t, doc)
		quoted := false
		for _, l := range report {
			checkLineIsTidy(t, l)
			quoted = quoted || strings.Contains(l.Message, "pulse") || strings.Contains(l.Message, "weird")
		}
		if !quoted {
			t.Errorf("%s: the value is not quoted at all: %v", name, report)
		}
		if lines := strings.Split(strings.TrimSuffix(FormatReport(report), "\n"), "\n"); len(lines) != len(report) {
			t.Errorf("%s: %d report lines print as %d lines", name, len(report), len(lines))
		}
	}
}

// checkLineIsTidy checks that a report line is one printable line of a bounded
// length.
func checkLineIsTidy(t testing.TB, l ReportLine) {
	t.Helper()
	if i := strings.IndexFunc(l.Message, unicode.IsControl); i >= 0 {
		t.Errorf("line %s %q has a control character at %d", l.Code, l.Message, i)
	}
	if n := len([]rune(l.Message)); n > maxMessageRunes {
		t.Errorf("line %s is %d characters long", l.Code, n)
	}
}

// maxMessageRunes is the longest message a line may have: the longest fixed
// message is about a hundred characters, and a quote is cut at 60.
const maxMessageRunes = 400

// A refused file's message quotes the file only in a cut piece.
func TestRefusalMessagesAreBounded(t *testing.T) {
	long := strings.Repeat("A", 400_000)
	for name, doc := range map[string]string{
		"mismatched names": `<gamecontroller configversion="19"><` + long + `></` + strings.ReplaceAll(long, "A", "B") + `></gamecontroller>`,
		"unknown entity":   `<gamecontroller configversion="19">&` + long + `;</gamecontroller>`,
		"attribute":        `<gamecontroller configversion="19" ` + long + `=1/>`,
		"newline in name":  "<gamecontroller configversion=\"19\"><a\nb></c></gamecontroller>",
	} {
		_, _, err := Import([]byte(doc), Options{})
		var e *Error
		if !errors.As(err, &e) {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if n := len([]rune(e.Message)); n > 300 || strings.IndexFunc(e.Message, unicode.IsControl) >= 0 {
			t.Errorf("%s: the message is %d characters long: %.200q", name, n, e.Message)
		}
	}
}

// A profile made from hostile numbers has no timing that could hold a key for
// minutes: the longest is a key press time (10 seconds).
func TestHostileNumbersGiveBoundedTimings(t *testing.T) {
	big := "2147483647"
	doc := xmlDoc("<keyPressTime>"+big+"</keyPressTime>", xmlSet(1, "",
		xmlButton(1, "<useturbo>true</useturbo><turbointerval>"+big+"</turbointerval>", xmlSlot("keyboard", 'A')),
		xmlButton(2, "", xmlSlot("keyboard", 'A'), xmlSlot("keypress", 2147483647), xmlSlot("pause", 2147483647), xmlSlot("keyboard", 'B')),
		xmlButton(3, "<cycleresetactive>true</cycleresetactive><cycleresetinterval>"+big+"</cycleresetinterval>", xmlSlot("keyboard", 'A'), xmlSlot("cycle", 0), xmlSlot("keyboard", 'B')),
		xmlButton(4, "", xmlSlot("hold", 2147483647), xmlSlot("keyboard", 'A'))))
	out, _, err := Import([]byte(doc), Options{})
	if err != nil {
		t.Fatal(err)
	}
	checkTimingBounds(t, out)
}

// checkTimingBounds checks the times of an imported profile that keep a key
// down: none is longer than the key press time AntiMicroX allows.
func checkTimingBounds(t testing.TB, out []byte) {
	t.Helper()
	p, ds := profile.Parse(out)
	if err := ds.Err(); err != nil {
		t.Fatalf("the profile does not validate: %v", err)
	}
	check := func(where string, tm profile.Timing) {
		if tm.TapPressSet && tm.TapPressMs > maxKeyDownMs {
			t.Fatalf("%s: tapPressMs %d\n%s", where, tm.TapPressMs, out)
		}
		if tm.TapGapSet && tm.TapGapMs > maxKeyDownMs {
			t.Fatalf("%s: tapGapMs %d\n%s", where, tm.TapGapMs, out)
		}
	}
	check("profile", p.Timing)
	for _, l := range p.Layers {
		for _, b := range l.Bindings {
			check(l.Name+" "+b.Control.String(), timingOf(b))
		}
	}
	if p.FunctionKeys != nil {
		for _, l := range []*profile.Layer{p.FunctionKeys.Back, p.FunctionKeys.Start} {
			if l == nil {
				continue
			}
			for _, b := range l.Bindings {
				check(l.Name+" "+b.Control.String(), timingOf(b))
			}
		}
	}
}

// timingOf returns the timing of a binding, the zero one if it has none.
func timingOf(b *profile.Binding) profile.Timing {
	if b.Timing == nil {
		return profile.Timing{}
	}
	return *b.Timing
}

func TestClipText(t *testing.T) {
	long := strings.Repeat("字", 130)
	for _, tc := range []struct {
		in    string
		limit int
		want  string
	}{
		{"abc", 5, "abc"},
		{"abcde", 5, "abcde"},
		{"abcdef", 5, "abcde…"},
		{"ab\ncd ef", 5, "ab cd…"},
		{"a\x00b", 5, "a b"},
		{long, 120, strings.Repeat("字", 120) + "…"},
		{"", 5, ""},
	} {
		if got := clipText(tc.in, tc.limit); got != tc.want {
			t.Errorf("clipText(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}
