package output_test

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// Plans of FuzzMenuMask's Sender for one batch; k comes from the input.
const (
	fmAll   = iota // inject every event
	fmFail         // inject at most k events and return errBlocked
	fmWarn         // inject every event and return errWarn
	fmShort        // inject at most k events and return no error
	fmPanic        // inject at most k events and panic
	fmPlans
)

// fmKeys are the keys of FuzzMenuMask's batches, the menu keys first.
var fmKeys = [...]string{"LAlt", "RAlt", "LWin", "RWin", "LCtrl", "LShift", "Z", "Tab", "Pause"}

// Indexes into fmKeys.
const (
	fkLAlt = iota
	fkRAlt
	fkLWin
	fkRWin
	fkLCtrl
	fkLShift
	fkZ
	fkTab
	fkPause
	fmMenuKeys = fkLCtrl // fmKeys[:fmMenuKeys] are the menu keys
)

// fmBatch encodes a batch of 1–6 events for FuzzMenuMask: a header byte
// with the plan and the event count, the plan's k, and the events.
func fmBatch(plan, k byte, events ...byte) []byte {
	return append([]byte{plan*6 + byte(len(events)-1), k}, events...)
}

// fmDown and fmUp encode the press and the release of fmKeys[i].
func fmDown(i byte) byte { return 2 * i }
func fmUp(i byte) byte   { return 2*i + 1 }

// fmSeeds are named coverage inputs.
var fmSeeds = []struct {
	name string
	data []byte
}{
	{"lone Alt", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)), fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"Alt+Tab", slices.Concat(
		fmBatch(fmAll, 0, fmDown(fkLAlt), fmDown(fkTab)), fmBatch(fmAll, 0, fmUp(fkTab), fmUp(fkLAlt)))},
	{"key held before", slices.Concat(fmBatch(fmAll, 0, fmDown(fkZ)), fmBatch(fmAll, 0, fmDown(fkLAlt)),
		fmBatch(fmAll, 0, fmUp(fkZ)), fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"Ctrl+Alt", slices.Concat(
		fmBatch(fmAll, 0, fmDown(fkLCtrl), fmDown(fkLAlt)), fmBatch(fmAll, 0, fmUp(fkLAlt), fmUp(fkLCtrl)))},
	{"fail at 0", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)), fmBatch(fmFail, 0, fmUp(fkLAlt)),
		fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"fail at 1", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)), fmBatch(fmFail, 1, fmUp(fkLAlt)),
		fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"fail at 2", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)), fmBatch(fmFail, 2, fmUp(fkLAlt)),
		fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"short at 1", slices.Concat(fmBatch(fmAll, 0, fmDown(fkRWin)), fmBatch(fmShort, 1, fmUp(fkRWin)),
		fmBatch(fmAll, 0, fmUp(fkRWin)))},
	{"carry fails", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)), fmBatch(fmFail, 1, fmUp(fkLAlt)),
		fmBatch(fmFail, 0, fmDown(fkZ)), fmBatch(fmAll, 0, fmDown(fkZ)), fmBatch(fmAll, 0, fmUp(fkZ)))},
	{"panic on a masked batch", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)),
		fmBatch(fmPanic, 3, fmUp(fkLAlt)), fmBatch(fmAll, 0, fmDown(fkZ)), fmBatch(fmAll, 0, fmUp(fkZ)))},
	{"panic in the mask", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)),
		fmBatch(fmPanic, 1, fmUp(fkLAlt)), fmBatch(fmAll, 0, fmUp(fkLAlt)))},
	{"warn", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLWin)), fmBatch(fmWarn, 0, fmUp(fkLWin)))},
	{"one mask per batch", slices.Concat(fmBatch(fmAll, 0, fmDown(fkRAlt), fmDown(fkLAlt)),
		fmBatch(fmFail, 3, fmUp(fkLAlt), fmUp(fkRAlt), fmDown(fkPause), fmUp(fkPause)))},
	// The last byte 1 picks the ledger's release at the end.
	{"ledger: key down", slices.Concat(fmBatch(fmAll, 0, fmDown(fkLAlt)),
		fmBatch(fmFail, 1, fmUp(fkLAlt)), []byte{1})},
	{"ledger: failed press, mask down", slices.Concat(fmBatch(fmFail, 0, fmDown(fkLAlt)),
		fmBatch(fmFail, 1, fmUp(fkLAlt)), fmBatch(fmFail, 0, fmUp(fkLAlt)), []byte{1})},
	{"ledger: failed press, panic", slices.Concat(fmBatch(fmFail, 0, fmDown(fkLAlt)),
		fmBatch(fmPanic, 3, fmUp(fkLAlt)), []byte{1})},
}

