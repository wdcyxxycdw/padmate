package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/haptic"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// effectRecorder collects what the worker of an effectQueue sends, in order,
// as text.
type effectRecorder struct {
	mu   sync.Mutex
	got  []string
	hold map[string]chan struct{} // a call named here waits for its channel
	seen map[string]chan struct{} // closed when that call has started
}

func (r *effectRecorder) sender() effectSender {
	return effectSender{
		feedback: func(f input.Feedback) error { return r.record(fmt.Sprintf("feedback %d %s", f.Device, f.Color)) },
		play: func(e input.Effect) error {
			return r.record(fmt.Sprintf("play %d %v", e.Device, e.Preset))
		},
		light: func(l input.Light) error {
			if l.Release {
				return r.record(fmt.Sprintf("release %d %v", l.Device, l.Class))
			}
			return r.record(fmt.Sprintf("light %d %v %v", l.Device, l.Class, l.Color))
		},
		stop: func(st input.Stop) error {
			return r.record(fmt.Sprintf("stop %d %v %v", st.Device, st.Class, st.Everything))
		},
	}
}

func (r *effectRecorder) record(call string) error {
	r.mu.Lock()
	enter, wait := r.seen[call], r.hold[call]
	r.mu.Unlock()
	if enter != nil {
		close(enter)
	}
	if wait != nil {
		<-wait
	}
	r.mu.Lock()
	r.got = append(r.got, call)
	r.mu.Unlock()
	return nil
}

func (r *effectRecorder) calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.got...)
}

// waitCalls waits until the recorder has n calls, and returns them.
func (r *effectRecorder) waitCalls(t *testing.T, n int) []string {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if got := r.calls(); len(got) >= n {
			return got
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waited for %d calls, have %v", n, r.calls())
	return nil
}

// blockFirst makes the call named call wait for the returned channel, and
// the other returned channel is closed when that call has begun.
func (r *effectRecorder) blockFirst(call string) (resume chan struct{}, entered <-chan struct{}) {
	resume, enter := make(chan struct{}), make(chan struct{})
	r.hold = map[string]chan struct{}{call: resume}
	r.seen = map[string]chan struct{}{call: enter}
	return resume, enter
}

func TestEffectQueueSerializesAndCoalesces(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("feedback 1 first")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.post(input.Feedback{Device: 1, Color: "first"})
	<-entered
	posted := make(chan struct{})
	go func() {
		q.post(input.Feedback{Device: 1, Color: "old"})
		q.post(input.Feedback{Device: 2, Color: "other"})
		q.post(input.Feedback{Device: 1, Color: "latest", Paused: true})
		close(posted)
	}()
	select {
	case <-posted:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("effect post blocked mapping")
	}
	close(resume)
	got := r.waitCalls(t, 3)
	q.close()
	// The latest state of device 1 took the place of the old one, which was
	// ahead of the other device's.
	if want := []string{"feedback 1 first", "feedback 1 latest", "feedback 2 other"}; !equalStrings(got, want) {
		t.Fatalf("effect order: %v, want %v", got, want)
	}
	q.post(input.Feedback{Device: 1, Color: "late"}) // after close: ignored
	q.postEffect(input.Effect{Device: 1})
}

func equalStrings(a, b []string) bool { return strings.Join(a, "\n") == strings.Join(b, "\n") }

// One-shot effects are not merged, nor reordered, and the ambient state of a
// device keeps its place among them.
func TestEffectQueueKeepsEventsInOrder(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 tick")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Preset: haptic.Tick})
	<-entered
	q.postEffect(input.Effect{Device: 1, Preset: haptic.Confirm})
	q.post(input.Feedback{Device: 1, Color: "a"})
	q.postEffect(input.Effect{Device: 2, Preset: haptic.Tick})
	q.postEffect(input.Effect{Device: 1, Preset: haptic.Confirm}) // twice: two confirms
	q.post(input.Feedback{Device: 1, Color: "b"})                 // replaces "a" in its place
	close(resume)
	got := r.waitCalls(t, 5)
	q.close()
	want := []string{"play 1 tick", "play 1 confirm", "feedback 1 b", "play 2 tick", "play 1 confirm"}
	if !equalStrings(got, want) {
		t.Fatalf("order: %v, want %v", got, want)
	}
}

// A line of one-shot effects that the worker cannot keep up with is bounded:
// the oldest go first, the ambient state is not one of them, and post never
// waits.
func TestEffectQueueBoundsEvents(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 error")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Preset: haptic.Error})
	<-entered
	q.post(input.Feedback{Device: 7, Color: "layer"})
	done := make(chan struct{})
	go func() {
		for i := range 3 * maxPendingEvents {
			q.postEffect(input.Effect{Device: input.DeviceID(100 + i), Preset: haptic.Tick})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(resume)
		t.Fatal("posting events blocked")
	}
	close(resume)
	got := r.waitCalls(t, 2+maxPendingEvents)
	q.close()
	if len(got) != 2+maxPendingEvents || got[0] != "play 1 error" || got[1] != "feedback 7 layer" {
		t.Fatalf("calls %v: want the running effect, the ambient state, and %d events", got, maxPendingEvents)
	}
	first := 100 + 3*maxPendingEvents - maxPendingEvents
	for i, call := range got[2:] {
		if want := fmt.Sprintf("play %d tick", first+i); call != want {
			t.Fatalf("event %d is %q, want %q (the newest %d, in order)", i, call, want, maxPendingEvents)
		}
	}
}

// When the line of one-shot effects is full, a burst of Event-class ticks is
// what goes first: a low-battery alert (Alert class) that waits among them is
// not dropped for them, however old it is.
func TestEffectQueueDropsEventsBeforeAlerts(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 error")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Class: haptic.Event, Preset: haptic.Error})
	<-entered
	q.postEffect(input.Effect{Device: 9, Class: haptic.Alert, Preset: haptic.Pulse3})
	for i := range 3 * maxPendingEvents {
		q.postEffect(input.Effect{Device: input.DeviceID(100 + i), Class: haptic.Event, Preset: haptic.Tick})
	}
	close(resume)
	got := r.waitCalls(t, 1+maxPendingEvents)
	q.close()
	if len(got) != 1+maxPendingEvents || got[0] != "play 1 error" || got[1] != "play 9 pulse3" {
		t.Fatalf("calls %v: want the running effect, the alert, and the newest %d ticks", got, maxPendingEvents-1)
	}
	first := 100 + 3*maxPendingEvents - (maxPendingEvents - 1)
	for i, call := range got[2:] {
		if want := fmt.Sprintf("play %d tick", first+i); call != want {
			t.Fatalf("tick %d is %q, want %q", i, call, want)
		}
	}

	// Only alerts in the line: the oldest of them goes, as for any class.
	r = &effectRecorder{}
	resume, entered = r.blockFirst("play 1 pulse3")
	q = startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Pulse3})
	<-entered
	for i := range maxPendingEvents + 3 {
		q.postEffect(input.Effect{Device: input.DeviceID(200 + i), Class: haptic.Alert, Preset: haptic.Pulse3})
	}
	close(resume)
	got = r.waitCalls(t, 1+maxPendingEvents)
	q.close()
	if len(got) != 1+maxPendingEvents || got[1] != "play 203 pulse3" {
		t.Fatalf("calls %v: want the three oldest alerts dropped", got)
	}
}

