package amgp

import (
	"math"
)

// The gestures of a padmate binding, in the order they are written.
const (
	gPress   = "press"
	gRelease = "release"
	gTap     = "tap"
	gDouble  = "doubleTap"
	gTriple  = "tripleTap"
	gQuad    = "quadTap"
	gHold    = "hold"
)

var gestureOrder = []string{gPress, gRelease, gTap, gDouble, gTriple, gQuad, gHold}

// multiGestures are the gestures of the first four partitions of a cycle.
var multiGestures = [4]string{gTap, gDouble, gTriple, gQuad}

const (
	// longBandMs is the time from which a release zone is a long press: a
	// zone that fires on release after 200 ms or more is not a tap.
	longBandMs = 200
	// maxMultiTapMs is the longest cycle reset time that is taken for the
	// window of a double tap.
	maxMultiTapMs = 1000
	// shortReleaseMs is the release time above which a tap is not the same
	// as AntiMicroX's.
	shortReleaseMs = 50
)

type optInt struct {
	v  int
	ok bool
}

func (o *optInt) set(v int) { o.v, o.ok = v, true }

// binding is a binding of the profile being made.
type binding struct {
	label string
	block bool
	acts  map[string]*obj
	// held[g] is true when acts[g] holds keys until the button is released.
	held       map[string]bool
	holdMs     optInt
	multiTapMs optInt
	tapPressMs optInt
	tapGapMs   optInt
	// keyPress is the time of a keypress slot, which only times keys that are
	// tapped (applyKeyPress).
	keyPress optInt
	// heldReported says that the loss of held keys in a multi-tap partition has
	// been reported for this binding, once being enough.
	heldReported bool
	latch        bool
}

func newBinding() *binding {
	return &binding{acts: map[string]*obj{}, held: map[string]bool{}}
}

// content reports whether the binding does anything.
func (b *binding) content() bool { return b.block || len(b.acts) > 0 }

// timingObj returns the binding's timing, or nil if it has none.
func (b *binding) timingObj() *obj {
	t := newObj()
	for _, m := range []struct {
		name string
		v    optInt
	}{{"holdMs", b.holdMs}, {"multiTapMs", b.multiTapMs}, {"tapPressMs", b.tapPressMs}, {"tapGapMs", b.tapGapMs}} {
		if m.v.ok {
			t.set(m.name, m.v.v)
		}
	}
	if t.empty() {
		return nil
	}
	return t
}

// toObj returns the binding as JSON.
func (b *binding) toObj() *obj {
	o := newObj()
	if b.label != "" {
		o.set("label", b.label)
	}
	if b.block && len(b.acts) == 0 {
		return o.set("block", true)
	}
	for _, g := range gestureOrder {
		if a := b.acts[g]; a != nil {
			o.set(g, a)
		}
	}
	if t := b.timingObj(); t != nil {
		o.set("timing", t)
	}
	if b.latch {
		o.set("latch", true)
	}
	return o
}

// btnKind is what kind of control a button belongs to.
type btnKind int

const (
	kindPlain btnKind = iota
	// kindModifier is the button of a stick's modifier zone, which cannot
	// change the set.
	kindModifier
)

// switchAction is the action that switches the base layer to set t.
func (cv *converter) switchAction(t int) *obj {
	cv.refs[t]++
	return newObj().set("type", "layer").set("layer", cv.layerName(t)).set("op", "switch")
}

// holdAction is the action that holds the layer of set t.
func (cv *converter) holdAction(t int) *obj {
	cv.refs[t]++
	cv.held[t] = true
	return newObj().set("type", "layer").set("layer", cv.layerName(t)).set("op", "hold")
}

// put sets the action of a gesture. The gesture may be taken already, by
// another zone that maps to it.
func (cv *converter) put(bb *binding, at loc, g string, a *obj, held bool) {
	if a == nil {
		return
	}
	if bb.acts[g] != nil {
		cv.rep.dropped("gesture.taken", at, "这个按键的“%s”已经有别的动作，这一个丢弃", gestureName(g))
		return
	}
	bb.acts[g] = a
	bb.held[g] = held
}

