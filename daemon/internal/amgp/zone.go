package amgp

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// A button's slot list is cut into zones at the slots that change when the
// keys fire (section 5 of the format notes):
//
//   - the press zone is everything before the first hold, release,
//     distance or cycle slot: it fires when the button goes down;
//   - a hold slot opens a zone that fires once the button has been held for
//     its time;
//   - a release slot opens a zone that fires when the button is released
//     after the cumulative time of the release slots so far;
//   - a distance slot opens a zone for a depth of an axis, which a button
//     does not have;
//   - a cycle slot ends a partition: each press runs the next partition and
//     the last is followed by the first.

type zone struct {
	items []rawSlot
	// pos is the position of the slot that opened the zone, 0 for the press
	// zone.
	pos int
	// ms is a hold zone's time, or a release zone's cumulative time.
	ms int
}

type partition struct {
	press *zone
	holds []*zone
	rels  []*zone
	dists []*zone
	// pos is the position of the cycle slot that opened the partition.
	pos int
}

func newPartition(pos int) *partition {
	return &partition{press: &zone{}, pos: pos}
}

// maxCumulative keeps the sum of release times from overflowing.
const maxCumulative = 1 << 30

func splitSlots(slots []rawSlot) []*partition {
	parts := []*partition{newPartition(0)}
	cur := parts[0].press
	cum := 0
	for _, s := range slots {
		p := parts[len(parts)-1]
		switch s.mode {
		case "cycle":
			np := newPartition(s.pos)
			parts = append(parts, np)
			cur, cum = np.press, 0
		case "hold":
			cur = &zone{pos: s.pos, ms: s.code}
			p.holds = append(p.holds, cur)
		case "release":
			cum = min(cum+s.code, maxCumulative)
			cur = &zone{pos: s.pos, ms: cum}
			p.rels = append(p.rels, cur)
		case "distance":
			cur = &zone{pos: s.pos, ms: s.code}
			p.dists = append(p.dists, cur)
		default:
			cur.items = append(cur.items, s)
		}
	}
	return parts
}

// hasOutput reports whether the slots make something happen that padmate can
// do: a key, a mouse button, a text, a set change or a mouse speed change.
func hasOutput(items []rawSlot) bool {
	for _, s := range items {
		switch s.mode {
		case "keyboard", "mousebutton", "textentry", "setchange", "mousespeedmod":
			return true
		case "mix":
			if hasOutput(s.mix) {
				return true
			}
		}
	}
	return false
}

// expand replaces mix slots by their slots, which AntiMicroX presses
// together like the keys of a chord.
func expand(items []rawSlot) []rawSlot {
	var out []rawSlot
	for _, s := range items {
		if s.mode != "mix" {
			out = append(out, s)
			continue
		}
		for _, m := range s.mix {
			m.pos = s.pos
			out = append(out, m)
		}
	}
	return out
}

// zoneAct is what a zone does in padmate.
type zoneAct struct {
	// action is the one action of the zone: keys, a sequence, a text or a
	// mouse speed change; nil when the zone does none of these.
	action *obj
	// held is true when action holds keys until the button is released.
	held bool
	// switchTo is the set a setchange slot switches to, 1-based, or 0.
	switchTo int
	// tapPress is the time of a keypress slot, 0 when there is none.
	tapPress int
	// chord is true when action is a shortcut made of the keys of the zone, which
	// AntiMicroX holds while the button is down.
	chord bool
}

type seqStep struct {
	keys string
	wait int
	text string
}

// modFamilies orders the modifier families as keys.Chord formats them.
var modFamilies = [4]string{"Ctrl", "Shift", "Alt", "Win"}

const (
	sideGeneric = 1 << iota
	sideLeft
	sideRight
)

// chordBuilder collects the keys of a zone that AntiMicroX presses together.
type chordBuilder struct {
	mods [4]uint8
	main string
	// wheel and horizontal say that main is a wheel notch.
	wheel, horizontal bool
	has               bool
}

// addMod adds a modifier by its KeySpec name. A generic modifier next to a
// sided one of the same family is dropped, since KeySpec refuses both.
func (c *chordBuilder) addMod(name string) {
	c.has = true
	for f, family := range modFamilies {
		var side uint8
		switch name {
		case family:
			side = sideGeneric
		case "L" + family:
			side = sideLeft
		case "R" + family:
			side = sideRight
		default:
			continue
		}
		switch {
		case side == sideGeneric && c.mods[f]&^sideGeneric != 0:
		case side == sideGeneric:
			c.mods[f] |= side
		default:
			c.mods[f] = c.mods[f]&^sideGeneric | side
		}
		return
	}
}

