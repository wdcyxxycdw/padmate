package keys_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

func TestValidateText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string // substring of the error; "" for a valid text
	}{
		{"one letter", "a", ""},
		{"words", "Hello, world", ""},
		{"CJK", "你好，世界", ""},
		{"newline and tab", "a\nb\tc", ""},
		{"only a newline", "\n", ""},
		{"only a tab", "\t", ""},
		{"surrogate pair", "😀", ""},
		{"combining mark", "é", ""},
		{"zero width joiner sequence", "👨‍👩‍👧", ""},
		{"line separator", "a b", ""},
		{"noncharacter", "￿", ""},
		{"empty", "", "empty"},
		{"NUL", "a\x00b", "control character U+0000"},
		{"bell", "\a", "control character U+0007"},
		{"carriage return", "a\r\nb", "control character U+000D"},
		{"escape", "\x1b[0m", "control character U+001B"},
		{"DEL", "a\x7f", "control character U+007F"},
		{"C1 control", "a\u0085", "control character U+0085"},
		{"invalid UTF-8", "a\xffb", "UTF-8"},
		{"encoded surrogate", "\xed\xa0\x80", "UTF-8"},
		{"256 ASCII units", strings.Repeat("a", 256), ""},
		{"257 ASCII units", strings.Repeat("a", 257), "too long"},
		{"256 units as surrogate pairs", strings.Repeat("😀", 128), ""},
		{"258 units as surrogate pairs", strings.Repeat("😀", 129), "too long"},
		{"255 units and a pair", strings.Repeat("a", 255) + "😀", "too long"},
		{"254 units and a pair", strings.Repeat("a", 254) + "😀", ""},
		{"256 CJK characters", strings.Repeat("字", 256), ""},
		{"257 CJK characters", strings.Repeat("字", 257), "too long"},
		{"256 newlines", strings.Repeat("\n", 256), ""},
		{"257 tabs", strings.Repeat("\t", 257), "too long"},
	}
	for _, tt := range tests {
		err := keys.ValidateText(tt.text)
		switch {
		case tt.want == "" && err != nil:
			t.Errorf("%s: ValidateText = %v, want nil", tt.name, err)
		case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
			t.Errorf("%s: ValidateText = %v, want an error with %q", tt.name, err, tt.want)
		}
	}
}

// An invalid text reports where the first problem is, in bytes, as KeySpec
// parsing does, so that a caller can point at it.
func TestValidateTextError(t *testing.T) {
	const text = "ab\x07cd"
	err := keys.ValidateText(text)
	var te *keys.TextError
	if !errors.As(err, &te) {
		t.Fatalf("ValidateText(%q) = %v (%T), want a *keys.TextError", text, err, err)
	}
	if te.Offset != 2 || text[te.Offset] != '\a' || te.Msg == "" {
		t.Errorf("error at %d (%q): %+v, want the bell at offset 2", te.Offset, text[te.Offset:], te)
	}
	// The message names a control character in a form that can be read.
	if strings.ContainsRune(te.Error(), '\a') {
		t.Errorf("the message %q holds the control character itself", te.Error())
	}
	// A text that is too long points at the character that goes over the limit:
	// the second of the CJK characters here is the 257th unit, at byte 258.
	err = keys.ValidateText(strings.Repeat("a", 255) + "你好")
	if !errors.As(err, &te) || te.Offset != 258 {
		t.Errorf("a long text: %v, offset %+v, want the error at byte 258", err, te)
	}
	// A pair that does not fit is the character that goes over, not its
	// second half.
	err = keys.ValidateText(strings.Repeat("a", 255) + "😀")
	if !errors.As(err, &te) || te.Offset != 255 {
		t.Errorf("a long text ending in a pair: %v, offset %+v, want the error at byte 255", err, te)
	}
	// The first problem in reading order is the one reported.
	if err := keys.ValidateText(strings.Repeat("a", 300) + "\a"); err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("a long text with a bell at the end: %v, want too long", err)
	}
	if err := keys.ValidateText("\a" + strings.Repeat("a", 300)); err == nil || !strings.Contains(err.Error(), "control character") {
		t.Errorf("a long text with a bell at the start: %v, want the control character", err)
	}
}

func TestTextUnits(t *testing.T) {
	tests := []struct {
		text string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"你好", 2},
		{"😀", 2},
		{"a😀b", 4},
		{"a\n\tb", 4},
		{"é", 2},
	}
	for _, tt := range tests {
		if got := keys.TextUnits(tt.text); got != tt.want {
			t.Errorf("TextUnits(%q) = %d, want %d", tt.text, got, tt.want)
		}
	}
	if keys.MaxTextUnits != 256 {
		t.Errorf("MaxTextUnits = %d, want 256 (ADR-0070)", keys.MaxTextUnits)
	}
}

func FuzzValidateText(f *testing.F) {
	for _, s := range []string{
		"", "a", "你好", "😀", "a\nb\tc", "\x00", "\r", "\xff", "\xed\xa0\x80", strings.Repeat("a", 257), strings.Repeat("😀", 129),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		err := keys.ValidateText(s)
		if err != nil {
			var te *keys.TextError
			if !errors.As(err, &te) {
				t.Fatalf("ValidateText(%q) error %T, want *keys.TextError", s, err)
			}
			if te.Offset < 0 || te.Offset > len(s) || te.Msg == "" {
				t.Fatalf("ValidateText(%q) error at %d: %+v is out of bounds or silent", s, te.Offset, te)
			}
			return
		}
		if n := keys.TextUnits(s); n < 1 || n > keys.MaxTextUnits {
			t.Fatalf("ValidateText(%q) accepted a text of %d units", s, n)
		}
	})
}
