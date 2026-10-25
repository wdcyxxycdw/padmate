package haptic

import (
	"slices"
	"testing"
)

func TestPresetNames(t *testing.T) {
	want := []string{"tick", "confirm", "error", "pulse1", "pulse2", "pulse3", "pulse4"}
	if got := PresetNames(); !slices.Equal(got, want) {
		t.Fatalf("PresetNames() = %v, want %v", got, want)
	}
	presets := Presets()
	if len(presets) != len(want) {
		t.Fatalf("Presets() has %d presets, want %d", len(presets), len(want))
	}
	for i, p := range presets {
		if !p.Valid() || p.String() != want[i] {
			t.Errorf("Presets()[%d] = %d (%s), want valid %q", i, p, p, want[i])
		}
		back, ok := ParsePreset(want[i])
		if !ok || back != p {
			t.Errorf("ParsePreset(%q) = %d, %v; want %d", want[i], back, ok, p)
		}
	}
	for _, bad := range []string{"", "Tick", "TICK", "pulse0", "pulse5", "pulse", "none", " tick", "tick ", "buzz"} {
		if p, ok := ParsePreset(bad); ok {
			t.Errorf("ParsePreset(%q) = %v, true; want false", bad, p)
		}
	}
	var zero Preset
	if zero.Valid() || zero.String() != "preset(0)" || Preset(8).Valid() || Preset(8).String() != "preset(8)" {
		t.Errorf("invalid presets: %q valid=%v, %q valid=%v", zero, zero.Valid(), Preset(8), Preset(8).Valid())
	}
}

func TestStepsTable(t *testing.T) {
	const s = 0x6000
	both := func(ms uint16) Step { return Step{Low: s, High: s, Ms: ms} }
	gap := func(ms uint16) Step { return Step{Ms: ms} }
	for _, tc := range []struct {
		name    string
		preset  Preset
		short   int
		gapMs   int
		master  uint16
		want    []Step
		samples string
	}{
		{"tick", Tick, 0, 0, s, []Step{{High: s, Ms: 25}}, "the high motor only"},
		{"tick ignores the pulse times", Tick, 500, 500, s, []Step{{High: s, Ms: 25}}, ""},
		{"confirm", Confirm, 0, 0, s, []Step{both(40), gap(60), both(40)}, "two 40 ms pulses, 60 ms apart"},
		{"error", Error, 0, 0, s, []Step{{Low: s, Ms: 300}}, "the low motor only"},
		{"pulse1", Pulse1, 80, 120, s, []Step{both(80)}, ""},
		{"pulse2", Pulse2, 80, 120, s, []Step{both(80), gap(120), both(80)}, ""},
		{"pulse3 defaults", Pulse3, 0, 0, s, []Step{both(70), gap(100), both(70), gap(100), both(70)}, "profile.Feedback's shortMs and gapMs"},
		{"pulse4", Pulse4, 70, 100, s, []Step{both(70), gap(100), both(70), gap(100), both(70), gap(100), both(70)}, ""},
		{"pulse capped", Pulse1, 99999, 1, s, []Step{both(MaxStepMs)}, "a pulse is at most MaxStepMs"},
		{"master 0 is off", Tick, 0, 0, 0, nil, ""},
		{"master 0 is off for pulses", Pulse2, 70, 100, 0, nil, ""},
		{"invalid", Preset(0), 0, 0, s, nil, ""},
		{"invalid high", Preset(99), 0, 0, s, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.preset.Steps(tc.master, tc.short, tc.gapMs)
			if !slices.Equal(got, tc.want) {
				t.Errorf("%v.Steps(%d, %d, %d) = %+v, want %+v (%s)", tc.preset, tc.master, tc.short, tc.gapMs, got, tc.want, tc.samples)
			}
			for _, st := range got {
				if st.Ms < 1 || st.Ms > MaxStepMs {
					t.Errorf("step %+v is not finite and positive", st)
				}
			}
		})
	}
}

func TestTrain(t *testing.T) {
	// Not positive values are the defaults, as the pulse plan of M3 had them.
	if got, want := Train(0, 0, 0, 0), []Step{{Low: 0x7000, High: 0x7000, Ms: 70}}; !slices.Equal(got, want) {
		t.Errorf("Train(0, 0, 0, 0) = %+v, want %+v", got, want)
	}
	if got, want := Train(-3, -1, -1, 0), Train(1, 70, 100, 0x7000); !slices.Equal(got, want) {
		t.Errorf("negative Train = %+v, want %+v", got, want)
	}
	got := Train(3, 70, 100, 0x7000)
	if len(got) != 5 {
		t.Fatalf("Train(3, ...) has %d steps, want pulse gap pulse gap pulse", len(got))
	}
	for i, st := range got {
		if on := st.Low != 0 || st.High != 0; on != (i%2 == 0) {
			t.Errorf("step %d = %+v: on=%v, want pulses at even steps and no gap at the end", i, st, on)
		}
	}
	if n := len(Train(1000, 1, 1, 1)); n != 2*MaxPulses-1 {
		t.Errorf("Train(1000, ...) has %d steps, want %d (MaxPulses)", n, 2*MaxPulses-1)
	}
	for _, st := range Train(2, 1_000_000, 1_000_000, 1) {
		if st.Ms != MaxStepMs {
			t.Errorf("step %+v: not capped to MaxStepMs", st)
		}
	}
}

func TestParseColor(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want RGB
		ok   bool
	}{
		{"#000000", RGB{}, true},
		{"#FFAA00", RGB{0xFF, 0xAA, 0x00}, true},
		{"#ffaa00", RGB{0xFF, 0xAA, 0x00}, true},
		{"#12345", RGB{}, false},
		{"#1234567", RGB{}, false},
		{"123456", RGB{}, false},
		{"#12345g", RGB{}, false},
		{"", RGB{}, false},
		{"#１２３４５６", RGB{}, false},
	} {
		if got, ok := ParseColor(tc.in); got != tc.want || ok != tc.ok {
			t.Errorf("ParseColor(%q) = %v, %v; want %v, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestBlink(t *testing.T) {
	got := Blink(RGB{1, 2, 3}, 100, 50, 3)
	want := []LEDPhase{
		{Value: RGB{1, 2, 3}, Show: true, Ms: 100}, {Ms: 50},
		{Value: RGB{1, 2, 3}, Show: true, Ms: 100}, {Ms: 50},
		{Value: RGB{1, 2, 3}, Show: true, Ms: 100},
	}
	if !slices.Equal(got, want) {
		t.Errorf("Blink = %+v, want %+v", got, want)
	}
	if n := len(Blink(RGB{}, 1, 1, 0)); n != 1 {
		t.Errorf("Blink with n=0 has %d phases, want 1", n)
	}
}
