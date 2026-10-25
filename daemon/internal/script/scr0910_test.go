package script

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/dtsgen"
)

func TestHostHasConfiguredFeatures(t *testing.T) {
	h := NewHostWithOptions(HostOptions{
		Clock:    clock.NewFake(),
		Features: map[Feature]bool{FeatureHUD: true, FeatureGyro: false, Feature("unknown"): true},
	})
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"hud", true}, {"gyro", false}, {"unknown", false},
	} {
		v, err := h.VM.RunString(`__padmate.host.has("` + tc.name + `")`)
		if err != nil {
			t.Fatalf("host.has(%q): %v", tc.name, err)
		}
		if got := v.ToBoolean(); got != tc.want {
			t.Errorf("host.has(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestHostRuntimeSurfaceHasV05Members(t *testing.T) {
	h := NewHostWithOptions(HostOptions{Clock: clock.NewFake()})
	root := h.VM.Get(compile.HostGlobal).ToObject(h.VM)
	for _, name := range []string{"host", "defineScript", "on", "global", "observe", "pad", "key", "now", "sleep", "every", "log"} {
		v := root.Get(name)
		if v == nil || v == goja.Undefined() || v == goja.Null() {
			t.Errorf("runtime surface is missing %q", name)
		}
	}
	for object, names := range map[string][]string{
		"pad": {"info", "has", "isDown", "heldFor", "axis"},
		"key": {"tap", "down", "up", "isHeld", "releaseAll", "parse"},
		"log": {"debug", "info", "warn", "error"},
	} {
		o, ok := root.Get(object).(*goja.Object)
		if !ok {
			t.Fatalf("runtime %s = %T, want object", object, root.Get(object))
		}
		for _, name := range names {
			v := o.Get(name)
			if v == nil || v == goja.Undefined() || v == goja.Null() {
				t.Errorf("runtime %s is missing %q", object, name)
			}
		}
	}
}

func TestHostCurrentSurfaceMatchesFilteredDeclarations(t *testing.T) {
	source, err := os.ReadFile("dtsgen/padmate.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := dtsgen.Filter(source, dtsgen.HostAPI)
	if err != nil {
		t.Fatal(err)
	}
	declarations := stripDeclarationComments(string(filtered))
	declarations = declarationBody(t, declarations, `declare module "padmate" {`)
	h := NewHost(clock.NewFake())
	root := h.VM.Get(compile.HostGlobal).ToObject(h.VM)
	values := regexp.MustCompile(`(?m)^\s*export (?:const|function) (\w+)`).FindAllStringSubmatch(declarations, -1)
	var names []string
	for _, value := range values {
		names = append(names, value[1])
	}
	assertKeys(t, "padmate", root, names)
	for _, name := range []string{"host", "pad", "key", "log", "param", "layers", "app", "profiles", "hud"} {
		want := declarationMembers(t, declarationBody(t, declarations, "export const "+name+": {"))
		object, ok := root.Get(name).(*goja.Object)
		if !ok {
			t.Fatalf("%s is not an object", name)
		}
		assertMemberArity(t, name, object, want)
	}
	// pad.info is declared as DeviceInfo rather than an inline object. SCR-05
	// supplies its runtime snapshot; this check also catches empty placeholders.
	// Its battery is there once the gamepad has read one (SCR-23).
	powered := NewHostWithOptions(HostOptions{Clock: clock.NewFake(), Pad: PadState{
		Connected: true, Info: input.DeviceInfo{Name: "Pad", Type: "xboxone"}, Key: "k",
		Power: input.Power{State: input.PowerDischarging, Percent: 80}, HasPower: true,
	}})
	info := powered.VM.Get(compile.HostGlobal).ToObject(powered.VM).Get("pad").ToObject(powered.VM).Get("info").ToObject(powered.VM)
	assertMemberArity(t, "pad.info", info, declarationMembers(t, declarationBody(t, declarations, "export interface DeviceInfo {")))
	assertMemberArity(t, "app.current", h.appValue(&AppInfo{}).ToObject(h.VM), declarationMembers(t, declarationBody(t, declarations, "export interface AppInfo {")))
	scope := declarationMembers(t, declarationBody(t, declarations, "export interface Scope {"))
	global, ok := goja.AssertFunction(root.Get("global"))
	if !ok {
		t.Fatal("global is not callable")
	}
	globalValue, err := global(goja.Undefined())
	if err != nil {
		t.Fatal(err)
	}
	assertMemberArity(t, "global()", globalValue.ToObject(h.VM), scope)
	eventMembers := declarationMembers(t, declarationBody(t, declarations, "export interface InputEvent {"))
	event := h.event(Event{})
	for name, arity := range eventMembers {
		if event.Get(name) == nil {
			t.Errorf("InputEvent lacks %s", name)
		} else if arity >= 0 {
			assertArity(t, "InputEvent."+name, event.Get(name), arity)
		}
	}
	allEventNames := map[string]bool{}
	for obj := event; obj != nil && obj != h.VM.Get("Object").ToObject(h.VM).Get("prototype"); obj = obj.Prototype() {
		for _, name := range obj.Keys() {
			allEventNames[name] = true
		}
	}
	if len(allEventNames) != len(eventMembers) {
		t.Errorf("InputEvent members = %v; d.ts = %v", allEventNames, eventMembers)
	}
	assertMemberArity(t, "session", root.Get("session").ToObject(h.VM), declarationMembers(t, declarationBody(t, declarations, "export interface KV {")))
	for _, name := range []string{"defineScript", "defineAction", "global", "layer", "observe", "onAxis", "onStick", "onTick", "now", "sleep", "every"} {
		arity := exportedFunctionArity(t, declarations, name)
		assertArity(t, name, root.Get(name), arity)
	}
	assertArity(t, "on", root.Get("on"), scope["on"])
	assertArity(t, "claim", root.Get("claim"), scope["claim"])
	on, _ := goja.AssertFunction(root.Get("on"))
	handler, err := h.VM.RunString(`() => {}`)
	if err != nil {
		t.Fatal(err)
	}
	disposable, err := on(goja.Undefined(), h.VM.ToValue("south"), h.VM.ToValue("press"), handler)
	if err != nil {
		t.Fatal(err)
	}
	assertMemberArity(t, "Disposable", disposable.ToObject(h.VM), declarationMembers(t, declarationBody(t, declarations, "export interface Disposable {")))
}

func stripDeclarationComments(s string) string {
	s = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(s, "")
	return regexp.MustCompile(`(?m)//[^\n]*`).ReplaceAllString(s, "")
}

func declarationBody(t *testing.T, s, prefix string) string {
	t.Helper()
	i := strings.Index(s, prefix)
	if i < 0 {
		t.Fatalf("filtered declarations lack %q", prefix)
	}
	start := i + len(prefix)
	depth := 1
	for j := start; j < len(s); j++ {
		switch s[j] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start:j]
			}
		}
	}
	t.Fatalf("unclosed declaration %q", prefix)
	return ""
}

