package input_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// The GUIDs of the DeviceKey tests: a USB Xbox pad of the hidapi backend,
// an Xbox Series X pad of the XInput backend and an SDL virtual gamepad.
const (
	guidXbox    = "030003f05e0400001302000000006800"
	guidXInput  = "0300509d5e040000120b000000007801"
	guidVirtual = "ff005cd5657870207061640000007601"
)

func TestKeyOf(t *testing.T) {
	const path = `\\?\HID#VID_045E&PID_0B13#8&1a2b&0&0000#{4d1e55b2-f16f-11cf-88cb-001111000030}`
	tests := []struct {
		name string
		info *input.DeviceInfo
		want input.DeviceKey
		str  string
	}{
		{
			"serial", &input.DeviceInfo{GUID: guidXbox, Serial: "ABC123", Path: path},
			input.DeviceKey{GUID: guidXbox, Serial: "ABC123"}, guidXbox + "/s:ABC123",
		},
		{
			"path", &input.DeviceInfo{GUID: guidXbox, Path: path},
			input.DeviceKey{GUID: guidXbox, Path: path}, guidXbox + "/p:" + path,
		},
		{
			"neither", &input.DeviceInfo{GUID: guidVirtual},
			input.DeviceKey{GUID: guidVirtual}, guidVirtual,
		},
		// The path of an XInput gamepad is its slot, which does not count.
		{
			"XInput slot", &input.DeviceInfo{GUID: guidXInput, Path: "XInput#1"},
			input.DeviceKey{GUID: guidXInput}, guidXInput,
		},
		{"no GUID", &input.DeviceInfo{Serial: "x/p:y"}, input.DeviceKey{Serial: "x/p:y"}, "/s:x/p:y"},
		{"nothing", &input.DeviceInfo{Name: "pad", Vendor: 1, IsGamepad: true}, input.DeviceKey{}, ""},
		{"nil", nil, input.DeviceKey{}, ""},
	}
	for _, tt := range tests {
		k := input.KeyOf(tt.info)
		if k != tt.want {
			t.Errorf("%s: KeyOf = %+v, want %+v", tt.name, k, tt.want)
		}
		if s := k.String(); s != tt.str {
			t.Errorf("%s: String() = %q, want %q", tt.name, s, tt.str)
		}
	}
}

// TestKeyOfJoyCon checks the keys of Joy-Cons shaped like SDL's HIDAPI
// ones (IN-07): a lone Joy-Con by its serial, the MAC address, and the
// pair by its two serials, never by its fixed path.
func TestKeyOfJoyCon(t *testing.T) {
	const (
		guidL    = "050000007e0500000620000001006801"
		guidPair = "050000007e0500000820000001006803"
		serial1  = "aa-bb-cc-dd-ee-01"
		serial2  = "aa-bb-cc-dd-ee-02"
		pairPath = "nintendo_joycons_combined"
	)
	joycon := func(typ string, product uint16, guid, serial, path string) *input.DeviceInfo {
		return &input.DeviceInfo{
			Name: "Joy-Con", Type: typ, Vendor: 0x057e, Product: product,
			GUID: guid, Driver: "hidapi", Serial: serial, Path: path, IsGamepad: true,
		}
	}
	for _, tc := range []struct {
		name string
		info *input.DeviceInfo
		want input.DeviceKey
	}{
		{"L", joycon("joyconleft", 0x2006, guidL, serial1, "hid-path-1"), input.DeviceKey{GUID: guidL, Serial: serial1}},
		// Another L, even on the same path, is another key.
		{"another L", joycon("joyconleft", 0x2006, guidL, serial2, "hid-path-1"), input.DeviceKey{GUID: guidL, Serial: serial2}},
		{
			"pair", joycon("joyconpair", 0x2008, guidPair, serial1+","+serial2, pairPath),
			input.DeviceKey{GUID: guidPair, Serial: serial1 + "," + serial2},
		},
	} {
		if k := input.KeyOf(tc.info); k != tc.want {
			t.Errorf("%s: KeyOf = %+v, want %+v", tc.name, k, tc.want)
		}
	}
}

