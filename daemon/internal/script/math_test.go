package script

import (
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func TestMathModuleNormalizationAndRateCap(t *testing.T) {
	dir := t.TempDir()
	source := `import {clamp,lerp,remap,deadzone,curve,angle,sector,Hysteresis,RateAccumulator,SectorTracker} from "padmate/math";
const assert=(ok,label)=>{if(!ok)throw Error(label)};
assert(clamp(2,0,1)===1&&lerp(0,10,.5)===5&&remap(.5,0,1,10,20)===15,"basic");
assert(Math.abs(deadzone(-.6,.2,1)+.5)<1e-12&&curve(-.5,"quadratic")===-.25,"curve");
assert(angle(0,1)===0&&angle(1,0)===90&&angle(0,-1)===180&&angle(-1,0)===270,"angles");
assert(sector(359,4)===0&&sector(45,4)===1&&sector(45,4,45)===0,"sectors");
let h=new Hysteresis(.3,.2);assert(!h.update(.25)&&h.update(.3)&&h.update(.25)&&!h.update(.2),"hysteresis");
let s=new SectorTracker(4,{hysteresisDeg:8});assert(s.update(0)===0&&s.update(50)===0&&s.update(54)===1,"sector hysteresis");s.reset();assert(s.update(50)===1,"reset");
let a=new RateAccumulator();assert(a.step(20,1000)===0,"dt must be capped to two ticks");assert(a.step(20,1000)===0&&a.step(20,1000)===0&&a.step(20,1000)===1,"accumulate bounded dt");
a.reset();assert(a.step(1000,1000)===1&&a.step(0,8)===0,"no replay backlog");`
	if err := writeScript(dir, "main.ts", source); err != nil {
		t.Fatal(err)
	}
	r, err := compile.Build(compile.BuildOptions{Root: dir, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("math.js", string(r.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHost(clock.NewFake())
	defer func() { _ = h.Dispose() }()
	if err := h.Activate(p); err != nil {
		t.Fatal(err)
	}
}
