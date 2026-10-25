package haptic

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

const dv Device = 1

var epoch = time.Unix(1_000_000, 0)

// sdlLag is how long after its expiry the fake still counts a rumble as
// running: SDL expires a rumble in SDL_UpdateJoysticks, which T-input runs
// every few milliseconds, so a request that comes right at the end of the
// previous one can still meet its strengths.
const sdlLag = 3 * time.Millisecond

// rig is a Mixer with a fake controller and a clock that the test moves.
type rig struct {
	t  *testing.T
	m  *Mixer
	f  *fakeEff
	ms int
}

// fakeEff is an Effector that records its calls as text, each with the
// time of the call, and models what SDL does with them.
type fakeEff struct {
	r     *rig
	calls []string
	// fail, when set, is asked about each call and may fail it; a failed call
	// changes nothing on the controller.
	fail func(call string) error

	low, high, left, right uint16
	expire, trigExpire     time.Time
	merged                 int // requests that SDL would take for the strengths it has
	endless                int // requests that SDL would never stop

	// ledSet and led are what the controller shows. SDL (SDL_SetJoystickLED)
	// keeps the color of every call, whether or not the driver call worked, and
	// drops a call for the same color inside sdlLEDRepeat of the end of the last
	// driver call, which ends ledLag after it started: sdl, sdlSet and sdlUntil.
	// A dropped call is counted in swallowed, and changes nothing.
	ledSet    bool
	led       RGB
	ledLag    time.Duration
	sdl       RGB
	sdlSet    bool
	sdlUntil  time.Time
	swallowed int

	player int
	reads  int          // PlayerIndex calls
	taken  map[int]bool // the player indices that other controllers hold
	// table, when set, replaces player and taken with SDL's shared table, in
	// which this controller has the id tableID.
	table   *playerTable
	tableID int

	// limits, when set, are the caps of the controller: a call it forbids
	// is a violation.
	limits     *Caps
	violations []string
}

var errBoom = errors.New("boom")

// sdlLagMs is more than sdlLag, in milliseconds.
const sdlLagMs = 10

// forbid records call as a violation when the controller cannot do it.
func (f *fakeEff) forbid(can func(Caps) bool, call string) {
	if f.limits != nil && !can(*f.limits) {
		f.violations = append(f.violations, fmt.Sprintf("@%d %s", f.r.ms, call))
	}
}

func newRig(t *testing.T, caps Caps) *rig {
	t.Helper()
	r := &rig{t: t, m: NewMixer()}
	r.f = &fakeEff{r: r, player: 2}
	r.m.Add(dv, r.f, caps)
	return r
}

func (r *rig) now() time.Time { return epoch.Add(time.Duration(r.ms) * time.Millisecond) }

// at moves the clock to ms and returns the time.
func (r *rig) at(ms int) time.Time { r.ms = ms; return r.now() }

// run advances the Mixer every millisecond after from, up to and including
// to, and fails the test on an error.
func (r *rig) run(from, to int) {
	r.t.Helper()
	for ms := from + 1; ms <= to; ms++ {
		if err := r.m.Advance(r.at(ms)); err != nil {
			r.t.Fatalf("Advance at %d ms: %v", ms, err)
		}
	}
}

// expect checks the calls since the last expect, and forgets them.
func (r *rig) expect(want ...string) {
	r.t.Helper()
	if !slices.Equal(r.f.calls, want) {
		r.t.Errorf("calls:\n  %s\nwant:\n  %s", strings.Join(r.f.calls, "\n  "), strings.Join(want, "\n  "))
	}
	r.f.calls = nil
}

// play plays steps as class c at ms; it fails the test on an error.
func (r *rig) play(c Class, steps []Step, ms int) Outcome {
	r.t.Helper()
	o, err := r.m.Play(dv, c, steps, r.at(ms))
	if err != nil {
		r.t.Fatalf("Play(%v) at %d ms: %v", c, ms, err)
	}
	return o
}

func (f *fakeEff) record(call string) error {
	f.calls = append(f.calls, fmt.Sprintf("@%d %s", f.r.ms, call))
	if f.fail != nil {
		return f.fail(call)
	}
	return nil
}

func (f *fakeEff) Rumble(low, high uint16, ms uint32) error {
	call := "rumble stop"
	if low|high != 0 || ms != 0 {
		call = fmt.Sprintf("rumble %d/%d %dms", low, high, ms)
	}
	f.forbid(func(c Caps) bool { return c.Rumble }, call)
	if low|high != 0 && (ms < 1 || ms > MaxStepMs) {
		f.endless++
	}
	if err := f.record(call); err != nil {
		return err
	}
	now := f.r.now()
	if f.motorsRunning(now) && low|high != 0 && low == f.low && high == f.high {
		f.merged++
	}
	f.low, f.high = low, high
	f.expire = now.Add(time.Duration(ms) * time.Millisecond)
	return nil
}

func (f *fakeEff) RumbleTriggers(left, right uint16, ms uint32) error {
	call := "trig stop"
	if left|right != 0 || ms != 0 {
		call = fmt.Sprintf("trig %d/%d %dms", left, right, ms)
	}
	f.forbid(func(c Caps) bool { return c.TriggerRumble }, call)
	if left|right != 0 && (ms < 1 || ms > MaxStepMs) {
		f.endless++
	}
	if err := f.record(call); err != nil {
		return err
	}
	now := f.r.now()
	if f.triggersRunning(now) && left|right != 0 && left == f.left && right == f.right {
		f.merged++
	}
	f.left, f.right = left, right
	f.trigExpire = now.Add(time.Duration(ms) * time.Millisecond)
	return nil
}

func (f *fakeEff) SetLED(r, g, b uint8) error {
	call := fmt.Sprintf("led #%02X%02X%02X", r, g, b)
	f.forbid(func(c Caps) bool { return c.LED }, call)
	color, now := RGB{r, g, b}, f.r.now()
	if f.sdlSet && f.sdl == color && now.Before(f.sdlUntil) {
		f.calls = append(f.calls, fmt.Sprintf("@%d %s", f.r.ms, call))
		f.swallowed++
		return nil
	}
	err := f.record(call)
	f.sdl, f.sdlSet, f.sdlUntil = color, true, now.Add(f.ledLag+sdlLEDRepeat)
	if err != nil {
		return err
	}
	f.ledSet, f.led = true, color
	return nil
}

func (f *fakeEff) SetPlayerIndex(i int) error {
	call := fmt.Sprintf("player %d", i)
	f.forbid(func(c Caps) bool { return c.PlayerLED }, call)
	if err := f.record(call); err != nil {
		return err
	}
	if f.table != nil {
		f.table.set(f.tableID, i)
		return nil
	}
	f.player = i
	return nil
}

func (f *fakeEff) PlayerIndex() int {
	f.forbid(func(c Caps) bool { return c.PlayerLED }, "player index read")
	f.reads++
	if f.table != nil {
		return f.table.indexOf(f.tableID)
	}
	return f.player
}

func (f *fakeEff) PlayerIndexTaken(index int) bool {
	f.forbid(func(c Caps) bool { return c.PlayerLED }, "player index table read")
	if f.table != nil {
		return f.table.holder(index) != 0 && f.table.holder(index) != f.tableID
	}
	return f.taken[index]
}

// playerTable is SDL's table of player indices, shared by all controllers
// (SDL_SetJoystickIDForPlayerIndex): giving an index to a controller empties
// the slot it had, and moves the controller that had the index to the first
// free slot. A slot holds a controller's id, or 0.
type playerTable struct{ slots []int }

func newPlayerTable(ids ...int) *playerTable { return &playerTable{slots: slices.Clone(ids)} }

func (t *playerTable) holder(index int) int {
	if index < 0 || index >= len(t.slots) {
		return 0
	}
	return t.slots[index]
}

func (t *playerTable) indexOf(id int) int {
	for i, s := range t.slots {
		if s == id {
			return i
		}
	}
	return -1
}

func (t *playerTable) firstFree() int {
	for i, s := range t.slots {
		if s == 0 {
			return i
		}
	}
	return len(t.slots)
}

