package input

import (
	"encoding/hex"
	"math"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// These tests need no SDL and no clock: the hub is driven with a fake
// transport and a fake clock (jc2hub_rig_test.go).

// knownAuto makes the hub remember the four test controllers, with
// auto-connect on.
func knownAuto(c *joyCon2Config) {
	c.Known = []KnownJoyCon2{
		{addrL1, serL1, jc2.SideL}, {addrR1, serR1, jc2.SideR},
		{addrL2, serL2, jc2.SideL}, {addrR2, serR2, jc2.SideR},
	}
	c.AutoConnect = true
}

func equalStrings(t *testing.T, what string, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Errorf("%s:\n got %q\nwant %q", what, got, want)
	}
}

func TestHubScanPolicy(t *testing.T) {
	known := []KnownJoyCon2{{addrL1, serL1, jc2.SideL}}
	for _, tc := range []struct {
		name  string
		mod   func(*joyCon2Config)
		after func(*hubRig)
		want  bool
	}{
		{"nothing remembered", func(c *joyCon2Config) { c.AutoConnect = true }, nil, false},
		{"remembered, auto-connect on", func(c *joyCon2Config) { c.Known, c.AutoConnect = known, true }, nil, true},
		{"remembered, auto-connect off", func(c *joyCon2Config) { c.Known = known }, nil, false},
		{"window", nil, func(r *hubRig) { r.h.OpenWindow(r.now, 0) }, true},
		{"window canceled", nil, func(r *hubRig) { r.h.OpenWindow(r.now, 0); r.h.CancelWindow(r.now) }, false},
		{"window expired", nil, func(r *hubRig) { r.h.OpenWindow(r.now, 0); r.step(DefaultJoyCon2Window) }, false},
		{"window within a remembered scan", func(c *joyCon2Config) { c.Known, c.AutoConnect = known, true },
			func(r *hubRig) { r.h.OpenWindow(r.now, 0); r.h.CancelWindow(r.now) }, true},
		{"auto-connect turned off", func(c *joyCon2Config) { c.Known, c.AutoConnect = known, true },
			func(r *hubRig) { r.h.SetAutoConnect(false) }, false},
		{"auto-connect turned on", func(c *joyCon2Config) { c.Known = known },
			func(r *hubRig) { r.h.SetAutoConnect(true) }, true},
		{"known list emptied", func(c *joyCon2Config) { c.Known, c.AutoConnect = known, true },
			func(r *hubRig) { r.h.SetKnown(nil, r.now) }, false},
		{"known list filled", func(c *joyCon2Config) { c.AutoConnect = true },
			func(r *hubRig) { r.h.SetKnown(known, r.now) }, true},
		{"forgotten", func(c *joyCon2Config) { c.Known, c.AutoConnect = known, true },
			func(r *hubRig) { r.h.ForgetSerial(serL1, r.now) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.mod)
			if tc.after != nil {
				tc.after(r)
			}
			if got := r.tr.scanning(); got != tc.want {
				t.Errorf("scanning = %v, want %v (calls %v)", got, tc.want, r.tr.scanList())
			}
			// The transport is told only about changes.
			s := r.tr.scanList()
			for i := 1; i < len(s); i++ {
				if s[i] == s[i-1] {
					t.Errorf("Scan(%v) twice in a row: %v", s[i], s)
				}
			}
		})
	}
}

func TestHubIgnoresUnknownOutsideWindow(t *testing.T) {
	r := newRig(t, func(c *joyCon2Config) {
		c.Known, c.AutoConnect = []KnownJoyCon2{{addrL1, serL1, jc2.SideL}}, true
	})
	r.advertise(addrR2, jc2.SideR, true) // not remembered
	if n := len(r.tr.connectList()); n != 0 {
		t.Fatalf("connected to an unknown controller outside the window: %v", r.tr.connectList())
	}
	// Auto-connect off: not even the remembered one.
	r.h.SetAutoConnect(false)
	r.advertise(addrL1, jc2.SideL, true)
	if n := len(r.tr.connectList()); n != 0 {
		t.Fatalf("connected with auto-connect off: %v", r.tr.connectList())
	}
}

func TestHubNeverConnectsNonStandard(t *testing.T) {
	r := newRig(t, knownAuto)
	r.h.OpenWindow(r.now, 0)
	r.advertise(addrL1, jc2.SideL, false) // reconnecting to its console
	r.advertise(addrR2, jc2.SideR, false)
	r.feed(jc2win.Event{Kind: jc2win.Advert, Addr: addrL2, Company: 0x1234, Data: advertEvent(addrL2, jc2.SideL, true).Data})
	r.feed(jc2win.Event{Kind: jc2win.Advert, Addr: addrL2, Company: jc2.CompanyID, Data: []byte{1, 2, 3}})
	if n := len(r.tr.connectList()); n != 0 {
		t.Fatalf("connected to a non-standard advertisement: %v", r.tr.connectList())
	}
}

func TestHubPairingWindow(t *testing.T) {
	r := newRig(t, nil)
	r.h.OpenWindow(r.now, 0)
	if !r.tr.scanning() {
		t.Fatal("not scanning with the window open")
	}
	r.advertise(addrL1, jc2.SideL, true)
	cs := r.tr.connectList()
	if len(cs) != 1 || cs[0].addr != addrL1 || cs[0].side != jc2.SideL || cs[0].player != 0 {
		t.Fatalf("connects %+v, want one to L1 as player 0", cs)
	}
	// The first controller holds the window: another unknown one waits.
	r.advertise(addrR1, jc2.SideR, true)
	r.advertise(addrL1, jc2.SideL, true) // and one connect per address
	if n := len(r.tr.connectList()); n != 1 {
		t.Fatalf("%d connects while the first is under way", n)
	}
	if len(r.remembered) != 0 {
		t.Fatalf("remembered %v before the controller is up", r.remembered)
	}
	r.feed(connectedEvent(cs[0].id, jc2.SideL, serL1, nil))
	want := []KnownJoyCon2{{addrL1, serL1, jc2.SideL}}
	if !slices.Equal(r.remembered, want) {
		t.Fatalf("remembered %v, want %v", r.remembered, want)
	}
	st := r.h.Status(r.now)
	if st.WindowOpen || r.tr.scanning() {
		t.Errorf("window open %v, scanning %v after the controller connected (auto-connect is off)", st.WindowOpen, r.tr.scanning())
	}
	// The window is closed: the other controller is unknown again.
	r.advertise(addrR1, jc2.SideR, true)
	if n := len(r.tr.connectList()); n != 1 {
		t.Errorf("connected to an unknown controller after the window closed")
	}
	// Connecting the same controller again does not remember it again.
	r.h.OpenWindow(r.now, 0)
	r.lost(cs[0].id)
	r.step(joyCon2Backoff)
	r.advertise(addrL1, jc2.SideL, true)
	cs = r.tr.connectList()
	r.feed(connectedEvent(cs[len(cs)-1].id, jc2.SideL, serL1, nil))
	if len(r.remembered) != 1 {
		t.Errorf("remembered %v, want the one entry only once", r.remembered)
	}
}

