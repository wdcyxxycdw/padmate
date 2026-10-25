package engine_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
)

// TestPause checks paused mode (DMN-06): Pause cancels every cycle and
// sends nothing, a press while paused ends its cycle at once with
// ReasonPaused, and Resume sends nothing. "reload@t" in the input switches
// the engine to the program of reloaded, or to the same program when it is
// nil.
func TestPause(t *testing.T) {
	for _, tc := range []struct {
		name                string
		bindings, reloaded  []string
		input               string
		want, wantDecisions string
		paused              bool // Paused() at the end
	}{
		{
			name:     "Pause cancels every cycle and releases nothing",
			bindings: []string{eastHold, southTap, ltAlt},
			// west is unbound: its cycle ends too. The Arbiter releases the
			// keys, not the engine.
			input: "+east@0 +south@5 lefttrigger=20000@10 +west@15 pause@20 -east@30 -south@35 -west@38 lefttrigger=0@40",
			want:  "@0 down decl/1 Space, @5 tap decl/2 Ctrl+Z 20ms, @10 down decl/3 Alt",
			wantDecisions: "@0 east down dispatch Space 抓手, @5 south down dispatch Ctrl+Z 撤销, @10 lt down dispatch Alt, @15 west down unbound, " +
				"@20 south cancel 撤销, @20 east cancel Space 抓手, @20 west cancel, @20 lt cancel Alt, " +
				"@30 east up canceled 抓手, @35 south up canceled 撤销, @38 west up canceled, @40 lt up canceled",
			paused: true,
		},
		{
			name:     "a press while paused ends its cycle at once and sends nothing",
			bindings: []string{eastHold, southTap, ltAlt},
			input:    "pause@0 +east@10 +south@20 +west@30 lefttrigger=32767@40 -east@50 -south@60 -west@70 lefttrigger=0@80",
			want:     "",
			wantDecisions: "@10 east down paused 抓手, @20 south down paused 撤销, @30 west down paused, @40 lt down paused, " +
				"@50 east up canceled 抓手, @60 south up canceled 撤销, @70 west up canceled, @80 lt up canceled",
			paused: true,
		},
		{
			name:     "Resume sends nothing, and a control held across it stays canceled until it is released",
			bindings: []string{eastHold, southTap},
			input:    "+east@0 pause@10 +south@20 resume@30 -south@40 +east@45 -east@50 +east@60 +south@65 -east@70 -south@75",
			want:     "@0 down decl/1 Space, @60 down decl/3 Space, @65 tap decl/4 Ctrl+Z 20ms, @70 up decl/3 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @20 south down paused 撤销, " +
				"@40 south up canceled 撤销, @45 east down spurious, @50 east up canceled 抓手, " +
				"@60 east down dispatch Space 抓手, @65 south down dispatch Ctrl+Z 撤销, @70 east up release Space 抓手, @75 south up end 撤销",
		},
		{
			name:     "a trigger that crosses while paused presses again only after it falls to the release threshold",
			bindings: []string{ltAlt},
			input: "pause@0 lefttrigger=20000@10 resume@20 lefttrigger=32767@30 lefttrigger=6554@40 lefttrigger=9830@45 " +
				"lefttrigger=6553@50 lefttrigger=9830@60 lefttrigger=0@70",
			want:          "@60 down decl/2 Alt, @70 up decl/2 Alt",
			wantDecisions: "@10 lt down paused, @50 lt up canceled, @60 lt down dispatch Alt, @70 lt up release Alt",
		},
		{
			name:     "a trigger that falls and crosses again while paused starts another paused cycle",
			bindings: []string{ltAlt},
			input: "pause@0 lefttrigger=20000@10 lefttrigger=6553@20 lefttrigger=9830@30 resume@40 lefttrigger=0@50 " +
				"lefttrigger=9830@60 lefttrigger=0@70",
			want:          "@60 down decl/3 Alt, @70 up decl/3 Alt",
			wantDecisions: "@10 lt down paused, @20 lt up canceled, @30 lt down paused, @50 lt up canceled, @60 lt down dispatch Alt, @70 lt up release Alt",
		},
		{
			name:     "Pause while paused decides nothing",
			bindings: []string{eastHold},
			input:    "+east@0 pause@10 pause@20 -east@30 +east@35 pause@38 -east@40 resume@50 +east@60 -east@70",
			want:     "@0 down decl/1 Space, @60 down decl/3 Space, @70 up decl/3 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @30 east up canceled 抓手, " +
				"@35 east down paused 抓手, @40 east up canceled 抓手, @60 east down dispatch Space 抓手, @70 east up release Space 抓手",
		},
		{
			name:     "Resume while not paused does nothing",
			bindings: []string{eastHold},
			input:    "resume@0 +east@10 resume@15 -east@20 pause@30 resume@40 resume@50 +east@60 -east@70",
			want:     "@10 down decl/1 Space, @20 up decl/1 Space, @60 down decl/2 Space, @70 up decl/2 Space",
			wantDecisions: "@10 east down dispatch Space 抓手, @20 east up release Space 抓手, " +
				"@60 east down dispatch Space 抓手, @70 east up release Space 抓手",
		},
		{
			name:     "CancelAll keeps the pause and has nothing to end",
			bindings: []string{eastHold},
			input:    "pause@0 +east@10 cancel@20 -east@30 +east@40 -east@50 resume@60 +east@70 -east@80",
			want:     "@70 down decl/3 Space, @80 up decl/3 Space",
			wantDecisions: "@10 east down paused 抓手, @30 east up canceled 抓手, @40 east down paused 抓手, @50 east up canceled 抓手, " +
				"@70 east down dispatch Space 抓手, @80 east up release Space 抓手",
		},
		{
			name:     "Reset keeps the pause",
			bindings: []string{eastHold, southTap},
			// Reset forgets the controls down, so their releases are
			// spurious; the next press is still paused.
			input: "+east@0 pause@10 +south@15 reset@20 -east@30 -south@35 +east@40 -east@50 resume@60 +east@70 -east@80",
			want:  "@0 down decl/1 Space, @70 down decl/4 Space, @80 up decl/4 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @15 south down paused 撤销, " +
				"@30 east up spurious, @35 south up spurious, @40 east down paused 抓手, @50 east up canceled 抓手, " +
				"@70 east down dispatch Space 抓手, @80 east up release Space 抓手",
		},
		{
			name:     "Reload keeps the pause, and presses after Resume use the new program",
			bindings: []string{eastHold}, reloaded: []string{eastShift},
			// The release after the reload reports the binding its press
			// found; the paused press finds the new one.
			input: "+east@0 pause@10 reload@20 -east@30 +east@40 -east@50 resume@60 +east@70 -east@80",
			want:  "@0 down decl/1 Space, @70 down decl/3 Shift, @80 up decl/3 Shift",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @30 east up canceled 抓手, " +
				"@40 east down paused 移动, @50 east up canceled 移动, @70 east down dispatch Shift 移动, @80 east up release Shift 移动",
		},
		{
			name:     "Reload with the same program while a paused press is held",
			bindings: []string{eastHold},
			input:    "pause@0 +east@10 reload@20 -east@30 reload@40 resume@50 +east@60 -east@70",
			want:     "@60 down decl/2 Space, @70 up decl/2 Space",
			wantDecisions: "@10 east down paused 抓手, @30 east up canceled 抓手, " +
				"@60 east down dispatch Space 抓手, @70 east up release Space 抓手",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prog := compile(t, tc.bindings...)
			reloaded := prog
			if tc.reloaded != nil {
				reloaded = compile(t, tc.reloaded...)
			}
			r := &recorder{}
			var decisions []string
			e := engine.New(prog, r, engine.Options{Trace: func(d engine.Decision) {
				decisions = append(decisions, brief(d))
			}})
			for tok := range strings.FieldsSeq(tc.input) {
				at, ok := strings.CutPrefix(tok, "reload@")
				if !ok {
					run(t, e, r, tok)
					continue
				}
				n, err := strconv.Atoi(at)
				if err != nil {
					t.Fatalf("step %q: want reload@ms", tok)
				}
				r.now = clock.Time(time.Duration(n) * ms)
				e.Reload(r.now, reloaded)
			}
			if got := r.String(); got != tc.want {
				t.Errorf("output:\n got %s\nwant %s", got, tc.want)
			}
			if got := strings.Join(decisions, ", "); got != tc.wantDecisions {
				t.Errorf("decisions:\n got %s\nwant %s", got, tc.wantDecisions)
			}
			if got := e.Paused(); got != tc.paused {
				t.Errorf("Paused() = %v at the end, want %v", got, tc.paused)
			}
		})
	}
}

