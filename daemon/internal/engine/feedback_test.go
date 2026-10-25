package engine_test

import (
	"fmt"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// switchesOff returns the Options.Switches source of the global feedback
// switches (UI-27, ADR-0067) with the named ones off: "rumble", "led", "pause"
// and "lowBattery".
func switchesOff(off ...string) func() profile.FeedbackSwitches {
	sw := profile.DefaultFeedbackSwitches()
	for _, name := range off {
		switch name {
		case "rumble":
			sw.Rumble = false
		case "led":
			sw.LED = false
		case "pause":
			sw.Pause = false
		case "lowBattery":
			sw.LowBattery = false
		default:
			panic("unknown switch " + name)
		}
	}
	return func() profile.FeedbackSwitches { return sw }
}

// feedbackProfile is a profile with the feedback member fb ("" for none), a
// cycle over base, alt and third on leftstick, and a color on every layer.
func feedbackProfile(t testing.TB, fb string) *engine.Program {
	t.Helper()
	member := ""
	if fb != "" {
		member = `"feedback":` + fb + `,`
	}
	return compileLayers(t, `{"schemaVersion":1,"name":"fb",`+member+`"common":{"leftstick":{"press":{"type":"layer","op":"cycle","layers":["base","alt","third"]}}},"layers":{"base":{"color":"#101010"},"alt":{"color":"#112233"},"third":{"color":"#334455"}}}`)
}

// collectFeedback returns the engine with the feedback it publishes, with the
// global switches named off.
func collectFeedback(t testing.TB, prog *engine.Program, off ...string) (*engine.Engine, *recorder, *[]engine.Feedback) {
	t.Helper()
	var got []engine.Feedback
	r := &recorder{}
	e := engine.New(prog, r, engine.Options{
		Feedback: func(f engine.Feedback) { got = append(got, f) },
		Switches: switchesOff(off...),
	})
	return e, r, &got
}

// TestPauseSwitchSuppressesPulseAndColor pins the global pause switch (UI-27,
// ADR-0067): with it off, pausing and resuming the mapping neither pulses nor
// shows the pause color, and the layer color stays; it works through rumble
// and led, so with either of those off the matching part is not shown with it
// on either. The pulses and colors of layer changes do not depend on it.
func TestPauseSwitchSuppressesPulseAndColor(t *testing.T) {
	const layerColor = "#101010" // base
	for _, tc := range []struct {
		name  string
		fb    string   // the profile's feedback
		off   []string // the global switches that are off
		pulse bool     // the pause and the resume pulse
		color string   // the color the pause shows; the resume shows the layer's
	}{
		{"default", ``, nil, true, "#FFAA00"},
		{"pause off", ``, []string{"pause"}, false, layerColor},
		{"pause off, rumble and led off", ``, []string{"pause", "rumble", "led"}, false, ""},
		{"rumble off", ``, []string{"rumble"}, false, "#FFAA00"},
		{"led off", ``, []string{"led"}, true, ""},
		{"pause off, own color", `{"pausedColor":"#AA5500"}`, []string{"pause"}, false, layerColor},
		{"own color", `{"pausedColor":"#AA5500"}`, nil, true, "#AA5500"},
		// The switches a profile's feedback once held are not read.
		{"profile pause false is ignored", `{"pause":false}`, nil, true, "#FFAA00"},
		{"profile rumble and led false are ignored", `{"rumble":false,"led":false}`, nil, true, "#FFAA00"},
		{"profile pause true does not turn the global off", `{"pause":true}`, []string{"pause"}, false, layerColor},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, _, got := collectFeedback(t, feedbackProfile(t, tc.fb), tc.off...)
			e.Pause(1)
			e.Resume()
			if len(*got) != 2 {
				t.Fatalf("%d feedback states, want the pause and the resume: %+v", len(*got), *got)
			}
			pause, resume := (*got)[0], (*got)[1]
			if !pause.Paused || resume.Paused {
				t.Errorf("Paused = %t then %t, want true then false (the state is true whatever the switch says)", pause.Paused, resume.Paused)
			}
			if pause.Pulse != tc.pulse || resume.Pulse != tc.pulse {
				t.Errorf("pulses = %t then %t, want %t", pause.Pulse, resume.Pulse, tc.pulse)
			}
			if tc.pulse && (pause.Pulses != 1 || pause.PulseMs != 210) {
				t.Errorf("the pause pulse = x%d %d ms, want one long pulse", pause.Pulses, pause.PulseMs)
			}
			if pause.Color != tc.color {
				t.Errorf("the pause color = %q, want %q", pause.Color, tc.color)
			}
			wantResume := layerColor
			if tc.color == "" {
				wantResume = ""
			}
			if resume.Color != wantResume {
				t.Errorf("the resume color = %q, want %q", resume.Color, wantResume)
			}
		})
	}
}