// FuzzMenuMask sends up to 64 random batches through a MenuMask to a fake
// Sender that injects them, fails part way, warns, reports a short count
// with no error, or panics, per the input. After each batch it checks the
// out batch and the result against the physical key state and a model of
// the rule. At the end it releases, with failures off, every key that is
// down or whose release did not go out, as the Arbiter would, or, when the
// first byte left after the batches is odd, only the keys that are down,
// as the Loop's ledger does after a panic: nothing may be left down, the
// mask key included.
func FuzzMenuMask(f *testing.F) {
	for _, seed := range fmSeeds {
		f.Add(seed.data)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		fz := newMaskFuzzer(t)
		for b := 0; b < 64 && len(data) > 2; b++ {
			h, k := data[0], data[1]
			n := min(int(h%6)+1, len(data)-2)
			events := make([]output.KeyEvent, n)
			for i, e := range data[2 : 2+n] {
				events[i] = fz.event(int(e/2)%len(fmKeys), e%2 == 1)
			}
			data = data[2+n:]
			fz.step(b, int(h/6)%fmPlans, int(k%8), events)
		}
		fz.finish(len(data) > 0 && data[0]%2 == 1)
	})
}

var fmMaskID = output.MenuMaskEvent(false).ID()

// maskFuzzer is FuzzMenuMask's state, and the Sender under its MenuMask.
type maskFuzzer struct {
	t     *testing.T
	m     *output.MenuMask
	press [len(fmKeys)]output.KeyEvent
	menu  map[output.KeyID]int // index of each menu key in fmKeys

	// The Sender.
	plan, k int
	calls   int
	out     []output.KeyEvent // a copy of the last batch it got
	n       int               // how many of out it injected

	down    map[output.KeyID]bool // physically down
	dirty   [fmMenuKeys]bool      // the model: another key went down since the menu key's last press
	pending map[output.KeyID]bool // keys whose last caller event is a release that did not go out

	panicked bool // a Send panicked: the rule is conservative from now on (C5 off)
	unsure   bool // a Send panicked, and none has injected its whole batch since
	desc     string
}

func newMaskFuzzer(t *testing.T) *maskFuzzer {
	fz := &maskFuzzer{t: t, menu: map[output.KeyID]int{}, down: map[output.KeyID]bool{}, pending: map[output.KeyID]bool{}}
	for i, name := range fmKeys {
		e, err := output.KeyEventFor(keys.MustParse(name).Key(), false)
		if err != nil {
			t.Fatal(err)
		}
		fz.press[i] = e
		if i < fmMenuKeys {
			fz.menu[e.ID()] = i
		}
	}
	fz.m = output.NewMenuMask(fz)
	return fz
}

func (fz *maskFuzzer) event(i int, up bool) output.KeyEvent {
	e := fz.press[i]
	e.Up = up
	return e
}

// menuKey returns the index of e's key among the menu keys, or -1.
func (fz *maskFuzzer) menuKey(e output.KeyEvent) int {
	if i, ok := fz.menu[e.ID()]; ok {
		return i
	}
	return -1
}

// Send implements output.Sender with the current plan.
func (fz *maskFuzzer) Send(events []output.KeyEvent) (int, error) {
	fz.calls++
	fz.out = append(fz.out[:0], events...)
	n, err := len(events), error(nil)
	switch fz.plan {
	case fmFail:
		n, err = min(fz.k, n), errBlocked
	case fmWarn:
		err = errWarn
	case fmShort, fmPanic:
		n = min(fz.k, n)
	}
	fz.n = n
	if fz.plan == fmPanic {
		panic("test: sender panic")
	}
	return n, err
}

