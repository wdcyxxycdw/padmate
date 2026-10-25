package amgp

import (
	"fmt"
	"strings"
)

const (
	// mouseSpeedFactor turns AntiMicroX's mouse speed into pixels per second
	// at full deflection: its speed is multiplied by 20 (ADR-0072).
	mouseSpeedFactor = 20
	// triggerGap is how far below the press threshold the release threshold
	// is.
	triggerGap = 0.05
	// minTrigger is the lowest press threshold: a trigger at rest reads 0,
	// which a threshold of 0 would count as pressed.
	minTrigger = 0.01
)

// trigger holds the thresholds of a trigger.
type triggerGlobal struct {
	press, release float64
}

// globals works out the settings that padmate keeps for the whole profile
// and AntiMicroX keeps for each set. The first set decides; the others that
// differ are reported.
func (cv *converter) globals() {
	base := cv.doc.sets[1]
	baseSet := base
	if baseSet == nil {
		baseSet = newSetModel(1)
	}
	for w, name := range [2]string{"ls", "rs"} {
		st := baseSet.sticks[w]
		at := loc{set: 1, control: name}
		g := stickGlobal{mode: st.mode, dead: st.dead, max: st.max, diagonal: st.diagonal, delay: st.delay}
		if r4(float64(g.dead)/32767) >= r4(float64(g.max)/32767) {
			cv.rep.dropped("stick.zone", at, "死区（%d）不小于外圈（%d），改用 AntiMicroX 的默认值 %d 和 %d", g.dead, g.max, defStickDead, defStickMax)
			g.dead, g.max = defStickDead, defStickMax
		}
		if base != nil {
			if speed, curve, ok := cv.detectMouse(at, st); ok {
				g.mouse, g.mouseSpeed, g.curve = true, speed, curve
				g.mode = "mouse"
			}
		}
		// The modifier zone: the first set that binds it decides the ring.
		for _, i := range cv.doc.setIndexes() {
			s := cv.doc.sets[i].sticks[w]
			if s.modifierBtn == nil || !hasOutput(s.modifierBtn.slots) {
				continue
			}
			ring := "outer"
			if s.modInverted {
				ring = "inner"
			}
			span := float64(s.max - s.dead)
			threshold := 0.0
			if span > 0 {
				threshold = clampF(float64(s.modifier-s.dead)/span, 0, 1)
			}
			if g.ring == "" {
				g.ring, g.outer = ring, r4(threshold)
				cv.rep.approx("stick.modifier", loc{set: i, control: name + "." + ring},
					"修饰区按摇杆的%s导入，从推动深度 %.2f 起；padmate 的圈带滞回（离开时再低 0.05），和 AntiMicroX 的不完全一样",
					map[string]string{"outer": "外圈", "inner": "内圈"}[ring], g.outer)
			} else if r4(threshold) != g.outer {
				cv.rep.approx("stick.modifier.differs", loc{set: i, control: name + "." + ring},
					"这一套的修饰区范围与前面一套不同；padmate 的内外圈范围是整个配置的，用 %.2f", g.outer)
			}
		}
		cv.stick[w] = g
		// The other sets.
		for _, i := range cv.doc.setIndexes() {
			if i == 1 {
				continue
			}
			s := cv.doc.sets[i].sticks[w]
			if !s.present || !s.hasSettings {
				continue
			}
			var diff []string
			if s.dead != st.dead {
				diff = append(diff, fmt.Sprintf("死区 %d（set 1 是 %d）", s.dead, st.dead))
			}
			if s.max != st.max {
				diff = append(diff, fmt.Sprintf("外圈 %d（set 1 是 %d）", s.max, st.max))
			}
			if s.mode != st.mode {
				diff = append(diff, fmt.Sprintf("模式“%s”（set 1 是“%s”）", modeLabel(s.mode), modeLabel(st.mode)))
			}
			if s.diagonal != st.diagonal {
				diff = append(diff, fmt.Sprintf("对角宽度 %d°（set 1 是 %d°）", s.diagonal, st.diagonal))
			}
			if s.delay != st.delay {
				diff = append(diff, fmt.Sprintf("方向延迟 %d 毫秒（set 1 是 %d）", s.delay, st.delay))
			}
			if len(diff) > 0 {
				cv.rep.approx("stick.differs", loc{set: i, control: name},
					"这一套的摇杆设置与 set 1 的不同：%s；padmate 的摇杆设置是整个配置的，用 set 1 的", strings.Join(diff, "、"))
			}
		}
	}

	// The d-pad.
	d := baseSet.dpad
	switch d.mode {
	case "eight-way":
		cv.dpadMode = "eightWay"
	case "four-way":
		cv.dpadMode = "fourWay"
	case "diagonal":
		cv.dpadMode = "eightWay"
		cv.rep.approx("dpad.diagonal", loc{set: 1, control: "dpad"},
			"十字键的“只有对角”模式没有对应，按八向模式导入；正方向上的绑定在 AntiMicroX 里不会触发，在这里会")
	default:
		cv.dpadMode = "buttons"
	}
	for _, i := range cv.doc.setIndexes() {
		if i != 1 && cv.doc.sets[i].dpad.present && cv.doc.sets[i].dpad.mode != d.mode {
			cv.rep.approx("dpad.differs", loc{set: i, control: "dpad"}, "这一套的十字键模式与 set 1 的不同；padmate 的十字键模式是整个配置的，用 set 1 的")
		}
	}

	// The triggers.
	for w, name := range [2]string{"lt", "rt"} {
		dead := baseSet.trig[w].dead
		for _, i := range cv.doc.setIndexes() {
			if t := cv.doc.sets[i].trig[w]; i != 1 && t.present && t.dead != dead {
				cv.rep.approx("trigger.differs", loc{set: i, control: name},
					"这一套的扳机死区（%d）与 set 1 的（%d）不同；padmate 的扳机阈值是整个配置的，用 set 1 的", t.dead, dead)
			}
		}
		press := float64(dead) / 32767
		if press < minTrigger {
			cv.rep.approx("trigger.zone", loc{set: 1, control: name}, "扳机死区 %d 太小，按下阈值用 %.2f，免得扳机不动就算按下", dead, minTrigger)
			press = minTrigger
		}
		press = r4(min(press, 1))
		cv.trig[w] = triggerGlobal{press: press, release: r4(max(press-triggerGap, 0))}
	}
}