// putStructural sets the action of a gesture, replacing what is there: a
// change of set matters more than a key.
func (cv *converter) putStructural(bb *binding, at loc, g string, a *obj) {
	if bb.acts[g] != nil {
		cv.rep.dropped("gesture.replaced", at, "这个按键的“%s”换成了切换 set，原来的动作丢弃", gestureName(g))
	}
	bb.acts[g] = a
	bb.held[g] = false
}

func gestureName(g string) string {
	switch g {
	case gPress:
		return "按下"
	case gRelease:
		return "松开"
	case gTap:
		return "点按"
	case gDouble:
		return "双击"
	case gTriple:
		return "三击"
	case gQuad:
		return "四击"
	case gHold:
		return "长按"
	}
	return g
}

// place puts what a zone does on gesture g. A setchange slot switches the set
// when the button is released, which only a press zone can still wait for;
// anywhere else the switch is the zone's action.
func (cv *converter) place(bb *binding, at loc, g string, act zoneAct) {
	if act.tapPress > 0 {
		bb.keyPress.set(act.tapPress)
	}
	action, held := act.action, act.held
	if act.switchTo != 0 {
		sw := cv.switchAction(act.switchTo)
		if g == gPress {
			cv.put(bb, at, g, action, held)
			cv.putStructural(bb, at, gRelease, sw)
			return
		}
		if action != nil {
			cv.rep.dropped("setchange.keys", at, "同一个动作里既发键又换 set：只保留换 set")
		}
		if g == gHold {
			cv.rep.approx("setchange.hold", at, "长按里的换 set 在长按成立时就换，不等松开")
		}
		action, held = sw, false
	}
	cv.put(bb, at, g, action, held)
}

// button converts one button of a set: a button, a stick direction, a trigger
// pull or a d-pad direction.
func (cv *converter) button(at loc, btn *buttonModel, kind btnKind) *binding {
	bb := newBinding()
	bb.label = cleanText(btn.actionName, 120)
	if btn.dropped > 0 {
		cv.rep.dropped("slot.limit", at, "一个按键最多读 %d 个 slot，后面的 %d 个丢弃", maxSlots, btn.dropped)
	}
	cv.reportUnconvertible(at, btn.slots)
	parts := splitSlots(btn.slots)
	n := len(parts)
	multi := false
	if n > 1 {
		switch {
		case btn.cycleReset && btn.hasCycleInterval && btn.cycleResetInterval <= maxMultiTapMs:
			multi = true
			if n > 4 {
				cv.rep.dropped("cycle.long", at.withSlot(parts[4].pos), "循环有 %d 段，多击最多四击，第五段起丢弃", n)
				n = 4
			}
			bb.multiTapMs.set(btn.cycleResetInterval)
		default:
			cv.rep.dropped("cycle.steps", at.withSlot(parts[1].pos),
				"每按一次换下一段动作的循环（共 %d 段）没有对应，只保留第一段；多击要打开“自动复位”，复位时间不超过 %d 毫秒", n, maxMultiTapMs)
			n = 1
		}
	}
	for i := 0; i < n; i++ {
		cv.partition(bb, at, parts[i], i, multi, btn)
	}
	cv.applyKeyPress(bb, at)
	if multi && bb.acts[gTap] != nil && (bb.acts[gDouble] != nil || bb.acts[gTriple] != nil || bb.acts[gQuad] != nil) {
		cv.rep.approx("multitap.single", at, "有双击时，单击的动作要等不到第二下才触发；AntiMicroX 里单击的动作先触发")
	}
	cv.turbo(bb, at, btn, parts[0], n)
	if kind != kindModifier {
		cv.selector(bb, at, btn)
	}
	if btn.toggle && btn.setCond == "" {
		if bb.held[gPress] || bb.held[gHold] {
			bb.latch = true
		} else if bb.content() {
			cv.rep.dropped("toggle.none", at, "“切换”（再按一次才松开）只对按住的键有意义，这个按键没有按住的键")
		}
	}
	return bb
}

