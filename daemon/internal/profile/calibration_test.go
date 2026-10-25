package profile

import (
	"math"
	"strings"
	"testing"
)

// devKey is a DeviceKey-shaped key for the config tests: ParseConfig does not
// check the syntax of a devices key (profilelib does).
const devKey = "03000000de2800000fff000000000000"

func calibrationConfig(cal string) string {
	return `{"schemaVersion":0,"devices":{"` + devKey + `":{"calibration":` + cal + `}}}`
}

// TestConfigCalibration: a device's calibration (IN-14) holds the stick
// members it sets, and nothing else.
func TestConfigCalibration(t *testing.T) {
	c, ds := ParseConfig([]byte(`{"schemaVersion":0,"devices":{"` + devKey + `":{"ignored":true,"calibration":{"ls":{"deadzone":0.22,"saturation":0.9},"rs":{"deadzone":0.18}}},"other":{"calibration":{"rs":{"saturation":0.8}}},"plain":{"ignored":false}}}`))
	if ds.HasErrors() {
		t.Fatal(ds)
	}
	d := c.Devices[devKey]
	if !d.Ignored || d.Calibration == nil || d.Calibration.LS == nil || d.Calibration.RS == nil {
		t.Fatalf("device: %+v", d)
	}
	if ls := d.Calibration.LS; ls.Deadzone == nil || *ls.Deadzone != 0.22 || ls.Saturation == nil || *ls.Saturation != 0.9 {
		t.Errorf("ls: %+v", ls)
	}
	if rs := d.Calibration.RS; rs.Deadzone == nil || *rs.Deadzone != 0.18 || rs.Saturation != nil {
		t.Errorf("rs: %+v", rs)
	}
	if o := c.Devices["other"].Calibration; o == nil || o.LS != nil || o.RS == nil || o.RS.Deadzone != nil || *o.RS.Saturation != 0.8 {
		t.Errorf("other: %+v", o)
	}
	if c.Devices["plain"].Calibration != nil {
		t.Errorf("plain: %+v", c.Devices["plain"])
	}
	for _, empty := range []string{`{}`, `{"ls":{}}`, `{"ls":{},"rs":{}}`} {
		if _, ds := ParseConfig([]byte(calibrationConfig(empty))); ds.HasErrors() {
			t.Errorf("rejected %s: %v", empty, ds)
		}
	}
	// The edges of the ranges: a deadzone of 0, an outer edge of 1.
	if _, ds := ParseConfig([]byte(calibrationConfig(`{"ls":{"deadzone":0,"saturation":1},"rs":{"deadzone":0.99,"saturation":1}}`))); ds.HasErrors() {
		t.Errorf("rejected the edges: %v", ds)
	}
}

func TestConfigCalibrationRejected(t *testing.T) {
	ptr := "/devices/" + devKey + "/calibration"
	for _, tc := range []struct {
		name, cal, pointer string
	}{
		{"deadzone below 0", `{"ls":{"deadzone":-0.1}}`, ptr + "/ls/deadzone"},
		{"deadzone above 1", `{"rs":{"deadzone":1.1}}`, ptr + "/rs/deadzone"},
		{"saturation below 0", `{"ls":{"saturation":-1}}`, ptr + "/ls/saturation"},
		{"saturation above 1", `{"rs":{"saturation":1.01}}`, ptr + "/rs/saturation"},
		{"not a number", `{"ls":{"deadzone":"0.2"}}`, ptr + "/ls/deadzone"},
		{"null", `{"ls":{"deadzone":null}}`, ptr + "/ls/deadzone"},
		// The error of an unknown member is on the object that holds it.
		{"unknown stick", `{"lt":{"deadzone":0.2}}`, ptr},
		{"unknown member", `{"ls":{"deadzone":0.2,"curve":"cubic"}}`, ptr + "/ls"},
		{"not an object", `{"ls":0.2}`, ptr + "/ls"},
		{"calibration not an object", `[]`, ptr},
		// Both members given: saturation must be greater than deadzone, as in
		// a profile. The error is on the stick, so a repair drops it whole.
		{"saturation equals deadzone", `{"ls":{"deadzone":0.5,"saturation":0.5}}`, ptr + "/ls"},
		{"saturation below deadzone", `{"rs":{"deadzone":0.6,"saturation":0.4}}`, ptr + "/rs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, ds := ParseConfig([]byte(calibrationConfig(tc.cal)))
			if c != nil || !ds.HasErrors() {
				t.Fatalf("accepted: %+v", c)
			}
			found := false
			for _, d := range ds {
				found = found || d.Pointer == tc.pointer
			}
			if !found || ds[0].Line == 0 {
				t.Fatalf("want an error at %s: %v", tc.pointer, ds)
			}
		})
	}
	// A saturation alone is only checked against the profile at run time.
	if _, ds := ParseConfig([]byte(calibrationConfig(`{"ls":{"saturation":0.05},"rs":{"deadzone":0.97}}`))); ds.HasErrors() {
		t.Errorf("a lone member was checked: %v", ds)
	}
}

