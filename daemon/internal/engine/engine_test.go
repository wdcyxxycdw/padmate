package engine_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

const ms = time.Millisecond

// Bindings of the base layer, as JSON members, for the test profiles.
const (
	eastHold   = `"east": {"label": "抓手", "press": {"type": "keys", "keys": "Space", "mode": "hold"}}`
	eastShift  = `"east": {"label": "移动", "press": {"type": "keys", "keys": "Shift", "mode": "hold"}}`
	southTap   = `"south": {"label": "撤销", "press": {"type": "keys", "keys": "Ctrl+Z", "mode": "tap"}}`
	ltAlt      = `"lt": {"press": {"type": "keys", "keys": "Alt"}}`
	rtShift    = `"rt": {"press": {"type": "keys", "keys": "Shift"}}`
	lbAlt      = `"lb": {"press": {"type": "keys", "keys": "Alt"}}`
	xLabelOnly = `"x": {"label": "todo"}`
)

// compile parses a profile whose base layer has the given bindings and
// compiles it. Warnings are fine; errors fail the test.
func compile(t testing.TB, bindings ...string) *engine.Program {
	t.Helper()
	src := `{"schemaVersion": 0, "name": "t", "layers": {"base": {` + strings.Join(bindings, ", ") + `}}}`
	p, ds := profile.Parse([]byte(src))
	if p == nil {
		t.Fatalf("profile.Parse: %v", ds)
	}
	prog, err := engine.Compile(p)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return prog
}

// recorder is an engine.Output that writes each command as text, such as
// "@0 down decl/1 Space", with the time of the step that sent it.
type recorder struct {
	now clock.Time
	out []string
}

func (r *recorder) add(s string) {
	r.out = append(r.out, "@"+strconv.FormatInt(int64(time.Duration(r.now)/ms), 10)+" "+s)
}

func (r *recorder) Down(o engine.OwnerID, c keys.Chord, minPress time.Duration) {
	s := "down " + o.String() + " " + c.String()
	if minPress != 0 {
		s += " " + minPress.String()
	}
	r.add(s)
}
func (r *recorder) Up(o engine.OwnerID, c keys.Chord) { r.add("up " + o.String() + " " + c.String()) }
func (r *recorder) Tap(o engine.OwnerID, c keys.Chord, hold time.Duration) {
	r.add("tap " + o.String() + " " + c.String() + " " + hold.String())
}
func (r *recorder) Repeat(o engine.OwnerID, c keys.Chord) {
	r.add("repeat " + o.String() + " " + c.String())
}

func (r *recorder) TapSequence(o engine.OwnerID, items []keys.Chord, hold, gap time.Duration) {
	parts := make([]string, len(items))
	for i, c := range items {
		parts[i] = c.String()
	}
	r.add("sequence " + o.String() + " [" + strings.Join(parts, ",") + "] " + hold.String() + " " + gap.String())
}
func (r *recorder) Sequence(o engine.OwnerID, steps []keys.Step, hold, gap time.Duration) {
	r.add("steps " + o.String() + " [" + keys.FormatSteps(steps) + "] " + hold.String() + " " + gap.String())
}
func (r *recorder) TypeText(o engine.OwnerID, text string, gap time.Duration) {
	r.add("type " + o.String() + " " + strconv.Quote(text) + " " + gap.String())
}
func (r *recorder) RepeatTap(o engine.OwnerID, c keys.Chord, hold time.Duration) {
	r.add("repeat-tap " + o.String() + " " + c.String() + " " + hold.String())
}
func (r *recorder) StopRepeat(o engine.OwnerID, c keys.Chord) {
	r.add("stop-repeat " + o.String() + " " + c.String())
}
func (r *recorder) Move(o engine.OwnerID, dx, dy int32) {
	r.add("move " + o.String() + " " + strconv.Itoa(int(dx)) + "," + strconv.Itoa(int(dy)))
}
func (r *recorder) Jump(o engine.OwnerID, x, y int32) {
	r.add("jump " + o.String() + " " + strconv.Itoa(int(x)) + "," + strconv.Itoa(int(y)))
}

