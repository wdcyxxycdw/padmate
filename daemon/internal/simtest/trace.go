package simtest

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
)

// EdgeKind says what an Edge does to its control or key. The zero value is
// invalid.
type EdgeKind uint8

const (
	Down  EdgeKind = iota + 1 // "+name": pressed
	Up                        // "-name": released
	Value                     // "name=v": moved to Edge.Value
	Move                      // "move=dx,dy": the pointer moved by Edge.Dx, Edge.Dy pixels
	Jump                      // "jump=x,y": the pointer was put at Edge.Dx, Edge.Dy (0 to 65535)
)

func (k EdgeKind) String() string {
	switch k {
	case Down:
		return "down"
	case Up:
		return "up"
	case Value:
		return "value"
	case Move:
		return "move"
	case Jump:
		return "jump"
	}
	return "invalid"
}

// Edge is one token of a trace: a control or key going down, going up or
// taking a value at time T, or the pointer moving or jumping (the name is then
// "move" or "jump").
type Edge struct {
	Name   string
	Kind   EdgeKind
	Value  float64 // for Value; ignored otherwise
	Dx, Dy int32   // for Move: the distance; for Jump: the position; ignored otherwise
	T      clock.Time
}

// String formats e as one trace token, as FormatTrace does.
func (e Edge) String() string { return string(appendEdge(nil, e)) }

// TraceError is a ParseTrace error: Msg describes Token, which starts at
// byte Offset of the input.
type TraceError struct {
	Offset int
	Token  string
	Msg    string
}

func (e *TraceError) Error() string {
	return fmt.Sprintf("trace: offset %d: %q: %s", e.Offset, e.Token, e.Msg)
}

// fracDigits is the number of fraction digits of a time in milliseconds
// that make up whole nanoseconds.
const fracDigits = 6

// ParseTrace parses a whitespace-separated trace such as
// "+south@0 -south@120 lefttrigger=0.35@10". A token is "+name[@t]" (down),
// "-name[@t]" (up) or "name=v[@t]" (value). The sign is the first byte and
// the name runs to the last "@", so "+-@0" presses "-"; a value token splits
// at the first "=", and v is a decimal float, except for the names "move" and
// "jump", whose value is two integers, "move=+8,-3" and "jump=32768,0" (a
// pointer move and a jump, ADR-0062). t is in milliseconds with up to
// six fraction digits; a token without it takes the previous token's time,
// 0 for the first. Times must not decrease. "#" starts a comment that runs
// to the end of the line. Names are not validated. Errors are *TraceError.
func ParseTrace(s string) ([]Edge, error) {
	var edges []Edge
	var prev clock.Time
	for i := 0; i < len(s); {
		switch {
		case s[i] == '#':
			if n := strings.IndexByte(s[i:], '\n'); n >= 0 {
				i += n
			} else {
				i = len(s)
			}
			continue
		case isSpace(s[i]):
			i++
			continue
		}
		j := i
		for j < len(s) && !isSpace(s[j]) && s[j] != '#' {
			j++
		}
		e, msg := parseEdge(s[i:j], prev)
		if msg != "" {
			return nil, &TraceError{Offset: i, Token: s[i:j], Msg: msg}
		}
		edges = append(edges, e)
		prev = e.T
		i = j
	}
	return edges, nil
}

// parseEdge parses one non-empty token whose time defaults to prev. It
// returns an error message instead of an error, for ParseTrace to wrap.
func parseEdge(tok string, prev clock.Time) (Edge, string) {
	e := Edge{T: prev}
	var v, at string
	var hasAt bool
	switch tok[0] {
	case '+', '-':
		e.Kind = Down
		if tok[0] == '-' {
			e.Kind = Up
		}
		e.Name, at, hasAt = cutLast(tok[1:], '@')
	default:
		var ok bool
		e.Name, v, ok = strings.Cut(tok, "=")
		if !ok {
			return e, "want +name, -name or name=value"
		}
		e.Kind = Value
		v, at, hasAt = cutLast(v, '@')
		switch e.Name {
		case "move":
			e.Kind = Move
		case "jump":
			e.Kind = Jump
		}
	}
	if e.Name == "" {
		return e, "empty name"
	}
	if e.Kind == Move || e.Kind == Jump {
		var ok bool
		if e.Dx, e.Dy, ok = parsePair(v); !ok || e.Kind == Jump && (e.Dx < 0 || e.Dy < 0) {
			return e, "bad " + e.Name + ": want " + e.Name + "=dx,dy"
		}
	}
	if e.Kind == Value {
		var ok bool
		if e.Value, ok = parseValue(v); !ok {
			return e, "bad value"
		}
	}
	if hasAt {
		var msg string
		if e.T, msg = parseTime(at); msg != "" {
			return e, msg
		}
		if e.T < prev {
			return e, "time goes backwards from @" + string(prev.AppendMs(nil))
		}
	}
	return e, ""
}

