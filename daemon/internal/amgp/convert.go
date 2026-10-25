package amgp

import (
	"fmt"
	"sort"
	"strings"
)

// Options tell Import about the file it is given.
type Options struct {
	// Name is the name of the profile. When it is empty the file's own
	// profilename is used, then FileName.
	Name string
	// FileName is the name of the file as the user knows it (a path is
	// fine), for a profile name when the file has none.
	FileName string
}

// stickGlobal is what padmate keeps for a whole profile of a stick:
// AntiMicroX keeps it for every set, padmate once, and the first set decides.
type stickGlobal struct {
	mode     string
	dead     int
	max      int
	diagonal int
	delay    int
	mouse    bool
	// mouseSpeed and curve belong to a stick that moves the pointer.
	mouseSpeed int
	curve      string
	// outer is the outer ring's threshold when a set has a modifier zone
	// button; ring is its control, "outer" or "inner".
	outer float64
	ring  string
}

// converter makes the padmate profile from the document.
type converter struct {
	doc  *document
	rep  *reporter
	opts Options

	// names are the layer names of the sets 2 to 8 that exist or are
	// pointed at.
	names map[int]string
	// refs counts the actions that point at a set, and held says that one
	// of them holds the set as a layer.
	refs [setCount + 1]int
	held [setCount + 1]bool
	// heldTarget is the sets that a while-held button points at, found
	// before the conversion.
	heldTarget map[int]bool
	// fn is the set that holds each function key, by control.
	fn map[string]int

	stick    [2]stickGlobal
	trig     [2]triggerGlobal
	dpadMode string
	// converted holds the bindings of each set.
	converted map[int]map[string]*binding
	// order is the order the controls are written in.
	rank map[string]int
}

// functionKeyControls are the controls that cannot be bound (ADR-0069).
var functionKeyControls = map[string]string{"back": "View（Back）", "start": "Menu（Start）"}

// Import reads the AntiMicroX profile in data and returns the text of a
// padmate profile.json, and a report of what was dropped or approximated.
// The profile always passes profile.Parse. The error, when there is one, is an
// *Error that says why the file is refused; the report is then nil.
func Import(data []byte, opts Options) ([]byte, []ReportLine, error) {
	root, err := parseTree(data)
	if err != nil {
		return nil, nil, err
	}
	switch root.name {
	case "gamecontroller":
	case "joystick":
		return nil, nil, refuse(ReasonJoystick,
			"这是原始摇杆的配置（根元素 joystick）：它按原始按键编号写，没有标准按键名，padmate 无法对应。只能导入游戏手柄的配置（根元素 gamecontroller）")
	default:
		return nil, nil, refuse(ReasonRoot, "这不是 AntiMicroX 的配置文件（根元素是 %q，应当是 gamecontroller）", cleanText(root.name, 40))
	}
	if v, ok := root.attr("configversion"); ok && v < minVersion {
		return nil, nil, refuse(ReasonVersion,
			"配置版本是 %d，太旧了（需要 %d 或更高）：旧版 AntiMicro 把键码存成各系统自己的编码，无法可靠转换。请用较新的 AntiMicroX 打开后另存一次", v, minVersion)
	}
	rep := newReporter()
	doc := readDocument(root, rep)
	cv := &converter{
		doc: doc, rep: rep, opts: opts,
		names: map[int]string{}, heldTarget: map[int]bool{}, fn: map[string]int{},
		converted: map[int]map[string]*binding{}, rank: controlRanks(),
	}
	cv.fileInfo()
	cv.prescan()
	cv.nameLayers()
	cv.globals()
	for _, i := range doc.setIndexes() {
		cv.converted[i] = cv.convertSet(doc.sets[i])
	}
	out, err := render(cv.build())
	if err != nil {
		return nil, nil, &Error{Reason: ReasonInternal, Message: err.Error()}
	}
	if err := verify(out); err != nil {
		return nil, nil, err
	}
	return out, rep.finish(), nil
}