// TestPauseSwitchLeavesLayerChangesAlone: the pulses and colors of layer
// changes are the same with the pause indication off, and while the mapping is
// paused the layer color is the one of the layer.
func TestPauseSwitchLeavesLayerChangesAlone(t *testing.T) {
	prog := feedbackProfile(t, ``)
	e, r, got := collectFeedback(t, prog, "pause")
	runLayerScript(t, e, r, "+leftstick@0 -leftstick@1")
	e.Pause(2)
	// A reload while paused starts at the default layer; the mapping stays
	// paused, and shows the color of that layer.
	e.Reload(3, prog)
	e.Resume()
	if len(*got) != 4 {
		t.Fatalf("%d feedback states, want 4: %+v", len(*got), *got)
	}
	layer := (*got)[0]
	if !layer.Pulse || layer.Pulses != 2 || layer.Mode != 2 || layer.Color != "#112233" || layer.Paused {
		t.Errorf("the layer change = %+v, want two pulses, mode 2 and the color of alt", layer)
	}
	paused := (*got)[1]
	if paused.Pulse || paused.Mode != 2 || paused.Color != "#112233" || !paused.Paused {
		t.Errorf("the pause = %+v, want no pulse and the color of alt", paused)
	}
	reload := (*got)[2]
	if reload.Pulse || reload.Mode != 1 || reload.Color != "#101010" || !reload.Paused {
		t.Errorf("the reload while paused = %+v, want the color of base and no pulse", reload)
	}
	resume := (*got)[3]
	if resume.Pulse || resume.Paused || resume.Color != "#101010" {
		t.Errorf("the resume = %+v, want no pulse and the color of base", resume)
	}
}

// TestFeedbackCarriesMode: Feedback.Mode is the number of the mode of the
// current layer, the number of its pulses; pausing does not change it, with
// the pause indication on or off, and LED says whether the global led switch
// is on.
func TestFeedbackCarriesMode(t *testing.T) {
	for _, off := range []string{"", "pause", "led", "rumble"} {
		t.Run(fmt.Sprintf("off=%s", off), func(t *testing.T) {
			var names []string
			if off != "" {
				names = []string{off}
			}
			e, r, got := collectFeedback(t, feedbackProfile(t, ``), names...)
			wantLED := off != "led"
			// leftstick cycles base -> alt -> third -> base.
			runLayerScript(t, e, r, "+leftstick@0 -leftstick@1")   // alt, mode 2
			e.Pause(2)                                             // paused on alt
			e.Resume()                                             // leftstick on alt
			runLayerScript(t, e, r, "+leftstick@10 -leftstick@11") // third, mode 3
			e.Pause(12)
			e.Resume()
			runLayerScript(t, e, r, "+leftstick@20 -leftstick@21") // base, mode 1
			e.RefreshFeedback()
			want := []struct {
				paused bool
				mode   int
			}{
				{false, 2}, {true, 2}, {false, 2}, {false, 3}, {true, 3}, {false, 3}, {false, 1}, {false, 1},
			}
			if len(*got) != len(want) {
				t.Fatalf("%d feedback states, want %d: %+v", len(*got), len(want), *got)
			}
			for i, w := range want {
				f := (*got)[i]
				if f.Paused != w.paused || f.Mode != w.mode {
					t.Errorf("state %d: paused=%t mode=%d, want paused=%t mode=%d", i, f.Paused, f.Mode, w.paused, w.mode)
				}
				if f.LED != wantLED {
					t.Errorf("state %d: LED = %t, want %t", i, f.LED, wantLED)
				}
			}
		})
	}
}

// TestFeedbackModeWithoutACycle: a profile that declares no cycle numbers its
// layers in the sorted order of their names, base being 1 (the way the pulses
// count them).
func TestFeedbackModeWithoutACycle(t *testing.T) {
	prog := compileLayers(t, `{"schemaVersion":1,"name":"fb","layers":{"base":{"north":{"press":{"type":"layer","layer":"zebra","op":"toggle"}},"east":{"press":{"type":"layer","layer":"mid","op":"toggle"}}},"zebra":{"north":{"press":{"type":"layer","layer":"zebra","op":"toggle"}}},"mid":{"east":{"press":{"type":"layer","layer":"mid","op":"toggle"}}}}}`)
	e, r, got := collectFeedback(t, prog)
	runLayerScript(t, e, r, "+north@0 -north@1 +north@2 -north@3 +east@4 -east@5 +east@6 -east@7")
	var modes []int
	for _, f := range *got {
		modes = append(modes, f.Mode)
		if !f.LED {
			t.Errorf("LED is false in %+v", f)
		}
	}
	// zebra on, off, mid on, off: base, mid and zebra in sorted order.
	if want := []int{3, 1, 2, 1}; !slices.Equal(modes, want) {
		t.Errorf("modes = %v, want %v", modes, want)
	}
}

