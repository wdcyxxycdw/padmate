package profile

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// node is one value of a scanned document with its location: the index
// that turns a JSON Pointer from the schema validator into a file offset.
type node struct {
	start, end int              // the value is data[start:end]
	key        int              // offset of the member's key, -1 for the root and array elements
	name       string           // the member's key
	val        any              // decoded as jsonschema expects: map[string]any, []any, string, json.Number, bool or nil
	members    map[string]*node // object members by key
	order      []*node          // object members or array elements in document order
	raw        bool             // a string without escapes: data[start+1:end-1] is its value
}

// lookup returns the node at the path of reference tokens below n, or nil.
func (n *node) lookup(tokens []string) *node {
	for _, t := range tokens {
		switch n.val.(type) {
		case map[string]any:
			n = n.members[t]
		case []any:
			i, ok := arrayIndex(t)
			if !ok || i >= len(n.order) {
				return nil
			}
			n = n.order[i]
		default:
			return nil
		}
		if n == nil {
			return nil
		}
	}
	return n
}

// keyOffset returns the offset of member key of object n, or n.start if it
// has no such member.
func (n *node) keyOffset(key string) int {
	if m := n.members[key]; m != nil {
		return m.key
	}
	return n.start
}

// lookupPointer returns the node at JSON Pointer p below n, or nil.
func (n *node) lookupPointer(p string) *node {
	tokens, ok := splitPointer(p)
	if !ok {
		return nil
	}
	return n.lookup(tokens)
}

// arrayIndex parses an RFC 6901 array index: decimal, no leading zeros.
func arrayIndex(t string) (int, bool) {
	if t == "" || len(t) > 1 && t[0] == '0' || strings.Trim(t, "0123456789") != "" {
		return 0, false
	}
	i, err := strconv.Atoi(t)
	return i, err == nil
}

// maxDepth limits nesting. A profile needs a handful of levels.
const maxDepth = 1000

// syntaxError is the first syntax error of a document.
type syntaxError struct {
	off int
	msg string
}

// scan parses data as strict JSON (RFC 8259) and indexes every value. A
// leading BOM is skipped. Unlike encoding/json it rejects duplicate keys and
// invalid UTF-8, and numbers that the rest of the pipeline cannot take (out
// of range for a float64, or too long, see numberInRange), and it reports the
// first error at the offending character with a message meant for people
// editing a profile by hand.
func scan(data []byte) (root *node, serr *syntaxError) {
	if off, msg, ok := checkUTF8(data); !ok {
		return nil, &syntaxError{off, msg}
	}
	s := &scanner{data: data, pos: bomLen(data)}
	// The scanner panics with a *syntaxError to stop at the first error.
	defer func() {
		if r := recover(); r != nil {
			e, ok := r.(*syntaxError)
			if !ok {
				panic(r)
			}
			root, serr = nil, e
		}
	}()
	s.skipSpace()
	if s.pos == len(data) {
		s.fail(s.pos, "the file is empty; a profile is a JSON object")
	}
	root = s.value(0, -1)
	s.skipSpace()
	if s.pos < len(data) {
		s.trailing(root)
	}
	return root, nil
}

// checkUTF8 reports the first byte that is not valid UTF-8.
func checkUTF8(data []byte) (off int, msg string, ok bool) {
	if utf8.Valid(data) {
		return 0, "", true
	}
	if len(data) >= 2 && (data[0] == 0xFF && data[1] == 0xFE || data[0] == 0xFE && data[1] == 0xFF) {
		return 0, "the file is UTF-16; save it as UTF-8", false
	}
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size == 1 {
			return i, fmt.Sprintf("invalid UTF-8 (byte 0x%02X); save the file as UTF-8", data[i]), false
		}
		i += size
	}
	return 0, "", true // unreachable: utf8.Valid failed
}

type scanner struct {
	data []byte
	pos  int
}

func (s *scanner) fail(off int, msg string) {
	panic(&syntaxError{off, msg})
}

// at formats the position of off for a message.
func (s *scanner) at(off int) string {
	line, col := position(s.data, off)
	return strconv.Itoa(line) + ":" + strconv.Itoa(col)
}

func (s *scanner) skipSpace() {
	for s.pos < len(s.data) {
		switch s.data[s.pos] {
		case ' ', '\t', '\n', '\r':
			s.pos++
		default:
			return
		}
	}
}

// eof reports the end of the file inside the container that starts at
// open, or at the top level when open is -1.
func (s *scanner) eof(open int) {
	if open < 0 {
		s.fail(s.pos, "unexpected end of file")
	}
	s.fail(s.pos, fmt.Sprintf("unexpected end of file; %s at %s is not closed", quote(string(s.data[open])), s.at(open)))
}