// tapsKeys reports whether the action taps keys (a shortcut, a sequence or a
// text; the caller knows whether it holds them): what a key press time times.
func tapsKeys(a *obj) bool {
	switch a.vals["type"] {
	case "keys", "sequence", "text":
		return true
	}
	return false
}

// applyKeyPress gives the binding the time of its keypress slot. In
// AntiMicroX the time is how long the keys stay down that a pause or a turbo
// presses, so a binding that only holds keys has nothing it applies to; in
// padmate the binding's tapPressMs is also the least time a held key of the
// binding stays down, which the report says when the binding holds keys too.
func (cv *converter) applyKeyPress(bb *binding, at loc) {
	if !bb.keyPress.ok {
		return
	}
	taps, holds := false, false
	for g, a := range bb.acts {
		switch {
		case bb.held[g]:
			holds = true
		case tapsKeys(a):
			taps = true
		}
	}
	if !taps {
		return
	}
	bb.tapPressMs.set(bb.keyPress.v)
	if holds {
		cv.rep.approx("keypress.held", at,
			"按键时间（%d 毫秒）是点按的键按下的时间；padmate 里它也让这个按键里按住的键松开前至少多按这么久", bb.keyPress.v)
	}
}

// reportUnconvertible reports the slots that padmate has nothing for, and
// that no zone logic would report.
func (cv *converter) reportUnconvertible(at loc, slots []rawSlot) {
	for _, s := range slots {
		sat := at.withSlot(s.pos)
		switch s.mode {
		case "execute":
			cv.rep.dropped("slot.execute", sat, "运行程序“%s”：padmate 不运行程序", shorten(s.text))
		case "loadprofile":
			cv.rep.dropped("slot.loadprofile", sat, "加载配置文件“%s”：padmate 不按文件切换配置", shorten(s.text))
		case "mousemovement":
			cv.rep.dropped("slot.mousemove", sat, "鼠标移动：摇杆移动鼠标是整个配置的设置，只有 set 1 里摇杆的上下左右四个方向都是鼠标移动时才导入；按键上的鼠标移动没有对应")
		case "mix":
			cv.reportUnconvertible(sat, s.mix)
		}
	}
}

// shorten cleans and cuts a path or a text of the file that a report line
// quotes.
func shorten(s string) string { return clipText(s, 60) }