func TestEffectQueuePanicKeepsProcessAlive(t *testing.T) {
	for name, send := range map[string]effectSender{
		"feedback": {feedback: func(input.Feedback) error { panic("feedback driver") }},
		"play":     {play: func(input.Effect) error { panic("play driver") }},
	} {
		t.Run(name, func(t *testing.T) {
			q := startEffectQueue(send, nil)
			if name == "feedback" {
				q.post(input.Feedback{Device: 1})
			} else {
				q.postEffect(input.Effect{Device: 1})
			}
			select {
			case <-q.done:
			case <-time.After(time.Second):
				t.Fatal("panicked effect worker did not stop")
			}
			if q.failure == nil || !strings.Contains(q.failure.Error(), "driver") {
				t.Errorf("failure %v, want the panic", q.failure)
			}
			q.post(input.Feedback{Device: 1})
			q.postEffect(input.Effect{Device: 1})
			q.close()
			q.close()
		})
	}
}

func TestEffectQueueCloseDropsWhatWaits(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("feedback 1 busy")
	q := startEffectQueue(r.sender(), nil)
	q.post(input.Feedback{Device: 1, Color: "busy"})
	<-entered
	q.post(input.Feedback{Device: 2, Color: "waiting"})
	q.postEffect(input.Effect{Device: 2, Preset: haptic.Tick})
	closed := make(chan struct{})
	go func() { q.close(); close(closed) }()
	// close waits for the worker, which is in the middle of a call.
	select {
	case <-closed:
		t.Fatal("close returned while the worker was in a call")
	case <-time.After(20 * time.Millisecond):
	}
	close(resume)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("close did not return")
	}
	if got := r.calls(); !equalStrings(got, []string{"feedback 1 busy"}) {
		t.Errorf("calls %v: what waited at close was sent", got)
	}
	var nilQueue *effectQueue
	nilQueue.close() // a run without a queue
}

// A failing call is logged at debug level while the queue is open, and the
// worker goes on with the next.
func TestEffectQueueLogsFailures(t *testing.T) {
	var buf syncBuffer
	log := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	boom := errors.New("write failed")
	var mu sync.Mutex
	var got []string
	q := startEffectQueue(effectSender{
		feedback: func(f input.Feedback) error {
			mu.Lock()
			got = append(got, "feedback")
			mu.Unlock()
			return boom
		},
		play: func(e input.Effect) error {
			mu.Lock()
			got = append(got, "play")
			mu.Unlock()
			return input.ErrNoDevice
		},
	}, log)
	q.post(input.Feedback{Device: 3})
	q.postEffect(input.Effect{Device: 4})
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && strings.Count(buf.String(), "controller effect unavailable") < 2 {
		time.Sleep(time.Millisecond)
	}
	q.close()
	out := buf.String()
	if strings.Count(out, "controller effect unavailable") != 2 || !strings.Contains(out, `"device":3`) || !strings.Contains(out, `"device":4`) ||
		!strings.Contains(out, "write failed") || !strings.Contains(out, input.ErrNoDevice.Error()) {
		t.Errorf("log:\n%s", out)
	}
	mu.Lock()
	defer mu.Unlock()
	if !equalStrings(got, []string{"feedback", "play"}) {
		t.Errorf("sent %v", got)
	}
}

