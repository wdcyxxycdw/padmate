package integration

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseItems(t *testing.T) {
	const ok = `{
  "format": 1,
  "items": [
    {"keys": "ctrl + z", "function": "command:basiccommand/editundo", "name": "Undo", "path": ["Edit"]},
    {"keys": "P", "function": "tool:00112233445566778899aabbccddeeff"}
  ]
}`
	items, err := ParseItems([]byte(ok))
	if err != nil {
		t.Fatal(err)
	}
	want := []Item{
		{Keys: "Ctrl+Z", Function: "command:basiccommand/editundo", Name: "Undo", Path: []string{"Edit"}},
		{Keys: "P", Function: "tool:00112233445566778899aabbccddeeff"},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("items = %+v", items)
	}
	// Formatting is stable and parses back to the same items.
	text := FormatItems(items)
	back, err := ParseItems(text)
	if err != nil || !reflect.DeepEqual(back, items) {
		t.Fatalf("round trip: %v %+v", err, back)
	}
	if string(FormatItems(back)) != string(text) {
		t.Error("format is not stable")
	}
	if !strings.HasSuffix(string(text), "}\n") || !strings.Contains(string(text), "\n  \"items\": [\n    {\n      \"keys\": \"Ctrl+Z\",\n      \"function\"") {
		t.Errorf("format:\n%s", text)
	}
	if got := string(EmptyItems()); got != "{\n  \"format\": 1,\n  \"items\": []\n}\n" {
		t.Errorf("empty = %q", got)
	}
}

func TestParseItemsErrors(t *testing.T) {
	long := strings.Repeat("a", 257)
	many := `{"format":1,"items":[` + strings.TrimSuffix(strings.Repeat(`{"keys":"A","function":"f"},`, 513), ",") + `]}`
	tests := map[string]string{
		"empty":            ``,
		"not object":       `[]`,
		"trailing comma":   `{"format":1,"items":[],}`,
		"duplicate member": `{"format":1,"format":1,"items":[]}`,
		"no format":        `{"items":[]}`,
		"format 2":         `{"format":2,"items":[]}`,
		"format string":    `{"format":"1","items":[]}`,
		"no items":         `{"format":1}`,
		"extra member":     `{"format":1,"items":[],"x":1}`,
		"item not object":  `{"format":1,"items":[1]}`,
		"unknown in item":  `{"format":1,"items":[{"keys":"A","function":"f","color":"red"}]}`,
		"no keys":          `{"format":1,"items":[{"function":"f"}]}`,
		"bad keys":         `{"format":1,"items":[{"keys":"Ctrl+","function":"f"}]}`,
		"sequence keys":    `{"format":1,"items":[{"keys":["A"],"function":"f"}]}`,
		"empty function":   `{"format":1,"items":[{"keys":"A","function":""}]}`,
		"long function":    `{"format":1,"items":[{"keys":"A","function":"` + long + `"}]}`,
		"long name":        `{"format":1,"items":[{"keys":"A","function":"f","name":"` + long + `"}]}`,
		"name number":      `{"format":1,"items":[{"keys":"A","function":"f","name":1}]}`,
		"path not array":   `{"format":1,"items":[{"keys":"A","function":"f","path":"x"}]}`,
		"path number":      `{"format":1,"items":[{"keys":"A","function":"f","path":[1]}]}`,
		"path too long":    `{"format":1,"items":[{"keys":"A","function":"f","path":["1","2","3","4","5","6","7","8","9"]}]}`,
		"duplicate keys":   `{"format":1,"items":[{"keys":"Ctrl+Z","function":"f"},{"keys":"control+z","function":"g"}]}`,
		"too many items":   many,
	}
	for name, text := range tests {
		if _, err := ParseItems([]byte(text)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := ParseItems([]byte("{\"format\":1,\n\"items\":[,]}")); err == nil || !strings.Contains(err.Error(), "2:") {
		t.Errorf("syntax error should say where: %v", err)
	}
}

func TestMergeItems(t *testing.T) {
	parent := []Item{{Keys: "A", Function: "p1"}, {Keys: "B", Function: "p2"}}
	child := []Item{{Keys: "B", Function: "c2"}, {Keys: "C", Function: "c3"}}
	got := MergeItems(parent, child)
	want := []Item{{Keys: "A", Function: "p1"}, {Keys: "B", Function: "c2"}, {Keys: "C", Function: "c3"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged = %+v", got)
	}
	if parent[1].Function != "p2" {
		t.Error("parent was changed")
	}
}

func FuzzParseItems(f *testing.F) {
	f.Add([]byte(`{"format":1,"items":[]}`))
	f.Add([]byte(`{"format":1,"items":[{"keys":"Ctrl+Z","function":"command:a/b","name":"n","path":["x"]}]}`))
	f.Add([]byte(`{"format":1,"items":[{"keys":"P","function":"tool:00"},{"keys":"p","function":"x"}]}`))
	f.Add([]byte("\xef\xbb\xbf{\"format\":1,\"items\":[]}"))
	f.Add([]byte(`{"format":1,"items":[{"keys":"A","function":"f"},]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		items, err := ParseItems(data)
		if err != nil {
			return
		}
		text := FormatItems(items)
		back, err := ParseItems(text)
		if err != nil {
			t.Fatalf("formatted items do not parse: %v\n%s", err, text)
		}
		bothEmpty := len(items) == 0 && len(back) == 0
		if !reflect.DeepEqual(items, back) && !bothEmpty {
			t.Fatalf("round trip changed items: %+v -> %+v", items, back)
		}
		if string(FormatItems(back)) != string(text) {
			t.Fatal("format is not stable")
		}
	})
}
