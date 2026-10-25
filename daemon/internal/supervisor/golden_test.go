package supervisor_test

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

var update = flag.Bool("update", false, "rewrite testdata/golden/*/output.trace and slots.txt from the current run")

// goldenCmd is the command that -update goes with.
const goldenCmd = "go test ./internal/supervisor -run TestGolden"

// goldenSteps bounds the deadlines that a golden case steps through
// between two events, and after the last.
const goldenSteps = 10000

// TestGolden replays each case of testdata/golden (IN-05, IN-08): the
// events of input.jsonl (input.ReadEvents, the format of padmatectl
// record), in the order of the file, through a Supervisor of the profile
// (profile.json, or the file profile.path names relative to the case, such
// as testdata/hotplug.json), with Config.Device set to the line of
// device.txt and Config.Slots to the number in slotcount.txt when the case
// has them (1 without). Among the events, input.ExpandCombos inserts the
// ComboHeld events a Source would send, with the default combos of the
// horizontal grip (comboOf), as padmated's replay does: each toggles the
// pause (DMN-07). Before each event the fake clock moves to its time,
// stopping at each deadline on the way, and a Sync follows each stop and
// each event; after the last, the clock steps through the deadlines left,
// Run ends and the output loop stops. The keys injected must match
// output.trace, and the changes of the slots, of the pause and of the
// suspension slots.txt; nothing may be held at the end, and nothing logged
// at WARN or above.
//
// A case with a session.txt drives the system's suspension (DMN-09)
// through a real winsession.Tracker that queries a fake world:
//
//	@<ms> world [locked=true|false|unknown] [state=active|disconnected|fail] [desktop=input|other|fail]
//	@<ms> <signal>
//	@<ms> events
//
// The world starts unlocked, active and on the input desktop; a world line
// changes what it names. A signal is one of winsession.ParseSignal's but
// init. The world lines at @0 that come first set the world at startup,
// where the Tracker steps SigInit and its reasons go to Suspend before Run
// starts. The other lines of one instant form a group, applied in order
// after the trace events of that instant, each of which got its Sync, and
// the deadlines up to it: each step of the Tracker at the clock's time (the
// awake time) that changes the reasons calls SuspendQuiet with them, and
// one Sync follows the group. So the router applies the group's reasons
// only at that Sync, and a lock and an unlock in one group make a
// collapsed cycle. An events line puts the trace events of its instant
// there instead, with no Sync between them: the router routes them before
// it looks at the reasons.
//
// Edges of different slots at the same instant have no order, so with
// several slots runGolden checks that no two slots decide on output at the
// same instant (Config.Trace) and that at most one slot holds keys when
// Run ends; no case drops events or reloads.
//
// After each Sync, slots.txt has a line for each slot whose status
// changed, at the clock's time in ms:
//
//	@<ms> slot <index> gen=<gen> <- id <id> "<name>" key=<DeviceKey> home=<DeviceKey>[ provisional][ reconnected][ handoff][ rehomed][ reclaimed][ takeover][ confirmed]
//	@<ms> slot <index> gen=<gen> free
//
// with " disabled" and " restarts=<n>" appended while they apply. home and
// provisional are those of the status; a free slot has neither.
// reconnected and handoff are those of the "slot assigned" log line: the
// gamepad's key was known, and it was waiting when it got the slot.
// rehomed, reclaimed, takeover and confirmed mark a "slot rehomed", a
// "slot reclaimed", a "slot taken over" and a "slot confirmed" line of the
// slot since the previous Sync. When Status.Paused changed, a line before
// those says so:
//
//	@<ms> paused
//	@<ms> resumed
//
// and then comes a line for each change of the suspension that the router
// logged since the previous Sync, with its reasons (winsession.Reason's
// String); collapsed marks a suspension that ended before the router
// looked. After the last, Status.Suspended must have the reasons it left:
//
//	@<ms> suspended <reasons>[ collapsed]
//	@<ms> suspension <reasons>
//	@<ms> unsuspended
//
// Leading "#" lines of both files are comments, which -update keeps.
func TestGolden(t *testing.T) {
	dirs, err := filepath.Glob(filepath.Join("testdata", "golden", "*"))
	if err != nil || len(dirs) == 0 {
		t.Fatalf("no golden cases: %v", err)
	}
	for _, dir := range dirs {
		t.Run(filepath.Base(dir), func(t *testing.T) { runGolden(t, dir, *update) })
	}
}