func TestHubWindowExpiresAndCancels(t *testing.T) {
	r := newRig(t, nil)
	r.h.OpenWindow(r.now, 5*time.Second)
	r.step(4900 * time.Millisecond)
	if !r.h.Status(r.now).WindowOpen {
		t.Fatal("window closed early")
	}
	r.step(100 * time.Millisecond)
	if r.h.Status(r.now).WindowOpen || r.tr.scanning() {
		t.Fatal("window still open at its end")
	}
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 0 {
		t.Fatal("connected after the window expired")
	}

	// A connection that the window started is finished and remembered even
	// when the window ends meanwhile.
	r.h.OpenWindow(r.now, 5*time.Second)
	r.advertise(addrL1, jc2.SideL, true)
	r.step(6 * time.Second)
	cs := r.tr.connectList()
	r.feed(connectedEvent(cs[0].id, jc2.SideL, serL1, nil))
	if len(r.remembered) != 1 {
		t.Fatalf("remembered %v, want the controller whose connection began in the window", r.remembered)
	}

	// Canceling the window ends the connection that is under way.
	r2 := newRig(t, nil)
	r2.h.OpenWindow(r2.now, 0)
	r2.advertise(addrR1, jc2.SideR, true)
	cs = r2.tr.connectList()
	r2.h.CancelWindow(r2.now)
	if d := r2.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{cs[0].id}) {
		t.Fatalf("disconnects %v after CancelWindow", d)
	}
	r2.feed(connectedEvent(cs[0].id, jc2.SideR, serR1, nil)) // a race: ignored
	if len(r2.remembered) != 0 || len(r2.take()) != 0 {
		t.Fatalf("a canceled connection was used: %v", r2.remembered)
	}
}

func TestHubWindowWithoutSerialKeepsWaiting(t *testing.T) {
	r := newRig(t, nil)
	r.h.OpenWindow(r.now, 0)
	id := r.mustConnect(addrL1, jc2.SideL, "") // the info block holds no serial
	if d := r.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{id}) {
		t.Fatalf("disconnects %v, want the link without a serial", d)
	}
	if len(r.remembered) != 0 || len(r.take()) != 0 {
		t.Fatalf("a controller without a serial was used")
	}
	if !r.h.Status(r.now).WindowOpen {
		t.Error("the window closed without a controller")
	}
	// The same controller waits out its backoff; another one is welcome.
	r.advertise(addrR1, jc2.SideR, true)
	if n := len(r.tr.connectList()); n != 2 {
		t.Errorf("%d connects, want the window to take the next controller", n)
	}
}

func TestHubKnownSerialFallback(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, "") // no readable info block
	_ = id
	evs := r.take()
	if len(evs) != 1 || evs[0].Info.Serial != serL1 {
		t.Fatalf("events %v, want one gamepad with the remembered serial", descs(evs))
	}
}

func TestHubAutoConnectAndBackoff(t *testing.T) {
	r := newRig(t, knownAuto)
	r.advertise(addrL1, jc2.SideL, true)
	cs := r.tr.connectList()
	if len(cs) != 1 {
		t.Fatalf("connects %v, want one to the remembered controller", cs)
	}
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 1 {
		t.Fatal("a second connect to the same address")
	}
	r.feed(jc2win.Event{Kind: jc2win.Failed, Link: cs[0].id, Addr: addrL1, Err: jc2win.ErrLinkDropped})
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 1 {
		t.Fatal("connected again at once after a failure")
	}
	r.step(joyCon2Backoff - time.Millisecond)
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 1 {
		t.Fatal("connected again before the backoff ended")
	}
	r.step(time.Millisecond)
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 2 {
		t.Fatal("not connecting after the backoff")
	}
	// A refusing transport (closed) leaves no link behind.
	r.tr.refuse = true
	r.advertise(addrR1, jc2.SideR, true)
	if st := r.h.Status(r.now); len(st.Links) != 1 {
		t.Errorf("links %+v, want only the first", st.Links)
	}
}

func TestHubPlayerNumbers(t *testing.T) {
	r := newRig(t, knownAuto)
	a := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.mustConnect(addrR1, jc2.SideR, serR1)
	r.mustConnect(addrL2, jc2.SideL, serL2)
	players := func() []int {
		var p []int
		for _, c := range r.tr.connectList() {
			p = append(p, c.player)
		}
		return p
	}
	if got := players(); !slices.Equal(got, []int{0, 1, 2}) {
		t.Fatalf("players %v, want 0 1 2", got)
	}
	r.lost(a)
	r.mustConnect(addrL1, jc2.SideL, serL1) // L1 is back: the lowest free number
	if got := players(); !slices.Equal(got, []int{0, 1, 2, 0}) {
		t.Fatalf("players %v, want the freed 0 again", got)
	}
	// A link that is still connecting holds its number too.
	r.advertise(addrR2, jc2.SideR, true)
	if got := players(); got[len(got)-1] != 3 {
		t.Fatalf("players %v, want 3 for the fifth", got)
	}
}

