package script

import (
	"testing"
	"time"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func continuousProgram(t *testing.T, src string) *goja.Program {
	t.Helper()
	p, err := goja.Compile("continuous.js", src, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestContinuousTickDeadlineAndNoBacklog(t *testing.T) {
	c := clock.NewFake()
	h := NewHostWithOptions(HostOptions{Clock: c, TickHz: 125})
	defer func() { _ = h.Dispose() }()
	if err := h.Activate(continuousProgram(t, `const b=__padmate; globalThis.samples=[];
	const d=b.onTick(dt=>samples.push(dt)); globalThis.cancel=()=>d.dispose();`)); err != nil {
		t.Fatal(err)
	}
	if d, ok := h.NextDeadline(); !ok || d != c.Now().Add(8*time.Millisecond) {
		t.Fatalf("deadline=%v,%v", d, ok)
	}
	c.Advance(8 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	c.Advance(time.Second)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	if got := h.VM.Get("samples").ToObject(h.VM).Get("length").ToInteger(); got != 2 {
		t.Fatalf("samples=%d, want 2", got)
	}
	if _, err := h.VM.RunString(`cancel()`); err != nil {
		t.Fatal(err)
	}
	if _, ok := h.NextDeadline(); ok {
		t.Fatal("disposed subscription retained timer")
	}
}

func TestContinuousAnalogDefaultsDirectionAndCopies(t *testing.T) {
	c := clock.NewFake()
	a := profile.DefaultAnalog()
	a.RS.Deadzone = 0.25
	a.RS.Saturation = 0.75
	pad := PadState{Connected: true, Analog: a, AnalogResolved: true}
	pad.Axes[control.RightX] = 16384
	pad.Axes[control.RightY] = 0
	pad.Axes[control.RightTrigger] = 16384
	h := NewHostWithOptions(HostOptions{Clock: c, Pad: pad})
	defer func() { _ = h.Dispose() }()
	if err := h.Activate(continuousProgram(t, `const b=__padmate;
	let first=b.pad.stick("rs"); if(Math.abs(first.magnitude-0.5)>0.001||first.direction(4)!==1||first.angle!==90)throw Error("analog defaults");
	if(Math.abs(b.pad.trigger("rt")-0.5)>0.001)throw Error("trigger");
	let override=b.pad.stick("rs",{deadzone:0,outer:1,curve:"quadratic"}); if(Math.abs(override.magnitude-0.25)>0.001)throw Error("override");
	b.onStick("rs",(s,dt)=>{ if(s.direction(4)!==1||dt!==8)throw Error("stick sample"); });
	b.onAxis("rt",(v,dt)=>{if(v<0.49||v>0.51||dt!==8)throw Error("axis sample");});`)); err != nil {
		t.Fatal(err)
	}
	c.Advance(8 * time.Millisecond)
	if err := h.RunDue(); err != nil {
		t.Fatal(err)
	}
	pad.Axes[control.RightX] = 0
	h.UpdatePad(pad)
	if _, err := h.VM.RunString(`if(__padmate.pad.stick("rs").direction(8)!==-1||first.direction(4)!==1)throw Error("snapshot copy");`); err != nil {
		t.Fatal(err)
	}
}

func TestContinuousSafetyCancelsSubscriptions(t *testing.T) {
	for _, stop := range []string{"Suspend", "ReleaseAll", "Dispose"} {
		t.Run(stop, func(t *testing.T) {
			c := clock.NewFake()
			h := NewHost(c)
			if err := h.Activate(continuousProgram(t, `globalThis.count=0;__padmate.onTick(()=>{count++;__padmate.key.down("Z")});`)); err != nil {
				t.Fatal(err)
			}
			c.Advance(8 * time.Millisecond)
			if err := h.RunDue(); err != nil {
				t.Fatal(err)
			}
			switch stop {
			case "Suspend":
				h.Suspend()
			case "ReleaseAll":
				h.ReleaseAll()
			case "Dispose":
				if err := h.Dispose(); err != nil {
					t.Fatal(err)
				}
			}
			c.Advance(time.Second)
			if err := h.RunDue(); err != nil {
				t.Fatal(err)
			}
			if got := h.VM.Get("count").ToInteger(); got != 1 {
				t.Fatalf("count=%d", got)
			}
			if len(h.base.held) != 0 {
				t.Fatalf("held=%v", h.base.held)
			}
		})
	}
}
