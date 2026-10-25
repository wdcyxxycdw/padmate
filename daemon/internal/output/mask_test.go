package output_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// maskEvents parses a batch written as "+LAlt -Tab -vk:0xE8": a press or
// release of each KeySpec, or of the mask key for vk:0xE8.
func maskEvents(t testing.TB, batch string) []output.KeyEvent {
	t.Helper()
	var events []output.KeyEvent
	for _, f := range strings.Fields(batch) {
		up := f[0] == '-'
		if !up && f[0] != '+' {
			t.Fatalf("bad event %q in %q", f, batch)
		}
		if unit, ok := strings.CutPrefix(f[1:], "text:U+"); ok {
			var u uint16
			if _, err := fmt.Sscanf(unit, "%X", &u); err != nil {
				t.Fatalf("bad Unicode event %q in %q: %v", f, batch, err)
			}
			events = append(events, output.UnicodeEvent(u, up))
			continue
		}
		if f[1:] == "vk:0xE8" {
			events = append(events, output.MenuMaskEvent(up))
			continue
		}
		e, err := output.KeyEventFor(keys.MustParse(f[1:]).Key(), up)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, e)
	}
	return events
}

// maskSender is the Sender under the MenuMask in TestMenuMask: a Recorder
// that panics, when told to, after it recorded a call.
type maskSender struct {
	*simtest.Recorder
	panicNext bool
}

func (s *maskSender) Send(events []output.KeyEvent) (int, error) {
	n, err := s.Recorder.Send(events)
	if s.panicNext {
		s.panicNext = false
		panic("test: sender panic")
	}
	return n, err
}

// Plans for a maskStep.
func failSend(n int) func(*maskSender) {
	return func(s *maskSender) { s.FailNext(n, errBlocked) }
}

func warnSend(s *maskSender) { s.WarnNext(errWarn) }

// shortSend makes the next call inject n events and return no error.
func shortSend(n int) func(*maskSender) {
	return func(s *maskSender) {
		s.Inject = func([]output.KeyEvent) (int, error) {
			s.Inject = nil
			return n, nil
		}
	}
}

func panicSend(s *maskSender) { s.panicNext = true }

// maskStep is one Send call on the MenuMask in TestMenuMask, or with
// release one ReleaseMask call.
type maskStep struct {
	send    string            // the caller's batch, for maskEvents
	release bool              // call ReleaseMask instead of Send
	plan    func(*maskSender) // run before the call, or nil
	want    string            // the calls the Recorder got, as batches writes them
	left    int               // caller events Send reports not injected
	err     string            // the error Send returns, "" for nil
	is      error             // when set, the error must wrap it
	panics  bool              // Send panics
}

// sendRecover calls s.Send and reports a panic instead of passing it on.
func sendRecover(s output.Sender, events []output.KeyEvent) (n int, panicked bool, err error) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	n, err = s.Send(events)
	return n, false, err
}

// releaseRecover calls ReleaseMask and reports a panic instead of passing
// it on.
func releaseRecover(m *output.MenuMask) (panicked bool, err error) {
	defer func() {
		if recover() != nil {
			panicked = true
		}
	}()
	return false, output.ReleaseMask(m)
}

const (
	errLeftDown    = "output: menu mask vk=0xE8 left down: "
	errNotInjected = "output: menu mask vk=0xE8 not injected: "
	errFewer       = "output: sender injected fewer events than given"
)

