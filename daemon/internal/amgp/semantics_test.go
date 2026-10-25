package amgp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// The tests here build small files and check one meaning each, so that a
// change of the golden files cannot hide a wrong one.

func xmlSlot(mode string, code int) string {
	if mode == "keyboard" {
		return fmt.Sprintf("<slot><code>0x%x</code><mode>keyboard</mode></slot>", code)
	}
	return fmt.Sprintf("<slot><code>%d</code><mode>%s</mode></slot>", code, mode)
}

func xmlButton(index int, settings string, slots ...string) string {
	return fmt.Sprintf(`<button index="%d">%s<slots>%s</slots></button>`, index, settings, strings.Join(slots, ""))
}

func xmlSet(index int, name string, kids ...string) string {
	n := ""
	if name != "" {
		n = "<name>" + name + "</name>"
	}
	return fmt.Sprintf(`<set index="%d">%s%s</set>`, index, n, strings.Join(kids, ""))
}

func xmlDoc(head string, sets ...string) string {
	return `<gamecontroller configversion="19">` + head + `<sets>` + strings.Join(sets, "") + `</sets></gamecontroller>`
}

// imported imports text and returns the profile as the daemon reads it, the
// JSON as a tree, and the report.
func imported(t *testing.T, text string) (*profile.Profile, map[string]any, []ReportLine) {
	t.Helper()
	out, report, err := Import([]byte(text), Options{FileName: "t.amgp"})
	if err != nil {
		t.Fatal(err)
	}
	p, ds := profile.Parse(out)
	if err := ds.Err(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if _, err := engine.Compile(p); err != nil {
		t.Fatal(err)
	}
	var tree map[string]any
	if err := json.Unmarshal(out, &tree); err != nil {
		t.Fatal(err)
	}
	return p, tree, report
}

// at walks a JSON tree by keys.
func at(t *testing.T, tree any, path ...string) any {
	t.Helper()
	for _, k := range path {
		m, ok := tree.(map[string]any)
		if !ok {
			t.Fatalf("%v: not an object at %q", path, k)
		}
		if tree, ok = m[k]; !ok {
			t.Fatalf("%v: no %q", path, k)
		}
	}
	return tree
}

func has(tree map[string]any, path ...string) bool {
	var cur any = tree
	for _, k := range path {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		if cur, ok = m[k]; !ok {
			return false
		}
	}
	return true
}

func hasCode(report []ReportLine, code string) bool {
	for _, l := range report {
		if l.Code == code {
			return true
		}
	}
	return false
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDefaultsAreWritten(t *testing.T) {
	// An empty file means AntiMicroX's defaults, not padmate's.
	p, tree, _ := imported(t, xmlDoc("", xmlSet(1, "")))
	for _, s := range []string{"ls", "rs"} {
		stick := at(t, tree, "analog", s).(map[string]any)
		if !near(stick["deadzone"].(float64), 0.2441) || !near(stick["saturation"].(float64), 0.9766) {
			t.Errorf("%s: %v", s, stick)
		}
		if stick["directionThreshold"].(float64) != 0 || stick["mode"] != "standard" {
			t.Errorf("%s: %v", s, stick)
		}
	}
	if p.Analog.LS.DirectionThreshold != 0 || !near(p.Analog.LS.Deadzone, 0.2441) {
		t.Errorf("the model has %+v", p.Analog.LS)
	}
	if !near(p.Analog.LT.Press, 0.061) || !near(p.Analog.LT.Release, 0.011) {
		t.Errorf("trigger %+v", p.Analog.LT)
	}
	if p.DefaultLayer != "base" || len(p.Layers) != 1 {
		t.Errorf("layers %v", p.Layers)
	}
}

func TestStickValues(t *testing.T) {
	doc := xmlDoc("", xmlSet(1, "", `<stick index="1"><deadZone>5000</deadZone><maxZone>30000</maxZone><diagonalRange>60</diagonalRange><mode>eight-way</mode><stickDelay>120</stickDelay></stick>`,
		`<stick index="2"><mode>four-way</mode></stick>`))
	p, _, _ := imported(t, doc)
	ls := p.Analog.LS
	if !near(ls.Deadzone, 0.1526) || !near(ls.Saturation, 0.9156) || ls.Mode != "eightWay" || ls.DiagonalWidth != 60 || ls.DirectionDelayMs != 120 || ls.DirectionThreshold != 0 {
		t.Errorf("ls %+v", ls)
	}
	if p.Analog.RS.Mode != "fourWay" || !near(p.Analog.RS.Deadzone, 0.2441) {
		t.Errorf("rs %+v", p.Analog.RS)
	}
	// A dead zone that is not below the outer edge falls back.
	_, tree, report := imported(t, xmlDoc("", xmlSet(1, "", `<stick index="1"><deadZone>20000</deadZone><maxZone>10000</maxZone></stick>`)))
	if !near(at(t, tree, "analog", "ls", "deadzone").(float64), 0.2441) || !hasCode(report, "stick.zone") {
		t.Errorf("report %v", report)
	}
}

func TestTriggerThresholds(t *testing.T) {
	for _, tc := range []struct{ dead, press, release float64 }{
		{2000, 0.061, 0.011}, {16000, 0.4883, 0.4383}, {600, 0.0183, 0}, {0, 0.01, 0}, {40000, 1, 0.95},
	} {
		doc := xmlDoc("", xmlSet(1, "", fmt.Sprintf(`<trigger index="5"><deadZone>%d</deadZone></trigger>`, int(tc.dead))))
		p, _, _ := imported(t, doc)
		if !near(p.Analog.LT.Press, tc.press) || !near(p.Analog.LT.Release, tc.release) {
			t.Errorf("dead zone %v: got %v/%v, want %v/%v", tc.dead, p.Analog.LT.Press, p.Analog.LT.Release, tc.press, tc.release)
		}
	}
}

// Turbo (MAP-23): the period must come out as the interval.
func TestTurbo(t *testing.T) {
	for _, tc := range []struct {
		interval   int
		rate       int
		press, gap int // 0: not written
	}{
		{100, 10, 50, 50}, {250, 4, 125, 125}, {40, 25, 20, 20}, {41, 25, 20, 21}, {30, 25, 0, 0}, {10, 25, 0, 0}, {1000, 1, 500, 500}, {333, 4, 166, 167},
	} {
		doc := xmlDoc("", xmlSet(1, "", xmlButton(1, fmt.Sprintf("<useturbo>true</useturbo><turbointerval>%d</turbointerval>", tc.interval), xmlSlot("keyboard", 0x41))))
		p, tree, _ := imported(t, doc)
		b := p.Layers[0].Bindings[0]
		a := b.Press
		if a == nil || a.Repeat == nil || a.Repeat.RateHz != tc.rate || a.Repeat.DelayMs != 0 || !a.Repeat.DelaySet || a.Mode != profile.ModeTap || a.Repeat.Style != profile.RepeatTap {
			t.Errorf("interval %d: action %+v repeat %+v", tc.interval, a, a.Repeat)
			continue
		}
		timing := map[string]any{}
		if has(tree, "layers", "base", "south", "timing") {
			timing = at(t, tree, "layers", "base", "south", "timing").(map[string]any)
		}
		if got := int(num(timing["tapPressMs"])); got != tc.press {
			t.Errorf("interval %d: tapPressMs %d, want %d", tc.interval, got, tc.press)
		}
		if got := int(num(timing["tapGapMs"])); got != tc.gap {
			t.Errorf("interval %d: tapGapMs %d, want %d", tc.interval, got, tc.gap)
		}
		// The period padmate repeats at: the larger of 1/rate and the
		// shortest period the press and gap times allow, those of the
		// binding at least the default 20 ms each.
		press, gap := max(tc.press, 20), max(tc.gap, 20)
		period := math.Max(1000/float64(tc.rate), float64(press+gap))
		want := math.Max(float64(tc.interval), 40)
		if math.Abs(period-want) > 1 {
			t.Errorf("interval %d: the period is %.1f ms, want %.0f", tc.interval, period, want)
		}
	}
	// Turbo is off below 10 ms, without useturbo, and with a pause.
	for name, settings := range map[string]string{
		"too fast":        "<useturbo>true</useturbo><turbointerval>5</turbointerval>",
		"not switched on": "<turbointerval>100</turbointerval>",
	} {
		p, _, _ := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, settings, xmlSlot("keyboard", 0x41)))))
		if a := p.Layers[0].Bindings[0].Press; a.Repeat != nil || a.Mode != profile.ModeHold {
			t.Errorf("%s: turbo is on: %+v", name, a)
		}
	}
	p, _, _ := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "<useturbo>true</useturbo>", xmlSlot("keyboard", 0x41), xmlSlot("pause", 10), xmlSlot("keyboard", 0x42)))))
	if a := p.Layers[0].Bindings[0].Press; a.Repeat != nil {
		t.Errorf("turbo with a pause: %+v", a)
	}
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func TestHoldLayerBlocksWhatItDoesNotBind(t *testing.T) {
	doc := xmlDoc("",
		xmlSet(1, "", xmlButton(10, "<setselect>2</setselect><setselectcondition>while-held</setselectcondition>"), xmlButton(1, "", xmlSlot("keyboard", 'A')), xmlButton(11, "<setselect>3</setselect><setselectcondition>one-way</setselectcondition>")),
		xmlSet(2, "Held", xmlButton(2, "", xmlSlot("keyboard", 'B')), xmlButton(10, "<setselect>1</setselect><setselectcondition>while-held</setselectcondition>")),
		xmlSet(3, "Switched", xmlButton(2, "", xmlSlot("keyboard", 'C'))))
	_, tree, _ := imported(t, doc)
	// The held layer blocks what it does not bind...
	if !has(tree, "layers", "Held", "south", "block") || has(tree, "layers", "Held", "east", "block") {
		t.Errorf("held layer: %v", at(t, tree, "layers", "Held"))
	}
	if has(tree, "layers", "base", "west", "block") {
		t.Error("the base layer is not blocked")
	}
	// ...a layer that replaces the base does not need to.
	if has(tree, "layers", "Switched", "south") {
		t.Errorf("a switched-to layer is not filled: %v", at(t, tree, "layers", "Switched"))
	}
	if at(t, tree, "layers", "base", "leftshoulder", "press", "op") != "hold" ||
		at(t, tree, "layers", "base", "rightshoulder", "release", "op") != "switch" {
		t.Errorf("selectors: %v", at(t, tree, "layers", "base"))
	}
}

