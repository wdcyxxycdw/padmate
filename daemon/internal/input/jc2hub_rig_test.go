package input

import (
	"encoding/binary"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// This file is the test rig of the Joy-Con 2 hub: a fake transport, a tracker
// that holds the hub to the rules a consumer relies on, and builders for the
// bytes of adverts, flash blocks and reports. None of it needs SDL or a
// clock.

// Addresses and serial numbers of the controllers of the tests.
const (
	addrL1 uint64 = 0xC84805000001
	addrR1 uint64 = 0xC84805000002
	addrL2 uint64 = 0xC84805000003
	addrR2 uint64 = 0xC84805000004

	serL1 = "XJL10000000001"
	serR1 = "XJR10000000001"
	serL2 = "XJL10000000002"
	serR2 = "XJR10000000002"
)

// The raw button bits of a report (jc2/report.go), by side.
const (
	bLDown, bLRight, bLLeft, bLUp = 1 << 0, 1 << 1, 1 << 2, 1 << 3
	bLL, bLZL, bLMinus, bLStick   = 1 << 4, 1 << 5, 1 << 6, 1 << 7
	bLCapture                     = 1 << 8
	bRB, bRA, bRY, bRX            = 1 << 0, 1 << 1, 1 << 2, 1 << 3
	bRR, bRZR, bRPlus, bRStick    = 1 << 4, 1 << 5, 1 << 6, 1 << 7
	bRHome, bRC                   = 1 << 8, 1 << 12
	bSR, bSL                      = 1 << 14, 1 << 15
)

// fakeBLE is a JoyCon2Transport that records what the hub asks of it and
// delivers the events that a test queues. Its methods are safe for concurrent
// use, as the real transport's are.
type fakeBLE struct {
	mu          sync.Mutex
	events      chan jc2win.Event
	scans       []bool
	connects    []connectCall
	disconnects []jc2win.LinkID
	closed      int
	next        jc2win.LinkID
	refuse      bool // Connect returns 0, as a closed transport does
}

type connectCall struct {
	addr   uint64
	side   jc2.Side
	player int
	id     jc2win.LinkID
}

func newFakeBLE() *fakeBLE { return &fakeBLE{events: make(chan jc2win.Event, 4096)} }

func (f *fakeBLE) Events() <-chan jc2win.Event { return f.events }

func (f *fakeBLE) Scan(on bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scans = append(f.scans, on)
}

func (f *fakeBLE) Connect(addr uint64, side jc2.Side, player int) jc2win.LinkID {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return 0
	}
	f.next++
	f.connects = append(f.connects, connectCall{addr, side, player, f.next})
	return f.next
}

func (f *fakeBLE) Disconnect(id jc2win.LinkID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.disconnects = append(f.disconnects, id)
}

func (f *fakeBLE) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
}

func (f *fakeBLE) Dropped() uint64 { return 7 }

// push queues an event for the hub to drain.
func (f *fakeBLE) push(ev jc2win.Event) { f.events <- ev }

func (f *fakeBLE) connectList() []connectCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]connectCall(nil), f.connects...)
}

func (f *fakeBLE) disconnectList() []jc2win.LinkID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jc2win.LinkID(nil), f.disconnects...)
}

func (f *fakeBLE) scanList() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool(nil), f.scans...)
}

func (f *fakeBLE) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed > 0
}

// scanning returns the last Scan the transport was told, false before any.
func (f *fakeBLE) scanning() bool {
	s := f.scanList()
	return len(s) > 0 && s[len(s)-1]
}

// tracker holds a stream of Events to the rules that make the hub safe for
// its consumers (CLAUDE.md invariant 1): a DeviceID is added once and never
// again, only a live device has events, a button goes up only after it went
// down, and a removal ends everything of the device. It keeps what is held,
// as a consumer that releases at DeviceRemoved does.
type tracker struct {
	errorf func(format string, args ...any)
	seen   map[DeviceID]bool
	live   map[DeviceID]*DeviceInfo
	held   map[DeviceID]map[string]int16
}

func newTracker(errorf func(format string, args ...any)) *tracker {
	return &tracker{
		errorf: errorf,
		seen:   map[DeviceID]bool{},
		live:   map[DeviceID]*DeviceInfo{},
		held:   map[DeviceID]map[string]int16{},
	}
}

