package padlabel

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// uiLabels is the UI's own table: controllerLabels of the mapping page.
const uiLabels = "../../../app/src/renderer/controller-model.ts"

// labelDriver prints controllerLabels of each case it gets, or exits 3
// when node cannot run TypeScript.
const labelDriver = `const [, , module, cases] = process.argv;
if (!process.features?.typescript) process.exit(3);
const { controllerLabels } = await import(module);
process.stdout.write(JSON.stringify(JSON.parse(cases).map((c) => controllerLabels(c.type, c.printed, c.mask))));
`

// labelCases are the pads TestLabelsMatchUI asks both tables about: every
// SDL gamepad type and one SDL does not define, with and without printed
// labels, and single Joy-Cons held either way.
func labelCases() []Pad {
	types := []string{"unknown", "standard", "xbox360", "xboxone", "ps3", "ps4", "ps5", "switchpro", "joyconleft", "joyconright", "joyconpair", "gamecube", "type99"}
	printed := []map[string]string{
		{},
		{"south": "A", "east": "B", "west": "X", "north": "Y"},
		{"south": "Cross", "east": "Circle", "west": "Square", "north": "Triangle"},
		{"south": "B", "east": "A"},
	}
	var cases []Pad
	for _, typ := range types {
		for _, labels := range printed {
			for _, mask := range []uint32{0, 1, 0x7fff &^ 1} {
				cases = append(cases, Pad{Type: typ, Printed: labels, ButtonMask: mask})
			}
		}
	}
	return cases
}

// TestLabelsMatchUI keeps one source of truth for the names (the owner's
// G11): every label the mapping page gives a control, this package gives
// too, for every pad. It runs the UI's TypeScript with node, which needs
// type stripping (Node 22.18 or later); without it, it skips.
func TestLabelsMatchUI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found")
	}
	module, err := filepath.Abs(uiLabels)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(module); err != nil {
		t.Skip("the UI's sources are not here:", err)
	}
	driver := filepath.Join(t.TempDir(), "labels.mjs")
	if err := os.WriteFile(driver, []byte(labelDriver), 0o600); err != nil {
		t.Fatal(err)
	}
	type wire struct {
		Type    string            `json:"type"`
		Printed map[string]string `json:"printed"`
		Mask    uint32            `json:"mask"`
	}
	cases := labelCases()
	in := make([]wire, len(cases))
	for i, c := range cases {
		in[i] = wire{c.Type, c.Printed, c.ButtonMask}
	}
	arg, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(node, driver, (&url.URL{Scheme: "file", Path: "/" + filepath.ToSlash(module)}).String(), string(arg))
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if exit, ok := errors.AsType[*exec.ExitError](err); ok && exit.ExitCode() == 3 {
			t.Skip("node cannot run TypeScript")
		}
		t.Fatalf("node: %v\n%s", err, &stderr)
	}
	var ui []map[string]string
	if err := json.Unmarshal(stdout.Bytes(), &ui); err != nil || len(ui) != len(cases) {
		t.Fatalf("node printed %q: %v", &stdout, err)
	}
	for i, c := range cases {
		got := Labels(c)
		for control, want := range ui[i] {
			if got[control] != want {
				t.Errorf("%+v: %s is %q, the UI says %q", c, control, got[control], want)
			}
		}
	}
}

// TestName: what the tray calls a binding's control (DMN-13): the label
// of the control on the mapping page, paddles and misc buttons included,
// and a stick or trigger for its directions and half axes.
func TestName(t *testing.T) {
	xbox := Pad{Type: "xboxone", ButtonMask: 0x7fff}
	ps := Pad{Type: "ps5", Printed: map[string]string{"south": "Cross"}}
	left := Pad{Type: "joyconleft", ButtonMask: 1}
	vertical := Pad{Type: "joyconleft", ButtonMask: 2}
	for _, tc := range []struct {
		control string
		pad     Pad
		want    string
	}{
		{"south", xbox, "A"}, {"south", ps, "×"}, {"east", Pad{Type: "switchpro"}, "A"}, {"south", Pad{}, "S"},
		{"lt", xbox, "LT"}, {"lt", ps, "L2"}, {"rt", Pad{Type: "switchpro"}, "ZR"}, {"lefttrigger+", xbox, "LT"}, {"righttrigger-", ps, "R2"},
		{"leftshoulder", xbox, "LB"}, {"leftshoulder", left, "SL"}, {"leftshoulder", vertical, "L"},
		{"back", xbox, "View"}, {"back", Pad{Type: "xbox360"}, "Back"}, {"guide", ps, "PS"}, {"touchpad", ps, "Touch"},
		{"ls.up", xbox, "LS"}, {"ls.active", ps, "L3"}, {"rightx+", xbox, "RS"}, {"lefty-", Pad{}, "LS"}, {"leftstick", xbox, "LS"},
		{"ls.inner", xbox, "LS"}, {"ls.outer", ps, "L3"}, {"rs.cw", xbox, "RS"}, {"rs.ccw", ps, "R3"},
		{"lt.full", xbox, "LT"}, {"lt.full", ps, "L2"}, {"rt.full", Pad{Type: "switchpro"}, "ZR"},
		{"dpad.up", xbox, "↑"}, {"dpad.downleft", xbox, "↙"},
		{"paddle1", xbox, "背键 1"}, {"paddle3", xbox, "背键 2"}, {"paddle2", xbox, "背键 3"}, {"paddle4", Pad{}, "背键 4"},
		{"paddle1", ps, "右背键"}, {"paddle4", ps, "左 Fn"},
		{"paddle2", left, "L"}, {"paddle4", left, "ZL"}, {"paddle2", vertical, "SL"}, {"paddle4", vertical, "SR"},
		{"paddle1", Pad{Type: "joyconright", ButtonMask: 2}, "SR"}, {"paddle3", Pad{Type: "joyconright", ButtonMask: 1}, "ZR"},
		{"paddle2", Pad{Type: "joyconpair"}, "左 SL"}, {"paddle1", Pad{Type: "joyconpair"}, "右 SR"},
		{"misc1", xbox, "Share"}, {"misc1", ps, "Mute"}, {"misc1", Pad{Type: "switchpro"}, "Capture"}, {"misc1", Pad{}, "附加键 1"}, {"misc6", xbox, "附加键 6"},
		{"bogus", xbox, "bogus"},
	} {
		if got := Name(tc.control, tc.pad); got != tc.want {
			t.Errorf("Name(%s, %+v) = %q, want %q", tc.control, tc.pad, got, tc.want)
		}
	}
}