func TestHubSingleDeviceInfo(t *testing.T) {
	for _, tc := range []struct {
		name     string
		vertical bool
		addr     uint64
		side     jc2.Side
		serial   string
		devName  string
		typ      string
		product  uint16
		guid     string
		buttons  uint32
		axes     uint8
	}{
		// The masks are those of SDL's Switch 1 Joy-Con (family_sdl_test.go),
		// with the C button of the right one (misc2, bit 21) added.
		{"L sideways", false, addrL1, jc2.SideL, serL1, "Joy-Con 2 (L)", "joyconleft", 0x2067, "050000007e0500006720000000006200", 0xa06ef, 0x3},
		{"R sideways", false, addrR1, jc2.SideR, serR1, "Joy-Con 2 (R)", "joyconright", 0x2066, "050000007e0500006620000000006200", 0x2506ef, 0x3},
		{"L upright", true, addrL1, jc2.SideL, serL1, "Joy-Con 2 (L)", "joyconleft", 0x2067, "050000007e0500006720000000006200", 0xafa90, 0x13},
		{"R upright", true, addrR1, jc2.SideR, serR1, "Joy-Con 2 (R)", "joyconright", 0x2066, "050000007e0500006620000000006200", 0x25056f, 0x2c},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, func(c *joyCon2Config) { knownAuto(c); c.Vertical = tc.vertical })
			r.now = 123456789 * time.Nanosecond
			r.mustConnect(tc.addr, tc.side, tc.serial)
			evs := r.take()
			if len(evs) != 1 || evs[0].Kind != DeviceAdded || evs[0].Device != joyCon2IDBase {
				t.Fatalf("events %v, want one DeviceAdded of the first id", descs(evs))
			}
			e, info := evs[0], evs[0].Info
			if e.Time != uint64(r.now) {
				t.Errorf("Time = %d, want the time the hub was given, %d", e.Time, r.now)
			}
			want := DeviceInfo{
				ID: joyCon2IDBase, Name: tc.devName, Type: tc.typ,
				ButtonLabels: FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"},
				Vendor:       0x057e, Product: tc.product, GUID: tc.guid, Driver: "ble",
				Path:   "ble:" + jc2win.FormatAddr(tc.addr),
				Serial: tc.serial, IsGamepad: true,
				ButtonMask: tc.buttons, AxisMask: tc.axes,
				Capabilities: Capabilities{Known: true},
			}
			if *info != want {
				t.Errorf("info\n got %+v\nwant %+v", *info, want)
			}
			if !info.ControlsKnown() {
				t.Error("ControlsKnown is false")
			}
			g := decodeGUID(t, info.GUID)
			if got := DriverName(g, runtime.GOOS); got != info.Driver {
				t.Errorf("DriverName(GUID) = %q, Driver = %q", got, info.Driver)
			}
			// The device key is the GUID and the serial; it round-trips.
			k := KeyOf(info)
			if k != (DeviceKey{GUID: tc.guid, Serial: tc.serial}) {
				t.Errorf("key %+v", k)
			}
			if back, err := ParseDeviceKey(k.String()); err != nil || back != k {
				t.Errorf("ParseDeviceKey(%q) = %+v, %v", k.String(), back, err)
			}
		})
	}
}

func decodeGUID(t *testing.T, s string) GUID {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 || !isGUIDString(s) {
		t.Fatalf("GUID %q is not 32 lowercase hex digits", s)
	}
	return GUID(b)
}

func TestHubIDsAvoidTheOthers(t *testing.T) {
	// SDL counts up from 1, receivers' aliases down from 2³²−1, the tests'
	// fake ids are 0x7ff00001 and near.
	if joyCon2IDBase < 1<<20 || joyCon2IDBase >= 0x7ff00001-1<<20 || joyCon2IDBase >= math.MaxUint32-1<<20 {
		t.Errorf("first id %#x is not clear of the other ranges", uint32(joyCon2IDBase))
	}
}

func TestHubReportsMakeEvents(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.take()
	r.now += 15 * time.Millisecond
	r.report(id, bLLeft|bSL)
	evs := r.take()
	equalStrings(t, "press", descs(evs), []string{"down 1 south", "down 1 leftshoulder"})
	for _, e := range evs {
		if e.Stamp != 1234 || e.Time != uint64(r.now) {
			t.Errorf("%s: Stamp %d, Time %d; want the transport's stamp and the hub's time", desc(e), e.Stamp, e.Time)
		}
	}
	r.report(id, bLLeft|bSL) // nothing changed
	r.report(id, bSL)
	r.report(id, 0, 2048, 3248) // the stick up by its whole travel, held sideways
	r.report(id, 0, 2048, 3248)
	r.report(id, 0)
	equalStrings(t, "release and stick", r.takeDesc(), []string{"up 1 south", "up 1 leftshoulder", "axis 1 leftx=-32767", "axis 1 leftx=0"})
	if h := r.trk.heldNames(joyCon2IDBase); len(h) != 0 {
		t.Errorf("held %v at rest", h)
	}
}

func TestHubUsesStickCalibration(t *testing.T) {
	cal := jc2.StickCal{NeutralX: 2053, NeutralY: 1999, PosX: 1225, PosY: 1205, NegX: 1242, NegY: 1209}
	r := newRig(t, knownAuto)
	n := len(r.tr.connectList())
	r.advertise(addrR1, jc2.SideR, true)
	id := r.tr.connectList()[n].id
	r.feed(connectedEvent(id, jc2.SideR, serR1, factoryBlock(cal)))
	r.take()
	r.report(id, 0, cal.NeutralX, cal.NeutralY) // exactly at the calibrated center
	if evs := r.take(); len(evs) != 0 {
		t.Fatalf("events %v at the calibrated center", descs(evs))
	}
	r.report(id, 0, cal.NeutralX, cal.NeutralY+cal.PosY) // up by its travel: right-hand horizontal, up is +leftx
	equalStrings(t, "up", r.takeDesc(), []string{"axis 1 leftx=32767"})
}

func TestHubPairFormsAndBreaks(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	equalStrings(t, "L", r.takeDesc(), []string{"added 1 Joy-Con 2 (L)"})
	r.report(l, bLL)
	equalStrings(t, "L pressed", r.takeDesc(), []string{"down 1 paddle2"})
	infoL := r.trk.live[joyCon2IDBase]
	keyL := KeyOf(infoL)

	// The right Joy-Con arrives: the single is removed (its consumer
	// releases the L button), then the pair is added and shows what is held.
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	equalStrings(t, "R arrives", r.takeDesc(), []string{"removed 1", "added 2 Joy-Con 2 pair", "down 2 leftshoulder"})
	if h := r.trk.heldNames(joyCon2IDBase); h != nil {
		t.Errorf("the removed single still holds %v", h)
	}
	pair := r.trk.live[joyCon2IDBase+1]
	if pair.Type != "joyconpair" || pair.Product != 0x2068 || pair.Serial != serL1+"+"+serR1 || pair.Name != "Joy-Con 2 pair" ||
		pair.Vendor != 0x057e || pair.Driver != "ble" || !pair.IsGamepad {
		t.Errorf("pair info %+v", pair)
	}
	if pair.ButtonMask != 0x2fffff || pair.AxisMask != 0x3f {
		t.Errorf("pair masks %#x %#x", pair.ButtonMask, pair.AxisMask)
	}
	keyP := KeyOf(pair)
	if back, err := ParseDeviceKey(keyP.String()); err != nil || back != keyP {
		t.Errorf("pair key %q does not round-trip: %+v, %v", keyP.String(), back, err)
	}
	r.report(rr, bRPlus|bRA)
	r.report(l, bLL|bLMinus|bLUp)
	equalStrings(t, "pair buttons", r.takeDesc(), []string{"down 2 east", "down 2 start", "down 2 back", "down 2 dpad.up"})

	// The right Joy-Con goes: the pair is removed, the left one comes back as
	// a single with a new id and its own, original key.
	r.lost(rr)
	equalStrings(t, "R lost", r.takeDesc(), []string{"removed 2", "added 3 Joy-Con 2 (L)", "down 3 west", "down 3 start", "down 3 paddle2"})
	again := r.trk.live[joyCon2IDBase+2]
	if k := KeyOf(again); k != keyL {
		t.Errorf("key of the single after the pair = %+v, before = %+v", k, keyL)
	}
	if keyP == keyL || keyP == (DeviceKey{GUID: keyL.GUID, Serial: serR1}) || keyP.GUID == keyL.GUID {
		t.Errorf("the pair's key %+v is not its own", keyP)
	}
	// And the other way round: R first, then L.
	r.lost(l)
	r.take()
	rr = r.mustConnect(addrR1, jc2.SideR, serR1)
	keyR := KeyOf(r.trk.live[joyCon2IDBase+3])
	if keyR.Serial != serR1 || keyR.GUID == keyL.GUID || keyR.GUID == keyP.GUID {
		t.Errorf("right key %+v", keyR)
	}
	l = r.mustConnect(addrL1, jc2.SideL, serL1)
	r.take()
	r.lost(l)
	evs := r.take()
	var single *DeviceInfo
	for _, e := range evs {
		if e.Kind == DeviceAdded {
			single = e.Info
		}
	}
	if single == nil || KeyOf(single) != keyR {
		t.Errorf("the right single came back with key %+v, want %+v", single, keyR)
	}
	_ = rr
}