// TestMenuMask runs each case on a fresh MenuMask over a Recorder, and on
// two stacked MenuMasks, which must behave as one. Each case ends with
// nothing physically down.
func TestMenuMask(t *testing.T) {
	type maskCase struct {
		name  string
		steps []maskStep
	}
	tests := []maskCase{
		{"lone press and release", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"lone in one batch", []maskStep{
			{send: "+LAlt -LAlt", want: "[+LAlt +vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"dirty across batches", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+Tab", want: "[+Tab]"},
			{send: "-Tab", want: "[-Tab]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"dirty in one batch", []maskStep{
			{send: "+LAlt +Tab -Tab -LAlt", want: "[+LAlt +Tab -Tab -LAlt]"},
		}},
		{"key held before", []maskStep{
			{send: "+Z", want: "[+Z]"},
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-Z", want: "[-Z]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"Alt then Win", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+LWin", want: "[+LWin]"},
			{send: "-LWin", want: "[+vk:0xE8 -vk:0xE8 -LWin]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"one mask per batch", []maskStep{
			{send: "-LAlt -RAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt -RAlt]"},
		}},
		{"press dirties other menu keys", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-RAlt", want: "[-RAlt]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"re-press cleans", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+Tab", want: "[+Tab]"},
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-Tab", want: "[-Tab]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		// A wheel notch is no key press for the mask: a lone Alt released after
		// Alt+WheelUp still gets the mask key (harmless, and safe whatever
		// Windows makes of it).
		{"wheel does not dirty", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+WheelUp", want: "[+WheelUp]"},
			{send: "-WheelUp", want: "[-WheelUp]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"wheel in one batch", []maskStep{
			{send: "+LAlt +WheelDown -WheelDown -LAlt", want: "[+LAlt +WheelDown -WheelDown +vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"wheel and a key", []maskStep{
			{send: "+LAlt +Tab +WheelLeft -WheelLeft -Tab -LAlt", want: "[+LAlt +Tab +WheelLeft -WheelLeft -Tab -LAlt]"},
		}},
		{"wheel under Win and RAlt", []maskStep{
			{send: "+LWin +RAlt +WheelRight -WheelRight -RAlt -LWin", want: "[+LWin +RAlt +WheelRight -WheelRight +vk:0xE8 -vk:0xE8 -RAlt -LWin]"},
		}},
		{"wheel keeps a dirty menu key dirty", []maskStep{
			{send: "+LAlt +Z +WheelUp", want: "[+LAlt +Z +WheelUp]"},
			{send: "-WheelUp -Z -LAlt", want: "[-WheelUp -Z -LAlt]"},
		}},
		{"failed wheel press", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+WheelUp", plan: failSend(0), want: "[+WheelUp!]", left: 1, err: "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		// A mouse button press is no key press for the mask either, as the
		// wheel's is: a lone Alt released after Alt+MouseLeft still gets the
		// mask key, harmless and safe whatever Windows makes of it. Neither the
		// press nor the release of a button counts.
		{"button does not dirty", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+MouseLeft", want: "[+MouseLeft]"},
			{send: "-MouseLeft", want: "[-MouseLeft]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"button in one batch", []maskStep{
			{send: "+LAlt +MouseRight -MouseRight -LAlt", want: "[+LAlt +MouseRight -MouseRight +vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"button held across the release", []maskStep{
			{send: "+LAlt +MouseMiddle", want: "[+LAlt +MouseMiddle]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
			{send: "-MouseMiddle", want: "[-MouseMiddle]"},
		}},
		{"button and a key", []maskStep{
			{send: "+LAlt +Tab +MouseX1 -MouseX1 -Tab -LAlt", want: "[+LAlt +Tab +MouseX1 -MouseX1 -Tab -LAlt]"},
		}},
		{"button under Win and RAlt", []maskStep{
			{send: "+LWin +RAlt +MouseX2 -MouseX2 -RAlt -LWin", want: "[+LWin +RAlt +MouseX2 -MouseX2 +vk:0xE8 -vk:0xE8 -RAlt -LWin]"},
		}},
		{"button keeps a dirty menu key dirty", []maskStep{
			{send: "+LAlt +Z +MouseLeft", want: "[+LAlt +Z +MouseLeft]"},
			{send: "-MouseLeft -Z -LAlt", want: "[-MouseLeft -Z -LAlt]"},
		}},
		{"failed button press", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+MouseLeft", plan: failSend(0), want: "[+MouseLeft!]", left: 1, err: "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		// A Unicode event (a unit of a typed text, OUT-21) is no key press for the
		// mask, like a mouse event: a lone Alt released after Alt and a typed
		// character still gets the mask key, which is harmless.
		{"text does not dirty", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+text:U+0041 -text:U+0041", want: "[+text:U+0041 -text:U+0041]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"text in one batch", []maskStep{
			{send: "+LAlt +text:U+4F60 -text:U+4F60 -LAlt", want: "[+LAlt +text:U+4F60 -text:U+4F60 +vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"text and a key", []maskStep{
			{send: "+LAlt +Tab +text:U+0041 -text:U+0041 -Tab -LAlt", want: "[+LAlt +Tab +text:U+0041 -text:U+0041 -Tab -LAlt]"},
		}},
		{"text keeps a dirty menu key dirty", []maskStep{
			{send: "+LAlt +Z +text:U+D83D -text:U+D83D", want: "[+LAlt +Z +text:U+D83D -text:U+D83D]"},
			{send: "-Z -LAlt", want: "[-Z -LAlt]"},
		}},
		{"failed text press", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+text:U+0041 -text:U+0041", plan: failSend(0), want: "[+text:U+0041! -text:U+0041!]", left: 2, err: "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"failed press does not dirty", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+Tab", plan: failSend(0), want: "[+Tab!]", left: 1, err: "test: blocked", is: errBlocked},
			{send: "-Tab", want: "[-Tab]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"failed menu key press", []maskStep{
			{send: "+LAlt", plan: failSend(0), want: "[+LAlt!]", left: 1, err: "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"fail at 0", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(0), want: "[+vk:0xE8! -vk:0xE8! -LAlt!]",
				left: 1, err: errNotInjected + "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"fail at 1", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[-vk:0xE8 -LAlt]"},
		}},
		{"fail at 2", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(2), want: "[+vk:0xE8 -vk:0xE8 -LAlt!]",
				left: 1, err: "test: blocked", is: errBlocked},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"carry", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{send: "+Z", plan: failSend(0), want: "[-vk:0xE8! +Z!]", left: 1, err: errLeftDown + "test: blocked"},
			{send: "+Z", want: "[-vk:0xE8 +Z]"},
			{send: "-Z", want: "[-Z]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"count maps to the caller's events", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-X -LAlt", plan: failSend(2), want: "[-X +vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{send: "-LAlt", want: "[-vk:0xE8 -LAlt]"},
		}},
		{"warning", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: warnSend, want: "[+vk:0xE8 -vk:0xE8 -LAlt]", err: "test: warning", is: errWarn},
		}},
		{"short count", []maskStep{
			{send: "+Z", plan: shortSend(0), want: "[+Z!]", left: 1, err: errFewer},
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: shortSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]", left: 1, err: errLeftDown + errFewer},
			{send: "-LAlt", want: "[-vk:0xE8 -LAlt]"},
		}},
		{"empty batch", []maskStep{
			{send: "", want: ""},
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{send: "", want: ""},
			{send: "-LAlt", want: "[-vk:0xE8 -LAlt]"},
		}},
		{"panic cleans pressed menu keys", []maskStep{
			{send: "+LAlt +Tab", plan: panicSend, want: "[+LAlt +Tab]", panics: true},
			{send: "-Tab", want: "[-Tab]"},
			{send: "-LAlt", want: "[+vk:0xE8 -vk:0xE8 -LAlt]"},
		}},
		{"panic on a masked batch", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: panicSend, want: "[+vk:0xE8 -vk:0xE8 -LAlt]", panics: true},
			{send: "+Z", want: "[-vk:0xE8 +Z]"},
			{send: "-Z", want: "[-Z]"},
		}},
		{"release the mask alone", []maskStep{
			{release: true, want: ""},
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{release: true, want: "[-vk:0xE8]"},
			{release: true, want: ""},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"release the mask alone fails", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{release: true, plan: failSend(0), want: "[-vk:0xE8!]", err: errLeftDown + "test: blocked", is: errBlocked},
			{release: true, plan: warnSend, want: "[-vk:0xE8]", err: "test: warning", is: errWarn},
			{release: true, want: ""},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"release the mask alone panics", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{release: true, plan: panicSend, want: "[-vk:0xE8]", panics: true},
			{release: true, want: "[-vk:0xE8]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"release the mask alone after a panic", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: panicSend, want: "[+vk:0xE8 -vk:0xE8 -LAlt]", panics: true},
			{release: true, want: "[-vk:0xE8]"},
			{release: true, want: ""},
		}},
		{"caller mask is another key", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "+vk:0xE8", want: "[+vk:0xE8]"},
			{send: "-vk:0xE8", want: "[-vk:0xE8]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
		{"caller mask with the mask down", []maskStep{
			{send: "+LAlt", want: "[+LAlt]"},
			{send: "-LAlt", plan: failSend(1), want: "[+vk:0xE8 -vk:0xE8! -LAlt!]",
				left: 1, err: errLeftDown + "test: blocked"},
			{send: "+vk:0xE8", want: "[-vk:0xE8 +vk:0xE8]"},
			{send: "-vk:0xE8", want: "[-vk:0xE8]"},
			{send: "-LAlt", want: "[-LAlt]"},
		}},
	}
	for _, k := range []string{"LAlt", "RAlt", "LWin", "RWin", "sc:0x38"} {
		tests = append(tests, maskCase{"menu key " + k, []maskStep{
			{send: "+" + k, want: "[+" + k + "]"},
			{send: "-" + k, want: "[+vk:0xE8 -vk:0xE8 -" + k + "]"},
		}})
	}
	for _, k := range []string{"LCtrl", "LShift", "Menu", "Space", "F10", "Pause"} {
		tests = append(tests, maskCase{"other key " + k, []maskStep{
			{send: "+" + k, want: "[+" + k + "]"},
			{send: "-" + k, want: "[-" + k + "]"},
		}})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, stacked := range []bool{false, true} {
				s := &maskSender{Recorder: simtest.NewRecorder(clock.NewFake())}
				m := output.NewMenuMask(s)
				if stacked {
					m = output.NewMenuMask(m)
				}
				for i, st := range tt.steps {
					runMaskStep(t, m, s, stacked, i, st)
				}
				if down := s.Down(); len(down) > 0 {
					t.Errorf("stacked %v: still down: %s", stacked, eventsString(down))
				}
			}
		})
	}
}

func runMaskStep(t *testing.T, m *output.MenuMask, s *maskSender, stacked bool, i int, st maskStep) {
	t.Helper()
	events := maskEvents(t, st.send)
	if st.plan != nil {
		st.plan(s)
	}
	from := s.Calls()
	var n int
	var panicked bool
	var err error
	desc := fmt.Sprintf("stacked %v, step %d [%s]", stacked, i, st.send)
	if st.release {
		desc = fmt.Sprintf("stacked %v, step %d ReleaseMask", stacked, i)
		panicked, err = releaseRecover(m)
	} else {
		n, panicked, err = sendRecover(m, events)
	}
	if got := batches(s.Recorder, from); got != st.want || s.Calls()-from != strings.Count(st.want, "[") {
		t.Errorf("%s: %d Send calls %q, want %q", desc, s.Calls()-from, got, st.want)
	}
	if panicked != st.panics {
		t.Fatalf("%s: panicked %v, want %v", desc, panicked, st.panics)
	}
	if panicked {
		return
	}
	if want := len(events) - st.left; !st.release && n != want {
		t.Errorf("%s: Send = %d, want %d", desc, n, want)
	}
	switch {
	case st.err == "" && err != nil:
		t.Errorf("%s: error %q, want none", desc, err)
	case st.err != "" && (err == nil || err.Error() != st.err):
		t.Errorf("%s: error %v, want %q", desc, err, st.err)
	}
	if st.is != nil && !errors.Is(err, st.is) {
		t.Errorf("%s: error %v does not wrap %v", desc, err, st.is)
	}
}

func TestMenuMaskEvent(t *testing.T) {
	for _, up := range []bool{false, true} {
		e := output.MenuMaskEvent(up)
		if !e.ByVK || e.VK != output.MenuMaskVK || e.Scan != 0 || e.Ext || e.Key.IsValid() || e.Up != up {
			t.Errorf("MenuMaskEvent(%v) = %+v", up, e)
		}
		if got := e.ID().String(); got != "vk:0xE8" {
			t.Errorf("MenuMaskEvent(%v).ID() = %s, want vk:0xE8", up, got)
		}
		if !output.IsMenuMask(e) {
			t.Errorf("IsMenuMask(MenuMaskEvent(%v)) = false", up)
		}
		for _, k := range keys.Table() {
			if ke, err := output.KeyEventFor(k, up); err == nil && output.IsMenuMask(ke) {
				t.Errorf("IsMenuMask(KeyEventFor(%v, %v)) = true", k, up)
			}
		}
	}
}

func TestNewMenuMaskNil(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewMenuMask(nil) did not panic")
		}
	}()
	output.NewMenuMask(nil)
}

// TestMenuMaskWithArbiter runs the MenuMask under an Arbiter, as simtest's
// WithMenuMask puts it.
func TestMenuMaskWithArbiter(t *testing.T) {
	t.Run("hold", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.down(ownerA, "Alt")
		s.Advance(10 * ms)
		s.up(ownerA, "Alt")
		s.ExpectOutput("+LAlt@0 +vk:0xE8@10 -vk:0xE8@10 -LAlt@10")
	})
	t.Run("tap", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.tap(ownerA, "Alt", 20*ms)
		s.Settle()
		s.ExpectOutput("+LAlt@0 +vk:0xE8@20 -vk:0xE8@20 -LAlt@20")
	})
	for _, first := range []output.Owner{ownerA, ownerB} {
		t.Run("shared released by "+ownerName(first)+" first", func(t *testing.T) {
			s := newSim(t, simtest.WithMenuMask())
			s.down(ownerA, "Alt")
			s.down(ownerB, "Alt+Z")
			s.Advance(10 * ms)
			if first == ownerA {
				s.up(ownerA, "Alt")
				s.up(ownerB, "Alt+Z")
			} else {
				s.up(ownerB, "Alt+Z")
				s.up(ownerA, "Alt")
			}
			s.ExpectOutput("+LAlt@0 +Z@0 -Z@10 -LAlt@10")
		})
	}
	t.Run("tap Alt+Tab", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.tap(ownerA, "Alt+Tab", 0)
		s.ExpectOutput("+LAlt@0 +Tab@0 -Tab@0 -LAlt@0")
	})
	t.Run("ReleaseAll", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.down(ownerA, "LWin")
		if err := s.Arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		s.ExpectOutput("+LWin@0 +vk:0xE8@0 -vk:0xE8@0 -LWin@0")
	})
	t.Run("resend", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.down(ownerA, "Alt")
		s.Out.FailNext(1, errBlocked)
		wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Alt")), errBlocked, "injected 0 of 1", "vk=0xE8 left down")
		s.expectBatches(1, "[+vk:0xE8 -vk:0xE8! -LAlt!] [-vk:0xE8 -LAlt]")
		s.expectHeld("")
	})
	t.Run("pending", func(t *testing.T) {
		s := newSim(t, simtest.WithMenuMask())
		s.down(ownerA, "Alt")
		failRelease(s.Out, 1)
		wantErr(t, s.Arb.Up(ownerA, keys.MustParse("Alt")), errBlocked, "vk=0xE8 left down", "releasing LAlt failed twice")
		s.expectBatches(1, "[+vk:0xE8 -vk:0xE8! -LAlt!] [-vk:0xE8! -LAlt!]")
		s.expectHeld("LAlt(pending)")
		s.Advance(100 * ms)
		if errs := s.Errors(); len(errs) > 0 {
			t.Errorf("Advance errors %q", errs)
		}
		s.expectBatches(3, "[-vk:0xE8 -LAlt]")
		s.ExpectOutput("+LAlt@0 +vk:0xE8@0 -vk:0xE8@100 -LAlt@100")
	})
}

