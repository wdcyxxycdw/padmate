package amgp

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// refusal imports data and returns the reason it is refused, or "".
func refusal(t *testing.T, data []byte) string {
	t.Helper()
	out, report, err := Import(data, Options{})
	if err == nil {
		if out == nil || report == nil {
			t.Fatal("an import with no error returns a profile and a report")
		}
		return ""
	}
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("error %v is not an *Error", err)
	}
	if out != nil || report != nil {
		t.Error("a refused file returns neither a profile nor a report")
	}
	if e.Message == "" {
		t.Error("a refusal says why")
	}
	return e.Reason
}

const minimal = `<gamecontroller configversion="19"><sets><set index="1"><button index="1"><slots><slot><code>0x41</code><mode>keyboard</mode></slot></slots></button></set></sets></gamecontroller>`

func TestRefusals(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"valid", []byte(minimal), ""},
		{"byte order mark", append([]byte{0xEF, 0xBB, 0xBF}, minimal...), ""},
		{"declared utf-8", []byte(`<?xml version="1.0" encoding="UTF-8"?>` + minimal), ""},
		{"utf-16 mark", []byte{0xFF, 0xFE, '<', 0, 'a', 0, '/', 0, '>', 0}, ReasonEncoding},
		{"utf-16 mark, big endian", []byte{0xFE, 0xFF, 0, '<', 0, 'a'}, ReasonEncoding},
		{"declared latin-1", []byte(`<?xml version="1.0" encoding="ISO-8859-1"?>` + minimal), ReasonEncoding},
		{"invalid utf-8", []byte("<gamecontroller configversion=\"19\"><sdlname>\xff\xfe</sdlname></gamecontroller>"), ReasonXML},
		{"empty", nil, ReasonXML},
		{"only a comment", []byte("<!-- nothing -->"), ReasonXML},
		{"two roots", []byte(minimal + minimal), ReasonXML},
		{"truncated", []byte(minimal[:len(minimal)-30]), ReasonXML},
		{"unknown entity", []byte(`<gamecontroller configversion="19"><sdlname>&nope;</sdlname></gamecontroller>`), ReasonXML},
		{"known entity", []byte(`<gamecontroller configversion="19"><sdlname>a &amp; b &#65;</sdlname></gamecontroller>`), ""},
		{"doctype", []byte(`<!DOCTYPE gamecontroller><gamecontroller configversion="19"/>`), ReasonDoctype},
		{"entity declaration", []byte(`<!DOCTYPE g [<!ENTITY a "aaaa"><!ENTITY b "&a;&a;&a;&a;">]><gamecontroller configversion="19">&b;</gamecontroller>`), ReasonDoctype},
		{"external entity", []byte(`<!DOCTYPE g [<!ENTITY x SYSTEM "file:///etc/passwd">]><gamecontroller configversion="19">&x;</gamecontroller>`), ReasonDoctype},
		{"cdata is text", []byte(`<gamecontroller configversion="19"><sdlname><![CDATA[a<b]]></sdlname></gamecontroller>`), ""},
		{"joystick", []byte(`<joystick configversion="19"><sets/></joystick>`), ReasonJoystick},
		{"other root", []byte(`<profile/>`), ReasonRoot},
		{"version 5", []byte(`<gamecontroller configversion="5"/>`), ReasonVersion},
		{"version 2", []byte(`<gamecontroller configversion="2"/>`), ReasonVersion},
		{"version 6", []byte(`<gamecontroller configversion="6"/>`), ""},
		{"version 0", []byte(`<gamecontroller configversion="0"/>`), ReasonVersion},
		{"version missing", []byte(`<gamecontroller/>`), ""},
		{"version not a number", []byte(`<gamecontroller configversion="new"/>`), ""},
		{"version from the future", []byte(`<gamecontroller configversion="99"/>`), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := refusal(t, tc.data); got != tc.want {
				t.Errorf("got reason %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLimits(t *testing.T) {
	t.Run("size", func(t *testing.T) {
		pad := strings.Repeat(" ", MaxBytes)
		if got := refusal(t, []byte(minimal+pad)); got != ReasonSize {
			t.Errorf("a file of %d bytes: got %q", MaxBytes+len(minimal), got)
		}
		// Exactly at the limit is read.
		ok := minimal + strings.Repeat(" ", MaxBytes-len(minimal))
		if got := refusal(t, []byte(ok)); got != "" {
			t.Errorf("a file of %d bytes: got %q", len(ok), got)
		}
	})
	t.Run("depth", func(t *testing.T) {
		deep := func(n int) []byte {
			return []byte(`<gamecontroller configversion="19">` + strings.Repeat("<a>", n) + strings.Repeat("</a>", n) + `</gamecontroller>`)
		}
		if got := refusal(t, deep(maxDepth-1)); got != "" {
			t.Errorf("depth %d: got %q", maxDepth, got)
		}
		if got := refusal(t, deep(maxDepth)); got != ReasonLimit {
			t.Errorf("depth %d: got %q", maxDepth+1, got)
		}
		// A very deep file must not exhaust the stack.
		if got := refusal(t, deep(200_000)); got != ReasonLimit && got != ReasonSize {
			t.Errorf("a file of 200000 levels: got %q", got)
		}
	})
	t.Run("elements", func(t *testing.T) {
		many := func(n int) []byte {
			return []byte(`<gamecontroller configversion="19">` + strings.Repeat("<a/>", n) + `</gamecontroller>`)
		}
		if got := refusal(t, many(maxElements-1)); got != "" {
			t.Errorf("%d elements: got %q", maxElements, got)
		}
		if got := refusal(t, many(maxElements)); got != ReasonLimit {
			t.Errorf("%d elements: got %q", maxElements+1, got)
		}
	})
}

// A file full of things that cannot be imported makes a report that is cut
// short and says so.
func TestReportIsBounded(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(`<gamecontroller configversion="19"><sets><set index="1">`)
	for i := 1; i <= 21; i++ {
		fmt.Fprintf(&b, `<button index="%d"><slots>`, i)
		for j := 0; j < maxSlots; j++ {
			b.WriteString(`<slot><code>0x1234567</code><mode>keyboard</mode></slot>`)
		}
		b.WriteString(`</slots></button>`)
	}
	b.WriteString(`</set></sets></gamecontroller>`)
	if b.Len() > MaxBytes {
		t.Fatalf("the test file is %d bytes", b.Len())
	}
	_, report, err := Import(b.Bytes(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(report) != maxReportLines+1 {
		t.Fatalf("report has %d lines, want %d", len(report), maxReportLines+1)
	}
	if last := report[len(report)-1]; last.Code != "report.omitted" || last.Severity != SeverityInfo {
		t.Errorf("last line is %+v", last)
	}
}

// A button keeps at most maxSlots slots; the rest are reported.
func TestSlotLimit(t *testing.T) {
	var b bytes.Buffer
	b.WriteString(`<gamecontroller configversion="19"><sets><set index="1"><button index="1"><slots>`)
	for j := 0; j < maxSlots+10; j++ {
		b.WriteString(`<slot><code>0x41</code><mode>keyboard</mode></slot>`)
	}
	b.WriteString(`</slots></button></set></sets></gamecontroller>`)
	_, report, err := Import(b.Bytes(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range report {
		found = found || l.Code == "slot.limit"
	}
	if !found {
		t.Errorf("no slot.limit line in %v", report)
	}
}

func TestParseNumber(t *testing.T) {
	tests := []struct {
		in   string
		want int
		ok   bool
	}{
		{"10", 10, true}, {"0x57", 0x57, true}, {"0X57", 0x57, true}, {"0x1000021", 0x1000021, true},
		{"010", 8, true}, {"0", 0, true}, {"-5", -5, true}, {"+7", 7, true},
		{"", 0, false}, {"abc", 0, false}, {"1_000", 0, false}, {"1.5", 0, false}, {"0x", 0, false},
		{"2147483647", 1<<31 - 1, true}, {"2147483648", 0, false}, {"-2147483648", -1 << 31, true},
		{"0xffffffff", 0, false}, {"99999999999999999999", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseNumber(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseNumber(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestCleanText(t *testing.T) {
	tests := []struct {
		in    string
		limit int
		want  string
	}{
		{"  a  b ", 10, "a b"},
		{"a\tb\nc", 10, "a b c"},
		{"abcdef", 3, "abc"},
		{"日本語のテキスト", 3, "日本語"},
		{"a\u200bb", 10, "a b"},
		{"\x00\x01", 10, ""},
		{"", 10, ""},
	}
	for _, tc := range tests {
		if got := cleanText(tc.in, tc.limit); got != tc.want {
			t.Errorf("cleanText(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
		}
	}
}

func TestFitText(t *testing.T) {
	if got, note := fitText("a\r\nb\rc\td"); got != "a\nb\nc\td" || note != "" {
		t.Errorf("got %q, %q", got, note)
	}
	if got, note := fitText("a\x01b\x7fc"); got != "abc" || note == "" {
		t.Errorf("got %q, %q", got, note)
	}
	long := strings.Repeat("字", 300)
	if got, note := fitText(long); len([]rune(got)) != 256 || note == "" {
		t.Errorf("got %d characters, note %q", len([]rune(got)), note)
	}
	// A character outside the Basic Multilingual Plane is two units.
	if got, _ := fitText(strings.Repeat("\U0001F600", 200)); len([]rune(got)) != 128 {
		t.Errorf("got %d emoji", len([]rune(got)))
	}
}

func TestFileBase(t *testing.T) {
	tests := map[string]string{
		"hybrid.gamecontroller.amgp":  "hybrid",
		"C:\\Users\\me\\My Pad.amgp":  "My Pad",
		"/home/me/pad.xml":            "pad",
		"foo.joystick.amgp":           "foo",
		"plain":                       "plain",
		".amgp":                       ".amgp",
		"Profile.GameController.AMGP": "Profile",
		"a.b.amgp":                    "a.b",
		"":                            "",
	}
	for in, want := range tests {
		if got := fileBase(in); got != want {
			t.Errorf("fileBase(%q) = %q, want %q", in, got, want)
		}
	}
}