func TestHubKeyRoundTripSinglePairSingle(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	single := KeyOf(r.trk.live[joyCon2IDBase])
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	pair := KeyOf(r.trk.live[joyCon2IDBase+1])
	r.lost(rr)
	back := KeyOf(r.trk.live[joyCon2IDBase+2])
	if back != single {
		t.Errorf("single → pair → single: key %+v, then %+v", single, back)
	}
	if pair == single || pair == back {
		t.Errorf("the pair's key %+v equals a single's", pair)
	}
	// The same holds for the registry that numbers identical gamepads.
	reg := NewRegistry()
	ks, _ := reg.Connect(&DeviceInfo{ID: 1, IsGamepad: true, GUID: single.GUID, Serial: single.Serial})
	kp, _ := reg.Connect(&DeviceInfo{ID: 2, IsGamepad: true, GUID: pair.GUID, Serial: pair.Serial})
	if ks.N != 0 || kp.N != 0 || ks == kp {
		t.Errorf("registry keys %+v, %+v", ks, kp)
	}
	_ = l
}

// TestHubPairsOnlyItsOwnLinks documents the rule that a Switch 1 Joy-Con from
// SDL is never combined with a Joy-Con 2: the hub knows only its own links,
// and pairs the first left with the first right of those. Two of one side
// never pair.
func TestHubPairsOnlyItsOwnLinks(t *testing.T) {
	r := newRig(t, knownAuto)
	r.mustConnect(addrL1, jc2.SideL, serL1)
	r.mustConnect(addrL2, jc2.SideL, serL2)
	equalStrings(t, "two lefts", r.takeDesc(), []string{"added 1 Joy-Con 2 (L)", "added 2 Joy-Con 2 (L)"})
	r.mustConnect(addrR1, jc2.SideR, serR1)
	for _, e := range r.evs {
		if e.Kind == DeviceAdded && e.Info.Type == "joyconpair" {
			if !strings.Contains(e.Info.Path, jc2win.FormatAddr(addrL1)) || !strings.Contains(e.Info.Path, jc2win.FormatAddr(addrR1)) {
				t.Errorf("the pair %q is not of the first left and the right Joy-Con 2", e.Info.Path)
			}
		}
	}
}

func TestHubTwoOfTheSameSide(t *testing.T) {
	r := newRig(t, knownAuto)
	l1 := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.mustConnect(addrL2, jc2.SideL, serL2)
	r.take()
	// R pairs with the first left; the second left stays a single.
	r.mustConnect(addrR1, jc2.SideR, serR1)
	equalStrings(t, "R arrives", r.takeDesc(), []string{"removed 1", "added 3 Joy-Con 2 pair"})
	if got := len(r.trk.live); got != 2 {
		t.Fatalf("%d live gamepads, want the pair and the second left", got)
	}
	// The first left goes: the pair is rebuilt with the second left.
	r.lost(l1)
	equalStrings(t, "L1 lost", r.takeDesc(), []string{"removed 2", "removed 3", "added 4 Joy-Con 2 pair"})
	pair := r.trk.live[joyCon2IDBase+3]
	if pair.Serial != serL2+"+"+serR1 {
		t.Errorf("pair serial %q, want the second left with the right one", pair.Serial)
	}
}

func TestHubWatchdogDormantAndRevival(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.report(id, bLLeft)
	r.take()
	key := KeyOf(r.trk.live[joyCon2IDBase])

	// Reports every 15 ms keep it alive for as long as they come.
	for range 100 {
		r.step(15 * time.Millisecond)
		r.report(id, bLLeft)
	}
	if evs := r.take(); len(evs) != 0 {
		t.Fatalf("events %v while reports came", descs(evs))
	}

	// Silent for 300 ms: removed, with a key held; the link stays.
	r.step(jc2.DefaultSilence - time.Millisecond)
	if evs := r.take(); len(evs) != 0 {
		t.Fatalf("events %v before the silence timeout", descs(evs))
	}
	r.step(time.Millisecond)
	equalStrings(t, "silent", r.takeDesc(), []string{"removed 1"})
	if h := r.trk.heldNames(joyCon2IDBase); h != nil {
		t.Errorf("a removed gamepad holds %v", h)
	}
	if d := r.tr.disconnectList(); len(d) != 0 {
		t.Fatalf("link disconnected after 300 ms: %v", d)
	}
	if st := r.h.Status(r.now); st.Links[0].State != "dormant" || st.Links[0].Device != 0 {
		t.Errorf("link status %+v, want dormant without a gamepad", st.Links[0])
	}

	// Its reports come again: a new gamepad, the same key, what is held now is
	// a fresh press.
	r.report(id, bLLeft)
	evs := r.take()
	equalStrings(t, "revival", descs(evs), []string{"added 2 Joy-Con 2 (L)", "down 2 south"})
	if k := KeyOf(evs[0].Info); k != key || evs[0].Device == joyCon2IDBase {
		t.Errorf("revived with key %+v id %d; want key %+v and a new id", k, evs[0].Device, key)
	}
	for _, e := range evs {
		if e.Kind == ButtonDown && e.Stamp != 1234 {
			t.Errorf("fresh press without the report's stamp: %d", e.Stamp)
		}
	}
}