func clampF(v, lo, hi float64) float64 {
	return min(max(v, lo), hi)
}

// detectMouse finds out whether the stick moves the pointer: all four
// directions up, down, left and right have a mousemovement slot that moves
// that way. It returns the speed at full deflection and the curve.
func (cv *converter) detectMouse(at loc, st *stickModel) (speed int, curve string, ok bool) {
	// The direction buttons and the mousemovement code each moves the pointer
	// in: up 1, down 2, left 3, right 4.
	dirs := [4]struct {
		button, code int
		vertical     bool
	}{{1, 1, true}, {5, 2, true}, {7, 3, false}, {3, 4, false}}
	speeds := make([]int, 0, 4)
	curves := make([]string, 0, 4)
	for _, d := range dirs {
		b := st.dirs[d.button]
		if b == nil || !movesPointer(b.slots, d.code) {
			return 0, "", false
		}
		if b.springMode {
			cv.rep.dropped("mouse.spring", at, "弹簧鼠标模式没有对应，这根摇杆不导入为鼠标摇杆")
			return 0, "", false
		}
		s := b.mouseSpeedX
		if d.vertical {
			s = b.mouseSpeedY
		}
		speeds = append(speeds, s)
		curves = append(curves, b.accel)
	}
	top := 0
	same := true
	for _, s := range speeds {
		top = max(top, s)
		same = same && s == speeds[0]
	}
	speed = clamp(top*mouseSpeedFactor, 50, 10000)
	if !same {
		cv.rep.approx("mouse.speed", at, "四个方向的鼠标速度不一样（%s），padmate 一根摇杆只有一个速度，用最大的 %d", joinInts(speeds), speed)
	}
	if top*mouseSpeedFactor != speed {
		cv.rep.approx("mouse.speed.range", at, "鼠标速度 %d 超出 padmate 的范围，改成 %d", top*mouseSpeedFactor, speed)
	}
	curve, exact := mouseCurve(curves[0])
	for _, c := range curves[1:] {
		if c != curves[0] {
			cv.rep.approx("mouse.curve.differs", at, "四个方向的加速曲线不一样，用“上”方向的")
			break
		}
	}
	if !exact {
		cv.rep.approx("mouse.curve", at, "鼠标加速曲线“%s”按最接近的%s导入", shorten(curves[0]), map[string]string{"quadratic": "二次曲线", "cubic": "三次曲线"}[curve])
	}
	// The pointer moves differently when a direction has a setting padmate has
	// nothing for.
	for _, d := range dirs {
		for _, ms := range st.dirs[d.button].mouseExtras {
			cv.rep.dropped("mouse.setting", at, "鼠标设置“%s”（%s）没有对应，没有导入；指针的手感会和 AntiMicroX 里的不同", ms.element, ms.label)
		}
	}
	return speed, curve, true
}

func movesPointer(slots []rawSlot, code int) bool {
	for _, s := range slots {
		if s.mode == "mousemovement" && s.code == code {
			return true
		}
	}
	return false
}

func joinInts(v []int) string {
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprint(n)
	}
	return strings.Join(parts, "、")
}

// mouseCurve maps AntiMicroX's mouse acceleration to the curve of padmate that
// is closest to it, and whether it is the same curve. The default,
// precision, is a linear stretch that is slow at first and then faster; the
// quadratic curve stays within 0.06 of it.
func mouseCurve(name string) (string, bool) {
	switch name {
	case "linear":
		return "linear", true
	case "quadratic":
		return "quadratic", true
	case "cubic":
		return "cubic", true
	case "easing-cubic":
		return "cubic", false
	}
	return "quadratic", false
}

// stickObj is the settings of a stick as padmate's analog section writes
// them. The dead zone and the outer edge are always written, since
// AntiMicroX's defaults are not padmate's, and a direction counts as soon as
// the stick leaves the dead zone, which is what directionThreshold 0 says.
func (g stickGlobal) obj() *obj {
	o := newObj()
	o.set("mode", g.mode)
	o.set("deadzone", r4(float64(g.dead)/32767))
	o.set("saturation", r4(float64(g.max)/32767))
	o.set("directionThreshold", 0)
	if (g.mode == "standard" || g.mode == "eightWay") && g.diagonal != defDiagonal {
		o.set("diagonalWidth", g.diagonal)
	}
	if g.delay > 0 {
		o.set("directionDelayMs", g.delay)
	}
	if g.ring != "" {
		o.set("outerThreshold", g.outer)
	}
	if g.mouse {
		o.set("mouseSpeed", g.mouseSpeed)
		o.set("curve", g.curve)
	}
	return o
}