// partition converts partition idx of a button's slots.
func (cv *converter) partition(bb *binding, at loc, p *partition, idx int, multi bool, btn *buttonModel) {
	first := idx == 0
	for _, z := range p.dists {
		if hasOutput(z.items) {
			cv.rep.dropped("zone.distance", at.withSlot(z.pos), "按推动距离分的动作区：按键没有推动距离，丢弃")
		}
	}
	var hz *zone
	for i, z := range p.holds {
		switch {
		case i == 0 && first:
			hz = z
		case hasOutput(z.items):
			cv.rep.dropped("zone.hold", at.withSlot(z.pos), "多出来的长按动作区丢弃：每个按键只有一个长按")
		}
	}
	// Of the release zones, the one that fires on a release is converted.
	var cands []*zone
	for _, z := range p.rels {
		if hasOutput(z.items) {
			cands = append(cands, z)
		}
	}
	var chosen *zone
	if hz != nil {
		for _, z := range cands {
			if z.ms < hz.ms {
				chosen = z
			}
		}
	} else if len(cands) > 0 {
		chosen = cands[0]
	}
	for _, z := range cands {
		if z != chosen {
			cv.rep.dropped("zone.release", at.withSlot(z.pos), "松开时间分段（从 %d 毫秒起）只能对应一段，这一段丢弃", z.ms)
		}
	}

	tapG := gTap
	if !first {
		tapG = multiGestures[idx]
	}
	asHold := first && hz == nil && chosen != nil && chosen.ms >= longBandMs
	pressOut := hasOutput(p.press.items)

	// The press zone.
	pressG := ""
	switch {
	case first && !multi:
		pressG = gPress
	case chosen == nil && !asHold:
		pressG = tapG
	case pressOut:
		cv.rep.dropped("zone.press", at, "多击里同一段既有按下的动作又有松开的动作：只保留松开的")
	}
	if pressG != "" && pressOut {
		act := cv.zone(at, p.press, pressG == gPress, btn)
		if pressG != gPress && act.chord && !bb.heldReported {
			bb.heldReported = true
			cv.rep.approx("multitap.held", at.withSlot(firstOutputPos(p.press.items)),
				"多击里按下区的键在 AntiMicroX 里按到松开；导入后是点按，键不会一直按着")
		}
		cv.place(bb, at, pressG, act)
	}

	// The hold zone.
	if hz != nil {
		if pressG == gPress && pressOut {
			cv.rep.approx("zone.press.hold", at.withSlot(hz.pos), "长按成立时，按下区的键不会像 AntiMicroX 那样先松开，会一直按到松开")
		}
		cv.place(bb, at, gHold, cv.zone(at, hz, true, btn))
		bb.holdMs.set(hz.ms)
	}

	// The release zone.
	if chosen == nil {
		return
	}
	at = at.withSlot(chosen.pos)
	if asHold {
		cv.rep.approx("zone.release.hold", at, "松开时间分段从 %d 毫秒起：按长按导入，按住够久就触发，不等松开", chosen.ms)
		cv.place(bb, at, gHold, cv.zone(at, chosen, true, btn))
		bb.holdMs.set(chosen.ms)
		return
	}
	if ms, pos, ok := bandEnd(p, hz, chosen); ok {
		cv.rep.approx("zone.release.bound", at.withSlot(pos),
			"松开时间的上限（%d 毫秒）没有对应：AntiMicroX 里按够这么久再松开什么也不触发，导入后按多久都会触发", ms)
	}
	g := tapG
	if first && !multi && hz == nil && pressG == gPress && pressOut {
		g = gRelease
	}
	if chosen.ms > shortReleaseMs {
		cv.rep.approx("zone.release.time", at, "松开前要先按住 %d 毫秒才触发；导入后按多久都触发", chosen.ms)
	}
	cv.place(bb, at, g, cv.zone(at, chosen, false, btn))
}

// turbo turns AntiMicroX's turbo into a repeat of taps (MAP-23). AntiMicroX
// presses the keys for half the interval and releases them for the other
// half, starting at once. padmate repeats a tap without waiting at the
// rate of one per period, and the binding's press and gap times give the
// period back; below 40 ms the 25 Hz that two default times allow is the
// limit.
func (cv *converter) turbo(bb *binding, at loc, btn *buttonModel, p *partition, parts int) {
	if !btn.useTurbo || btn.turbo < 10 {
		return
	}
	if parts != 1 || len(p.holds)+len(p.rels)+len(p.dists) > 0 {
		return
	}
	for _, s := range p.press.items {
		switch s.mode {
		case "pause", "setchange", "loadprofile":
			return
		}
	}
	a := bb.acts[gPress]
	if a == nil || !bb.held[gPress] {
		return
	}
	if btn.turboMode != "normal" {
		cv.rep.approx("turbo.mode", at, "连点方式“%s”（速度随推动变化）按普通连点导入", shorten(btn.turboMode))
	}
	interval := btn.turbo
	rate := min(int(math.Ceil(1000/float64(interval))), maxRepeatHz)
	a.set("mode", "tap")
	a.set("repeat", newObj().set("style", "tap").set("delayMs", 0).set("rateHz", rate))
	bb.held[gPress] = false
	if interval >= 40 {
		press := interval / 2
		gap := interval - press
		if press > maxKeyDownMs || gap > maxKeyDownMs {
			// The key stays down for the press time, also after the button is
			// released, so the press time is cut like a key press time is.
			press, gap = min(press, maxKeyDownMs), min(gap, maxKeyDownMs)
			cv.rep.approx("turbo.long", at, "连点间隔 %d 毫秒太长：按下和松开各最多 %d 毫秒，间隔改成 %d 毫秒", interval, maxKeyDownMs, press+gap)
		}
		bb.tapPressMs.set(press)
		bb.tapGapMs.set(gap)
		if cv.doc.keyPressTime > press {
			cv.rep.approx("turbo.presstime", at, "整个配置的按键时间（%d 毫秒）比连点的按下时间长，连点会比 %d 毫秒慢", cv.doc.keyPressTime, interval)
		}
	} else {
		cv.rep.approx("turbo.fast", at, "连点间隔 %d 毫秒，padmate 同一个键最快每 40 毫秒一次（25 次/秒）", interval)
	}
}

