package amgp

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// AntiMicroX's defaults, which a missing element means (section 7 of the
// format notes).
const (
	defStickDead     = 8000
	defStickMax      = 32000
	defStickModifier = 8000
	defDiagonal      = 45
	defTriggerDead   = 2000
	defMouseSpeed    = 50
	defWheelSpeed    = 20
	defTurboInterval = 100
	minKeyPressTime  = 10
	// maxKeyDownMs is the longest a key is held down by a time of the profile
	// or a binding: the key press time, and each half of a turbo interval, are
	// cut to it so that a hostile number cannot hold a key for hours.
	maxKeyDownMs  = 10000
	latestVersion = 19
	minVersion    = 6
	setCount      = 8
	// maxSlots bounds the slots read from one button.
	maxSlots = 256
)

// rawSlot is a slot of a button as the file lists it.
type rawSlot struct {
	mode    string
	code    int
	hasCode bool
	text    string // textentry's text, execute's or loadprofile's path
	extra   string // execute's arguments
	mix     []rawSlot
	// pos is the 1-based position among the slots the button keeps.
	pos int
}

// buttonModel is a button, a stick direction, a trigger pull or a d-pad
// direction of one set.
type buttonModel struct {
	actionName string
	toggle     bool
	useTurbo   bool
	turbo      int
	turboMode  string

	cycleReset         bool
	cycleResetInterval int
	hasCycleInterval   bool

	setSelect int // 1-based; 0 none
	setCond   string

	mouseSpeedX, mouseSpeedY int
	wheelX, wheelY           int
	accel                    string
	mouseMode                string

	slots []rawSlot
	// dropped counts the slots beyond maxSlots.
	dropped int
	// extras name the settings of the element that are not imported and
	// that the button actually uses.
	springMode bool
	// mouseExtras are the settings of the pointer's movement that the button
	// has and padmate has nothing for, as mouseSettings lists them, in the
	// order of the table.
	mouseExtras []mouseSetting
}

// mouseSetting is a setting of the pointer's movement that padmate does not
// have: the pointer's speed and curve are read (mouseSpeed, mouseCurve), and
// these are not.
type mouseSetting struct {
	// element is the name of the element as the file writes it.
	element string
	// label says in words what the setting is.
	label string
}

// mouseSettings lists the settings that a button may carry and that change how
// the pointer moves; a stick that is imported as a mouse stick reports each one
// that one of its four directions has, since the pointer then moves
// differently from the way it did in AntiMicroX. Add the elements of other
// settings here.
var mouseSettings = []mouseSetting{
	{"extraacceleration", "额外加速"},
	{"mousesensitivity", "灵敏度"},
	{"easingduration", "缓动时间"},
}

// stickModel is a stick of one set.
type stickModel struct {
	present     bool
	dead, max   int
	modifier    int
	modInverted bool
	diagonal    int
	mode        string
	delay       int
	square      int
	dirs        map[int]*buttonModel
	modifierBtn *buttonModel
	hasSettings bool
}

func newStickModel() *stickModel {
	return &stickModel{
		dead: defStickDead, max: defStickMax, modifier: defStickModifier, diagonal: defDiagonal,
		mode: "standard", dirs: map[int]*buttonModel{},
	}
}

// dpadModel is the d-pad of one set.
type dpadModel struct {
	present bool
	mode    string
	delay   int
	dirs    map[int]*buttonModel
}

// triggerModel is a trigger of one set.
type triggerModel struct {
	present bool
	dead    int
	haptic  string
	pull    *buttonModel
	neg     *buttonModel
}

// setModel is a set of the file.
type setModel struct {
	index   int
	name    string
	buttons map[int]*buttonModel
	sticks  [2]*stickModel
	dpad    *dpadModel
	trig    [2]*triggerModel
	// sensors counts sensors that carry bindings.
	sensors int
	// others counts elements of a game controller set that it never has.
	others []string
}

