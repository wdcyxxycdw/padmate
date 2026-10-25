package script

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// fakeHUD records the toasts a host sends while connected is true.
type fakeHUD struct {
	connected bool
	toasts    []fakeToast
}

type fakeToast struct {
	slot uint8
	text string
	ms   int
}

func (f *fakeHUD) Connected() bool { return f.connected }

func (f *fakeHUD) Toast(slot uint8, text string, ms int) {
	f.toasts = append(f.toasts, fakeToast{slot, text, ms})
}

// hudHost is an active host on slot 2 whose environment has hud.
func hudHost(t *testing.T, hud HUD, src string) (*Host, *clock.Fake) {
	t.Helper()
	env := new(Environment)
	env.SetHUD(hud)
	clk := clock.NewFake()
	h := NewHostWithOptions(HostOptions{Clock: clk, Environment: env, Owner: output.Owner{Slot: 2, Gen: 1, Source: output.SourceScript}})
	t.Cleanup(func() { _ = h.Dispose() })
	if err := h.Activate(environmentProgram(t, src)); err != nil {
		t.Fatal(err)
	}
	return h, clk
}

func hudRun(t *testing.T, h *Host, src string) {
	t.Helper()
	if _, err := h.VM.RunString(src); err != nil {
		t.Fatalf("%s: %v", src, err)
	}
}

func hudThrows(t *testing.T, h *Host, src, want string) {
	t.Helper()
	_, err := h.VM.RunString(src)
	if err == nil || !strings.Contains(err.Error(), "TypeError") || !strings.Contains(err.Error(), want) {
		t.Errorf("%s: err = %v, want a TypeError with %q", src, err, want)
	}
}

func TestHUDToast(t *testing.T) {
	hud := &fakeHUD{connected: true}
	h, _ := hudHost(t, hud, `const hud = __padmate.hud;`)
	hudRun(t, h, `hud.toast("  画笔\t大小\n12  ")`)
	hudRun(t, h, `hud.toast("短", { ms: 100 })`)
	hudRun(t, h, `hud.toast("长", { ms: 20000 })`)
	hudRun(t, h, `hud.toast("半", { ms: 1234.5 })`)
	hudRun(t, h, `hud.toast("缺", { ms: undefined, other: 1 })`)
	hudRun(t, h, `hud.toast(" \n\t ")`) // nothing left: not shown
	hudRun(t, h, `hud.toast("x".repeat(300))`)
	want := []fakeToast{
		{2, "画笔 大小 12", 1500}, {2, "短", 500}, {2, "长", 10000}, {2, "半", 1235}, {2, "缺", 1500},
		{2, strings.Repeat("x", 200), 1500},
	}
	if !reflect.DeepEqual(hud.toasts, want) {
		t.Fatalf("toasts = %+v\nwant %+v", hud.toasts, want)
	}
	hudThrows(t, h, `hud.toast(12)`, "hud.toast: text must be a string")
	hudThrows(t, h, `hud.toast("a", 5)`, "hud.toast: options must be an object")
	hudThrows(t, h, `hud.toast("a", { ms: "5" })`, "hud.toast: ms must be a finite number")
	hudThrows(t, h, `hud.toast("a", { ms: NaN })`, "hud.toast: ms must be a finite number")
	hudThrows(t, h, `hud.toast("a", { ms: Infinity })`, "hud.toast: ms must be a finite number")
	if len(hud.toasts) != len(want) {
		t.Fatalf("a rejected call showed a toast: %+v", hud.toasts)
	}
}

// host.has("hud") follows the overlay, read on each call; without one a
// toast does nothing.
func TestHUDConnected(t *testing.T) {
	hud := &fakeHUD{}
	h, _ := hudHost(t, hud, `const host = __padmate.host, hud = __padmate.hud;`)
	if v, _ := h.VM.RunString(`host.has("hud")`); v.ToBoolean() {
		t.Fatal(`has("hud") without an overlay`)
	}
	hudRun(t, h, `hud.toast("nobody")`)
	hud.connected = true
	if v, _ := h.VM.RunString(`host.has("hud")`); !v.ToBoolean() {
		t.Fatal(`has("hud") with an overlay`)
	}
	hudRun(t, h, `hud.toast("seen")`)
	if len(hud.toasts) != 1 || hud.toasts[0].text != "seen" {
		t.Fatalf("toasts = %+v", hud.toasts)
	}

	plain := NewHostWithOptions(HostOptions{Clock: clock.NewFake()})
	t.Cleanup(func() { _ = plain.Dispose() })
	if err := plain.Activate(environmentProgram(t, `const hud = __padmate.hud;`)); err != nil {
		t.Fatal(err)
	}
	hudRun(t, plain, `hud.toast("no environment")`)
	if v, _ := plain.VM.RunString(`__padmate.host.has("hud")`); v.ToBoolean() {
		t.Fatal(`has("hud") without an environment`)
	}
}

