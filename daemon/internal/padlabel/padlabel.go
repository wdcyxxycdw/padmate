// Package padlabel names a gamepad's controls the way the UI's mapping
// page does: by what is printed on a pad of that family, such as A, ×, LB,
// L2 or ZR, rather than by padmate's position names (south, lt, ...). The
// tray's notices use it (DMN-13). Labels is controllerLabels of
// app/src/renderer/controller-model.ts; TestLabelsMatchUI runs the UI's
// TypeScript and compares the two, so the UI's table stays the source of
// the names both show. The package is pure data with no platform
// dependencies.
package padlabel

import (
	"fmt"
	"maps"
	"regexp"
	"strings"
)

// Pad is what the labels of a gamepad depend on: input.DeviceInfo's
// Type, ButtonLabels and ButtonMask.
type Pad struct {
	// Type is SDL's gamepad type, such as "xboxone", "ps5" or
	// "joyconleft" (input.TypeName).
	Type string
	// Printed are SDL's printed face labels by control: "A", "Cross", ...
	// for south, east, west and north.
	Printed map[string]string
	// ButtonMask is SDL's buttons of the pad, 0 when unknown. A single
	// Joy-Con with a south button is held sideways.
	ButtonMask uint32
}

// printedSymbols are the symbols the UI shows for SDL's PlayStation face
// labels.
var printedSymbols = map[string]string{"Cross": "×", "Circle": "○", "Square": "□", "Triangle": "△"}

// faces are the face buttons, in the order of a family's face labels.
var faces = []string{"south", "east", "west", "north"}

var playstation = regexp.MustCompile(`^ps[345]$`)

// family is familyName of the UI: xbox, playstation, nintendo or generic.
func family(typ string) string {
	switch {
	case strings.HasPrefix(typ, "xbox"):
		return "xbox"
	case playstation.MatchString(typ):
		return "playstation"
	case strings.HasPrefix(typ, "joycon"), typ == "switchpro", typ == "gamecube":
		return "nintendo"
	}
	return "generic"
}

// sideways reports whether p is a single Joy-Con held sideways, as the UI
// tells it: one whose buttons include south.
func sideways(p Pad) bool {
	return (p.Type == "joyconleft" || p.Type == "joyconright") && p.ButtonMask&1 != 0
}

// Labels returns the label of each control of p: what controllerLabels
// of the UI returns, the paddles and misc buttons included (paddleLabels,
// miscLabel).
func Labels(p Pad) map[string]string {
	fam := family(p.Type)
	face := []string{"S", "E", "W", "N"}
	switch {
	case fam == "xbox":
		face = []string{"A", "B", "X", "Y"}
	case fam == "playstation":
		face = []string{"×", "○", "□", "△"}
	case fam == "nintendo" && p.Type != "joyconleft" && p.Type != "joyconright":
		face = []string{"B", "A", "Y", "X"}
	}
	labels := map[string]string{
		"dpad.up": "↑", "dpad.down": "↓", "dpad.left": "←", "dpad.right": "→",
		"leftshoulder": "L", "rightshoulder": "R", "lefttrigger": "LT", "righttrigger": "RT",
		"back": "Back", "start": "Start", "guide": "Home",
		"leftstick": "LS", "rightstick": "RS", "touchpad": "Touch",
	}
	for i, c := range faces {
		labels[c] = face[i]
	}
	switch fam {
	case "playstation":
		back, start := "Create", "Options"
		switch p.Type {
		case "ps3":
			back, start = "Select", "Start"
		case "ps4":
			back = "Share"
		}
		maps.Copy(labels, map[string]string{
			"leftshoulder": "L1", "rightshoulder": "R1", "lefttrigger": "L2", "righttrigger": "R2",
			"leftstick": "L3", "rightstick": "R3", "back": back, "start": start, "guide": "PS",
		})
	case "xbox":
		back, start := "View", "Menu"
		if p.Type == "xbox360" {
			back, start = "Back", "Start"
		}
		maps.Copy(labels, map[string]string{
			"leftshoulder": "LB", "rightshoulder": "RB", "lefttrigger": "LT", "righttrigger": "RT",
			"back": back, "start": start, "guide": "Xbox",
		})
	case "nintendo":
		maps.Copy(labels, map[string]string{
			"leftshoulder": "L", "rightshoulder": "R", "lefttrigger": "ZL", "righttrigger": "ZR",
			"back": "−", "start": "+", "guide": "Home",
		})
	}
	if sideways(p) {
		labels["leftshoulder"], labels["rightshoulder"] = "SL", "SR"
		labels["start"], labels["guide"] = "+", "Home"
		if p.Type == "joyconleft" {
			labels["start"], labels["guide"] = "−", "Capture"
		}
	}
	maps.Copy(labels, paddleLabels(p))
	for i := 1; i <= 6; i++ {
		labels[fmt.Sprintf("misc%d", i)] = miscLabel(fam, i)
	}
	// What SDL says is printed on the pad comes last, as in the UI.
	for c, label := range p.Printed {
		if label != "" {
			labels[c] = label
			if symbol, ok := printedSymbols[label]; ok {
				labels[c] = symbol
			}
		}
	}
	return labels
}

