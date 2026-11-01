package profilelib

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
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

func TestStateJoyCon2(t *testing.T) {
	left := profile.JoyCon2Controller{Addr: "C8:48:05:00:00:01", Serial: "XKW00000000001", Side: "L"}
	right := profile.JoyCon2Controller{Addr: "C8:48:05:00:00:02", Serial: "XKW00000000002", Side: "R"}
	tests := []struct {
		name string
		raw  string
		want []profile.JoyCon2Controller
		bad  bool
	}{
		{name: "an old file has none", raw: `{"schemaVersion":0,"lastProfile":"paint"}`},
		{name: "empty list", raw: `{"schemaVersion":0,"joycon2":[]}`, want: []profile.JoyCon2Controller{}},
		{name: "two", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"XKW00000000001","side":"L"},{"addr":"C8:48:05:00:00:02","serial":"XKW00000000002","side":"R"}]}`, want: []profile.JoyCon2Controller{left, right}},
		{name: "lower case address", raw: `{"schemaVersion":0,"joycon2":[{"addr":"c8:48:05:00:00:01","serial":"x","side":"L"}]}`, bad: true},
		{name: "short address", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05","serial":"x","side":"L"}]}`, bad: true},
		{name: "unknown side", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"x","side":"M"}]}`, bad: true},
		{name: "empty serial", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"","side":"L"}]}`, bad: true},
		{name: "missing side", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"x"}]}`, bad: true},
		{name: "extra member", raw: `{"schemaVersion":0,"joycon2":[{"addr":"C8:48:05:00:00:01","serial":"x","side":"L","name":"a"}]}`, bad: true},
		{name: "not a list", raw: `{"schemaVersion":0,"joycon2":{}}`, bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(tc.raw), 0o600); err != nil {
				t.Fatal(err)
			}
			lib := New(dir)
			got, err := lib.ReadState()
			if tc.bad {
				if !errors.Is(err, ErrInvalidState) {
					t.Fatalf("accepted: %+v, %v", got, err)
				}
				return
			}
			if err != nil || len(got.JoyCon2) != len(tc.want) {
				t.Fatalf("got %+v, %v", got.JoyCon2, err)
			}
			for i := range tc.want {
				if got.JoyCon2[i] != tc.want[i] {
					t.Fatalf("entry %d: %+v", i, got.JoyCon2[i])
				}
			}
			// What was read is written back unchanged, other members included.
			if err := lib.WriteState(got); err != nil {
				t.Fatal(err)
			}
			again, err := lib.ReadState()
			if err != nil || !slices.Equal(again.JoyCon2, got.JoyCon2) || again.LastProfile != got.LastProfile {
				t.Fatalf("roundtrip: %+v %v", again, err)
			}
		})
	}
}

func TestMemoryJoyCon2(t *testing.T) {
	left := profile.JoyCon2Controller{Addr: "C8:48:05:00:00:01", Serial: "S1", Side: "L"}
	right := profile.JoyCon2Controller{Addr: "C8:48:05:00:00:02", Serial: "S2", Side: "R"}
	moved := profile.JoyCon2Controller{Addr: "C8:48:05:00:00:03", Serial: "S1", Side: "L"}
	dir := t.TempDir()
	lib := New(dir)
	m := NewMemory(profile.State{LastProfile: "paint", LastProfileSet: true}, true)

	// Nothing changed, nothing to write.
	if err := m.Flush(lib); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("wrote a state nobody changed", err)
	}

	m.RememberJoyCon2(left)
	m.RememberJoyCon2(right)
	m.RememberJoyCon2(right) // the same again: no change
	if err := m.Flush(lib); err != nil {
		t.Fatal(err)
	}
	state, err := lib.ReadState()
	if err != nil || state.LastProfile != "paint" || !reflect.DeepEqual(state.JoyCon2, []profile.JoyCon2Controller{left, right}) {
		t.Fatalf("saved %+v, %v", state, err)
	}

	// The same serial under a new address replaces the old entry and goes last.
	m.RememberJoyCon2(moved)
	if got := m.JoyCon2(); !reflect.DeepEqual(got, []profile.JoyCon2Controller{right, moved}) {
		t.Fatalf("after a changed address: %+v", got)
	}
	// The returned list is a copy.
	got := m.JoyCon2()
	got[0].Serial = "changed"
	if m.JoyCon2()[0].Serial != "S2" {
		t.Fatal("JoyCon2 shares its list")
	}

	if m.ForgetJoyCon2("nope") {
		t.Fatal("forgot a controller that is not remembered")
	}
	if !m.ForgetJoyCon2("S2") {
		t.Fatal("did not forget S2")
	}
	if err := m.Flush(lib); err != nil {
		t.Fatal(err)
	}
	state, err = lib.ReadState()
	if err != nil || !reflect.DeepEqual(state.JoyCon2, []profile.JoyCon2Controller{moved}) {
		t.Fatalf("after forget: %+v, %v", state, err)
	}
	m.ForgetJoyCon2("S1")
	if err := m.Flush(lib); err != nil {
		t.Fatal(err)
	}
	if state, err = lib.ReadState(); err != nil || len(state.JoyCon2) != 0 || state.LastProfile != "paint" {
		t.Fatalf("after the last forget: %+v, %v", state, err)
	}

	// The list is bounded.
	for i := range MaxJoyCon2 + 3 {
		m.RememberJoyCon2(profile.JoyCon2Controller{Addr: fmt.Sprintf("C8:48:05:00:00:%02X", i), Serial: fmt.Sprintf("N%d", i), Side: "R"})
	}
	list := m.JoyCon2()
	if len(list) != MaxJoyCon2 || list[0].Serial != "N3" || list[len(list)-1].Serial != fmt.Sprintf("N%d", MaxJoyCon2+2) {
		t.Fatalf("bound: %d entries from %s to %s", len(list), list[0].Serial, list[len(list)-1].Serial)
	}
	if err := m.Flush(lib); err != nil {
		t.Fatalf("a full list does not save: %v", err)
	}
}