func newSetModel(index int) *setModel {
	s := &setModel{index: index, buttons: map[int]*buttonModel{}, dpad: &dpadModel{mode: "standard", dirs: map[int]*buttonModel{}}}
	s.sticks = [2]*stickModel{newStickModel(), newStickModel()}
	s.trig = [2]*triggerModel{{dead: defTriggerDead}, {dead: defTriggerDead}}
	return s
}

// document is the file's content.
type document struct {
	version      int
	hasVersion   bool
	sdlName      string
	uniqueID     string
	profileName  string
	keyPressTime int
	sets         map[int]*setModel
	// names are the display names of controls, by padmate control.
	names          map[string]string
	hasCalibration bool
}

// readDocument builds the model of a gamecontroller root.
func readDocument(root *node, rep *reporter) *document {
	doc := &document{sets: map[int]*setModel{}, names: map[string]string{}}
	if v, ok := root.attr("configversion"); ok {
		doc.version, doc.hasVersion = v, true
	}
	doc.sdlName, _ = root.str("sdlname")
	doc.uniqueID, _ = root.str("uniqueID")
	if doc.uniqueID == "" {
		doc.uniqueID, _ = root.str("guid")
	}
	doc.profileName, _ = root.str("profilename")
	if v, ok := root.num("keyPressTime"); ok && v >= minKeyPressTime {
		doc.keyPressTime = v
	}
	doc.hasCalibration = root.child("calibration") != nil
	if names := root.child("names"); names != nil {
		readNames(names, doc)
	}
	if sets := root.child("sets"); sets != nil {
		for _, s := range sets.children("set") {
			idx, ok := s.attr("index")
			if !ok || idx < 1 || idx > setCount {
				continue
			}
			if doc.sets[idx] != nil {
				rep.dropped("set.duplicate", loc{set: idx}, "set %d 在文件里出现了不止一次，只用第一个", idx)
				continue
			}
			doc.sets[idx] = readSet(s, idx, rep)
		}
	}
	return doc
}

// setIndexes returns the numbers of the sets the file has, ascending.
func (d *document) setIndexes() []int {
	out := make([]int, 0, len(d.sets))
	for i := range d.sets {
		out = append(out, i)
	}
	sort.Ints(out)
	return out
}

// buttonControls maps AntiMicroX's button numbers (SDL2's
// SDL_GameControllerButton plus one) to padmate controls. The d-pad's four
// buttons are not written under button, but are accepted.
var buttonControls = map[int]string{
	1: "south", 2: "east", 3: "west", 4: "north", 5: "back", 6: "guide", 7: "start",
	8: "leftstick", 9: "rightstick", 10: "leftshoulder", 11: "rightshoulder",
	12: "dpad.up", 13: "dpad.down", 14: "dpad.left", 15: "dpad.right",
	16: "misc1", 17: "paddle1", 18: "paddle2", 19: "paddle3", 20: "paddle4", 21: "touchpad",
}

// stickDirNames are the directions of a stick button, by AntiMicroX's number.
var stickDirNames = [9]string{"", "up", "upright", "right", "downright", "down", "downleft", "left", "upleft"}

// dpadDirNames are the directions of a d-pad button, by AntiMicroX's number.
var dpadDirNames = map[int]string{
	1: "up", 2: "right", 4: "down", 8: "left", 3: "upright", 6: "downright", 9: "upleft", 12: "downleft",
}