func (r *recorder) String() string { return strings.Join(r.out, ", ") }

// brief formats d as "@t control [edge] reason [keys] [label]", such as
// "@0 east down dispatch Space 抓手".
func brief(d engine.Decision) string {
	s := "@" + strconv.FormatInt(int64(time.Duration(d.T)/ms), 10) + " " + d.Control.String()
	if d.Edge != engine.EdgeNone {
		s += " " + d.Edge.String()
	}
	s += " " + d.Reason.String()
	if d.Action != nil {
		s += " " + d.Action.Keys.String()
	}
	if d.Binding != nil && d.Binding.Label != "" {
		s += " " + d.Binding.Label
	}
	return s
}

// run feeds script to e. The script is whitespace-separated steps, each
// at a time t in milliseconds: "+name@t" and "-name@t" are button edges
// (aliases allowed), "axis=raw@t" is a raw axis value such as
// "lefttrigger=9830@10", and "cancel@t", "reset@t", "apply@t", "pause@t"
// and "resume@t" call CancelAll, Reset, Apply, Pause and Resume.
func run(t *testing.T, e *engine.Engine, r *recorder, script string) {
	t.Helper()
	for _, tok := range strings.Fields(script) {
		step, at, ok := strings.Cut(tok, "@")
		n, err := strconv.Atoi(at)
		if !ok || err != nil {
			t.Fatalf("step %q: want name@ms", tok)
		}
		now := clock.Time(time.Duration(n) * ms)
		r.now = now
		switch {
		case step == "cancel":
			e.CancelAll(now)
		case step == "reset":
			e.Reset(now)
		case step == "apply":
			e.Apply(now)
		case step == "pause":
			e.Pause(now)
		case step == "resume":
			e.Resume()
		case step[0] == '+' || step[0] == '-':
			c, ok := control.Parse(step[1:])
			if !ok {
				t.Fatalf("step %q: unknown control", tok)
			}
			e.Step(engine.Input{T: now, Kind: engine.InputButton, Control: c, Down: step[0] == '+'})
		default:
			name, v, _ := strings.Cut(step, "=")
			a, ok := control.ParseAxis(name)
			raw, err := strconv.ParseInt(v, 10, 16)
			if !ok || err != nil {
				t.Fatalf("step %q: want +control, -control or axis=raw", tok)
			}
			e.Step(engine.Input{T: now, Kind: engine.InputAxis, Axis: a, Value: int16(raw)})
		}
	}
}