// A script's claim on the LED replaces its pending one for the same gamepad and
// class (the last wins, in the place of the first), and is never dropped for a
// burst of ticks.
func TestEffectQueueLightsCoalesceAndSurviveBursts(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 error")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Class: haptic.Script, Preset: haptic.Error})
	<-entered
	q.postLight(input.Light{Device: 1, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}})
	q.postLight(input.Light{Device: 2, Class: haptic.Script, Color: haptic.RGB{4, 5, 6}})
	q.postLight(input.Light{Device: 1, Class: haptic.Alert, Color: haptic.RGB{7, 8, 9}})
	q.postLight(input.Light{Device: 1, Class: haptic.Script, Color: haptic.RGB{9, 9, 9}}) // replaces the first
	for i := range 3 * maxPendingEvents {
		q.postEffect(input.Effect{Device: input.DeviceID(100 + i), Class: haptic.Event, Preset: haptic.Tick})
	}
	q.postLight(input.Light{Device: 2, Class: haptic.Script, Release: true}) // replaces the second: a release
	close(resume)
	got := r.waitCalls(t, 4+maxPendingEvents)
	q.close()
	want := []string{"play 1 error", "light 1 script [9 9 9]", "release 2 script", "light 1 alert [7 8 9]"}
	if !equalStrings(got[:4], want) {
		t.Fatalf("calls %v, want %v and then the newest %d ticks", got, want, maxPendingEvents)
	}
}

// The end of what a script has going is never dropped, however many one-shot
// effects crowd the line, and it takes the script's own effects and claims for
// that gamepad, which it would end at once, out of the line; those of another
// gamepad or class stay.
func TestEffectQueueStopsAreNeverDroppedAndTakeTheirClassOut(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 error")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Class: haptic.Event, Preset: haptic.Error})
	<-entered
	q.postEffect(input.Effect{Device: 1, Class: haptic.Script, Preset: haptic.Tick})
	q.postEffect(input.Effect{Device: 2, Class: haptic.Script, Preset: haptic.Confirm})
	q.postEffect(input.Effect{Device: 1, Class: haptic.Alert, Preset: haptic.Pulse3})
	q.postLight(input.Light{Device: 1, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}})
	q.postLight(input.Light{Device: 1, Class: haptic.Alert, Color: haptic.RGB{4, 5, 6}})
	// The script's rumble stops: its pending effect goes, the claim stays.
	q.postStop(input.Stop{Device: 1, Class: haptic.Script})
	// Everything it has stops: the claim goes too.
	q.postStop(input.Stop{Device: 1, Class: haptic.Script, Everything: true})
	for i := range 3 * maxPendingEvents {
		q.postEffect(input.Effect{Device: input.DeviceID(100 + i), Class: haptic.Event, Preset: haptic.Tick})
	}
	close(resume)
	got := r.waitCalls(t, 6+maxPendingEvents-2) // two effects of other gamepads and classes wait in the line too
	q.close()
	want := []string{
		"play 1 error", "play 2 confirm", "play 1 pulse3", "light 1 alert [4 5 6]", "stop 1 script false", "stop 1 script true",
	}
	if !equalStrings(got[:6], want) {
		t.Fatalf("calls %v, want %v and then the newest %d ticks", got, want, maxPendingEvents-2)
	}
}

// A stop keeps its place among the requests that it does not take out, and
// the worker takes nothing after the queue closed.
func TestEffectQueueStopsKeepTheirPlace(t *testing.T) {
	r := &effectRecorder{}
	resume, entered := r.blockFirst("play 1 error")
	q := startEffectQueue(r.sender(), nil)
	t.Cleanup(q.close)
	q.postEffect(input.Effect{Device: 1, Class: haptic.Event, Preset: haptic.Error})
	<-entered
	q.postEffect(input.Effect{Device: 2, Class: haptic.Script, Preset: haptic.Tick})
	q.postStop(input.Stop{Device: 1, Class: haptic.Script})
	q.postLight(input.Light{Device: 2, Class: haptic.Script, Color: haptic.RGB{1, 2, 3}})
	q.postStop(input.Stop{Device: 1, Class: haptic.Script, Everything: true})
	q.post(input.Feedback{Device: 2, Color: "layer"})
	close(resume)
	got := r.waitCalls(t, 6)
	q.close()
	if want := []string{"play 1 error", "play 2 tick", "stop 1 script false", "light 2 script [1 2 3]", "stop 1 script true", "feedback 2 layer"}; !equalStrings(got, want) {
		t.Fatalf("order: %v, want %v", got, want)
	}
	q.postStop(input.Stop{Device: 1, Class: haptic.Script}) // after close: ignored
	q.postLight(input.Light{Device: 1, Class: haptic.Script, Release: true})
}