// readNames reads the names the user gave to controls. Names of sticks,
// d-pads and axes themselves have no control to hang on and are skipped, as
// is the default "Stick 1" noise.
func readNames(names *node, doc *document) {
	put := func(control, text string) {
		text = cleanText(text, 80)
		if text == "" || control == "" {
			return
		}
		if _, ok := doc.names[control]; !ok {
			doc.names[control] = text
		}
	}
	for _, n := range names.kids {
		idx, _ := n.attr("index")
		btn, _ := n.attr("button")
		switch n.name {
		case "buttonname":
			put(buttonControls[idx], n.text)
		case "dpadbuttonname":
			if d, ok := dpadDirNames[btn]; ok && idx == 1 {
				put("dpad."+d, n.text)
			}
		case "controlstickbuttonname":
			if btn >= 1 && btn <= 8 && (idx == 1 || idx == 2) {
				put([...]string{"", "ls.", "rs."}[idx]+stickDirNames[btn], n.text)
			}
		case "axisbuttonname":
			if btn == 2 && (idx == 5 || idx == 6) {
				put([...]string{5: "lt", 6: "rt"}[idx], n.text)
			}
		}
	}
}

// readSet reads one set.
func readSet(n *node, idx int, rep *reporter) *setModel {
	s := newSetModel(idx)
	if name, ok := n.str("name"); ok {
		s.name = name
	}
	for _, c := range n.kids {
		at := loc{set: idx}
		switch c.name {
		case "button":
			i, ok := c.attr("index")
			control := buttonControls[i]
			if !ok || control == "" {
				if ok && hasSlots(c) {
					rep.dropped("button.unknown", at, "按键 %d 在游戏手柄上不存在", i)
				}
				continue
			}
			if s.buttons[i] != nil {
				rep.dropped("button.duplicate", loc{set: idx, control: control}, "同一个按键在这一套里写了不止一次，只用第一个")
				continue
			}
			s.buttons[i] = readButton(c)
		case "stick":
			i, ok := c.attr("index")
			if !ok || i < 1 || i > 2 {
				continue
			}
			if s.sticks[i-1].present {
				continue
			}
			s.sticks[i-1] = readStick(c)
		case "dpad":
			if i, ok := c.attr("index"); ok && i == 1 && !s.dpad.present {
				s.dpad = readDpad(c)
			}
		case "trigger":
			i, ok := c.attr("index")
			if !ok {
				continue
			}
			// Old files number the triggers 1 and 2, current ones 5 and 6.
			which := -1
			switch i {
			case 1, 5:
				which = 0
			case 2, 6:
				which = 1
			}
			if which < 0 || s.trig[which].present {
				continue
			}
			s.trig[which] = readTrigger(c)
		case "sensor":
			if hasSlots(c) {
				s.sensors++
			}
		case "name":
		case "axis", "vdpad":
			if hasSlots(c) {
				s.others = append(s.others, c.name)
			}
		}
	}
	return s
}

// hasSlots reports whether any element under n has a slot.
func hasSlots(n *node) bool {
	if n.name == "slot" {
		return true
	}
	for _, k := range n.kids {
		if hasSlots(k) {
			return true
		}
	}
	return false
}

func readStick(n *node) *stickModel {
	s := newStickModel()
	s.present = true
	if v, ok := n.num("deadZone"); ok {
		s.dead, s.hasSettings = clamp(v, 0, 32767), true
	}
	if v, ok := n.num("maxZone"); ok {
		s.max, s.hasSettings = clamp(v, 0, 32767), true
	}
	if v, ok := n.num("modifierZone"); ok {
		s.modifier, s.hasSettings = clamp(v, 0, 32767), true
	}
	if v, ok := n.num("modifierZoneInverted"); ok {
		s.modInverted = v != 0
	} else {
		s.modInverted = n.flag("modifierZoneInverted")
	}
	if v, ok := n.num("diagonalRange"); ok {
		s.diagonal, s.hasSettings = clamp(v, 1, 90), true
	}
	if m, ok := n.str("mode"); ok {
		switch m {
		case "eight-way":
			s.mode, s.hasSettings = "eightWay", true
		case "four-way":
			s.mode, s.hasSettings = "fourWay", true
		case "diagonal":
			s.mode, s.hasSettings = "fourWayDiagonal", true
		}
	}
	if v, ok := n.num("squareStick"); ok && v > 0 && v <= 100 {
		s.square = v
	}
	if v, ok := n.num("stickDelay"); ok && v > 0 {
		s.delay, s.hasSettings = clamp(v, 0, 1000), true
	}
	for _, c := range n.kids {
		switch c.name {
		case "stickbutton":
			if i, ok := c.attr("index"); ok && i >= 1 && i <= 8 && s.dirs[i] == nil {
				s.dirs[i] = readButton(c)
			}
		case "stickmodifierbutton":
			if s.modifierBtn == nil {
				s.modifierBtn = readButton(c)
			}
		}
	}
	return s
}