func declarationMembers(t *testing.T, body string) map[string]int {
	t.Helper()
	want := map[string]int{}
	for _, part := range splitDeclarationParts(body, ';') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name := regexp.MustCompile(`^(?:readonly )?(\w+)`).FindStringSubmatch(part)
		if name == nil {
			t.Fatalf("unparsed declaration member %q", part)
		}
		arity := -1
		if open := strings.Index(part, "("); open >= 0 && strings.Index(part, ":") > open {
			closeIndex := strings.LastIndex(part, ")")
			if closeIndex < open {
				t.Fatalf("unclosed member call %q", part)
			}
			arity = requiredParameterCount(part[open+1 : closeIndex])
		}
		want[name[1]] = arity
	}
	return want
}

func splitDeclarationParts(s string, separator byte) []string {
	var parts []string
	start, braces, parens, brackets, angles := 0, 0, 0, 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			braces++
		case '}':
			braces--
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		case '<':
			angles++
		case '>':
			if i == 0 || s[i-1] != '=' {
				angles--
			}
		}
		if s[i] == separator && braces == 0 && parens == 0 && brackets == 0 && angles == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		parts = append(parts, s[start:])
	}
	return parts
}

func requiredParameterCount(s string) int {
	count := 0
	for _, part := range splitDeclarationParts(s, ',') {
		part = strings.TrimSpace(part)
		if part != "" && !strings.HasPrefix(part, "...") && !strings.Contains(strings.SplitN(part, ":", 2)[0], "?") {
			count++
		}
	}
	return count
}

func exportedFunctionArity(t *testing.T, s, name string) int {
	t.Helper()
	marker := "export function " + name
	i := strings.Index(s, marker)
	if i < 0 {
		t.Fatalf("filtered declarations lack function %s", name)
	}
	rest := s[i+len(marker):]
	open := strings.Index(rest, "(")
	if open < 0 {
		t.Fatalf("function %s lacks parameter list", name)
	}
	depth := 1
	for j := open + 1; j < len(rest); j++ {
		switch rest[j] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return requiredParameterCount(rest[open+1 : j])
			}
		}
	}
	t.Fatalf("unclosed parameter list for %s", name)
	return 0
}

func assertKeys(t *testing.T, path string, object *goja.Object, want []string) {
	t.Helper()
	got := object.Keys()
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s members = %q; filtered d.ts = %q", path, got, want)
	}
}

func assertMemberArity(t *testing.T, path string, object *goja.Object, want map[string]int) {
	t.Helper()
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	assertKeys(t, path, object, names)
	for name, arity := range want {
		if arity >= 0 {
			assertArity(t, path+"."+name, object.Get(name), arity)
		}
	}
}

func assertArity(t *testing.T, path string, value goja.Value, want int) {
	t.Helper()
	if _, ok := goja.AssertFunction(value); !ok {
		t.Errorf("%s is not callable", path)
		return
	}
	if got := int(value.(*goja.Object).Get("length").ToInteger()); got != want {
		t.Errorf("%s.length = %d; filtered d.ts requires %d", path, got, want)
	}
}

func TestHostFeatureOptionsAreCopied(t *testing.T) {
	features := map[Feature]bool{FeatureHUD: true}
	h := NewHostWithOptions(HostOptions{Features: features})
	features[FeatureHUD] = false
	v, err := h.VM.RunString(`__padmate.host.has("hud")`)
	if err != nil || !v.ToBoolean() {
		t.Fatalf("host.has(hud) changed with caller map: %v, %v", v, err)
	}
}

func TestHostRejectsInvalidGesturesAndOptions(t *testing.T) {
	h := NewHost(clock.NewFake())
	for _, expression := range []string{
		`__padmate.on("south", "quintTap", () => {})`,
		`__padmate.on("south", "press", () => {}, {holdMs: -1})`,
		`__padmate.on("south", "press", () => {}, {multiTapMs: Infinity})`,
		`__padmate.on("south", "press", () => {}, {priority: 1.5})`,
		`__padmate.on("south", "press", () => {}, {unknown: 10})`,
	} {
		if _, err := h.VM.RunString(expression); err == nil || !strings.Contains(err.Error(), "TypeError") {
			t.Errorf("%s = %v, want explicit unsupported error", expression, err)
		}
	}
	if len(h.Registrations()) != 0 {
		t.Fatalf("unsupported registrations were retained: %+v", h.Registrations())
	}
}
