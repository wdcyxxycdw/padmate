package profile

import "testing"

func TestFeedbackDefaultsAndOverrides(t *testing.T) {
	defaultProfile, ds := Parse([]byte(`{"schemaVersion":0,"name":"f","layers":{"base":{}}}`))
	if defaultProfile == nil || ds.HasErrors() || defaultProfile.Feedback == nil {
		t.Fatalf("default feedback: profile=%v diagnostics=%v", defaultProfile, ds)
	}
	if got, want := *defaultProfile.Feedback, DefaultFeedback(); got != want {
		t.Fatalf("default feedback = %+v; want %+v", got, want)
	}
	p, ds := Parse([]byte(`{"schemaVersion":0,"name":"f","feedback":{"shortMs":80,"longMs":240,"gapMs":120,"strength":12345,"pausedColor":"#AA5500"},"layers":{"base":{}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("override feedback: profile=%v diagnostics=%v", p, ds)
	}
	want := Feedback{ShortMs: 80, LongMs: 240, GapMs: 120, Strength: 12345, PausedColor: "#AA5500"}
	if got := *p.Feedback; got != want {
		t.Fatalf("override feedback = %+v; want %+v", got, want)
	}
}

func TestFeedbackSchemaRejectsBadValues(t *testing.T) {
	for _, doc := range []string{
		`{"shortMs":0}`, `{"longMs":0}`, `{"gapMs":0}`, `{"strength":65536}`, `{"pausedColor":"orange"}`, `{"unknown":true}`,
		`{"pause":"no"}`, `{"pause":0}`, `{"pause":null}`,
		`{"lowBattery":"no"}`, `{"lowBattery":0}`, `{"lowBattery":null}`,
	} {
		p, ds := Parse([]byte(`{"schemaVersion":0,"name":"f","feedback":` + doc + `,"layers":{"base":{}}}`))
		if p != nil || !ds.HasErrors() {
			t.Errorf("feedback %s: profile=%v diagnostics=%v", doc, p, ds)
		}
	}
}

// TestFeedbackSwitchesAreIgnored pins ADR-0067: the four switches that a
// profile's feedback once held (rumble, led, pause, lowBattery) are global
// settings now. A file that has them stays valid, with no diagnostic at all
// (not even a warning), and parses to the same feedback as a file without
// them.
func TestFeedbackSwitchesAreIgnored(t *testing.T) {
	for _, doc := range []string{
		`{"rumble":false}`, `{"led":false}`, `{"pause":false}`, `{"lowBattery":false}`,
		`{"rumble":false,"led":false,"pause":false,"lowBattery":false}`,
		`{"pause":true,"rumble":true,"led":true,"lowBattery":true}`,
		`{}`,
	} {
		p, ds := Parse([]byte(`{"schemaVersion":1,"name":"f","feedback":` + doc + `,"layers":{"base":{}}}`))
		if p == nil || len(ds) != 0 {
			t.Fatalf("feedback %s: profile=%v diagnostics=%v; want a valid profile with no diagnostics", doc, p, ds)
		}
		if got, want := *p.Feedback, DefaultFeedback(); got != want {
			t.Errorf("feedback %s = %+v; want the defaults %+v", doc, got, want)
		}
	}
	// The members that stay are read next to the ignored ones.
	p, ds := Parse([]byte(`{"schemaVersion":1,"name":"f","feedback":{"rumble":false,"strength":0,"gapMs":5},"layers":{"base":{}}}`))
	if p == nil || ds.HasErrors() {
		t.Fatalf("profile=%v diagnostics=%v", p, ds)
	}
	want := DefaultFeedback()
	want.Strength, want.GapMs = 0, 5
	if got := *p.Feedback; got != want {
		t.Errorf("feedback = %+v; want %+v", got, want)
	}
}
