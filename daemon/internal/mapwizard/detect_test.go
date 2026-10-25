package mapwizard

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlmapping"
)

// vectors is testdata/vectors.json, shared with the UI's copy of the rules.
type vectors struct {
	Comment  string `json:"comment"`
	AxisSpec []struct {
		Name  string `json:"name"`
		Index int    `json:"index"`
		Rest  int    `json:"rest"`
		Far   int    `json:"far"`
		Want  string `json:"want"`
	} `json:"axisSpec"`
	Detect []struct {
		Name  string `json:"name"`
		Info  vInfo  `json:"info"`
		Quiet []vEv  `json:"quiet"`
		// Prompt are the events of the prompt; Want has one entry for each.
		Prompt []vEv    `json:"prompt"`
		Want   []string `json:"want"`
	} `json:"detect"`
}

type vInfo struct {
	Axes     int     `json:"axes"`
	Buttons  int     `json:"buttons"`
	Hats     int     `json:"hats"`
	AxisRest []int16 `json:"axisRest"`
}

type vEv struct {
	K string `json:"k"`
	I int    `json:"i"`
	V int    `json:"v"`
	// Wait is the time in milliseconds since the event before it (default 10).
	Wait int64 `json:"waitMs"`
}

func (e vEv) step() int64 {
	if e.Wait == 0 {
		return 10_000_000
	}
	return e.Wait * 1_000_000
}

func (e vEv) event(t int64) Event {
	kind := map[string]Kind{"b": Button, "a": Axis, "h": Hat}[e.K]
	return Event{Kind: kind, Index: e.I, Value: e.V, Time: t}
}

func loadVectors(t *testing.T) vectors {
	t.Helper()
	data, err := os.ReadFile("testdata/vectors.json")
	if err != nil {
		t.Fatal(err)
	}
	var v vectors
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func describe(o Outcome) string {
	switch o.Status {
	case Done:
		return "done:" + o.Input.Value()
	case Rejected:
		return "rejected:" + o.Reason
	}
	return ""
}

func TestAxisSpecVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.AxisSpec) == 0 {
		t.Fatal("no axisSpec vectors")
	}
	for _, c := range v.AxisSpec {
		if got := AxisSpec(c.Index, c.Rest, c.Far); got != c.Want {
			t.Errorf("%s: AxisSpec(%d, %d, %d) = %q, want %q", c.Name, c.Index, c.Rest, c.Far, got, c.Want)
		}
	}
}

func TestDetectVectors(t *testing.T) {
	v := loadVectors(t)
	if len(v.Detect) == 0 {
		t.Fatal("no detect vectors")
	}
	for _, c := range v.Detect {
		t.Run(c.Name, func(t *testing.T) {
			if len(c.Want) != len(c.Prompt) {
				t.Fatalf("%d events but %d wanted outcomes", len(c.Prompt), len(c.Want))
			}
			d := NewDetector(Info{Axes: c.Info.Axes, Buttons: c.Info.Buttons, Hats: c.Info.Hats, AxisRest: c.Info.AxisRest})
			now := int64(0)
			for _, e := range c.Quiet {
				now += e.step()
				d.Observe(e.event(now))
			}
			d.Begin()
			for i, e := range c.Prompt {
				now += e.step()
				if got := describe(d.Feed(e.event(now))); got != c.Want[i] {
					t.Errorf("event %d (%+v): %q, want %q", i, e, got, c.Want[i])
				}
			}
		})
	}
}

func TestInputKeyAndDirection(t *testing.T) {
	up := Input{Kind: Axis, Index: 1, Rest: 0, Far: 30000}
	down := Input{Kind: Axis, Index: 1, Rest: 0, Far: -30000}
	if up.Direction() != 1 || down.Direction() != -1 || up.Key() == down.Key() {
		t.Errorf("up %d %q, down %d %q", up.Direction(), up.Key(), down.Direction(), down.Key())
	}
	if (Input{Kind: Button, Index: 3}).Direction() != 0 || (Input{Kind: Button, Index: 3}).Key() != "b3" || (Input{Kind: Hat, Index: 1, Mask: 4}).Key() != "h1.4" {
		t.Error("button/hat keys")
	}
	if (Input{Kind: Axis}).Direction() != 0 {
		t.Error("an axis without an excursion has no direction")
	}
}

// FuzzDetector feeds random events and checks that
// whatever the detector accepts parses as a binding value (the property the
// mapping depends on).
func FuzzDetector(f *testing.F) {
	f.Add([]byte{1, 3, 1, 1, 3, 0, 2, 0, 200, 2, 0, 0})
	f.Fuzz(func(t *testing.T, data []byte) {
		d := NewDetector(Info{Axes: 4, Buttons: 8, Hats: 2, AxisRest: []int16{0, 0, -32768, 32767}})
		d.Begin()
		now := int64(0)
		for len(data) >= 3 {
			now += 1_000_000
			e := Event{Kind: Kind(data[0]%3 + 1), Index: int(data[1] % 6), Time: now}
			switch e.Kind {
			case Axis:
				e.Value = int(int8(data[2])) * 256
			case Hat:
				e.Value = int(data[2] % 16)
			default:
				e.Value = int(data[2] % 2)
			}
			data = data[3:]
			if o := d.Feed(e); o.Status == Done {
				if _, err := sdlmapping.ParseInput(o.Input.Value()); err != nil {
					t.Fatalf("%+v gave %q: %v", e, o.Input.Value(), err)
				}
				d.Begin()
			}
		}
	})
}
