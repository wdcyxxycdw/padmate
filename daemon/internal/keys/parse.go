package keys

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
)

// ParseError reports an invalid KeySpec. Offset and Len locate the
// offending token in Input, in bytes, so diagnostics can point at a column.
// Len may be 0 when the problem is a missing token.
type ParseError struct {
	Input  string
	Offset int
	Len    int
	Msg    string
}

// Error returns the message. It does not repeat Input; callers that show
// the error next to the spec already have it.
func (e *ParseError) Error() string { return e.Msg }

// SequenceError reports which element of a KeySpec[] failed to parse.
type SequenceError struct {
	Index int
	Err   *ParseError
}

func (e *SequenceError) Error() string {
	return fmt.Sprintf("element %d: %v", e.Index, e.Err)
}

// Unwrap returns the underlying *ParseError.
func (e *SequenceError) Unwrap() error { return e.Err }

// SequenceHint ends the message of a ParseError for a chord whose
// non-modifier is not last. Callers that take no sequences can replace it.
const SequenceHint = " (write a sequence as an array of key specs)"

// ErrEmptySequence is returned by ParseSequence for an empty KeySpec[].
var ErrEmptySequence = errors.New("empty key sequence")

// Parse parses a KeySpec: Chord := (Modifier "+")* Key.
//
// Names are matched case-insensitively and spaces or tabs around tokens are
// dropped, so "shift + ctrl + z" parses to the same Chord as "Ctrl+Shift+Z".
// Only the last token may be a non-modifier; a chord made only of modifiers
// is a set and is canonicalized as such. Parse reports errors as
// *ParseError.
func Parse(s string) (Chord, error) {
	c, err := parse(s)
	if err != nil {
		return Chord{}, err
	}
	return c, nil
}

// MustParse is like Parse but panics on error. It is meant for constants
// and tests.
func MustParse(s string) Chord {
	c, err := parse(s)
	if err != nil {
		panic("keys.MustParse(" + strconv.Quote(s) + "): " + err.Error())
	}
	return c
}

// ParseSequence parses the KeySpec[] form, which taps the chords one after
// another. Sequences are arrays, so "," is always a key. Errors are
// *SequenceError, or ErrEmptySequence when specs is empty.
func ParseSequence(specs []string) ([]Chord, error) {
	if len(specs) == 0 {
		return nil, ErrEmptySequence
	}
	out := make([]Chord, len(specs))
	for i, s := range specs {
		c, err := parse(s)
		if err != nil {
			return nil, &SequenceError{Index: i, Err: err}
		}
		out[i] = c
	}
	return out, nil
}

func parse(s string) (Chord, *ParseError) {
	var seen Mods
	start := 0
	for {
		end := strings.IndexByte(s[start:], '+')
		last := end < 0
		if last {
			end = len(s)
		} else {
			end += start
		}
		off, tok := trimToken(s, start, end)
		if tok == "" {
			return Chord{}, emptyTokenError(s, start, end, last)
		}
		k, ok := Lookup(tok)
		if !ok {
			return Chord{}, &ParseError{Input: s, Offset: off, Len: len(tok), Msg: unknownKeyMessage(tok)}
		}
		if !k.IsModifier() {
			if !last {
				return Chord{}, errorAt(s, off, len(tok),
					"%q is not a modifier; only the last key of a chord can be a non-modifier"+SequenceHint, tok)
			}
			return Chord{mods: seen, key: k}, nil
		}
		m := k.Mod()
		if msg := modifierConflict(seen, m); msg != "" {
			return Chord{}, &ParseError{Input: s, Offset: off, Len: len(tok), Msg: msg}
		}
		seen |= m
		if last {
			return modifierChord(seen), nil
		}
		start = end + 1
	}
}

func errorAt(s string, off, n int, format string, args ...any) *ParseError {
	return &ParseError{Input: s, Offset: off, Len: n, Msg: fmt.Sprintf(format, args...)}
}

