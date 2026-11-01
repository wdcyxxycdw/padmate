package jc2win

import (
	"testing"
	"time"
)

func TestConnParams(t *testing.T) {
	var zero ConnParams
	if zero.Known() || zero.IntervalMs() != 0 {
		t.Errorf("the zero value is %v / %v ms, want unknown", zero.Known(), zero.IntervalMs())
	}
	// Windows reports the interval in units of 1.25 ms, so these are exact.
	for _, tc := range []struct {
		units uint16
		ms    float64
	}{{6, 7.5}, {9, 11.25}, {12, 15}, {48, 60}} {
		p := ConnParams{Interval: time.Duration(tc.units) * 1250 * time.Microsecond, Timeout: 9600 * time.Millisecond}
		if !p.Known() || p.IntervalMs() != tc.ms {
			t.Errorf("%d units: %v ms (known %v), want %v ms", tc.units, p.IntervalMs(), p.Known(), tc.ms)
		}
	}
}

func TestParamsEventKind(t *testing.T) {
	if Params.String() != "params" {
		t.Errorf("Params.String() = %q", Params.String())
	}
	for _, k := range []EventKind{Advert, Connected, Report, Lost, Failed, Params} {
		if s := k.String(); s == "" || s[0] == 'E' {
			t.Errorf("kind %d has the name %q", k, s)
		}
	}
}
