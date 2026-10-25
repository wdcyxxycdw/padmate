package script

import (
	"strconv"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// What the hud API hands the overlay always fits it, whatever a script
// passes: a call either throws a TypeError or keeps its limits (SCR-22).
func FuzzHUD(f *testing.F) {
	f.Add("brush", `{"title":"画笔","rows":[["大小","12"]]}`, "笔刷 2", 1500.0)
	f.Add(" a\tb ", `{"rows":[["\u0000x\n","  y  "],["a","b"]]}`, "　\n", -1.0)
	f.Add("", `[["a","b"]]`, "", 1e9)
	f.Add("p", `{"rows":"ab"}`, strings.Repeat("长", 300), 0.5)
	f.Add("p", `{"title":1,"rows":[]}`, "x", 12345.6)
	f.Fuzz(func(t *testing.T, id, content, text string, ms float64) {
		hud := &fakeHUD{connected: true}
		h, _ := hudHost(t, hud, `const hud = __padmate.hud;`)
		// The arguments cross as JSON so the script sees exactly the bytes.
		parsed := "(() => { try { return JSON.parse(" + strconv.Quote(content) + "); } catch { return null; } })()"
		// The same content with a hole in each row: JSON has no holes.
		holes := "(() => { const v = " + parsed + "; if (v && Array.isArray(v.rows)) for (const r of v.rows) if (Array.isArray(r)) delete r[0]; return v; })()"
		for _, src := range []string{
			"hud.panel(" + strconv.Quote(id) + ", " + parsed + ")",
			"hud.panel(" + strconv.Quote(id+"3") + ", " + holes + ")",
			"hud.panel(" + strconv.Quote(id+"2") + ", { rows: [[" + strconv.Quote(text) + ", " + strconv.Quote(id) + "]] })",
			"hud.toast(" + strconv.Quote(text) + ", { ms: " + strconv.FormatFloat(ms, 'g', -1, 64) + " })",
		} {
			if !utf8.ValidString(src) {
				continue
			}
			if _, err := h.VM.RunString(src); err != nil && !strings.Contains(err.Error(), "TypeError") {
				t.Fatalf("%s: %v", src, err)
			}
		}
		panels := h.HUDPanels()
		if len(panels) > hudPanels {
			t.Fatalf("%d panels", len(panels))
		}
		for _, p := range panels {
			if n := utf8.RuneCountInString(p.ID); n == 0 || n > hudPanelIDLength || p.ID != strings.TrimSpace(p.ID) {
				t.Fatalf("id %q", p.ID)
			}
			if len(p.Rows) > hudPanelRows {
				t.Fatalf("%d rows", len(p.Rows))
			}
			checkLine(t, p.Title, hudPanelTextLength)
			for _, row := range p.Rows {
				checkLine(t, row[0], hudPanelTextLength)
				checkLine(t, row[1], hudPanelTextLength)
			}
		}
		for _, toast := range hud.toasts {
			checkLine(t, toast.text, hudToastLength)
			if toast.text == "" || toast.ms < hudToastMinMs || toast.ms > hudToastMaxMs {
				t.Fatalf("toast %+v", toast)
			}
		}
	})
}

// checkLine fails unless s is one line of at most limit characters with no
// control characters, no space at either end and no run of spaces, and
// folding it again changes nothing.
func checkLine(t *testing.T, s string, limit int) {
	t.Helper()
	if utf8.RuneCountInString(s) > limit || strings.TrimSpace(s) != s || strings.Contains(s, "  ") || oneLine(s, limit) != s {
		t.Fatalf("line %q", s)
	}
	for _, r := range s {
		if unicode.IsControl(r) || r != ' ' && unicode.IsSpace(r) {
			t.Fatalf("line %q has %U", s, r)
		}
	}
}