// fileInfo reports what the file says about itself.
func (cv *converter) fileInfo() {
	d := cv.doc
	switch {
	case !d.hasVersion:
		cv.rep.info("file.version", loc{}, "文件没有写配置版本，按新版本读")
	case d.version > latestVersion:
		cv.rep.info("file.version", loc{}, "配置版本 %d 比这里认识的新版本（%d）还新，可能有读不出的内容", d.version, latestVersion)
	}
	if d.sdlName != "" {
		cv.rep.info("file.device", loc{}, "这份配置是为“%s”做的", cleanText(d.sdlName, 60))
	}
	if isNintendo(d.sdlName, d.uniqueID) {
		cv.rep.info("file.nintendo", loc{},
			"这份配置像是在任天堂手柄上做的：AntiMicroX 用的 SDL2 默认按键帽上印的字母排 A/B/X/Y，padmate 按位置排（south 是下面那个键），导入时没有交换，请核对 A 与 B、X 与 Y")
	}
	if d.keyPressTime > 0 {
		cv.rep.approx("keypress.profile", loc{}, "文件里的按键时间（%d 毫秒）导入为整个配置的点按最短按下时间；AntiMicroX 里它只管暂停和连点产生的按键，padmate 里按住的键也会至少按这么久才松开", min(d.keyPressTime, maxKeyDownMs))
	}
	if d.hasCalibration {
		cv.rep.dropped("file.calibration", loc{}, "摇杆校准（偏移与增益）没有导入；padmate 在设置页按手柄调死区和外圈")
	}
}