// selector adds the change of set that the button makes (section 6 of the
// format notes).
func (cv *converter) selector(bb *binding, at loc, btn *buttonModel) {
	t := btn.setSelect
	if t == 0 || btn.setCond == "" {
		return
	}
	if t == at.set {
		cv.rep.info("select.self", at, "换 set 的目标是它自己所在的 set，忽略")
		return
	}
	switch btn.setCond {
	case "one-way", "two-way":
		// The key's own slots fire at the press; the set changes on release.
		cv.putStructural(bb, at, gRelease, cv.switchAction(t))
	case "while-held":
		if t == 1 {
			// A way back to set 1 while held: in padmate the layer ends when
			// the button is released, whatever the layer says.
			if !cv.heldTarget[at.set] {
				cv.rep.dropped("select.held.base", at, "按住时回到 set 1 没有对应：层只能叠在 base 上")
			}
			cv.clearOwn(bb, at, false)
			return
		}
		cv.clearOwn(bb, at, true)
		bb.acts[gPress] = cv.holdAction(t)
		bb.held[gPress] = false
	}
}

// clearOwn removes what the button does by itself, which AntiMicroX does not
// do while the button holds a set.
func (cv *converter) clearOwn(bb *binding, at loc, say bool) {
	if len(bb.acts) > 0 && say {
		cv.rep.info("select.held.own", at, "按住换 set 的按键自己的动作不会触发，没有导入")
	}
	bb.acts = map[string]*obj{}
	bb.held = map[string]bool{}
	bb.holdMs, bb.multiTapMs, bb.tapPressMs, bb.tapGapMs, bb.keyPress = optInt{}, optInt{}, optInt{}, optInt{}, optInt{}
	bb.latch = false
}

// firstOutputPos returns the position of the first slot that makes something
// happen, 0 when there is none.
func firstOutputPos(items []rawSlot) int {
	for _, s := range items {
		if hasOutput([]rawSlot{s}) {
			return s.pos
		}
	}
	return 0
}

// bandEnd returns where AntiMicroX stops firing the release zone chosen: the
// start of the empty zone that follows it, or of an empty hold zone that comes
// first. Its position is the empty zone's slot. ok is false when the zone
// fires however long the button was held, or when a hold gesture takes over at
// that time, which padmate has.
func bandEnd(p *partition, hz, chosen *zone) (ms, pos int, ok bool) {
	for i, z := range p.rels {
		if z == chosen && i+1 < len(p.rels) && !hasOutput(p.rels[i+1].items) {
			ms, pos, ok = p.rels[i+1].ms, p.rels[i+1].pos, true
		}
	}
	if hz != nil && !hasOutput(hz.items) && hz.ms > chosen.ms && (!ok || hz.ms < ms) {
		ms, pos, ok = hz.ms, hz.pos, true
	}
	if ok && hz != nil && hasOutput(hz.items) && hz.ms <= ms {
		return 0, 0, false
	}
	return ms, pos, ok
}