func TestDeviceKeyString(t *testing.T) {
	for _, tt := range []struct {
		k    input.DeviceKey
		want string
	}{
		{input.DeviceKey{GUID: guidVirtual, N: 1}, guidVirtual + "#1"},
		{input.DeviceKey{GUID: guidXbox, Path: `\\?\HID#VID_045E`, N: 1}, guidXbox + `#1/p:\\?\HID#VID_045E`},
		{input.DeviceKey{GUID: guidXbox, Serial: "ABC123", N: 12}, guidXbox + "#12/s:ABC123"},
		{input.DeviceKey{N: 2}, "#2"},
		{input.DeviceKey{Path: "p"}, "/p:p"},
	} {
		if got := tt.k.String(); got != tt.want {
			t.Errorf("%+v.String() = %q, want %q", tt.k, got, tt.want)
		}
		b, err := tt.k.MarshalText()
		if err != nil || string(b) != tt.want {
			t.Errorf("%+v.MarshalText() = %q, %v; want %q", tt.k, b, err, tt.want)
		}
	}
	// JSON shows a key as its string, also as a map key.
	k := input.DeviceKey{GUID: guidXbox, Serial: "ABC123", N: 1}
	b, err := json.Marshal(map[string]any{"key": k, "held": map[input.DeviceKey]int{k: 2}})
	want := `{"held":{"` + guidXbox + `#1/s:ABC123":2},"key":"` + guidXbox + `#1/s:ABC123"}`
	if err != nil || string(b) != want {
		t.Errorf("json.Marshal = %s, %v; want %s", b, err, want)
	}
}

// pad returns the info of gamepad id with the given GUID, serial and
// path.
func pad(id input.DeviceID, guid, serial, path string) *input.DeviceInfo {
	return &input.DeviceInfo{ID: id, Name: "pad", Type: "xboxone", GUID: guid, Serial: serial, Path: path, IsGamepad: true}
}

// connect connects info to r and checks the result.
func connect(t *testing.T, r *input.Registry, info *input.DeviceInfo, want input.DeviceKey, wantBack bool) {
	t.Helper()
	if k, back := r.Connect(info); k != want || back != wantBack {
		t.Errorf("Connect(%d) = %v, %t; want %v, %t", info.ID, k, back, want, wantBack)
	}
}

// disconnect disconnects id from r and checks the result.
func disconnect(t *testing.T, r *input.Registry, id input.DeviceID, want input.DeviceKey) {
	t.Helper()
	if k, ok := r.Disconnect(id); k != want || !ok {
		t.Errorf("Disconnect(%d) = %v, %t; want %v, true", id, k, ok, want)
	}
}

// checkConnected checks r.Connected, and Key of each id in ids, which are
// the connected ids in connection order.
func checkConnected(t *testing.T, r *input.Registry, ids []input.DeviceID, want []input.DeviceKey) {
	t.Helper()
	if got := r.Connected(); !slices.Equal(got, want) {
		t.Errorf("Connected() = %v, want %v", got, want)
	}
	for i, id := range ids {
		if k, ok := r.Key(id); !ok || k != want[i] {
			t.Errorf("Key(%d) = %v, %t; want %v", id, k, ok, want[i])
		}
	}
}

// TestRegistryIdentical connects three identical gamepads without a
// serial, which get #0, #1 and #2, and reconnects them.
func TestRegistryIdentical(t *testing.T) {
	r := input.NewRegistry()
	k := func(n int) input.DeviceKey { return input.DeviceKey{GUID: guidVirtual, N: n} }
	for id := range input.DeviceID(3) {
		connect(t, r, pad(id+1, guidVirtual, "", ""), k(int(id)), false)
	}
	checkConnected(t, r, []input.DeviceID{1, 2, 3}, []input.DeviceKey{k(0), k(1), k(2)})

	// #1 and then #0 go. The first one back gets the lowest disconnected
	// number, #0, the next #1; a fourth pad the next free one.
	disconnect(t, r, 2, k(1))
	disconnect(t, r, 1, k(0))
	checkConnected(t, r, []input.DeviceID{3}, []input.DeviceKey{k(2)})
	connect(t, r, pad(4, guidVirtual, "", ""), k(0), true)
	connect(t, r, pad(5, guidVirtual, "", ""), k(1), true)
	connect(t, r, pad(6, guidVirtual, "", ""), k(3), false)
	checkConnected(t, r, []input.DeviceID{3, 4, 5, 6}, []input.DeviceKey{k(2), k(0), k(1), k(3)})

	// The same path also counts as identical; another path does not.
	p := func(n int) input.DeviceKey { return input.DeviceKey{GUID: guidXbox, Path: "p1", N: n} }
	connect(t, r, pad(7, guidXbox, "", "p1"), p(0), false)
	connect(t, r, pad(8, guidXbox, "", "p1"), p(1), false)
	connect(t, r, pad(9, guidXbox, "", "p2"), input.DeviceKey{GUID: guidXbox, Path: "p2"}, false)
}

