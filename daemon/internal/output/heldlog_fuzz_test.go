package output_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// fhKeys are the keys of FuzzHeldLog: bit i of its key bytes is fhKeys[i],
// and bit 6 of the KeyDown byte is the menu mask key.
var fhKeys = [...]string{"LCtrl", "LShift", "LAlt", "Z", "Pause", "Num1"}

// fhChords are the chords of its commands, made of fhKeys.
var fhChords = [...]string{
	"LCtrl", "LShift", "LAlt", "Z", "Pause", "Num1", "LCtrl+Z", "LShift+Pause",
	"LCtrl+LAlt+Num1", "LAlt+Z", "LCtrl+LShift", "LAlt", "LShift+Num1", "LCtrl+Pause", "Z", "LAlt+LShift+Z",
}

// Commands of FuzzHeldLog, op%4 of each step's first byte; op/4%16 is the
// chord and op/64 the owner (Tap: its hold, 0 or 30 ms).
const (
	fhDown       = iota
	fhUp         // Up, or with owner 3 Advance 40 ms
	fhTap        // Tap(owner 0, chord, hold)
	fhReleaseAll // ReleaseAll, or with owner 3 ReleaseOwners(owner 1)
)

var fhOwners = [...]output.Owner{
	{Slot: 0, Gen: 1, Source: output.SourceDecl, Cycle: 1},
	{Slot: 1, Gen: 1, Source: output.SourceDecl, Cycle: 1},
	{Slot: 0, Gen: 1, Source: output.SourceScript},
	{Slot: 2, Gen: 1, Source: output.SourceDecl, Cycle: 1},
}

// fhSender is the Sender of FuzzHeldLog. At every Send it checks that the
// held-key log has the keys it has down and the keys the batch presses,
// then answers as the step's result byte says: r%8 of 0-3 injects all, 4
// a prefix of r/8 events with an error, 5 nothing, 6 all with a warning,
// 7 panics (once).
type fhSender struct {
	t    *testing.T
	path string

	mu       sync.Mutex
	result   byte
	panicked bool
	down     []output.KeyID // without the menu mask key
}

var errFuzzSend = errors.New("fuzz: send failed")

func (s *fhSender) setResult(r byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.result = r
}

func (s *fhSender) held() []output.KeyID {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.down)
}

func (s *fhSender) Send(events []output.KeyEvent) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	need := slices.Clone(s.down)
	for _, e := range events {
		if !e.Up && !output.IsMenuMask(e) {
			need = append(need, e.ID())
		}
	}
	s.checkLog(fmt.Sprintf("Send(%s)", eventsTrace(events)), need)
	n, err := len(events), error(nil)
	switch r := s.result; r % 8 {
	case 4:
		n, err = min(int(r/8), len(events)), errFuzzSend
	case 5:
		n, err = 0, errFuzzSend
	case 6:
		err = errFuzzSend
	case 7:
		if !s.panicked {
			s.panicked = true
			panic("fuzz: Send panics")
		}
	}
	for _, e := range events[:n] {
		switch id := e.ID(); {
		case output.IsMenuMask(e):
		case e.Up:
			s.down = slices.DeleteFunc(s.down, func(d output.KeyID) bool { return d == id })
		case !slices.Contains(s.down, id):
			s.down = append(s.down, id)
		}
	}
	return n, err
}

// checkLog checks that the log has every key of need.
func (s *fhSender) checkLog(what string, need []output.KeyID) {
	s.t.Helper()
	b, err := os.ReadFile(s.path)
	if err != nil {
		s.t.Errorf("%s: reading the held-key log: %v", what, err)
		return
	}
	logged, err := output.DecodeHeld(b)
	if err != nil {
		s.t.Errorf("%s: %v", what, err)
		return
	}
	for _, id := range need {
		if !slices.Contains(logged, id) {
			s.t.Errorf("%s: the held-key log has %v, not %v", what, logged, id)
		}
	}
}