func TestFunctionKeySets(t *testing.T) {
	doc := xmlDoc("",
		xmlSet(1, "", xmlButton(5, "<setselect>2</setselect><setselectcondition>while-held</setselectcondition>"), xmlButton(7, "<setselect>3</setselect><setselectcondition>one-way</setselectcondition>", xmlSlot("keyboard", 'Z'))),
		xmlSet(2, "On view", xmlButton(1, "", xmlSlot("keyboard", 'A'))))
	p, tree, report := imported(t, doc)
	if p.FunctionKeys == nil || p.FunctionKeys.Back == nil || p.FunctionKeys.Start != nil {
		t.Fatalf("function keys %+v", p.FunctionKeys)
	}
	if !has(tree, "functionKeys", "back", "south", "press") || !has(tree, "functionKeys", "back", "east", "block") {
		t.Errorf("function set: %v", at(t, tree, "functionKeys", "back"))
	}
	// The set that only holds the key is not a layer as well.
	if has(tree, "layers", "On view") || len(p.Layers) != 1 {
		t.Errorf("layers: %v", at(t, tree, "layers"))
	}
	// One-way on Menu and the bindings on View and Menu are dropped.
	if !hasCode(report, "function.key") {
		t.Errorf("report: %v", report)
	}
}

func TestLayerNames(t *testing.T) {
	sets := []string{xmlSet(1, "Start")}
	for i, name := range []string{"base", "Common", "", "same", "SAME", "set7", "  spaced   out  "} {
		sets = append(sets, xmlSet(i+2, name, xmlButton(1, "", xmlSlot("keyboard", 'A'))))
	}
	p, _, _ := imported(t, xmlDoc("", sets...))
	var got []string
	for _, l := range p.Layers {
		got = append(got, l.Name)
	}
	seen := map[string]bool{}
	for _, n := range got {
		if seen[strings.ToLower(n)] {
			t.Errorf("layer %q twice in %v", n, got)
		}
		seen[strings.ToLower(n)] = true
	}
	for _, want := range []string{"base", "set2", "set3", "set4", "same", "set6", "set7", "spaced out"} {
		if !seen[want] {
			t.Errorf("no layer %q in %v", want, got)
		}
	}
}

