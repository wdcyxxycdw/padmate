package script

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
	"github.com/wdcyxxycdw/padmate/daemon/internal/padlabel"
)

// The limits of the hud API (SCR-22, API 0.16). The UI cuts toasts to the
// same 200 characters and panel texts to the 40 of its key hints.
const (
	hudToastLength     = 200
	hudToastDefaultMs  = 1500
	hudToastMinMs      = 500
	hudToastMaxMs      = 10000
	hudToastsPerSecond = 10
	hudPanelIDLength   = 32
	hudPanelTextLength = 40
	hudPanelRows       = 16
	hudPanels          = 4
)

// HUD is the overlay a script's hud API reaches (SCR-22): whether one is
// connected and where toasts go. The host calls it on the slot goroutine
// within the script's budget, so neither method may block.
type HUD interface {
	// Connected reports whether an overlay is listening now.
	Connected() bool
	// Toast shows text, already one line and cut, for ms milliseconds;
	// slot is the slot of the runtime that asked.
	Toast(slot uint8, text string, ms int)
}

// HUDPanel is one panel a script keeps on the overlay with hud.panel: an
// optional title and rows of two texts, each already one line and cut.
type HUDPanel struct {
	ID    string
	Title string
	Rows  [][2]string
}

// hudHolder lets Environment keep an interface in an atomic.Pointer.
type hudHolder struct{ hud HUD }

// SetHUD connects the hud API of every host using e to hud; nil
// disconnects it. Hosts read it on each call.
func (e *Environment) SetHUD(hud HUD) {
	if hud == nil {
		e.hud.Store(nil)
		return
	}
	e.hud.Store(&hudHolder{hud: hud})
}

func (e *Environment) loadHUD() HUD {
	if e == nil {
		return nil
	}
	if holder := e.hud.Load(); holder != nil {
		return holder.hud
	}
	return nil
}

// hudConnected is host.has("hud"): an overlay listens now. A describe
// runtime never shows anything, so it has none.
func (h *Host) hudConnected() bool {
	if h.mode == "describe" {
		return false
	}
	hud := h.environment.loadHUD()
	return hud != nil && hud.Connected()
}

// HUDPanels returns a copy of the runtime's panels in the order they were
// first set, nil when it has none or is not the active runtime (paused,
// released, disposed): a panel shows only while its runtime runs. It must
// run on the Host owner goroutine.
func (h *Host) HUDPanels() []HUDPanel {
	if len(h.panels) == 0 || !h.active || h.candidate || h.disposed {
		return nil
	}
	out := make([]HUDPanel, len(h.panels))
	for i, p := range h.panels {
		out[i] = HUDPanel{ID: p.ID, Title: p.Title, Rows: slices.Clone(p.Rows)}
	}
	return out
}

// hudCall says what a hud call does in the runtime's current state: it
// throws in setup, where nothing is on screen yet, and does nothing where
// nothing should show (describe, teardown, a runtime not active).
func (h *Host) hudCall(name string) bool {
	if h.mode == "describe" {
		return false
	}
	if h.setup {
		panic(h.VM.NewTypeError("%s is not available in setup; call it from onActivate or a handler", name))
	}
	return h.active && !h.candidate && !h.disposed
}

func (h *Host) hud() *goja.Object {
	o := h.VM.NewObject()
	setFunction(h.VM, o, "toast", 1, h.hudToast)
	setFunction(h.VM, o, "panel", 2, h.hudPanel)
	return o
}

func (h *Host) hudToast(call goja.FunctionCall) goja.Value {
	text, ok := call.Argument(0).Export().(string)
	if !ok {
		panic(h.VM.NewTypeError("hud.toast: text must be a string"))
	}
	ms := hudToastDefaultMs
	if o := call.Argument(1); !goja.IsUndefined(o) && !goja.IsNull(o) {
		obj, ok := o.(*goja.Object)
		if !ok {
			panic(h.VM.NewTypeError("hud.toast: options must be an object"))
		}
		if v := obj.Get("ms"); v != nil && !goja.IsUndefined(v) {
			n, ok := exportNumber(v)
			if !ok {
				panic(h.VM.NewTypeError("hud.toast: ms must be a finite number"))
			}
			ms = int(min(max(n, hudToastMinMs), hudToastMaxMs) + 0.5)
		}
	}
	if !h.hudCall("hud.toast") {
		return goja.Undefined()
	}
	text = oneLine(text, hudToastLength)
	hud := h.environment.loadHUD()
	if text == "" || hud == nil || !hud.Connected() || !h.allowToast() {
		return goja.Undefined()
	}
	hud.Toast(h.base.owner.Slot, text, ms)
	return goja.Undefined()
}

// allowToast keeps a runtime to hudToastsPerSecond toasts in each second of
// the slot clock, logging one warning for a second that drops any.
func (h *Host) allowToast() bool {
	ok, warn := h.toasts.allow(h.clock.Now(), hudToastsPerSecond)
	if warn {
		h.logs.push(h.logEntry("warn", "", "hud.toast: more than 10 toasts in a second; the rest of this second are dropped"))
	}
	return ok
}

