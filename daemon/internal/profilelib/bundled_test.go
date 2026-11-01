package profilelib

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// dataFile reads a file of the data folder, "" when it is missing.
func dataFile(t *testing.T, dir, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

// rulesFor returns the profiles config.json's rules select for exe.
func rulesFor(t *testing.T, l *Library, exe string) []string {
	t.Helper()
	c, _, err := l.ReadConfig()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range c.Rules {
		if strings.EqualFold(r.Exe, exe) && r.Profile != nil {
			ids = append(ids, *r.Profile)
		}
	}
	return ids
}

// The shipped profiles are valid, without warnings, carry no path of a
// machine, and each has an application to select it.
func TestBundledProfiles(t *testing.T) {
	if got := BundledIDs(); len(got) != 1 || got[0] != "csp" {
		t.Fatalf("shipped profiles %v, want [csp]", got)
	}
	for _, b := range bundledProfiles {
		data, err := BundledText(b.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !ValidID(b.ID) || b.Exe == "" {
			t.Errorf("shipped profile %q has an invalid ID or no application", b.ID)
		}
		p, ds := New(t.TempDir()).Parse(b.ID, data)
		if ds.Err() != nil || len(ds) != 0 || p == nil {
			t.Errorf("shipped profile %s: %v", b.ID, ds)
			continue
		}
		if p.Name != "CSP" {
			t.Errorf("shipped profile %s is called %q", b.ID, p.Name)
		}
		// No user folder, drive path or script of the person who made it.
		if m := regexp.MustCompile(`(?i)[a-z]:\\|\\\\|users[\\/]|appdata|"script"|"file"`).Find(data); m != nil {
			t.Errorf("shipped profile %s holds %q", b.ID, m)
		}
	}
}

func TestEnsureBundledAddsOnceWithItsRule(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	result, err := l.EnsureBundled()
	if err != nil || len(result.Added) != 1 || result.Added[0] != "csp" || len(result.Rules) != 1 || result.Rules[0] != "CLIPStudioPaint.exe" {
		t.Fatalf("first start: %+v %v", result, err)
	}
	want, _ := BundledText("csp")
	if data, err := l.Read("csp"); err != nil || string(data) != string(want) {
		t.Fatalf("csp is %q, %v", data, err)
	}
	if got := rulesFor(t, l, "CLIPStudioPaint.exe"); len(got) != 1 || got[0] != "csp" {
		t.Fatalf("rules for CLIP STUDIO PAINT: %v", got)
	}
	if got := dataFile(t, dir, BundledFile); got != `{"offered":["csp"]}`+"\n" {
		t.Fatalf("%s holds %q", BundledFile, got)
	}

	// A second start does nothing, and a profile and a rule the person deleted
	// stay deleted.
	config := dataFile(t, dir, "config.json")
	if result, err := l.EnsureBundled(); err != nil || len(result.Added)+len(result.Rules) != 0 {
		t.Fatalf("second start: %+v %v", result, err)
	}
	if dataFile(t, dir, "config.json") != config {
		t.Fatal("a second start changed config.json")
	}
	if err := os.RemoveAll(filepath.Join(dir, "profiles", "csp")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schemaVersion":0}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if result, err := l.EnsureBundled(); err != nil || len(result.Added)+len(result.Rules) != 0 {
		t.Fatalf("after the person deleted them: %+v %v", result, err)
	}
	if _, err := l.Read("csp"); !os.IsNotExist(err) {
		t.Fatal("a deleted csp came back", err)
	}
	if got := rulesFor(t, l, "CLIPStudioPaint.exe"); len(got) != 0 {
		t.Fatalf("a deleted rule came back: %v", got)
	}
}

func TestEnsureBundledNeverTouchesThePersonsOwn(t *testing.T) {
	const own = `{"schemaVersion":1,"name":"CSP","layers":{"base":{"south":{"press":{"type":"keys","keys":"B"}}}}}`
	t.Run("a profile of the same ID", func(t *testing.T) {
		dir := t.TempDir()
		fixture(t, dir, "csp", own)
		config := `{"schemaVersion":0}`
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		l := New(dir)
		if result, err := l.EnsureBundled(); err != nil || len(result.Added)+len(result.Rules) != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		if data, _ := l.Read("csp"); string(data) != own {
			t.Fatalf("the person's csp was replaced: %q", data)
		}
		// No rule for a profile padmate did not add.
		if dataFile(t, dir, "config.json") != config {
			t.Fatal("config.json changed")
		}
		// Offered: deleting the person's csp later does not bring padmate's.
		if err := os.RemoveAll(filepath.Join(dir, "profiles", "csp")); err != nil {
			t.Fatal(err)
		}
		if result, err := l.EnsureBundled(); err != nil || len(result.Added) != 0 {
			t.Fatalf("%+v %v", result, err)
		}
	})
	t.Run("a profile of the same name", func(t *testing.T) {
		dir := t.TempDir()
		fixture(t, dir, "mine", own)
		l := New(dir)
		if result, err := l.EnsureBundled(); err != nil || len(result.Added) != 0 {
			t.Fatalf("%+v %v", result, err)
		}
		if _, err := l.Read("csp"); !os.IsNotExist(err) {
			t.Fatal("a second profile named CSP was added", err)
		}
	})
	t.Run("a rule for the application", func(t *testing.T) {
		for _, exe := range []string{"CLIPStudioPaint.exe", "clipstudiopaint.EXE", `C:\Program Files\CELSYS\CLIP STUDIO 1.5\CLIP STUDIO PAINT\CLIPStudioPaint.exe`} {
			dir := t.TempDir()
			config := `{"schemaVersion":0,"rules":[{"exe":` + mustJSON(t, exe) + `,"profile":"mine"}]}`
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture(t, dir, "mine", validProfile)
			l := New(dir)
			result, err := l.EnsureBundled()
			if err != nil || len(result.Added) != 1 || len(result.Rules) != 0 {
				t.Fatalf("%s: %+v %v", exe, result, err)
			}
			if dataFile(t, dir, "config.json") != config {
				t.Fatalf("%s: config.json changed although it has a rule for the application", exe)
			}
		}
	})
}

func TestEnsureBundledKeepsConfigJSON(t *testing.T) {
	dir := t.TempDir()
	config := "{\n  \"schemaVersion\": 0,\n  \"rules\": [\n    {\n      \"exe\": \"krita.exe\",\n      \"profile\": \"default\"\n    }\n  ],\n  \"devices\": {},\n  \"output\": {\n    \"sendToOwnWindow\": true\n  }\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	if result, err := l.EnsureBundled(); err != nil || len(result.Rules) != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	want := "{\n  \"schemaVersion\": 0,\n  \"rules\": [\n    {\n      \"exe\": \"krita.exe\",\n      \"profile\": \"default\"\n    },\n    {\n      \"exe\": \"CLIPStudioPaint.exe\",\n      \"profile\": \"csp\"\n    }\n  ],\n  \"devices\": {},\n  \"output\": {\n    \"sendToOwnWindow\": true\n  }\n}\n"
	if got := dataFile(t, dir, "config.json"); got != want {
		t.Fatalf("config.json:\n%s\nwant:\n%s", got, want)
	}
	// A config.json without rules gets them at its end; one with errors is left.
	for _, c := range []struct{ in, want string }{
		{`{"schemaVersion":0,"output":{"sendToOwnWindow":false}}`, "{\n  \"schemaVersion\": 0,\n  \"output\": {\n    \"sendToOwnWindow\": false\n  },\n  \"rules\": [\n    {\n      \"exe\": \"CLIPStudioPaint.exe\",\n      \"profile\": \"csp\"\n    }\n  ]\n}\n"},
		{`{"schemaVersion":0,"rules":"x"}`, `{"schemaVersion":0,"rules":"x"}`},
	} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(c.in), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _ = New(dir).EnsureBundled()
		if got := dataFile(t, dir, "config.json"); got != c.want {
			t.Errorf("config.json %s became\n%s\nwant\n%s", c.in, got, c.want)
		}
	}
}

func TestEnsureBundledWithABadRecordAddsNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, BundledFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := New(dir)
	if result, err := l.EnsureBundled(); err == nil || len(result.Added) != 0 {
		t.Fatalf("%+v %v", result, err)
	}
	if _, err := l.Read("csp"); !os.IsNotExist(err) {
		t.Fatal("csp added on a guess", err)
	}
}

func mustJSON(t *testing.T, s string) string {
	t.Helper()
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
