package winsession_test

import (
	"errors"
	"testing"
	"time"

	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// world is a fake Querier: the state the queries read, whether they fail,
// and how often each was called.
type world struct {
	info       ws.SessionInfo
	sessionErr error
	other      bool
	desktopErr error

	sessions, desktops int
}

func (w *world) Session() (ws.SessionInfo, error) {
	w.sessions++
	if w.sessionErr != nil {
		return ws.SessionInfo{}, w.sessionErr
	}
	return w.info, nil
}

func (w *world) Desktop() (bool, error) {
	w.desktops++
	if w.desktopErr != nil {
		return false, w.desktopErr
	}
	return w.other, nil
}

var errQuery = errors.New("query failed")

// Worlds of the tests: unlocked and active on our desktop, and variations.
func clean() *world    { return &world{info: ws.SessionInfo{ID: 1, Active: true, LockKnown: true}} }
func locked() *world   { w := clean(); w.info.Locked = true; return w }
func unknown() *world  { w := clean(); w.info.LockKnown = false; return w }
func inactive() *world { w := clean(); w.info.Active = false; return w }
func secure() *world   { w := clean(); w.other = true; return w }
func failing() *world  { return &world{sessionErr: errQuery, desktopErr: errQuery} }

// trackerWith returns a Tracker whose reasons are r, set through signals
// at awake time 0, and checks it.
func trackerWith(t *testing.T, r ws.Reason) *ws.Tracker {
	t.Helper()
	tr := ws.NewTracker(0)
	for _, s := range []struct {
		bit ws.Reason
		sig ws.Signal
	}{{ws.Lock, ws.SigLock}, {ws.Disconnect, ws.SigDisconnect}, {ws.Sleep, ws.SigSleep}, {ws.End, ws.SigQueryEnd}} {
		if r&s.bit != 0 {
			tr.Step(s.sig, 0, nil)
		}
	}
	if r&ws.Desktop != 0 {
		tr.Step(ws.SigDesktopSwitch, 0, secure())
	}
	if got := tr.Reasons(); got != r {
		t.Fatalf("trackerWith(%v) has %v", r, got)
	}
	return tr
}

func TestTrackerTable(t *testing.T) {
	const (
		lock, disc, desk, sleep, end = ws.Lock, ws.Disconnect, ws.Desktop, ws.Sleep, ws.End
	)
	for _, tc := range []struct {
		name               string
		start              ws.Reason
		world              *world
		sig                ws.Signal
		want               ws.Reason
		sessions, desktops int
	}{
		{"init clean", 0, clean(), ws.SigInit, 0, 1, 1},
		{"init locked", 0, locked(), ws.SigInit, lock, 1, 1},
		{"init lock unknown", 0, unknown(), ws.SigInit, 0, 1, 1},
		{"init disconnected on the secure desktop", 0, func() *world { w := inactive(); w.other = true; return w }(), ws.SigInit, disc | desk, 1, 1},
		{"init starts over", lock | disc | desk | sleep | end, clean(), ws.SigInit, 0, 1, 1},
		{"init keeps nothing it cannot see", lock | sleep | end, unknown(), ws.SigInit, 0, 1, 1},

		{"lock", 0, clean(), ws.SigLock, lock, 0, 0},
		{"lock again", lock, clean(), ws.SigLock, lock, 0, 0},
		{"disconnect", desk, clean(), ws.SigDisconnect, disc | desk, 0, 0},
		{"sleep", lock, clean(), ws.SigSleep, lock | sleep, 0, 0},
		{"queryend", 0, clean(), ws.SigQueryEnd, end, 0, 0},
		{"end", 0, clean(), ws.SigEnd, end, 0, 0},
		{"end after queryend", end, clean(), ws.SigEnd, end, 0, 0},

		{"unlock", lock, clean(), ws.SigUnlock, 0, 1, 1},
		{"unlock while locked", lock, locked(), ws.SigUnlock, lock, 1, 1},
		{"unlock while the lock is unknown", lock, unknown(), ws.SigUnlock, 0, 1, 1},
		{"unlock onto the secure desktop", lock, secure(), ws.SigUnlock, desk, 1, 1},
		{"unlock back from the secure desktop", lock | desk, clean(), ws.SigUnlock, 0, 1, 1},
		{"unlock keeps sleep", lock | sleep, clean(), ws.SigUnlock, sleep, 1, 1},
		{"unlock not locked", 0, clean(), ws.SigUnlock, 0, 1, 1},
		{"connect", disc, clean(), ws.SigConnect, 0, 1, 1},
		{"connect while inactive", disc, inactive(), ws.SigConnect, disc, 1, 1},
		{"connect to a locked session", disc, locked(), ws.SigConnect, lock, 1, 1},
		{"connect keeps a lock it cannot see", lock | disc, unknown(), ws.SigConnect, lock, 1, 1},
		{"wake", sleep, clean(), ws.SigWake, 0, 1, 1},
		{"wake locked", sleep, locked(), ws.SigWake, lock, 1, 1},
		{"wake corrects a missed unlock", sleep | lock, clean(), ws.SigWake, 0, 1, 1},
		{"endcanceled", end, clean(), ws.SigEndCanceled, 0, 1, 1},
		{"endcanceled keeps sleep", end | sleep, clean(), ws.SigEndCanceled, sleep, 1, 1},

		{"desktop switch away", 0, secure(), ws.SigDesktopSwitch, desk, 0, 1},
		{"desktop switch back", desk, clean(), ws.SigDesktopSwitch, 0, 0, 1},
		{"desktop switch leaves the lock", lock, clean(), ws.SigDesktopSwitch, lock, 0, 1},

		{"tick", 0, locked(), ws.SigTick, 0, 0, 1},
		{"tick finds the secure desktop", 0, secure(), ws.SigTick, desk, 0, 1},
		{"tick while locked", lock, clean(), ws.SigTick, 0, 1, 1},
		{"tick while still locked", lock, locked(), ws.SigTick, lock, 1, 1},
		{"tick while disconnected", disc, clean(), ws.SigTick, 0, 1, 1},
		{"tick keeps a lock it cannot see", lock, unknown(), ws.SigTick, lock, 1, 1},
		{"tick while asleep", sleep, clean(), ws.SigTick, sleep, 0, 1},

		{"invalid", lock, clean(), 0, lock, 0, 0},
		{"past tick", lock, clean(), ws.SigTick + 1, lock, 0, 0},
	} {
		tr := trackerWith(t, tc.start)
		c := tr.Step(tc.sig, time.Second, tc.world)
		want := ws.Change{Signal: tc.sig, Was: tc.start, Now: tc.want}
		if c != want || tr.Reasons() != tc.want {
			t.Errorf("%s: Step = %+v, Reasons %v; want %+v", tc.name, c, tr.Reasons(), want)
		}
		if tc.world.sessions != tc.sessions || tc.world.desktops != tc.desktops {
			t.Errorf("%s: %d session and %d desktop queries, want %d and %d",
				tc.name, tc.world.sessions, tc.world.desktops, tc.sessions, tc.desktops)
		}
		if c.Changed() != (tc.start != tc.want) {
			t.Errorf("%s: Changed() = %v", tc.name, c.Changed())
		}
	}
}

func TestTrackerSetNoQuery(t *testing.T) {
	for _, sig := range []ws.Signal{ws.SigLock, ws.SigDisconnect, ws.SigSleep, ws.SigQueryEnd, ws.SigEnd} {
		for _, start := range []ws.Reason{0, ws.Lock, ws.Disconnect | ws.Desktop, ws.AllReasons} {
			tr := trackerWith(t, start)
			for _, w := range []*world{clean(), locked(), secure(), failing()} {
				tr.Step(sig, 0, w)
				if w.sessions != 0 || w.desktops != 0 {
					t.Errorf("%v from %v queried the session %d and the desktop %d times",
						sig, start, w.sessions, w.desktops)
				}
			}
		}
	}
	// Nor does any signal without a Querier.
	for _, sig := range allSignals {
		tr := trackerWith(t, ws.Lock|ws.Desktop)
		c := tr.Step(sig, 0, nil)
		if sig.Queries() && c.Now != c.Was&^clearedBy(sig) {
			t.Errorf("%v with no Querier: %+v", sig, c)
		}
	}
}

// clearedBy is what Step clears for sig before it queries.
func clearedBy(sig ws.Signal) ws.Reason {
	switch sig {
	case ws.SigInit:
		return ws.AllReasons
	case ws.SigUnlock:
		return ws.Lock
	case ws.SigConnect:
		return ws.Disconnect
	case ws.SigWake:
		return ws.Sleep
	case ws.SigEndCanceled:
		return ws.End
	}
	return 0
}

func TestTrackerQueryBeatsSignal(t *testing.T) {
	// The world stays locked: an unlock (late, duplicated or forged) keeps
	// Lock, and so does the tick after it.
	tr := trackerWith(t, ws.Lock)
	w := locked()
	for _, sig := range []ws.Signal{ws.SigUnlock, ws.SigUnlock, ws.SigTick, ws.SigConnect, ws.SigWake, ws.SigEndCanceled} {
		if c := tr.Step(sig, 0, w); c.Now != ws.Lock || c.Changed() {
			t.Errorf("%v while locked: %+v, want Lock unchanged", sig, c)
		}
	}
	// The session stays inactive: a connect keeps Disconnect.
	tr = trackerWith(t, ws.Disconnect)
	w = inactive()
	for _, sig := range []ws.Signal{ws.SigConnect, ws.SigUnlock, ws.SigTick} {
		if c := tr.Step(sig, 0, w); c.Now != ws.Disconnect || c.Changed() {
			t.Errorf("%v while inactive: %+v, want Disconnect unchanged", sig, c)
		}
	}
	// A query also sets what no signal said: a wake into a locked,
	// disconnected session on the secure desktop.
	tr = trackerWith(t, ws.Sleep)
	w = inactive()
	w.info.Locked, w.other = true, true
	if c := tr.Step(ws.SigWake, 0, w); c.Now != ws.Lock|ws.Disconnect|ws.Desktop {
		t.Errorf("wake: %+v, want lock,disconnect,desktop", c)
	}
}

func TestTrackerQueryFails(t *testing.T) {
	for _, tc := range []struct {
		name  string
		start ws.Reason
		world *world
		sig   ws.Signal
		want  ws.Reason
	}{
		{"init", 0, failing(), ws.SigInit, 0},
		{"init after everything", ws.AllReasons, failing(), ws.SigInit, 0},
		{"unlock", ws.Lock, failing(), ws.SigUnlock, 0},
		{"unlock keeps the desktop", ws.Lock | ws.Desktop, failing(), ws.SigUnlock, ws.Desktop},
		{"connect keeps the lock", ws.Lock | ws.Disconnect, failing(), ws.SigConnect, ws.Lock},
		{"wake", ws.Sleep | ws.Lock, failing(), ws.SigWake, ws.Lock},
		{"endcanceled", ws.End, failing(), ws.SigEndCanceled, 0},
		{"desktop switch", ws.Desktop, failing(), ws.SigDesktopSwitch, ws.Desktop},
		{"tick", ws.Lock | ws.Desktop, failing(), ws.SigTick, ws.Lock | ws.Desktop},
		{"session fails, desktop answers", ws.Lock | ws.Desktop,
			&world{sessionErr: errQuery}, ws.SigUnlock, 0},
		{"desktop fails, session answers", ws.Lock | ws.Desktop,
			&world{info: ws.SessionInfo{Active: true, LockKnown: true, Locked: true}, desktopErr: errQuery}, ws.SigUnlock, ws.Lock | ws.Desktop},
		{"init, desktop fails", 0,
			&world{info: ws.SessionInfo{Active: false, LockKnown: true}, desktopErr: errQuery}, ws.SigInit, ws.Disconnect},
	} {
		tr := trackerWith(t, tc.start)
		if c := tr.Step(tc.sig, 0, tc.world); c.Now != tc.want || c.Was != tc.start {
			t.Errorf("%s: %+v, want %v -> %v", tc.name, c, tc.start, tc.want)
		}
	}
}

func TestTrackerTickQueries(t *testing.T) {
	// ticks runs n ticks and returns the ticks, counted from 1, that
	// queried the session. Every tick must query the desktop once.
	ticks := func(t *testing.T, tr *ws.Tracker, w *world, n int) []int {
		t.Helper()
		var got []int
		for i := 1; i <= n; i++ {
			s, d := w.sessions, w.desktops
			tr.Step(ws.SigTick, 0, w)
			if w.desktops != d+1 {
				t.Fatalf("tick %d queried the desktop %d times", i, w.desktops-d)
			}
			if w.sessions != s {
				got = append(got, i)
			}
		}
		return got
	}
	equal := func(t *testing.T, name string, got, want []int) {
		t.Helper()
		if len(got) != len(want) {
			t.Errorf("%s: session queried on ticks %v, want %v", name, got, want)
			return
		}
		for i := range got {
			if got[i] != want[i] {
				t.Errorf("%s: session queried on ticks %v, want %v", name, got, want)
				return
			}
		}
	}

	tr := ws.NewTracker(0)
	w := clean()
	equal(t, "idle", ticks(t, tr, w, 11), []int{5, 10})
	// Other signals do not count as ticks.
	tr.Step(ws.SigDesktopSwitch, 0, w)
	tr.Step(ws.SigUnlock, 0, w)
	equal(t, "idle after other signals", ticks(t, tr, w, 4), []int{4})

	tr = ws.NewTracker(0)
	tr.SetRegistered(false)
	equal(t, "unregistered", ticks(t, tr, w, 6), []int{1, 2, 3, 4, 5, 6})
	tr.SetRegistered(true)
	equal(t, "registered again", ticks(t, tr, w, 5), []int{4})

	for _, start := range []ws.Reason{ws.Lock, ws.Disconnect, ws.Lock | ws.Sleep} {
		tr = trackerWith(t, start)
		w := locked()
		w.info.Active = start&ws.Disconnect == 0
		equal(t, start.String(), ticks(t, tr, w, 6), []int{1, 2, 3, 4, 5, 6})
	}
	// Sleep, End and Desktop do not make the tick query the session.
	tr = trackerWith(t, ws.Sleep|ws.End|ws.Desktop)
	w = secure()
	equal(t, "sleep,end,desktop", ticks(t, tr, w, 6), []int{5})
}

func TestTrackerStale(t *testing.T) {
	const s = time.Second
	tr := ws.NewTracker(60 * s)
	tr.Step(ws.SigLock, 0, nil)
	tr.Step(ws.SigSleep, 10*s, nil)
	if c := tr.Step(ws.SigTick, 69*s+999*time.Millisecond, nil); c.Changed() || c.Stale != 0 {
		t.Fatalf("at 59.999 s awake: %+v, want Sleep kept", c)
	}
	c := tr.Step(ws.SigTick, 70*s, nil)
	if want := (ws.Change{Signal: ws.SigTick, Was: ws.Lock | ws.Sleep, Now: ws.Lock, Stale: ws.Sleep}); c != want {
		t.Fatalf("at 60 s awake: %+v, want %+v", c, want)
	}
	// LastStale is the last step's, none again after a step that clears
	// nothing.
	if got := tr.LastStale(); got != ws.Sleep {
		t.Fatalf("LastStale() = %v after the stale step, want sleep", got)
	}
	if tr.Step(ws.SigTick, 71*s, nil); tr.LastStale() != 0 {
		t.Fatalf("LastStale() = %v after a step that cleared nothing, want none", tr.LastStale())
	}

	// A second SigSleep, SigQueryEnd or SigEnd restamps.
	tr.Step(ws.SigQueryEnd, 100*s, nil)
	tr.Step(ws.SigEnd, 130*s, nil)
	tr.Step(ws.SigSleep, 140*s, nil)
	tr.Step(ws.SigSleep, 150*s, nil)
	if c := tr.Step(ws.SigTick, 189*s, nil); c.Now != ws.Lock|ws.Sleep|ws.End {
		t.Fatalf("at 189 s: %+v, want End and Sleep kept", c)
	}
	if c := tr.Step(ws.SigTick, 190*s, nil); c.Stale != ws.End || c.Now != ws.Lock|ws.Sleep {
		t.Fatalf("at 190 s: %+v, want End stale", c)
	}
	if c := tr.Step(ws.SigTick, 210*s, nil); c.Stale != ws.Sleep || c.Now != ws.Lock {
		t.Fatalf("at 210 s: %+v, want Sleep stale", c)
	}

	// The guard runs before the signal: a sleep long after the last one is
	// both stale and set again, with no net change.
	tr.Step(ws.SigSleep, 300*s, nil)
	if c := tr.Step(ws.SigSleep, 400*s, nil); c.Stale != ws.Sleep || c.Changed() {
		t.Fatalf("sleep again at 400 s: %+v", c)
	}
	// Lock never goes stale.
	if c := tr.Step(ws.SigTick, 10000*s, nil); c.Now != ws.Lock {
		t.Fatalf("at 10000 s: %+v, want Lock kept", c)
	}
	// Nor does an invalid signal skip the guard.
	tr.Step(ws.SigQueryEnd, 10000*s, nil)
	if c := tr.Step(0, 10060*s, nil); c.Stale != ws.End || c.Now != ws.Lock {
		t.Fatalf("invalid signal at 10060 s: %+v", c)
	}

	for _, after := range []time.Duration{0, -s} {
		tr := ws.NewTracker(after)
		tr.Step(ws.SigSleep, 0, nil)
		if c := tr.Step(ws.SigTick, ws.DefaultStaleAfter-1, nil); c.Stale != 0 {
			t.Errorf("NewTracker(%v): stale before DefaultStaleAfter: %+v", after, c)
		}
		if c := tr.Step(ws.SigTick, ws.DefaultStaleAfter, nil); c.Stale != ws.Sleep {
			t.Errorf("NewTracker(%v): not stale at DefaultStaleAfter: %+v", after, c)
		}
	}
	if ws.DefaultStaleAfter != 60*s {
		t.Errorf("DefaultStaleAfter = %v", ws.DefaultStaleAfter)
	}
}

func TestTrackerNetChange(t *testing.T) {
	tr := ws.NewTracker(0)
	w := clean()
	for i, st := range []struct {
		sig   ws.Signal
		world func()
		want  ws.Change
	}{
		{ws.SigInit, nil, ws.Change{Signal: ws.SigInit}},
		{ws.SigLock, func() { w.info.Locked = true }, ws.Change{Signal: ws.SigLock, Now: ws.Lock}},
		{ws.SigDesktopSwitch, func() { w.other = true }, ws.Change{Signal: ws.SigDesktopSwitch, Was: ws.Lock, Now: ws.Lock | ws.Desktop}},
		{ws.SigLock, nil, ws.Change{Signal: ws.SigLock, Was: ws.Lock | ws.Desktop, Now: ws.Lock | ws.Desktop}},
		{ws.SigTick, func() { w.other = false }, ws.Change{Signal: ws.SigTick, Was: ws.Lock | ws.Desktop, Now: ws.Lock}},
		{ws.SigUnlock, nil, ws.Change{Signal: ws.SigUnlock, Was: ws.Lock, Now: ws.Lock}},
		{ws.SigUnlock, func() { w.info.Locked = false }, ws.Change{Signal: ws.SigUnlock, Was: ws.Lock}},
		{ws.SigTick, nil, ws.Change{Signal: ws.SigTick}},
	} {
		if st.world != nil {
			st.world()
		}
		c := tr.Step(st.sig, 0, w)
		if c != st.want || c.Changed() != (st.want.Was != st.want.Now) {
			t.Errorf("step %d (%v): %+v, want %+v", i, st.sig, c, st.want)
		}
	}
}

func TestTrackerRecheck(t *testing.T) {
	// Gen counts the signals that set the reason, and nothing else.
	tr := ws.NewTracker(0)
	for _, sig := range []ws.Signal{ws.SigInit, ws.SigLock, ws.SigTick, ws.SigDesktopSwitch, ws.SigUnlock} {
		if g := tr.Gen(sig); g != 0 {
			t.Errorf("Gen(%v) = %d on a new Tracker, want 0", sig, g)
		}
	}
	tr.Step(ws.SigInit, 0, locked())
	tr.Step(ws.SigUnlock, 0, clean())
	tr.Step(ws.SigTick, 0, locked())
	if g := tr.Gen(ws.SigUnlock); g != 0 {
		t.Errorf("Gen(unlock) = %d after queries set Lock, want 0", g)
	}
	tr.Step(ws.SigLock, 0, nil)
	tr.Step(ws.SigLock, 0, nil)
	tr.Step(ws.SigQueryEnd, 0, nil)
	tr.Step(ws.SigEnd, 0, nil)
	for _, g := range []struct {
		sig  ws.Signal
		want uint32
	}{{ws.SigUnlock, 2}, {ws.SigConnect, 0}, {ws.SigWake, 0}, {ws.SigEndCanceled, 2}, {ws.SigLock, 0}, {ws.SigEnd, 0}} {
		if got := tr.Gen(g.sig); got != g.want {
			t.Errorf("Gen(%v) = %d, want %d", g.sig, got, g.want)
		}
	}

	for _, tc := range []struct {
		name     string
		start    ws.Reason
		world    *world
		sig      ws.Signal
		setAgain ws.Signal // set after the recheck was posted, or 0
		want     ws.Reason
		queries  int
	}{
		{name: "wake", start: ws.Sleep, world: clean(), sig: ws.SigWake, want: 0, queries: 1},
		{name: "wake after a new sleep", start: ws.Sleep, world: clean(), sig: ws.SigWake, setAgain: ws.SigSleep, want: ws.Sleep, queries: 1},
		{name: "wake before the first sleep", start: 0, world: clean(), sig: ws.SigWake, setAgain: ws.SigSleep, want: ws.Sleep, queries: 1},
		{name: "end canceled after a new query", start: ws.End, world: clean(), sig: ws.SigEndCanceled, setAgain: ws.SigQueryEnd, want: ws.End, queries: 1},
		{name: "end canceled after the end", start: ws.End, world: clean(), sig: ws.SigEndCanceled, setAgain: ws.SigEnd, want: ws.End, queries: 1},
		{name: "unlock after a new lock, the query failing", start: ws.Lock, world: failing(), sig: ws.SigUnlock, setAgain: ws.SigLock, want: ws.Lock, queries: 1},
		// The query still wins over the generation: it says unlocked.
		{name: "unlock after a new lock, the query unlocked", start: ws.Lock, world: clean(), sig: ws.SigUnlock, setAgain: ws.SigLock, want: 0, queries: 1},
		{name: "connect after a new disconnect", start: ws.Disconnect, world: failing(), sig: ws.SigConnect, setAgain: ws.SigDisconnect, want: ws.Disconnect, queries: 1},
		// Another reason's signal does not make the recheck stale.
		{name: "wake after a lock", start: ws.Sleep, world: &world{sessionErr: errQuery}, sig: ws.SigWake, setAgain: ws.SigLock, want: ws.Lock, queries: 1},
		// A signal that does not clear only runs the stale guard.
		{name: "not a clearing signal", start: ws.Lock | ws.Sleep, world: clean(), sig: ws.SigLock, want: ws.Lock | ws.Sleep},
		{name: "a tick", start: ws.Lock, world: clean(), sig: ws.SigTick, want: ws.Lock},
		{name: "invalid", start: ws.Lock, world: clean(), sig: ws.SigTick + 1, want: ws.Lock},
	} {
		tr := trackerWith(t, tc.start)
		gen := tr.Gen(tc.sig)
		if tc.setAgain != 0 {
			tr.Step(tc.setAgain, 0, nil)
		}
		was := tr.Reasons()
		c := tr.Recheck(tc.sig, gen, time.Second, tc.world)
		if want := (ws.Change{Signal: tc.sig, Was: was, Now: tc.want}); c != want || tr.Reasons() != tc.want {
			t.Errorf("%s: Recheck = %+v, Reasons %v; want %+v", tc.name, c, tr.Reasons(), want)
		}
		if tc.world.sessions != tc.queries || tc.world.desktops != tc.queries {
			t.Errorf("%s: %d session and %d desktop queries, want %d of each",
				tc.name, tc.world.sessions, tc.world.desktops, tc.queries)
		}
	}

	// A recheck at the current generation is Step's, and an older one is
	// never current again.
	tr = trackerWith(t, ws.Sleep)
	old := tr.Gen(ws.SigWake)
	tr.Step(ws.SigSleep, 0, nil)
	if c := tr.Recheck(ws.SigWake, old, 0, clean()); c.Now != ws.Sleep {
		t.Errorf("an old recheck: %+v, want Sleep kept", c)
	}
	if c := tr.Recheck(ws.SigWake, tr.Gen(ws.SigWake), 0, clean()); c.Now != 0 {
		t.Errorf("a current recheck: %+v, want Sleep cleared", c)
	}
	tr.Step(ws.SigSleep, 0, nil)
	if c := tr.Recheck(ws.SigWake, old, 0, clean()); c.Now != ws.Sleep {
		t.Errorf("the old recheck again: %+v, want Sleep kept", c)
	}

	// The stale guard runs on every recheck.
	tr = ws.NewTracker(time.Minute)
	tr.Step(ws.SigSleep, 0, nil)
	tr.Step(ws.SigQueryEnd, 0, nil)
	if c := tr.Recheck(ws.SigLock, 0, time.Minute, nil); c.Stale != ws.Sleep|ws.End || c.Now != 0 || c.Signal != ws.SigLock {
		t.Errorf("stale guard: %+v", c)
	}
}

// FuzzSessionTracker steps a Tracker through random signals, worlds and
// awake times and checks each step against a model of the rules: set
// signals and steps without a Querier never query; the desktop is queried
// once on every querying step and the session on Init, the clearing
// signals and the ticks the cadence picks; after a successful session query
// Lock matches the world when it is known and Disconnect is !Active; after
// a successful desktop query Desktop matches the world; Sleep and End
// follow their signals and never outlive the stale time; Change is the net
// change of the step; and no reason outside AllReasons appears. Rechecks
// are posted and dispatched later, as the session thread does: one clears
// its reason only if no signal set it since it was posted, and Gen moves
// exactly when such a signal came.
//
// Each step is three bytes. The op: its low nibble picks a signal from
// allSignals when below 12, toggles the registration when 12 (on when bit
// 4 is set), gives an invalid signal when 13, a tick when 14, and when 15
// dispatches the oldest posted recheck, or ticks if there is none; bit 4
// takes the Querier away from a signal; bit 5 posts a clearing signal as a
// recheck instead of stepping it, and makes a dispatched recheck carry a
// generation older than the one it was posted at. The world: bits 0-1
// unlocked, locked, unknown or a failing session query; bit 2 inactive;
// bit 3 the secure desktop; bit 4 a failing desktop query. Then the awake
// time to add, in half seconds, so awake time only moves forward.
func FuzzSessionTracker(f *testing.F) {
	const (
		start, lock, unlock, disc, conn, sleep, wake, qend, endc, end, desk, tick = 0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11
		register, invalid, dispatch, noQuery, later                               = 12, 13, 15, 16, 32
		wLocked, wUnknown, wSessFail, wInactive, wOther, wDeskFail                = 1, 2, 3, 4, 8, 16
	)
	for _, seed := range [][]byte{
		{start, 0, 0, lock, wLocked, 1, unlock, wLocked, 1, tick, 0, 4, tick, 0, 4},
		{start, wLocked | wOther, 0, tick, 0, 4, desk, wOther, 1, desk, 0, 1},
		{sleep, 0, 1, tick, 0, 119, tick, 0, 1, wake, wLocked, 1},
		{qend, 0, 1, endc, wSessFail | wDeskFail, 1, qend, 0, 10, end, 0, 110, tick, 0, 10},
		{register, 0, 0, tick, wUnknown | wInactive, 4, conn, wInactive, 1, register | noQuery, 0, 0, tick, 0, 4},
		{invalid, 0, 0, unlock | noQuery, 0, 1, tick | noQuery, wLocked, 200, disc, 0, 0, tick, wSessFail, 4},
		{lock, 0, 0, 14, wLocked, 4, 15, wUnknown, 4, 14, 0, 4, 14, 0, 4, 14, 0, 4},
		{sleep, 0, 0, wake | later, 0, 1, sleep, 0, 1, dispatch, 0, 1, wake | later, 0, 1, dispatch, 0, 1},
		{qend, 0, 0, endc | later, 0, 1, end, 0, 1, dispatch, 0, 1, endc | later, 0, 1, dispatch | later, 0, 1},
		{lock, 0, 0, unlock | later, 0, 1, lock, 0, 1, dispatch, wSessFail, 1, unlock | later, 0, 1, conn | later, 0, 1,
			disc, 0, 1, dispatch | noQuery, 0, 1, dispatch, wLocked, 1},
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		const staleAfter = 60 * time.Second
		tr := ws.NewTracker(staleAfter)
		var (
			awake          time.Duration
			model          ws.Reason
			sleepAt, endAt time.Duration
			registered     = true
			ticks          int
			sets           = map[ws.Reason]int{} // the signals that set each reason
			pending        []posted
		)
		for ; len(data) >= 3; data = data[3:] {
			op, wb, dt := data[0], data[1], data[2]
			awake += time.Duration(dt) * 500 * time.Millisecond

			known, isLocked, sessOK := wb&3 != wUnknown, wb&3 == wLocked, wb&3 != wSessFail
			active, other, deskOK := wb&wInactive == 0, wb&wOther != 0, wb&wDeskFail == 0
			w := &world{info: ws.SessionInfo{Active: active, LockKnown: known, Locked: isLocked}, other: other}
			if !sessOK {
				w.sessionErr = errQuery
			}
			if !deskOK {
				w.desktopErr = errQuery
			}
			var q ws.Querier = w
			if op&noQuery != 0 {
				q = nil
			}
			var sig ws.Signal
			var re *posted // the recheck dispatched, or nil
			switch n := op & 0x0f; {
			case n < register && op&later != 0 && allSignals[n].Clears():
				sig = allSignals[n]
				pending = append(pending, posted{sig, tr.Gen(sig), sets[clearedBy(sig)]})
				continue
			case n < register:
				sig = allSignals[n]
			case n == dispatch && len(pending) > 0:
				p := pending[0]
				re = &p
				pending = pending[1:]
				sig = re.sig
				if moved, set := tr.Gen(sig) != re.gen, sets[clearedBy(sig)] != re.sets; moved != set {
					t.Fatalf("%v: Gen moved %v, a signal set the reason %v since the recheck was posted", sig, moved, set)
				}
			case n == register:
				registered = op&noQuery != 0
				tr.SetRegistered(registered)
				continue
			case n == invalid:
				sig = ws.SigTick + 1 + ws.Signal(op>>5)
				if op&noQuery != 0 {
					sig = 0
				}
			default:
				sig = ws.SigTick
			}

			// The model's step.
			was := model
			var stale ws.Reason
			if model&ws.Sleep != 0 && awake-sleepAt >= staleAfter {
				stale |= ws.Sleep
			}
			if model&ws.End != 0 && awake-endAt >= staleAfter {
				stale |= ws.End
			}
			model &^= stale
			var wantSess, wantDesk bool
			switch sig {
			case ws.SigInit:
				model = 0
				wantSess, wantDesk = true, true
			case ws.SigLock:
				model |= ws.Lock
				sets[ws.Lock]++
			case ws.SigDisconnect:
				model |= ws.Disconnect
				sets[ws.Disconnect]++
			case ws.SigSleep:
				model |= ws.Sleep
				sleepAt = awake
				sets[ws.Sleep]++
			case ws.SigQueryEnd, ws.SigEnd:
				model |= ws.End
				endAt = awake
				sets[ws.End]++
			case ws.SigUnlock, ws.SigConnect, ws.SigWake, ws.SigEndCanceled:
				if re == nil || op&later == 0 && sets[clearedBy(sig)] == re.sets {
					model &^= clearedBy(sig)
				}
				wantSess, wantDesk = true, true
			case ws.SigDesktopSwitch:
				wantDesk = true
			case ws.SigTick:
				ticks++
				wantDesk = true
				wantSess = model&(ws.Lock|ws.Disconnect) != 0 || !registered || ticks%5 == 0
			}
			if q == nil {
				wantSess, wantDesk = false, false
			}
			if wantSess && sessOK {
				if known {
					model = withBit(model, ws.Lock, isLocked)
				}
				model = withBit(model, ws.Disconnect, !active)
			}
			if wantDesk && deskOK {
				model = withBit(model, ws.Desktop, other)
			}

			var c ws.Change
			if re != nil {
				gen := re.gen
				if op&later != 0 {
					gen-- // older than any generation to come
				}
				c = tr.Recheck(sig, gen, awake, q)
			} else {
				c = tr.Step(sig, awake, q)
			}
			if want := (ws.Change{Signal: sig, Was: was, Now: model, Stale: stale}); c != want || tr.Reasons() != model {
				t.Fatalf("%v at %v (world %#x, registered %v, tick %d, no Querier %v, recheck %+v): %+v, Reasons %v; want %+v",
					sig, awake, wb, registered, ticks, q == nil, re, c, tr.Reasons(), want)
			}
			if c.Now&^ws.AllReasons != 0 || c.Stale&^(c.Was&(ws.Sleep|ws.End)) != 0 {
				t.Fatalf("%+v has stray bits", c)
			}
			if got := tr.LastStale(); got != c.Stale {
				t.Fatalf("%v at %v: LastStale() = %v, want the Change's %v", sig, awake, got, c.Stale)
			}
			if w.sessions != b2i(wantSess) || w.desktops != b2i(wantDesk) {
				t.Fatalf("%v (tick %d, registered %v, no Querier %v): %d session and %d desktop queries, want %v and %v",
					sig, ticks, registered, q == nil, w.sessions, w.desktops, wantSess, wantDesk)
			}
			if !sig.Queries() && (w.sessions != 0 || w.desktops != 0) {
				t.Fatalf("%v queried", sig)
			}
			if c.Now&ws.Sleep != 0 && awake-sleepAt >= staleAfter || c.Now&ws.End != 0 && awake-endAt >= staleAfter {
				t.Fatalf("%v outlived the stale time at %v", c.Now, awake)
			}
		}
	})
}

// posted is a recheck FuzzSessionTracker posted: its clearing signal, the
// generation it carries, and how many signals had set the reason then.
type posted struct {
	sig  ws.Signal
	gen  uint32
	sets int
}

// withBit returns r with bit set if on, and cleared otherwise.
func withBit(r, bit ws.Reason, on bool) ws.Reason {
	if on {
		return r | bit
	}
	return r &^ bit
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