// trimToken drops spaces and tabs around s[start:end] and returns the
// token with its offset.
func trimToken(s string, start, end int) (int, string) {
	for start < end && isSpace(s[start]) {
		start++
	}
	for end > start && isSpace(s[end-1]) {
		end--
	}
	return start, s[start:end]
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' }

// emptyTokenError explains an empty token in s[start:end]. It points at the
// "+" that follows the token, or the one before it when the token is last.
func emptyTokenError(s string, start, end int, last bool) *ParseError {
	leading := start == 0
	if leading && last {
		return errorAt(s, 0, len(s), "empty key spec")
	}
	if last {
		msg := `missing key after "+"`
		if end > start { // the token was only spaces
			msg += "; write Space for the space bar"
		}
		return errorAt(s, start-1, 1, "%s", msg)
	}
	// Two "+" in a row (or a lone "+") most likely mean the + character.
	if !leading || nextTokenEmpty(s, end+1) {
		return errorAt(s, end, 1, "empty key name; write Plus for the + character")
	}
	return errorAt(s, end, 1, `missing modifier before "+"`)
}

// nextTokenEmpty reports whether the token starting at s[start:] is empty
// or only spaces.
func nextTokenEmpty(s string, start int) bool {
	end := strings.IndexByte(s[start:], '+')
	if end < 0 {
		end = len(s)
	} else {
		end += start
	}
	_, tok := trimToken(s, start, end)
	return tok == ""
}

// modifierConflict checks adding modifier m to seen. It rejects duplicates
// and a generic modifier together with a sided one of the same family
// (Ctrl with LCtrl); LCtrl with RCtrl is fine.
func modifierConflict(seen, m Mods) string {
	if seen&m != 0 {
		return fmt.Sprintf("duplicate modifier %q", m)
	}
	shift := bits.TrailingZeros16(uint16(m)) / 3 * 3
	generic := Mods(1) << shift
	family := Mods(7) << shift
	var other Mods
	if m == generic {
		other = seen & family
	} else {
		other = seen & generic
	}
	if other == 0 {
		return ""
	}
	other &= -other // lowest bit, for a stable message
	return fmt.Sprintf("%q conflicts with %q; use either the generic or the sided modifier", m, other)
}

// modifierChord canonicalizes a chord made only of modifiers: the modifier
// that sorts last becomes the key.
func modifierChord(set Mods) Chord {
	i := bits.Len16(uint16(set)) - 1
	return Chord{mods: set &^ (1 << i), key: tbl.mods[i]}
}

const scanPrefix = "sc:"

// parseScan parses a raw scan code token ("sc:0x2C", "sc:0xE01D"). On
// failure it returns an explanation.
func parseScan(tok string) (Key, string) {
	hex := tok[len(scanPrefix):]
	if !hasPrefixFold(hex, "0x") || len(hex) < 3 || len(hex) > 6 {
		return Key{}, scanSyntaxMessage(tok)
	}
	v, err := strconv.ParseUint(hex[2:], 16, 16)
	if err != nil {
		return Key{}, scanSyntaxMessage(tok)
	}
	switch {
	case 0x01 <= v && v <= 0x7F, 0xE001 <= v && v <= 0xE07F:
		return Key{kind: KindScan, code: uint16(v)}, ""
	case v>>8 == 0xE1:
		return Key{}, fmt.Sprintf("scan code %q has an E1 prefix, which only Pause uses; write Pause", tok)
	}
	return Key{}, fmt.Sprintf("scan code %q is out of range; use 0x01-0x7F, or 0xE001-0xE07F for an E0-extended key", tok)
}

func scanSyntaxMessage(tok string) string {
	return fmt.Sprintf("invalid scan code %q; write it in hex, as sc:0x2C, or sc:0xE01D for an E0-extended key", tok)
}

// hasPrefixFold reports whether s starts with the lower-case ASCII prefix,
// ignoring case.
func hasPrefixFold(s, prefix string) bool {
	if len(s) < len(prefix) {
		return false
	}
	for i := range len(prefix) {
		c := s[i]
		if 'A' <= c && c <= 'Z' {
			c += 'a' - 'A'
		}
		if c != prefix[i] {
			return false
		}
	}
	return true
}

// shifted maps the characters typed with Shift on the US layout to the key
// that types them. They are not keys; the hint tells the user what to bind.
var shifted = map[string]string{
	"!": "1", "@": "2", "#": "3", "$": "4", "%": "5", "^": "6", "&": "7", "*": "8", "(": "9", ")": "0",
	"_": "-", "{": "[", "}": "]", "|": "\\", ":": ";", "\"": "'", "<": ",", ">": ".", "?": "/", "~": "`",
}

// unknownKeyMessage explains why tok is not a key, with a hint when one is
// cheap to find.
func unknownKeyMessage(tok string) string {
	if hasPrefixFold(tok, scanPrefix) {
		_, msg := parseScan(tok)
		return msg
	}
	if base, ok := shifted[tok]; ok {
		return fmt.Sprintf("%q is a shifted character; bind the unshifted key with Shift, e.g. Shift+%s", tok, base)
	}
	if isFunctionKeyName(tok) {
		return fmt.Sprintf("unknown key %q; function keys go from F1 to F24", tok)
	}
	if hint := separatorHint(tok); hint != "" {
		return fmt.Sprintf("unknown key %q; keys in a chord are joined with \"+\", e.g. %s", tok, hint)
	}
	if names := suggest(tok); len(names) > 0 {
		return fmt.Sprintf("unknown key %q; did you mean %s?", tok, strings.Join(names, ", "))
	}
	return fmt.Sprintf("unknown key %q", tok)
}

// isFunctionKeyName reports whether tok looks like F<number>.
func isFunctionKeyName(tok string) bool {
	if len(tok) < 2 || len(tok) > 6 || (tok[0] != 'F' && tok[0] != 'f') {
		return false
	}
	for i := 1; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return false
		}
	}
	return true
}

