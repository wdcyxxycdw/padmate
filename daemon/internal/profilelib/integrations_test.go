package profilelib

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func itemsOf(keys ...string) []integration.Item {
	var out []integration.Item
	for _, k := range keys {
		out = append(out, integration.Item{Keys: k, Function: "command:basiccommand/" + strings.ToLower(k)})
	}
	return out
}

func sidecar(dir, id, integ string) string {
	return filepath.Join(dir, "profiles", id, "integrations", integ+".json")
}

func TestIntegrationMissingIsEmpty(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	f, err := l.Integration("paint", "csp")
	if err != nil || f.Exists || f.Revision != "" || len(f.Items) != 0 {
		t.Fatalf("%+v %v", f, err)
	}
	if _, err := l.Integration("nope", "csp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing profile: %v", err)
	}
	if _, err := l.Integration("paint", "CSP"); err == nil {
		t.Error("invalid integration id accepted")
	}
	if _, err := l.Integration("../x", "csp"); err == nil {
		t.Error("invalid profile id accepted")
	}
}

func TestPutIntegration(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)

	saved, err := l.PutIntegration("paint", "csp", itemsOf("Ctrl+Z"), "")
	if err != nil || saved.HistoryErr != nil {
		t.Fatal(err, saved.HistoryErr)
	}
	data, err := os.ReadFile(sidecar(dir, "paint", "csp"))
	if err != nil || saved.Revision != Revision(data) {
		t.Fatalf("revision %q, file %q, %v", saved.Revision, data, err)
	}
	if string(data) != string(integration.FormatItems(itemsOf("Ctrl+Z"))) {
		t.Errorf("file = %s", data)
	}
	f, err := l.Integration("paint", "csp")
	if err != nil || !f.Exists || f.Revision != saved.Revision || !reflect.DeepEqual(f.Items, itemsOf("Ctrl+Z")) {
		t.Fatalf("%+v %v", f, err)
	}
	// No history yet: nothing was replaced.
	if _, err := os.Stat(filepath.Join(dir, "profiles", "paint", ".history")); err == nil {
		t.Error("history made for a new file")
	}

	// Stale revision, and a create over an existing file.
	var ce *ConflictError
	if _, err := l.PutIntegration("paint", "csp", itemsOf("A"), "stale"); !errors.As(err, &ce) || ce.Current != saved.Revision {
		t.Errorf("stale: %v", err)
	}
	if _, err := l.PutIntegration("paint", "csp", itemsOf("A"), ""); !errors.As(err, &ce) {
		t.Errorf("create over existing: %v", err)
	}
	if _, err := l.PutIntegration("paint", "other", itemsOf("A"), "x"); !errors.As(err, &ce) {
		t.Errorf("revision against a missing file: %v", err)
	}
	// Duplicate keys are refused before anything is written.
	dup := []integration.Item{{Keys: "A", Function: "f"}, {Keys: "a", Function: "g"}}
	if _, err := l.PutIntegration("paint", "csp", dup, saved.Revision); err == nil {
		t.Error("duplicate keys accepted")
	}
	if _, err := l.PutIntegration("nope", "csp", nil, ""); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing profile: %v", err)
	}

	// Replacing keeps the old bytes in the history, and profile.json's own
	// history does not see them.
	second, err := l.PutIntegration("paint", "csp", itemsOf("Ctrl+Y"), saved.Revision)
	if err != nil || second.HistoryErr != nil {
		t.Fatal(err)
	}
	snaps, err := os.ReadDir(filepath.Join(dir, "profiles", "paint", ".history"))
	if err != nil || len(snaps) != 1 || !integrationSnapshotPattern.MatchString(snaps[0].Name()) {
		t.Fatalf("history = %v %v", snaps, err)
	}
	old, _ := os.ReadFile(filepath.Join(dir, "profiles", "paint", ".history", snaps[0].Name()))
	if Revision(old) != saved.Revision {
		t.Error("snapshot is not the replaced file")
	}
	if h, err := l.History("paint"); err != nil || len(h) != 0 {
		t.Errorf("profile history = %v %v", h, err)
	}
	// Writing the same bytes again keeps no snapshot.
	if _, err := l.PutIntegration("paint", "csp", itemsOf("Ctrl+Y"), second.Revision); err != nil {
		t.Fatal(err)
	}
	if snaps, _ := os.ReadDir(filepath.Join(dir, "profiles", "paint", ".history")); len(snaps) != 1 {
		t.Errorf("history = %d", len(snaps))
	}
}