// TestRegistryReconnect reconnects gamepads with a serial: the same
// serial gets its key back, another serial is another gamepad.
func TestRegistryReconnect(t *testing.T) {
	r := input.NewRegistry()
	a := input.DeviceKey{GUID: guidXbox, Serial: "A"}
	b := input.DeviceKey{GUID: guidXbox, Serial: "B"}
	connect(t, r, pad(1, guidXbox, "A", "p1"), a, false)
	disconnect(t, r, 1, a)
	if k, ok := r.Key(1); ok {
		t.Errorf("Key(1) = %v after Disconnect", k)
	}
	// The serial counts, not the path, which changes with the USB port.
	connect(t, r, pad(2, guidXbox, "A", "p2"), a, true)
	connect(t, r, pad(3, guidXbox, "B", "p1"), b, false)
	// Another GUID with the same serial is another gamepad too, such as
	// the same pad through another SDL backend (ADR-0009).
	disconnect(t, r, 2, a)
	connect(t, r, pad(4, guidVirtual, "A", ""), input.DeviceKey{GUID: guidVirtual, Serial: "A"}, false)
	connect(t, r, pad(5, guidXbox, "A", ""), a, true)
	checkConnected(t, r, []input.DeviceID{3, 4, 5}, []input.DeviceKey{b, {GUID: guidVirtual, Serial: "A"}, a})
}

// TestRegistryXInput reconnects an XInput gamepad, which has no serial, in
// another slot, as another XInput device took its slot meanwhile: it gets
// its key back.
func TestRegistryXInput(t *testing.T) {
	// The GameSir Nova 2 Lite of docs/hardware-matrix.md, named after its
	// slot.
	const guidNova = "0300fa67373500009810000000017801"
	r := input.NewRegistry()
	k := func(n int) input.DeviceKey { return input.DeviceKey{GUID: guidXInput, N: n} }
	connect(t, r, pad(1, guidXInput, "", "XInput#0"), k(0), false)
	disconnect(t, r, 1, k(0))
	connect(t, r, pad(2, guidNova, "", "XInput#0"), input.DeviceKey{GUID: guidNova}, false)
	connect(t, r, pad(3, guidXInput, "", "XInput#1"), k(0), true)
	// A second pad of the model is #1, whatever its slot.
	connect(t, r, pad(4, guidXInput, "", "XInput#2"), k(1), false)
	checkConnected(t, r, []input.DeviceID{2, 3, 4}, []input.DeviceKey{{GUID: guidNova}, k(0), k(1)})
}

// TestRegistryNoGUID connects gamepads without a GUID, serial or path, as
// in hand-written traces: they are registered, and the first one has the
// zero key.
func TestRegistryNoGUID(t *testing.T) {
	r := input.NewRegistry()
	connect(t, r, pad(1, "", "", ""), input.DeviceKey{}, false)
	connect(t, r, pad(2, "", "", ""), input.DeviceKey{N: 1}, false)
	checkConnected(t, r, []input.DeviceID{1, 2}, []input.DeviceKey{{}, {N: 1}})
	disconnect(t, r, 1, input.DeviceKey{})
	connect(t, r, pad(3, "", "", ""), input.DeviceKey{}, true)
}