func (k *tracker) see(e Event) {
	if e.Kind == DeviceAdded {
		switch {
		case k.seen[e.Device]:
			k.errorf("DeviceID %d added twice", e.Device)
		case e.Info == nil || e.Info.ID != e.Device:
			k.errorf("DeviceAdded %d with info %+v", e.Device, e.Info)
		}
		k.seen[e.Device] = true
		k.live[e.Device] = e.Info
		k.held[e.Device] = map[string]int16{}
		return
	}
	if _, ok := k.live[e.Device]; !ok {
		k.errorf("%s for device %d, which is not live", e.Kind, e.Device)
		return
	}
	h := k.held[e.Device]
	switch e.Kind {
	case DeviceRemoved:
		delete(k.live, e.Device)
		delete(k.held, e.Device) // a consumer releases everything of it
	case ButtonDown:
		if _, down := h[e.Control]; down {
			k.errorf("device %d: %s down twice", e.Device, e.Control)
		}
		h[e.Control] = 1
	case ButtonUp:
		if _, down := h[e.Control]; !down {
			k.errorf("device %d: %s up without a down", e.Device, e.Control)
		}
		delete(h, e.Control)
	case AxisMotion:
		if e.Value == 0 {
			delete(h, e.Control)
		} else {
			h[e.Control] = e.Value
		}
	case DevicePower, DeviceState:
	default:
		k.errorf("unexpected %s", e.Kind)
	}
}

// heldNames returns what device id holds, sorted: the buttons that are down
// and the axes that are off zero, as "name" and "name=value".
func (k *tracker) heldNames(id DeviceID) []string {
	var out []string
	for c, v := range k.held[id] {
		if isButtonName(c) {
			out = append(out, c)
		} else {
			out = append(out, fmt.Sprintf("%s=%d", c, v))
		}
	}
	sort.Strings(out)
	return out
}

func isButtonName(name string) bool {
	_, ok := buttonIndex(name)
	return ok
}

// desc describes e on one line, for comparing streams.
func desc(e Event) string {
	switch e.Kind {
	case DeviceAdded:
		return fmt.Sprintf("added %d %s", e.Device-joyCon2IDBase+1, e.Info.Name)
	case DeviceRemoved:
		return fmt.Sprintf("removed %d", e.Device-joyCon2IDBase+1)
	case ButtonDown:
		return fmt.Sprintf("down %d %s", e.Device-joyCon2IDBase+1, e.Control)
	case ButtonUp:
		return fmt.Sprintf("up %d %s", e.Device-joyCon2IDBase+1, e.Control)
	case AxisMotion:
		return fmt.Sprintf("axis %d %s=%d", e.Device-joyCon2IDBase+1, e.Control, e.Value)
	case DevicePower:
		return fmt.Sprintf("power %d %s %d", e.Device-joyCon2IDBase+1, e.Power.State, e.Power.Percent)
	}
	return e.Kind.String()
}

func descs(evs []Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = desc(e)
	}
	return out
}

// hubRig drives a hub with a fake transport and a fake clock.
type hubRig struct {
	t          *testing.T
	h          *joyCon2Hub
	tr         *fakeBLE
	trk        *tracker
	now        time.Duration
	evs        []Event
	taken      int
	remembered []KnownJoyCon2
	counter    byte
}

// newRig makes a rig; mod may change the hub's config.
func newRig(t *testing.T, mod func(*joyCon2Config)) *hubRig {
	t.Helper()
	r := &hubRig{t: t, tr: newFakeBLE(), now: time.Second}
	r.trk = newTracker(t.Errorf)
	cfg := joyCon2Config{
		Transport:  r.tr,
		Emit:       r.record,
		OnRemember: func(k KnownJoyCon2) { r.remembered = append(r.remembered, k) },
	}
	if mod != nil {
		mod(&cfg)
	}
	r.h = newJoyCon2Hub(cfg)
	return r
}

func (r *hubRig) record(e Event) {
	r.trk.see(e)
	r.evs = append(r.evs, e)
}

// take returns the events since the last call.
func (r *hubRig) take() []Event {
	out := r.evs[r.taken:]
	r.taken = len(r.evs)
	return out
}

// takeDesc is take as descs.
func (r *hubRig) takeDesc() []string { return descs(r.take()) }

// step moves the clock by d and runs the timers.
func (r *hubRig) step(d time.Duration) {
	r.now += d
	r.h.Tick(r.now)
}

func (r *hubRig) feed(ev jc2win.Event) { r.h.Handle(ev, r.now) }

// advertise delivers an advertisement.
func (r *hubRig) advertise(addr uint64, side jc2.Side, standard bool) {
	r.feed(advertEvent(addr, side, standard))
}