// isNintendo reports whether the file looks like it was made for a Nintendo
// controller: by its name, or by the vendor in the SDL2 GUID, which starts
// the uniqueID (vendor 0x057e is stored little endian after the bus and CRC).
func isNintendo(name, uniqueID string) bool {
	n := strings.ToLower(name)
	for _, w := range []string{"nintendo", "joy-con", "joycon", "switch", "wii"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return len(uniqueID) >= 12 && strings.EqualFold(uniqueID[8:12], "7e05")
}

// forEachButton calls f for every button of the set that has a control.
func forEachButton(s *setModel, f func(control string, b *buttonModel)) {
	for _, i := range sortedKeys(s.buttons) {
		f(buttonControls[i], s.buttons[i])
	}
	for _, i := range sortedKeys(s.dpad.dirs) {
		f("dpad."+dpadDirNames[i], s.dpad.dirs[i])
	}
	for w, name := range [2]string{"ls", "rs"} {
		st := s.sticks[w]
		for _, i := range sortedKeys(st.dirs) {
			f(name+"."+stickDirNames[i], st.dirs[i])
		}
		if st.modifierBtn != nil {
			f(name+".outer", st.modifierBtn)
		}
	}
	for w, name := range [2]string{"lt", "rt"} {
		if p := s.trig[w].pull; p != nil {
			f(name, p)
		}
	}
}

func sortedKeys[V any](m map[int]V) []int {
	keys := make([]int, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// prescan finds the sets that buttons point at, before any is converted: a
// hold of a set needs the controls the set leaves unbound to be blocked.
func (cv *converter) prescan() {
	for _, i := range cv.doc.setIndexes() {
		set := cv.doc.sets[i]
		forEachButton(set, func(control string, b *buttonModel) {
			if b.setCond == "while-held" && b.setSelect > 1 && b.setSelect != i {
				if _, fn := functionKeyControls[control]; fn {
					return
				}
				cv.heldTarget[b.setSelect] = true
			}
		})
	}
}

// pointedAt reports whether any button or slot points at set t.
func (cv *converter) pointedAt(t int) bool {
	found := false
	for _, i := range cv.doc.setIndexes() {
		forEachButton(cv.doc.sets[i], func(_ string, b *buttonModel) {
			if b.setCond != "" && b.setSelect == t && i != t {
				found = true
			}
			for _, s := range b.slots {
				if s.mode == "setchange" && s.code+1 == t && i != t {
					found = true
				}
			}
		})
	}
	return found
}

// nameLayers gives each of the sets 2 to 8 that exist or are pointed at the
// name of its layer: the set's name, else set<N>.
func (cv *converter) nameLayers() {
	used := map[string]bool{"base": true, "common": true}
	for t := 2; t <= setCount; t++ {
		set := cv.doc.sets[t]
		if set == nil && !cv.pointedAt(t) {
			continue
		}
		name := ""
		if set != nil {
			name = cleanText(set.name, 40)
		}
		fallback := fmt.Sprintf("set%d", t)
		switch {
		case name == "":
			name = fallback
		case used[strings.ToLower(name)]:
			cv.rep.info("layer.name", loc{set: t}, "set 的名称“%s”已被别的层用了或是保留名，层叫“%s”", name, fallback)
			name = fallback
		}
		for n := 2; used[strings.ToLower(name)]; n++ {
			name = fmt.Sprintf("%s-%d", fallback, n)
		}
		used[strings.ToLower(name)] = true
		cv.names[t] = name
	}
}

// layerName returns the layer of set t.
func (cv *converter) layerName(t int) string {
	if t == 1 {
		return "base"
	}
	if n, ok := cv.names[t]; ok {
		return n
	}
	return fmt.Sprintf("set%d", t)
}

// profileName is the name of the profile.
func (cv *converter) profileName() string {
	for _, n := range []string{cv.opts.Name, cv.doc.profileName, fileBase(cv.opts.FileName)} {
		if n = cleanText(n, 64); n != "" {
			return n
		}
	}
	return "AntiMicroX 配置"
}

// fileBase returns the name of a file without its folder and extension, and
// without the ".gamecontroller" that AntiMicroX suggests.
func fileBase(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		path = path[i+1:]
	}
	for _, ext := range []string{".amgp", ".xml"} {
		if len(path) > len(ext) && strings.EqualFold(path[len(path)-len(ext):], ext) {
			path = path[:len(path)-len(ext)]
			break
		}
	}
	for _, kind := range []string{".gamecontroller", ".joystick"} {
		if len(path) > len(kind) && strings.EqualFold(path[len(path)-len(kind):], kind) {
			path = path[:len(path)-len(kind)]
		}
	}
	return path
}

// convertSet converts the bindings of one set.
func (cv *converter) convertSet(set *setModel) map[string]*binding {
	out := map[string]*binding{}
	add := func(at loc, control string, bb *binding) {
		if !bb.content() {
			return
		}
		if out[control] != nil {
			cv.rep.dropped("control.twice", at, "同一个控件在这一套里绑了两次，只用第一个")
			return
		}
		if why := cv.inert(control); why != "" {
			cv.rep.approx("control.inert", at, "%s", why)
		}
		out[control] = bb
	}
	for _, i := range sortedKeys(set.buttons) {
		control, btn := buttonControls[i], set.buttons[i]
		at := loc{set: set.index, control: control}
		if name, fn := functionKeyControls[control]; fn {
			cv.functionKey(at, set, name, btn)
			continue
		}
		add(at, control, cv.button(at, btn, kindPlain))
	}
	for _, i := range sortedKeys(set.dpad.dirs) {
		control := "dpad." + dpadDirNames[i]
		at := loc{set: set.index, control: control}
		add(at, control, cv.button(at, set.dpad.dirs[i], kindPlain))
	}
	for w, name := range [2]string{"ls", "rs"} {
		st := set.sticks[w]
		if !st.present {
			continue
		}
		if st.square > 0 {
			cv.rep.dropped("stick.square", loc{set: set.index, control: name}, "方形摇杆修正（%d%%）没有对应", st.square)
		}
		for _, i := range sortedKeys(st.dirs) {
			control := name + "." + stickDirNames[i]
			at := loc{set: set.index, control: control}
			if cv.stick[w].mouse {
				cv.mouseDirection(at, st.dirs[i])
				continue
			}
			add(at, control, cv.button(at, st.dirs[i], kindPlain))
		}
		if st.modifierBtn != nil {
			control := name + ".outer"
			if st.modInverted {
				control = name + ".inner"
			}
			at := loc{set: set.index, control: control}
			add(at, control, cv.button(at, st.modifierBtn, kindModifier))
		}
	}
	for w, name := range [2]string{"lt", "rt"} {
		t := set.trig[w]
		if !t.present {
			continue
		}
		at := loc{set: set.index, control: name}
		if t.haptic != "" && !strings.EqualFold(t.haptic, "none") {
			cv.rep.dropped("trigger.haptic", at, "触觉扳机（%s）没有导入", cleanText(t.haptic, 30))
		}
		if t.neg != nil && hasOutput(t.neg.slots) {
			cv.rep.dropped("trigger.negative", at, "扳机没有“向回拉”这一半，丢弃")
		}
		if t.pull != nil {
			add(at, name, cv.button(at, t.pull, kindPlain))
		}
	}
	if set.sensors > 0 {
		cv.rep.dropped("sensor", loc{set: set.index}, "传感器（加速度计、陀螺仪）的按键没有导入")
	}
	for _, other := range set.others {
		cv.rep.dropped("element.other", loc{set: set.index}, "游戏手柄的配置里不会有的“%s”元素，没有导入", other)
	}
	if set.dpad.delay > 0 {
		cv.rep.dropped("dpad.delay", loc{set: set.index, control: "dpad"}, "十字键的方向延迟（%d 毫秒）没有对应", set.dpad.delay)
	}
	return out
}

// functionKey handles a button on back or start, the function keys.
func (cv *converter) functionKey(at loc, set *setModel, name string, btn *buttonModel) {
	// AntiMicroX writes the way back itself where a button holds a set: the
	// same button of that set holds set 1, with no slots. Nothing is in it.
	if set.index != 1 && btn.setCond == "while-held" && btn.setSelect == 1 && len(btn.slots) == 0 && cv.fn[at.control] == set.index {
		return
	}
	if btn.setCond == "while-held" && set.index == 1 && btn.setSelect > 1 {
		if _, ok := cv.fn[at.control]; !ok {
			cv.fn[at.control] = btn.setSelect
			cv.rep.info("function.key", at, "%s 在 AntiMicroX 里按住换到 set %d；导入为“按住 %s”的一套按键（功能键）", name, btn.setSelect, name)
		}
		return
	}
	if btn.setCond != "" || len(btn.slots) > 0 {
		cv.rep.dropped("function.key", at, "%s 在 padmate 里是功能键，不能绑定；只有 set 1 里“按住”换 set 的设置能导入为按住它时的一套按键", name)
	}
}

// mouseDirection handles the button of a direction of a stick that moves the
// pointer: the movement is the stick's mode, nothing else can happen there.
func (cv *converter) mouseDirection(at loc, btn *buttonModel) {
	for _, s := range btn.slots {
		if s.mode != "mousemovement" && hasOutput([]rawSlot{s}) {
			cv.rep.dropped("stick.mouse.keys", at.withSlot(s.pos), "摇杆在移动鼠标，方向上不能再发键，丢弃")
		}
	}
	cv.reportUnconvertibleExceptMouse(at, btn.slots)
}

func (cv *converter) reportUnconvertibleExceptMouse(at loc, slots []rawSlot) {
	var rest []rawSlot
	for _, s := range slots {
		if s.mode != "mousemovement" {
			rest = append(rest, s)
		}
	}
	cv.reportUnconvertible(at, rest)
}

// inert says why a binding on control never fires with the stick or d-pad
// mode of the profile, or "" if it can.
func (cv *converter) inert(control string) string {
	for w, name := range [2]string{"ls.", "rs."} {
		dir, ok := strings.CutPrefix(control, name)
		if !ok {
			continue
		}
		g := cv.stick[w]
		diagonal := strings.HasPrefix(dir, "up") && dir != "up" || strings.HasPrefix(dir, "down") && dir != "down"
		switch {
		case g.mode == "fourWayDiagonal" && !diagonal && dir != "outer" && dir != "inner":
			return "摇杆在“只有对角”模式下，四个正方向不触发这个绑定"
		case (g.mode == "standard" || g.mode == "fourWay") && diagonal:
			return fmt.Sprintf("摇杆在“%s”模式下，对角方向不触发这个绑定（要用八向模式）", modeLabel(g.mode))
		}
		return ""
	}
	switch control {
	case "dpad.upleft", "dpad.upright", "dpad.downleft", "dpad.downright":
		if cv.dpadMode != "eightWay" {
			return "十字键在“普通”或“四向”模式下，对角方向不触发这个绑定（要用八向模式）"
		}
	}
	return ""
}

func modeLabel(mode string) string {
	switch mode {
	case "standard":
		return "标准"
	case "fourWay":
		return "四向"
	case "eightWay":
		return "八向"
	case "fourWayDiagonal":
		return "只有对角"
	}
	return mode
}