func readDpad(n *node) *dpadModel {
	d := &dpadModel{present: true, mode: "standard", dirs: map[int]*buttonModel{}}
	if m, ok := n.str("mode"); ok {
		switch m {
		case "eight-way", "four-way", "diagonal":
			d.mode = m
		}
	}
	if v, ok := n.num("dpadDelay"); ok && v > 0 {
		d.delay = v
	}
	for _, c := range n.children("dpadbutton") {
		if i, ok := c.attr("index"); ok && dpadDirNames[i] != "" && d.dirs[i] == nil {
			d.dirs[i] = readButton(c)
		}
	}
	return d
}

func readTrigger(n *node) *triggerModel {
	t := &triggerModel{present: true, dead: defTriggerDead}
	if v, ok := n.num("deadZone"); ok {
		t.dead = clamp(v, 0, 32767)
	}
	t.haptic, _ = n.str("hapticTrigger")
	for _, c := range n.children("triggerbutton") {
		switch i, _ := c.attr("index"); i {
		case 2:
			if t.pull == nil {
				t.pull = readButton(c)
			}
		case 1:
			if t.neg == nil {
				t.neg = readButton(c)
			}
		}
	}
	return t
}

// readButton reads the settings and slots shared by every kind of button.
func readButton(n *node) *buttonModel {
	b := &buttonModel{
		turbo: defTurboInterval, turboMode: "normal",
		mouseSpeedX: defMouseSpeed, mouseSpeedY: defMouseSpeed,
		wheelX: defWheelSpeed, wheelY: defWheelSpeed,
		accel: "precision", mouseMode: "cursor",
	}
	b.actionName, _ = n.str("actionname")
	b.toggle = n.flag("toggle")
	b.useTurbo = n.flag("useturbo")
	if v, ok := n.num("turbointerval"); ok {
		b.turbo = v
	}
	if m, ok := n.str("turbomode"); ok && m != "" {
		b.turboMode = m
	}
	b.cycleReset = n.flag("cycleresetactive")
	if v, ok := n.num("cycleresetinterval"); ok && v >= 10 && v <= 60000 {
		b.cycleResetInterval, b.hasCycleInterval = v, true
	}
	if v, ok := n.num("setselect"); ok && v >= 1 && v <= setCount {
		b.setSelect = v
	}
	if c, ok := n.str("setselectcondition"); ok {
		switch c {
		case "one-way", "two-way", "while-held":
			b.setCond = c
		}
	}
	if v, ok := n.num("mousespeedx"); ok {
		b.mouseSpeedX = v
	}
	if v, ok := n.num("mousespeedy"); ok {
		b.mouseSpeedY = v
	}
	if v, ok := n.num("wheelspeedx"); ok {
		b.wheelX = v
	}
	if v, ok := n.num("wheelspeedy"); ok {
		b.wheelY = v
	}
	if m, ok := n.str("mouseacceleration"); ok && m != "" {
		b.accel = m
	}
	if m, ok := n.str("mousemode"); ok && m != "" {
		b.mouseMode = m
		b.springMode = m == "spring"
	}
	for _, ms := range mouseSettings {
		c := n.child(ms.element)
		if c == nil {
			continue
		}
		// A switch that is off, as a file may spell out, changes nothing.
		if ms.element == "extraacceleration" && !n.flag(ms.element) {
			continue
		}
		b.mouseExtras = append(b.mouseExtras, ms)
	}
	if slots := n.child("slots"); slots != nil {
		for _, s := range slots.children("slot") {
			if len(b.slots) >= maxSlots {
				b.dropped++
				continue
			}
			slot, ok := readSlot(s, 0)
			if !ok {
				continue
			}
			slot.pos = len(b.slots) + 1
			b.slots = append(b.slots, slot)
		}
	}
	return b
}