// TestGoldenUpdate checks that -update creates the output.trace and
// slots.txt of a new case, which then passes, and that without it a
// missing file says how.
func TestGoldenUpdate(t *testing.T) {
	dir := t.TempDir()
	const guid = "030000000a0000000a00000000000000"
	files := map[string]string{
		"profile.json": `{"schemaVersion": 0, "name": "t", "layers": {"base": {` + eastHold + `}}}`,
		"input.jsonl": `{"timeNs":0,"kind":"added","device":1,"info":{"id":1,"name":"Pad A","guid":"` + guid + `","gamepad":true}}
{"timeNs":100000000,"kind":"down","device":1,"control":"east"}
{"timeNs":300000000,"kind":"removed","device":1}
`,
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"output.trace", "slots.txt"} {
		if _, err := simtest.ReadGolden(filepath.Join(dir, name), false, goldenCmd); err == nil || !strings.Contains(err.Error(), "-update") {
			t.Errorf("ReadGolden of a missing %s = %v; want an error that mentions -update", name, err)
		}
	}
	runGolden(t, dir, true)
	for name, want := range map[string]string{
		"output.trace": "+Space@100\n-Space@300\n",
		"slots.txt": `@0 slot 0 gen=1 <- id 1 "Pad A" key=` + guid + " home=" + guid + " provisional\n" +
			`@100 slot 0 gen=1 <- id 1 "Pad A" key=` + guid + " home=" + guid + " confirmed\n@300 slot 0 gen=1 free\n",
	} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != want {
			t.Errorf("%s = %q, %v; want %q", name, data, err, want)
		}
	}
	runGolden(t, dir, false)
}

