package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

const importedText = `{
  "schemaVersion": 1,
  "name": "From AntiMicroX",
  "defaultLayer": "base",
  "layers": {
    "base": { "south": { "press": { "type": "keys", "keys": "Space" } } }
  }
}
`

func TestImportProfile(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if _, err := l.EnsureDefault(); err != nil {
		t.Fatal(err)
	}
	imported, err := l.ImportProfile([]byte(importedText), "", false)
	if err != nil || imported.ID != "from-antimicrox" || imported.Name != "From AntiMicroX" {
		t.Fatalf("%+v %v", imported, err)
	}
	// The file is the text as it was given, and the library reads it.
	if got, err := l.Read("from-antimicrox"); err != nil || string(got) != importedText {
		t.Fatalf("%q %v", got, err)
	}
	if imported.Revision != Revision([]byte(importedText)) || string(imported.Data) != importedText {
		t.Errorf("revision %s", imported.Revision)
	}
	entries, err := l.List()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range entries {
		found = found || e.ID == "from-antimicrox" && e.Name == "From AntiMicroX" && len(e.Diagnostics) == 0
	}
	if !found {
		t.Errorf("not listed: %+v", entries)
	}

	// The name is taken: an error, with the profile that has it.
	_, err = l.ImportProfile([]byte(importedText), "", false)
	var taken *NameTakenError
	if !errors.As(err, &taken) || taken.ID != "from-antimicrox" {
		t.Fatalf("%v", err)
	}
	// A name of its own, which also changes the text.
	imported, err = l.ImportProfile([]byte(importedText), "  Mine ", false)
	if err != nil || imported.Name != "Mine" || !strings.Contains(string(imported.Data), `"name": "Mine"`) {
		t.Fatalf("%+v %v", imported, err)
	}
	// Unique: numbered names, and the original text with the name changed.
	for i, want := range []string{"From AntiMicroX 2", "From AntiMicroX 3", "From AntiMicroX 4"} {
		imported, err = l.ImportProfile([]byte(importedText), "", true)
		if err != nil || imported.Name != want {
			t.Fatalf("%d: %+v %v", i, imported, err)
		}
	}
	// The default's names count as taken.
	imported, err = l.ImportProfile([]byte(importedText), "默认", true)
	if err != nil || imported.Name != "默认 2" {
		t.Fatalf("%+v %v", imported, err)
	}
	assertNoCopyLeftovers(t, dir, "from-antimicrox", "from-antimicrox-2", "from-antimicrox-3", "from-antimicrox-4", DefaultID, "mine", "2")
}

func TestImportProfileRefusals(t *testing.T) {
	l := New(t.TempDir())
	for name, tc := range map[string]struct {
		data string
		name string
	}{
		"not a profile": {data: "{", name: "x"},
		"no layers":     {data: `{"schemaVersion":1,"name":"x"}`, name: "x"},
		"too long":      {data: importedText, name: strings.Repeat("名", MaxNameRunes+1)},
		"too large":     {data: strings.Repeat(" ", MaxDocumentBytes+1), name: "x"},
	} {
		if _, err := l.ImportProfile([]byte(tc.data), tc.name, true); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(l.dir, "profiles")); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

func TestNumberedName(t *testing.T) {
	long := strings.Repeat("名", MaxNameRunes)
	got := numberedName(long, 12)
	if utf8.RuneCountInString(got) != MaxNameRunes || !strings.HasSuffix(got, " 12") {
		t.Errorf("%q has %d characters", got, utf8.RuneCountInString(got))
	}
	if got := numberedName("a", 2); got != "a 2" {
		t.Errorf("%q", got)
	}
}