// apply updates dirty, the model of the rule, with an injected event.
func (fz *maskFuzzer) apply(dirty *[fmMenuKeys]bool, e output.KeyEvent) {
	k := fz.menuKey(e)
	switch {
	case e.Up && k >= 0:
		dirty[k] = false
	case !e.Up:
		for j := range dirty {
			dirty[j] = j != k
		}
	}
}

func (fz *maskFuzzer) fatalf(format string, args ...any) {
	fz.t.Helper()
	fz.t.Fatalf("%s: out [%s], %d injected: %s", fz.desc, eventsString(fz.out), fz.n, fmt.Sprintf(format, args...))
}

func (fz *maskFuzzer) step(b, plan, k int, events []output.KeyEvent) {
	fz.plan, fz.k = plan, k
	fz.desc = fmt.Sprintf("batch %d [%s], plan %d k %d", b, eventsString(events), plan, k)
	maskWas := fz.down[fmMaskID]
	calls := fz.calls
	c, panicked, err := sendRecover(fz.m, events)
	if fz.calls != calls+1 {
		fz.t.Fatalf("%s: %d Send calls, want 1", fz.desc, fz.calls-calls)
	}
	out, n := fz.out, fz.n

	// C1: the out batch is events with mask events added.
	var callers []output.KeyEvent
	for _, e := range out {
		if !output.IsMenuMask(e) {
			callers = append(callers, e)
		}
	}
	if !slices.Equal(callers, events) {
		fz.fatalf("the caller's events changed")
	}

	// C3, C5 and C6 on the batch as planned, as if all of it went out.
	if maskWas && (!output.IsMenuMask(out[0]) || !out[0].Up) {
		fz.fatalf("the mask is down, and the batch does not start with its release")
	}
	dirty, maskDown := fz.dirty, maskWas
	for i, e := range out {
		k := fz.menuKey(e)
		switch {
		case output.IsMenuMask(e) && !e.Up:
			if i+2 >= len(out) || !output.IsMenuMask(out[i+1]) || !out[i+1].Up ||
				fz.menuKey(out[i+2]) < 0 || !out[i+2].Up {
				fz.fatalf("mask press %d is not followed by its release and a menu key's", i)
			}
			if maskDown {
				fz.fatalf("mask press %d while the mask is down", i)
			}
			if !fz.panicked && dirty[fz.menuKey(out[i+2])] {
				fz.fatalf("mask before %s, which is not lone", keyName(out[i+2]))
			}
		case output.IsMenuMask(e):
			pair := i > 0 && output.IsMenuMask(out[i-1]) && !out[i-1].Up
			if !pair && i > 0 {
				fz.fatalf("stray mask release %d", i)
			}
			if !pair && !maskWas && !fz.unsure {
				fz.fatalf("leading mask release, but the mask is up")
			}
		case k >= 0 && e.Up && !fz.panicked && !dirty[k]:
			if i < 2 || !output.IsMenuMask(out[i-2]) || out[i-2].Up {
				fz.fatalf("lone release of %s is not masked", keyName(e))
			}
		}
		if output.IsMenuMask(e) {
			maskDown = !e.Up
		}
		fz.apply(&dirty, e)
	}

	// Apply the injected prefix. C4: no menu key that is down goes up
	// alone.
	injected := 0
	for _, e := range out[:n] {
		if k := fz.menuKey(e); k >= 0 && e.Up && fz.down[e.ID()] && !fz.dirty[k] {
			fz.fatalf("%s went up alone", keyName(e))
		}
		if e.Up {
			delete(fz.down, e.ID())
		} else {
			fz.down[e.ID()] = true
		}
		fz.apply(&fz.dirty, e)
		if !output.IsMenuMask(e) {
			injected++
		}
	}
	for i, e := range events {
		switch {
		case i < injected:
			delete(fz.pending, e.ID())
		case e.Up:
			fz.pending[e.ID()] = true
		}
	}
	if panicked {
		fz.panicked, fz.unsure = true, true
		return
	}

	// C1 and C2: the count, and the error.
	if c != injected {
		fz.fatalf("Send = %d, want %d", c, injected)
	}
	full := n == len(out)
	if full {
		fz.unsure = false
		// C6: the mask is up after a batch that went out whole.
		if fz.down[fmMaskID] {
			fz.fatalf("the mask is down after the whole batch went out")
		}
	}
	switch {
	case full && (fz.plan == fmFail || fz.plan == fmWarn):
		want := map[int]error{fmFail: errBlocked, fmWarn: errWarn}[fz.plan]
		if err == nil || err.Error() != want.Error() {
			fz.fatalf("error %v, want the warning %v unwrapped", err, want)
		}
	case full:
		if err != nil {
			fz.fatalf("error %v, want none", err)
		}
	case err == nil:
		fz.fatalf("Send = %d of %d with no error", c, len(events))
	default:
		fz.checkWrap(err, out[n])
	}
}