// connect advertises addr and, if the hub connects, brings the link up with
// the serial number; it returns the link, or 0 when the hub did not connect.
func (r *hubRig) connect(addr uint64, side jc2.Side, serial string) jc2win.LinkID {
	r.t.Helper()
	n := len(r.tr.connectList())
	r.advertise(addr, side, true)
	cs := r.tr.connectList()
	if len(cs) == n {
		return 0
	}
	id := cs[n].id
	r.feed(connectedEvent(id, side, serial, nil))
	return id
}

// mustConnect is connect that fails the test when the hub does not connect.
func (r *hubRig) mustConnect(addr uint64, side jc2.Side, serial string) jc2win.LinkID {
	r.t.Helper()
	id := r.connect(addr, side, serial)
	if id == 0 {
		r.t.Fatalf("the hub did not connect to %x", addr)
	}
	return id
}

// report delivers a report with the buttons and the stick at rest, or at
// (x, y) when both are given.
func (r *hubRig) report(id jc2win.LinkID, bits uint16, xy ...uint16) {
	x, y := uint16(2048), uint16(2048)
	if len(xy) == 2 {
		x, y = xy[0], xy[1]
	}
	r.counter++
	r.feed(reportEvent(id, r.counter, bits, x, y, 0x24))
}

// reportPower is report with a power byte.
func (r *hubRig) reportPower(id jc2win.LinkID, bits uint16, power byte) {
	r.counter++
	r.feed(reportEvent(id, r.counter, bits, 2048, 2048, power))
}

// lost delivers the end of a link.
func (r *hubRig) lost(id jc2win.LinkID) { r.feed(jc2win.Event{Kind: jc2win.Lost, Link: id}) }

// advertEvent builds an advertisement of a Joy-Con 2.
func advertEvent(addr uint64, side jc2.Side, standard bool) jc2win.Event {
	data := make([]byte, 24)
	copy(data, []byte{0x01, 0x00, 0x03, 0x7e, 0x05})
	product := uint16(jc2.ProductL)
	if side == jc2.SideR {
		product = jc2.ProductR
	}
	binary.LittleEndian.PutUint16(data[5:], product)
	data[8] = 0x01
	if !standard {
		copy(data[10:16], []byte{0xC8, 0x48, 0x05, 0x11, 0x22, 0x33})
	}
	data[16] = 0x0f
	return jc2win.Event{Kind: jc2win.Advert, Addr: addr, RSSI: -50, Company: jc2.CompanyID, Data: data}
}

// infoBlock builds the FlashInfo block with the serial number.
func infoBlock(serial string) []byte {
	b := make([]byte, 0x40)
	copy(b[2:2+14], serial)
	binary.LittleEndian.PutUint16(b[0x12:], jc2.VendorID)
	binary.LittleEndian.PutUint16(b[0x14:], jc2.ProductL)
	return b
}

// pack12 packs two 12-bit values into 3 bytes, like the stick in a report.
func pack12(x, y uint16) []byte {
	return []byte{byte(x), byte(x>>8)&0x0f | byte(y&0x0f)<<4, byte(y >> 4)}
}

// factoryBlock builds the FlashFactoryStick block with cal at +0x28.
func factoryBlock(cal jc2.StickCal) []byte {
	b := make([]byte, 0x40)
	copy(b[0x28:], pack12(cal.NeutralX, cal.NeutralY))
	copy(b[0x2b:], pack12(cal.PosX, cal.PosY))
	copy(b[0x2e:], pack12(cal.NegX, cal.NegY))
	return b
}

// connectedEvent builds the Connected event of a link; factory may be nil.
func connectedEvent(id jc2win.LinkID, side jc2.Side, serial string, factory []byte) jc2win.Event {
	info := jc2win.LinkInfo{Side: side, Firmware: [3]byte{2, 1, 4}, Interval: "15 ms", FactoryStick: factory}
	if serial != "" {
		info.InfoBlock = infoBlock(serial)
	}
	return jc2win.Event{Kind: jc2win.Connected, Link: id, Info: info}
}

// reportEvent builds a native input report.
func reportEvent(id jc2win.LinkID, counter byte, bits, x, y uint16, power byte) jc2win.Event {
	ev := jc2win.Event{Kind: jc2win.Report, Link: id, Stamp: 1234}
	b := &ev.Report
	b[0], b[1] = counter, power
	b[2], b[3] = byte(bits), byte(bits>>8)
	b[4] = 0x07
	b[5] = byte(x)
	b[6] = byte(x>>8)&0x0f | byte(y&0x0f)<<4
	b[7] = byte(y >> 4)
	b[8] = 0x30
	return ev
}