func TestHubWatchdogGivesUp(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.report(id, 0)
	r.take()
	r.step(jc2.DefaultSilence)
	equalStrings(t, "dormant", r.takeDesc(), []string{"removed 1"})
	r.step(jc2.DefaultLinkGiveUp - jc2.DefaultSilence - time.Millisecond)
	if d := r.tr.disconnectList(); len(d) != 0 {
		t.Fatalf("disconnected early: %v", d)
	}
	r.step(time.Millisecond)
	if d := r.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{id}) {
		t.Fatalf("disconnects %v, want the silent link", d)
	}
	if evs := r.take(); len(evs) != 0 {
		t.Fatalf("events %v at give-up: the gamepad was removed long ago", descs(evs))
	}
	// A report that is still on its way and the Lost that follows are
	// ignored, and the address is left alone for the backoff.
	r.report(id, bLLeft)
	r.lost(id)
	r.advertise(addrL1, jc2.SideL, true)
	if evs := r.take(); len(evs) != 0 || len(r.tr.connectList()) != 1 {
		t.Fatalf("events %v, connects %v after the give-up", descs(evs), r.tr.connectList())
	}
	r.step(joyCon2Backoff)
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 2 {
		t.Error("the controller cannot come back after the backoff")
	}
}

func TestHubDormantSideOfAPair(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.take() // L single, then the pair (id 2)
	// R keeps reporting; L goes silent. At the silence the pair is replaced at
	// once by R alone, holding what R holds as a fresh press.
	for range 2 {
		r.step(100 * time.Millisecond)
		r.report(rr, bRA)
	}
	equalStrings(t, "before the silence", r.takeDesc(), []string{"down 2 east"})
	r.step(100 * time.Millisecond)
	equalStrings(t, "L silent", r.takeDesc(), []string{"removed 2", "added 3 Joy-Con 2 (R)", "down 3 south"})
	r.report(rr, bRA|bRB)
	equalStrings(t, "single R", r.takeDesc(), []string{"down 3 west"})
	// L comes back: the single is replaced by the pair, which shows both
	// sides' state.
	r.report(l, bLMinus)
	equalStrings(t, "L back", r.takeDesc(), []string{"removed 3", "added 4 Joy-Con 2 pair", "down 4 south", "down 4 east", "down 4 back"})
	// R goes silent for good while L reports: after 300 ms the pair is
	// replaced by L alone, and after 3 s the link is closed, with no event
	// of its own.
	for range 30 {
		r.step(100 * time.Millisecond)
		r.report(l, bLMinus)
	}
	equalStrings(t, "R gives up", r.takeDesc(), []string{"removed 4", "added 5 Joy-Con 2 (L)", "down 5 start"})
	if d := r.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{rr}) {
		t.Errorf("disconnects %v, want the silent right link", d)
	}
}

// TestHubPairBreaksUpOnSilence: when one side of a pair goes silent, the side
// that still reports is a single at once, as for a fresh connection, and the
// silent side's revival makes the pair again. Every step releases first.
func TestHubPairBreaksUpOnSilence(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.take()
	pairID := joyCon2IDBase + 1
	r.report(l, bLMinus)
	r.report(rr, bRA|bRX)
	r.take()
	if h := r.trk.heldNames(pairID); !slices.Equal(h, []string{"back", "east", "north"}) {
		t.Fatalf("the pair holds %v, want A, X and minus (east, north, back)", h)
	}

	// Right keeps reporting for 299 ms: nothing happens. At 300 ms the left
	// side, the silent one, ends the pair.
	r.step(jc2.DefaultSilence - time.Millisecond)
	r.report(rr, bRA|bRX)
	if evs := r.take(); len(evs) != 0 {
		t.Fatalf("events %v before the silence timeout", descs(evs))
	}
	r.step(time.Millisecond)
	evs := r.take()
	equalStrings(t, "the pair breaks up", descs(evs), []string{"removed 2", "added 3 Joy-Con 2 (R)", "down 3 south", "down 3 east"})
	if h := r.trk.heldNames(pairID); h != nil {
		t.Errorf("the removed pair still holds %v", h)
	}
	if h := r.trk.heldNames(joyCon2IDBase + 2); !slices.Equal(h, []string{"east", "south"}) {
		t.Errorf("the single holds %v, want what the right Joy-Con holds", h)
	}
	for _, e := range evs {
		if e.Kind == ButtonDown && e.Stamp != 0 {
			t.Errorf("a press made by the timer has the stamp %d of no report", e.Stamp)
		}
	}
	single := r.trk.live[joyCon2IDBase+2]
	if single.Type != "joyconright" || single.Serial != serR1 {
		t.Errorf("single %+v, want the right Joy-Con alone", single)
	}
	if d := r.tr.disconnectList(); len(d) != 0 {
		t.Errorf("disconnects %v at the silence: the link stays", d)
	}
	st := r.h.Status(r.now)
	if st.Links[0].State != "dormant" || st.Links[0].Device != 0 || st.Links[1].State != "connected" || st.Links[1].Device != joyCon2IDBase+2 {
		t.Errorf("links %+v", st.Links)
	}
	// Another tick changes nothing.
	r.step(10 * time.Millisecond)
	if evs := r.take(); len(evs) != 0 {
		t.Errorf("events %v on a tick with nothing new", descs(evs))
	}

	// The single works on its own.
	r.report(rr, bRA)
	equalStrings(t, "single", r.takeDesc(), []string{"up 3 east"})

	// The silent side reports again while the other is live: the pair is made
	// again, as for a fresh second connection, with what both hold.
	r.report(l, bLMinus|bLUp)
	evs = r.take()
	equalStrings(t, "the pair again", descs(evs), []string{"removed 3", "added 4 Joy-Con 2 pair", "down 4 east", "down 4 back", "down 4 dpad.up"})
	for _, e := range evs {
		if e.Kind == ButtonDown && e.Stamp != 1234 {
			t.Errorf("fresh press without the report's stamp: %d", e.Stamp)
		}
	}
	if h := r.trk.heldNames(joyCon2IDBase + 2); h != nil {
		t.Errorf("the removed single still holds %v", h)
	}
}

// TestHubPairSilenceKeepsKeys: the single that follows a pair has its own
// DeviceKey, and the pair that follows a single has the pair's again.
func TestHubPairSilenceKeepsKeys(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	keyPair := KeyOf(r.trk.live[joyCon2IDBase+1])
	r.take()
	// Right goes silent: the left one is the single.
	for range 4 {
		r.step(100 * time.Millisecond)
		r.report(l, 0)
	}
	if k := KeyOf(r.trk.live[joyCon2IDBase+2]); k.Serial != serL1 || k == keyPair {
		t.Errorf("single key %+v", k)
	}
	r.report(rr, 0)
	if k := KeyOf(r.trk.live[joyCon2IDBase+3]); k != keyPair {
		t.Errorf("pair key %+v after the revival, want %+v", k, keyPair)
	}
}