// addMain sets the key that is not a modifier. It reports false when the
// chord already has one.
func (c *chordBuilder) addMain(name string, wheel, horizontal bool) bool {
	c.has = true
	if c.main != "" {
		return false
	}
	c.main, c.wheel, c.horizontal = name, wheel, horizontal
	return true
}

// spec formats the chord as a KeySpec, in the order keys.Chord.String uses.
func (c *chordBuilder) spec() string {
	var parts []string
	for f, family := range modFamilies {
		if c.mods[f]&sideGeneric != 0 {
			parts = append(parts, family)
		}
		if c.mods[f]&sideLeft != 0 {
			parts = append(parts, "L"+family)
		}
		if c.mods[f]&sideRight != 0 {
			parts = append(parts, "R"+family)
		}
	}
	if c.main != "" {
		parts = append(parts, c.main)
	}
	return strings.Join(parts, "+")
}

// normalized returns the KeySpec as keys.Parse formats it, or "" if padmate
// would not read it.
func normalized(spec string) string {
	chord, err := keys.Parse(spec)
	if err != nil {
		return ""
	}
	return chord.String()
}

// zone converts the slots of a zone. holds is true for the gestures that
// hold what they do (press and hold), false for those that happen once.
func (cv *converter) zone(at loc, z *zone, holds bool, btn *buttonModel) zoneAct {
	var act zoneAct
	if z == nil || len(z.items) == 0 {
		return act
	}
	var (
		cb        chordBuilder
		steps     []seqStep
		seq       bool
		overflow  bool
		factor    float64
		hasFactor bool
	)
	addStep := func(s seqStep) {
		if len(steps) >= keys.MaxSteps {
			overflow = true
			return
		}
		steps = append(steps, s)
	}
	flush := func() {
		if cb.has {
			if spec := normalized(cb.spec()); spec != "" {
				addStep(seqStep{keys: spec})
			}
			cb = chordBuilder{}
		}
	}
	for _, s := range expand(z.items) {
		sat := at.withSlot(s.pos)
		switch s.mode {
		case "keyboard":
			k := qtKeyFor(s.code)
			if k.drop != "" {
				cv.rep.dropped("key.unmapped", sat, "%s", k.drop)
				continue
			}
			if k.approx != "" {
				cv.rep.approx("key.approximate", sat, "%s", k.approx)
			}
			for _, m := range k.mods {
				cb.addMod(m)
			}
			if k.modifier {
				cb.addMod(k.name)
			} else if !cb.addMain(k.name, false, false) {
				cv.rep.dropped("chord.extra", sat, "同时按住的键里只能有一个不是修饰键：保留了 %s，丢弃了 %s", cb.main, k.name)
			}
		case "mousebutton":
			wheel, horizontal := isWheel(s.code)
			name := mouseButtonNames[s.code]
			if !cb.addMain(name, wheel, horizontal) {
				cv.rep.dropped("chord.extra", sat, "同时按住的键里只能有一个不是修饰键：保留了 %s，丢弃了 %s", cb.main, name)
			}
		case "pause":
			flush()
			seq = true
			if s.code > 0 {
				addWait(&steps, s.code, &overflow)
			}
		case "delay":
			cv.rep.approx("slot.delay", sat, "延迟 %d 毫秒期间按住的键本来不放开；导入时这些键按在一起，延迟没有保留", s.code)
		case "textentry":
			text, note := fitText(s.text)
			if note != "" {
				cv.rep.approx("text.fitted", sat, "%s", note)
			}
			if text == "" {
				continue
			}
			flush()
			seq = true
			addStep(seqStep{text: text})
		case "keypress":
			act.tapPress = clamp(s.code, 1, maxKeyDownMs)
		case "mousespeedmod":
			switch {
			case s.code <= 0 || s.code > 100:
				cv.rep.dropped("mouse.speedmod", sat, "鼠标速度改成 %d%%：padmate 的精确模式只能把鼠标变慢，范围 5%% 至 100%%", s.code)
			default:
				if s.code < 5 {
					cv.rep.approx("mouse.speedmod", sat, "鼠标速度改成 %d%%，padmate 最慢是 5%%", s.code)
				}
				factor, hasFactor = float64(max(s.code, 5))/100, true
			}
		case "setchange":
			if act.switchTo != 0 {
				cv.rep.dropped("setchange.extra", sat, "同一个动作里有不止一个换 set，只用第一个")
				continue
			}
			if s.code+1 == at.set {
				continue
			}
			act.switchTo = s.code + 1
		}
	}
	if seq {
		flush()
		for len(steps) > 0 && steps[len(steps)-1].wait > 0 {
			steps = steps[:len(steps)-1]
		}
	}
	if seq && overflow {
		if len(steps) > 0 {
			cv.rep.dropped("sequence.long", at, "序列最多 %d 步，后面的丢弃", keys.MaxSteps)
		} else {
			// Nothing is left but waits, which are cut off at the end.
			cv.rep.dropped("sequence.long", at, "开头的等待太长（序列最多 %d 步，一步最多等 %d 毫秒），后面的键和文字都丢弃了", keys.MaxSteps, keys.MaxWait.Milliseconds())
		}
	}
	switch {
	case seq && len(steps) > 0:
		if holds && endsWithKeys(steps) {
			cv.rep.approx("sequence.tap", at, "序列里的键是点按，不是按住到松开")
		}
		act.action = sequenceAction(steps, holds)
	case !seq && cb.has:
		spec := normalized(cb.spec())
		if spec == "" {
			cv.rep.dropped("key.invalid", at, "这组键 padmate 不认识，丢弃")
			break
		}
		act.action = newObj().set("type", "keys").set("keys", spec)
		act.chord = true
		switch {
		case cb.wheel && holds:
			speed := btn.wheelRate(cb.horizontal)
			rate := clamp(speed, 1, maxRepeatHz)
			if rate != speed {
				cv.rep.approx("wheel.rate", at, "滚轮每秒 %d 格，padmate 一个键最快每秒 %d 次，改成 %d", speed, maxRepeatHz, rate)
			}
			act.action.set("mode", "tap")
			act.action.set("repeat", newObj().set("style", "tap").set("delayMs", 0).set("rateHz", rate))
		case holds:
			act.held = true
		}
	}
	if hasFactor {
		switch {
		case act.action != nil:
			cv.rep.dropped("mouse.speedmod", at, "同一个动作里已经有按键，鼠标速度的改变丢弃")
		case !holds:
			cv.rep.dropped("mouse.speedmod", at, "鼠标速度的改变只在按住期间有效，这里的动作只发生一次")
		default:
			act.action = newObj().set("type", "mouse").set("op", "precision").set("factor", factor)
		}
	}
	return act
}