// TestRegistryForgets checks that a Registry remembers at most 32
// disconnected gamepads, forgetting the one disconnected earliest.
func TestRegistryForgets(t *testing.T) {
	const remembered = 32
	r := input.NewRegistry()
	key := func(i int) input.DeviceKey { return input.DeviceKey{GUID: guidXbox, Serial: fmt.Sprint("S", i)} }
	for i := range remembered + 1 {
		connect(t, r, pad(input.DeviceID(i+1), guidXbox, key(i).Serial, ""), key(i), false)
	}
	for i := range remembered + 1 {
		disconnect(t, r, input.DeviceID(i+1), key(i))
	}
	// The first one is forgotten: it comes back as a new gamepad, with
	// the same key because nothing else has it. The second one is still
	// remembered.
	connect(t, r, pad(100, guidXbox, key(0).Serial, ""), key(0), false)
	connect(t, r, pad(101, guidXbox, key(1).Serial, ""), key(1), true)
	connect(t, r, pad(102, guidXbox, key(remembered).Serial, ""), key(remembered), true)
}

func TestRegistryIgnores(t *testing.T) {
	r := input.NewRegistry()
	joystick := &input.DeviceInfo{ID: 1, Name: "Wheel", GUID: guidXbox, Serial: "W"}
	for _, info := range []*input.DeviceInfo{nil, joystick} {
		if k, back := r.Connect(info); k != (input.DeviceKey{}) || back {
			t.Errorf("Connect(%+v) = %v, %t; want the zero key", info, k, back)
		}
	}
	if k, ok := r.Key(1); ok {
		t.Errorf("Key of the joystick = %v", k)
	}
	for _, id := range []input.DeviceID{0, 1, 2} {
		if k, ok := r.Disconnect(id); ok || k != (input.DeviceKey{}) {
			t.Errorf("Disconnect(%d) of an unknown id = %v, %t", id, k, ok)
		}
	}
	checkConnected(t, r, nil, nil)

	// A second Connect of an id keeps its key; a second Disconnect finds
	// nothing.
	a := input.DeviceKey{GUID: guidXbox, Serial: "A"}
	connect(t, r, pad(2, guidXbox, "A", ""), a, false)
	connect(t, r, pad(2, guidXbox, "B", ""), a, false)
	checkConnected(t, r, []input.DeviceID{2}, []input.DeviceKey{a})
	disconnect(t, r, 2, a)
	if k, ok := r.Disconnect(2); ok {
		t.Errorf("a second Disconnect(2) = %v", k)
	}
	checkConnected(t, r, nil, nil)
}

// TestRegistryOrder checks that Connected lists the gamepads in the order
// they connected, a reconnected one last.
func TestRegistryOrder(t *testing.T) {
	r := input.NewRegistry()
	keys := []input.DeviceKey{{GUID: guidXbox, Serial: "A"}, {GUID: guidXbox, Path: "p"}, {GUID: guidVirtual}}
	for i, k := range keys {
		connect(t, r, pad(input.DeviceID(i+1), k.GUID, k.Serial, k.Path), k, false)
	}
	checkConnected(t, r, []input.DeviceID{1, 2, 3}, keys)
	disconnect(t, r, 2, keys[1])
	checkConnected(t, r, []input.DeviceID{1, 3}, []input.DeviceKey{keys[0], keys[2]})
	connect(t, r, pad(4, guidXbox, "", "p"), keys[1], true)
	checkConnected(t, r, []input.DeviceID{1, 3, 4}, []input.DeviceKey{keys[0], keys[2], keys[1]})
}

// base returns k with N = 0.
func base(k input.DeviceKey) input.DeviceKey {
	k.N = 0
	return k
}