// paddleLabels names the four paddles (ARCHITECTURE §7.1; SDL's
// RIGHT_PADDLE1, LEFT_PADDLE1, RIGHT_PADDLE2, LEFT_PADDLE2): a single
// Joy-Con's are L and ZL, or R and ZR, held sideways, and SL and SR
// upright; a pair of Joy-Cons has the SL and SR of both sides; a
// DualSense Edge has its back buttons and Fn buttons. Other pads number
// them as the Xbox Elite does (P1 to P4): 背键 1 and 2 under the right
// hand, 3 and 4 under the left.
func paddleLabels(p Pad) map[string]string {
	switch {
	case p.Type == "joyconleft" && sideways(p):
		return map[string]string{"paddle2": "L", "paddle4": "ZL"}
	case p.Type == "joyconleft":
		return map[string]string{"paddle2": "SL", "paddle4": "SR"}
	case p.Type == "joyconright" && sideways(p):
		return map[string]string{"paddle1": "R", "paddle3": "ZR"}
	case p.Type == "joyconright":
		return map[string]string{"paddle1": "SR", "paddle3": "SL"}
	case p.Type == "joyconpair":
		return map[string]string{"paddle1": "右 SR", "paddle2": "左 SL", "paddle3": "右 SL", "paddle4": "左 SR"}
	case family(p.Type) == "playstation":
		return map[string]string{"paddle1": "右背键", "paddle2": "左背键", "paddle3": "右 Fn", "paddle4": "左 Fn"}
	}
	return map[string]string{"paddle1": "背键 1", "paddle2": "背键 3", "paddle3": "背键 2", "paddle4": "背键 4"}
}

// miscLabel names misc button i of a pad of family fam: misc1 is the
// Share button of an Xbox Series pad, the mute button of a DualSense and
// the Capture button of a Nintendo pad (SDL_gamepad.h).
func miscLabel(fam string, i int) string {
	if i == 1 {
		switch fam {
		case "xbox":
			return "Share"
		case "playstation":
			return "Mute"
		case "nintendo":
			return "Capture"
		}
	}
	return fmt.Sprintf("附加键 %d", i)
}

// diagonals are the D-pad's diagonals, as the UI's key preview shows them.
var diagonals = map[string]string{"dpad.upleft": "↖", "dpad.upright": "↗", "dpad.downleft": "↙", "dpad.downright": "↘"}

// Name returns what the tray calls control, a binding's control name
// (control.Control.String()), on p: its label (Labels); for a trigger's
// lt, rt, their second stage and half axes the trigger's, and for a stick's
// directions, active control, rings, knob ticks and half axes the stick's, as
// the mapping page has one key for each; an arrow for a D-pad diagonal. An unknown name stays as it is.
func Name(control string, p Pad) string {
	labels := Labels(p)
	if label, ok := labels[control]; ok {
		return label
	}
	if arrow, ok := diagonals[control]; ok {
		return arrow
	}
	switch {
	case control == "lt", control == "lt.full", strings.HasPrefix(control, "lefttrigger"):
		return labels["lefttrigger"]
	case control == "rt", control == "rt.full", strings.HasPrefix(control, "righttrigger"):
		return labels["righttrigger"]
	case strings.HasPrefix(control, "ls."), strings.HasPrefix(control, "leftx"), strings.HasPrefix(control, "lefty"):
		return labels["leftstick"]
	case strings.HasPrefix(control, "rs."), strings.HasPrefix(control, "rightx"), strings.HasPrefix(control, "righty"):
		return labels["rightstick"]
	}
	return control
}