func TestStep(t *testing.T) {
	for _, tc := range []struct {
		name     string
		bindings []string
		input    string
		want     string
	}{
		{
			name:     "hold pass-through",
			bindings: []string{eastHold},
			input:    "+east@0 -east@300",
			want:     "@0 down decl/1 Space, @300 up decl/1 Space",
		},
		{
			name:     "hold is the default mode of press",
			bindings: []string{`"east": {"press": {"type": "keys", "keys": "Space"}}`},
			input:    "+east@0 -east@300",
			want:     "@0 down decl/1 Space, @300 up decl/1 Space",
		},
		{
			name:     "hold of a chord with modifiers",
			bindings: []string{`"east": {"press": {"type": "keys", "keys": "ctrl+shift+z", "mode": "hold"}}`},
			input:    "+east@0 -east@300",
			want:     "@0 down decl/1 Ctrl+Shift+Z, @300 up decl/1 Ctrl+Shift+Z",
		},
		{
			name:     "tap mode taps at the press and ignores the release",
			bindings: []string{southTap},
			input:    "+south@0 -south@10 +south@100 -south@200",
			want:     "@0 tap decl/1 Ctrl+Z 20ms, @100 tap decl/2 Ctrl+Z 20ms",
		},
		{
			name:     "binding written with an alias",
			bindings: []string{`"a": {"press": {"type": "keys", "keys": "Space"}}`},
			input:    "+south@0 -south@50",
			want:     "@0 down decl/1 Space, @50 up decl/1 Space",
		},
		{
			name:     "unbound control starts a cycle but sends nothing",
			bindings: []string{eastHold},
			input:    "+west@0 -west@10 +east@20 -east@30",
			want:     "@20 down decl/2 Space, @30 up decl/2 Space",
		},
		{
			name:     "binding without an action",
			bindings: []string{xLabelOnly},
			input:    "+west@0 -west@10",
			want:     "",
		},
		{
			name:     "spurious edges",
			bindings: []string{eastHold},
			input:    "-east@0 +east@10 +east@20 -east@30 -east@40",
			want:     "@10 down decl/1 Space, @30 up decl/1 Space",
		},
		{
			name:     "equal times",
			bindings: []string{eastHold, southTap},
			input:    "+east@0 +south@0 -east@0 -south@0",
			want:     "@0 down decl/1 Space, @0 tap decl/2 Ctrl+Z 20ms, @0 up decl/1 Space",
		},
		{
			name:     "cycles count across controls",
			bindings: []string{eastHold, southTap},
			input:    "+east@0 +south@10 -south@20 -east@30 +east@40 -east@50",
			want:     "@0 down decl/1 Space, @10 tap decl/2 Ctrl+Z 20ms, @30 up decl/1 Space, @40 down decl/3 Space, @50 up decl/3 Space",
		},
		{
			name:     "trigger thresholds on the way up and down",
			bindings: []string{ltAlt},
			input: "lefttrigger=6553@0 lefttrigger=9829@10 lefttrigger=9830@20 lefttrigger=32767@30 " +
				"lefttrigger=9829@40 lefttrigger=6554@50 lefttrigger=6553@60 lefttrigger=0@70",
			want: "@20 down decl/1 Alt, @60 up decl/1 Alt",
		},
		{
			name:     "trigger hysteresis",
			bindings: []string{ltAlt},
			input: "lefttrigger=20000@0 lefttrigger=8000@10 lefttrigger=9830@20 lefttrigger=6554@30 " +
				"lefttrigger=6000@40 lefttrigger=9000@50 lefttrigger=6553@60 lefttrigger=9830@70 lefttrigger=0@80",
			want: "@0 down decl/1 Alt, @40 up decl/1 Alt, @70 down decl/2 Alt, @80 up decl/2 Alt",
		},
		{
			name:     "repeated trigger crossings",
			bindings: []string{ltAlt},
			input: "lefttrigger=32767@0 lefttrigger=0@10 lefttrigger=32767@20 lefttrigger=0@30 " +
				"lefttrigger=32767@40 lefttrigger=0@50",
			want: "@0 down decl/1 Alt, @10 up decl/1 Alt, @20 down decl/2 Alt, @30 up decl/2 Alt, @40 down decl/3 Alt, @50 up decl/3 Alt",
		},
		{
			name:     "negative trigger values count as 0",
			bindings: []string{ltAlt},
			input:    "lefttrigger=-32768@0 lefttrigger=-9830@5 lefttrigger=10000@10 lefttrigger=-1@20",
			want:     "@10 down decl/1 Alt, @20 up decl/1 Alt",
		},
		{
			name:     "both triggers are independent",
			bindings: []string{ltAlt, rtShift},
			input:    "lefttrigger=10000@0 righttrigger=10000@10 lefttrigger=0@20 righttrigger=32767@30 righttrigger=0@40",
			want:     "@0 down decl/1 Alt, @10 down decl/2 Shift, @20 up decl/1 Alt, @40 up decl/2 Shift",
		},
		{
			name:     "trigger and button on the same chord",
			bindings: []string{ltAlt, lbAlt},
			input:    "lefttrigger=10000@0 +lb@10 lefttrigger=0@20 -lb@30",
			want:     "@0 down decl/1 Alt, @10 down decl/2 Alt, @20 up decl/1 Alt, @30 up decl/2 Alt",
		},
		{
			name:     "trigger in tap mode",
			bindings: []string{`"rt": {"press": {"type": "keys", "keys": "F13", "mode": "tap"}}`},
			input:    "righttrigger=32767@0 righttrigger=0@50",
			want:     "@0 tap decl/1 F13 20ms",
		},
		{
			name:     "stick axes are ignored",
			bindings: []string{eastHold},
			input:    "leftx=32767@0 lefty=-32768@10 rightx=20000@20 righty=0@30",
			want:     "",
		},
		{
			name:     "trigger buttons are ignored",
			bindings: []string{ltAlt},
			input:    "+lt@0 -lt@10 +rt@20 -rt@30",
			want:     "",
		},
		{
			name:     "CancelAll ends every cycle without releasing",
			bindings: []string{eastHold, southTap, ltAlt},
			// west is unbound: its cycle (4) ends too. The trigger stays
			// down until it falls to the release threshold, then presses
			// again from there.
			input: "+east@0 +south@10 lefttrigger=20000@20 +west@25 cancel@30 -east@40 -south@50 -west@55 " +
				"lefttrigger=15000@60 lefttrigger=32767@65 lefttrigger=6553@70 lefttrigger=9830@80 +east@90 -east@100 lefttrigger=0@110",
			want: "@0 down decl/1 Space, @10 tap decl/2 Ctrl+Z 20ms, @20 down decl/3 Alt, " +
				"@80 down decl/5 Alt, @90 down decl/6 Space, @100 up decl/6 Space, @110 up decl/5 Alt",
		},
		{
			name:     "a press after CancelAll works while the control is still down",
			bindings: []string{eastHold, southTap},
			input:    "+east@0 cancel@10 +south@20 -south@30 -east@40 +east@50 -east@60",
			want:     "@0 down decl/1 Space, @20 tap decl/2 Ctrl+Z 20ms, @50 down decl/3 Space, @60 up decl/3 Space",
		},
		{
			name:     "CancelAll twice",
			bindings: []string{eastHold},
			input:    "+east@0 cancel@10 cancel@20 -east@30 +east@40 -east@50",
			want:     "@0 down decl/1 Space, @40 down decl/2 Space, @50 up decl/2 Space",
		},
		{
			name:     "CancelAll with nothing down",
			bindings: []string{eastHold},
			input:    "cancel@0 +east@10 -east@20",
			want:     "@10 down decl/1 Space, @20 up decl/1 Space",
		},
		{
			name:     "Reset forgets which controls are down",
			bindings: []string{eastHold, ltAlt},
			// After Reset the release of east is spurious, and the trigger
			// value presses lt again at once.
			input: "+east@0 lefttrigger=20000@10 reset@20 -east@30 lefttrigger=20000@40 +east@50 -east@60 lefttrigger=0@70",
			want:  "@0 down decl/1 Space, @10 down decl/2 Alt, @40 down decl/3 Alt, @50 down decl/4 Space, @60 up decl/4 Space, @70 up decl/3 Alt",
		},
		{
			name:     "Reset with nothing down",
			bindings: []string{eastHold},
			input:    "reset@0 +east@10 -east@20",
			want:     "@10 down decl/1 Space, @20 up decl/1 Space",
		},
		{
			name:     "Apply does nothing in M1a",
			bindings: []string{eastHold, southTap},
			input:    "+east@0 apply@0 +south@10 apply@20 apply@1000 -south@1500 -east@2000",
			want:     "@0 down decl/1 Space, @10 tap decl/2 Ctrl+Z 20ms, @2000 up decl/1 Space",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			e := engine.New(compile(t, tc.bindings...), r, engine.Options{})
			run(t, e, r, tc.input)
			if got := r.String(); got != tc.want {
				t.Errorf("output:\n got %s\nwant %s", got, tc.want)
			}
		})
	}
}