// TestHubSilenceOfBothSidesOfAPair: both sides going silent leaves no gamepad,
// and each revival brings back what fits.
func TestHubSilenceOfBothSidesOfAPair(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.report(l, bLMinus)
	r.report(rr, bRA)
	r.take()
	r.step(jc2.DefaultSilence)
	equalStrings(t, "both silent", r.takeDesc(), []string{"removed 2"})
	r.report(l, bLMinus)
	equalStrings(t, "L back", r.takeDesc(), []string{"added 3 Joy-Con 2 (L)", "down 3 start"})
	r.report(rr, bRA)
	equalStrings(t, "R back", r.takeDesc(), []string{"removed 3", "added 4 Joy-Con 2 pair", "down 4 east", "down 4 back"})
}

// TestHubSilenceWithASpareController: with a second left Joy-Con connected,
// the right one of a pair whose left goes silent pairs with the spare, and
// the first left Joy-Con takes the pair back when it reports again: what a
// fresh connection of the three would make.
func TestHubSilenceWithASpareController(t *testing.T) {
	r := newRig(t, knownAuto)
	l1 := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	l2 := r.mustConnect(addrL2, jc2.SideL, serL2)
	equalStrings(t, "connect", r.takeDesc(), []string{"added 1 Joy-Con 2 (L)", "removed 1", "added 2 Joy-Con 2 pair", "added 3 Joy-Con 2 (L)"})
	for range 4 {
		r.step(100 * time.Millisecond)
		r.report(rr, 0)
		r.report(l2, 0)
	}
	equalStrings(t, "L1 silent", r.takeDesc(), []string{"removed 2", "removed 3", "added 4 Joy-Con 2 pair"})
	if p := r.trk.live[joyCon2IDBase+3]; p.Serial != serL2+"+"+serR1 {
		t.Errorf("pair %q, want the spare left with the right one", p.Serial)
	}
	r.report(l1, 0)
	want := []string{"removed 4", "added 5 Joy-Con 2 pair", "added 6 Joy-Con 2 (L)"}
	if got := r.takeDesc(); !slices.Equal(got, want) {
		t.Errorf("L1 back: %q, want %q: it connected first, so it is the left of the pair again", got, want)
	}
}

// TestHubConnectionParams: the status carries the request's result and the
// current parameters, which Params events update; an unknown value is 0.
func TestHubConnectionParams(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.take()
	link := func() JoyCon2LinkStatus { return r.h.Status(r.now).Links[0] }
	if got := link(); got.ParamsRequest != "accepted" || got.IntervalMs != 15 || got.Latency != 0 || got.TimeoutMs != 9600 {
		t.Fatalf("at connect %+v", got)
	}
	// Windows settled on another interval, and a latency.
	p := testParams(7.5)
	p.Latency = 4
	r.feed(paramsEvent(l, p))
	if got := link(); got.IntervalMs != 7.5 || got.Latency != 4 || got.TimeoutMs != 9600 || got.ParamsRequest != "accepted" {
		t.Errorf("after Params %+v", got)
	}
	if evs := r.take(); len(evs) != 0 {
		t.Errorf("events %v from a Params event", descs(evs))
	}
	// The same value again, an unknown link and a stray link change nothing.
	r.feed(paramsEvent(l, p))
	r.feed(paramsEvent(l+40, testParams(30)))
	if got := link(); got.IntervalMs != 7.5 {
		t.Errorf("after strays %+v", got)
	}
	// Windows 10: the request is unavailable and the parameters unknown.
	r.advertise(addrR1, jc2.SideR, true)
	id := r.tr.connectList()[1].id
	ev := connectedEvent(id, jc2.SideR, serR1, nil)
	ev.Info.ParamsRequest, ev.Info.Params = jc2win.ParamsUnavailable, jc2win.ConnParams{}
	r.feed(ev)
	if got := r.h.Status(r.now).Links[1]; got.ParamsRequest != "unavailable (Windows 10)" || got.IntervalMs != 0 || got.Latency != 0 || got.TimeoutMs != 0 {
		t.Errorf("Windows 10 link %+v", got)
	}
	// A Params event of a link that is gone is ignored.
	r.lost(l)
	r.feed(paramsEvent(l, testParams(30)))
	if st := r.h.Status(r.now); len(st.Links) != 1 || st.Links[0].IntervalMs != 0 {
		t.Errorf("links %+v", st.Links)
	}
}

func TestHubLostWhileHeld(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.report(id, bLLeft|bSL|bSR, 2048, 3248)
	r.take()
	if h := r.trk.heldNames(joyCon2IDBase); len(h) != 4 {
		t.Fatalf("held %v, want 3 buttons and an axis", h)
	}
	r.lost(id)
	equalStrings(t, "lost", r.takeDesc(), []string{"removed 1"})
	if h := r.trk.heldNames(joyCon2IDBase); h != nil {
		t.Errorf("held %v after removal", h)
	}
	r.report(id, bLLeft) // a late report of the dead link
	r.step(10 * time.Second)
	if evs := r.take(); len(evs) != 0 {
		t.Errorf("events %v after the link was lost", descs(evs))
	}
	if st := r.h.Status(r.now); len(st.Links) != 0 {
		t.Errorf("links %+v after Lost", st.Links)
	}
}

func TestHubForget(t *testing.T) {
	r := newRig(t, knownAuto)
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.report(id, bLLeft)
	r.take()
	if r.h.ForgetSerial("nothing", r.now) {
		t.Fatal("forgot a serial that is not remembered")
	}
	if !r.h.ForgetSerial(serL1, r.now) {
		t.Fatal("did not find the remembered controller")
	}
	equalStrings(t, "forget", r.takeDesc(), []string{"removed 1"})
	if d := r.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{id}) {
		t.Errorf("disconnects %v", d)
	}
	r.lost(id)
	if st := r.h.Status(r.now); len(st.Known) != 3 {
		t.Errorf("known %+v, want three left", st.Known)
	}
	// It is not connected to any more, even though it advertises.
	r.step(10 * time.Second)
	r.advertise(addrL1, jc2.SideL, true)
	if len(r.tr.connectList()) != 1 {
		t.Error("connected to a forgotten controller")
	}
	// Forgotten while it connects: it is not used when it comes up.
	r.advertise(addrR1, jc2.SideR, true)
	cs := r.tr.connectList()
	if !r.h.ForgetAddr(addrR1, r.now) {
		t.Fatal("ForgetAddr found nothing")
	}
	r.feed(connectedEvent(cs[1].id, jc2.SideR, serR1, nil))
	if evs := r.take(); len(evs) != 0 {
		t.Errorf("events %v from a link that was forgotten while it connected", descs(evs))
	}
	if d := r.tr.disconnectList(); len(d) != 2 || d[1] != cs[1].id {
		t.Errorf("disconnects %v", d)
	}
	// SetKnown drops like Forget.
	id = r.mustConnect(addrL2, jc2.SideL, serL2)
	r.take()
	r.h.SetKnown([]KnownJoyCon2{{addrR2, serR2, jc2.SideR}}, r.now)
	equalStrings(t, "SetKnown", r.takeDesc(), []string{"removed 2"})
	_ = id
}

