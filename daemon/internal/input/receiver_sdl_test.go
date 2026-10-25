package input_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
)

// These tests need the SDL library, as those of source_test.go do. They
// drive the receiver watchdog (IN-30) with virtual Switch Pro gamepads
// whose heartbeat reports the test sends (Virtual.Beat, Virtual.Report):
// SDL sees nothing of those reports, only T-input's heartbeat source.

// recvTimeout is the ReceiverTimeout of these tests: prove 250 ms, gap
// 62.5 ms.
const recvTimeout = 500 * time.Millisecond

// recvBeat is how often the tests' heartbeats come.
const recvBeat = 5 * time.Millisecond

// syncLog is a log that T-input writes while the test reads it.
type syncLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *syncLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

// records returns the records with message msg, decoded.
func (l *syncLog) records(t *testing.T, msg string) []map[string]any {
	t.Helper()
	l.mu.Lock()
	text := l.b.String()
	l.mu.Unlock()
	var out []map[string]any
	for line := range strings.Lines(text) {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if r["msg"] == msg {
			out = append(out, r)
		}
	}
	return out
}

// wait returns the records with message msg once there are n of them or
// more, waiting up to 5 s: a goroutine of the Source writes T-input's log,
// after T-input has sent the events that go with it.
func (l *syncLog) wait(t *testing.T, msg string, n int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		r := l.records(t, msg)
		if len(r) >= n || time.Now().After(deadline) {
			return r
		}
		time.Sleep(time.Millisecond)
	}
}