// checkWrap checks the error of a Send that left out out[n:], starting
// with next.
func (fz *maskFuzzer) checkWrap(err error, next output.KeyEvent) {
	switch {
	case fz.plan == fmFail && !errors.Is(err, errBlocked):
		fz.fatalf("error %v does not wrap %v", err, errBlocked)
	case fz.plan == fmShort && !strings.HasSuffix(err.Error(), errFewer):
		fz.fatalf("error %v, want one ending in %q", err, errFewer)
	}
	msg := err.Error()
	switch {
	case fz.down[fmMaskID]:
		if !strings.HasPrefix(msg, errLeftDown) {
			fz.fatalf("the mask is down, but error %q does not say so", msg)
		}
	case fz.unsure:
		// The MenuMask may count the mask as down.
	case output.IsMenuMask(next):
		if !strings.HasPrefix(msg, errNotInjected) {
			fz.fatalf("error %q, want one starting %q", msg, errNotInjected)
		}
	case strings.Contains(msg, "menu mask"):
		fz.fatalf("error %q names the mask, which is up and injected", msg)
	}
}

// finish releases, with failures off, every key that is down or whose
// release did not go out, as the Arbiter would. With ledger it releases
// only the keys that are down, as the Loop's ledger does after a panic,
// and with none down the mask alone. Nothing may be left down.
func (fz *maskFuzzer) finish(ledger bool) {
	var ups []output.KeyEvent
	for i := len(fmKeys) - 1; i >= 0; i-- {
		if id := fz.press[i].ID(); fz.down[id] || fz.pending[id] && !ledger {
			ups = append(ups, fz.event(i, true))
		}
	}
	switch {
	case len(ups) > 0:
		fz.step(-1, fmAll, 0, ups)
	case ledger:
		fz.releaseMask()
	case fz.down[fmMaskID]:
		fz.t.Fatalf("finish: the mask is down, and no release is left to carry its release")
	}
	if len(fz.down) > 0 {
		fz.t.Fatalf("finish: still down after [%s]: %v", eventsString(fz.out), fz.down)
	}
}

// releaseMask calls ReleaseMask, which must send the mask's release alone,
// and only when the mask may be down.
func (fz *maskFuzzer) releaseMask() {
	fz.plan, fz.desc = fmAll, "finish: ReleaseMask"
	calls := fz.calls
	if err := output.ReleaseMask(fz.m); err != nil {
		fz.t.Fatalf("%s: %v", fz.desc, err)
	}
	switch {
	case fz.calls == calls:
	case fz.calls > calls+1 || len(fz.out) != 1 || !output.IsMenuMask(fz.out[0]) || !fz.out[0].Up:
		fz.fatalf("want one call with the mask's release")
	case !fz.down[fmMaskID] && !fz.unsure:
		fz.fatalf("the mask is up, but ReleaseMask released it")
	default:
		delete(fz.down, fmMaskID)
	}
}