func TestPutIntegrationPrunesPerIntegration(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	rev := ""
	for i := range MaxIntegrationSnapshots + 5 {
		items := []integration.Item{{Keys: "A", Function: "f" + string(rune('a'+i%26)) + string(rune('a'+i/26))}}
		saved, err := l.PutIntegration("paint", "csp", items, rev)
		if err != nil || saved.HistoryErr != nil {
			t.Fatal(i, err, saved.HistoryErr)
		}
		rev = saved.Revision
	}
	// A snapshot of another integration and a profile snapshot stay.
	hist := filepath.Join(dir, "profiles", "paint", ".history")
	writeFile(t, filepath.Join(hist, "integrations-other-20200101-000000.json"), "{}")
	writeFile(t, filepath.Join(hist, "20200101-000000-0123abcd.json"), validProfile)
	if _, err := l.PutIntegration("paint", "csp", itemsOf("B"), rev); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(hist)
	csp, other, prof := 0, 0, 0
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "integrations-csp-"):
			csp++
		case strings.HasPrefix(e.Name(), "integrations-other-"):
			other++
		default:
			prof++
		}
	}
	if csp != MaxIntegrationSnapshots || other != 1 || prof != 1 {
		t.Errorf("csp %d other %d profile %d", csp, other, prof)
	}
}