// value scans the value at s.pos, inside a container at depth that starts
// at open.
func (s *scanner) value(depth, open int) *node {
	if s.pos == len(s.data) {
		s.eof(open)
	}
	switch c := s.data[s.pos]; {
	case c == '{':
		return s.object(depth + 1)
	case c == '[':
		return s.array(depth + 1)
	case c == '"':
		return s.str()
	case c == '-' || '0' <= c && c <= '9':
		return s.number()
	}
	if w := s.word(false); w != "" {
		return s.literal(w)
	}
	s.unexpected("a value")
	return nil
}

func (s *scanner) object(depth int) *node {
	open := s.pos
	if depth > maxDepth {
		s.fail(open, fmt.Sprintf("nesting is deeper than %d levels", maxDepth))
	}
	s.pos++
	n := &node{start: open, key: -1}
	vals := map[string]any{}
	n.val = vals
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == '}' {
		s.pos++
		n.end = s.pos
		return n
	}
	n.members = map[string]*node{}
	for {
		s.skipSpace()
		if s.pos == len(s.data) {
			s.eof(open)
		}
		if s.data[s.pos] != '"' {
			s.unexpectedKey()
		}
		keyOff := s.pos
		key := s.str().val.(string)
		if first, dup := n.members[key]; dup {
			s.fail(keyOff, fmt.Sprintf("duplicate key %s; the first is at %s", quote(key), s.at(first.key)))
		}
		s.skipSpace()
		if s.pos == len(s.data) {
			s.eof(open)
		}
		if s.data[s.pos] != ':' {
			s.unexpected(`":" after the key`)
		}
		s.pos++
		s.skipSpace()
		child := s.value(depth, open)
		child.key, child.name = keyOff, key
		n.members[key] = child
		n.order = append(n.order, child)
		vals[key] = child.val

		s.skipSpace()
		if s.pos == len(s.data) {
			s.eof(open)
		}
		switch s.data[s.pos] {
		case ',':
			comma := s.pos
			s.pos++
			s.skipSpace()
			if s.pos < len(s.data) && s.data[s.pos] == '}' {
				s.fail(comma, `trailing comma before "}" (strict JSON)`)
			}
			continue
		case '}':
			s.pos++
			n.end = s.pos
			return n
		case '"':
			s.fail(s.pos, `missing "," between object members`)
		case ']':
			s.fail(s.pos, fmt.Sprintf(`expected "}" to close "{" at %s, found "]"`, s.at(open)))
		}
		s.afterString(child)
		s.unexpected(`"," or "}"`)
	}
}

func (s *scanner) array(depth int) *node {
	open := s.pos
	if depth > maxDepth {
		s.fail(open, fmt.Sprintf("nesting is deeper than %d levels", maxDepth))
	}
	s.pos++
	n := &node{start: open, key: -1}
	vals := []any{}
	s.skipSpace()
	if s.pos < len(s.data) && s.data[s.pos] == ']' {
		s.pos++
		n.end, n.val = s.pos, vals
		return n
	}
	for {
		s.skipSpace()
		child := s.value(depth, open)
		n.order = append(n.order, child)
		vals = append(vals, child.val)

		s.skipSpace()
		if s.pos == len(s.data) {
			s.eof(open)
		}
		switch c := s.data[s.pos]; {
		case c == ',':
			comma := s.pos
			s.pos++
			s.skipSpace()
			if s.pos < len(s.data) && s.data[s.pos] == ']' {
				s.fail(comma, `trailing comma before "]" (strict JSON)`)
			}
			continue
		case c == ']':
			s.pos++
			n.end, n.val = s.pos, vals
			return n
		case c == '}':
			s.fail(s.pos, fmt.Sprintf(`expected "]" to close "[" at %s, found "}"`, s.at(open)))
		case c == '"' || c == '{' || c == '[' || c == '-' || '0' <= c && c <= '9':
			s.fail(s.pos, `missing "," between array elements`)
		}
		s.afterString(child)
		s.unexpected(`"," or "]"`)
	}
}

// str scans a string. Strings with escapes are decoded by encoding/json, so
// keys and values match what json.Unmarshal sees later.
func (s *scanner) str() *node {
	start := s.pos
	s.pos++
	escaped := false
	for {
		if s.pos == len(s.data) {
			s.fail(start, "unterminated string; the closing quote is missing")
		}
		switch c := s.data[s.pos]; {
		case c == '"':
			s.pos++
			n := &node{start: start, end: s.pos, key: -1}
			if !escaped {
				n.val, n.raw = string(s.data[start+1:s.pos-1]), true
				return n
			}
			var v string
			if err := json.Unmarshal(s.data[start:s.pos], &v); err != nil {
				s.fail(start, "invalid string: "+err.Error()) // not expected: the escapes were checked
			}
			n.val = v
			return n
		case c == '\\':
			escaped = true
			s.escape(start)
		case c == '\n' || c == '\r':
			s.fail(start, "unterminated string; the closing quote is missing")
		case c == '\t':
			s.fail(s.pos, `tab in a string; write \t instead`)
		case c < 0x20:
			s.fail(s.pos, fmt.Sprintf(`control character U+%04X in a string; write \u%04X instead`, c, c))
		default:
			s.pos++
		}
	}
}