func TestHUDToastRate(t *testing.T) {
	hud := &fakeHUD{connected: true}
	h, clk := hudHost(t, hud, `const hud = __padmate.hud;`)
	hudRun(t, h, `for (let i = 0; i < 12; i++) hud.toast("t" + i)`)
	if len(hud.toasts) != hudToastsPerSecond {
		t.Fatalf("%d toasts in one second, want %d", len(hud.toasts), hudToastsPerSecond)
	}
	logs, _ := h.DrainLogs()
	if len(logs) != 1 || logs[0].Level != "warn" || !strings.Contains(logs[0].Message, "more than 10 toasts in a second") {
		t.Fatalf("logs = %+v", logs)
	}
	clk.Advance(999 * time.Millisecond)
	hudRun(t, h, `hud.toast("still")`)
	if len(hud.toasts) != hudToastsPerSecond {
		t.Fatal("a toast passed within the second")
	}
	if logs, _ := h.DrainLogs(); len(logs) != 0 {
		t.Fatalf("second warning in one window: %+v", logs)
	}
	clk.Advance(time.Millisecond)
	hudRun(t, h, `hud.toast("next")`)
	if n := len(hud.toasts); n != hudToastsPerSecond+1 || hud.toasts[n-1].text != "next" {
		t.Fatalf("toasts after the second: %+v", hud.toasts)
	}
}

// The hud API has nothing to show in setup, where it throws, and does
// nothing where nothing should show: a describe run and teardown.
func TestHUDOutsideAnActiveRuntime(t *testing.T) {
	hud := &fakeHUD{connected: true}
	for _, src := range []string{
		`__padmate.hud.toast("setup");`,
		`__padmate.hud.panel("p", { rows: [] });`,
	} {
		env := new(Environment)
		env.SetHUD(hud)
		h := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env})
		err := h.Activate(environmentProgram(t, src))
		if err == nil || !strings.Contains(err.Error(), "is not available in setup; call it from onActivate or a handler") {
			t.Errorf("%s: %v", src, err)
		}
		_ = h.Dispose()
	}

	env := new(Environment)
	env.SetHUD(hud)
	describe := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Environment: env, Mode: "describe"})
	t.Cleanup(func() { _ = describe.Dispose() })
	if err := describe.Activate(environmentProgram(t, `__padmate.hud.toast("describe"); __padmate.hud.panel("p", { rows: [["a", "b"]] });
var has = __padmate.host.has("hud");`)); err != nil {
		t.Fatal(err)
	}
	if v := describe.VM.Get("has"); v.ToBoolean() || describe.HUDPanels() != nil {
		t.Fatalf(`describe: has("hud") = %v, panels %v`, v, describe.HUDPanels())
	}

	h, _ := hudHost(t, hud, `const hud = __padmate.hud;
var teardown = () => { hud.toast("teardown"); hud.panel("t", { rows: [] }); };`)
	hudRun(t, h, `hud.panel("p", { rows: [["a", "b"]] })`)
	h.ReleaseAll()
	if h.HUDPanels() != nil {
		t.Fatalf("panels after ReleaseAll: %v", h.HUDPanels())
	}
	hudRun(t, h, `teardown()`)
	if len(hud.toasts) != 0 {
		t.Fatalf("toasts outside an active runtime: %+v", hud.toasts)
	}
}

