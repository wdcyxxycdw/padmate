package tray

import (
	"slices"
	"strings"
	"testing"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// TestPresentationTracksMapping: each pad's line names the pad and its
// profile only, never layers or latched controls (DMN-18, the owner's
// G10).
func TestPresentationTracksMapping(t *testing.T) {
	views := []slot.View{{Index: 0, Device: 8, Info: input.DeviceInfo{Name: "Xbox Wireless Controller"}, Mapping: engine.View{
		Profile: "绘画", Layers: engine.LayerState{Base: "base", Overlays: []string{"ink"}},
		Latches: []string{"east"}, Mode: 2,
	}}, {Index: 1, Device: 9, Mapping: engine.View{Profile: "绘画", Layers: engine.LayerState{Base: "mode2"}}}}
	p := Present(supervisor.Status{}, views)
	want := []string{"Xbox Wireless Controller · 绘画", "手柄 2 · 绘画"}
	if !slices.Equal(p.Details, want) || p.Tooltip != "padmate · 运行中\n"+strings.Join(want, "\n") {
		t.Fatalf("presentation %q, tooltip %q", p.Details, p.Tooltip)
	}
	for _, layer := range []string{"base", "ink", "mode2", "east", "锁存", "|"} {
		if strings.Contains(p.Tooltip, layer) {
			t.Fatalf("tooltip %q shows %q", p.Tooltip, layer)
		}
	}
	if p.Badge != 2 || p.Inactive {
		t.Fatalf("presentation: %+v", p)
	}
	for _, st := range []supervisor.Status{{Paused: true}, {Suspended: winsession.Sleep}} {
		if !Present(st, views).Inactive {
			t.Fatal("pause/suspend did not change icon state")
		}
	}
	// The words the UI uses: 系统暂停.
	if p := Present(supervisor.Status{Suspended: winsession.Lock}, views); !strings.HasPrefix(p.Tooltip, "padmate · 运行中 · 系统暂停\n") {
		t.Fatalf("tooltip %q", p.Tooltip)
	}
	views[0].Device, views[1].Device = 0, 0
	if p = Present(supervisor.Status{}, views); p.Badge != 0 || !slices.Equal(p.Details, []string{"未连接手柄 · 绘画"}) {
		t.Fatalf("disconnected view: %+v", p)
	}
	if p = Present(supervisor.Status{}, nil); !slices.Equal(p.Details, []string{"未连接手柄"}) {
		t.Fatalf("no view: %+v", p)
	}
}

func TestTooltipUTF16BoundAndFullDetails(t *testing.T) {
	name := strings.Repeat("🖌", 150) + "\x00\r\n"
	p := Present(supervisor.Status{}, []slot.View{{Device: 1, Mapping: engine.View{Profile: name, Mode: 7}}})
	if !utf8.ValidString(p.Tooltip) || len(utf16.Encode([]rune(p.Tooltip))) > 127 || strings.ContainsAny(p.Tooltip, "\x00\r") {
		t.Fatalf("invalid Windows tooltip: %q", p.Tooltip)
	}
	if !strings.HasSuffix(p.Tooltip, "…") || len([]rune(p.Details[0])) < 150 || p.Badge != 0 {
		t.Fatalf("truncation lost full detail or misrepresented mode: %+v", p)
	}
}

// TestPresentWithPower (IN-12): the power of a pad is appended to its line in
// the menu, by the pad's DeviceKey, and is not in the tooltip, whose 127 units
// the name and the profile fill. A pad whose power is unknown, that is plugged
// in (wired) or that has none keeps its line: the menu shows a battery only
// for a pad that discharges or charges (ADR-0067).
func TestPresentWithPower(t *testing.T) {
	views := []slot.View{
		{Index: 0, Device: 8, Key: "a", Info: input.DeviceInfo{Name: "Nova"}, Mapping: engine.View{Profile: "绘画"}},
		{Index: 1, Device: 9, Key: "b", Info: input.DeviceInfo{Name: "Pro"}, Mapping: engine.View{Profile: "绘画"}},
		{Index: 2, Device: 10, Key: "c", Info: input.DeviceInfo{Name: "Third"}, Mapping: engine.View{Profile: "绘画"}},
		{Index: 3, Device: 11, Key: "d", Info: input.DeviceInfo{Name: "Fourth"}, Mapping: engine.View{Profile: "绘画"}},
	}
	power := map[string]input.Power{
		"a": {State: input.PowerDischarging, Percent: 80},
		"b": {State: input.PowerCharging, Percent: 42},
		"c": {State: input.PowerUnknown, Percent: -1},
		"d": {State: input.PowerWired, Percent: 100}, // a receiver's permanent full
	}
	p := PresentWithPower(supervisor.Status{}, views, power)
	want := []string{"Nova · 绘画 · 电量 80%", "Pro · 绘画 · 电量 42% · 充电中", "Third · 绘画", "Fourth · 绘画"}
	if !slices.Equal(p.Details, want) {
		t.Fatalf("details = %q, want %q", p.Details, want)
	}
	if want := "padmate · 运行中\nNova · 绘画\nPro · 绘画\nThird · 绘画\nFourth · 绘画"; p.Tooltip != want {
		t.Fatalf("tooltip = %q, want it without the power: %q", p.Tooltip, want)
	}
	// Present is the same without a power.
	if q := Present(supervisor.Status{}, views); !slices.Equal(q.Details, []string{"Nova · 绘画", "Pro · 绘画", "Third · 绘画", "Fourth · 绘画"}) || q.Tooltip != p.Tooltip {
		t.Fatalf("Present = %+v", q)
	}
	// A charging pad without a charge, and a pad plugged in with one.
	for _, tc := range []struct {
		p    input.Power
		want []string
	}{
		{input.Power{State: input.PowerCharging, Percent: -1}, []string{"充电中"}},
		{input.Power{State: input.PowerWired, Percent: -1}, nil},
		{input.Power{State: input.PowerWired, Percent: 100}, nil},
		{input.Power{State: input.PowerWired, Percent: 40}, nil},
		{input.Power{State: input.PowerCharging, Percent: 100}, []string{"电量 100%", "充电中"}},
		{input.Power{State: input.PowerDischarging, Percent: 0}, []string{"电量 0%"}},
		{input.Power{State: input.PowerDischarging, Percent: -1}, nil},
		{input.Power{State: input.PowerUnknown, Percent: -1}, nil},
	} {
		if got := PowerParts(tc.p); !slices.Equal(got, tc.want) {
			t.Errorf("PowerParts(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
	// No pad is mapped: the line is the same, whatever power there is.
	if q := PresentWithPower(supervisor.Status{}, nil, power); !slices.Equal(q.Details, []string{"未连接手柄"}) {
		t.Fatalf("no view: %+v", q)
	}
}
