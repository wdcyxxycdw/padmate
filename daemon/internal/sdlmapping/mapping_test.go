package sdlmapping

import (
	"strings"
	"testing"
)

const guid = "ff00aabb000000000000000000007600"

func TestParseInput(t *testing.T) {
	tests := []struct {
		in   string
		want Input
		text string
	}{
		{"b0", Input{Kind: InputButton}, "b0"},
		{"b15", Input{Kind: InputButton, Index: 15}, "b15"},
		{"a2", Input{Kind: InputAxis, Index: 2}, "a2"},
		{"+a2", Input{Kind: InputAxis, Index: 2, Half: Positive}, "+a2"},
		{"-a2", Input{Kind: InputAxis, Index: 2, Half: Negative}, "-a2"},
		{"a2~", Input{Kind: InputAxis, Index: 2, Inverted: true}, "a2~"},
		{"+a5~", Input{Kind: InputAxis, Index: 5, Half: Positive, Inverted: true}, "+a5~"},
		{"-a5~", Input{Kind: InputAxis, Index: 5, Half: Negative, Inverted: true}, "-a5~"},
		{"h0.1", Input{Kind: InputHat, Mask: 1}, "h0.1"},
		{"h1.8", Input{Kind: InputHat, Index: 1, Mask: 8}, "h1.8"},
		{"h0.3", Input{Kind: InputHat, Mask: 3}, "h0.3"},
	}
	for _, tc := range tests {
		got, err := ParseInput(tc.in)
		if err != nil || got != tc.want || got.String() != tc.text {
			t.Errorf("ParseInput(%q) = %+v %q, %v; want %+v %q", tc.in, got, got.String(), err, tc.want, tc.text)
		}
	}
	for _, bad := range []string{
		"", "b", "a", "h", "x1", "b-1", "b01", "bb", "+b1", "-h0.1", "h0", "h0.", "h0.0", "h0.16", "h.1",
		"a64", "b256", "a1~~", "~a1", "a1 ", "++a1", "+", "-", "a1.5", "h0.1~",
	} {
		if got, err := ParseInput(bad); err == nil {
			t.Errorf("ParseInput(%q) = %+v, want an error", bad, got)
		}
	}
}