// TestStepInvalidInput checks inputs the slot never sends: they are
// ignored, and ignoring them leaves the engine working.
func TestStepInvalidInput(t *testing.T) {
	r := &recorder{}
	var got []string
	e := engine.New(compile(t, eastHold, ltAlt), r, engine.Options{Trace: func(d engine.Decision) { got = append(got, d.String()) }})
	for _, in := range []engine.Input{
		{Kind: engine.InputButton, Control: control.Invalid, Down: true},
		{Kind: engine.InputButton, Control: control.Control(200), Down: false},
		{Kind: engine.InputButton, Control: control.RT, Down: true},
		{Kind: 0, Control: control.East, Down: true},
		{Kind: 9, Axis: control.LeftTrigger, Value: 32767},
		{Kind: engine.InputAxis, Axis: control.InvalidAxis, Value: 32767},
		{Kind: engine.InputAxis, Axis: control.Axis(99), Value: 32767},
		{Kind: engine.InputAxis, Control: control.LT, Axis: control.LeftX, Value: 32767},
	} {
		e.Step(in)
	}
	// The stick axis is valid input, just unused: it gets no decision.
	want := []string{
		`{"t":0,"control":"control(0)","edge":"down","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"control(200)","edge":"up","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"rt","edge":"down","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"east","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"control(0)","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"control(0)","cycle":0,"reason":"spurious"}`,
		`{"t":0,"control":"control(0)","cycle":0,"reason":"spurious"}`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("decisions:\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if r.String() != "" {
		t.Errorf("output %s, want none", r)
	}
	run(t, e, r, "+east@10 -east@20")
	if want := "@10 down decl/1 Space, @20 up decl/1 Space"; r.String() != want {
		t.Errorf("output after invalid inputs:\n got %s\nwant %s", r, want)
	}
}

// TestReload switches engines to another program (PRO-03) at each
// "reload@t" of the input, alternating between the program of bindings
// and that of reloaded: the cycles end as with CancelAll, the controls
// down stay down, and later presses use the new program.
func TestReload(t *testing.T) {
	const (
		southRedo = `"south": {"label": "重做", "press": {"type": "keys", "keys": "Ctrl+Y", "mode": "tap"}}`
		ltCtrl    = `"lt": {"press": {"type": "keys", "keys": "Ctrl"}}`
	)
	for _, tc := range []struct {
		name                string
		bindings, reloaded  []string // nil reloaded: the same Program
		input               string
		want, wantDecisions string
	}{
		{
			name:     "a held control is canceled and its release does nothing",
			bindings: []string{eastHold}, reloaded: []string{eastShift},
			input: "+east@0 reload@10 -east@20 +east@30 -east@40",
			want:  "@0 down decl/1 Space, @30 down decl/2 Shift, @40 up decl/2 Shift",
			// The canceled release reports the binding its press found.
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @20 east up canceled 抓手, " +
				"@30 east down dispatch Shift 移动, @40 east up release Shift 移动",
		},
		{
			name:     "a tap's press ends at the reload",
			bindings: []string{southTap}, reloaded: []string{southRedo},
			input:         "+south@0 reload@5 -south@10 +south@20 -south@30",
			want:          "@0 tap decl/1 Ctrl+Z 20ms, @20 tap decl/2 Ctrl+Y 20ms",
			wantDecisions: "@0 south down dispatch Ctrl+Z 撤销, @5 south cancel 撤销, @10 south up canceled 撤销, @20 south down dispatch Ctrl+Y 重做, @30 south up end 重做",
		},
		{
			name:     "a trigger held across the reload presses again under the new program after it falls",
			bindings: []string{ltAlt}, reloaded: []string{ltCtrl},
			input:         "lefttrigger=20000@0 reload@10 lefttrigger=32767@20 lefttrigger=6554@30 lefttrigger=6553@40 lefttrigger=9830@50 lefttrigger=0@60",
			want:          "@0 down decl/1 Alt, @50 down decl/2 Ctrl, @60 up decl/2 Ctrl",
			wantDecisions: "@0 lt down dispatch Alt, @10 lt cancel Alt, @40 lt up canceled, @50 lt down dispatch Ctrl, @60 lt up release Ctrl",
		},
		{
			name:     "reload with the same program",
			bindings: []string{eastHold},
			input:    "+east@0 reload@10 -east@20 +east@30 -east@40",
			want:     "@0 down decl/1 Space, @30 down decl/2 Space, @40 up decl/2 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @20 east up canceled 抓手, " +
				"@30 east down dispatch Space 抓手, @40 east up release Space 抓手",
		},
		{
			name:     "reload with nothing down",
			bindings: []string{eastHold}, reloaded: []string{eastShift},
			input:         "reload@0 +east@10 -east@20",
			want:          "@10 down decl/1 Shift, @20 up decl/1 Shift",
			wantDecisions: "@10 east down dispatch Shift 移动, @20 east up release Shift 移动",
		},
		{
			name:     "two reloads while held cancel once, and the controls up use the latest program",
			bindings: []string{eastHold, southTap}, reloaded: []string{eastShift, southRedo},
			input: "+east@0 reload@10 reload@20 -east@30 +south@40 +east@50 -east@60",
			want:  "@0 down decl/1 Space, @40 tap decl/2 Ctrl+Z 20ms, @50 down decl/3 Space, @60 up decl/3 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @30 east up canceled 抓手, " +
				"@40 south down dispatch Ctrl+Z 撤销, @50 east down dispatch Space 抓手, @60 east up release Space 抓手",
		},
		{
			name:     "a binding the new program drops",
			bindings: []string{eastHold, southTap}, reloaded: []string{southTap},
			input:         "+east@0 reload@10 -east@20 +east@30 -east@40",
			want:          "@0 down decl/1 Space",
			wantDecisions: "@0 east down dispatch Space 抓手, @10 east cancel Space 抓手, @20 east up canceled 抓手, @30 east down unbound, @40 east up end",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			progs := [2]*engine.Program{compile(t, tc.bindings...)}
			progs[1] = progs[0]
			if tc.reloaded != nil {
				progs[1] = compile(t, tc.reloaded...)
			}
			r := &recorder{}
			var decisions []string
			e := engine.New(progs[0], r, engine.Options{Trace: func(d engine.Decision) {
				decisions = append(decisions, brief(d))
			}})
			reloads := 0
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
				reloads++
				r.now = clock.Time(time.Duration(n) * ms)
				e.Reload(r.now, progs[reloads%2])
			}
			if got := r.String(); got != tc.want {
				t.Errorf("output:\n got %s\nwant %s", got, tc.want)
			}
			if got := strings.Join(decisions, ", "); got != tc.wantDecisions {
				t.Errorf("decisions:\n got %s\nwant %s", got, tc.wantDecisions)
			}
		})
	}
}