func TestProfileName(t *testing.T) {
	with := `<gamecontroller configversion="19"><profilename>From file</profilename></gamecontroller>`
	without := `<gamecontroller configversion="19"></gamecontroller>`
	tests := []struct {
		xml  string
		opts Options
		want string
	}{
		{with, Options{}, "From file"},
		{with, Options{Name: "Chosen", FileName: "x.amgp"}, "Chosen"},
		{without, Options{FileName: `C:\pads\Painter.gamecontroller.amgp`}, "Painter"},
		{without, Options{}, "AntiMicroX 配置"},
		{without, Options{Name: "  \t "}, "AntiMicroX 配置"},
		{without, Options{Name: strings.Repeat("长", 100)}, strings.Repeat("长", 64)},
	}
	for _, tc := range tests {
		out, _, err := Import([]byte(tc.xml), tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		p, _ := profile.Parse(out)
		if p.Name != tc.want {
			t.Errorf("%+v: name %q, want %q", tc.opts, p.Name, tc.want)
		}
	}
}

func TestKeyPressTime(t *testing.T) {
	for _, tc := range []struct {
		head string
		want int
		set  bool
	}{
		{"<keyPressTime>150</keyPressTime>", 150, true}, {"<keyPressTime>5</keyPressTime>", 0, false}, {"", 0, false}, {"<keyPressTime>x</keyPressTime>", 0, false},
	} {
		p, _, _ := imported(t, xmlDoc(tc.head, xmlSet(1, "")))
		if p.Timing.TapPressSet != tc.set || p.Timing.TapPressMs != tc.want {
			t.Errorf("%q: timing %+v", tc.head, p.Timing)
		}
	}
}

func TestMouseStick(t *testing.T) {
	dir := func(i int, code int) string {
		return fmt.Sprintf(`<stickbutton index="%d"><mousespeedx>30</mousespeedx><mousespeedy>45</mousespeedy><mouseacceleration>cubic</mouseacceleration><slots>%s</slots></stickbutton>`, i, xmlSlot("mousemovement", code))
	}
	all := xmlDoc("", xmlSet(1, "", `<stick index="1">`+dir(1, 1)+dir(5, 2)+dir(7, 3)+dir(3, 4)+`</stick>`))
	p, _, _ := imported(t, all)
	ls := p.Analog.LS
	// Up and down use the vertical speed, left and right the horizontal one;
	// the faster counts, times 20.
	if ls.Mode != "mouse" || ls.MouseSpeed != 900 || ls.Curve != "cubic" {
		t.Errorf("ls %+v", ls)
	}
	// Three of four directions is not a mouse stick.
	three := xmlDoc("", xmlSet(1, "", `<stick index="1">`+dir(1, 1)+dir(5, 2)+dir(7, 3)+`</stick>`))
	p, _, report := imported(t, three)
	if p.Analog.LS.Mode == "mouse" || !hasCode(report, "slot.mousemove") {
		t.Errorf("ls %+v, report %v", p.Analog.LS, report)
	}
	// The directions have to move the right way.
	wrong := xmlDoc("", xmlSet(1, "", `<stick index="1">`+dir(1, 2)+dir(5, 2)+dir(7, 3)+dir(3, 4)+`</stick>`))
	if p, _, _ = imported(t, wrong); p.Analog.LS.Mode == "mouse" {
		t.Error("up moving down is not a mouse stick")
	}
	// A speed beyond the range is cut.
	fast := strings.ReplaceAll(all, "<mousespeedy>45", "<mousespeedy>900")
	if p, _, _ = imported(t, fast); p.Analog.LS.MouseSpeed != 10000 {
		t.Errorf("speed %d", p.Analog.LS.MouseSpeed)
	}
}

func TestModifierZone(t *testing.T) {
	stick := func(extra string) string {
		return xmlDoc("", xmlSet(1, "", `<stick index="1"><deadZone>4000</deadZone><maxZone>28000</maxZone>`+extra+`<stickmodifierbutton><slots>`+xmlSlot("keyboard", 0x1000020)+`</slots></stickmodifierbutton></stick>`))
	}
	p, tree, _ := imported(t, stick("<modifierZone>16000</modifierZone>"))
	if !has(tree, "layers", "base", "ls.outer", "press") || !near(p.Analog.LS.OuterThreshold, 0.5) {
		t.Errorf("outer ring: threshold %v, layer %v", p.Analog.LS.OuterThreshold, at(t, tree, "layers", "base"))
	}
	_, tree, _ = imported(t, stick("<modifierZone>16000</modifierZone><modifierZoneInverted>1</modifierZoneInverted>"))
	if !has(tree, "layers", "base", "ls.inner", "press") || has(tree, "layers", "base", "ls.outer") {
		t.Errorf("inverted zone: %v", at(t, tree, "layers", "base"))
	}
}

func TestSetChangeNeverStrandsTheUser(t *testing.T) {
	// A set that is pointed at but missing becomes an empty layer, so that the
	// switch has somewhere to go.
	doc := xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("setchange", 4))))
	p, tree, _ := imported(t, doc)
	if len(p.Layers) != 2 || !has(tree, "layers", "set5") {
		t.Errorf("layers %v", at(t, tree, "layers"))
	}
	if at(t, tree, "layers", "base", "south", "release", "layer") != "set5" {
		t.Errorf("binding %v", at(t, tree, "layers", "base", "south"))
	}
	// A switch to its own set is nothing; one outside the eight is ignored.
	doc = xmlDoc("", xmlSet(1, "", xmlButton(1, "", xmlSlot("setchange", 0)), xmlButton(2, "", xmlSlot("setchange", 8))))
	if p, _, _ = imported(t, doc); len(p.Layers) != 1 || len(p.Layers[0].Bindings) != 0 {
		t.Errorf("layers %+v", p.Layers)
	}
}