func TestHubDisconnectPairBothSides(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.take()
	if r.h.Disconnect(joyCon2IDBase, r.now) { // the single of the first life
		t.Fatal("disconnected a gamepad that is gone")
	}
	if !r.h.Disconnect(joyCon2IDBase+1, r.now) {
		t.Fatal("pair not found")
	}
	equalStrings(t, "disconnect", r.takeDesc(), []string{"removed 2"})
	d := r.tr.disconnectList()
	slices.Sort(d)
	if !slices.Equal(d, []jc2win.LinkID{l, rr}) {
		t.Errorf("disconnects %v, want both links", d)
	}
	r.advertise(addrL1, jc2.SideL, true) // left alone for the backoff
	if len(r.tr.connectList()) != 2 {
		t.Error("reconnected at once")
	}
}

func TestHubIgnoresStrayEvents(t *testing.T) {
	r := newRig(t, knownAuto)
	r.report(99, bLLeft)
	r.feed(connectedEvent(99, jc2.SideL, serL1, nil))
	r.lost(99)
	r.feed(jc2win.Event{Kind: jc2win.Failed, Link: 99})
	// A Connected for a link in a state it cannot be in: twice.
	id := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.feed(connectedEvent(id, jc2.SideL, serL1, nil))
	if got := r.takeDesc(); len(got) != 1 {
		t.Errorf("events %q, want the one gamepad", got)
	}
	// The same serial under another address is refused.
	r.advertise(addrL2, jc2.SideL, true)
	cs := r.tr.connectList()
	r.feed(connectedEvent(cs[1].id, jc2.SideL, serL1, nil))
	if evs := r.take(); len(evs) != 0 {
		t.Errorf("events %v for a duplicate serial", descs(evs))
	}
	if d := r.tr.disconnectList(); !slices.Equal(d, []jc2win.LinkID{cs[1].id}) {
		t.Errorf("disconnects %v", d)
	}
}

func TestHubPowerEvents(t *testing.T) {
	r := newRig(t, func(c *joyCon2Config) { knownAuto(c); c.Power = true })
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	equalStrings(t, "connected", r.takeDesc(), []string{"added 1 Joy-Con 2 (L)"}) // no report, no power yet
	r.reportPower(l, 0, 9<<2)
	equalStrings(t, "first", r.takeDesc(), []string{"power 1 discharging 100"})
	r.reportPower(l, 0, 9<<2) // unchanged
	r.reportPower(l, 0, 7<<2)
	r.reportPower(l, 0, 7<<2|0x02)
	r.reportPower(l, 0, 7<<2|0x01)
	r.reportPower(l, 0, 1<<2)
	r.reportPower(l, 0, 0)
	equalStrings(t, "changes", r.takeDesc(), []string{
		"power 1 discharging 80", "power 1 charging 80", "power 1 wired 80", "power 1 discharging 10", "power 1 discharging 0",
	})

	// A pair reports the lower of the two; a revival sends it again.
	r.reportPower(l, 0, 9<<2)
	r.take()
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.reportPower(rr, 0, 3<<2)
	got := r.takeDesc()
	if got[len(got)-1] != "power 2 discharging 30" {
		t.Errorf("pair events %q, want the right side's 30%% at the end", got)
	}
	r.reportPower(l, 0, 2<<2) // now the left is lower
	equalStrings(t, "left lower", r.takeDesc(), []string{"power 2 discharging 20"})
	r.step(jc2.DefaultSilence)
	r.take()
	r.reportPower(l, 0, 2<<2)
	r.reportPower(rr, 0, 3<<2)
	got = r.takeDesc()
	want := []string{"added 3 Joy-Con 2 (L)", "power 3 discharging 20", "removed 3", "added 4 Joy-Con 2 pair", "power 4 discharging 20"}
	if !slices.Equal(got, want) {
		t.Errorf("revival events %q, want %q: the left alone, then the pair and its power", got, want)
	}
	// Off by default.
	r2 := newRig(t, knownAuto)
	l2 := r2.mustConnect(addrL1, jc2.SideL, serL1)
	r2.reportPower(l2, 0, 9<<2)
	for _, e := range r2.evs {
		if e.Kind == DevicePower {
			t.Error("DevicePower without PowerEvents")
		}
	}
}

func TestHubReadoutAndStatus(t *testing.T) {
	r := newRig(t, func(c *joyCon2Config) { knownAuto(c); c.Power = true })
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	if _, ok := r.h.Readout(joyCon2IDBase); !ok {
		t.Fatal("no readout")
	}
	ro, _ := r.h.Readout(joyCon2IDBase)
	if ro.Power.Percent != -1 || ro.Connection != "wireless" || ro.PlayerIndex != 0 {
		t.Errorf("readout before a report %+v", ro)
	}
	r.reportPower(l, 0, 5<<2|0x02)
	ro, _ = r.h.Readout(joyCon2IDBase)
	if ro.Power != (Power{PowerCharging, 60}) {
		t.Errorf("readout power %+v", ro.Power)
	}
	if _, ok := r.h.Readout(joyCon2IDBase + 5); ok {
		t.Error("readout of an unknown id")
	}
	r.h.OpenWindow(r.now, 10*time.Second)
	st := r.h.Status(r.now)
	if !st.WindowOpen || st.WindowLeft != 10*time.Second || !st.Scanning || !st.AutoConnect || st.Dropped != 7 {
		t.Errorf("status %+v", st)
	}
	if len(st.Known) != 4 || !st.Known[0].Connected || st.Known[1].Connected {
		t.Errorf("known %+v", st.Known)
	}
	if len(st.Links) != 1 {
		t.Fatalf("links %+v", st.Links)
	}
	if got := st.Links[0]; got.Side != jc2.SideL || got.Serial != serL1 || got.Firmware != "2.1.4" || got.State != "connected" ||
		got.Device != joyCon2IDBase || !got.Known || got.ParamsRequest != "accepted" || got.IntervalMs != 15 ||
		got.Latency != 0 || got.TimeoutMs != 9600 || got.Player != 0 {
		t.Errorf("link %+v", got)
	}
	r.advertise(addrR1, jc2.SideR, true) // connecting
	if st = r.h.Status(r.now); st.Links[1].State != "connecting" {
		t.Errorf("link %+v", st.Links[1])
	}
}