// escape checks the escape sequence at s.pos in the string that starts at
// start, and skips it.
func (s *scanner) escape(start int) {
	if s.pos+1 == len(s.data) {
		s.fail(start, "unterminated string; the closing quote is missing")
	}
	switch e := s.data[s.pos+1]; e {
	case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
		s.pos += 2
		return
	case 'u':
		if s.pos+6 <= len(s.data) && isHex(s.data[s.pos+2:s.pos+6]) {
			s.pos += 6
			return
		}
		s.fail(s.pos, `invalid escape: \u needs four hex digits`)
	}
	r, _ := utf8.DecodeRune(s.data[s.pos+1:])
	if unicode.IsPrint(r) && r != ' ' {
		s.fail(s.pos, fmt.Sprintf(`invalid escape "\%c"; write \\ for a backslash`, r))
	}
	s.fail(s.pos, `invalid escape; write \\ for a backslash`)
}

func isHex(b []byte) bool {
	for _, c := range b {
		switch {
		case '0' <= c && c <= '9', 'a' <= c && c <= 'f', 'A' <= c && c <= 'F':
		default:
			return false
		}
	}
	return true
}

func (s *scanner) number() *node {
	start := s.pos
	if s.data[s.pos] == '-' {
		s.pos++
		if !s.digitAt() {
			s.fail(s.pos, `expected a digit after "-"`)
		}
	}
	if s.data[s.pos] == '0' {
		s.pos++
		if s.digitAt() {
			s.fail(start, "numbers must not have leading zeros")
		}
	} else {
		s.digits()
	}
	if s.pos < len(s.data) && s.data[s.pos] == '.' {
		s.pos++
		if !s.digitAt() {
			s.fail(s.pos, "expected a digit after the decimal point")
		}
		s.digits()
	}
	if s.pos < len(s.data) && (s.data[s.pos] == 'e' || s.data[s.pos] == 'E') {
		s.pos++
		if s.pos < len(s.data) && (s.data[s.pos] == '+' || s.data[s.pos] == '-') {
			s.pos++
		}
		if !s.digitAt() {
			s.fail(s.pos, "expected a digit in the exponent")
		}
		s.digits()
	}
	lit := string(s.data[start:s.pos])
	if !numberInRange(lit) {
		s.fail(start, "number out of range; it has too many digits or an exponent that is too large")
	}
	return &node{start: start, end: s.pos, key: -1, val: json.Number(lit)}
}

// maxNumberLen is the longest number literal the scanner takes. The longest
// a float64 needs is 309 digits for its largest value, and converting a
// literal to a big.Rat, as the schema validator does, takes time that grows
// with the square of its length (9 s for a million digits).
const maxNumberLen = 1000

// numberInRange reports whether the literal lit, whose syntax is checked, is a
// number the rest of the pipeline can take. The schema validator converts
// every number to a big.Rat, which fails for an exponent beyond about 1e6
// (the validator then panics on the nil result), and the decoder reads a
// number as a float64, which fails for a value beyond 1.8e308. A value so
// small that it rounds to 0 is not an error and is accepted.
func numberInRange(lit string) bool {
	if len(lit) > maxNumberLen {
		return false
	}
	if _, err := strconv.ParseFloat(lit, 64); err != nil {
		return false // the syntax is right, so the value is out of range
	}
	_, ok := new(big.Rat).SetString(lit)
	return ok
}

func (s *scanner) digitAt() bool {
	return s.pos < len(s.data) && '0' <= s.data[s.pos] && s.data[s.pos] <= '9'
}

func (s *scanner) digits() {
	for s.digitAt() {
		s.pos++
	}
}

// literal checks a bare word w at s.pos: true, false or null.
func (s *scanner) literal(w string) *node {
	start := s.pos
	var v any
	switch w {
	case "true":
		v = true
	case "false":
		v = false
	case "null":
	default:
		if l := strings.ToLower(w); l == "true" || l == "false" || l == "null" {
			s.fail(start, "use lower-case "+l)
		}
		s.fail(start, "strings need double quotes: write "+quote(w))
	}
	s.pos += len(w)
	return &node{start: start, end: s.pos, key: -1, val: v}
}

