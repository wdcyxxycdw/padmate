package profile

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestScanErrors(t *testing.T) {
	const outOfRange = "number out of range; it has too many digits or an exponent that is too large"
	deep := strings.Repeat("[", maxDepth+1) + strings.Repeat("]", maxDepth+1)
	for _, tc := range []struct {
		in   string
		off  int
		want string
	}{
		{"", 0, "the file is empty; a profile is a JSON object"},
		{" \r\n\t", 4, "the file is empty; a profile is a JSON object"},
		{"\xEF\xBB\xBF", 3, "the file is empty; a profile is a JSON object"},
		{"{", 1, `unexpected end of file; "{" at 1:1 is not closed`},
		{`{"a": [1,`, 9, `unexpected end of file; "[" at 1:7 is not closed`},
		{`{"a"`, 4, `unexpected end of file; "{" at 1:1 is not closed`},
		{`{"a":`, 5, `unexpected end of file; "{" at 1:1 is not closed`},
		{`-`, 1, `expected a digit after "-"`},
		{`{"a": 1,}`, 7, `trailing comma before "}" (strict JSON)`},
		{"[1,\n ]", 2, `trailing comma before "]" (strict JSON)`},
		{`{"a": 1 "b": 2}`, 8, `missing "," between object members`},
		{`[1 2]`, 3, `missing "," between array elements`},
		{`[{} {}]`, 4, `missing "," between array elements`},
		{`{"a": 1]`, 7, `expected "}" to close "{" at 1:1, found "]"`},
		{`[1}`, 2, `expected "]" to close "[" at 1:1, found "}"`},
		{`{"a" 1}`, 5, `unexpected "1"; expected ":" after the key`},
		{`{"a": 1 x}`, 8, `unexpected "x"; expected "," or "}"`},
		{`{"a": "b"c}`, 9, `unexpected "c" right after a string; a closing quote may be missing before it`},
		{`["b"c]`, 4, `unexpected "c" right after a string; a closing quote may be missing before it`},
		{`{"a": 1 @}`, 8, `unexpected "@"; expected "," or "}"`},
		{`{a: 1}`, 1, `keys need double quotes: write "a"`},
		{`{1: 1}`, 1, `keys need double quotes: write "1"`},
		{`{@: 1}`, 1, `unexpected "@"; expected a key in double quotes`},
		{`{'a': 1}`, 1, "strings need double quotes, not single quotes"},
		{`{"a": 'b'}`, 6, "strings need double quotes, not single quotes"},
		{`{"a": tap}`, 6, `strings need double quotes: write "tap"`},
		{`{"a": True}`, 6, "use lower-case true"},
		{`{"a": NULL}`, 6, "use lower-case null"},
		{`{"a": truex}`, 6, `strings need double quotes: write "truex"`},
		{`{"a": 撤销}`, 6, `strings need double quotes: write "撤销"`},
		{`{"a": ` + strings.Repeat("x", 40) + `}`, 6, `strings need double quotes: write "` + strings.Repeat("x", maxWord) + `…"`},
		{"{\"a\": 1 // no\n}", 8, "comments are not allowed (strict JSON)"},
		{"// c\n{}", 0, "comments are not allowed (strict JSON)"},
		{"{} /* c */", 3, "comments are not allowed (strict JSON)"},
		{`{"a": /x/}`, 6, `unexpected "/"; expected a value`},
		{`{"a": "x`, 6, "unterminated string; the closing quote is missing"},
		{"{\"a\": \"x\n\"}", 6, "unterminated string; the closing quote is missing"},
		{"{\"a\": \"x\r\"}", 6, "unterminated string; the closing quote is missing"},
		{`{"a": "x\`, 6, "unterminated string; the closing quote is missing"},
		{"{\"a\": \"\tx\"}", 7, `tab in a string; write \t instead`},
		{"{\"a\": \"\x01\"}", 7, `control character U+0001 in a string; write \u0001 instead`},
		{`{"a": "\x"}`, 7, `invalid escape "\x"; write \\ for a backslash`},
		{`{"a": "\撤"}`, 7, `invalid escape "\撤"; write \\ for a backslash`},
		{`{"a": "\ "}`, 7, `invalid escape; write \\ for a backslash`},
		{`{"a": "\u12"}`, 7, `invalid escape: \u needs four hex digits`},
		{`{"a": "\u12G4"}`, 7, `invalid escape: \u needs four hex digits`},
		{`{"a": 01}`, 6, "numbers must not have leading zeros"},
		{`{"a": -01}`, 6, "numbers must not have leading zeros"},
		{`{"a": -x}`, 7, `expected a digit after "-"`},
		{`{"a": 1.}`, 8, "expected a digit after the decimal point"},
		{`{"a": 1e}`, 8, "expected a digit in the exponent"},
		{`{"a": 1e+}`, 9, "expected a digit in the exponent"},
		{`{"a": 1E1000001}`, 6, outOfRange},
		{`{"a": 1E-1000001}`, 6, outOfRange},
		{`[0, -1E400]`, 4, outOfRange},
		{`{"a": 1` + strings.Repeat("0", 400) + `}`, 6, outOfRange},
		{`{"a": 0.` + strings.Repeat("0", 999) + `1}`, 6, outOfRange}, // 1001 characters
		{`{"a": 1E+1000000000000}`, 6, outOfRange},
		{`{"a": .5}`, 6, `unexpected "."; expected a value`},
		{`{"a": +1}`, 6, `unexpected "+"; expected a value`},
		{`{"a": 1, "a": 2}`, 9, `duplicate key "a"; the first is at 1:2`},
		{"{\"撤销\": 1,\n \"\\u64a4\\u9500\": 2}", 15, `duplicate key "撤销"; the first is at 1:2`},
		{`{} {}`, 3, `extra data after the end; the profile already ended with "}" at 1:2`},
		{"[]\n]", 3, `extra data after the end; the profile already ended with "]" at 1:2`},
		{`1 2`, 2, "extra data after the top-level value"},
		{"{\x00}", 1, "unexpected NUL byte; if the file is UTF-16, save it as UTF-8"},
		{"\xFF\xFE{\x00}\x00", 0, "the file is UTF-16; save it as UTF-8"},
		{"\xFE\xFF\x00{\x00}", 0, "the file is UTF-16; save it as UTF-8"},
		{"{\"a\": \"\xB3\xB7\"}", 7, "invalid UTF-8 (byte 0xB3); save the file as UTF-8"},
		{"{\"撤\": \"\xE6\x92\"}", 9, "invalid UTF-8 (byte 0xE6); save the file as UTF-8"},
		{`{"a": 1，"b": 2}`, 7, `full-width comma "，"; use ","`},
		{`{"a"：1}`, 4, `full-width colon "："; use ":"`},
		{`{"a": “b”}`, 6, `curly quote "“"; use a straight double quote`},
		{`｛"a": 1}`, 0, `full-width bracket "｛"; use "{"`},
		{`{"a": ［]}`, 6, `full-width bracket "［"; use "["`},
		{"{\"a\":\u30001}", 5, "full-width space U+3000; use an ordinary space"},
		{"{\"a\": \u00a0}", 6, `unexpected U+00A0; expected a value`},
		{deep, maxDepth, "nesting is deeper than 1000 levels"},
	} {
		_, serr := scan([]byte(tc.in))
		if serr == nil {
			t.Errorf("scan(%q) succeeded, want error at %d: %s", tc.in, tc.off, tc.want)
			continue
		}
		if serr.off != tc.off || serr.msg != tc.want {
			t.Errorf("scan(%q) = error at %d: %s\nwant error at %d: %s", tc.in, serr.off, serr.msg, tc.off, tc.want)
		}
	}
}

// TestScanMatchesJSON checks that the scanner decodes values like
// encoding/json and indexes every value at its first byte.
func TestScanMatchesJSON(t *testing.T) {
	for _, in := range []string{
		`{}`,
		`[]`,
		`0`,
		`"x"`,
		` {"a": [1, -2.5e+3, true, false, null, {"b": {}}], "c": ""} `,
		"\xEF\xBB\xBF{\"撤销\": \"画笔 🎨\", \"\\u64a4\": [\"\\n\\t\\\"\\\\\\/\"]}",
		`{"a/b": {"m~n": [[], [{"": 0}]]}, "~1": "\ud800", "0": [0]}`,
		strings.Repeat("[", maxDepth) + strings.Repeat("]", maxDepth),
		`{"schemaVersion": 0, "name": "n", "layers": {"base": {"a": {"press": {"type": "keys", "keys": "Z"}}}}}`,
	} {
		data := []byte(in)
		root, serr := scan(data)
		if serr != nil {
			t.Errorf("scan(%q): %d: %s", in, serr.off, serr.msg)
			continue
		}
		var want any
		dec := json.NewDecoder(strings.NewReader(in[bomLen(data):]))
		dec.UseNumber()
		if err := dec.Decode(&want); err != nil {
			t.Fatalf("json.Decode(%q): %v", in, err)
		}
		if !reflect.DeepEqual(root.val, want) {
			t.Errorf("scan(%q).val = %#v, want %#v", in, root.val, want)
		}
		walk(t, data, root, nil, want)
	}
}

// walk checks that every value in v is in the index at tokens, and that
// the indexed bytes decode to it.
func walk(t *testing.T, data []byte, root *node, tokens []string, v any) {
	t.Helper()
	p := pointer(tokens)
	n := root.lookupPointer(p)
	if n == nil {
		t.Errorf("pointer %q is not in the index", p)
		return
	}
	var got any
	dec := json.NewDecoder(strings.NewReader(string(data[n.start:n.end])))
	dec.UseNumber()
	if err := dec.Decode(&got); err != nil || !reflect.DeepEqual(got, v) {
		t.Errorf("%q: data[%d:%d] = %q decodes to %#v (%v), want %#v", p, n.start, n.end, data[n.start:n.end], got, err, v)
	}
	if s, ok := v.(string); ok && n.raw && string(data[n.start+1:n.end-1]) != s {
		t.Errorf("%q: raw string %q, value %q", p, data[n.start+1:n.end-1], s)
	}
	switch v := v.(type) {
	case map[string]any:
		for k, c := range v {
			m := n.members[k]
			if m == nil || m.name != k || m.key < 0 || data[m.key] != '"' {
				t.Errorf("%q: member %q is missing or has no key offset", p, k)
			}
			walk(t, data, root, append(tokens[:len(tokens):len(tokens)], k), c)
		}
	case []any:
		for i, c := range v {
			walk(t, data, root, append(tokens[:len(tokens):len(tokens)], strconv.Itoa(i)), c)
		}
	}
}

func TestLookup(t *testing.T) {
	root, serr := scan([]byte(`{"a": [10, {"b": 1}], "c": 2}`))
	if serr != nil {
		t.Fatal(serr.msg)
	}
	for _, tc := range []struct {
		p     string
		found bool
	}{
		{"", true},
		{"/a/1/b", true},
		{"/a/0", true},
		{"/a/2", false},
		{"/a/01", false},
		{"/a/-", false},
		{"/a/x", false},
		{"/c/0", false},
		{"/d", false},
		{"a", false},
	} {
		if got := root.lookupPointer(tc.p) != nil; got != tc.found {
			t.Errorf("lookupPointer(%q) found = %v, want %v", tc.p, got, tc.found)
		}
	}
}
