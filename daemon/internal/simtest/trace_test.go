package simtest_test

import (
	"errors"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

// ms is a Time in (possibly fractional) milliseconds.
func ms(v float64) clock.Time { return clock.Time(math.Round(v * float64(time.Millisecond))) }

func down(name string, t clock.Time) simtest.Edge {
	return simtest.Edge{Name: name, Kind: simtest.Down, T: t}
}

func up(name string, t clock.Time) simtest.Edge {
	return simtest.Edge{Name: name, Kind: simtest.Up, T: t}
}

func value(name string, v float64, t clock.Time) simtest.Edge {
	return simtest.Edge{Name: name, Kind: simtest.Value, Value: v, T: t}
}

var parseTests = []struct {
	in   string
	want []simtest.Edge
	out  string // FormatTrace of want
}{
	{"", nil, ""},
	{" \t\r\n ", nil, ""},
	{"# only a comment", nil, ""},
	{"+south@0 -south@120", []simtest.Edge{down("south", 0), up("south", ms(120))}, "+south@0 -south@120"},
	{"+LCtrl@0 +Z@0 -Z@20 -LCtrl@20",
		[]simtest.Edge{down("LCtrl", 0), down("Z", 0), up("Z", ms(20)), up("LCtrl", ms(20))},
		"+LCtrl@0 +Z@0 -Z@20 -LCtrl@20"},
	{"lefttrigger=0.35@10", []simtest.Edge{value("lefttrigger", 0.35, ms(10))}, "lefttrigger=0.35@10"},
	// Omitted times repeat the previous one; the first is 0.
	{"+a -a@5 +b\tleftx=1", []simtest.Edge{down("a", 0), up("a", ms(5)), down("b", ms(5)), value("leftx", 1, ms(5))},
		"+a@0 -a@5 +b@5 leftx=1@5"},
	// The sign is the first byte; the name runs up to the last "@".
	{"+-@0 -=@5", []simtest.Edge{down("-", 0), up("=", ms(5))}, "+-@0 -=@5"},
	{"++ -- +Key= -a=b", []simtest.Edge{down("+", 0), up("-", 0), down("Key=", 0), up("a=b", 0)}, "++@0 --@0 +Key=@0 -a=b@0"},
	{"+a@b@2 +@@3", []simtest.Edge{down("a@b", ms(2)), down("@", ms(3))}, "+a@b@2 +@@3"},
	{"+sc:0xE01D@1.5 -sc:0xE01D@1.500001", []simtest.Edge{down("sc:0xE01D", ms(1.5)), up("sc:0xE01D", ms(1.500001))},
		"+sc:0xE01D@1.5 -sc:0xE01D@1.500001"},
	// Times: leading zeros, trailing fraction zeros, ns resolution, the maximum.
	{"+a@007 +a@12.500000 +a@13.000001 +a@9223372036854.775807",
		[]simtest.Edge{down("a", ms(7)), down("a", ms(12.5)), down("a", clock.Time(13_000_001)), down("a", math.MaxInt64)},
		"+a@7 +a@12.5 +a@13.000001 +a@9223372036854.775807"},
	// Values: a value edge splits at the first "=".
	{"leftx=-0.5@0.000001 x=.5 x=+1 x=1e-3 x=2E+2 x=-0 a@b=1@2",
		[]simtest.Edge{
			value("leftx", -0.5, 1), value("x", 0.5, 1), value("x", 1, 1), value("x", 0.001, 1),
			value("x", 200, 1), value("x", math.Copysign(0, -1), 1), value("a@b", 1, ms(2)),
		},
		"leftx=-0.5@0.000001 x=0.5@0.000001 x=1@0.000001 x=0.001@0.000001 x=200@0.000001 x=-0@0.000001 a@b=1@2"},
	// Comments run to the end of the line, also mid-token; CRLF is fine.
	{"+a@1 # press\r\n-a@2#release\n# +b@3\n+c#@9\n",
		[]simtest.Edge{down("a", ms(1)), up("a", ms(2)), down("c", ms(2))}, "+a@1 -a@2 +c@2"},
	// Names are not validated.
	{"+é@1 x.y/z=0", []simtest.Edge{down("é", ms(1)), value("x.y/z", 0, ms(1))}, "+é@1 x.y/z=0@1"},
	// The pointer: "move" takes a signed distance, "jump" a position.
	{"move=+8,-3@4 move=0,12@8 jump=32768,0@9 +a@9",
		[]simtest.Edge{move(8, -3, ms(4)), move(0, 12, ms(8)), jump(32768, 0, ms(9)), down("a", ms(9))},
		"move=+8,-3@4 move=+0,+12@8 jump=32768,0@9 +a@9"},
	{"move=-2147483648,2147483647", []simtest.Edge{move(math.MinInt32, math.MaxInt32, 0)}, "move=-2147483648,+2147483647@0"},
}

func move(dx, dy int32, t clock.Time) simtest.Edge {
	return simtest.Edge{Name: "move", Kind: simtest.Move, Dx: dx, Dy: dy, T: t}
}

func jump(x, y int32, t clock.Time) simtest.Edge {
	return simtest.Edge{Name: "jump", Kind: simtest.Jump, Dx: x, Dy: y, T: t}
}

func TestParseTrace(t *testing.T) {
	for _, tt := range parseTests {
		got, err := simtest.ParseTrace(tt.in)
		if err != nil {
			t.Errorf("ParseTrace(%q): %v", tt.in, err)
			continue
		}
		if !slices.Equal(got, tt.want) {
			t.Errorf("ParseTrace(%q) =\n  %v\nwant\n  %v", tt.in, got, tt.want)
		}
		out := simtest.FormatTrace(got)
		if out != tt.out {
			t.Errorf("FormatTrace(ParseTrace(%q)) = %q, want %q", tt.in, out, tt.out)
		}
		back, err := simtest.ParseTrace(out)
		if err != nil || !slices.Equal(back, got) {
			t.Errorf("ParseTrace(%q) = %v, %v; want %v", out, back, err, got)
		}
	}
}

func TestParseTraceErrors(t *testing.T) {
	tests := []struct {
		in    string
		off   int
		token string
		msg   string // substring of the message
	}{
		{"+", 0, "+", "empty name"},
		{"  -@5", 2, "-@5", "empty name"},
		{"=0.5", 0, "=0.5", "empty name"},
		{"=", 0, "=", "empty name"},
		{"south", 0, "south", "want +name, -name or name=value"},
		{"+a@0 south@1", 5, "south@1", "want +name, -name or name=value"},
		{"x=", 0, "x=", "bad value"},
		{"x=@1", 0, "x=@1", "bad value"},
		{"x=abc", 0, "x=abc", "bad value"},
		{"x=0x10", 0, "x=0x10", "bad value"},
		{"x=inf", 0, "x=inf", "bad value"},
		{"x=NaN", 0, "x=NaN", "bad value"},
		{"x=1e999", 0, "x=1e999", "bad value"},
		{"x=1_0", 0, "x=1_0", "bad value"},
		{"move=", 0, "move=", "bad move"},
		{"move=5", 0, "move=5", "bad move"},
		{"move=1,2,3", 0, "move=1,2,3", "bad move"},
		{"move=1.5,2", 0, "move=1.5,2", "bad move"},
		{"move=2147483648,0", 0, "move=2147483648,0", "bad move"},
		{"jump=-1,0", 0, "jump=-1,0", "bad jump"},
		{"jump=3", 0, "jump=3", "bad jump"},
		{"x=1=2", 0, "x=1=2", "bad value"},
		{"x=e5", 0, "x=e5", "bad value"},
		{"+a@", 0, "+a@", "bad time"},
		{"+a@x", 0, "+a@x", "bad time"},
		{"+a@-1", 0, "+a@-1", "bad time"},
		{"+a@+1", 0, "+a@+1", "bad time"},
		{"+a@1.", 0, "+a@1.", "bad time"},
		{"+a@.5", 0, "+a@.5", "bad time"},
		{"+a@1e3", 0, "+a@1e3", "bad time"},
		{"+a@1.2.3", 0, "+a@1.2.3", "bad time"},
		{"+a@1.1234567", 0, "+a@1.1234567", "at most 6 fraction digits"},
		{"+a@9223372036854.775808", 0, "+a@9223372036854.775808", "out of range"},
		{"+a@9223372036855", 0, "+a@9223372036855", "out of range"},
		{"+a@99999999999999999999999", 0, "+a@99999999999999999999999", "out of range"},
		{"+a@10 -a@5", 6, "-a@5", "time goes backwards"},
		{"x=0.5@10\ny=1@9.999999", 9, "y=1@9.999999", "time goes backwards"},
		{"+a@1 # ok\n  -a@0.5#c", 12, "-a@0.5", "time goes backwards"},
	}
	for _, tt := range tests {
		edges, err := simtest.ParseTrace(tt.in)
		if err == nil {
			t.Errorf("ParseTrace(%q) = %v, want an error containing %q", tt.in, edges, tt.msg)
			continue
		}
		var te *simtest.TraceError
		if !errors.As(err, &te) {
			t.Errorf("ParseTrace(%q) error %T, want *simtest.TraceError", tt.in, err)
			continue
		}
		if te.Offset != tt.off || te.Token != tt.token || !strings.Contains(te.Msg, tt.msg) {
			t.Errorf("ParseTrace(%q) error at %d %q: %q; want at %d %q: %q", tt.in, te.Offset, te.Token, te.Msg, tt.off, tt.token, tt.msg)
		}
		if edges != nil {
			t.Errorf("ParseTrace(%q) returned edges %v with its error", tt.in, edges)
		}
	}

	_, err := simtest.ParseTrace("+a@0 +b@0\n  +south@x")
	if want := `trace: offset 12: "+south@x": bad time`; err == nil || err.Error() != want {
		t.Errorf("Error() = %v, want %q", err, want)
	}
}

func TestFormatTrace(t *testing.T) {
	tests := []struct {
		edges []simtest.Edge
		want  string
	}{
		{nil, ""},
		{[]simtest.Edge{}, ""},
		{[]simtest.Edge{down("south", 0), up("south", ms(120))}, "+south@0 -south@120"},
		{[]simtest.Edge{down("a", 1), down("a", 10), down("a", 1_000_000), down("a", 1_234_567), down("a", 60_000_000_000)},
			"+a@0.000001 +a@0.00001 +a@1 +a@1.234567 +a@60000"},
		{[]simtest.Edge{value("lefttrigger", 0.35, ms(10)), value("x", 1e21, ms(10)), value("x", -1.0/3, ms(10))},
			"lefttrigger=0.35@10 x=1e+21@10 x=-0.3333333333333333@10"},
		// Value is ignored for Down and Up.
		{[]simtest.Edge{{Name: "a", Kind: simtest.Down, Value: 5}}, "+a@0"},
		// Edges ParseTrace cannot produce still format, best effort.
		{[]simtest.Edge{down("a", -1_500_000), down("a", math.MinInt64)}, "+a@-1.5 +a@-9223372036854.775808"},
		{[]simtest.Edge{{Name: "a"}}, "?a@0"},
	}
	for _, tt := range tests {
		if got := simtest.FormatTrace(tt.edges); got != tt.want {
			t.Errorf("FormatTrace(%v) = %q, want %q", tt.edges, got, tt.want)
		}
	}
	if got := up("LCtrl", ms(20)).String(); got != "-LCtrl@20" {
		t.Errorf("Edge.String() = %q", got)
	}
	for k, want := range map[simtest.EdgeKind]string{simtest.Down: "down", simtest.Up: "up", simtest.Value: "value", 0: "invalid", 9: "invalid"} {
		if got := k.String(); got != want {
			t.Errorf("EdgeKind(%d).String() = %q, want %q", k, got, want)
		}
	}
}

func TestFormatTraceLines(t *testing.T) {
	tests := []struct {
		edges []simtest.Edge
		want  string
	}{
		{nil, ""},
		{[]simtest.Edge{down("a", 0)}, "+a@0\n"},
		{[]simtest.Edge{down("LCtrl", 0), down("Z", 0), up("Z", ms(20)), up("LCtrl", ms(20)), down("a", ms(20.5))},
			"+LCtrl@0 +Z@0\n-Z@20 -LCtrl@20\n+a@20.5\n"},
	}
	for _, tt := range tests {
		got := simtest.FormatTraceLines(tt.edges)
		if got != tt.want {
			t.Errorf("FormatTraceLines(%v) = %q, want %q", tt.edges, got, tt.want)
		}
		if back, err := simtest.ParseTrace(got); err != nil || !slices.Equal(back, tt.edges) {
			t.Errorf("ParseTrace(%q) = %v, %v; want the edges back", got, back, err)
		}
	}
}

// FuzzParseTrace checks that ParseTrace never panics, that errors point at
// the token in the input, that parsed edges are well formed, that
// FormatTrace output parses back to the same edges, and that it is a fixed
// point.
func FuzzParseTrace(f *testing.F) {
	for _, tt := range parseTests {
		f.Add(tt.in)
	}
	for _, s := range []string{
		"+", "-", "=", "@", "#", "+@", "-@", "x=", "x=@", "+a@", "+a@x", "+a@-1", "+a@1.", "+a@.5",
		"+a@1.1234567", "+a@9223372036854.775808", "+a@10 -a@5", "south", "x=1e999", "x=inf", "x=0x1p-2",
		"+a@1\n#c\n-a@2", "\x00", "+\xff@1", "x==1", "+a@1@2", "+a@@", "x=1@@2", "a=b=c",
		"move=1,2", "move=-1,-2@3", "jump=5,6", "jump=-1,0", "move=1", "move=,", "move=1,2,3",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		edges, err := simtest.ParseTrace(s)
		if err != nil {
			var te *simtest.TraceError
			if !errors.As(err, &te) {
				t.Fatalf("ParseTrace(%q) error %T, want *simtest.TraceError", s, err)
			}
			if te.Token == "" || te.Offset < 0 || te.Offset+len(te.Token) > len(s) || s[te.Offset:te.Offset+len(te.Token)] != te.Token {
				t.Fatalf("ParseTrace(%q) error token %q at %d is not in the input", s, te.Token, te.Offset)
			}
			if te.Msg == "" || !strings.HasPrefix(err.Error(), "trace: offset ") {
				t.Fatalf("ParseTrace(%q) error %q", s, err)
			}
			if edges != nil {
				t.Fatalf("ParseTrace(%q) returned edges with its error", s)
			}
			return
		}
		var prev clock.Time
		for i, e := range edges {
			switch {
			case e.Name == "":
				t.Fatalf("ParseTrace(%q) edge %d has no name", s, i)
			case e.Kind != simtest.Down && e.Kind != simtest.Up && e.Kind != simtest.Value && e.Kind != simtest.Move && e.Kind != simtest.Jump:
				t.Fatalf("ParseTrace(%q) edge %d has kind %v", s, i, e.Kind)
			case e.Kind == simtest.Move && e.Name != "move" || e.Kind == simtest.Jump && (e.Name != "jump" || e.Dx < 0 || e.Dy < 0):
				t.Fatalf("ParseTrace(%q) edge %d is %v named %q at %d,%d", s, i, e.Kind, e.Name, e.Dx, e.Dy)
			case e.Kind != simtest.Move && e.Kind != simtest.Jump && (e.Dx != 0 || e.Dy != 0):
				t.Fatalf("ParseTrace(%q) edge %d is %v with a distance %d,%d", s, i, e.Kind, e.Dx, e.Dy)
			case e.T < prev:
				t.Fatalf("ParseTrace(%q) edge %d goes back to %v from %v", s, i, e.T, prev)
			case e.Kind != simtest.Value && e.Value != 0:
				t.Fatalf("ParseTrace(%q) edge %d is %v with value %v", s, i, e.Kind, e.Value)
			case math.IsNaN(e.Value) || math.IsInf(e.Value, 0):
				t.Fatalf("ParseTrace(%q) edge %d has value %v", s, i, e.Value)
			}
			prev = e.T
		}
		out := simtest.FormatTrace(edges)
		back, err := simtest.ParseTrace(out)
		if err != nil {
			t.Fatalf("ParseTrace(%q) = %q, which does not parse: %v", s, out, err)
		}
		if !slices.Equal(back, edges) {
			t.Fatalf("ParseTrace(%q) = %q, which parses to different edges:\n  %v\n  %v", s, out, edges, back)
		}
		if again := simtest.FormatTrace(back); again != out {
			t.Fatalf("FormatTrace is not a fixed point: %q -> %q", out, again)
		}
	})
}
