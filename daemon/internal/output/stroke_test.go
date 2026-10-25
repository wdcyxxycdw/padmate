package output

import (
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// strokeLines formats the events of st, one String per event.
func strokeLines(st Stroke) string {
	var lines []string
	for _, e := range st.Down {
		lines = append(lines, e.String())
	}
	for _, e := range st.Up {
		lines = append(lines, e.String())
	}
	return strings.Join(lines, "\n")
}

func TestChordStroke(t *testing.T) {
	tests := []struct {
		spec string
		want []string
	}{
		{"Z", []string{"down Z sc=0x2C", "up   Z sc=0x2C"}},
		{"Ctrl+Shift+Z", []string{
			"down Ctrl sc=0x1D", "down Shift sc=0x2A", "down Z sc=0x2C",
			"up   Z sc=0x2C", "up   Shift sc=0x2A", "up   Ctrl sc=0x1D",
		}},
		// Canonical order whatever the spelling.
		{"Win+Alt+Shift+Ctrl+F13", []string{
			"down Ctrl sc=0x1D", "down Shift sc=0x2A", "down Alt sc=0x38", "down Win sc=E0 5B", "down F13 sc=0x64",
			"up   F13 sc=0x64", "up   Win sc=E0 5B", "up   Alt sc=0x38", "up   Shift sc=0x2A", "up   Ctrl sc=0x1D",
		}},
		{"RCtrl+RAlt+Delete", []string{
			"down RCtrl sc=E0 1D", "down RAlt sc=E0 38", "down Delete sc=E0 53",
			"up   Delete sc=E0 53", "up   RAlt sc=E0 38", "up   RCtrl sc=E0 1D",
		}},
		{"F13", []string{"down F13 sc=0x64", "up   F13 sc=0x64"}},
		// Plus is Shift+= on the US layout.
		{"Plus", []string{"down LShift sc=0x2A", "down Plus sc=0x0D", "up   Plus sc=0x0D", "up   LShift sc=0x2A"}},
		{"Ctrl+Plus", []string{
			"down Ctrl sc=0x1D", "down LShift sc=0x2A", "down Plus sc=0x0D",
			"up   Plus sc=0x0D", "up   LShift sc=0x2A", "up   Ctrl sc=0x1D",
		}},
		// A Shift of either side already in the chord is enough.
		{"Shift+Plus", []string{"down Shift sc=0x2A", "down Plus sc=0x0D", "up   Plus sc=0x0D", "up   Shift sc=0x2A"}},
		{"RShift+Plus", []string{"down RShift sc=0x36", "down Plus sc=0x0D", "up   Plus sc=0x0D", "up   RShift sc=0x36"}},
		{"RAlt", []string{"down RAlt sc=E0 38", "up   RAlt sc=E0 38"}},
		{"Pause", []string{"down Pause vk=0x13 sc=0x45", "up   Pause vk=0x13 sc=0x45"}},
		{"Ctrl+Pause", []string{
			"down Ctrl sc=0x1D", "down Pause vk=0x13 sc=0x45",
			"up   Pause vk=0x13 sc=0x45", "up   Ctrl sc=0x1D",
		}},
		{"Num7", []string{"down Num7 vk=0x67 sc=0x47", "up   Num7 vk=0x67 sc=0x47"}},
		{"sc:0xE01D", []string{"down sc:0xE01D sc=E0 1D", "up   sc:0xE01D sc=E0 1D"}},
		// Modifier-only chords press each modifier once.
		{"Ctrl+Shift", []string{"down Ctrl sc=0x1D", "down Shift sc=0x2A", "up   Shift sc=0x2A", "up   Ctrl sc=0x1D"}},
		{"LCtrl+RCtrl", []string{"down LCtrl sc=0x1D", "down RCtrl sc=E0 1D", "up   RCtrl sc=E0 1D", "up   LCtrl sc=0x1D"}},
		{"Shift", []string{"down Shift sc=0x2A", "up   Shift sc=0x2A"}},
		// The same physical key is never pressed twice.
		{"Ctrl+sc:0x1D", []string{"down Ctrl sc=0x1D", "up   Ctrl sc=0x1D"}},
		{"LShift+sc:0x2A", []string{"down LShift sc=0x2A", "up   LShift sc=0x2A"}},
		{"RCtrl+sc:0xE01D", []string{"down RCtrl sc=E0 1D", "up   RCtrl sc=E0 1D"}},
		{"Ctrl+sc:0xE01D", []string{"down Ctrl sc=0x1D", "down sc:0xE01D sc=E0 1D", "up   sc:0xE01D sc=E0 1D", "up   Ctrl sc=0x1D"}},
		// Wheel keys (ADR-0062): a mouse event with no scan code, behind its
		// modifiers; the release of the notch comes first and injects nothing.
		{"WheelUp", []string{"down WheelUp", "up   WheelUp"}},
		{"WheelRight", []string{"down WheelRight", "up   WheelRight"}},
		{"Ctrl+Shift+WheelDown", []string{
			"down Ctrl sc=0x1D", "down Shift sc=0x2A", "down WheelDown",
			"up   WheelDown", "up   Shift sc=0x2A", "up   Ctrl sc=0x1D",
		}},
		{"RAlt+WheelLeft", []string{"down RAlt sc=E0 38", "down WheelLeft", "up   WheelLeft", "up   RAlt sc=E0 38"}},
		// Mouse buttons (ADR-0062): mouse events too, but held like keys: the
		// release is a real event, in reverse order behind the press.
		{"MouseLeft", []string{"down MouseLeft", "up   MouseLeft"}},
		{"LButton", []string{"down MouseLeft", "up   MouseLeft"}},
		{"MouseX2", []string{"down MouseX2", "up   MouseX2"}},
		{"Ctrl+Shift+MouseRight", []string{
			"down Ctrl sc=0x1D", "down Shift sc=0x2A", "down MouseRight",
			"up   MouseRight", "up   Shift sc=0x2A", "up   Ctrl sc=0x1D",
		}},
		{"RAlt+MButton", []string{"down RAlt sc=E0 38", "down MouseMiddle", "up   MouseMiddle", "up   RAlt sc=E0 38"}},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			c, err := keys.Parse(tt.spec)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			st, err := ChordStroke(c)
			if err != nil {
				t.Fatalf("ChordStroke: %v", err)
			}
			if got, want := strokeLines(st), strings.Join(tt.want, "\n"); got != want {
				t.Errorf("ChordStroke(%s):\n%s\nwant:\n%s", tt.spec, got, want)
			}
			checkStroke(t, st)
		})
	}
}