// slotModes are the modes a slot can have.
var slotModes = map[string]bool{
	"keyboard": true, "mousebutton": true, "mousemovement": true, "pause": true, "hold": true,
	"cycle": true, "distance": true, "release": true, "mousespeedmod": true, "keypress": true,
	"delay": true, "loadprofile": true, "setchange": true, "textentry": true, "execute": true, "mix": true,
}

// readSlot reads a slot, and whether AntiMicroX would keep it: a keyboard
// slot without a code, a text slot without text, and the like, are dropped by
// AntiMicroX itself.
func readSlot(n *node, depth int) (rawSlot, bool) {
	var s rawSlot
	if inner := n.child("slots"); inner != nil {
		// A mix slot holds the slots of a chord.
		if mode, _ := n.str("mode"); mode != "mix" || depth > 0 {
			return s, false
		}
		s.mode = "mix"
		for _, m := range inner.children("slot") {
			if len(s.mix) >= maxSlots {
				break
			}
			if mini, ok := readSlot(m, depth+1); ok {
				s.mix = append(s.mix, mini)
			}
		}
		return s, len(s.mix) > 0
	}
	s.mode, _ = n.str("mode")
	if !slotModes[s.mode] || s.mode == "mix" {
		return s, false
	}
	if v, ok := n.num("code"); ok {
		s.code, s.hasCode = v, true
	}
	switch s.mode {
	case "keyboard":
		return s, s.hasCode && s.code > 0
	case "mousebutton":
		return s, s.hasCode && mouseButtonNames[s.code] != ""
	case "mousemovement":
		return s, s.hasCode && s.code >= 1 && s.code <= 4
	case "textentry":
		s.text, _ = n.str("text")
		if c := n.child("text"); c != nil {
			s.text = c.text
		}
		return s, s.text != ""
	case "execute":
		if c := n.child("path"); c != nil {
			s.text = strings.TrimSpace(c.text)
		}
		if c := n.child("arguments"); c != nil {
			s.extra = strings.TrimSpace(c.text)
		}
		return s, s.text != ""
	case "loadprofile":
		if c := n.child("profile"); c != nil {
			s.text = strings.TrimSpace(c.text)
		}
		return s, s.text != ""
	case "setchange":
		return s, s.hasCode && s.code >= 0 && s.code < setCount
	case "cycle":
		return s, true
	}
	// Times and percentages. A missing or negative one is 0.
	if s.code < 0 {
		s.code = 0
	}
	return s, true
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

// cleanText makes a name for display from text of the file: control
// characters become spaces, runs of white space one space, the ends are
// trimmed, and at most limit characters are kept.
func cleanText(s string, limit int) string {
	var b strings.Builder
	space := true
	n := 0
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) || unicode.Is(unicode.Cf, r) {
			if !space {
				b.WriteByte(' ')
				space = true
			}
			continue
		}
		if n >= limit {
			break
		}
		b.WriteRune(r)
		space = false
		n++
	}
	return strings.TrimSpace(b.String())
}

// clipText is cleanText that says it has cut the text: a quote of the file in
// a message is at most limit characters, and ends in an ellipsis if there was
// more.
func clipText(s string, limit int) string {
	c := cleanText(s, limit+1)
	if r := []rune(c); len(r) > limit {
		return strings.TrimSpace(string(r[:limit])) + "…"
	}
	return c
}