// parsePair parses "dx,dy", two decimal integers with an optional sign.
func parsePair(s string) (dx, dy int32, ok bool) {
	a, b, found := strings.Cut(s, ",")
	if !found {
		return 0, 0, false
	}
	x, errX := strconv.ParseInt(a, 10, 32)
	y, errY := strconv.ParseInt(b, 10, 32)
	if errX != nil || errY != nil {
		return 0, 0, false
	}
	return int32(x), int32(y), true
}

// cutLast splits s around the last sep.
func cutLast(s string, sep byte) (before, after string, found bool) {
	i := strings.LastIndexByte(s, sep)
	if i < 0 {
		return s, "", false
	}
	return s[:i], s[i+1:], true
}

// parseValue parses a finite decimal float: an optional sign, digits with
// an optional point, and an optional exponent. Unlike strconv.ParseFloat it
// rejects hex, underscores, Inf and NaN.
func parseValue(s string) (float64, bool) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	n := digits(s[i:])
	i += n
	if i < len(s) && s[i] == '.' {
		i++
		m := digits(s[i:])
		i += m
		n += m
	}
	if n == 0 {
		return 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		m := digits(s[i:])
		if m == 0 {
			return 0, false
		}
		i += m
	}
	if i != len(s) {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

// parseTime parses a non-negative time in milliseconds with up to six
// fraction digits.
func parseTime(s string) (clock.Time, string) {
	whole, frac, dot := strings.Cut(s, ".")
	if whole == "" || digits(whole) != len(whole) || dot && (frac == "" || digits(frac) != len(frac)) {
		return 0, "bad time"
	}
	if len(frac) > fracDigits {
		return 0, "bad time: at most 6 fraction digits"
	}
	var ns uint64
	for _, c := range []byte(whole + frac + strings.Repeat("0", fracDigits-len(frac))) {
		d := uint64(c - '0')
		if ns > (math.MaxInt64-d)/10 {
			return 0, "bad time: out of range"
		}
		ns = ns*10 + d
	}
	return clock.Time(ns), ""
}

// digits returns the length of the run of ASCII digits that starts s.
func digits(s string) int {
	n := 0
	for n < len(s) && '0' <= s[n] && s[n] <= '9' {
		n++
	}
	return n
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

// FormatTrace writes edges as a trace that ParseTrace reads back to the same
// edges: tokens joined by one space, each with its time written as the
// shortest exact decimal, such as "@0", "@12.5" or "@0.000001". Edges that
// ParseTrace cannot produce (no name, a name with whitespace or "#", an
// invalid kind, a negative time, a value that is not finite) are written as
// well as they can be, for messages.
func FormatTrace(edges []Edge) string {
	var b []byte
	for i, e := range edges {
		if i > 0 {
			b = append(b, ' ')
		}
		b = appendEdge(b, e)
	}
	return string(b)
}

// FormatTraceLines writes edges as FormatTrace does, but with a line for
// each time, each line ending in a newline: the form of a golden trace
// file.
func FormatTraceLines(edges []Edge) string {
	var b []byte
	for i, e := range edges {
		switch {
		case i == 0:
		case e.T == edges[i-1].T:
			b = append(b, ' ')
		default:
			b = append(b, '\n')
		}
		b = appendEdge(b, e)
	}
	if len(edges) > 0 {
		b = append(b, '\n')
	}
	return string(b)
}

func appendEdge(b []byte, e Edge) []byte {
	switch e.Kind {
	case Down:
		b = append(b, '+')
		b = append(b, e.Name...)
	case Up:
		b = append(b, '-')
		b = append(b, e.Name...)
	case Value:
		b = append(b, e.Name...)
		b = append(b, '=')
		b = strconv.AppendFloat(b, e.Value, 'g', -1, 64)
	case Move, Jump:
		b = append(b, e.Name...)
		b = append(b, '=')
		if e.Kind == Move {
			b = append(b, fmt.Sprintf("%+d,%+d", e.Dx, e.Dy)...)
		} else {
			b = append(b, fmt.Sprintf("%d,%d", e.Dx, e.Dy)...)
		}
	default:
		b = append(b, '?')
		b = append(b, e.Name...)
	}
	b = append(b, '@')
	return e.T.AppendMs(b)
}