func (t *playerTable) set(id, index int) {
	existing := t.holder(index)
	if index >= len(t.slots) {
		t.slots = append(t.slots, make([]int, index+1-len(t.slots))...)
	} else if index >= 0 && t.slots[index] == id {
		return
	}
	if old := t.indexOf(id); old >= 0 {
		t.slots[old] = 0
	}
	if index >= 0 {
		t.slots[index] = id
	}
	if existing > 0 {
		t.set(existing, t.firstFree())
	}
}

func (f *fakeEff) motorsRunning(now time.Time) bool {
	return f.low|f.high != 0 && now.Before(f.expire.Add(sdlLag))
}

func (f *fakeEff) triggersRunning(now time.Time) bool {
	return f.left|f.right != 0 && now.Before(f.trigExpire.Add(sdlLag))
}

func (f *fakeEff) quiet(now time.Time) bool { return !f.motorsRunning(now) && !f.triggersRunning(now) }

var (
	red   = RGB{0xFF, 0, 0}
	green = RGB{0, 0xFF, 0}
	blue  = RGB{0, 0, 0xFF}
	white = RGB{0xFF, 0xFF, 0xFF}

	allCaps = Caps{Rumble: true, TriggerRumble: true, LED: true, RGBLED: true, PlayerLED: true}
)

const m3Strength = "28672/28672" // 0x7000 on both motors

func TestMixerAmbientTrainMatchesM3(t *testing.T) {
	// The pulse plan of M3 (input/feedback_test.go): 3 pulses of 70 ms, 100 ms
	// apart, at 0x7000; the first at once, the next ones 170 ms after each
	// other. The Mixer also stops the motors when the last pulse ends, where
	// M3 did a gap later; the pulse is over by then either way.
	r := newRig(t, Caps{Rumble: true})
	if o := r.play(Ambient, Train(3, 70, 100, 0x7000), 0); o != Started {
		t.Fatalf("Play = %v, want started", o)
	}
	r.run(0, 600)
	r.expect(
		"@0 rumble "+m3Strength+" 70ms",
		"@170 rumble "+m3Strength+" 70ms",
		"@340 rumble "+m3Strength+" 70ms",
		"@410 rumble stop",
	)
	if r.f.merged != 0 || r.f.endless != 0 {
		t.Errorf("merged %d, endless %d requests", r.f.merged, r.f.endless)
	}

	// The defaults: one pulse of 70 ms at 0x7000 (TestFeedbackDefaults).
	r.play(Ambient, Train(0, 0, 0, 0), 1000)
	r.run(1000, 1200)
	r.expect("@1000 rumble "+m3Strength+" 70ms", "@1070 rumble stop")
}

func TestMixerPriority(t *testing.T) {
	r := newRig(t, Caps{Rumble: true})
	steps := Train(3, 70, 100, 0x7000)
	want := func(c Class, ms int, o Outcome) {
		t.Helper()
		if got := r.play(c, steps, ms); got != o {
			t.Errorf("%v at %d ms: %v, want %v", c, ms, got, o)
		}
	}

	want(Ambient, 0, Started)
	r.f.calls = nil
	// Events and scripts never interrupt an ambient pulse train, and a
	// request that is dropped does not touch the controller.
	want(Event, 10, Dropped)
	want(Script, 20, Dropped)
	r.expect()
	// The same class replaces, and the pulse that ran is stopped first.
	want(Ambient, 40, Started)
	r.expect("@40 rumble stop", "@40 rumble "+m3Strength+" 70ms")
	// A higher class preempts.
	want(Alert, 50, Started)
	r.expect("@50 rumble stop", "@50 rumble "+m3Strength+" 70ms")
	want(Event, 60, Dropped)
	want(Script, 60, Dropped)
	want(Ambient, 60, Dropped)
	want(Alert, 100, Started) // the same class again
	r.f.calls = nil
	want(Identify, 110, Started)
	want(Alert, 120, Dropped)
	want(Ambient, 120, Dropped)
	r.f.calls = nil
	// Whatever ran has ended, and a lower class may start again.
	r.run(120, 1000)
	r.f.calls = nil
	want(Event, 1000, Started)
	want(Script, 1010, Started) // a higher class than the event running
	want(Event, 1100, Dropped)
	r.f.calls = nil

	for _, c := range []Class{0, Identify + 1} {
		if _, err := r.m.Play(dv, c, steps, r.at(2000)); !errors.Is(err, ErrClass) {
			t.Errorf("Play(class %d) = %v, want ErrClass", c, err)
		}
	}
	if _, err := r.m.Play(99, Event, steps, r.at(2000)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("Play(unknown device) = %v, want ErrUnknownDevice", err)
	}
	if o := r.play(Event, nil, 2000); o != Empty {
		t.Errorf("Play(no steps) = %v, want empty", o)
	}
}

func TestMixerEventRateLimit(t *testing.T) {
	r := newRig(t, Caps{Rumble: true})
	tick := Tick.Steps(0x7000, 0, 0)
	for _, tc := range []struct {
		ms   int
		want Outcome
	}{
		{0, Started},
		{1, Limited},
		{29, Limited},
		{30, Started}, // a limited one did not move the window
		{59, Limited},
		{60, Started},
		{500, Started},
	} {
		r.run(r.ms, tc.ms)
		if got := r.play(Event, tick, tc.ms); got != tc.want {
			t.Errorf("event at %d ms: %v, want %v", tc.ms, got, tc.want)
		}
	}
	// The limit is for events: another class is not held to it.
	r.run(500, 600)
	r.play(Event, tick, 600)
	if got := r.play(Script, tick, 601); got != Started {
		t.Errorf("script right after an event: %v, want started", got)
	}
	// Each device has its own window.
	other := &fakeEff{r: r, player: -1}
	r.m.Add(2, other, Caps{Rumble: true})
	if o, err := r.m.Play(2, Event, tick, r.at(601)); o != Started || err != nil {
		t.Errorf("event on another device: %v, %v; want started", o, err)
	}
}

func TestMixerSeparatesIdenticalSteps(t *testing.T) {
	r := newRig(t, Caps{Rumble: true, TriggerRumble: true})
	one := Step{Low: 0x7000, High: 0x7000, Ms: 25}

	// Two identical steps in a row would merge into one buzz: SDL takes the
	// second for the strengths it has, and only extends the expiry.
	r.play(Event, []Step{one, one}, 0)
	r.run(0, 100)
	r.expect(
		"@0 rumble "+m3Strength+" 25ms",
		"@25 rumble stop", "@25 rumble "+m3Strength+" 25ms",
		"@50 rumble stop",
	)

	// So do two programs of the same strengths that follow each other closely.
	r.play(Event, []Step{one}, 1000)
	r.run(1000, 1024)
	r.play(Script, []Step{one}, 1025)
	r.run(1025, 1100)
	r.expect(
		"@1000 rumble "+m3Strength+" 25ms",
		"@1025 rumble stop", "@1025 rumble "+m3Strength+" 25ms",
		"@1050 rumble stop",
	)

	// A gap between them needs no zero of its own: the first ends by itself.
	r.play(Ambient, []Step{one, {Ms: 40}, one}, 2000)
	r.run(2000, 2200)
	r.expect(
		"@2000 rumble "+m3Strength+" 25ms",
		"@2065 rumble "+m3Strength+" 25ms",
		"@2090 rumble stop",
	)

	// The same on the triggers.
	trig := Step{LeftTrig: 0x4000, RightTrig: 0x4000, Ms: 30}
	r.play(Ambient, []Step{trig, trig}, 3000)
	r.run(3000, 3100)
	r.expect(
		"@3000 trig 16384/16384 30ms",
		"@3030 trig stop", "@3030 trig 16384/16384 30ms",
		"@3060 trig stop",
	)
	if r.f.merged != 0 || r.f.endless != 0 {
		t.Errorf("merged %d, endless %d requests", r.f.merged, r.f.endless)
	}
}