func (l *syncLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// openReceiver opens a Source with the receiver watchdog at timeout, 0
// for off, logging at debug level, and closes it at the end of the test.
func openReceiver(t *testing.T, timeout time.Duration) (*input.Source, *syncLog) {
	t.Helper()
	lb := &syncLog{}
	s, err := input.Open(input.Options{
		Library:         inputtest.Library(t),
		ReceiverTimeout: timeout,
		Log:             slog.New(slog.NewJSONHandler(lb, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	return s, lb
}

// attachDesc adds the virtual gamepad d and returns it with its
// DeviceAdded event.
func attachDesc(t *testing.T, s *input.Source, d input.VirtualDesc) (*input.Virtual, input.Event) {
	t.Helper()
	v, err := s.AttachVirtualDesc(d)
	if err != nil {
		t.Fatalf("AttachVirtualDesc: %v", err)
	}
	evs := collect(t, s, v.ID(), 1)
	if evs[0].Kind != input.DeviceAdded {
		t.Fatalf("first event %+v, want DeviceAdded", evs[0])
	}
	return v, evs[0]
}

// state returns the watchdog's state of v.
func state(t *testing.T, s *input.Source, v *input.Virtual) input.ReceiverStatus {
	t.Helper()
	st, err := s.ReceiverState(v)
	if err != nil {
		t.Fatalf("ReceiverState: %v", err)
	}
	return st
}

// waitArmed waits until the watchdog of v is armed, and returns its state.
func waitArmed(t *testing.T, s *input.Source, v *input.Virtual) input.ReceiverStatus {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := state(t, s, v)
		if st.Armed {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("not armed after 5 s: %+v", st)
		}
		time.Sleep(recvBeat)
	}
}

// events returns the next n events of the devices ids, in order.
func events(t *testing.T, s *input.Source, n int, ids ...input.DeviceID) []input.Event {
	t.Helper()
	var evs []input.Event
	timeout := time.After(5 * time.Second)
	for len(evs) < n {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatalf("Events closed after %d of %d events: %v", len(evs), n, evs)
			}
			for _, id := range ids {
				if e.Device == id {
					evs = append(evs, e)
				}
			}
		case <-timeout:
			t.Fatalf("timed out after %d of %d events: %v", len(evs), n, evs)
		}
	}
	return evs
}

// silence checks that no event of the devices ids arrives within d.
func silence(t *testing.T, s *input.Source, d time.Duration, ids ...input.DeviceID) {
	t.Helper()
	timeout := time.After(d)
	for {
		select {
		case e, ok := <-s.Events():
			if !ok {
				t.Fatal("Events closed")
			}
			for _, id := range ids {
				if e.Device == id {
					t.Fatalf("unexpected event %+v", e)
				}
			}
		case <-timeout:
			return
		}
	}
}

// removal waits for the DeviceRemoved of id, the next event of id, and
// checks that it comes between timeout and timeout+1s after since, the
// time of the last heartbeat or control event, less recvSlack.
func removal(t *testing.T, s *input.Source, id input.DeviceID, since time.Time) {
	t.Helper()
	e := events(t, s, 1, id)[0]
	elapsed := time.Since(since)
	if e.Kind != input.DeviceRemoved {
		t.Fatalf("event %+v, want DeviceRemoved", e)
	}
	if elapsed < recvTimeout-recvSlack || elapsed > recvTimeout+time.Second {
		t.Fatalf("DeviceRemoved %v after the heartbeat stopped, want %v to %v", elapsed, recvTimeout, recvTimeout+time.Second)
	}
}

// recvSlack covers the two clocks: T-input times the watchdog with
// SDL_GetTicksNS, the test with time.Now, which on Windows moves in steps
// of 0.5 to 2 ms (docs/PITFALLS.md). A loss earlier than the timeout by
// more than that still fails.
const recvSlack = 50 * time.Millisecond

// stopped stops the heartbeat with stop and waits for the removal of id
// that follows the last heartbeat.
func stopped(t *testing.T, s *input.Source, id input.DeviceID, stop func() time.Time) {
	t.Helper()
	last := stop()
	if last.IsZero() {
		t.Fatal("no heartbeat was sent")
	}
	removal(t, s, id, last)
}

func setButton(t *testing.T, v *input.Virtual, control string, down bool) {
	t.Helper()
	if err := v.SetButton(control, down); err != nil {
		t.Fatalf("SetButton(%s, %v): %v", control, down, err)
	}
}

// feed sends report(i) to v every recvBeat, for i = 0, 1, …, until stop
// is called.
func feed(t *testing.T, v *input.Virtual, report func(i int) []byte) (stop func()) {
	t.Helper()
	done, quit := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(recvBeat)
		defer tick.Stop()
		for i := 0; ; i++ {
			if err := v.Report(report(i)); err != nil {
				return
			}
			select {
			case <-quit:
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() { close(quit) })
		<-done
	}
	t.Cleanup(stop)
	return stop
}

// report returns a report with id and timer, 13 bytes long.
func report(id, timer byte) []byte {
	return []byte{id, timer, 0x90, 0, 0, 0, 0x00, 0x08, 0x80, 0x00, 0x08, 0x80, 0}
}

// TestHIDBindings calls the three SDL_hid_* bindings on T-input with
// values SDL rejects: a path that does not exist, and the NULL device,
// which SDL's CHECK_DEVICE_MAGIC turns into -1. It guards their ABI,
// the sign of the C int results included, and on Windows/amd64 that
// reading allocates nothing. Elsewhere the bindings are purego's
// RegisterLibFunc, which allocates on every call (docs/PITFALLS.md).
func TestHIDBindings(t *testing.T) {
	s := open(t, nil)
	r, err := s.HIDProbe(`\\?\HID#VID_0000&PID_0000#padmate-missing`)
	if errors.Is(err, input.ErrNoHIDBinding) {
		t.Skip(err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if r.Dev != 0 || r.Read != -1 || r.Closed != -1 {
		t.Errorf("open %#x, read %d, close %d; want 0, -1, -1", r.Dev, r.Read, r.Closed)
	}
	if r.DrainErr == nil {
		t.Error("a drain of the NULL device did not fail")
	}
	// T-input reads every watched device at least every 5 ms: on
	// Windows/amd64, neither the binding nor a drain through it may
	// allocate.
	if runtime.GOOS == "windows" && runtime.GOARCH == "amd64" && (r.ReadAllocs != 0 || r.DrainAllocs != 0) {
		t.Errorf("SDL_hid_read_timeout allocates %v times a call, a drain %v times; want 0, 0", r.ReadAllocs, r.DrainAllocs)
	}
	// T-input still works.
	v, _ := attach(t, s, "hid probe")
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
}

// TestReceiverHeldStillKeeps: a gamepad whose heartbeat goes on keeps a
// button held for seconds, without a single event.
func TestReceiverHeldStillKeeps(t *testing.T) {
	s, _ := openReceiver(t, recvTimeout)
	v, added := attachDesc(t, s, inputtest.SwitchPro("receiver held"))
	if added.Info.Type != "switchpro" || added.Info.Vendor != 0x057e || added.Info.Product != 0x2009 {
		t.Fatalf("info %+v, want a switchpro 057e:2009", added.Info)
	}
	inputtest.Beats(t, v, recvBeat)
	if st := waitArmed(t, s, v); !st.Watched || st.Dormant || st.Pub != v.ID() {
		t.Fatalf("state %+v", st)
	}
	setButton(t, v, "south", true)
	expect(t, s, v.ID(), "south down")
	silence(t, s, 3*recvTimeout, v.ID())
	setButton(t, v, "south", false)
	expect(t, s, v.ID(), "south up")
}

// TestReceiverLossAndRevival: a gamepad whose heartbeat stops while it
// holds east is reported removed a timeout later, and nothing more of its
// id. Its next SDL event brings it back as a new device, with a new id
// from 2³²−1 down and the same info and key otherwise; the release of
// east follows as an orphan up. Feedback reaches it by its new id only.
func TestReceiverLossAndRevival(t *testing.T) {
	s, _ := openReceiver(t, recvTimeout)
	v, added := attachDesc(t, s, inputtest.SwitchPro("receiver loss"))
	reg := input.NewRegistry()
	key, _ := reg.Connect(added.Info)
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	setButton(t, v, "east", true)
	expect(t, s, v.ID(), "east down")
	stopped(t, s, v.ID(), stop)
	if _, ok := reg.Disconnect(v.ID()); !ok {
		t.Fatal("Disconnect failed")
	}
	if st := state(t, s, v); !st.Dormant || !st.Watched || st.Pub != v.ID() {
		t.Fatalf("state %+v after the loss, want dormant and still watched", st)
	}

	const next = input.DeviceID(math.MaxUint32)
	setButton(t, v, "east", false)
	evs := events(t, s, 2, v.ID(), next)
	if evs[0].Kind != input.DeviceAdded || evs[0].Device != next || evs[0].Info == nil || evs[0].Info.ID != next {
		t.Fatalf("event %+v, want DeviceAdded of %d", evs[0], next)
	}
	was, now := *added.Info, *evs[0].Info
	was.ID = next
	if was != now {
		t.Errorf("info %+v, want %+v but the id", now, was)
	}
	if k, reconnected := reg.Connect(evs[0].Info); !reconnected || k != key {
		t.Errorf("Connect = %v, %v; want %v reconnected", k, reconnected, key)
	}
	if format(evs[1]) != "east up" || evs[1].Device != next {
		t.Errorf("event %+v, want the orphan east up of %d", evs[1], next)
	}
	setButton(t, v, "south", true)
	if e := events(t, s, 1, v.ID(), next)[0]; format(e) != "south down" || e.Device != next {
		t.Errorf("event %+v, want south down of %d", e, next)
	}
	if st := state(t, s, v); st.Dormant || st.Pub != next || !st.Watched {
		t.Errorf("state %+v after the revival", st)
	}

	// A virtual gamepad can do no effect, but Identify finds it by its new id,
	// and not at all by the old id. SetFeedback ignores both without an error:
	// the Mixer does nothing a gamepad cannot.
	if _, err := s.Identify(next); errors.Is(err, input.ErrNoDevice) {
		t.Errorf("Identify of the new id: %v, want it found", err)
	}
	if _, err := s.Identify(v.ID()); !errors.Is(err, input.ErrNoDevice) {
		t.Errorf("Identify of the old id: %v, want ErrNoDevice", err)
	}
	if err := s.SetFeedback(input.Feedback{Device: next, Color: "#123456"}); err != nil {
		t.Errorf("SetFeedback of the new id: %v, want a virtual gamepad to ignore its LED", err)
	}
	if err := s.SetFeedback(input.Feedback{Device: v.ID(), Color: "#654321"}); err != nil {
		t.Errorf("SetFeedback of the old id: %v, want nothing done", err)
	}
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
	evs = events(t, s, 2, v.ID(), next)
	if format(evs[0]) != "south up" || evs[1].Kind != input.DeviceRemoved || evs[1].Device != next {
		t.Fatalf("events %+v, want south up and the DeviceRemoved of %d", evs, next)
	}
	silence(t, s, 100*time.Millisecond, v.ID(), next)
}

// TestReceiverIdleLoss: an idle gamepad whose heartbeat stops is reported
// removed too, so that a latched key or a script's key is released; the
// log says so.
func TestReceiverIdleLoss(t *testing.T) {
	s, lb := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver idle"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	stopped(t, s, v.ID(), stop)
	r := lb.wait(t, "receiver silent: reported removed", 1)
	if len(r) != 1 || r[0]["level"] != "WARN" || r[0]["id"] != float64(v.ID()) {
		t.Fatalf("receiver silent records %v, want one warn of %d\nlog:\n%s", r, v.ID(), lb)
	}
	silent, err := time.ParseDuration(r[0]["silent"].(string))
	if err != nil || silent < recvTimeout {
		t.Errorf("silent %v, %v; want a duration of at least %v", r[0]["silent"], err, recvTimeout)
	}
	if len(lb.records(t, "receiver heartbeat watching")) != 1 || len(lb.records(t, "receiver heartbeat armed")) != 1 {
		t.Errorf("want one watching and one armed record:\n%s", lb)
	}
}

// TestReceiverDormantNeedsControl: a dormant gamepad whose heartbeat comes
// back stays gone until one of its controls moves. Then it is back, must
// prove its heartbeat again, and is lost again when it stops.
func TestReceiverDormantNeedsControl(t *testing.T) {
	s, lb := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver dormant"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	stopped(t, s, v.ID(), stop)

	const next = input.DeviceID(math.MaxUint32)
	stop = inputtest.Beats(t, v, recvBeat)
	silence(t, s, 2*recvTimeout, v.ID(), next)
	setButton(t, v, "south", true)
	evs := events(t, s, 2, v.ID(), next)
	if evs[0].Kind != input.DeviceAdded || evs[0].Device != next || format(evs[1]) != "south down" || evs[1].Device != next {
		t.Fatalf("events %+v, want DeviceAdded and south down of %d", evs, next)
	}
	if r := lb.wait(t, "receiver back: reported as a new device", 1); len(r) != 1 || r[0]["id"] != float64(next) || r[0]["replaces"] != float64(v.ID()) {
		t.Errorf("receiver back records %v", r)
	}
	if st := waitArmed(t, s, v); st.Pub != next {
		t.Fatalf("state %+v, want armed as %d", st, next)
	}
	stopped(t, s, next, stop)
}

// TestReceiverRestBeforeArm: heartbeats while a stick moves prove nothing,
// so the watchdog does not arm and does not report a loss when they stop.
// It arms once the gamepad has rested for prove with its heartbeat on.
func TestReceiverRestBeforeArm(t *testing.T) {
	s, _ := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver moving"))
	prove := input.ReceiverProve(recvTimeout)
	stop := inputtest.Beats(t, v, recvBeat)
	for i, end := 0, time.Now().Add(3*prove); time.Now().Before(end); i++ {
		if err := v.SetAxis("leftx", int16(20000*(i%2*2-1))); err != nil {
			t.Fatal(err)
		}
		if st := state(t, s, v); st.Armed {
			t.Fatalf("armed while moving: %+v", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	stop()
	quietFor := time.After(2 * recvTimeout)
	for done := false; !done; {
		select {
		case e := <-s.Events():
			if e.Device == v.ID() && e.Kind != input.AxisMotion {
				t.Fatalf("unexpected event %+v", e)
			}
		case <-quietFor:
			done = true
		}
	}
	inputtest.Beats(t, v, recvBeat)
	start := time.Now()
	waitArmed(t, s, v)
	if d := time.Since(start); d < prove-2*recvBeat {
		t.Errorf("armed %v after resting, want at least %v", d, prove)
	}
}

// TestReceiverChangesNeverArm: a gamepad that reports only when its input
// changes, here every report with a small change of its left stick and
// an SDL event inside the band, as a thumb that rests on the stick makes,
// never arms (ADR-0029 decision 5). So when it holds a button still and
// sends nothing, it is not reported removed.
func TestReceiverChangesNeverArm(t *testing.T) {
	s, _ := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver changes"))
	prove := input.ReceiverProve(recvTimeout)
	stop := feed(t, v, func(i int) []byte {
		r := report(0x30, byte(i))
		r[6] = byte(i % 2)
		return r
	})
	for i, end := 0, time.Now().Add(4*prove); time.Now().Before(end); i++ {
		if err := v.SetAxis("leftx", int16(300*(i%2))); err != nil {
			t.Fatal(err)
		}
		if st := state(t, s, v); st.Armed || !st.Watched {
			t.Fatalf("state %+v while every report changes, want watched and not armed", st)
		}
		time.Sleep(10 * time.Millisecond)
	}
	setButton(t, v, "east", true)
	stop()
	quietFor := time.After(3 * recvTimeout)
	for done := false; !done; {
		select {
		case e := <-s.Events():
			if e.Device == v.ID() && e.Kind != input.AxisMotion && format(e) != "east down" {
				t.Fatalf("unexpected event %+v", e)
			}
		case <-quietFor:
			done = true
		}
	}
	setButton(t, v, "east", false)
	expect(t, s, v.ID(), "east up")
}

// simpleReport is a simple report (0x3F) of a gamepad at rest.
var simpleReport = []byte{0x3f, 0x00, 0x00, 0x08, 0x00, 0x80, 0x00, 0x80, 0x00, 0x80, 0x00, 0x80}

// TestReceiverModeChange: an armed gamepad that another program puts into
// simple reports (0x3F) goes on working, as SDL reads those too, but
// reports only on changes. The watchdog disarms and says so, so the
// gamepad is not reported removed when it then holds east still; once its
// heartbeat is back it arms again.
func TestReceiverModeChange(t *testing.T) {
	s, lb := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver mode"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	stop()
	for _, down := range []bool{true, false, true} {
		if err := v.Report(simpleReport); err != nil {
			t.Fatal(err)
		}
		setButton(t, v, "east", down)
		if down {
			expect(t, s, v.ID(), "east down")
		} else {
			expect(t, s, v.ID(), "east up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	silence(t, s, 3*recvTimeout, v.ID())
	if st := state(t, s, v); st.Armed || st.Dormant || !st.Watched {
		t.Fatalf("state %+v after the simple reports, want watched, not armed, not dormant", st)
	}
	r := lb.wait(t, "receiver heartbeat mode changed", 1)
	if len(r) != 1 || r[0]["level"] != "INFO" || r[0]["id"] != float64(v.ID()) || strings.Contains(fmt.Sprint(r[0]["ids"]), "3f:0") {
		t.Errorf("mode changed records %v, want one info of %d after a simple report", r, v.ID())
	}
	setButton(t, v, "east", false)
	expect(t, s, v.ID(), "east up")

	inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	if r := lb.wait(t, "receiver heartbeat armed", 2); len(r) != 2 {
		t.Errorf("armed records %v, want two", r)
	}
}

// TestReceiverWakeAxes: the report that wakes a dormant gamepad may move
// several axes at once, as pressing ZL and ZR together does. After the
// DeviceAdded each of them is reported; none is dropped as a repeat of
// the value SDL holds by then.
func TestReceiverWakeAxes(t *testing.T) {
	s, _ := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver wake axes"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	stopped(t, s, v.ID(), stop)

	const next = input.DeviceID(math.MaxUint32)
	for _, value := range []int16{32767, 0} {
		if err := v.SetAxes(map[string]int16{"lefttrigger": value, "righttrigger": value}); err != nil {
			t.Fatal(err)
		}
		n := 2
		if value != 0 {
			n = 3 // the DeviceAdded first
		}
		evs := events(t, s, n, v.ID(), next)
		if n == 3 {
			if evs[0].Kind != input.DeviceAdded || evs[0].Device != next {
				t.Fatalf("events %+v, want the DeviceAdded of %d first", evs, next)
			}
			evs = evs[1:]
		}
		var got []string
		for _, e := range evs {
			if e.Device != next {
				t.Fatalf("event %+v of the old id", e)
			}
			got = append(got, format(e))
		}
		slices.Sort(got)
		if want := []string{fmt.Sprintf("lefttrigger %d", value), fmt.Sprintf("righttrigger %d", value)}; !slices.Equal(got, want) {
			t.Errorf("axis events %q, want %q", got, want)
		}
	}
}

// blockedLog is a log whose every write waits until gate is closed, as a
// write to a console does while text is selected in it.
type blockedLog struct{ gate chan struct{} }

func (w blockedLog) Write(p []byte) (int, error) {
	<-w.gate
	return len(p), nil
}

// TestReceiverLogBlocked: T-input never waits for its log (ADR-0029
// decision 11). With a log that blocks from the first record on, the
// watchdog still arms, reports the silent gamepad removed and brings it
// back, and Close waits for the log a bounded time only.
func TestReceiverLogBlocked(t *testing.T) {
	w := blockedLog{gate: make(chan struct{})}
	s, err := input.Open(input.Options{
		Library:         inputtest.Library(t),
		ReceiverTimeout: recvTimeout,
		Log:             slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	// The gate opens before that Close, and in any case after 10 s, so
	// that a T-input that waits for its log fails the test instead of
	// hanging it.
	var once sync.Once
	open := func() { once.Do(func() { close(w.gate) }) }
	t.Cleanup(open)
	hung := time.AfterFunc(10*time.Second, func() {
		t.Error("T-input still waits for its log after 10 s; the log is opened")
		open()
	})
	t.Cleanup(func() { hung.Stop() })
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver log blocked"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	setButton(t, v, "east", true)
	expect(t, s, v.ID(), "east down")
	stopped(t, s, v.ID(), stop)
	const next = input.DeviceID(math.MaxUint32)
	setButton(t, v, "east", false)
	if evs := events(t, s, 2, v.ID(), next); evs[0].Kind != input.DeviceAdded || evs[0].Device != next ||
		format(evs[1]) != "east up" || evs[1].Device != next {
		t.Fatalf("events %+v, want DeviceAdded and east up of %d", evs, next)
	}
	start := time.Now()
	s.Close()
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Close took %v with the log blocked, want about 1s", d)
	}
}

// TestReceiverReportKinds sends raw reports: a 0x30 whose timer froze is
// no heartbeat, so the gamepad is lost; subcommand replies (0x21), USB
// replies (0x81) and simple reports (0x3F) never prove a heartbeat.
func TestReceiverReportKinds(t *testing.T) {
	t.Run("frozen timer", func(t *testing.T) {
		s, _ := openReceiver(t, recvTimeout)
		v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver frozen"))
		stop := feed(t, v, func(i int) []byte { return report(0x30, byte(i)) })
		waitArmed(t, s, v)
		stop()
		feed(t, v, func(int) []byte { return report(0x30, 7) })
		since := time.Now()
		setButton(t, v, "east", true)
		expect(t, s, v.ID(), "east down")
		removal(t, s, v.ID(), since)
	})
	for _, tc := range []struct {
		name   string
		report func(i int) []byte
	}{
		{"replies", func(i int) []byte {
			if i%2 == 0 {
				return report(0x21, byte(i))
			}
			return []byte{0x81, 0x01, 0x00, 0x03}
		}},
		{"simple", func(i int) []byte { return []byte{0x3f, 0x00, 0x00, 0x08, 0x00, 0x80, byte(i)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := openReceiver(t, recvTimeout)
			v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver "+tc.name))
			stop := feed(t, v, tc.report)
			setButton(t, v, "east", true)
			expect(t, s, v.ID(), "east down")
			silence(t, s, 3*recvTimeout, v.ID())
			if st := state(t, s, v); st.Armed || !st.Watched {
				t.Errorf("state %+v, want watched and not armed", st)
			}
			stop()
			silence(t, s, 2*recvTimeout, v.ID())
		})
	}
}

// TestReceiverNotWatched: a virtual gamepad that is not a Pro protocol one
// with a heartbeat, or a Source without the watchdog, is never watched,
// and silence never removes it.
func TestReceiverNotWatched(t *testing.T) {
	for _, tc := range []struct {
		name    string
		timeout time.Duration
		desc    input.VirtualDesc
	}{
		{"standard with heartbeat", recvTimeout, input.VirtualDesc{Name: "receiver standard", Heartbeat: true}},
		{"Pro without heartbeat", recvTimeout, func() input.VirtualDesc {
			d := inputtest.SwitchPro("receiver no heartbeat")
			d.Heartbeat = false
			return d
		}()},
		{"watchdog off", 0, inputtest.SwitchPro("receiver off")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, lb := openReceiver(t, tc.timeout)
			v, _ := attachDesc(t, s, tc.desc)
			if st := state(t, s, v); st.Watched {
				t.Fatalf("state %+v, want not watched", st)
			}
			if err := v.Beat(); (err == nil) != tc.desc.Heartbeat {
				t.Errorf("Beat = %v with Heartbeat %v", err, tc.desc.Heartbeat)
			}
			setButton(t, v, "south", true)
			expect(t, s, v.ID(), "south down")
			silence(t, s, 2*recvTimeout, v.ID())
			if strings.Contains(lb.String(), "receiver heartbeat") {
				t.Errorf("the log has heartbeat records:\n%s", lb)
			}
			if err := v.Detach(); err != nil {
				t.Fatal(err)
			}
			if err := v.Beat(); tc.desc.Heartbeat && !errors.Is(err, input.ErrDetached) {
				t.Errorf("Beat after Detach = %v, want ErrDetached", err)
			}
		})
	}
}

// TestReceiverDormantDetach: when a dormant gamepad goes for real, SDL's
// recentering release is dropped, since SDL no longer lists it, and its
// removal is not reported a second time.
func TestReceiverDormantDetach(t *testing.T) {
	s, lb := openReceiver(t, recvTimeout)
	v, _ := attachDesc(t, s, inputtest.SwitchPro("receiver detach"))
	stop := inputtest.Beats(t, v, recvBeat)
	waitArmed(t, s, v)
	setButton(t, v, "east", true)
	expect(t, s, v.ID(), "east down")
	stopped(t, s, v.ID(), stop)
	gone, err := s.Gone()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Detach(); err != nil {
		t.Fatal(err)
	}
	silence(t, s, 200*time.Millisecond, v.ID(), math.MaxUint32)
	if g, err := s.Gone(); err != nil || g != gone {
		t.Errorf("Gone = %d, %v; want %d", g, err, gone)
	}
	if r := lb.wait(t, "receiver heartbeat ended", 1); len(r) != 1 || r[0]["id"] != float64(v.ID()) {
		t.Errorf("receiver heartbeat ended records %v, want one", r)
	}
}