func TestNewPanics(t *testing.T) {
	prog := compile(t, eastHold)
	for name, fn := range map[string]func(){
		"nil program": func() { engine.New(nil, &recorder{}, engine.Options{}) },
		"nil output":  func() { engine.New(prog, nil, engine.Options{}) },
		"Reload of a nil program": func() {
			engine.New(prog, &recorder{}, engine.Options{}).Reload(0, nil)
		},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("New did not panic")
				}
			}()
			fn()
		})
	}
}

// TestEnginesAreIndependent checks that engines sharing a Program keep
// their own state and cycle counters.
func TestEnginesAreIndependent(t *testing.T) {
	prog := compile(t, eastHold)
	r1, r2 := &recorder{}, &recorder{}
	e1 := engine.New(prog, r1, engine.Options{})
	e2 := engine.New(prog, r2, engine.Options{})
	run(t, e1, r1, "+east@0")
	run(t, e2, r2, "+east@5 -east@10 +east@15")
	run(t, e1, r1, "-east@20")
	if want := "@0 down decl/1 Space, @20 up decl/1 Space"; r1.String() != want {
		t.Errorf("engine 1:\n got %s\nwant %s", r1, want)
	}
	if want := "@5 down decl/1 Space, @10 up decl/1 Space, @15 down decl/2 Space"; r2.String() != want {
		t.Errorf("engine 2:\n got %s\nwant %s", r2, want)
	}
}