func TestParse(t *testing.T) {
	m, err := Parse(" " + strings.ToUpper(guid) + ",*,a:b0,b:b1,-leftx:-a0,+leftx:+a0,lefttrigger:a4,dpup:h0.1,platform:Windows, ")
	if err != nil {
		t.Fatal(err)
	}
	if m.GUID != guid || m.Name != "*" || len(m.Fields) != 7 {
		t.Fatalf("Parse = %+v", m)
	}
	if v, ok := m.Get("lefttrigger"); !ok || v != "a4" {
		t.Errorf("Get(lefttrigger) = %q %v", v, ok)
	}
	if got := m.String(); got != guid+",*,a:b0,b:b1,-leftx:-a0,+leftx:+a0,lefttrigger:a4,dpup:h0.1,platform:Windows" {
		t.Errorf("String() = %q", got)
	}
	if got := m.DBLine(); got != m.String()+"," {
		t.Errorf("DBLine() = %q; the platform field must end with a comma", got)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	if !m.Binds("leftx") || m.Binds("lefty") || !m.Binds("a") || !m.BindsButton("south") || m.BindsButton("back") || m.BindsButton("nonsense") {
		t.Errorf("Binds: leftx %v lefty %v a %v south %v back %v", m.Binds("leftx"), m.Binds("lefty"), m.Binds("a"), m.BindsButton("south"), m.BindsButton("back"))
	}
}

func TestParseRejects(t *testing.T) {
	for name, s := range map[string]string{
		"empty":           "",
		"no commas":       guid,
		"one comma":       guid + ",*",
		"short guid":      "abcd,*,a:b0",
		"special guid":    "xinput,*,a:b0",
		"default guid":    "default,*,a:b0",
		"bad hex":         strings.Repeat("g", 32) + ",*,a:b0",
		"empty name":      guid + ",,a:b0",
		"no colon":        guid + ",*,a",
		"empty value":     guid + ",*,a:",
		"unknown key":     guid + ",*,nonsense:b0",
		"bad value":       guid + ",*,a:x0",
		"duplicate key":   guid + ",*,a:b0,a:b1",
		"long key":        guid + ",*," + strings.Repeat("k", 20) + ":b0",
		"long value":      guid + ",*,a:b" + strings.Repeat("1", 130),
		"control char":    guid + ",*,a:b0\n,b:b1",
		"nul":             guid + ",*,a:b0\x00",
		"prefixed button": guid + ",*,+a:b0",
		"huge":            guid + ",*," + strings.Repeat("a:b0,", MaxLen),
	} {
		if m, err := Parse(s); err == nil {
			t.Errorf("%s: Parse(%q) = %+v, want an error", name, s, m)
		}
	}
	m, err := Parse(guid + ",*,platform:Windows")
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err == nil {
		t.Error("a mapping that binds nothing validated")
	}
}

func TestParseKeepsTheNameWithoutComma(t *testing.T) {
	m, err := Parse(guid + ",My Pad (USB),a:b0")
	if err != nil || m.Name != "My Pad (USB)" {
		t.Fatalf("Parse = %+v, %v", m, err)
	}
	// A comma ends the name: what follows is a field, which must be one.
	if _, err := Parse(guid + ",My Pad, rev 2,a:b0"); err == nil {
		t.Error("a name with a comma was accepted; SDL would read ' rev 2' as a field")
	}
}

func TestNormalize(t *testing.T) {
	m, err := Parse(guid + ",Some Pad,a:b0,type:xboxone,face:abxy,hint:SDL_X:=1,crc:1234,platform:Windows,b:b1")
	if err != nil {
		t.Fatal(err)
	}
	n, notes, err := Normalize(m, strings.ToUpper(guid))
	if err != nil {
		t.Fatal(err)
	}
	want := guid + ",*,a:b0,crc:1234,b:b1,platform:Windows"
	if n.String() != want {
		t.Errorf("Normalize = %q, want %q", n, want)
	}
	if len(notes) != 4 {
		t.Errorf("notes = %q, want the name, type, face and hint", notes)
	}
	if _, _, err := Normalize(m, strings.Repeat("0", 32)); err == nil {
		t.Error("a mapping for another GUID was normalized")
	}
	mac, _ := Parse(guid + ",*,a:b0,platform:Mac OS X")
	if _, _, err := Normalize(mac, guid); err == nil {
		t.Error("platform:Mac OS X was normalized")
	}
	none, _ := Parse(guid + ",*,type:ps5")
	if _, _, err := Normalize(none, guid); err == nil {
		t.Error("a mapping without a binding was normalized")
	}
}

func TestEquivalent(t *testing.T) {
	a, _ := Parse(guid + ",*,a:b0,b:b1,platform:Windows")
	b, _ := Parse(guid + ",Name,b:b1,crc:ab12,a:b0")
	c, _ := Parse(guid + ",*,a:b0,b:b2")
	d, _ := Parse(guid + ",*,a:b0")
	if !Equivalent(a, b) || Equivalent(a, c) || Equivalent(a, d) {
		t.Errorf("Equivalent: a~b %v a~c %v a~d %v", Equivalent(a, b), Equivalent(a, c), Equivalent(a, d))
	}
}

func FuzzParse(f *testing.F) {
	f.Add(guid + ",*,a:b0,-leftx:-a0,+leftx:+a0~,dpup:h0.1,platform:Windows,")
	f.Add("")
	f.Add(",,,")
	f.Fuzz(func(t *testing.T, s string) {
		m, err := Parse(s)
		if err != nil {
			return
		}
		again, err := Parse(m.String())
		if err != nil {
			t.Fatalf("Parse(%q).String() = %q does not parse: %v", s, m.String(), err)
		}
		if again.String() != m.String() {
			t.Fatalf("String is not stable: %q then %q", m.String(), again.String())
		}
		if _, err := Parse(m.DBLine()); err != nil {
			t.Fatalf("DBLine %q does not parse: %v", m.DBLine(), err)
		}
	})
}