func TestMixerStepLimits(t *testing.T) {
	r := newRig(t, Caps{Rumble: true})
	// No step is endless (SDL: 0 ms means no expiry) or longer than MaxStepMs,
	// and a program has at most MaxSteps steps.
	r.play(Script, []Step{{Low: 1, Ms: 0}, {Low: 2, Ms: 60000}}, 0)
	r.run(0, 20)
	r.expect("@0 rumble 1/0 1ms", "@1 rumble stop", "@1 rumble 2/0 5000ms")
	r.run(20, 7000)
	r.expect("@5001 rumble stop")

	long := make([]Step, 3*MaxSteps)
	for i := range long {
		long[i] = Step{Low: uint16(i%2 + 1), Ms: 1}
	}
	r.play(Script, long, 10000)
	n := 0
	for ms := 10000; ms < 12000; ms++ {
		if err := r.m.Advance(r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range r.f.calls {
		if strings.Contains(c, "rumble 1/0") || strings.Contains(c, "rumble 2/0") {
			n++
		}
	}
	if n != MaxSteps {
		t.Errorf("%d steps ran, want MaxSteps = %d", n, MaxSteps)
	}
	if r.f.endless != 0 {
		t.Errorf("%d endless requests", r.f.endless)
	}
}

func TestMixerCapsGateEveryEffect(t *testing.T) {
	steps := []Step{{Low: 100, High: 100, LeftTrig: 100, RightTrig: 100, Ms: 50}}

	// A controller that cannot rumble gets none, and nothing is an error.
	r := newRig(t, Caps{})
	if o := r.play(Ambient, steps, 0); o != Unsupported {
		t.Errorf("Play on a controller without rumble: %v, want unsupported", o)
	}
	for name, f := range map[string]func() error{
		"SetLED":       func() error { return r.m.SetLED(dv, Ambient, red, r.at(1)) },
		"BlinkLED":     func() error { return r.m.BlinkLED(dv, Alert, Blink(red, 10, 10, 2), r.at(2)) },
		"ReleaseLED":   func() error { return r.m.ReleaseLED(dv, Ambient, r.at(3)) },
		"SetPlayer":    func() error { return r.m.SetPlayer(dv, Ambient, 1, r.at(4)) },
		"ReleasePlay.": func() error { return r.m.ReleasePlayer(dv, Ambient, r.at(5)) },
		"Stop":         func() error { return r.m.Stop(dv, r.at(6)) },
	} {
		if err := f(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	r.run(6, 100)
	r.expect()
	if r.f.reads != 0 {
		t.Errorf("read the player index of a controller without player lights")
	}

	// The triggers are left out of a program for a controller without them.
	r = newRig(t, Caps{Rumble: true})
	r.play(Event, steps, 0)
	r.run(0, 100)
	r.expect("@0 rumble 100/100 50ms", "@50 rumble stop")
	// And the motors for one that has only the triggers.
	r = newRig(t, Caps{TriggerRumble: true})
	r.play(Event, steps, 0)
	r.run(0, 100)
	r.expect("@0 trig 100/100 50ms", "@50 trig stop")
}

func TestMixerTriggerChannelFollowsTheProgram(t *testing.T) {
	r := newRig(t, Caps{Rumble: true, TriggerRumble: true})
	r.play(Script, []Step{{LeftTrig: 100, Ms: 1000}}, 0)
	r.expect("@0 trig 100/0 1000ms")
	// A program that does not use the triggers silences what it replaces.
	r.play(Script, []Step{{Low: 5, Ms: 20}}, 100)
	r.expect("@100 trig stop", "@100 rumble 5/0 20ms")
	r.run(100, 200)
	r.expect("@120 rumble stop")
}

func TestMixerLEDOwners(t *testing.T) {
	r := newRig(t, Caps{LED: true, RGBLED: true})
	set := func(c Class, color RGB, ms int) {
		t.Helper()
		if err := r.m.SetLED(dv, c, color, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	release := func(c Class, ms int) {
		t.Helper()
		if err := r.m.ReleaseLED(dv, c, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}

	// Identify > Alert > Script > Ambient. A claim below the shown one does
	// not touch the LED, and shows when the ones above it give up.
	set(Ambient, blue, 0)
	r.expect("@0 led #0000FF")
	set(Script, green, 1)
	r.expect("@1 led #00FF00")
	set(Alert, red, 2)
	r.expect("@2 led #FF0000")
	set(Identify, white, 3)
	r.expect("@3 led #FFFFFF")
	set(Ambient, red, 4) // under everything
	set(Script, blue, 5)
	r.expect()
	release(Identify, 6)
	r.expect("@6 led #FF0000") // the alert
	release(Alert, 7)
	r.expect("@7 led #0000FF") // the script's new color
	release(Script, 8)
	r.expect("@8 led #FF0000") // the ambient color
	set(Ambient, red, 9)       // the color it shows, asked for again: written again (TestMixerLEDResend)
	r.expect("@9 led #FF0000")
	release(Ambient, 10)
	r.expect("@10 led #000000") // padmate colored it, and nothing remains

	// A timed pattern shows over the claims below it and gives the underlying
	// color back by itself; its dark phases show what is under.
	set(Ambient, blue, 100)
	set(Script, green, 101)
	r.f.calls = nil
	if err := r.m.BlinkLED(dv, Alert, Blink(red, 100, 100, 2), r.at(200)); err != nil {
		t.Fatal(err)
	}
	r.run(200, 600)
	r.expect(
		"@200 led #FF0000",
		"@300 led #00FF00", // the dark phase: the script's color
		"@400 led #FF0000",
		"@500 led #00FF00", // the pattern ended
	)
	// Cancel gives up a pattern early, and the claim of a class only.
	if err := r.m.BlinkLED(dv, Identify, IdentifyFlashes(), r.at(1000)); err != nil {
		t.Fatal(err)
	}
	r.expect("@1000 led #FFFFFF")
	if err := r.m.Cancel(dv, Identify, r.at(1100)); err != nil {
		t.Fatal(err)
	}
	r.expect("@1100 led #00FF00")
	r.run(1100, 3000)
	r.expect()

	if err := r.m.SetLED(dv, Event, red, r.at(4000)); !errors.Is(err, ErrClass) {
		t.Errorf("SetLED(Event) = %v, want ErrClass", err)
	}
	if err := r.m.SetLED(99, Ambient, red, r.at(4000)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("SetLED(unknown device) = %v, want ErrUnknownDevice", err)
	}
}

func TestMixerLEDReleaseRules(t *testing.T) {
	for _, tc := range []struct {
		name      string
		caps      Caps
		colored   bool
		wantSet   []string // calls of the coloring
		wantFinal []string // calls of the release
	}{
		{"color LED that padmate colored", Caps{LED: true, RGBLED: true}, true, []string{"@0 led #FF0000"}, []string{"@1 led #000000"}},
		{"mono LED that padmate colored", Caps{LED: true}, true, []string{"@0 led #FF0000"}, nil},
		{"color LED that padmate never colored", Caps{LED: true, RGBLED: true}, false, nil, nil},
		{"no LED", Caps{Rumble: true}, true, nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.caps)
			if tc.colored {
				if err := r.m.SetLED(dv, Ambient, red, r.at(0)); err != nil {
					t.Fatal(err)
				}
			}
			r.expect(tc.wantSet...)
			if err := r.m.ReleaseLED(dv, Ambient, r.at(1)); err != nil {
				t.Fatal(err)
			}
			r.expect(tc.wantFinal...)
			// It is released once: nothing more to release.
			if err := r.m.ReleaseLED(dv, Ambient, r.at(2)); err != nil {
				t.Fatal(err)
			}
			if err := r.m.Stop(dv, r.at(3)); err != nil {
				t.Fatal(err)
			}
			r.expect()
		})
	}

	// Stop and a release that follows a color, on a color LED, black it.
	r := newRig(t, Caps{LED: true, RGBLED: true})
	if err := r.m.SetLED(dv, Script, green, r.at(0)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Stop(dv, r.at(1)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 led #00FF00", "@1 led #000000")
	// A color that is black itself is asked for like any other (and SDL drops it
	// as a repeat), but releasing it has nothing to write: it shows black.
	if err := r.m.SetLED(dv, Ambient, RGB{}, r.at(2)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.ReleaseLED(dv, Ambient, r.at(3)); err != nil {
		t.Fatal(err)
	}
	r.expect("@2 led #000000")
	if r.f.swallowed != 1 {
		t.Errorf("SDL dropped %d repeats, want the one black", r.f.swallowed)
	}
}

func TestMixerPlayerIndexRestore(t *testing.T) {
	r := newRig(t, Caps{PlayerLED: true})
	r.f.player = 2
	set := func(c Class, i, ms int) {
		t.Helper()
		if err := r.m.SetPlayer(dv, c, i, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.m.SetPlayer(dv, Event, 0, r.at(0)); !errors.Is(err, ErrClass) {
		t.Errorf("SetPlayer(Event) = %v, want ErrClass", err)
	}

	// The index SDL assigned is read before the first change, and comes back
	// when the last claim is released.
	set(Ambient, 0, 0)
	r.expect("@0 player 0")
	set(Ambient, 3, 10)
	r.expect("@10 player 3")
	set(Ambient, 3, 11)
	r.expect() // the controller still has the index: no write
	if r.f.reads != 2 {
		t.Errorf("%d reads of the player index, want two: before the first change, and to see that the index asked for again is still the controller's", r.f.reads)
	}
	set(Ambient, 1, 50)
	r.expect("@50 player 1")
	if r.f.reads != 2 {
		t.Errorf("%d reads of the player index, want two: a change of the index reads nothing", r.f.reads)
	}
	// A timed pattern shows over the ambient index and gives it back.
	if err := r.m.PlayerPattern(dv, Identify, IdentifyChase(), r.at(100)); err != nil {
		t.Fatal(err)
	}
	r.run(100, 2000)
	r.expect("@100 player 0", "@475 player 1", "@850 player 2", "@1225 player 3", "@1600 player 1")
	if err := r.m.ReleasePlayer(dv, Ambient, r.at(2100)); err != nil {
		t.Fatal(err)
	}
	r.expect("@2100 player 2")
	if err := r.m.ReleasePlayer(dv, Ambient, r.at(2101)); err != nil {
		t.Fatal(err)
	}
	r.expect() // already restored

	// The next change reads the index again.
	r.f.player = 1
	set(Script, 2, 3000)
	set(Alert, -1, 3001)
	set(Alert, 99, 3002) // out of range: the last light
	r.expect("@3000 player 2", "@3001 player -1", "@3002 player 7")
	if err := r.m.StopAll(r.at(3100)); err != nil {
		t.Fatal(err)
	}
	r.expect("@3100 player 1")
	if r.m.Has(dv) {
		t.Error("StopAll left the device added")
	}
}

// stopper is a way for the effects of a controller to end.
type stopper struct {
	name string
	stop func(r *rig, ms int) error
	has  bool // whether the device is still added
}

func TestMixerStopsEveryEffectOnEveryPath(t *testing.T) {
	for _, tc := range []stopper{
		{"Stop", func(r *rig, ms int) error { return r.m.Stop(dv, r.at(ms)) }, true},
		// A removed pad, and a pad that the receiver watchdog lost (dormant).
		{"Remove", func(r *rig, ms int) error { return r.m.Remove(dv, r.at(ms)) }, false},
		{"StopAll", func(r *rig, ms int) error { return r.m.StopAll(r.at(ms)) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, allCaps)
			r.f.player = 1
			long := []Step{{Low: 9, High: 9, LeftTrig: 9, RightTrig: 9, Ms: 3000}}
			r.play(Ambient, long, 0)
			if err := r.m.SetLED(dv, Ambient, red, r.at(1)); err != nil {
				t.Fatal(err)
			}
			if err := r.m.SetPlayer(dv, Ambient, 0, r.at(2)); err != nil {
				t.Fatal(err)
			}
			if err := r.m.BlinkLED(dv, Alert, Blink(green, 1000, 1000, 3), r.at(3)); err != nil {
				t.Fatal(err)
			}
			if !r.f.motorsRunning(r.at(10)) || !r.f.triggersRunning(r.at(10)) {
				t.Fatal("the program does not run")
			}
			if err := tc.stop(r, 50); err != nil {
				t.Fatal(err)
			}
			// At once, with no time for anything to expire by itself:
			if !r.f.quiet(r.at(50)) {
				t.Errorf("%s: the motors still run: %+v", tc.name, r.f)
			}
			if r.f.led != (RGB{}) {
				t.Errorf("%s: the LED is %v, want released (#000000)", tc.name, r.f.led)
			}
			if r.f.player != 1 {
				t.Errorf("%s: player index %d, want the original 1", tc.name, r.f.player)
			}
			if r.m.Has(dv) != tc.has {
				t.Errorf("%s: Has = %v, want %v", tc.name, r.m.Has(dv), tc.has)
			}
			// And nothing comes back later: no pattern, no program.
			r.f.calls = nil
			r.run(50, 10000)
			r.expect()
		})
	}

	t.Run("error cooldown", func(t *testing.T) {
		r := newRig(t, allCaps)
		r.f.player = 1
		// Each group of effects has a change due at 1003 ms: the second step of
		// the program, the end of the LED pattern, the second player light.
		program := []Step{{Low: 9, High: 9, Ms: 1003}, {Low: 8, High: 8, Ms: 2000}}
		r.play(Ambient, program, 0)
		if err := r.m.SetLED(dv, Ambient, red, r.at(1)); err != nil {
			t.Fatal(err)
		}
		if err := r.m.SetPlayer(dv, Ambient, 0, r.at(2)); err != nil {
			t.Fatal(err)
		}
		if err := r.m.BlinkLED(dv, Alert, Blink(green, 1000, 1000, 3), r.at(3)); err != nil {
			t.Fatal(err)
		}
		if err := r.m.PlayerPattern(dv, Alert, []PlayerPhase{{Value: 2, Show: true, Ms: 1000}, {Value: 3, Show: true, Ms: 1000}}, r.at(3)); err != nil {
			t.Fatal(err)
		}
		boom := errors.New("hid_write: timeout")
		r.f.fail = func(string) error { return boom }
		r.f.calls = nil
		// The three calls fail: the patterns and the program are dropped (the
		// motors stop by themselves at the end of their step), and no call is made
		// for the cooldowns.
		if err := r.m.Advance(r.at(1000)); err != nil {
			t.Fatalf("Advance at 1000 ms: %v (nothing is due yet)", err)
		}
		err := r.m.Advance(r.at(1003))
		if !errors.Is(err, boom) {
			t.Fatalf("Advance at 1003 ms: %v, want the failure of the controller", err)
		}
		if n := strings.Count(err.Error(), "left alone"); n != 3 {
			t.Errorf("Advance at 1003 ms: %d groups left alone, want the motors, the LED and the player lights: %v", n, err)
		}
		failed := len(r.f.calls)
		r.f.fail = nil
		r.run(1003, 1003+int(ErrorCooldown/time.Millisecond)-1)
		if len(r.f.calls) != failed {
			t.Errorf("calls during the cooldowns:\n  %s", strings.Join(r.f.calls[failed:], "\n  "))
		}
		// When the motors' and the player lights' is over, the static claim of the
		// player lights is applied again, and the motors have stopped on their own.
		// The LED waits for its longer one.
		r.f.calls = nil
		r.run(1003+int(ErrorCooldown/time.Millisecond)-1, 1003+int(ErrorCooldown/time.Millisecond)+1)
		r.expect("@6003 player 0")
		if !r.f.quiet(r.now()) {
			t.Errorf("the motors still run after the cooldown: %+v", r.f)
		}
		r.run(1003+int(ErrorCooldown/time.Millisecond)+1, 1003+int(LEDCooldown/time.Millisecond)+1)
		if r.f.led != red || r.f.player != 0 {
			t.Errorf("after the cooldowns: LED %v, player %d; want the ambient claims, red and 0", r.f.led, r.f.player)
		}
		if err := r.m.StopAll(r.now()); err != nil {
			t.Fatal(err)
		}
		if r.f.led != (RGB{}) || r.f.player != 1 || !r.f.quiet(r.now()) {
			t.Errorf("after StopAll: LED %v, player %d, motors %+v; want released, 1, still", r.f.led, r.f.player, r.f)
		}
	})
}

// A controller that SDL has removed is forgotten without a call: its player
// index above all must not be written (SDL would keep the dead instance id in
// the table of player indices for good).
func TestMixerForget(t *testing.T) {
	r := newRig(t, allCaps)
	r.f.player = 1
	long := []Step{{Low: 9, High: 9, LeftTrig: 9, RightTrig: 9, Ms: 3000}}
	r.play(Ambient, long, 0)
	if err := r.m.SetLED(dv, Ambient, red, r.at(1)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetPlayer(dv, Ambient, 0, r.at(2)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.BlinkLED(dv, Alert, Blink(green, 1000, 1000, 3), r.at(3)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.PlayerPattern(dv, Identify, IdentifyChase(), r.at(4)); err != nil {
		t.Fatal(err)
	}
	r.f.calls = nil

	r.m.Forget(dv)
	r.m.Forget(dv)      // again: nothing
	r.m.Forget(dv + 99) // never added: nothing
	r.expect()
	if r.m.Has(dv) {
		t.Fatal("Forget left the device added")
	}
	if _, err := r.m.Play(dv, Ambient, long, r.at(10)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("Play on the forgotten device = %v, want ErrUnknownDevice", err)
	}
	if err := r.m.SetPlayer(dv, Ambient, 2, r.at(11)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("SetPlayer on the forgotten device = %v, want ErrUnknownDevice", err)
	}
	// Nothing runs on it or comes back later: no program, no pattern, no
	// restore, however long the Mixer is advanced and however it is stopped.
	r.run(11, 10000)
	if err := r.m.Stop(dv, r.at(10001)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Remove(dv, r.at(10002)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.StopAll(r.at(10003)); err != nil {
		t.Fatal(err)
	}
	r.expect()
	if r.f.player != 0 {
		t.Errorf("player index %d, want 0: the claim of the forgotten controller is left as it was", r.f.player)
	}

	// The same id added again starts from nothing: no claim, no program, and
	// no original index to restore.
	r.m.Add(dv, r.f, allCaps)
	r.run(10003, 20000)
	if err := r.m.Stop(dv, r.at(20001)); err != nil {
		t.Fatal(err)
	}
	r.expect()
}

// Forget leaves the other controllers alone.
func TestMixerForgetOneOfTwo(t *testing.T) {
	r := newRig(t, Caps{PlayerLED: true})
	tbl := newPlayerTable(1, 2)
	r.f.table, r.f.tableID = tbl, 1
	other := &fakeEff{r: r, table: tbl, tableID: 2}
	r.m.Add(2, other, Caps{PlayerLED: true})
	if err := r.m.SetPlayer(2, Ambient, 3, r.at(0)); err != nil {
		t.Fatal(err)
	}
	other.calls = nil
	r.m.Forget(dv)
	if !r.m.Has(2) || r.m.Has(dv) {
		t.Fatalf("Has(1) = %v, Has(2) = %v; want only 2", r.m.Has(dv), r.m.Has(2))
	}
	if err := r.m.StopAll(r.at(10)); err != nil {
		t.Fatal(err)
	}
	if want := []string{"@10 player 1"}; !slices.Equal(other.calls, want) {
		t.Errorf("the other controller's calls %v, want its own restore %v", other.calls, want)
	}
	r.expect()
}

func TestMixerErrorCooldown(t *testing.T) {
	boom := errors.New("write failed")
	steps := Train(3, 70, 100, 0x7000)

	// The motors fail: the error names the device and the group, and starts
	// the cooldown of the motors; nothing reaches them for ErrorCooldown. The
	// LED and the player lights are other groups and go on.
	r := newRig(t, allCaps)
	r.f.fail = func(call string) error {
		if strings.Contains(call, "rumble ") {
			return boom
		}
		return nil
	}
	o, err := r.m.Play(dv, Ambient, steps, r.at(0))
	if !errors.Is(err, boom) || o != Cooling || !strings.Contains(err.Error(), "device 1") || !strings.Contains(err.Error(), "motors") {
		t.Fatalf("Play = %v, %v; want cooling and the failure of the motors of device 1", o, err)
	}
	r.expect("@0 rumble " + m3Strength + " 70ms")
	r.f.fail = nil
	if o, err := r.m.Play(dv, Alert, steps, r.at(100)); o != Cooling || err != nil {
		t.Errorf("Play in the cooldown = %v, %v; want cooling", o, err)
	}
	if err := r.m.SetLED(dv, Ambient, red, r.at(200)); err != nil {
		t.Errorf("SetLED in the motors' cooldown: %v", err)
	}
	if err := r.m.SetPlayer(dv, Ambient, 0, r.at(300)); err != nil {
		t.Errorf("SetPlayer in the motors' cooldown: %v", err)
	}
	r.expect("@200 led #FF0000", "@300 player 0")
	r.run(300, 4999)
	r.expect()
	// Stop writes nothing to the motors that failed, and still lets go of the
	// LED and the player lights.
	if err := r.m.Stop(dv, r.at(4999)); err != nil {
		t.Errorf("Stop in the cooldown: %v", err)
	}
	r.expect("@4999 led #000000", "@4999 player 2")
	r.run(4999, 5100)
	r.expect()
	if o, err := r.m.Play(dv, Event, steps, r.at(5100)); o != Started || err != nil {
		t.Errorf("Play after the cooldown = %v, %v; want started", o, err)
	}

	// The LED fails: the claims made meanwhile are kept, and the strongest
	// shows when its (longer) cooldown ends. The motors and the player lights
	// are not in it.
	r = newRig(t, allCaps)
	if err := r.m.SetLED(dv, Ambient, red, r.at(0)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 led #FF0000")
	r.f.fail = func(call string) error {
		if strings.Contains(call, "led") {
			return boom
		}
		return nil
	}
	err = r.m.SetLED(dv, Script, green, r.at(1000))
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "LED") {
		t.Fatalf("SetLED = %v, want the failure of the LED", err)
	}
	r.expect("@1000 led #00FF00")
	r.f.fail = nil
	if err := r.m.SetLED(dv, Alert, blue, r.at(2000)); err != nil {
		t.Fatal(err)
	}
	if o, err := r.m.Play(dv, Event, steps, r.at(2100)); o != Started || err != nil {
		t.Errorf("Play in the LED's cooldown = %v, %v; want started", o, err)
	}
	r.expect("@2100 rumble " + m3Strength + " 70ms")
	ledCool := 1000 + int(LEDCooldown/time.Millisecond)
	r.run(2100, ledCool-1)
	// The pulses of the train play to their end, the LED being in its cooldown.
	r.expect("@2270 rumble "+m3Strength+" 70ms", "@2440 rumble "+m3Strength+" 70ms", "@2510 rumble stop")
	r.run(ledCool-1, ledCool+1)
	r.expect(fmt.Sprintf("@%d led #0000FF", ledCool)) // the strongest claim of the moment
	if r.f.led != blue {
		t.Errorf("the LED shows %v, want the claim of the alert", r.f.led)
	}

	// The player lights fail: the same, for them only.
	r = newRig(t, allCaps)
	r.f.fail = func(call string) error {
		if strings.Contains(call, "player ") {
			return boom
		}
		return nil
	}
	err = r.m.SetPlayer(dv, Ambient, 0, r.at(0))
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "player lights") {
		t.Fatalf("SetPlayer = %v, want the failure of the player lights", err)
	}
	r.f.fail = nil
	if err := r.m.SetLED(dv, Ambient, red, r.at(10)); err != nil {
		t.Errorf("SetLED in the player lights' cooldown: %v", err)
	}
	if o, err := r.m.Play(dv, Event, steps, r.at(20)); o != Started || err != nil {
		t.Errorf("Play in the player lights' cooldown = %v, %v; want started", o, err)
	}
	r.expect("@0 player 0", "@10 led #FF0000", "@20 rumble "+m3Strength+" 70ms")
	r.run(20, int(ErrorCooldown/time.Millisecond)+1)
	r.expect("@190 rumble "+m3Strength+" 70ms", "@360 rumble "+m3Strength+" 70ms", "@430 rumble stop", "@5000 player 0")

	// A call that fails while the Mixer advances is the same: Advance returns
	// it, the program is not resumed after the cooldown.
	r = newRig(t, Caps{Rumble: true})
	r.play(Ambient, steps, 0)
	r.f.calls = nil
	r.f.fail = func(call string) error {
		if strings.Contains(call, "70ms") {
			return boom
		}
		return nil
	}
	r.run(0, 169)
	if err := r.m.Advance(r.at(170)); !errors.Is(err, boom) {
		t.Fatalf("Advance = %v, want the failure of the second pulse", err)
	}
	r.f.fail = nil
	r.f.calls = nil
	r.run(170, 10000)
	r.expect()

	// Identify is an explicit action of the user: it tries again, whichever
	// group is in its cooldown.
	r = newRig(t, allCaps)
	r.f.fail = func(string) error { return boom }
	if _, err := r.m.Play(dv, Event, steps, r.at(0)); err == nil {
		t.Fatal("Play did not fail")
	}
	if err := r.m.SetLED(dv, Ambient, red, r.at(1)); err == nil {
		t.Fatal("SetLED did not fail")
	}
	r.f.fail = nil
	if o, _ := r.m.Play(dv, Event, steps, r.at(100)); o != Cooling {
		t.Fatalf("Play in the cooldown = %v, want cooling", o)
	}
	cues, err := r.m.Identify(dv, r.at(200))
	if err != nil || cues != (Cues{Rumble: true, LED: true}) {
		t.Fatalf("Identify = %+v, %v", cues, err)
	}
	r.expect(
		"@0 rumble "+m3Strength+" 70ms", "@1 led #FF0000",
		"@200 rumble "+m3Strength+" 150ms", "@200 led #FFFFFF",
	)
}

func TestMixerLEDFailureKeepsThePulses(t *testing.T) {
	// A controller whose HOME-LED write fails (a receiver that does not take the
	// subcommand) still gives the pulses of a layer: the Nova in Switch mode.
	// Before the cooldowns were separate, the failing LED silenced it.
	r := newRig(t, Caps{Rumble: true, LED: true})
	boom := errors.New("hid_write: timeout")
	r.f.fail = func(call string) error {
		if strings.Contains(call, "led") {
			return boom
		}
		return nil
	}
	// What the engine asks of the Source on a layer change: the color, then the
	// pulses.
	layer := func(ms int, color RGB) {
		t.Helper()
		err := r.m.SetLED(dv, Ambient, color, r.at(ms))
		if ms == 0 && !errors.Is(err, boom) {
			t.Fatalf("SetLED = %v, want the failure of the LED", err)
		}
		if o, err := r.m.Play(dv, Ambient, Train(2, 70, 100, 0x7000), r.at(ms)); o != Started || err != nil {
			t.Fatalf("Play at %d ms = %v, %v; want started: the LED's failure is not the motors'", ms, o, err)
		}
	}
	layer(0, red)
	r.run(0, 400)
	layer(500, green) // in the LED's cooldown: no LED write, the pulses play
	r.run(500, 900)
	r.expect(
		"@0 led #FF0000",
		"@0 rumble "+m3Strength+" 70ms", "@170 rumble "+m3Strength+" 70ms", "@240 rumble stop",
		"@500 rumble "+m3Strength+" 70ms", "@670 rumble "+m3Strength+" 70ms", "@740 rumble stop",
	)
}

func TestMixerLEDRetryOutlastsSDLWindow(t *testing.T) {
	// SDL keeps the color of a failed write too, and drops the same color for
	// sdlLEDRepeat after the call returned. A write that timed out for 1.5 s and
	// is retried when the cooldown ends must not be taken for a repeat.
	r := newRig(t, Caps{LED: true, RGBLED: true})
	r.f.ledLag = 1500 * time.Millisecond
	fail := true
	r.f.fail = func(call string) error {
		if fail && strings.Contains(call, "led") {
			return errBoom
		}
		return nil
	}
	if err := r.m.SetLED(dv, Ambient, red, r.at(0)); !errors.Is(err, errBoom) {
		t.Fatalf("SetLED = %v, want the failure", err)
	}
	fail = false
	end := int(LEDCooldown / time.Millisecond)
	r.run(0, end+10)
	r.expect("@0 led #FF0000", fmt.Sprintf("@%d led #FF0000", end))
	if r.f.swallowed != 0 || !r.f.ledSet || r.f.led != red {
		t.Errorf("the retry was dropped as a repeat (%d dropped), the LED shows %v (set %v); want red", r.f.swallowed, r.f.led, r.f.ledSet)
	}
}

func TestMixerLEDResend(t *testing.T) {
	r := newRig(t, Caps{LED: true, RGBLED: true})
	set := func(c Class, color RGB, ms int) {
		t.Helper()
		if err := r.m.SetLED(dv, c, color, r.at(ms)); err != nil {
			t.Fatal(err)
		}
	}
	// A request for the color the LED shows is written again, as M3 did on
	// every layer change and refresh; SDL drops it inside its window, and sends
	// it after, which brings back a light that was reset behind our back.
	set(Ambient, red, 0)
	set(Ambient, red, 10)
	set(Ambient, red, int(sdlLEDRepeat/time.Millisecond)+20)
	r.expect("@0 led #FF0000", "@10 led #FF0000", "@5020 led #FF0000")
	if r.f.swallowed != 1 {
		t.Errorf("SDL dropped %d repeats, want the one inside its window", r.f.swallowed)
	}

	// A claim below the one that shows is not what the LED shows: no write.
	set(Alert, green, 6000)
	set(Ambient, red, 6010)
	r.expect("@6000 led #00FF00")
	// The claim of the owner that shows is. A release is not a request.
	set(Alert, green, 6020)
	r.expect("@6020 led #00FF00")
	if err := r.m.ReleaseLED(dv, Alert, r.at(6030)); err != nil {
		t.Fatal(err)
	}
	r.expect("@6030 led #FF0000")
	if err := r.m.ReleaseLED(dv, Alert, r.at(6040)); err != nil {
		t.Fatal(err)
	}
	r.expect()

	// A pattern that moves on is not a request: the phase that reveals the color
	// already shown writes nothing. Its start is one, for its first color.
	r = newRig(t, Caps{LED: true, RGBLED: true})
	set(Ambient, red, 0)
	if err := r.m.BlinkLED(dv, Alert, Blink(red, 100, 100, 3), r.at(200)); err != nil {
		t.Fatal(err)
	}
	r.run(200, 1000)
	r.expect("@0 led #FF0000", "@200 led #FF0000")

	// A pattern of another color gives the color below back by itself, one
	// write for every change of what shows.
	r = newRig(t, Caps{LED: true, RGBLED: true})
	set(Ambient, red, 0)
	if err := r.m.BlinkLED(dv, Alert, Blink(white, 100, 100, 2), r.at(200)); err != nil {
		t.Fatal(err)
	}
	r.run(200, 1000)
	r.expect("@0 led #FF0000", "@200 led #FFFFFF", "@300 led #FF0000", "@400 led #FFFFFF", "@500 led #FF0000")
}

func TestMixerStopWithOneGroupCooling(t *testing.T) {
	// The groups end on their own: the one in its cooldown is not written to,
	// the others are let go of.
	boom := errors.New("write failed")
	for _, tc := range []struct {
		name string
		fail string // the calls that fail
		want []string
	}{
		{"motors", "rumble ", []string{"@100 led #000000", "@100 player 2"}},
		{"LED", "led #", []string{"@100 rumble stop", "@100 player 2"}},
		{"player lights", "player ", []string{"@100 rumble stop", "@100 led #000000"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, allCaps)
			r.f.player = 2
			steps := []Step{{Low: 5, High: 5, Ms: 3000}}
			r.play(Ambient, steps, 0)
			_ = r.m.SetLED(dv, Ambient, red, r.at(1))
			_ = r.m.SetPlayer(dv, Ambient, 0, r.at(2))
			r.f.calls = nil
			// The failing group gets one failing call, and is cooling after it.
			r.f.fail = func(call string) error {
				if strings.Contains(call, tc.fail) {
					return boom
				}
				return nil
			}
			switch tc.name {
			case "motors":
				_, _ = r.m.Play(dv, Ambient, steps, r.at(50))
			case "LED":
				_ = r.m.SetLED(dv, Ambient, green, r.at(50))
			case "player lights":
				_ = r.m.SetPlayer(dv, Ambient, 1, r.at(50))
			}
			r.f.fail = nil
			r.f.calls = nil
			if err := r.m.Stop(dv, r.at(100)); err != nil {
				t.Fatal(err)
			}
			r.expect(tc.want...)
		})
	}
}

func TestMixerCancel(t *testing.T) {
	r := newRig(t, allCaps)
	r.play(Ambient, []Step{{Low: 7, Ms: 1000}}, 0)
	if err := r.m.SetLED(dv, Ambient, red, r.at(1)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetLED(dv, Script, green, r.at(2)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.SetPlayer(dv, Ambient, 0, r.at(3)); err != nil {
		t.Fatal(err)
	}
	r.f.calls = nil

	// Another class's cancel changes nothing of the ambient state.
	if err := r.m.Cancel(dv, Event, r.at(10)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Cancel(dv, Alert, r.at(11)); err != nil {
		t.Fatal(err)
	}
	r.expect()
	// The ambient cancel ends the program and its two claims; the script's
	// color shows.
	if err := r.m.Cancel(dv, Ambient, r.at(20)); err != nil {
		t.Fatal(err)
	}
	r.expect("@20 rumble stop", "@20 player 2")
	if err := r.m.Cancel(dv, Script, r.at(30)); err != nil {
		t.Fatal(err)
	}
	r.expect("@30 led #000000")
	if err := r.m.Cancel(dv, 0, r.at(40)); !errors.Is(err, ErrClass) {
		t.Errorf("Cancel(class 0) = %v, want ErrClass", err)
	}
	if err := r.m.Cancel(99, Ambient, r.at(40)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("Cancel(unknown device) = %v, want ErrUnknownDevice", err)
	}
	if err := r.m.Remove(99, r.at(40)); err != nil {
		t.Errorf("Remove(unknown device) = %v, want nothing to do", err)
	}
}

// StopRumble ends the rumble program of its class and leaves the class's LED
// and player claims: a script that ends its buzz keeps the color it set.
func TestMixerStopRumble(t *testing.T) {
	r := newRig(t, allCaps)
	r.play(Script, []Step{{Low: 7, Ms: 1000}}, 0)
	if err := r.m.SetLED(dv, Script, green, r.at(1)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 rumble 7/0 1000ms", "@1 led #00FF00")

	// Another class's program is not the script's to stop.
	if err := r.m.StopRumble(dv, Event, r.at(10)); err != nil {
		t.Fatal(err)
	}
	if err := r.m.StopRumble(dv, Ambient, r.at(11)); err != nil {
		t.Fatal(err)
	}
	r.expect()
	if err := r.m.StopRumble(dv, Script, r.at(20)); err != nil {
		t.Fatal(err)
	}
	r.expect("@20 rumble stop")
	// Nothing runs any more, and the color is still the script's.
	if err := r.m.StopRumble(dv, Script, r.at(30)); err != nil {
		t.Fatal(err)
	}
	r.expect()
	if err := r.m.Cancel(dv, Script, r.at(40)); err != nil {
		t.Fatal(err)
	}
	r.expect("@40 led #000000")

	if err := r.m.StopRumble(dv, 0, r.at(50)); !errors.Is(err, ErrClass) {
		t.Errorf("StopRumble(class 0) = %v, want ErrClass", err)
	}
	if err := r.m.StopRumble(99, Script, r.at(50)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("StopRumble(unknown device) = %v, want ErrUnknownDevice", err)
	}
}

func TestMixerIdentify(t *testing.T) {
	// A color LED with rumble: three buzzes and three white flashes, and the
	// layer color comes back after every flash.
	r := newRig(t, Caps{Rumble: true, LED: true, RGBLED: true, PlayerLED: true})
	if err := r.m.SetLED(dv, Ambient, red, r.at(0)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 led #FF0000")
	cues, err := r.m.Identify(dv, r.at(100))
	if err != nil || cues != (Cues{Rumble: true, LED: true}) {
		t.Fatalf("Identify = %+v, %v; want a buzz and a flash (the player lights only when there is no color LED)", cues, err)
	}
	r.run(100, 1500)
	r.expect(
		"@100 rumble "+m3Strength+" 150ms", "@100 led #FFFFFF",
		"@350 led #FF0000",
		"@400 rumble "+m3Strength+" 150ms",
		"@600 led #FFFFFF",
		"@700 rumble "+m3Strength+" 150ms",
		"@850 rumble stop", "@850 led #FF0000",
		"@1100 led #FFFFFF",
		"@1350 led #FF0000",
	)

	// Only player lights: a chase, and the index comes back.
	r = newRig(t, Caps{PlayerLED: true})
	r.f.player = 3
	if cues, err = r.m.Identify(dv, r.at(0)); err != nil || cues != (Cues{Player: true}) {
		t.Fatalf("Identify = %+v, %v; want the player lights", cues, err)
	}
	r.run(0, 2000)
	r.expect("@0 player 0", "@375 player 1", "@750 player 2", "@1125 player 3", "@1500 player 3")

	// A mono LED gives no cue; nor does a pad that can do nothing.
	for _, caps := range []Caps{{LED: true}, {}} {
		r = newRig(t, caps)
		if cues, err = r.m.Identify(dv, r.at(0)); err != nil || cues.Any() {
			t.Errorf("Identify on %+v = %+v, %v; want no cue", caps, cues, err)
		}
		r.run(0, 2000)
		r.expect()
	}
	if _, err := r.m.Identify(99, r.at(0)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("Identify(unknown device) = %v, want ErrUnknownDevice", err)
	}

	// It wins over the rest: it takes the motors from an ambient train.
	r = newRig(t, Caps{Rumble: true})
	r.play(Ambient, Train(3, 70, 100, 0x7000), 0)
	r.f.calls = nil
	if _, err := r.m.Identify(dv, r.at(10)); err != nil {
		t.Fatal(err)
	}
	r.expect("@10 rumble stop", "@10 rumble "+m3Strength+" 150ms")
}

func TestCuesFor(t *testing.T) {
	for _, tc := range []struct {
		caps Caps
		want Cues
	}{
		{Caps{}, Cues{}},
		{Caps{Rumble: true}, Cues{Rumble: true}},
		{Caps{LED: true}, Cues{}},
		{Caps{LED: true, RGBLED: true}, Cues{LED: true}},
		{Caps{PlayerLED: true}, Cues{Player: true}},
		{Caps{LED: true, RGBLED: true, PlayerLED: true}, Cues{LED: true}},
		{Caps{TriggerRumble: true}, Cues{}},
		{allCaps, Cues{Rumble: true, LED: true}},
	} {
		if got := CuesFor(tc.caps); got != tc.want {
			t.Errorf("CuesFor(%+v) = %+v, want %+v", tc.caps, got, tc.want)
		}
	}
}

func TestPlayerTableModel(t *testing.T) {
	// The model reproduces what the code of SDL does (SDL_joystick.c): with
	// three controllers at 0, 1 and 2, a chase of the first over all four
	// lights and back leaves the second and the third swapped.
	tbl := newPlayerTable(1, 2, 3)
	for _, p := range IdentifyChase() {
		tbl.set(1, p.Value)
	}
	tbl.set(1, 0)
	if want := []int{1, 3, 2, 0}; !slices.Equal(tbl.slots, want) {
		t.Errorf("table after the full chase = %v, want %v", tbl.slots, want)
	}
	// Two controllers come back to where they were by luck.
	tbl = newPlayerTable(1, 2)
	for _, p := range IdentifyChase() {
		tbl.set(1, p.Value)
	}
	tbl.set(1, 0)
	if want := []int{1, 2, 0, 0}; !slices.Equal(tbl.slots, want) {
		t.Errorf("table of two after the full chase = %v, want %v", tbl.slots, want)
	}
}

func TestMixerIdentifyChaseLeavesOtherControllersAlone(t *testing.T) {
	// Pads with player lights and no color LED (the Switch protocol) share
	// SDL's table of player indices. The chase may light only the indices that no
	// other pad holds, or it renumbers them (TestPlayerTableModel).
	for _, tc := range []struct {
		name  string
		pads  int
		chase []int // the lights of the first pad's chase; none: no cue
	}{
		{"alone", 1, []int{0, 1, 2, 3}},
		{"two pads", 2, []int{0, 2, 3}},
		{"three pads", 3, []int{0, 3}},
		{"four pads", 4, nil},
		{"five pads", 5, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, Caps{PlayerLED: true})
			ids := make([]int, tc.pads)
			for i := range ids {
				ids[i] = i + 1
			}
			tbl := newPlayerTable(ids...)
			r.f.table, r.f.tableID = tbl, 1
			others := make([]*fakeEff, 0, tc.pads)
			for i := 1; i < tc.pads; i++ {
				e := &fakeEff{r: r, table: tbl, tableID: i + 1}
				r.m.Add(Device(i+1), e, Caps{PlayerLED: true})
				others = append(others, e)
			}

			cues, err := r.m.Identify(dv, r.at(0))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cues.Player, len(tc.chase) >= 2; got != want || cues.Rumble || cues.LED {
				t.Errorf("cues = %+v, want only Player = %v", cues, want)
			}
			// At every millisecond, nobody else has moved.
			for ms := 1; ms <= 2000; ms++ {
				if err := r.m.Advance(r.at(ms)); err != nil {
					t.Fatal(err)
				}
				for i := 1; i < tc.pads; i++ {
					if got := tbl.indexOf(i + 1); got != i {
						t.Fatalf("at %d ms pad %d has index %d, want %d", ms, i+1, got, i)
					}
				}
			}
			// The first pad has its index again (SDL's table only grows).
			for i := range ids {
				if got := tbl.indexOf(i + 1); got != i {
					t.Errorf("pad %d has index %d after the chase, want %d", i+1, got, i)
				}
			}
			var lit []string
			for _, c := range r.f.calls {
				lit = append(lit, c[strings.Index(c, " ")+1:])
			}
			var want []string // the lights in turn, then the index it had comes back
			if len(tc.chase) >= 2 {
				for _, i := range tc.chase {
					want = append(want, fmt.Sprintf("player %d", i))
				}
				want = append(want, "player 0")
			}
			if !slices.Equal(lit, want) {
				t.Errorf("writes of the first pad: %v, want %v", lit, want)
			}
			for _, e := range others {
				if len(e.calls) != 0 {
					t.Errorf("another pad was written to: %v", e.calls)
				}
			}
		})
	}
}

func TestAlertPatterns(t *testing.T) {
	flashes := AlertFlashes()
	if len(flashes) != 5 {
		t.Fatalf("AlertFlashes = %+v, want three flashes with two dark phases", flashes)
	}
	total := 0
	for i, p := range flashes {
		if p.Show != (i%2 == 0) || (p.Show && p.Value != (RGB{0xFF, 0, 0})) {
			t.Errorf("phase %d = %+v, want a red flash on the even phases and a dark one between", i, p)
		}
		total += int(p.Ms)
	}
	if total != 1250 {
		t.Errorf("the flashes last %d ms, want 1250", total)
	}
	players := AlertPlayerFlashes()
	if len(players) != 5 {
		t.Fatalf("AlertPlayerFlashes = %+v", players)
	}
	for i, p := range players {
		want := -1
		if i%2 == 0 {
			want = 3
		}
		if !p.Show || p.Value != want || p.Ms != 250 {
			t.Errorf("player phase %d = %+v, want index %d for 250 ms", i, p, want)
		}
	}
}

func TestAlertLightFor(t *testing.T) {
	for _, tc := range []struct {
		caps Caps
		want AlertLight
	}{
		{Caps{}, AlertNoLight},
		{Caps{Rumble: true}, AlertNoLight},
		{Caps{LED: true}, AlertNoLight}, // a mono LED is no flash
		{Caps{LED: true, RGBLED: true}, AlertLED},
		{Caps{PlayerLED: true}, AlertPlayer},
		{Caps{LED: true, RGBLED: true, PlayerLED: true}, AlertLED},
	} {
		if got := AlertLightFor(tc.caps); got != tc.want {
			t.Errorf("AlertLightFor(%+v) = %v, want %v", tc.caps, got, tc.want)
		}
	}
}

// An alert flashes a color LED red above the layer color and gives the color
// back, flashes the player lights of a controller without a color LED and gives
// the index back, and does nothing for a controller that cannot flash.
func TestMixerAlertBlink(t *testing.T) {
	r := newRig(t, Caps{Rumble: true, LED: true, RGBLED: true, PlayerLED: true})
	if err := r.m.SetLED(dv, Ambient, red, r.at(0)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 led #FF0000")
	light, err := r.m.AlertBlink(dv, r.at(100))
	if err != nil || light != AlertLED {
		t.Fatalf("AlertBlink = %v, %v; want the LED", light, err)
	}
	r.run(100, 1500)
	// The layer color is red as well: the request is written (it is a request
	// for the color), the Mixer's own phase changes show the same color and
	// write nothing.
	r.expect("@100 led #FF0000")

	// Over a green layer the red shows, and the green comes back.
	r = newRig(t, Caps{LED: true, RGBLED: true})
	green := RGB{0, 0xFF, 0}
	if err := r.m.SetLED(dv, Ambient, green, r.at(0)); err != nil {
		t.Fatal(err)
	}
	if _, err := r.m.AlertBlink(dv, r.at(100)); err != nil {
		t.Fatal(err)
	}
	r.run(100, 1500)
	r.expect("@0 led #00FF00",
		"@100 led #FF0000", "@350 led #00FF00", "@600 led #FF0000", "@850 led #00FF00", "@1100 led #FF0000", "@1350 led #00FF00")

	// Player lights only: four lights out and on, the original index back.
	r = newRig(t, Caps{PlayerLED: true})
	r.f.player = 1
	if light, err = r.m.AlertBlink(dv, r.at(0)); err != nil || light != AlertPlayer {
		t.Fatalf("AlertBlink = %v, %v; want the player lights", light, err)
	}
	r.run(0, 1500)
	r.expect("@0 player 3", "@250 player -1", "@500 player 3", "@750 player -1", "@1000 player 3", "@1250 player 1")

	// Nothing to flash with a mono LED or no lights at all.
	for _, caps := range []Caps{{LED: true}, {Rumble: true}, {}} {
		r = newRig(t, caps)
		if light, err = r.m.AlertBlink(dv, r.at(0)); err != nil || light != AlertNoLight {
			t.Errorf("AlertBlink on %+v = %v, %v; want no light", caps, light, err)
		}
		r.run(0, 1500)
		r.expect()
	}
	if _, err = r.m.AlertBlink(99, r.at(0)); !errors.Is(err, ErrUnknownDevice) {
		t.Errorf("AlertBlink(unknown device) = %v, want ErrUnknownDevice", err)
	}

	// It is an Alert claim: Identify outranks it, and it ends with Cancel.
	r = newRig(t, Caps{LED: true, RGBLED: true})
	if _, err = r.m.AlertBlink(dv, r.at(0)); err != nil {
		t.Fatal(err)
	}
	r.expect("@0 led #FF0000")
	if err = r.m.Cancel(dv, Alert, r.at(10)); err != nil {
		t.Fatal(err)
	}
	r.expect("@10 led #000000")
	r.run(10, 1500)
	r.expect()
}
