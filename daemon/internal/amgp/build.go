package amgp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// controlRanks numbers the controls in the order a layer lists them, so that
// the profile reads the same every time.
func controlRanks() map[string]int {
	order := []string{
		"south", "east", "west", "north", "leftshoulder", "rightshoulder", "lt", "rt",
		"leftstick", "rightstick", "back", "start", "guide", "touchpad",
		"misc1", "paddle1", "paddle2", "paddle3", "paddle4",
		"dpad.up", "dpad.down", "dpad.left", "dpad.right",
		"dpad.upleft", "dpad.upright", "dpad.downleft", "dpad.downright",
	}
	for _, s := range []string{"ls", "rs"} {
		for _, d := range []string{"up", "upright", "right", "downright", "down", "downleft", "left", "upleft", "outer", "inner"} {
			order = append(order, s+"."+d)
		}
	}
	rank := make(map[string]int, len(order))
	for i, c := range order {
		rank[c] = i
	}
	return rank
}

// fillControls lists the controls that a hold of a set blocks when the set
// does not bind them: every control that can fire with the stick and d-pad
// modes of the profile and every ring that is bound, except back and start. One
// of them is the function key, which cannot be blocked, and which one is
// chosen in padmate's settings, so neither is touched.
func (cv *converter) fillControls() []string {
	list := []string{
		"south", "east", "west", "north", "leftshoulder", "rightshoulder", "lt", "rt",
		"leftstick", "rightstick", "guide", "touchpad", "misc1", "paddle1", "paddle2", "paddle3", "paddle4",
		"dpad.up", "dpad.down", "dpad.left", "dpad.right",
	}
	if cv.dpadMode == "eightWay" {
		list = append(list, "dpad.upleft", "dpad.upright", "dpad.downleft", "dpad.downright")
	}
	for w, name := range [2]string{"ls", "rs"} {
		g := cv.stick[w]
		var dirs []string
		switch g.mode {
		case "standard", "fourWay":
			dirs = []string{"up", "right", "down", "left"}
		case "eightWay":
			dirs = []string{"up", "upright", "right", "downright", "down", "downleft", "left", "upleft"}
		case "fourWayDiagonal":
			dirs = []string{"upright", "downright", "downleft", "upleft"}
		}
		for _, d := range dirs {
			list = append(list, name+"."+d)
		}
		// The rings fire in every mode. One that no set binds has nothing to
		// hide.
		for _, ring := range []string{"inner", "outer"} {
			if cv.bound(name + "." + ring) {
				list = append(list, name+"."+ring)
			}
		}
	}
	return list
}

// bound reports whether any set of the file binds the control.
func (cv *converter) bound(control string) bool {
	for _, binds := range cv.converted {
		if binds[control] != nil {
			return true
		}
	}
	return false
}

// layerObj returns the bindings of set t as a layer. With fill, the controls
// the set does not bind are blocked, so that the layer replaces the set below
// it entirely, as an AntiMicroX set does.
func (cv *converter) layerObj(binds map[string]*binding, fill bool, at loc) *obj {
	merged := map[string]*binding{}
	for c, b := range binds {
		merged[c] = b
	}
	blocked := 0
	if fill {
		for _, c := range cv.fillControls() {
			if merged[c] == nil {
				merged[c] = &binding{block: true, acts: map[string]*obj{}, held: map[string]bool{}}
				blocked++
			}
		}
	}
	if blocked > 0 {
		cv.rep.info("layer.fill", at, "按住它的层里没有绑定的 %d 个控件设成了“屏蔽”：AntiMicroX 的 set 是整套替换，没绑的就什么也不做", blocked)
	}
	controls := make([]string, 0, len(merged))
	for c := range merged {
		controls = append(controls, c)
	}
	sort.Slice(controls, func(i, j int) bool { return cv.rank[controls[i]] < cv.rank[controls[j]] })
	layer := newObj()
	for _, c := range controls {
		layer.set(c, merged[c].toObj())
	}
	return layer
}