// maxWord limits how much of an unquoted word a message repeats.
const maxWord = 30

// word returns the unquoted word at s.pos, or "" if there is none there. A
// word starts with a letter, "_" or "$", or also a digit or "-" for a key,
// and runs to the next space or JSON punctuation.
func (s *scanner) word(key bool) string {
	r, _ := utf8.DecodeRune(s.data[s.pos:])
	if !unicode.IsLetter(r) && r != '_' && r != '$' && (!key || r != '-' && !unicode.IsDigit(r)) {
		return ""
	}
	end := s.pos
	for end < len(s.data) {
		r, size := utf8.DecodeRune(s.data[end:])
		if strings.ContainsRune(`,:{}[]"'/`, r) || unicode.IsSpace(r) || unicode.IsControl(r) || fullWidth(r) != "" {
			break
		}
		end += size
	}
	return string(s.data[s.pos:end])
}

// afterString reports a word that directly follows string n, which
// usually means that an earlier string lost its closing quote and n is the
// text between two strings.
func (s *scanner) afterString(n *node) {
	if _, ok := n.val.(string); !ok || s.pos != n.end {
		return
	}
	if w := s.word(true); w != "" {
		s.fail(s.pos, "unexpected "+quote(w)+" right after a string; a closing quote may be missing before it")
	}
}

// unexpectedKey reports what stands where an object key should be.
func (s *scanner) unexpectedKey() {
	if w := s.word(true); w != "" {
		s.fail(s.pos, "keys need double quotes: write "+quote(w))
	}
	s.unexpected("a key in double quotes")
}

// unexpected reports the character at s.pos, where expected should be.
func (s *scanner) unexpected(expected string) {
	if s.pos == len(s.data) {
		s.fail(s.pos, "unexpected end of file")
	}
	c := s.data[s.pos]
	switch {
	case c == '/' && s.pos+1 < len(s.data) && (s.data[s.pos+1] == '/' || s.data[s.pos+1] == '*'):
		s.fail(s.pos, "comments are not allowed (strict JSON)")
	case c == '\'':
		s.fail(s.pos, "strings need double quotes, not single quotes")
	case c == 0:
		s.fail(s.pos, "unexpected NUL byte; if the file is UTF-16, save it as UTF-8")
	}
	r, _ := utf8.DecodeRune(s.data[s.pos:])
	if msg := fullWidth(r); msg != "" {
		s.fail(s.pos, msg)
	}
	if w := s.word(true); w != "" {
		s.fail(s.pos, "unexpected "+quote(w)+"; expected "+expected)
	}
	s.fail(s.pos, "unexpected "+describeRune(r)+"; expected "+expected)
}

// trailing reports data after the top-level value.
func (s *scanner) trailing(root *node) {
	if c := s.data[s.pos]; c == '/' && s.pos+1 < len(s.data) && (s.data[s.pos+1] == '/' || s.data[s.pos+1] == '*') {
		s.fail(s.pos, "comments are not allowed (strict JSON)")
	}
	switch root.val.(type) {
	case map[string]any, []any:
		closer := root.end - 1
		s.fail(s.pos, fmt.Sprintf("extra data after the end; the profile already ended with %s at %s", quote(string(s.data[closer])), s.at(closer)))
	}
	s.fail(s.pos, "extra data after the top-level value")
}

// fullWidth returns a message for the full-width punctuation that Chinese
// and Japanese input methods type instead of JSON punctuation, or "".
func fullWidth(r rune) string {
	switch r {
	case '，':
		return `full-width comma "，"; use ","`
	case '：':
		return `full-width colon "："; use ":"`
	case '“', '”', '‘', '’':
		return fmt.Sprintf(`curly quote "%c"; use a straight double quote`, r)
	case '｛', '｝', '［', '］':
		return fmt.Sprintf(`full-width bracket "%c"; use "%c"`, r, r-0xFEE0)
	case '　':
		return "full-width space U+3000; use an ordinary space"
	}
	return ""
}

// describeRune names r for a message: quoted if printable, else U+XXXX.
func describeRune(r rune) string {
	if unicode.IsPrint(r) && r != utf8.RuneError {
		return quote(string(r))
	}
	return fmt.Sprintf("U+%04X", r)
}

// quote returns s in double quotes for a message, shortened to maxWord
// characters. Printable characters, Chinese included, are kept as they are.
func quote(s string) string {
	return strconv.Quote(shorten(s))
}

// shorten cuts s to maxWord characters and marks the cut with "…".
func shorten(s string) string {
	if utf8.RuneCountInString(s) > maxWord {
		runes := []rune(s)
		s = string(runes[:maxWord]) + "…"
	}
	return s
}