// nopSender injects every event.
type nopSender struct{}

func (nopSender) Send(events []output.KeyEvent) (int, error) { return len(events), nil }

func TestMenuMaskNoAllocs(t *testing.T) {
	if raceEnabled {
		t.Skip("the race detector may allocate; the other CI jobs run this")
	}
	m := output.NewMenuMask(nopSender{})
	press, release, lone := maskEvents(t, "+Z"), maskEvents(t, "-Z"), maskEvents(t, "+LAlt -LAlt")
	for _, tt := range []struct {
		name string
		f    func()
	}{
		{"pass-through", func() { _, _ = m.Send(press); _, _ = m.Send(release) }},
		{"masked", func() { _, _ = m.Send(lone) }}, // reuses its out batch after the first run
	} {
		if n := testing.AllocsPerRun(100, tt.f); n != 0 {
			t.Errorf("%s: %v allocations per run, want 0", tt.name, n)
		}
	}
}

// BenchmarkMenuMask measures a batch the MenuMask passes through and one it
// masks, for ENG-11.
func BenchmarkMenuMask(b *testing.B) {
	for _, bb := range []struct{ name, batch string }{
		{"pass-through", "+LCtrl +Z -Z -LCtrl"},
		{"masked", "+LAlt -LAlt"},
	} {
		events := maskEvents(b, bb.batch)
		b.Run(bb.name, func(b *testing.B) {
			m := output.NewMenuMask(nopSender{})
			b.ReportAllocs()
			for b.Loop() {
				_, _ = m.Send(events)
			}
		})
	}
}