// build makes the profile.json of the document.
func (cv *converter) build() *obj {
	doc := cv.doc
	p := newObj()
	p.set("schemaVersion", profile.CurrentSchema)
	p.set("name", cv.profileName())
	p.set("defaultLayer", "base")
	if len(doc.names) > 0 {
		controls := make([]string, 0, len(doc.names))
		for c := range doc.names {
			controls = append(controls, c)
		}
		sort.Slice(controls, func(i, j int) bool { return cv.rank[controls[i]] < cv.rank[controls[j]] })
		names := newObj()
		for _, c := range controls {
			names.set(c, doc.names[c])
		}
		p.set("controlNames", names)
	}
	if doc.keyPressTime > 0 {
		p.set("timing", newObj().set("tapPressMs", min(doc.keyPressTime, maxKeyDownMs)))
	}

	analog := newObj()
	analog.set("ls", cv.stick[0].obj())
	analog.set("rs", cv.stick[1].obj())
	if cv.dpadMode != "buttons" {
		analog.set("dpad", newObj().set("mode", cv.dpadMode))
	}
	analog.set("lt", newObj().set("press", cv.trig[0].press).set("release", cv.trig[0].release))
	analog.set("rt", newObj().set("press", cv.trig[1].press).set("release", cv.trig[1].release))
	p.set("analog", analog)

	// A set that only holds a function key is that key's set, not a layer.
	functionOnly := map[int]bool{}
	for _, t := range cv.fn {
		functionOnly[t] = true
	}
	layers := newObj()
	// The sets that become layers: set 1, every set of the file and every
	// set an action points at.
	emitted := 0
	for t := 1; t <= setCount; t++ {
		binds, exists := cv.converted[t]
		if t != 1 && !exists && cv.refs[t] == 0 {
			continue
		}
		if t != 1 && functionOnly[t] && cv.refs[t] == 0 {
			continue
		}
		at := loc{set: t}
		layers.set(cv.layerName(t), cv.layerObj(binds, cv.heldTarget[t] || cv.held[t], at))
		if t != 1 {
			emitted++
			cv.rep.info("layer.set", at, "导入为层“%s”", cv.layerName(t))
		}
	}
	p.set("layers", layers)

	if len(cv.fn) > 0 {
		p.set("functionSet", cv.functionSet())
	}
	if cv.startBound {
		cv.rep.info("function.default", loc{control: "start"}, "Menu（Start）是 padmate 默认的功能键，默认设置下它上面的绑定不生效；要让它们生效，到设置里把功能键换成 View 或 Xbox 键")
	}
	if emitted > 0 {
		cv.rep.info("layers.ui", loc{}, "这个配置除了 base 还有 %d 个别的层；映射页只编辑 base，其他层只能在文件里改，界面保存时原样保留", emitted)
	}
	for w, name := range [2]string{"ls", "rs"} {
		if cv.stick[w].mouse && (emitted > 0 || len(cv.fn) > 0) {
			cv.rep.info("stick.mouse.layers", loc{control: name},
				"在移动鼠标：这是整个配置的设置，换到其他 set 或层时指针仍然会动")
		}
	}
	return p
}

// verify checks that the profile is one that padmate reads. It always is;
// a failure is a bug of the importer, and never reaches the library.
func verify(text []byte) error {
	_, ds := profile.Parse(text)
	if err := ds.Err(); err != nil {
		return &Error{Reason: ReasonInternal, Message: fmt.Sprintf("导入器做出了无效的配置（这是 padmate 的错误）：%s", firstLine(err.Error()))}
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// functionSet returns the function set (ADR-0075): padmate has one function
// key, chosen in its settings (Menu by default), so the sets that View and Menu
// hold in AntiMicroX become one. Menu's comes first, and View's fills the
// controls Menu's does not bind. Like a held layer, the controls the set does
// not bind are blocked. The report says so once, with where the function key is
// chosen.
func (cv *converter) functionSet() *obj {
	merged := map[string]*binding{}
	start, hasStart := cv.fn["start"]
	back, hasBack := cv.fn["back"]
	at := loc{set: start}
	if hasStart {
		for c, b := range cv.converted[start] {
			merged[c] = b
		}
	}
	both := hasStart && hasBack && back != start
	if hasBack && (!hasStart || both) {
		if !hasStart {
			at = loc{set: back}
		}
		for c, b := range cv.converted[back] {
			if merged[c] == nil {
				merged[c] = b
			}
		}
	}
	const where = "padmate 只有一个功能键，在设置里选，默认是 Menu（右边的小键）"
	switch {
	case both:
		cv.rep.info("function.set", at, "按住功能键时用 set %d（Menu 的）的按键，再加 set %d（View 的）里 Menu 的没有绑定的控件。%s", start, back, where)
	case hasStart:
		cv.rep.info("function.set", at, "按住功能键时用 set %d（Menu 的）的按键。%s", start, where)
	default:
		cv.rep.info("function.set", at, "按住功能键时用 set %d（View 的）的按键。%s", back, where)
	}
	return cv.layerObj(merged, true, at)
}