// checkStroke checks the Stroke invariants: presses are all downs, every
// press has its release in exact reverse order, and no key is pressed
// twice.
func checkStroke(t *testing.T, st Stroke) {
	t.Helper()
	if len(st.Down) != len(st.Up) {
		t.Fatalf("%d presses, %d releases", len(st.Down), len(st.Up))
	}
	for i, d := range st.Down {
		u := st.Up[len(st.Up)-1-i]
		if d.Up || !u.Up {
			t.Errorf("event %d: down.Up=%v up.Up=%v", i, d.Up, u.Up)
		}
		if d.release() != u {
			t.Errorf("release of %v is %v", d, u)
		}
		for _, o := range st.Down[:i] {
			if o.sameKey(d) {
				t.Errorf("%v pressed twice", d)
			}
		}
	}
}

// Every key of the table, alone and under every modifier, gives a well
// formed stroke.
func TestChordStrokeTable(t *testing.T) {
	mods := []string{"", "Ctrl+", "Ctrl+Shift+", "RAlt+", "Win+Alt+", "LShift+RShift+"}
	for _, k := range keys.Table() {
		for _, m := range mods {
			spec := m + k.String()
			c, err := keys.Parse(spec)
			if err != nil {
				continue // for example "Ctrl+Ctrl"
			}
			st, err := ChordStroke(c)
			if err != nil {
				t.Errorf("%s: %v", spec, err)
				continue
			}
			checkStroke(t, st)
			if last := st.Down[len(st.Down)-1]; !k.IsModifier() && last.Key != k {
				t.Errorf("%s: last press is %v, want the main key", spec, last)
			}
		}
	}
}

func TestChordStrokeInvalid(t *testing.T) {
	if _, err := ChordStroke(keys.Chord{}); err == nil {
		t.Fatal("ChordStroke(zero Chord) succeeded")
	}
}
