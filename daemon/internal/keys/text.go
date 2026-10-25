package keys

import (
	"fmt"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxTextUnits is the longest text a text action or step types (ADR-0070): 256
// UTF-16 code units. A character outside the Basic Multilingual Plane is two
// units; a newline and a tab are one each.
const MaxTextUnits = 256

// TextError reports why a text cannot be typed. Offset is the byte offset in
// the text of the first character that is wrong, and Msg says why. Like a
// *ParseError it does not repeat the input.
type TextError struct {
	Offset int
	Msg    string
}

// Error returns the message.
func (e *TextError) Error() string { return e.Msg }

// ValidateText checks a text a binding types (ADR-0070, OUT-21): 1 to
// MaxTextUnits UTF-16 code units of valid UTF-8, with no control character but
// the newline (typed as an Enter tap) and the tab (typed as a Tab tap). Control
// characters are the C0 and C1 ranges and DEL, so a carriage return is refused:
// write "\n". It returns a *TextError for the first problem in reading order.
//
// It is pure and cross-platform so that profile parsing, the script host and
// the output layer share one rule.
func ValidateText(s string) error {
	if s == "" {
		return &TextError{Msg: "empty text; write at least one character"}
	}
	units := 0
	for i, r := range s {
		if r == utf8.RuneError {
			// A real U+FFFD has three bytes; one byte is a bad encoding.
			if _, size := utf8.DecodeRuneInString(s[i:]); size == 1 {
				return &TextError{Offset: i, Msg: fmt.Sprintf("text is not valid UTF-8 at byte %d", i)}
			}
		}
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return &TextError{Offset: i, Msg: fmt.Sprintf(
				"control character U+%04X at byte %d; only a newline (\\n) and a tab (\\t) can be typed", r, i)}
		}
		units += utf16.RuneLen(r)
		if units > MaxTextUnits {
			return &TextError{Offset: i, Msg: fmt.Sprintf(
				"text is too long: at most %d UTF-16 units (a character outside the Basic Multilingual Plane is two); it goes over at byte %d",
				MaxTextUnits, i)}
		}
	}
	return nil
}

// TextUnits returns the number of UTF-16 code units of s, which is what
// MaxTextUnits limits. A byte that is not valid UTF-8 counts as the one unit
// of U+FFFD.
func TextUnits(s string) int {
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}