func TestIntegrationRefusesLinksAndBadFiles(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	writeFile(t, sidecar(dir, "paint", "csp"), `{"format":1,"items":[{"keys":"A"}]}`)
	f, err := l.Integration("paint", "csp")
	if err == nil || !f.Exists || f.Revision == "" {
		t.Errorf("bad file: %+v %v", f, err)
	}
	// It can be replaced by the revision it has.
	if _, err := l.PutIntegration("paint", "csp", itemsOf("A"), f.Revision); err != nil {
		t.Errorf("replace a bad file: %v", err)
	}
	// A folder where the file belongs.
	if err := os.Mkdir(sidecar(dir, "paint", "dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	var ue *UnsupportedError
	if _, err := l.Integration("paint", "dir"); !errors.As(err, &ue) {
		t.Errorf("folder: %v", err)
	}
	if _, err := l.PutIntegration("paint", "dir", nil, ""); !errors.As(err, &ue) {
		t.Errorf("put over folder: %v", err)
	}
	// A file where the integrations folder belongs.
	fixture(t, dir, "plain", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "plain", "integrations"), "x")
	if _, err := l.Integration("plain", "csp"); !errors.As(err, &ue) {
		t.Errorf("integrations is a file: %v", err)
	}
	if _, err := l.PutIntegration("plain", "csp", nil, ""); !errors.As(err, &ue) {
		t.Errorf("put into a file: %v", err)
	}
}

func TestEffectiveIntegration(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "base", validProfile)
	fixture(t, dir, "mid", strings.Replace(validProfile, "{", `{"extends":"base",`, 1))
	fixture(t, dir, "leaf", strings.Replace(validProfile, "{", `{"extends":"mid",`, 1))
	put := func(id string, items ...integration.Item) {
		t.Helper()
		if _, err := l.PutIntegration(id, "csp", items, ""); err != nil {
			t.Fatal(err)
		}
	}
	put("base", integration.Item{Keys: "A", Function: "base-a"}, integration.Item{Keys: "B", Function: "base-b"})
	put("mid", integration.Item{Keys: "B", Function: "mid-b"})
	put("leaf", integration.Item{Keys: "A", Function: "leaf-a"}, integration.Item{Keys: "C", Function: "leaf-c"})
	got, err := l.EffectiveIntegration("leaf", "csp")
	if err != nil {
		t.Fatal(err)
	}
	want := []integration.Item{{Keys: "A", Function: "leaf-a"}, {Keys: "B", Function: "mid-b"}, {Keys: "C", Function: "leaf-c"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("effective = %+v", got)
	}
}

func TestCopyExportImportCarryIntegrations(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	l := New(src)
	fixture(t, src, "paint", validProfile)
	saved, err := l.PutIntegration("paint", "csp", itemsOf("Ctrl+Z", "P"), "")
	if err != nil {
		t.Fatal(err)
	}
	// Its snapshot is history, which a copy and an export leave out.
	if _, err := l.PutIntegration("paint", "csp", itemsOf("Ctrl+Z", "P", "Q"), saved.Revision); err != nil {
		t.Fatal(err)
	}
	want, _ := l.Integration("paint", "csp")

	if _, err := l.Copy("paint", "paint-2"); err != nil {
		t.Fatal(err)
	}
	if got, err := l.Integration("paint-2", "csp"); err != nil || got.Revision != want.Revision || !reflect.DeepEqual(got.Items, want.Items) {
		t.Errorf("copy: %+v %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(src, "profiles", "paint-2", ".history")); err == nil {
		t.Error("copy has history")
	}

	exported, err := l.Export("paint", "test")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range a.Paths() {
		found = found || p == "integrations/csp.json"
		if strings.Contains(p, "history") {
			t.Error(p)
		}
	}
	if !found {
		t.Errorf("paths = %v", a.Paths())
	}
	imported, err := New(dst).Import(a, ImportOptions{Macros: MacrosReplace})
	if err != nil {
		t.Fatal(err)
	}
	got, err := New(dst).Integration(imported.ID, "csp")
	if err != nil || !reflect.DeepEqual(got.Items, want.Items) {
		t.Errorf("import: %+v %v", got, err)
	}

	// Remove takes the folder with it.
	rev := Revision([]byte(validProfile))
	if err := l.Remove("paint-2", rev); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(src, "profiles", "paint-2")); err == nil {
		t.Error("not removed")
	}
}

func TestArchiveValidatesIntegrations(t *testing.T) {
	good := `{"format":1,"items":[{"keys":"A","function":"f"}]}`
	entries := func(extra ...zipEntry) []byte { return makeZip(t, append(validEntries(), extra...)...) }
	if _, err := ReadArchive(entries(zipEntry{name: "profile/integrations/csp.json", data: good})); err != nil {
		t.Fatal(err)
	}
	for name, e := range map[string]zipEntry{
		"bad content":     {name: "profile/integrations/csp.json", data: `{"format":1,"items":[{"keys":"A"}]}`},
		"trailing comma":  {name: "profile/integrations/csp.json", data: `{"format":1,"items":[],}`},
		"duplicate keys":  {name: "profile/integrations/csp.json", data: `{"format":1,"items":[{"keys":"A","function":"f"},{"keys":"a","function":"g"}]}`},
		"upper-case name": {name: "profile/integrations/CSP.json", data: good},
		"bad id":          {name: "profile/integrations/1x.json", data: good},
		"upper folder":    {name: "profile/Integrations/csp.json", data: `[]`},
	} {
		_, err := ReadArchive(entries(e))
		var ae *ArchiveError
		if !errors.As(err, &ae) || ae.Reason != ArchiveIntegrationReason {
			t.Errorf("%s: %v", name, err)
		}
	}
	// A deeper file is not a sidecar and is not checked.
	if _, err := ReadArchive(entries(zipEntry{name: "profile/integrations/sub/x.json", data: `[]`})); err != nil {
		t.Errorf("nested json: %v", err)
	}
}

func TestExportRefusesBadIntegration(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	fixture(t, dir, "paint", validProfile)
	writeFile(t, sidecar(dir, "paint", "csp"), `{"format":1,"items":[{"keys":"A"}]}`)
	var ue *UnsupportedError
	if _, err := l.Export("paint", "t"); !errors.As(err, &ue) || !strings.Contains(ue.Path, "integrations/csp.json") {
		t.Errorf("export: %v", err)
	}
}

func TestMemoryDismissed(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	m := NewMemory(profile.State{}, true)
	if m.Dismissed("paint", "csp") != "" {
		t.Fatal("not empty")
	}
	m.SetDismissed("paint", "csp", "abc")
	m.SetDismissed("paint", "other", "def")
	m.SetDismissed("BAD", "csp", "x")
	m.SetDismissed("paint", "CSP", "x")
	if m.Dismissed("paint", "csp") != "abc" || m.Dismissed("paint", "other") != "def" || m.Dismissed("BAD", "csp") != "" {
		t.Fatal("values")
	}
	if err := m.Flush(l); err != nil {
		t.Fatal(err)
	}
	state, err := l.ReadState()
	if err != nil || state.Integrations["paint"]["csp"].Dismissed != "abc" {
		t.Fatalf("%+v %v", state, err)
	}
	// A new Memory from the saved state keeps it; clearing removes it.
	m2 := NewMemory(state, true)
	if m2.Dismissed("paint", "other") != "def" {
		t.Error("not restored")
	}
	m2.SetDismissed("paint", "csp", "")
	m2.SetDismissed("paint", "other", "")
	if m2.Dismissed("paint", "csp") != "" {
		t.Error("not cleared")
	}
	if m.Dismissed("paint", "csp") != "abc" {
		t.Error("the clone shares maps")
	}
	if err := m2.Flush(l); err != nil {
		t.Fatal(err)
	}
	if state, _ := l.ReadState(); len(state.Integrations) != 0 {
		t.Errorf("state = %+v", state)
	}
}