func runGolden(t *testing.T, dir string, update bool) {
	prog := loadProfile(t, dir)
	device := readOptional(t, dir, "device.txt")
	slots := 1
	if text := readOptional(t, dir, "slotcount.txt"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > supervisor.MaxSlots {
			t.Fatalf("slotcount.txt: %q is not a slot count from 1 to %d", text, supervisor.MaxSlots)
		}
		slots = n
	}
	f, err := os.Open(filepath.Join(dir, "input.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	evs, err := input.ReadEvents(f)
	_ = f.Close()
	if err != nil {
		t.Fatalf("input.jsonl: %v", err)
	}
	evs = input.ExpandCombos(evs, comboOf)
	sess := readSession(t, dir)

	o := opts{program: prog, device: device, slots: slots}
	if sess != nil {
		r := sess.init()
		o.beforeRun = func(sup *supervisor.Supervisor) { sup.Suspend(r) }
	}
	var (
		mu    sync.Mutex
		first = map[clock.Time]uint8{} // the slot that decided on output first at each instant
		clash string
	)
	if slots > 1 {
		o.trace = func(i uint8, d engine.Decision) {
			if d.Owner == (engine.OwnerID{}) {
				return // no output
			}
			mu.Lock()
			defer mu.Unlock()
			if j, ok := first[d.T]; !ok {
				first[d.T] = i
			} else if j != i && clash == "" {
				clash = fmt.Sprintf("slots %d and %d both decide on output at %sms", j, i, d.T.AppendMs(nil))
			}
		}
	}
	h := start(t, o)
	w := &slotsWriter{h: h, last: h.sup.Status(), suspended: winsession.Reason(0).String()}
	var groups []sessionGroup
	if sess != nil {
		groups = sess.groups
	}
	// applyBefore applies the session groups before limit, each after the
	// deadlines up to its instant.
	applyBefore := func(limit clock.Time) {
		for len(groups) > 0 && groups[0].at < limit {
			g := groups[0]
			groups = groups[1:]
			if g.events {
				t.Fatalf("session.txt: events at @%s, where input.jsonl has none", g.at.AppendMs(nil))
			}
			w.runDeadlines(g.at)
			h.clk.Set(g.at)
			sess.apply(w, g, nil)
		}
	}
	for i := 0; i < len(evs); {
		at := clock.Time(evs[i].Time)
		if at < h.clk.Now() {
			t.Fatalf("event %d at %v, before the one before it", i+1, at)
		}
		applyBefore(at)
		n := 1 // the events of the instant
		for i+n < len(evs) && clock.Time(evs[i+n].Time) == at {
			n++
		}
		if len(groups) > 0 && groups[0].at == at && groups[0].events {
			g := groups[0]
			groups = groups[1:]
			w.runDeadlines(at)
			h.clk.Set(at)
			sess.apply(w, g, evs[i:i+n])
		} else {
			for _, e := range evs[i : i+n] {
				w.runDeadlines(at)
				h.clk.Set(at)
				h.send(e)
				w.sync()
			}
		}
		i += n
	}
	applyBefore(math.MaxInt64)
	w.runDeadlines(math.MaxInt64)
	if slots > 1 {
		mu.Lock()
		c := clash
		mu.Unlock()
		if c != "" {
			t.Fatal(c)
		}
		// The releases of the exit would come in no order.
		if owners := h.owners(); slices.ContainsFunc(owners, func(o output.Owner) bool { return o.Slot != owners[0].Slot }) {
			t.Fatalf("keys held by several slots at the end: %+v", owners)
		}
	}
	h.cancel()
	if err := h.wait(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := h.out.Stop(); err != nil {
		t.Fatalf("output.Loop.Stop: %v", err)
	}
	for _, r := range h.logs.with("") {
		if r.Level >= slog.LevelWarn {
			t.Errorf("logged %v %q %v", r.Level, r.Msg, r.Attrs)
		}
	}
	var edges []simtest.Edge
	h.rec.With(func(r *simtest.Recorder) { edges = r.Edges() })
	checkTrace(t, filepath.Join(dir, "output.trace"), edges, update)
	checkText(t, filepath.Join(dir, "slots.txt"), w.b.String(), update)
}

// comboOf is the combo of each gamepad in a replay: its default with the
// horizontal grip, as padmated's replay has no hint to tell the grip.
func comboOf(info input.DeviceInfo) input.Combo { return input.DefaultCombo(info, false) }

// readOptional returns the trimmed text of the file name of the case in
// dir, or "" when it has none.
func readOptional(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(data))
}

// loadProfile parses and compiles the profile of the case in dir.
// Warnings are fine.
func loadProfile(t *testing.T, dir string) *engine.Program {
	t.Helper()
	path := filepath.Join(dir, "profile.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		rel, err := os.ReadFile(filepath.Join(dir, "profile.path"))
		if err != nil {
			t.Fatal(err)
		}
		path = filepath.Join(dir, filepath.FromSlash(strings.TrimSpace(string(rel))))
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ds := profile.Parse(src)
	if p == nil || ds.HasErrors() {
		t.Fatalf("%s: %v", path, ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return prog
}

// slotsWriter writes the lines of slots.txt.
type slotsWriter struct {
	h    *harness
	b    strings.Builder
	last supervisor.Status
	logs int // the log lines seen
	// suspended is the reasons the suspension lines written so far left.
	suspended string
}

// sync waits for a Sync, then writes a line if the pause changed since the
// last, one for each change of the suspension, and one for each slot whose
// status changed.
func (w *slotsWriter) sync() {
	w.h.t.Helper()
	w.h.sync()
	lines := w.h.logs.with("")
	now := w.h.sup.Status()
	if now.Paused != w.last.Paused {
		fmt.Fprintf(&w.b, "@%s %s\n", w.h.clk.Now().AppendMs(nil), map[bool]string{true: "paused", false: "resumed"}[now.Paused])
	}
	w.writeSuspension(now.Suspended, lines[w.logs:])
	for j, st := range now.Slots {
		if st != w.last.Slots[j] {
			w.writeSlot(st, lines[w.logs:])
		}
	}
	w.last, w.logs = now, len(lines)
}

// runDeadlines moves the clock to each deadline not after limit in turn,
// after a Sync each time, so that the slots do their work at its time.
func (w *slotsWriter) runDeadlines(limit clock.Time) {
	w.h.t.Helper()
	for range goldenSteps {
		w.sync()
		next, ok := w.h.clk.Next()
		if !ok || next > limit {
			return
		}
		w.h.clk.Set(next)
	}
	w.h.t.Fatalf("more than %d deadlines before %v", goldenSteps, limit)
}

// writeSuspension writes the lines of slots.txt of the changes of the
// suspension in lines, the log lines since the previous Sync (DMN-09). The
// last must leave now, Status.Suspended.
func (w *slotsWriter) writeSuspension(now winsession.Reason, lines []logRecord) {
	at := w.h.clk.Now().AppendMs(nil)
	for _, r := range lines {
		switch r.Msg {
		case "mapping suspended":
			fmt.Fprintf(&w.b, "@%s suspended %s", at, r.Attrs["reasons"])
			if r.Attrs["collapsed"] == "true" {
				w.b.WriteString(" collapsed")
			}
			w.b.WriteByte('\n')
			w.suspended = r.Attrs["reasons"]
		case "mapping suspension changed":
			fmt.Fprintf(&w.b, "@%s suspension %s\n", at, r.Attrs["reasons"])
			w.suspended = r.Attrs["reasons"]
		case "mapping unsuspended":
			fmt.Fprintf(&w.b, "@%s unsuspended\n", at)
			w.suspended = winsession.Reason(0).String()
		}
	}
	if now.String() != w.suspended {
		w.h.t.Errorf("@%s: Status.Suspended %v, but the log left %s", at, now, w.suspended)
	}
}

// writeSlot writes the line of slots.txt of st, which changed at the
// clock's time; lines are the log lines since the previous Sync.
func (w *slotsWriter) writeSlot(st supervisor.SlotStatus, lines []logRecord) {
	b := &w.b
	fmt.Fprintf(b, "@%s slot %d gen=%d", w.h.clk.Now().AppendMs(nil), st.Index, st.Gen)
	if st.ID == 0 {
		if st.Home != (input.DeviceKey{}) || st.Provisional {
			w.h.t.Errorf("slot %d is free with home %v, provisional %v", st.Index, st.Home, st.Provisional)
		}
		b.WriteString(" free")
	} else {
		fmt.Fprintf(b, " <- id %d %q key=%v home=%v", st.ID, st.Name, st.Key, st.Home)
		if st.Provisional {
			b.WriteString(" provisional")
		}
		index := strconv.Itoa(int(st.Index))
		for _, r := range slices.Backward(lines) {
			if r.Msg == "slot assigned" && r.Attrs["slot"] == index {
				for _, attr := range []string{"reconnected", "handoff"} {
					if r.Attrs[attr] == "true" {
						b.WriteString(" " + attr)
					}
				}
				break
			}
		}
		for _, f := range [...]struct{ flag, msg string }{
			{"rehomed", "slot rehomed"}, {"reclaimed", "slot reclaimed"}, {"takeover", "slot taken over"}, {"confirmed", "slot confirmed"},
		} {
			if slices.ContainsFunc(lines, func(r logRecord) bool { return r.Msg == f.msg && r.Attrs["slot"] == index }) {
				b.WriteString(" " + f.flag)
			}
		}
	}
	if st.Disabled {
		b.WriteString(" disabled")
	}
	if st.Restarts > 0 {
		b.WriteString(" restarts=" + strconv.Itoa(st.Restarts))
	}
	b.WriteByte('\n')
}

// checkTrace compares the edges with the trace in path, or rewrites it
// with update, keeping its leading comment lines. update also creates a
// missing path, for a new case.
func checkTrace(t *testing.T, path string, got []simtest.Edge, update bool) {
	t.Helper()
	old := readGolden(t, path, update)
	if update {
		simtest.RewriteGolden(t, path, old, simtest.FormatTraceLines(got))
		return
	}
	want, err := simtest.ParseTrace(old)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	if !slices.Equal(got, want) {
		t.Errorf("%s differs (rerun with -update -v and review the diff):\n%s",
			path, simtest.LineDiff(simtest.FormatTraceLines(want), simtest.FormatTraceLines(got)))
	}
}

// checkText compares got with the text of path after its comment lines,
// or rewrites it with update, as checkTrace does.
func checkText(t *testing.T, path, got string, update bool) {
	t.Helper()
	old := readGolden(t, path, update)
	if update {
		simtest.RewriteGolden(t, path, old, got)
		return
	}
	if want := strings.TrimPrefix(old, simtest.GoldenComments(old)); got != want {
		t.Errorf("%s differs (rerun with -update -v and review the diff):\n%s", path, simtest.LineDiff(want, got))
	}
}

func readGolden(t *testing.T, path string, update bool) string {
	t.Helper()
	text, err := simtest.ReadGolden(path, update, goldenCmd)
	if err != nil {
		t.Fatal(err)
	}
	return text
}

// session is the system of a case's session.txt (DMN-09): the fake world
// its Tracker queries, and the groups of lines after startup.
type session struct {
	world   sessionWorld
	tracker *winsession.Tracker
	groups  []sessionGroup
}

// sessionGroup is the lines of session.txt at one instant, in order.
type sessionGroup struct {
	at     clock.Time
	lines  []sessionLine
	events bool // one of the lines is an events line
}

// sessionLine is a line of session.txt: a change of the world (the
// key=value pairs), a signal, or where the trace events go.
type sessionLine struct {
	world  [][2]string
	sig    winsession.Signal
	events bool
}

// sessionWorld is the fake system of a session.txt, a winsession.Querier:
// each field is the value of its key in a world line.
type sessionWorld struct {
	locked, state, desktop string
}

// worldValues are the values of each key of a world line.
var worldValues = map[string][]string{
	"locked":  {"true", "false", "unknown"},
	"state":   {"active", "disconnected", "fail"},
	"desktop": {"input", "other", "fail"},
}

var errWorld = errors.New("the query fails")

func (w *sessionWorld) Session() (winsession.SessionInfo, error) {
	if w.state == "fail" {
		return winsession.SessionInfo{}, errWorld
	}
	return winsession.SessionInfo{ID: 1, Active: w.state == "active", LockKnown: w.locked != "unknown", Locked: w.locked == "true"}, nil
}

func (w *sessionWorld) Desktop() (bool, error) {
	if w.desktop == "fail" {
		return false, errWorld
	}
	return w.desktop == "other", nil
}

func (w *sessionWorld) set(pairs [][2]string) {
	for _, p := range pairs {
		switch p[0] {
		case "locked":
			w.locked = p[1]
		case "state":
			w.state = p[1]
		case "desktop":
			w.desktop = p[1]
		}
	}
}

// readSession reads the session.txt of the case in dir, or returns nil
// when it has none.
func readSession(t *testing.T, dir string) *session {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "session.txt"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	s, err := parseSession(string(data))
	if err != nil {
		t.Fatalf("session.txt: %v", err)
	}
	return s
}

// parseSession parses the text of a session.txt. Blank lines and "#"
// lines are left out. The world lines at @0 before any other line set the
// world at startup; the other lines make the groups.
func parseSession(text string) (*session, error) {
	s := &session{
		world:   sessionWorld{locked: "false", state: "active", desktop: "input"},
		tracker: winsession.NewTracker(0),
	}
	startup := true
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		ms, ok := strings.CutPrefix(fields[0], "@")
		n, err := strconv.ParseInt(ms, 10, 64)
		if !ok || err != nil || n < 0 || n > math.MaxInt64/int64(time.Millisecond) || len(fields) < 2 {
			return nil, fmt.Errorf("line %d: %q is not @<ms> world|events|<signal>", i+1, line)
		}
		at := clock.Time(n * int64(time.Millisecond))
		var l sessionLine
		switch word := fields[1]; word {
		case "world":
			if l.world, err = parseWorld(fields[2:]); err != nil {
				return nil, fmt.Errorf("line %d: %w", i+1, err)
			}
		case "events":
			l.events = true
		default:
			sig, ok := winsession.ParseSignal(word)
			if !ok || sig == winsession.SigInit {
				return nil, fmt.Errorf("line %d: %q is no signal but init", i+1, word)
			}
			l.sig = sig
		}
		if l.world == nil && len(fields) > 2 {
			return nil, fmt.Errorf("line %d: %q after %s", i+1, fields[2], fields[1])
		}
		if startup && at == 0 && l.world != nil {
			s.world.set(l.world)
			continue
		}
		startup = false
		k := len(s.groups)
		switch {
		case k > 0 && at < s.groups[k-1].at:
			return nil, fmt.Errorf("line %d: @%d is before the line before it", i+1, n)
		case k == 0 || at > s.groups[k-1].at:
			s.groups = append(s.groups, sessionGroup{at: at})
		}
		g := &s.groups[len(s.groups)-1]
		if l.events && g.events {
			return nil, fmt.Errorf("line %d: a second events line at @%d", i+1, n)
		}
		g.events = g.events || l.events
		g.lines = append(g.lines, l)
	}
	return s, nil
}

// parseWorld parses the key=value pairs of a world line.
func parseWorld(fields []string) ([][2]string, error) {
	if len(fields) == 0 {
		return nil, errors.New("a world line with no key=value")
	}
	var pairs [][2]string
	for _, f := range fields {
		k, v, _ := strings.Cut(f, "=")
		if !slices.Contains(worldValues[k], v) {
			return nil, fmt.Errorf("world: %q is none of locked=true|false|unknown, state=active|disconnected|fail, desktop=input|other|fail", f)
		}
		if slices.ContainsFunc(pairs, func(p [2]string) bool { return p[0] == k }) {
			return nil, fmt.Errorf("world: %s given twice", k)
		}
		pairs = append(pairs, [2]string{k, v})
	}
	return pairs, nil
}

// init steps the Tracker's SigInit at startup and returns its reasons,
// for Suspend before Run.
func (s *session) init() winsession.Reason {
	return s.tracker.Step(winsession.SigInit, 0, &s.world).Now
}

// apply applies g at the clock's time, then waits for a Sync: a world
// line changes the world, each step of a signal that changes the reasons
// calls SuspendQuiet with them, and an events line sends batch, the trace
// events of the instant, with no Sync between them.
func (s *session) apply(w *slotsWriter, g sessionGroup, batch []input.Event) {
	h := w.h
	h.t.Helper()
	for _, l := range g.lines {
		switch {
		case l.world != nil:
			s.world.set(l.world)
		case l.events:
			for _, e := range batch {
				h.send(e)
			}
		default:
			if c := s.tracker.Step(l.sig, time.Duration(h.clk.Now()), &s.world); c.Changed() {
				h.sup.SuspendQuiet(c.Now)
			}
		}
	}
	w.sync()
}

// TestParseSession checks the errors of parseSession, and the startup
// and the groups of a session.txt.
func TestParseSession(t *testing.T) {
	for _, tc := range []struct{ text, err string }{
		{"lock", "not @<ms>"},
		{"@x lock", "not @<ms>"},
		{"@-1 lock", "not @<ms>"},
		{"@100", "not @<ms>"},
		{"@100 init", "no signal but init"},
		{"@100 wobble", "no signal but init"},
		{"@100 lock now", `"now" after lock`},
		{"@100 events 2", `"2" after events`},
		{"@100 world", "no key=value"},
		{"@100 world locked=maybe", "none of"},
		{"@100 world shade=red", "none of"},
		{"@100 world locked=true locked=false", "locked given twice"},
		{"@200 lock\n@100 unlock", "line 2: @100 is before"},
		{"@100 events\n@100 lock\n@100 events", "line 3: a second events line"},
	} {
		if _, err := parseSession(tc.text); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("parseSession(%q) = %v; want an error with %q", tc.text, err, tc.err)
		}
	}

	s, err := parseSession("# a comment\n\n@0 world locked=true\n@0 world desktop=other\n@0 tick\n@0 world locked=false\n@100 world state=fail\n@100 unlock\n@100 events\n@300 lock\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.init(); got != winsession.Lock|winsession.Desktop {
		t.Errorf("init() = %v; want lock,desktop", got)
	}
	var got []string
	for _, g := range s.groups {
		got = append(got, fmt.Sprintf("@%s %d %v", g.at.AppendMs(nil), len(g.lines), g.events))
	}
	if want := []string{"@0 2 false", "@100 3 true", "@300 1 false"}; !slices.Equal(got, want) {
		t.Errorf("groups %q; want %q", got, want)
	}
	// A failed query changes nothing.
	s.world.set(s.groups[1].lines[0].world)
	if c := s.tracker.Step(winsession.SigUnlock, time.Second, &s.world); c.Now != winsession.Desktop {
		t.Errorf("unlock with the session query failing: %v; want desktop", c.Now)
	}
}