func TestGestureMapping(t *testing.T) {
	cases := []struct {
		name  string
		slots []string
		check func(t *testing.T, b *profile.Binding)
	}{
		{"press", []string{xmlSlot("keyboard", 'A')}, func(t *testing.T, b *profile.Binding) {
			if b.Press == nil || b.Press.Mode != profile.ModeHold || b.Tap != nil || b.Hold != nil {
				t.Errorf("%+v", b)
			}
		}},
		{"tap and hold", []string{xmlSlot("hold", 400), xmlSlot("keyboard", 'K'), xmlSlot("release", 10), xmlSlot("keyboard", 'A'), xmlSlot("release", 390)}, func(t *testing.T, b *profile.Binding) {
			if b.Tap == nil || b.Hold == nil || b.Press != nil || !b.Timing.HoldSet || b.Timing.HoldMs != 400 {
				t.Errorf("%+v", b)
			}
		}},
		{"long release is a hold", []string{xmlSlot("release", 600), xmlSlot("keyboard", 'A')}, func(t *testing.T, b *profile.Binding) {
			if b.Hold == nil || b.Tap != nil || b.Timing.HoldMs != 600 {
				t.Errorf("%+v", b)
			}
		}},
		{"short release is a tap", []string{xmlSlot("release", 10), xmlSlot("keyboard", 'A')}, func(t *testing.T, b *profile.Binding) {
			if b.Tap == nil || b.Hold != nil {
				t.Errorf("%+v", b)
			}
		}},
		{"press and release", []string{xmlSlot("keyboard", 'A'), xmlSlot("release", 10), xmlSlot("keyboard", 'B')}, func(t *testing.T, b *profile.Binding) {
			if b.Press == nil || b.Release == nil {
				t.Errorf("%+v", b)
			}
		}},
		{"double tap", []string{xmlSlot("release", 10), xmlSlot("pause", 0), xmlSlot("release", 490), xmlSlot("cycle", 1), xmlSlot("release", 10), xmlSlot("keyboard", 'D')}, func(t *testing.T, b *profile.Binding) {
			if b.DoubleTap == nil || b.Tap != nil || !b.Timing.MultiTapSet || b.Timing.MultiTapMs != 500 {
				t.Errorf("%+v", b)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _, _ := imported(t, xmlDoc("", xmlSet(1, "", xmlButton(1, "<cycleresetactive>true</cycleresetactive><cycleresetinterval>500</cycleresetinterval>", tc.slots...))))
			if len(p.Layers[0].Bindings) != 1 {
				t.Fatalf("bindings %+v", p.Layers[0].Bindings)
			}
			tc.check(t, p.Layers[0].Bindings[0])
		})
	}
}

func TestDeterministic(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.amgp"))
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out1, rep1, err1 := Import(data, Options{})
		for i := 0; i < 3; i++ {
			out2, rep2, err2 := Import(data, Options{})
			if (err1 == nil) != (err2 == nil) || !bytes.Equal(out1, out2) || FormatReport(rep1) != FormatReport(rep2) {
				t.Fatalf("%s: two imports differ", f)
			}
		}
	}
}