func TestHubSnapshots(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	rr := r.mustConnect(addrR1, jc2.SideR, serR1)
	r.report(l, bLL|bLZL, 2048, 3248)
	r.report(rr, bRX)
	snaps := r.h.Snapshots()
	if len(snaps) != 1 || snaps[0].Device != joyCon2IDBase+1 || snaps[0].Info.Type != "joyconpair" {
		t.Fatalf("snapshots %+v", snaps)
	}
	g := snaps[0].gamepadState()
	slices.Sort(g.Buttons)
	if !slices.Equal(g.Buttons, []string{"leftshoulder", "north"}) {
		t.Errorf("buttons %v", g.Buttons)
	}
	if len(g.Axes) != 6 || g.Axes["lefttrigger"] != 32767 || g.Axes["lefty"] != -32767 || g.Axes["rightx"] != 0 {
		t.Errorf("axes %v", g.Axes)
	}
	r.step(jc2.DefaultSilence) // both silent
	if s := r.h.Snapshots(); len(s) != 0 {
		t.Errorf("snapshots %+v of gamepads that are reported removed", s)
	}
}

func TestHubClose(t *testing.T) {
	r := newRig(t, knownAuto)
	l := r.mustConnect(addrL1, jc2.SideL, serL1)
	r.mustConnect(addrR1, jc2.SideR, serR1)
	r.mustConnect(addrL2, jc2.SideL, serL2)
	r.report(l, bLL)
	r.take()
	r.h.Close(r.now)
	if !r.tr.isClosed() {
		t.Fatal("the transport was not closed")
	}
	equalStrings(t, "close", r.takeDesc(), []string{"removed 2", "removed 3"})
	if len(r.trk.live) != 0 {
		t.Errorf("live after Close: %v", r.trk.live)
	}
	// Nothing follows: the transport's events and the timers are ignored.
	r.report(l, bLL|bLZL)
	r.advertise(addrR2, jc2.SideR, true)
	r.step(time.Minute)
	r.tr.push(reportEvent(l, 1, bLL, 2048, 2048, 0))
	r.h.Drain(r.now)
	r.h.Close(r.now)
	if evs := r.take(); len(evs) != 0 || r.tr.closed != 1 {
		t.Errorf("events %v, closes %d after Close", descs(evs), r.tr.closed)
	}
}

func TestHubDrainIsBounded(t *testing.T) {
	r := newRig(t, nil)
	for range joyCon2DrainMax + 10 {
		r.tr.push(advertEvent(addrR2, jc2.SideR, false))
	}
	r.h.Drain(r.now)
	if left := len(r.tr.events); left != 10 {
		t.Errorf("%d events left after one Drain, want 10", left)
	}
	r.h.Drain(r.now)
	if left := len(r.tr.events); left != 0 {
		t.Errorf("%d events left", left)
	}
}

// TestJoyCon2ControlNames checks that every control the jc2 package names is
// one that ButtonName or AxisName produces, so that the events of the hub
// carry names the rest of padmate knows.
func TestJoyCon2ControlNames(t *testing.T) {
	for c := jc2.Control(0); c <= jc2.ControlRightTrigger; c++ {
		name := c.Name()
		if c.IsAxis() {
			i, ok := axisIndex(name)
			if !ok || AxisName(uint8(i)) != name {
				t.Errorf("axis %v: %q is not an AxisName", c, name)
			}
			continue
		}
		i, ok := buttonIndex(name)
		if !ok || ButtonName(uint8(i)) != name {
			t.Errorf("button %v: %q is not a ButtonName", c, name)
		}
	}
	// And the lists of controls that a mode has are all known names.
	for _, mode := range []jc2.Mode{jc2.Horizontal, jc2.Vertical, jc2.Pair} {
		for _, side := range []jc2.Side{jc2.SideL, jc2.SideR} {
			for _, n := range jc2.Controls(mode, side) {
				if _, ok := buttonIndex(n); !ok {
					t.Errorf("%v %v: button %q", mode, side, n)
				}
			}
			for _, n := range jc2.Axes(mode, side) {
				if _, ok := axisIndex(n); !ok {
					t.Errorf("%v %v: axis %q", mode, side, n)
				}
			}
		}
	}
}

// TestHubDefaultCombos checks that the emergency combo of DefaultCombo can be
// pressed on every kind of Joy-Con 2 gamepad: the events of the hub reach a
// ComboWatch like any device's.
func TestHubDefaultCombos(t *testing.T) {
	type press struct {
		l, r uint16 // bits per side, 0 for nothing
	}
	for _, tc := range []struct {
		name     string
		vertical bool
		pair     bool
		side     jc2.Side
		press    press
		fires    bool
	}{
		{"L sideways SL+SR", false, false, jc2.SideL, press{l: bSL | bSR}, true},
		{"R sideways SL+SR", false, false, jc2.SideR, press{r: bSL | bSR}, true},
		{"L sideways SL alone", false, false, jc2.SideL, press{l: bSL}, false},
		{"L upright SL+SR", true, false, jc2.SideL, press{l: bSL | bSR}, true},
		{"R upright SL+SR", true, false, jc2.SideR, press{r: bSL | bSR}, true},
		{"R upright SL alone", true, false, jc2.SideR, press{r: bSL}, false},
		{"pair Minus+Plus", false, true, 0, press{l: bLMinus, r: bRPlus}, true},
		{"pair upright Minus+Plus", true, true, 0, press{l: bLMinus, r: bRPlus}, true},
		{"pair SL+SR of one side", false, true, 0, press{l: bSL | bSR}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, func(c *joyCon2Config) { knownAuto(c); c.Vertical = tc.vertical })
			var l, rr jc2win.LinkID
			switch {
			case tc.pair:
				l = r.mustConnect(addrL1, jc2.SideL, serL1)
				rr = r.mustConnect(addrR1, jc2.SideR, serR1)
			case tc.side == jc2.SideL:
				l = r.mustConnect(addrL1, jc2.SideL, serL1)
			default:
				rr = r.mustConnect(addrR1, jc2.SideR, serR1)
			}
			if l != 0 {
				r.report(l, tc.press.l)
			}
			if rr != 0 {
				r.report(rr, tc.press.r)
			}
			w := NewComboWatch(func(info DeviceInfo) Combo { return DefaultCombo(info, tc.vertical) })
			for _, e := range r.evs {
				w.Observe(e)
			}
			fired := w.Fire(math.MaxUint64)
			if tc.fires != (len(fired) == 1) {
				t.Errorf("combo fired %v, want %v; events %q", fired, tc.fires, descs(r.evs))
			}
			if tc.fires && fired[0].Device != r.liveID() {
				t.Errorf("combo of device %d, want %d", fired[0].Device, r.liveID())
			}
		})
	}
}

// liveID returns the one live gamepad.
func (r *hubRig) liveID() DeviceID {
	if len(r.trk.live) != 1 {
		r.t.Fatalf("%d live gamepads, want one", len(r.trk.live))
	}
	for id := range r.trk.live {
		return id
	}
	return 0
}