// maxRepeatHz is the fastest a key repeats at the default press and gap
// times (1000/(20+20)).
const maxRepeatHz = 25

// wheelRate is the rate at which AntiMicroX repeats a wheel notch while its
// button is down.
func (b *buttonModel) wheelRate(horizontal bool) int {
	if horizontal {
		return b.wheelX
	}
	return b.wheelY
}

// endsWithKeys reports whether the last step is a key tap.
func endsWithKeys(steps []seqStep) bool {
	return len(steps) > 0 && steps[len(steps)-1].keys != ""
}

// addWait adds a wait, in several steps if it is longer than one allows.
func addWait(steps *[]seqStep, ms int, overflow *bool) {
	for ms > 0 {
		if len(*steps) >= keys.MaxSteps {
			*overflow = true
			return
		}
		w := min(ms, int(keys.MaxWait.Milliseconds()))
		*steps = append(*steps, seqStep{wait: w})
		ms -= w
	}
}

// sequenceAction makes the action for steps: a single key tap or a single
// text are the simpler actions.
func sequenceAction(steps []seqStep, holds bool) *obj {
	if len(steps) == 1 {
		switch {
		case steps[0].keys != "":
			a := newObj().set("type", "keys").set("keys", steps[0].keys)
			if holds {
				a.set("mode", "tap")
			}
			return a
		case steps[0].text != "":
			return newObj().set("type", "text").set("text", steps[0].text)
		}
	}
	list := make([]*obj, len(steps))
	for i, s := range steps {
		o := newObj()
		switch {
		case s.keys != "":
			o.set("keys", s.keys)
		case s.text != "":
			o.set("text", s.text)
		default:
			o.set("wait", s.wait)
		}
		list[i] = o
	}
	return newObj().set("type", "sequence").set("steps", list)
}

// fitText makes text AntiMicroX types a text padmate can type: a newline
// and a tab stay, other control characters go, and it is cut at 256 UTF-16
// units. The note says what changed.
func fitText(s string) (string, string) {
	s2 := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)
	var b strings.Builder
	units := 0
	removed, cut := false, false
	for _, r := range s2 {
		if r != '\n' && r != '\t' && (unicode.IsControl(r) || r == unicode.ReplacementChar) {
			removed = true
			continue
		}
		n := utf16.RuneLen(r)
		if units+n > keys.MaxTextUnits {
			cut = true
			break
		}
		units += n
		b.WriteRune(r)
	}
	var notes []string
	if removed {
		notes = append(notes, "去掉了不能输入的控制字符")
	}
	if cut {
		notes = append(notes, fmt.Sprintf("文字超过 %d 个字符，后面的丢弃", keys.MaxTextUnits))
	}
	if len(notes) == 0 {
		return b.String(), ""
	}
	return b.String(), "输入文字：" + strings.Join(notes, "，")
}