// TestPaused checks that only Pause and Resume change Paused.
func TestPaused(t *testing.T) {
	prog := compile(t, eastHold)
	e := engine.New(prog, &recorder{}, engine.Options{})
	east := func(down bool) func() {
		return func() { e.Step(engine.Input{Kind: engine.InputButton, Control: control.East, Down: down}) }
	}
	for _, step := range []struct {
		name string
		fn   func()
		want bool
	}{
		{"New", func() {}, false},
		{"press", east(true), false},
		{"Pause", func() { e.Pause(0) }, true},
		{"Pause again", func() { e.Pause(0) }, true},
		{"release", east(false), true},
		{"press", east(true), true},
		{"CancelAll", func() { e.CancelAll(0) }, true},
		{"Reload", func() { e.Reload(0, prog) }, true},
		{"Reset", func() { e.Reset(0) }, true},
		{"Resume", e.Resume, false},
		{"Resume again", e.Resume, false},
		{"CancelAll", func() { e.CancelAll(0) }, false},
		{"Reload", func() { e.Reload(0, prog) }, false},
		{"Reset", func() { e.Reset(0) }, false},
	} {
		step.fn()
		if got := e.Paused(); got != step.want {
			t.Errorf("after %s: Paused() = %v, want %v", step.name, got, step.want)
		}
	}
}