// TestPlayerModeOutsideOneToFour: the engine carries the mode number as it
// is, whatever it is; it is whoever shows it that has four player lights
// (input.Feedback.PlayerMode), so a fifth layer is mode 5 and not clamped to
// 4, or wrapped to 1.
func TestPlayerModeOutsideOneToFour(t *testing.T) {
	layers := []string{"base", "l2", "l3", "l4", "l5", "l6"}
	prog := compileLayers(t, `{"schemaVersion":1,"name":"many","common":{"leftstick":{"press":{"type":"layer","op":"cycle","layers":["base","l2","l3","l4","l5","l6"]}}},"layers":{"base":{},"l2":{},"l3":{},"l4":{},"l5":{},"l6":{}}}`)
	e, r, got := collectFeedback(t, prog)
	script := ""
	for i := range layers {
		script += fmt.Sprintf("+leftstick@%d -leftstick@%d ", 10*i, 10*i+1)
	}
	runLayerScript(t, e, r, script)
	if len(*got) != len(layers) {
		t.Fatalf("%d feedback states, want %d", len(*got), len(layers))
	}
	// The cycle goes base -> l2 ... l6 -> base.
	for i, f := range *got {
		want := (i+1)%len(layers) + 1
		if f.Mode != want || f.Pulses != want || !f.LED {
			t.Errorf("state %d (%s): mode %d, pulses %d, LED %t; want mode %d", i, f.Layer.Base, f.Mode, f.Pulses, f.LED, want)
		}
	}
}

// TestFeedbackModeIsKeptWhilePaused: pause overrides the pulses (one long
// one) and the color, and nothing else that describes the layer.
func TestFeedbackModeIsKeptWhilePaused(t *testing.T) {
	e, r, got := collectFeedback(t, feedbackProfile(t, ``))
	runLayerScript(t, e, r, "+leftstick@0 -leftstick@1 +leftstick@2 -leftstick@3")
	e.Pause(4)
	paused := (*got)[len(*got)-1]
	if paused.Pulses != 1 || paused.PulseMs != 210 || paused.Mode != 3 || paused.Layer.Base != "third" || paused.Color != "#FFAA00" {
		t.Errorf("the pause = %+v: want one long pulse and color, and mode 3 of third", paused)
	}
}

// TestSwitchesAreReadAtEachState: the engine asks Options.Switches each time it
// publishes, so a change of config.json applies to the next state, and
// RefreshFeedback gives the state again with the new switches (ADR-0067).
func TestSwitchesAreReadAtEachState(t *testing.T) {
	sw := profile.DefaultFeedbackSwitches()
	var got []engine.Feedback
	r := &recorder{}
	e := engine.New(feedbackProfile(t, ``), r, engine.Options{
		Feedback: func(f engine.Feedback) { got = append(got, f) },
		Switches: func() profile.FeedbackSwitches { return sw },
	})
	runLayerScript(t, e, r, "+leftstick@0 -leftstick@1")
	if len(got) != 1 || !got[0].Pulse || !got[0].LED || got[0].Color != "#112233" {
		t.Fatalf("with every switch on: %+v; want a pulse and the color of alt", got)
	}
	sw.Rumble, sw.LED = false, false
	e.RefreshFeedback()
	if len(got) != 2 || got[1].Pulse || got[1].LED || got[1].Color != "" || got[1].Mode != 2 {
		t.Fatalf("after rumble and led went off: %+v; want no pulse, no color, and mode 2 as before", got[1:])
	}
	runLayerScript(t, e, r, "+leftstick@10 -leftstick@11")
	if last := got[len(got)-1]; last.Pulse || last.Color != "" || last.Mode != 3 {
		t.Fatalf("a layer change with rumble and led off = %+v; want no pulse and no color", last)
	}
	sw = profile.DefaultFeedbackSwitches()
	e.RefreshFeedback()
	if last := got[len(got)-1]; last.Pulse || !last.LED || last.Color != "#334455" {
		t.Fatalf("after the switches came back = %+v; want the color of third and no pulse (a refresh never pulses)", last)
	}
}
