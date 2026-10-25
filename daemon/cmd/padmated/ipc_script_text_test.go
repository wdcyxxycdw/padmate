package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
)

// escapeAll writes s as a JSON string with every character as \u escapes,
// a surrogate pair for characters outside the BMP.
func escapeAll(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, unit := range utf16.Encode([]rune(s)) {
		fmt.Fprintf(&b, `\u%04x`, unit)
	}
	b.WriteByte('"')
	return b.String()
}

func TestScriptText(t *testing.T) {
	bom, replacement := string(rune(0xfeff)), string(rune(0xfffd))
	valid := []string{"", "a", "// 画笔\n", "\U0001F600", bom + "export {};", replacement, `\` + "ud800", `"\u"`}
	for _, text := range valid {
		marshaled, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{string(marshaled), escapeAll(text)} {
			if got, err := scriptText(json.RawMessage(raw)); err != nil || got != text {
				t.Errorf("scriptText(%s) = %q, %v; want %q", raw, got, err, text)
			}
		}
	}
	invalid := []string{
		"", "null", "7", "[]", `{"a":1}`,
		"\"\xff\"", "\"a\xc3\"", "\"\xed\xa0\x80\"",
		`"\ud800"`, `"\udfff"`, `"x\udc00"`, `"\ud800x"`, `"\ud800\n"`, `"\ud800\ud800"`,
		`"\udc00\ud800"`, escapeAll("\U0001F600")[:13] + `\ude00"`, `"\ud800\\udc00"`,
		`"` + strings.Repeat("a", profilelib.MaxScriptBytes+1) + `"`,
	}
	for _, raw := range invalid {
		_, err := scriptText(json.RawMessage(raw))
		if e := new(jrpc2.Error); !errors.As(err, &e) || e.Code != jrpc2.InvalidParams {
			t.Errorf("scriptText(%q) = %v; want InvalidParams", raw, err)
		}
	}
	if _, err := scriptText(json.RawMessage(`"` + strings.Repeat("a", profilelib.MaxScriptBytes) + `"`)); err != nil {
		t.Fatal("1 MiB of text refused:", err)
	}
	// Every lone surrogate escape is refused; every pair is accepted.
	for unit := 0xd800; unit <= 0xdfff; unit++ {
		if !hasLoneSurrogate(fmt.Appendf(nil, `"\u%04x"`, unit)) {
			t.Fatalf("lone \\u%04x accepted", unit)
		}
	}
	for _, r := range []rune{0x10000, 0x1F600, 0x10FFFF} {
		if raw := escapeAll(string(r)); hasLoneSurrogate([]byte(raw)) {
			t.Fatalf("pair %s refused", raw)
		}
	}
}