// separatorHint detects chords joined with "-" or spaces ("ctrl-z") and
// returns the chord spelled correctly, or "".
func separatorHint(tok string) string {
	parts := strings.FieldsFunc(tok, func(r rune) bool { return r == '-' || r == ' ' || r == '\t' })
	if len(parts) < 2 {
		return ""
	}
	c, err := parse(strings.Join(parts, "+"))
	if err != nil {
		return ""
	}
	return c.String()
}

// suggest returns up to three canonical names close to tok.
func suggest(tok string) []string {
	if len(tok) < 2 || len(tok) > maxNameLen+2 {
		return nil
	}
	lt := foldASCII(tok)
	limit := 1
	if len(lt) > 4 {
		limit = 2
	}
	best := limit
	var names []string
	for _, e := range tbl.entries {
		d := distance(lt, foldASCII(e.name))
		for _, a := range e.aliases {
			d = min(d, distance(lt, foldASCII(a)))
		}
		switch {
		case d < best || (d == best && len(names) == 0):
			best, names = d, append(names[:0], e.name)
		case d == best && len(names) < 3:
			names = append(names, e.name)
		}
	}
	return names
}

// distance is the optimal string alignment distance between a and b
// (Levenshtein plus adjacent transpositions), over bytes.
func distance(a, b string) int {
	if diff := len(a) - len(b); diff > 2 || diff < -2 {
		return 3 // further than any suggestion limit
	}
	// Three rolling rows: two back, previous, current.
	prev2 := make([]int, len(b)+1)
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
			if i > 1 && j > 1 && a[i-1] == b[j-2] && a[i-2] == b[j-1] {
				cur[j] = min(cur[j], prev2[j-2]+1)
			}
		}
		prev2, prev, cur = prev, cur, prev2
	}
	return prev[len(b)]
}
