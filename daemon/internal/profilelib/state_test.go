package profilelib

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func TestConfigAndStateFiles(t *testing.T) {
	dir := t.TempDir()
	lib := New(dir)
	c, _, err := lib.ReadConfig()
	if err != nil || !c.Enabled {
		t.Fatal(c, err)
	}
	s, err := lib.ReadState()
	if err != nil || s.LastProfile != "" {
		t.Fatal(s, err)
	}
	key := strings.Repeat("a0", 16) + "#1/s:device"
	s = profile.State{LastProfile: "paint", Devices: map[string]profile.DeviceMemory{
		key: {Profiles: map[string]profile.LayerMemory{"paint": {Base: "mode2", Toggled: []string{"alt"}, Cycles: map[string]int{"/common/back/press": 1}}}},
	}}
	if err := lib.WriteState(s); err != nil {
		t.Fatal(err)
	}
	got, err := lib.ReadState()
	if err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	bad := s.Clone()
	bad.LastProfile = "../escape"
	if err := lib.WriteState(bad); err == nil {
		t.Fatal("saved invalid state")
	}
	after, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil || string(before) != string(after) {
		t.Fatal("failure changed state", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schemaVersion":0,"onUnmatched":"none","devices":{"bad":{"ignored":true}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := lib.ReadConfig(); err == nil {
		t.Fatal("accepted invalid device identity")
	}
	for _, raw := range []string{`{"schemaVersion":1}`, `{"schemaVersion":0,"devices":null}`, `{"schemaVersion":0,"lastProfile":"paint","lastProfile":"other"}`} {
		if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(raw), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := lib.ReadState(); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestMemoryRestorePolicy(t *testing.T) {
	key := strings.Repeat("b1", 16)
	mem := NewMemory(profile.State{}, true)
	mem.Save(key, "paint", profile.LayerMemory{Base: "base", Toggled: []string{"alt"}})
	mem.SetLastProfile("paint")
	got, ok := mem.Load(key, "paint")
	if !ok {
		t.Fatal("missing state")
	}
	got.Toggled[0] = "changed"
	got, _ = mem.Load(key, "paint")
	if got.Toggled[0] != "alt" {
		t.Fatal("state alias")
	}
	lib := New(t.TempDir())
	if err := mem.Flush(lib); err != nil {
		t.Fatal(err)
	}
	saved, err := lib.ReadState()
	if err != nil {
		t.Fatal(err)
	}
	restored := NewMemory(saved, true)
	if got, ok := restored.Load(key, "paint"); !ok || got.Toggled[0] != "alt" {
		t.Fatal(got, ok)
	}
	if _, ok := restored.Load(key, "other"); ok {
		t.Fatal("profiles share state")
	}
	if _, ok := NewMemory(saved, false).Load(key, "paint"); ok {
		t.Fatal("restoreLayerState false ignored")
	}
	if _, ok := restored.Load(strings.Repeat("b2", 16), "paint"); ok {
		t.Fatal("different device restored state")
	}
}

func TestStateRenameFailureCleansTemporaryFile(t *testing.T) {
	dir := t.TempDir()
	// A nonempty directory cannot be replaced by the temporary state file.
	target := filepath.Join(dir, "state.json")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "keep")
	if err := os.WriteFile(marker, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := New(dir).WriteState(profile.State{}); err == nil {
		t.Fatal("rename unexpectedly succeeded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "state.json" {
		t.Fatal("temporary file leaked", entries, err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "old" {
		t.Fatal("failed write changed destination", err)
	}
}
