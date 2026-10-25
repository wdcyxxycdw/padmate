package control_test

import (
	"slices"
	"strconv"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// TestNames checks every canonical name and alias against ARCHITECTURE
// §7.1.
func TestNames(t *testing.T) {
	canonical := []struct {
		name string
		c    control.Control
	}{
		{"south", control.South},
		{"east", control.East},
		{"west", control.West},
		{"north", control.North},
		{"back", control.Back},
		{"guide", control.Guide},
		{"start", control.Start},
		{"leftstick", control.LeftStick},
		{"rightstick", control.RightStick},
		{"leftshoulder", control.LeftShoulder},
		{"rightshoulder", control.RightShoulder},
		{"dpad.up", control.DpadUp},
		{"dpad.down", control.DpadDown},
		{"dpad.left", control.DpadLeft},
		{"dpad.right", control.DpadRight},
		{"misc1", control.Misc1},
		{"misc2", control.Misc2},
		{"misc3", control.Misc3},
		{"misc4", control.Misc4},
		{"misc5", control.Misc5},
		{"misc6", control.Misc6},
		{"paddle1", control.Paddle1},
		{"paddle2", control.Paddle2},
		{"paddle3", control.Paddle3},
		{"paddle4", control.Paddle4},
		{"touchpad", control.Touchpad},
		{"lt", control.LT},
		{"rt", control.RT},
		{"ls.up", control.LSUp}, {"ls.down", control.LSDown}, {"ls.left", control.LSLeft}, {"ls.right", control.LSRight},
		{"ls.upleft", control.LSUpLeft}, {"ls.upright", control.LSUpRight}, {"ls.downleft", control.LSDownLeft}, {"ls.downright", control.LSDownRight}, {"ls.active", control.LSActive},
		{"rs.up", control.RSUp}, {"rs.down", control.RSDown}, {"rs.left", control.RSLeft}, {"rs.right", control.RSRight},
		{"rs.upleft", control.RSUpLeft}, {"rs.upright", control.RSUpRight}, {"rs.downleft", control.RSDownLeft}, {"rs.downright", control.RSDownRight}, {"rs.active", control.RSActive},
		{"dpad.upleft", control.DpadUpLeft}, {"dpad.upright", control.DpadUpRight}, {"dpad.downleft", control.DpadDownLeft}, {"dpad.downright", control.DpadDownRight},
		{"leftx+", control.LeftXPositive}, {"leftx-", control.LeftXNegative}, {"lefty+", control.LeftYPositive}, {"lefty-", control.LeftYNegative},
		{"rightx+", control.RightXPositive}, {"rightx-", control.RightXNegative}, {"righty+", control.RightYPositive}, {"righty-", control.RightYNegative},
		{"lefttrigger+", control.LeftTriggerPositive}, {"lefttrigger-", control.LeftTriggerNegative}, {"righttrigger+", control.RightTriggerPositive}, {"righttrigger-", control.RightTriggerNegative},
		{"ls.inner", control.LSInner}, {"ls.outer", control.LSOuter}, {"ls.cw", control.LSCW}, {"ls.ccw", control.LSCCW},
		{"rs.inner", control.RSInner}, {"rs.outer", control.RSOuter}, {"rs.cw", control.RSCW}, {"rs.ccw", control.RSCCW},
		{"lt.full", control.LTFull}, {"rt.full", control.RTFull},
	}
	all := control.All()
	if len(all) != len(canonical) {
		t.Fatalf("All() has %d controls, the test lists %d", len(all), len(canonical))
	}
	for i, tc := range canonical {
		if all[i] != tc.c {
			t.Errorf("All()[%d] = %v, want %v", i, all[i], tc.c)
		}
		if got := tc.c.String(); got != tc.name {
			t.Errorf("%d.String() = %q, want %q", uint8(tc.c), got, tc.name)
		}
		if c, ok := control.Parse(tc.name); !ok || c != tc.c {
			t.Errorf("Parse(%q) = %v, %t, want %v", tc.name, c, ok, tc.c)
		}
	}

	aliases := []control.Alias{
		{Name: "a", Control: control.South},
		{Name: "b", Control: control.East},
		{Name: "x", Control: control.West},
		{Name: "y", Control: control.North},
		{Name: "lb", Control: control.LeftShoulder},
		{Name: "rb", Control: control.RightShoulder},
		{Name: "l3", Control: control.LeftStick},
		{Name: "r3", Control: control.RightStick},
		{Name: "select", Control: control.Back},
	}
	if got := control.Aliases(); !slices.Equal(got, aliases) {
		t.Errorf("Aliases() = %v, want %v", got, aliases)
	}
	for _, a := range aliases {
		c, ok := control.Parse(a.Name)
		if !ok || c != a.Control {
			t.Errorf("Parse(%q) = %v, %t, want %v", a.Name, c, ok, a.Control)
		}
		// Aliases are input only.
		if c.String() == a.Name {
			t.Errorf("alias %q formats as itself", a.Name)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, name := range []string{
		"", "South", "SOUTH", " south", "south ", "dpad", "dpad.", "dpad.bad",
		"button3", "control(1)", "lefttrigger", "righttrigger", "leftx", "LT", "A",
		"ls.bad", "rs.bad", "ls.inner ", "ls.CW", "ls.cwx", "lt.half", "lt.", "rt.full.", "lt.positive", "combo:a+b", "a>b", "paddle0", "paddle5", "misc0", "misc7",
	} {
		if c, ok := control.Parse(name); ok || c != control.Invalid {
			t.Errorf("Parse(%q) = %v, %t, want Invalid, false", name, c, ok)
		}
	}
}

// TestAliasesDistinct checks that no alias is also a canonical name and
// that every alias is spelled once.
func TestAliasesDistinct(t *testing.T) {
	canonical := make(map[string]bool)
	for _, c := range control.All() {
		if canonical[c.String()] {
			t.Errorf("canonical name %q appears twice", c)
		}
		canonical[c.String()] = true
	}
	seen := make(map[string]bool)
	for _, a := range control.Aliases() {
		if canonical[a.Name] {
			t.Errorf("alias %q is also a canonical name", a.Name)
		}
		if seen[a.Name] {
			t.Errorf("alias %q appears twice", a.Name)
		}
		seen[a.Name] = true
		if !a.Control.IsValid() || a.Control.IsTrigger() {
			t.Errorf("alias %q names %v", a.Name, a.Control)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, c := range control.All() {
		if !c.IsValid() {
			t.Errorf("%v from All() is invalid", c)
		}
		back, ok := control.Parse(c.String())
		if !ok || back != c {
			t.Errorf("Parse(%q) = %v, %t, want %v", c.String(), back, ok, c)
		}
	}
}

func TestInvalid(t *testing.T) {
	last := control.All()[len(control.All())-1]
	for _, tc := range []struct {
		c    control.Control
		want string
	}{
		{control.Invalid, "control(0)"},
		{last + 1, "control(" + strconv.Itoa(control.Count) + ")"},
		{255, "control(255)"},
	} {
		if got := tc.c.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
		if tc.c.IsValid() || tc.c.IsTrigger() || tc.c.Axis() != control.InvalidAxis {
			t.Errorf("%v: IsValid %t, IsTrigger %t, Axis %v", tc.c, tc.c.IsValid(), tc.c.IsTrigger(), tc.c.Axis())
		}
	}
}

// TestCount checks that Count and AxisCount size arrays indexed by every
// valid value.
func TestCount(t *testing.T) {
	all := control.All()
	if len(all) != control.Count-1 || int(all[len(all)-1]) != control.Count-1 || control.Control(control.Count).IsValid() {
		t.Errorf("Count = %d, want %d", control.Count, len(all)+1)
	}
	axes := control.AllAxes()
	if len(axes) != control.AxisCount-1 || int(axes[len(axes)-1]) != control.AxisCount-1 || control.Axis(control.AxisCount).IsValid() {
		t.Errorf("AxisCount = %d, want %d", control.AxisCount, len(axes)+1)
	}
}

func TestTriggers(t *testing.T) {
	want := map[control.Control]control.Axis{
		control.LT: control.LeftTrigger,
		control.RT: control.RightTrigger,
	}
	for _, c := range control.All() {
		axis, trigger := want[c]
		if c.IsTrigger() != trigger {
			t.Errorf("%v.IsTrigger() = %t, want %t", c, !trigger, trigger)
		}
		if !trigger {
			axis = control.InvalidAxis
		}
		if got := c.Axis(); got != axis {
			t.Errorf("%v.Axis() = %v, want %v", c, got, axis)
		}
	}
}

// TestSlicesAreCopies checks that callers cannot change the tables.
func TestSlicesAreCopies(t *testing.T) {
	all := control.All()
	all[0] = control.Invalid
	if control.All()[0] != control.South {
		t.Error("All() returned the package's own slice")
	}
	aliases := control.Aliases()
	aliases[0].Control = control.Invalid
	if control.Aliases()[0].Control != control.South {
		t.Error("Aliases() returned the package's own slice")
	}
	axes := control.AllAxes()
	axes[0] = control.InvalidAxis
	if control.AllAxes()[0] != control.LeftX {
		t.Error("AllAxes() returned the package's own slice")
	}
}

func TestAxes(t *testing.T) {
	want := []struct {
		name string
		a    control.Axis
	}{
		{"leftx", control.LeftX},
		{"lefty", control.LeftY},
		{"rightx", control.RightX},
		{"righty", control.RightY},
		{"lefttrigger", control.LeftTrigger},
		{"righttrigger", control.RightTrigger},
	}
	all := control.AllAxes()
	if len(all) != len(want) {
		t.Fatalf("AllAxes() has %d axes, the test lists %d", len(all), len(want))
	}
	for i, tc := range want {
		if all[i] != tc.a {
			t.Errorf("AllAxes()[%d] = %v, want %v", i, all[i], tc.a)
		}
		if !tc.a.IsValid() {
			t.Errorf("%v is invalid", tc.a)
		}
		if got := tc.a.String(); got != tc.name {
			t.Errorf("%d.String() = %q, want %q", uint8(tc.a), got, tc.name)
		}
		if a, ok := control.ParseAxis(tc.name); !ok || a != tc.a {
			t.Errorf("ParseAxis(%q) = %v, %t, want %v", tc.name, a, ok, tc.a)
		}
	}
	for _, name := range []string{"", "LeftX", "lt", "rt", "south", "axis(1)", "leftx "} {
		if a, ok := control.ParseAxis(name); ok || a != control.InvalidAxis {
			t.Errorf("ParseAxis(%q) = %v, %t, want InvalidAxis, false", name, a, ok)
		}
	}
	for _, tc := range []struct {
		a    control.Axis
		want string
	}{
		{control.InvalidAxis, "axis(0)"},
		{control.RightTrigger + 1, "axis(7)"},
		{255, "axis(255)"},
	} {
		if got := tc.a.String(); got != tc.want {
			t.Errorf("String() = %q, want %q", got, tc.want)
		}
		if tc.a.IsValid() {
			t.Errorf("%v is valid", tc.a)
		}
	}
}

func TestM4VirtualControls(t *testing.T) {
	for _, name := range []string{
		"ls.up", "ls.down", "ls.left", "ls.right", "ls.upleft", "ls.upright", "ls.downleft", "ls.downright", "ls.active",
		"rs.up", "rs.down", "rs.left", "rs.right", "rs.upleft", "rs.upright", "rs.downleft", "rs.downright", "rs.active",
		"dpad.upleft", "dpad.upright", "dpad.downleft", "dpad.downright",
		"leftx+", "leftx-", "lefty+", "lefty-", "rightx+", "rightx-", "righty+", "righty-",
		"lefttrigger+", "lefttrigger-", "righttrigger+", "righttrigger-",
	} {
		c, ok := control.Parse(name)
		if !ok || !c.IsValid() || c.String() != name {
			t.Errorf("Parse(%q) = %v, %t; want canonical virtual control", name, c, ok)
		}
	}
	for _, tc := range []struct{ alias, canonical string }{
		{"leftx.positive", "leftx+"}, {"leftx.negative", "leftx-"},
		{"righttrigger.positive", "righttrigger+"}, {"righttrigger.negative", "righttrigger-"},
	} {
		c, ok := control.Parse(tc.alias)
		want, _ := control.Parse(tc.canonical)
		if !ok || c != want {
			t.Errorf("Parse(%q) = %v, %t; want %v", tc.alias, c, ok, want)
		}
	}
}

// TestM15Controls checks the controls of the rings, the virtual knob and the
// second trigger stage (ANA-10, ANA-11, ANA-14): their numbers come after the
// half axes, so no earlier value moved, and they are virtual with no aliases.
func TestM15Controls(t *testing.T) {
	if control.RightTriggerNegative+1 != control.LSInner {
		t.Errorf("LSInner = %d, want %d: the new controls follow the half axes", control.LSInner, control.RightTriggerNegative+1)
	}
	if control.RTFull != control.Control(control.Count-1) {
		t.Errorf("RTFull = %d, want the last control, %d", control.RTFull, control.Count-1)
	}
	for _, name := range []string{"ls.inner", "ls.outer", "ls.cw", "ls.ccw", "rs.inner", "rs.outer", "rs.cw", "rs.ccw", "lt.full", "rt.full"} {
		c, ok := control.Parse(name)
		if !ok || !c.IsValid() || c.String() != name || !c.IsVirtual() || c.IsTrigger() || c.Axis() != control.InvalidAxis {
			t.Errorf("Parse(%q) = %v, %t (virtual %t, trigger %t, axis %v); want a canonical virtual control",
				name, c, ok, c.IsVirtual(), c.IsTrigger(), c.Axis())
		}
	}
	for _, name := range []string{"ls.inner", "lt.full", "rs.cw"} {
		for _, a := range control.Aliases() {
			if a.Name == name {
				t.Errorf("%q must not be an alias", name)
			}
		}
	}
}

// TestHasDepth checks which controls follow how far a stick or a trigger is
// pushed (ANA-12, ANA-15): the ones a repeat may give a minRateHz.
func TestHasDepth(t *testing.T) {
	want := map[string]bool{}
	for _, name := range []string{
		"ls.up", "ls.down", "ls.left", "ls.right", "ls.upleft", "ls.upright", "ls.downleft", "ls.downright", "ls.active", "ls.inner", "ls.outer",
		"rs.up", "rs.down", "rs.left", "rs.right", "rs.upleft", "rs.upright", "rs.downleft", "rs.downright", "rs.active", "rs.inner", "rs.outer",
		"leftx+", "leftx-", "lefty+", "lefty-", "rightx+", "rightx-", "righty+", "righty-",
		"lefttrigger+", "lefttrigger-", "righttrigger+", "righttrigger-",
		"lt", "rt", "lt.full", "rt.full",
	} {
		want[name] = true
	}
	for _, c := range control.All() {
		if got := c.HasDepth(); got != want[c.String()] {
			t.Errorf("%s.HasDepth() = %t, want %t", c, got, want[c.String()])
		}
	}
	if control.Invalid.HasDepth() || control.Control(250).HasDepth() {
		t.Error("an invalid control has depth")
	}
}

// Back and Start are the function keys (MAP-27, ADR-0069), and only they.
func TestIsFunctionKey(t *testing.T) {
	for _, c := range control.All() {
		want := c == control.Back || c == control.Start
		if got := c.IsFunctionKey(); got != want {
			t.Errorf("%s.IsFunctionKey() = %t, want %t", c, got, want)
		}
	}
	if control.Invalid.IsFunctionKey() {
		t.Error("Invalid is a function key")
	}
	// The alias of back is the function key, too.
	if c, ok := control.Parse("select"); !ok || !c.IsFunctionKey() {
		t.Errorf("select parses to %v, %t", c, ok)
	}
}