func (h *Host) hudPanel(call goja.FunctionCall) goja.Value {
	raw, ok := call.Argument(0).Export().(string)
	id := strings.TrimSpace(raw)
	if n := len([]rune(id)); !ok || n == 0 || n > hudPanelIDLength {
		panic(h.VM.NewTypeError("hud.panel: id must be a string of 1 to 32 characters"))
	}
	content := call.Argument(1)
	var panel *HUDPanel
	if !goja.IsUndefined(content) && !goja.IsNull(content) {
		panel = h.parsePanel(id, content)
	}
	if !h.hudCall("hud.panel") {
		return goja.Undefined()
	}
	i := slices.IndexFunc(h.panels, func(p HUDPanel) bool { return p.ID == id })
	switch {
	case panel == nil && i >= 0:
		h.panels = slices.Delete(h.panels, i, i+1)
	case panel == nil:
	case i >= 0:
		h.panels[i] = *panel
	case len(h.panels) >= hudPanels:
		panic(h.VM.NewTypeError("hud.panel: at most 4 panels; remove one with hud.panel(id, null)"))
	default:
		h.panels = append(h.panels, *panel)
	}
	return goja.Undefined()
}

// parsePanel reads hud.panel's content. It reads only the first
// hudPanelRows rows, so a long array costs no more than a short one.
func (h *Host) parsePanel(id string, content goja.Value) *HUDPanel {
	obj, ok := content.(*goja.Object)
	if !ok || obj.ClassName() == "Array" {
		panic(h.VM.NewTypeError("hud.panel: content must be an object or null"))
	}
	panel := &HUDPanel{ID: id}
	if title := obj.Get("title"); title != nil && !goja.IsUndefined(title) && !goja.IsNull(title) {
		text, ok := title.Export().(string)
		if !ok {
			panic(h.VM.NewTypeError("hud.panel: title must be a string"))
		}
		panel.Title = oneLine(text, hudPanelTextLength)
	}
	rows, ok := obj.Get("rows").(*goja.Object)
	if !ok || rows.ClassName() != "Array" {
		panic(h.VM.NewTypeError("hud.panel: rows must be an array"))
	}
	n := rows.Get("length").ToInteger()
	for i := int64(0); i < min(n, hudPanelRows); i++ {
		row, ok := rows.Get(itoa(i)).(*goja.Object)
		if !ok || row.ClassName() != "Array" || row.Get("length").ToInteger() != 2 {
			panic(h.VM.NewTypeError("hud.panel: each row is [string, string]"))
		}
		left, lok := stringValue(row.Get("0"))
		right, rok := stringValue(row.Get("1"))
		if !lok || !rok {
			panic(h.VM.NewTypeError("hud.panel: each row is [string, string]"))
		}
		panel.Rows = append(panel.Rows, [2]string{oneLine(left, hudPanelTextLength), oneLine(right, hudPanelTextLength)})
	}
	return panel
}

// padLabel is pad.label: what this pad prints on control c, as the UI's
// mapping page and the tray name it (padlabel). An unknown name comes back
// unchanged.
func (h *Host) padLabel(call goja.FunctionCall) goja.Value {
	name := call.Argument(0).String()
	c, ok := control.Parse(name)
	if !ok {
		return h.VM.ToValue(name)
	}
	pad := padlabel.Pad{}
	if state := h.padState; state.Connected {
		pad = padlabel.Pad{Type: state.Info.Type, ButtonMask: state.Info.ButtonMask, Printed: map[string]string{
			"south": state.Info.ButtonLabels.South, "east": state.Info.ButtonLabels.East,
			"west": state.Info.ButtonLabels.West, "north": state.Info.ButtonLabels.North,
		}}
	}
	return h.VM.ToValue(padlabel.Name(c.String(), pad))
}

// oneLine folds s to one line, as the overlay shows it: control characters
// and runs of white space become one space, the ends are trimmed, and at
// most limit characters (code points) are kept.
func oneLine(s string, limit int) string {
	var b strings.Builder
	space, n := false, 0
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			space = b.Len() > 0
			continue
		}
		if n >= limit {
			break
		}
		if space {
			if n+1 >= limit {
				break
			}
			b.WriteByte(' ')
			n++
			space = false
		}
		b.WriteRune(r)
		n++
	}
	return b.String()
}

// stringValue is v as a string, if it is one. Get returns nil, not
// undefined, for a missing element, such as the hole in [, "x"].
func stringValue(v goja.Value) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.Export().(string)
	return s, ok
}

// exportNumber is v as a finite float64, if it is a number.
func exportNumber(v goja.Value) (float64, bool) {
	switch n := v.Export().(type) {
	case int64:
		return float64(n), true
	case float64:
		return n, n == n && n-n == 0
	}
	return 0, false
}

func itoa(i int64) string { return strconv.FormatInt(i, 10) }
