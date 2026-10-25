package engine_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// A macro action runs the macro once through Options.Script, on every
// gesture, and holds nothing: there are no key commands, the release
// sends nothing, a hold runs once at holdMs however long the control stays
// down, and latch does not keep anything (SCR-50).
func TestMacroAction(t *testing.T) {
	r := &recorder{}
	var runs []string
	e := engine.New(compile(t,
		`"south": {"press": {"type": "macro", "file": "按下.ts"}, "release": {"type": "macro", "file": "松开.ts"}, "latch": true}`,
		`"east": {"tap": {"type": "macro", "file": "点按.ts"}, "hold": {"type": "macro", "file": "长按.js"}}`,
		`"west": {"doubleTap": {"type": "macro", "file": "双击.ts"}}`,
	), r, engine.Options{Script: func(ev engine.ScriptActionEvent) {
		if ev.Name != "" || ev.Args != nil {
			t.Errorf("macro event with a name: %+v", ev)
		}
		runs = append(runs, fmt.Sprintf("@%d %s %s %s", time.Duration(ev.Time)/time.Millisecond, ev.Macro, ev.Control, ev.Gesture))
	}})
	step := func(c control.Control, down bool, at int) {
		r.now = clock.Time(time.Duration(at) * time.Millisecond)
		e.Apply(r.now)
		e.Step(engine.Input{T: r.now, Kind: engine.InputButton, Control: c, Down: down})
	}
	step(control.South, true, 0)
	step(control.South, false, 50)
	step(control.East, true, 100)
	step(control.East, false, 150)
	step(control.East, true, 1000)
	if d, ok := e.NextDeadline(); ok {
		e.Apply(d)
	}
	step(control.East, false, 3000)
	step(control.West, true, 4000)
	step(control.West, false, 4020)
	step(control.West, true, 4040)
	step(control.West, false, 4060)
	e.Apply(clock.Time(5 * time.Second))
	want := []string{
		"@0 按下.ts south press", "@50 松开.ts south release",
		"@150 点按.ts east tap", "@1250 长按.js east hold",
		"@4060 双击.ts west doubleTap",
	}
	if strings.Join(runs, "\n") != strings.Join(want, "\n") {
		t.Errorf("runs:\n%s\nwant:\n%s", strings.Join(runs, "\n"), strings.Join(want, "\n"))
	}
	if r.String() != "" {
		t.Errorf("key commands %q", r.String())
	}
	if v := e.View(); len(v.Latches) != 0 {
		t.Errorf("latches %v", v.Latches)
	}
}

// The observer sees the transitions of the bound controls and of the
// controls Button would see, but does not consume them, and does not make
// an unbound stick direction a control.
func TestButtonObserver(t *testing.T) {
	r := &recorder{}
	var seen []string
	e := engine.New(compileAnalog(t, `{"ls":{"directionDelayMs":0}}`, `"south":{"press":{"type":"keys","keys":"F13","mode":"hold"}},"ls.up":{"press":{"type":"keys","keys":"U"}}`), r, engine.Options{
		ButtonObserver: func(ev engine.ButtonEvent) { seen = append(seen, fmt.Sprintf("%s %v", ev.Control, ev.Down)) },
	})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftX, Value: 30000}) // ls.right: unbound
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftX})
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftY, Value: -30000}) // ls.up: bound
	e.Step(engine.Input{Kind: engine.InputAxis, Axis: control.LeftY})
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South})
	want := "south true, ls.up true, ls.up false, south false"
	if got := strings.Join(seen, ", "); got != want {
		t.Errorf("observed %q, want %q", got, want)
	}
	if got := r.String(); got != "@0 down decl/1 F13, @0 down decl/2 U, @0 up decl/2 U, @0 up decl/1 F13" {
		t.Errorf("output %q", got)
	}
	e.SetButtonObserver(nil)
	e.Step(engine.Input{Kind: engine.InputButton, Control: control.South, Down: true})
	if len(seen) != 4 {
		t.Errorf("observer still called: %v", seen)
	}
	e.CancelAll(0)
}

// Compile checks a macro action's file name as Parse does.
func TestCompileMacroFile(t *testing.T) {
	for file, ok := range map[string]bool{"复制全部.ts": true, "a.js": true, "": false, "a.TS": false, "a.d.ts": false, "CON.ts": false} {
		b := &profile.Binding{ID: "/layers/base/south", Control: control.South,
			Tap: &profile.Action{ID: "/layers/base/south/tap", Type: profile.ActionMacro, File: file}}
		_, err := engine.Compile(&profile.Profile{Layers: []*profile.Layer{{Name: "base", Bindings: []*profile.Binding{b}}}})
		if (err == nil) != ok {
			t.Errorf("%q: Compile = %v", file, err)
		}
		if cerr := engine.CheckBinding(b); (cerr == nil) != ok {
			t.Errorf("%q: CheckBinding = %v", file, cerr)
		}
	}
}