// TestRegistryRandomWalk connects and disconnects gamepads of five base
// keys at random and checks the Registry after each step against the
// rules of Connect and Disconnect.
func TestRegistryRandomWalk(t *testing.T) {
	const remembered = 32
	infos := []*input.DeviceInfo{
		pad(0, guidXbox, "A", "p1"),
		pad(0, guidXbox, "B", ""),
		pad(0, guidXbox, "", "p1"),
		pad(0, guidVirtual, "", ""),
		pad(0, "", "", ""),
	}
	rng := rand.New(rand.NewPCG(1, 2))
	r := input.NewRegistry()
	var (
		order  []input.DeviceID // the connected ids, in connection order
		keys   = make(map[input.DeviceID]input.DeviceKey)
		nextID input.DeviceID
		// What the walk did, to check below that it tried each rule.
		reconnects, forgotten, maxN int
	)
	for step := range 10_000 {
		disc := r.DisconnectedKeys()
		// Phases of 500 steps connect or disconnect more, so that many
		// identical gamepads are connected and then disconnected.
		pConnect := 0.3
		if step/500%2 == 0 {
			pConnect = 0.7
		}
		switch x := rng.Float64(); {
		case x < 0.02: // an id that is not connected
			if k, ok := r.Disconnect(nextID + 1); ok {
				t.Fatalf("step %d: Disconnect of an unknown id = %v", step, k)
			}
		case x < 0.04 && len(order) > 0: // an id that is connected
			id := order[rng.IntN(len(order))]
			info := *infos[rng.IntN(len(infos))]
			info.ID = id
			if k, back := r.Connect(&info); k != keys[id] || back {
				t.Fatalf("step %d: a second Connect(%d) = %v, %t; want %v", step, id, k, back, keys[id])
			}
		case x < 0.06: // not a gamepad
			nextID++
			info := *infos[rng.IntN(len(infos))]
			info.ID, info.IsGamepad = nextID, false
			if k, back := r.Connect(&info); k != (input.DeviceKey{}) || back {
				t.Fatalf("step %d: Connect of a joystick = %v, %t", step, k, back)
			}
		case x < pConnect || len(order) == 0:
			nextID++
			info := *infos[rng.IntN(len(infos))]
			info.ID = nextID
			b := input.KeyOf(&info)
			k, back := r.Connect(&info)
			if base(k) != b {
				t.Fatalf("step %d: Connect = %v, want a key of %v", step, k, b)
			}
			// The disconnected gamepad of this base with the lowest N
			// comes back, else the lowest N that is free.
			want, i := input.DeviceKey{}, -1
			for j, d := range disc {
				if base(d) == b && (i < 0 || d.N < want.N) {
					want, i = d, j
				}
			}
			if i >= 0 {
				disc = slices.Delete(disc, i, i+1)
			} else {
				want = b
				for slices.ContainsFunc(order, func(id input.DeviceID) bool { return keys[id] == want }) {
					want.N++
				}
			}
			if k != want || back != (i >= 0) {
				t.Fatalf("step %d: Connect = %v, %t; want %v, %t", step, k, back, want, i >= 0)
			}
			if back {
				reconnects++
			}
			maxN = max(maxN, k.N)
			order = append(order, nextID)
			keys[nextID] = k
		default:
			i := rng.IntN(len(order))
			id := order[i]
			if k, ok := r.Disconnect(id); !ok || k != keys[id] {
				t.Fatalf("step %d: Disconnect(%d) = %v, %t; want %v", step, id, k, ok, keys[id])
			}
			if len(disc) == remembered {
				disc = disc[1:]
				forgotten++
			}
			disc = append(disc, keys[id])
			order = slices.Delete(order, i, i+1)
			delete(keys, id)
		}

		if got := r.DisconnectedKeys(); !slices.Equal(got, disc) || len(got) > remembered {
			t.Fatalf("step %d: disconnected %v, want %v", step, got, disc)
		}
		want := make([]input.DeviceKey, len(order))
		for i, id := range order {
			want[i] = keys[id]
			if k, ok := r.Key(id); !ok || k != keys[id] {
				t.Fatalf("step %d: Key(%d) = %v, %t; want %v", step, id, k, ok, keys[id])
			}
		}
		if got := r.Connected(); !slices.Equal(got, want) {
			t.Fatalf("step %d: Connected() = %v, want %v", step, got, want)
		}
		// Every key, connected or remembered, is unique, and so is its
		// string.
		seen := make(map[string]bool)
		for _, k := range slices.Concat(want, disc) {
			if seen[k.String()] {
				t.Fatalf("step %d: %v twice in %v and %v", step, k, want, disc)
			}
			seen[k.String()] = true
		}
	}
	t.Logf("%d reconnects, %d forgotten, N up to %d", reconnects, forgotten, maxN)
	if reconnects < 100 || forgotten < 100 || maxN < 5 {
		t.Errorf("the walk tried too little: %d reconnects, %d forgotten, N up to %d", reconnects, forgotten, maxN)
	}
}