func f64(v float64) *float64 { return &v }

// TestAnalogCalibrated: a device's values replace the profile's, a left-out
// member keeps the profile's, and a stick whose pair would not hold
// (saturation greater than deadzone) keeps the profile's pair whole.
func TestAnalogCalibrated(t *testing.T) {
	base := DefaultAnalog()
	base.LS.Deadzone, base.LS.Saturation = 0.3, 0.8
	base.RS.Deadzone, base.RS.Saturation = 0.1, 0.9
	for _, tc := range []struct {
		name          string
		cal           *Calibration
		ls, rs        [2]float64
		ignored       [2]bool
		unchangedRest bool
	}{
		{"none", nil, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{}, true},
		{"empty", &Calibration{LS: &StickCalibration{}}, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{}, true},
		{"both", &Calibration{LS: &StickCalibration{Deadzone: f64(0.22), Saturation: f64(0.9)}}, [2]float64{0.22, 0.9}, [2]float64{0.1, 0.9}, [2]bool{}, true},
		{"deadzone only", &Calibration{RS: &StickCalibration{Deadzone: f64(0.05)}}, [2]float64{0.3, 0.8}, [2]float64{0.05, 0.9}, [2]bool{}, true},
		{"saturation only", &Calibration{LS: &StickCalibration{Saturation: f64(0.6)}}, [2]float64{0.3, 0.6}, [2]float64{0.1, 0.9}, [2]bool{}, true},
		// The effective pair must hold: 0.85 is above the profile's saturation.
		{"deadzone over the profile's saturation", &Calibration{LS: &StickCalibration{Deadzone: f64(0.85)}}, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{true, false}, true},
		{"saturation under the profile's deadzone", &Calibration{RS: &StickCalibration{Saturation: f64(0.05)}}, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{false, true}, true},
		{"one stick ignored, the other applied", &Calibration{LS: &StickCalibration{Deadzone: f64(0.9)}, RS: &StickCalibration{Deadzone: f64(0.2)}}, [2]float64{0.3, 0.8}, [2]float64{0.2, 0.9}, [2]bool{true, false}, true},
		{"out of range", &Calibration{LS: &StickCalibration{Deadzone: f64(-0.1), Saturation: f64(0.9)}, RS: &StickCalibration{Deadzone: f64(0.1), Saturation: f64(1.5)}}, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{true, true}, true},
		{"not a number", &Calibration{LS: &StickCalibration{Deadzone: f64(math.NaN())}}, [2]float64{0.3, 0.8}, [2]float64{0.1, 0.9}, [2]bool{true, false}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ignored := base.Calibrated(tc.cal)
			if [2]float64{got.LS.Deadzone, got.LS.Saturation} != tc.ls || [2]float64{got.RS.Deadzone, got.RS.Saturation} != tc.rs || ignored != tc.ignored {
				t.Fatalf("ls %v rs %v ignored %v", [2]float64{got.LS.Deadzone, got.LS.Saturation}, [2]float64{got.RS.Deadzone, got.RS.Saturation}, ignored)
			}
			// Nothing but the two numbers of each stick changes.
			got.LS.Deadzone, got.LS.Saturation, got.RS.Deadzone, got.RS.Saturation = base.LS.Deadzone, base.LS.Saturation, base.RS.Deadzone, base.RS.Saturation
			if got != base {
				t.Errorf("other settings changed: %+v", got)
			}
		})
	}
}

// TestCalibrationIsEmpty: a calibration that sets nothing is the same as none.
func TestCalibrationIsEmpty(t *testing.T) {
	for _, tc := range []struct {
		cal   *Calibration
		empty bool
	}{
		{nil, true}, {&Calibration{}, true}, {&Calibration{LS: &StickCalibration{}, RS: &StickCalibration{}}, true},
		{&Calibration{LS: &StickCalibration{Deadzone: f64(0)}}, false},
		{&Calibration{RS: &StickCalibration{Saturation: f64(1)}}, false},
	} {
		if got := tc.cal.IsEmpty(); got != tc.empty {
			t.Errorf("%+v: empty %v", tc.cal, got)
		}
	}
}

// TestConfigCalibrationDiagnosticsText: the message says what to change.
func TestConfigCalibrationDiagnosticsText(t *testing.T) {
	_, ds := ParseConfig([]byte(calibrationConfig(`{"ls":{"deadzone":0.5,"saturation":0.4}}`)))
	if len(ds) != 1 || !strings.Contains(ds[0].Message, "saturation must be greater than deadzone") {
		t.Fatal(ds)
	}
}