// FuzzHeldLog runs a Loop of the logon session heldNow with a
// held-key log that starts as the input says, with a KeyDown the input
// sets, through random commands whose Sends inject all, a prefix or
// nothing, warn or panic. The first byte is the log's mode: %4 of 0 no
// file, 1 a valid log of heldNow with the keys of the second byte, 2 a
// file of the second byte%(HeldSize+1) bytes of the input, any content, 3
// a valid log of another logon session with the keys of the second byte.
// Then the KeyDown byte, then up to 64 steps of two bytes, the command and
// the Sends' result. It checks that the sweep releases no key of a log of
// another session; that the log is written before every Send, as
// TestLoopHeldLogCoversPresses does, and after every step has the keys
// down; and at the end, by Stop or by a panic, that the log is removed
// when it has no key, and has every key left down otherwise.
func FuzzHeldLog(f *testing.F) {
	f.Add([]byte{0, 0, 0})
	f.Add([]byte{1, 0b1, 0b1, fhDown, 0, fhUp, 0})                                    // LCtrl swept, pressed, released
	f.Add([]byte{1, 0b111111, 0b1000101, fhDown | 8<<2, 5})                           // swept with the mask, Send fails
	f.Add([]byte{1, 0b100, 0b1000100, fhTap | 2<<2 | 1<<6, 0, fhUp | 3<<6, 0})        // LAlt and the mask swept, a tap
	f.Add([]byte{2, 8, 'P', 'D', 'M', 'H', 2, 0, 0, 0, fhDown, 0})                    // too short
	f.Add([]byte{0, 0, 0, fhDown | 6<<2, 4 | 1<<3, fhDown | 7<<2 | 1<<6, 7, fhUp, 0}) // a prefix, then a panic
	f.Add([]byte{1, 0b1, 0b1, fhDown | 3<<2, 5, fhReleaseAll, 5, fhReleaseAll, 0})    // the sweep and a release fail
	// Bits no key has (VK 0, the mask key's) and LCtrl, all down.
	keyless := output.WithHeldVK(output.EncodeHeldAs(heldNow, press(f, "LCtrl")), 0, output.MenuMaskVK)
	f.Add(append(append([]byte{2, byte(output.HeldSize)}, keyless...), 0x7F))
	f.Add(append(append([]byte{2, byte(output.HeldSize)}, keyless...), 0, fhDown, 0))
	f.Add([]byte{3, 0b1001, 0b1001001, fhDown, 0}) // another session's LCtrl and Z, down with the mask
	chords := make([]keys.Chord, len(fhChords))
	for i, s := range fhChords {
		chords[i] = keys.MustParse(s)
	}
	keyEvents := make([]output.KeyEvent, len(fhKeys))
	for i, s := range fhKeys {
		keyEvents[i] = press(f, s)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		next := func() byte {
			if len(data) == 0 {
				return 0
			}
			b := data[0]
			data = data[1:]
			return b
		}
		path := filepath.Join(t.TempDir(), "held.bin")
		mode, arg := next()%4, next()
		switch mode {
		case 1, 3:
			var evs []output.KeyEvent
			for i, e := range keyEvents {
				if arg&(1<<i) != 0 {
					evs = append(evs, e)
				}
			}
			id := heldNow
			if mode == 3 {
				id.Logon++
			}
			if err := os.WriteFile(path, output.EncodeHeldAs(id, evs...), 0o600); err != nil {
				t.Fatal(err)
			}
		case 2:
			n := min(int(arg)%(output.HeldSize+1), len(data))
			if err := os.WriteFile(path, data[:n], 0o600); err != nil {
				t.Fatal(err)
			}
			data = data[n:]
		}
		downBits := next()
		keyDown := func(e output.KeyEvent) bool {
			if output.IsMenuMask(e) {
				return downBits&(1<<6) != 0
			}
			i := slices.IndexFunc(keyEvents, func(k output.KeyEvent) bool { return k.ID() == e.ID() })
			return i >= 0 && downBits&(1<<i) != 0
		}
		fake := clock.NewFake()
		s := &fhSender{t: t, path: path}
		// The sweep's Sends answer as the first step says.
		if len(data) >= 2 {
			s.setResult(data[1])
		}
		l := output.StartLoop(fake, s, output.LoopOptions{HeldFile: path, KeyDown: keyDown, Identity: identityOf(heldNow)})
		if res := l.Swept(); mode == 3 && !errors.Is(res.Err, output.ErrLoopClosed) {
			if !res.Stale {
				t.Errorf("Swept() = %+v, want Stale", res)
			}
			for _, e := range res.Released {
				if !output.IsMenuMask(e) {
					t.Errorf("the sweep released %v of another session's log", e)
				}
			}
		}
		for step := 0; step < 64 && len(data) > 0; step++ {
			op, r := next(), next()
			s.setResult(r)
			c, o := chords[op/4%16], fhOwners[op/64]
			var cmd func(*output.Arbiter) error
			switch op % 4 {
			case fhDown:
				cmd = func(a *output.Arbiter) error { return a.Down(o, c) }
			case fhUp:
				if op/64 == 3 {
					fake.Advance(40 * ms)
					cmd = noop
				} else {
					cmd = func(a *output.Arbiter) error { return a.Up(o, c) }
				}
			case fhTap:
				hold := time.Duration(op/64%2) * 30 * ms
				cmd = func(a *output.Arbiter) error { return a.Tap(fhOwners[0], c, hold) }
			default:
				if op/64 == 3 {
					cmd = func(a *output.Arbiter) error {
						return a.ReleaseOwners(func(x output.Owner) bool { return x == fhOwners[1] })
					}
				} else {
					cmd = func(a *output.Arbiter) error { return a.ReleaseAll() }
				}
			}
			if err := l.Do(cmd); errors.Is(err, output.ErrLoopClosed) {
				break
			}
			s.mu.Lock()
			s.checkLog(fmt.Sprintf("step %d", step), s.down)
			s.mu.Unlock()
		}
		// Stop injects: the log keeps only what the retries left.
		s.setResult(0)
		stopErr := make(chan error, 1)
		go func() { stopErr <- l.Stop() }()
		for done := false; !done; {
			select {
			case err := <-stopErr:
				var pe *output.PanicError
				if errors.As(err, &pe) && pe.HeldErr != nil {
					t.Errorf("HeldErr = %v, want nil", pe.HeldErr)
				}
				done = true
			default:
				if d, ok := fake.Next(); ok {
					fake.Set(max(d, fake.Now()))
				} else {
					runtime.Gosched()
				}
			}
		}
		held := s.held()
		b, err := os.ReadFile(path)
		switch {
		case errors.Is(err, os.ErrNotExist):
			if len(held) > 0 {
				t.Errorf("no held-key log with %v down", held)
			}
		case err != nil:
			t.Fatal(err)
		default:
			logged, err := output.DecodeHeld(b)
			if err != nil {
				t.Fatalf("the log left: %v", err)
			}
			if len(logged) == 0 {
				t.Error("a held-key log with no key left, want it removed")
			}
			for _, id := range logged {
				if id == output.MenuMaskEvent(false).ID() || id.ByVK && id.VK == 0 {
					t.Errorf("the held-key log left has %v, which no key has", id)
				}
			}
			for _, id := range held {
				if !slices.Contains(logged, id) {
					t.Errorf("the held-key log left has %v, not %v, which is down", logged, id)
				}
			}
		}
	})
}