func TestHUDPanel(t *testing.T) {
	h, _ := hudHost(t, &fakeHUD{}, `const hud = __padmate.hud;`)
	if h.HUDPanels() != nil {
		t.Fatal("panels before any")
	}
	hudRun(t, h, `hud.panel("brush", { title: " 画笔\n", rows: [["大小", "12"], ["流量", " 80% "]] })`)
	hudRun(t, h, `hud.panel(" layer ", { rows: [] })`)
	hudRun(t, h, `hud.panel("brush", { title: "笔", rows: [["大小", "13"]] })`) // keeps its place
	got := h.HUDPanels()
	want := []HUDPanel{
		{ID: "brush", Title: "笔", Rows: [][2]string{{"大小", "13"}}},
		{ID: "layer"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("panels = %+v\nwant %+v", got, want)
	}
	got[0].Rows[0][0] = "changed"
	if h.HUDPanels()[0].Rows[0][0] != "大小" {
		t.Fatal("HUDPanels shares its rows")
	}

	hudRun(t, h, `hud.panel("layer", null); hud.panel("missing", undefined)`)
	hudRun(t, h, `const rows = []; for (let i = 0; i < 20; i++) rows.push(["k" + i, "v"]); hud.panel("long", { rows, extra: 1 })`)
	hudRun(t, h, `hud.panel("cut", { title: "t".repeat(50), rows: [["l".repeat(50), "r"]] })`)
	got = h.HUDPanels()
	if len(got) != 3 || got[0].ID != "brush" || got[1].ID != "long" || len(got[1].Rows) != hudPanelRows || got[1].Rows[15][0] != "k15" {
		t.Fatalf("panels = %+v", got)
	}
	if got[2].Title != strings.Repeat("t", 40) || got[2].Rows[0][0] != strings.Repeat("l", 40) {
		t.Fatalf("cut = %+v", got[2])
	}

	hudRun(t, h, `hud.panel("four", { rows: [] })`)
	hudThrows(t, h, `hud.panel("five", { rows: [] })`, "hud.panel: at most 4 panels; remove one with hud.panel(id, null)")
	hudRun(t, h, `hud.panel("four", { rows: [["updated", ""]] })`) // an update is not a fifth
	for src, want := range map[string]string{
		`hud.panel("", { rows: [] })`:                     "hud.panel: id must be a string of 1 to 32 characters",
		`hud.panel("   ", { rows: [] })`:                  "hud.panel: id must be a string of 1 to 32 characters",
		`hud.panel("x".repeat(33), { rows: [] })`:         "hud.panel: id must be a string of 1 to 32 characters",
		`hud.panel(1, { rows: [] })`:                      "hud.panel: id must be a string of 1 to 32 characters",
		`hud.panel("brush", "text")`:                      "hud.panel: content must be an object or null",
		`hud.panel("brush", [["a", "b"]])`:                "hud.panel: content must be an object or null",
		`hud.panel("brush", { title: 1, rows: [] })`:      "hud.panel: title must be a string",
		`hud.panel("brush", {})`:                          "hud.panel: rows must be an array",
		`hud.panel("brush", { rows: [["a"]] })`:           "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: [["a", 1]] })`:        "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: ["ab"] })`:            "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: [["a", "b", "c"]] })`: "hud.panel: each row is [string, string]",
		// Holes: Get returns nil for them, which must not reach Export.
		`hud.panel("brush", { rows: [[, "x"]] })`:                          "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: [["x", ,]] })`:                         "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: [new Array(2)] })`:                     "hud.panel: each row is [string, string]",
		`const r = ["a"]; r.length = 2; hud.panel("brush", { rows: [r] })`: "hud.panel: each row is [string, string]",
		`hud.panel("brush", { rows: [, ["a", "b"]] })`:                     "hud.panel: each row is [string, string]",
	} {
		hudThrows(t, h, src, want)
	}
	if got := h.HUDPanels(); len(got) != 4 || got[0].Rows[0][1] != "13" || got[3].Rows[0][0] != "updated" {
		t.Fatalf("a rejected call changed the panels: %+v", got)
	}
	if err := h.Dispose(); err != nil {
		t.Fatal(err)
	}
	if h.HUDPanels() != nil {
		t.Fatal("panels after Dispose")
	}
}

func TestPadLabel(t *testing.T) {
	h, _ := hudHost(t, &fakeHUD{}, `const pad = __padmate.pad;`)
	label := func(src string) string {
		t.Helper()
		v, err := h.VM.RunString(src)
		if err != nil {
			t.Fatal(err)
		}
		return v.String()
	}
	if got := label(`pad.label("south")`); got != "S" {
		t.Errorf("no pad: south = %q, want the generic S", got)
	}
	h.UpdatePad(PadState{Connected: true, Info: input.DeviceInfo{Type: "xboxone"}})
	for src, want := range map[string]string{
		`pad.label("south")`: "A", `pad.label("a")`: "A", `pad.label("north")`: "Y",
		`pad.label("leftshoulder")`: "LB", `pad.label("ls.up")`: "LS", `pad.label("nothing")`: "nothing",
	} {
		if got := label(src); got != want {
			t.Errorf("xbox: %s = %q, want %q", src, got, want)
		}
	}
	h.UpdatePad(PadState{Connected: true, Info: input.DeviceInfo{Type: "ps5", ButtonLabels: input.FaceButtonLabels{South: "Cross", East: "Circle"}}})
	if got := label(`pad.label("south") + pad.label("east")`); got != "×○" {
		t.Errorf("ps5: %q", got)
	}
}

func TestOneLine(t *testing.T) {
	for _, tc := range []struct {
		in    string
		limit int
		want  string
	}{
		{"", 10, ""},
		{"  a \t\n b　c\u0000d  ", 10, "a b c d"},
		{"abcdef", 3, "abc"},
		{"ab cd", 3, "ab"},
		{"画笔大小", 2, "画笔"},
	} {
		if got := oneLine(tc.in, tc.limit); got != tc.want {
			t.Errorf("oneLine(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}