func TestReportText(t *testing.T) {
	lines := []ReportLine{
		{Severity: SeverityDropped, Code: "a", Set: 2, Control: "lt", Slot: 3, Message: "x"},
		{Severity: SeverityApproximate, Code: "b", Set: 1, Control: "south", Message: "y"},
		{Severity: SeverityInfo, Code: "c", Message: "z"},
	}
	want := "丢弃  set 2 / lt / slot 3：x\n近似  set 1 / south：y\n信息  z\n"
	if got := FormatReport(lines); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
	if d, a, i := Counts(lines); d != 1 || a != 1 || i != 1 {
		t.Errorf("counts %d %d %d", d, a, i)
	}
	if FormatReport(nil) != "" {
		t.Error("an empty report is empty text")
	}
	data, err := json.Marshal(lines[0])
	if err != nil || string(data) != `{"severity":"dropped","code":"a","set":2,"control":"lt","slot":3,"message":"x"}` {
		t.Errorf("json %s %v", data, err)
	}
}

// The report is the same line once.
func TestReportDeduplicates(t *testing.T) {
	r := newReporter()
	for i := 0; i < 3; i++ {
		r.info("c", loc{set: 1}, "same")
	}
	r.info("c", loc{set: 2}, "same")
	if got := r.finish(); len(got) != 2 {
		t.Errorf("got %v", got)
	}
}
