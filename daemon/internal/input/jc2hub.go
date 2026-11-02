package input

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// This file is the Joy-Con 2 manager (ADR-0002): the pure logic that turns
// what a Bluetooth LE transport saw (jc2win.Event) into the Events of
// gamepads. It reads no clock and calls no SDL: T-input passes the time of
// each call, on the clock of Event.Time (SDL_GetTicksNS), and everything is
// tested on Linux with a fake transport. Source (ble.go) owns one hub, and
// only T-input touches it.
//
// What it decides:
//
//   - Which controller to connect to (the pairing window, the remembered
//     controllers, auto-connect) and when to scan.
//   - Which gamepads the connected controllers make: one left and one right
//     Joy-Con form one pair gamepad ("joyconpair"); every other controller is
//     a single gamepad, held upright.
//     When a side arrives or goes, the old gamepad is reported removed (its
//     consumer releases everything) before the new one is reported added.
//   - The events: each report is mapped (package jc2) and compared with the
//     gamepad's last state; the differences are the ButtonDown, ButtonUp and
//     AxisMotion events.
//   - The watchdog: a link that is silent for jc2.DefaultSilence is dormant,
//     and the link stays while it counts for no gamepad: the gamepad it was
//     in is reported removed, and a pair that loses a side this way is
//     replaced at once by the side that still reports, as a single (so a
//     live side does not wait for the silent one to be given up). When
//     reports come again, the layout is made anew as for a fresh connection:
//     a gamepad is reported added with a new DeviceID and the same
//     DeviceKey, and a single that finds its partner back is replaced by the
//     pair. After jc2.DefaultLinkGiveUp of silence the link is closed.
//
// Only the hub's own links ever form a pair: a Joy-Con 2 left and a Joy-Con 2
// right. A Switch 1 Joy-Con that SDL reports is a gamepad of Source, never
// combined with a Joy-Con 2, so a mixed connection shows each controller as a
// single gamepad of its own.
//
// No path leaves a key held (CLAUDE.md invariant 1): a gamepad's events stop
// with its DeviceRemoved, which every consumer takes as a release of
// everything of that DeviceID, and a DeviceID is never used again. The hub
// never has to send a ButtonUp for a removal.

// JoyCon2Transport is what the hub needs of the Bluetooth LE transport:
// *jc2win.Transport on Windows, a fake in tests. Its methods must not block.
type JoyCon2Transport interface {
	// Events carries everything the transport saw; the hub reads it without
	// blocking and never expects it to close.
	Events() <-chan jc2win.Event
	// Scan turns the scan for advertisements on or off.
	Scan(on bool)
	// Connect starts a connection and returns the id of its link, or 0 when
	// the transport is closed. The result is a Connected or Failed event.
	Connect(addr uint64, side jc2.Side, player int) jc2win.LinkID
	// Disconnect ends a link, connecting or connected.
	Disconnect(id jc2win.LinkID)
	// Close stops everything and waits for the transport to finish.
	Close()
}

// KnownJoyCon2 is a Joy-Con 2 that padmate remembers and connects to without
// the pairing window (while auto-connect is on).
type KnownJoyCon2 struct {
	// Addr is the Bluetooth address (jc2win.Event.Addr), stable across
	// sessions.
	Addr uint64
	// Serial is the serial number from the controller's flash, the identity
	// that settings keep (DeviceInfo.Serial).
	Serial string
	// Side is the side of the Joy-Con.
	Side jc2.Side
}

// AddrText returns Addr as AA:BB:CC:DD:EE:FF.
func (k KnownJoyCon2) AddrText() string { return jc2win.FormatAddr(k.Addr) }

// The hub's numbers.
const (
	// DefaultJoyCon2Window is how long the pairing window stays open when
	// asked to open without a length.
	DefaultJoyCon2Window = 30 * time.Second
	// MaxJoyCon2Window is the longest pairing window.
	MaxJoyCon2Window = 10 * time.Minute

	// joyCon2Backoff is how long an address is left alone after a connection
	// failed or was closed by padmate: a controller keeps a connection
	// cooldown, and a failing one must not be tried in a tight loop.
	joyCon2Backoff = 5 * time.Second
	// joyCon2MaxLinks is the number of Joy-Cons the hub connects at once: the
	// controllers have eight player numbers.
	joyCon2MaxLinks = 8
	// joyCon2ConnectTimeout is how long a link may stay connecting before
	// the hub cancels it (the transport has timeouts of its own).
	joyCon2ConnectTimeout = 30 * time.Second
	// joyCon2CloseTimeout is how long the hub waits for the Lost or Failed
	// of a link it closed, before it forgets the link so that its address
	// can be used again.
	joyCon2CloseTimeout = 10 * time.Second
	// joyCon2DrainMax bounds the transport events one Drain handles, so
	// that a flood of advertisements cannot hold up T-input.
	joyCon2DrainMax = 512

	// joyCon2IDBase is the first DeviceID of the hub. SDL counts its ids up
	// from 1, the receivers' aliases count down from 2³²−1 and the tests' fake
	// ids are 0x7ff00001 and near: this range meets none of them.
	joyCon2IDBase DeviceID = 0x40000001

	// joyCon2ProductPair is the product id the hub gives a pair; the real
	// Joy-Cons are jc2.ProductL and jc2.ProductR.
	joyCon2ProductPair = 0x2068

	// guidBusBluetooth is the bus type of a Bluetooth GUID (SDL_HARDWARE_BUS_BLUETOOTH).
	guidBusBluetooth = 0x0005
	// guidBLE is the driver signature of the GUIDs the hub makes. SDL has no
	// driver for these controllers, so it is padmate's own (guid.go).
	guidBLE = 'b'
)

// linkPhase is where a link is in its life.
type linkPhase uint8

const (
	linkConnecting linkPhase = iota + 1
	linkConnected
	// linkClosing is a link that padmate ended and whose Lost or Failed has
	// not come. Nothing of it is used any more.
	linkClosing
)

// jc2Link is one connection to a Joy-Con 2.
type jc2Link struct {
	id    jc2win.LinkID
	addr  uint64
	side  jc2.Side
	phase linkPhase
	since time.Duration // when the phase began
	// candidate is set on the connection that the pairing window started: it
	// is remembered once it is up.
	candidate bool
	player    int
	connSeq   int // the order in which links came up

	serial   string
	cal      jc2.StickCal
	firmware [3]byte
	// paramsRequest is what became of the request for the
	// throughput-optimized connection parameters; params are the current
	// ones, as far as the transport can tell (zero when unknown).
	paramsRequest string
	params        jc2win.ConnParams

	silence jc2.Silence
	dormant bool
	rep     jc2.Report // the last report, valid when haveRep
	haveRep bool

	dev *jc2Device
}

// jc2Device is a gamepad made of one link, or of the left and the right one.
type jc2Device struct {
	links []*jc2Link // one, or the left and then the right
	mode  jc2.Mode
	// id is the DeviceID of the gamepad while it is live, or of its last
	// life.
	id    DeviceID
	live  bool
	state jc2.State // as reported to the consumer
	info  *DeviceInfo
	// power is the value sent last, when powerSent.
	power     Power
	powerSent bool
}

// joyCon2Config is what newJoyCon2Hub needs.
type joyCon2Config struct {
	Transport JoyCon2Transport
	// Emit receives the events, in order: Source.emit on T-input.
	Emit func(Event)
	// Known are the remembered controllers; AutoConnect says whether to
	// connect to them.
	Known       []KnownJoyCon2
	AutoConnect bool
	// OnRemember is called when a controller of the pairing window has been
	// connected and read: the entry to remember. It runs on the caller of
	// Handle and must return at once.
	OnRemember func(KnownJoyCon2)
	// Power turns DevicePower events on.
	Power bool
	// Silence and GiveUp override jc2.DefaultSilence and
	// jc2.DefaultLinkGiveUp when positive.
	Silence, GiveUp time.Duration
	Log             *slog.Logger
}

// joyCon2Hub is the manager. Its methods must be called from one goroutine;
// none blocks.
type joyCon2Hub struct {
	tr         JoyCon2Transport
	emit       func(Event)
	onRemember func(KnownJoyCon2)
	power      bool
	silence    time.Duration
	giveUp     time.Duration
	log        *slog.Logger

	known []KnownJoyCon2
	auto  bool
	// winOpen is set while the pairing window is open, until winEnd.
	winOpen  bool
	winEnd   time.Duration
	scanning bool // what the transport was last told

	links   []*jc2Link // in the order they were started
	devs    []*jc2Device
	backoff map[uint64]time.Duration // address → left alone until
	seq     int
	nextID  DeviceID
	closed  bool

	// stamp is the latency stamp of the report being handled.
	stamp latency.Stamp
	// scratch holds the changes of one report.
	scratch []jc2.Change
}

// newJoyCon2Hub makes the hub and starts the scan if the policy asks for it.
func newJoyCon2Hub(c joyCon2Config) *joyCon2Hub {
	h := &joyCon2Hub{
		tr:         c.Transport,
		emit:       c.Emit,
		onRemember: c.OnRemember,
		power:      c.Power,
		silence:    c.Silence,
		giveUp:     c.GiveUp,
		log:        c.Log,
		auto:       c.AutoConnect,
		backoff:    make(map[uint64]time.Duration),
		nextID:     joyCon2IDBase,
	}
	if h.log == nil {
		h.log = slog.New(slog.DiscardHandler)
	}
	h.known = dedupeKnown(c.Known)
	h.syncScan()
	return h
}

// dedupeKnown returns a copy of list in which an address and a serial occur
// once (the last entry wins), and entries without an address are dropped.
func dedupeKnown(list []KnownJoyCon2) []KnownJoyCon2 {
	var out []KnownJoyCon2
	for _, k := range list {
		if k.Addr == 0 || (k.Side != jc2.SideL && k.Side != jc2.SideR) {
			continue
		}
		out = slices.DeleteFunc(out, func(o KnownJoyCon2) bool {
			return o.Addr == k.Addr || (k.Serial != "" && o.Serial == k.Serial)
		})
		out = append(out, k)
	}
	return out
}

// wantScan is the scan policy: scan exactly while the pairing window is
// open, or while there is a remembered controller to connect to.
func (h *joyCon2Hub) wantScan() bool {
	return h.winOpen || (h.auto && len(h.known) > 0)
}

// syncScan tells the transport when the scan policy changed.
func (h *joyCon2Hub) syncScan() {
	if want := h.wantScan(); want != h.scanning {
		h.scanning = want
		h.tr.Scan(want)
	}
}

// Drain handles the events that the transport has now, at now, at most
// joyCon2DrainMax of them. It never blocks. It is the first thing a turn of
// T-input does with the hub, before Tick: a link whose reports waited in the
// channel is seen alive before its silence is looked at.
func (h *joyCon2Hub) Drain(now time.Duration) {
	ch := h.tr.Events()
	for range joyCon2DrainMax {
		select {
		case ev, ok := <-ch:
			if !ok {
				return
			}
			h.Handle(ev, now)
		default:
			return
		}
	}
}

// Handle handles one transport event at now.
func (h *joyCon2Hub) Handle(ev jc2win.Event, now time.Duration) {
	if h.closed {
		return
	}
	switch ev.Kind {
	case jc2win.Advert:
		h.advert(ev, now)
	case jc2win.Connected:
		h.connected(ev, now)
	case jc2win.Report:
		h.report(ev, now)
	case jc2win.Params:
		h.paramsChanged(ev)
	case jc2win.Lost:
		if l := h.find(ev.Link); l != nil {
			h.log.Info("joy-con 2 link lost", "addr", jc2win.FormatAddr(l.addr), "serial", l.serial, "err", ev.Err)
			h.drop(l, now)
		}
	case jc2win.Failed:
		if l := h.find(ev.Link); l != nil {
			h.log.Info("joy-con 2 connection failed", "addr", jc2win.FormatAddr(l.addr), "err", ev.Err)
			h.backoff[l.addr] = now + joyCon2Backoff
			h.drop(l, now)
		}
	}
}

// find returns the link with the id, or nil.
func (h *joyCon2Hub) find(id jc2win.LinkID) *jc2Link {
	for _, l := range h.links {
		if l.id == id {
			return l
		}
	}
	return nil
}

// findAddr returns the link to the address addr, whatever its phase.
func (h *joyCon2Hub) findAddr(addr uint64) *jc2Link {
	for _, l := range h.links {
		if l.addr == addr {
			return l
		}
	}
	return nil
}

// knownAddr returns the remembered controller at addr.
func (h *joyCon2Hub) knownAddr(addr uint64) (KnownJoyCon2, bool) {
	for _, k := range h.known {
		if k.Addr == addr {
			return k, true
		}
	}
	return KnownJoyCon2{}, false
}

// windowActive reports whether the pairing window is open at now.
func (h *joyCon2Hub) windowActive(now time.Duration) bool {
	return h.winOpen && now < h.winEnd
}

// hasCandidate reports whether the pairing window has a connection under way.
func (h *joyCon2Hub) hasCandidate() bool {
	for _, l := range h.links {
		if l.candidate && l.phase != linkClosing {
			return true
		}
	}
	return false
}

// freePlayer returns the lowest player number that no live link has, or -1.
func (h *joyCon2Hub) freePlayer() int {
	var used uint8
	for _, l := range h.links {
		if l.phase != linkClosing {
			used |= 1 << l.player
		}
	}
	for n := range joyCon2MaxLinks {
		if used&(1<<n) == 0 {
			return n
		}
	}
	return -1
}

// advert decides whether to connect to the controller that advertised.
func (h *joyCon2Hub) advert(ev jc2win.Event, now time.Duration) {
	a, ok := jc2.ParseAdvert(ev.Company, ev.Data)
	// Only the standard advertisement, which the controller sends while SYNC is
	// held, accepts a PC; one with a host address is a controller that
	// reconnects to its console, and is never connected.
	if !ok || !a.Standard || h.findAddr(ev.Addr) != nil {
		return
	}
	if until, ok := h.backoff[ev.Addr]; ok {
		if now < until {
			return
		}
		delete(h.backoff, ev.Addr)
	}
	_, known := h.knownAddr(ev.Addr)
	candidate := false
	switch {
	case h.windowActive(now) && !h.hasCandidate():
		candidate = true // the first controller of the window
	case known && h.auto:
	default:
		return
	}
	player := h.freePlayer()
	if player < 0 {
		return
	}
	id := h.tr.Connect(ev.Addr, a.Side, player)
	if id == 0 {
		return // the transport is closed
	}
	h.links = append(h.links, &jc2Link{
		id: id, addr: ev.Addr, side: a.Side, phase: linkConnecting, since: now,
		candidate: candidate, player: player,
	})
	h.log.Info("joy-con 2 connecting", "addr", jc2win.FormatAddr(ev.Addr), "side", a.Side.String(), "player", player, "window", candidate)
}

// connected handles a link that came up: it reads the controller's identity
// and stick calibration, remembers a controller of the pairing window and
// reports the gamepad.
func (h *joyCon2Hub) connected(ev jc2win.Event, now time.Duration) {
	l := h.find(ev.Link)
	if l == nil || l.phase != linkConnecting {
		return // unknown, or a link that padmate has ended already
	}
	serial := ""
	if inf, err := jc2.ParseInfo(ev.Info.InfoBlock); err == nil {
		serial = inf.Serial
	}
	if k, ok := h.knownAddr(l.addr); ok && serial == "" {
		serial = k.Serial
	}
	if serial == "" {
		h.log.Warn("joy-con 2 has no readable serial number: disconnected", "addr", jc2win.FormatAddr(l.addr))
		h.end(l, now, true)
		return
	}
	for _, o := range h.links {
		if o != l && o.phase == linkConnected && o.serial == serial {
			h.log.Warn("joy-con 2 already connected under another address: disconnected", "addr", jc2win.FormatAddr(l.addr), "serial", serial)
			h.end(l, now, true)
			return
		}
	}
	user, _ := jc2.ParseUserStick(ev.Info.UserStick)
	factory, _ := jc2.ParseFactoryStick(ev.Info.FactoryStick)
	l.cal = jc2.ChooseStick(user, factory)
	l.serial, l.firmware = serial, ev.Info.Firmware
	l.paramsRequest, l.params = ev.Info.ParamsRequest, ev.Info.Params
	l.phase, l.since = linkConnected, now
	h.seq++
	l.connSeq = h.seq
	l.silence = jc2.Silence{Timeout: h.silence, GiveUp: h.giveUp}
	l.silence.Seen(now)
	h.log.Info("joy-con 2 connected", "addr", jc2win.FormatAddr(l.addr), "side", l.side.String(), "serial", serial, "player", l.player,
		"firmware", fmt.Sprintf("%d.%d.%d", l.firmware[0], l.firmware[1], l.firmware[2]),
		"calibration", calSource(user, factory), "params_request", l.paramsRequest,
		"interval_ms", l.params.IntervalMs(), "latency", l.params.Latency, "timeout_ms", l.params.Timeout.Milliseconds())
	if l.candidate {
		l.candidate = false
		h.remember(KnownJoyCon2{Addr: l.addr, Serial: serial, Side: l.side})
		h.closeWindow()
	}
	h.layout(now)
}

// calSource names the calibration that jc2.ChooseStick takes, for logs.
func calSource(user, factory jc2.StickCal) string {
	switch {
	case user.Valid():
		return "user"
	case factory.Valid():
		return "factory"
	}
	return "default"
}

// remember adds k to the remembered controllers, and tells OnRemember unless
// they have it as it is.
func (h *joyCon2Hub) remember(k KnownJoyCon2) {
	if old, ok := h.knownAddr(k.Addr); ok && old == k {
		return
	}
	h.known = dedupeKnown(append(h.known, k))
	if h.onRemember != nil {
		h.onRemember(k)
	}
	h.syncScan()
}

// report handles a native input report.
func (h *joyCon2Hub) report(ev jc2win.Event, now time.Duration) {
	l := h.find(ev.Link)
	if l == nil || l.phase != linkConnected {
		return
	}
	r, err := jc2.ParseReport(l.side, ev.Report[:])
	if err != nil {
		return // cannot happen: the transport sends whole reports
	}
	l.silence.Seen(now)
	wasDormant := l.dormant
	l.rep, l.haveRep, l.dormant = r, true, false
	h.stamp = ev.Stamp
	if wasDormant {
		// The link counts again: the layout is made anew, and the gamepad it
		// is in is reported added with what is held now as fresh presses.
		h.layout(now)
	}
	if dev := l.dev; dev != nil && dev.live {
		h.emitDiff(dev, now)
		h.updatePower(dev, now)
	}
	h.stamp = 0
}

// paramsChanged handles the new connection parameters of a link.
func (h *joyCon2Hub) paramsChanged(ev jc2win.Event) {
	l := h.find(ev.Link)
	if l == nil || l.phase != linkConnected || l.params == ev.Params {
		return
	}
	h.log.Info("joy-con 2 connection parameters changed", "addr", jc2win.FormatAddr(l.addr), "serial", l.serial,
		"interval_ms", ev.Params.IntervalMs(), "latency", ev.Params.Latency, "timeout_ms", ev.Params.Timeout.Milliseconds(),
		"was_interval_ms", l.params.IntervalMs())
	l.params = ev.Params
}

// Tick runs the timers at now: the pairing window, the silence watchdog of
// every link, and the time limits of connecting and closing links.
func (h *joyCon2Hub) Tick(now time.Duration) {
	if h.closed {
		return
	}
	if h.winOpen && now >= h.winEnd {
		h.closeWindow()
	}
	changed := false
	for _, l := range slices.Clone(h.links) {
		switch l.phase {
		case linkConnected:
			switch {
			case l.silence.GaveUp(now):
				h.log.Warn("joy-con 2 silent: link closed", "addr", jc2win.FormatAddr(l.addr), "serial", l.serial)
				h.end(l, now, true)
				changed = true
			case !l.dormant && l.silence.Dead(now):
				l.dormant = true
				h.log.Warn("joy-con 2 silent: link kept, gamepad reported removed", "addr", jc2win.FormatAddr(l.addr), "serial", l.serial)
				changed = true // the layout drops it; its partner becomes a single at once
			}
		case linkConnecting:
			if now-l.since >= joyCon2ConnectTimeout {
				h.log.Warn("joy-con 2 connection timed out", "addr", jc2win.FormatAddr(l.addr))
				h.end(l, now, true)
			}
		case linkClosing:
			if now-l.since >= joyCon2CloseTimeout {
				h.log.Warn("joy-con 2 link did not report its end", "addr", jc2win.FormatAddr(l.addr))
				h.removeLink(l)
			}
		}
	}
	if changed {
		h.layout(now)
	}
}

// end closes the link l on padmate's side: the transport is asked to
// disconnect it, and it counts for nothing from now on. With backoff its
// address is left alone for a while. The gamepads follow when the caller
// calls layout, once for all the links it ends.
func (h *joyCon2Hub) end(l *jc2Link, now time.Duration, backoff bool) {
	if l.phase == linkClosing {
		return
	}
	l.phase, l.since, l.candidate = linkClosing, now, false
	h.tr.Disconnect(l.id)
	if backoff {
		h.backoff[l.addr] = now + joyCon2Backoff
	}
}

// drop removes the link l, which the transport reported gone.
func (h *joyCon2Hub) drop(l *jc2Link, now time.Duration) {
	connected := l.phase == linkConnected
	h.removeLink(l)
	if connected {
		h.layout(now)
	}
}

// removeLink forgets the link l.
func (h *joyCon2Hub) removeLink(l *jc2Link) {
	h.links = slices.DeleteFunc(h.links, func(o *jc2Link) bool { return o == l })
}

// layout makes the gamepads fit the links that are connected and not dormant:
// one left and one right make a pair (the first of each, in the order they
// came up), and every other link is a gamepad of its own. A dormant link is in
// no gamepad, so when one side of a pair goes silent the pair is replaced at
// once by the other side alone, and when it reports again the pair is made
// again. A gamepad that no longer fits is reported removed first; then the
// new ones are reported added, each with a new DeviceID and with what its
// controls hold now as fresh presses.
func (h *joyCon2Hub) layout(now time.Duration) {
	var conn []*jc2Link
	for _, l := range h.links {
		if l.phase == linkConnected && !l.dormant {
			conn = append(conn, l)
		}
	}
	slices.SortFunc(conn, func(a, b *jc2Link) int { return a.connSeq - b.connSeq })
	var left, right *jc2Link
	for _, l := range conn {
		switch {
		case l.side == jc2.SideL && left == nil:
			left = l
		case l.side == jc2.SideR && right == nil:
			right = l
		}
	}
	var want [][]*jc2Link
	paired := left != nil && right != nil
	if paired {
		want = append(want, []*jc2Link{left, right})
	}
	for _, l := range conn {
		if !paired || (l != left && l != right) {
			want = append(want, []*jc2Link{l})
		}
	}
	used := make([]bool, len(want))
	keep := h.devs[:0:0]
	for _, dev := range h.devs {
		i := slices.IndexFunc(want, func(w []*jc2Link) bool { return slices.Equal(dev.links, w) })
		if i >= 0 && !used[i] {
			used[i] = true
			keep = append(keep, dev)
			continue
		}
		h.retire(dev, now)
	}
	h.devs = keep
	for i, w := range want {
		if used[i] {
			continue
		}
		dev := &jc2Device{links: w, mode: h.modeOf(w)}
		for _, l := range w {
			l.dev = dev
		}
		h.devs = append(h.devs, dev)
		h.addLive(dev, now)
	}
}

// modeOf returns the mode of a gamepad made of the links.
func (h *joyCon2Hub) modeOf(links []*jc2Link) jc2.Mode {
	if len(links) == 2 {
		return jc2.Pair
	}
	return jc2.Vertical // a single Joy-Con is always held upright
}

// retire ends the gamepad dev, whose links changed or went silent: it is
// reported removed if it was live.
func (h *joyCon2Hub) retire(dev *jc2Device, now time.Duration) {
	h.removeLive(dev, now)
	for _, l := range dev.links {
		if l.dev == dev {
			l.dev = nil
		}
	}
}

// addLive reports the gamepad dev as a new device, with a new DeviceID, and
// then what its controls hold: the state starts from nothing held, so every
// control that is down now is a fresh press.
func (h *joyCon2Hub) addLive(dev *jc2Device, now time.Duration) {
	dev.id = h.nextID
	h.nextID++
	dev.live, dev.state, dev.powerSent = true, jc2.State{}, false
	dev.info = h.describe(dev)
	h.emit(Event{Time: uint64(now), Device: dev.id, Kind: DeviceAdded, Info: dev.info})
	h.updatePower(dev, now)
	h.emitDiff(dev, now)
	h.log.Info("joy-con 2 gamepad added", "id", dev.id, "name", dev.info.Name, "mode", dev.mode.String())
}

// removeLive reports the live gamepad dev removed. Nothing of it follows.
func (h *joyCon2Hub) removeLive(dev *jc2Device, now time.Duration) {
	if !dev.live {
		return
	}
	dev.live, dev.state = false, jc2.State{}
	h.emit(Event{Time: uint64(now), Device: dev.id, Kind: DeviceRemoved})
	h.log.Info("joy-con 2 gamepad removed", "id", dev.id)
}

// restReport is the report of a controller that has sent none yet: nothing
// pressed, the stick at its center.
func restReport(cal jc2.StickCal) jc2.Report {
	return jc2.Report{StickX: cal.NeutralX, StickY: cal.NeutralY}
}

// reportOf returns the last report of l, or the rest report.
func reportOf(l *jc2Link) jc2.Report {
	if l.haveRep {
		return l.rep
	}
	return restReport(l.cal)
}

// stateOf maps the last reports of the gamepad's links.
func stateOf(dev *jc2Device) jc2.State {
	if dev.mode == jc2.Pair {
		l, r := dev.links[0], dev.links[1]
		return jc2.MapPair(reportOf(l), reportOf(r), l.cal, r.cal)
	}
	l := dev.links[0]
	return jc2.Map(dev.mode, l.side, reportOf(l), l.cal)
}

// emitDiff sends the changes from the gamepad's reported state to the one its
// reports make now.
func (h *joyCon2Hub) emitDiff(dev *jc2Device, now time.Duration) {
	next := stateOf(dev)
	if next == dev.state {
		return
	}
	h.scratch = jc2.AppendDiff(h.scratch[:0], dev.state, next)
	dev.state = next
	t := uint64(now)
	for _, c := range h.scratch {
		e := Event{Time: t, Stamp: h.stamp, Device: dev.id, Control: c.Control.Name()}
		switch {
		case c.Control.IsAxis():
			e.Kind, e.Value = AxisMotion, c.Value
		case c.Down:
			e.Kind = ButtonDown
		default:
			e.Kind = ButtonUp
		}
		h.emit(e)
	}
}

// describe makes the DeviceInfo of the gamepad dev, with its DeviceID.
//
// The ids are Nintendo's: vendor 0x057e, product 0x2067 for the left and
// 0x2066 for the right Joy-Con (jc2.ProductL, jc2.ProductR) and 0x2068 for a
// pair. The serial is the controller's, or for a pair the left one's and the
// right one's joined by "+", so the DeviceKey of every gamepad stays the same
// across connections.
func (h *joyCon2Hub) describe(dev *jc2Device) *DeviceInfo {
	l := dev.links[0]
	info := &DeviceInfo{
		ID:           dev.id,
		Vendor:       joyCon2Vendor,
		Driver:       "ble",
		IsGamepad:    true,
		Serial:       l.serial,
		Path:         "ble:" + jc2win.FormatAddr(l.addr),
		ButtonLabels: FaceButtonLabels{South: "B", East: "A", West: "Y", North: "X"},
		Capabilities: Capabilities{Known: true},
	}
	switch {
	case dev.mode == jc2.Pair:
		info.Name, info.Type, info.Product = "Joy-Con 2 pair", "joyconpair", joyCon2ProductPair
		info.Serial += "+" + dev.links[1].serial
		info.Path += "+" + jc2win.FormatAddr(dev.links[1].addr)
	case l.side == jc2.SideL:
		info.Name, info.Type, info.Product = "Joy-Con 2 (L)", "joyconleft", jc2.ProductL
	default:
		info.Name, info.Type, info.Product = "Joy-Con 2 (R)", "joyconright", jc2.ProductR
	}
	info.GUID = joyCon2GUID(info.Vendor, info.Product).String()
	for _, name := range jc2.Controls(dev.mode, l.side) {
		if i, ok := buttonIndex(name); ok {
			info.ButtonMask |= 1 << i
		}
	}
	for _, name := range jc2.Axes(dev.mode, l.side) {
		if i, ok := axisIndex(name); ok {
			info.AxisMask |= 1 << i
		}
	}
	return info
}

// joyCon2Vendor is Nintendo's USB vendor id (jc2.VendorID).
const joyCon2Vendor = jc2.VendorID

// joyCon2GUID makes the GUID of a Joy-Con 2 gamepad in SDL's layout
// (guid.go): the Bluetooth bus type, no name CRC (SDL has no device here to
// take a name from), the vendor and product ids, product version 0 and the
// driver signature guidBLE. It is the same for every Joy-Con 2 of a product,
// as SDL's is for a Joy-Con, and the serial number tells them apart in the
// DeviceKey.
func joyCon2GUID(vendor, product uint16) GUID {
	var g GUID
	binary.LittleEndian.PutUint16(g[0:], guidBusBluetooth)
	binary.LittleEndian.PutUint16(g[4:], vendor)
	binary.LittleEndian.PutUint16(g[8:], product)
	g[14] = guidBLE
	return g
}

// joyCon2Power converts the power byte of a report: the level 0..9 is a
// percent in steps of ten, charging is charging and external power without
// charging is wired.
func joyCon2Power(p jc2.Power) Power {
	out := Power{State: PowerDischarging, Percent: (int(p.Level)*100/9 + 5) / 10 * 10}
	switch {
	case p.Charging:
		out.State = PowerCharging
	case p.External:
		out.State = PowerWired
	}
	return out
}

// devicePower returns the power of the gamepad dev: its link's, or for a
// pair the lower of the two. It reports false until every link has sent a
// report.
func devicePower(dev *jc2Device) (Power, bool) {
	var out Power
	for i, l := range dev.links {
		if !l.haveRep {
			return Power{}, false
		}
		p := joyCon2Power(l.rep.Power)
		if i == 0 || p.Percent < out.Percent || (p.Percent == out.Percent && p.State == PowerDischarging) {
			out = p
		}
	}
	return out, true
}

// updatePower sends the power of dev when it is not the one sent last.
func (h *joyCon2Hub) updatePower(dev *jc2Device, now time.Duration) {
	if !h.power || !dev.live {
		return
	}
	p, ok := devicePower(dev)
	if !ok || (dev.powerSent && dev.power == p) {
		return
	}
	dev.power, dev.powerSent = p, true
	h.emit(Event{Time: uint64(now), Device: dev.id, Kind: DevicePower, Power: &p})
}

// OpenWindow opens the pairing window for d (DefaultJoyCon2Window when not
// positive) at now: the first controller that advertises while its SYNC
// button is held is connected and remembered.
func (h *joyCon2Hub) OpenWindow(now, d time.Duration) {
	if d <= 0 {
		d = DefaultJoyCon2Window
	}
	h.winOpen, h.winEnd = true, now+min(d, MaxJoyCon2Window)
	h.syncScan()
}

// CancelWindow closes the pairing window, and ends the connection that it
// has started and that is not up yet.
func (h *joyCon2Hub) CancelWindow(now time.Duration) {
	h.closeWindow()
	for _, l := range slices.Clone(h.links) {
		if l.candidate && l.phase == linkConnecting {
			h.end(l, now, false)
		}
	}
}

// closeWindow closes the pairing window.
func (h *joyCon2Hub) closeWindow() {
	h.winOpen = false
	h.syncScan()
}

// SetAutoConnect turns the connection to remembered controllers on or off.
// Controllers that are connected stay so.
func (h *joyCon2Hub) SetAutoConnect(on bool) {
	h.auto = on
	h.syncScan()
}

// SetKnown replaces the remembered controllers. One that is no longer in the
// list is forgotten: if it is connected, it is disconnected.
func (h *joyCon2Hub) SetKnown(list []KnownJoyCon2, now time.Duration) {
	next := dedupeKnown(list)
	for _, k := range h.known {
		if !slices.ContainsFunc(next, func(o KnownJoyCon2) bool { return o.Addr == k.Addr }) {
			h.disconnectKnown(k, now)
		}
	}
	h.known = next
	h.layout(now)
	h.syncScan()
}

// ForgetSerial forgets the remembered controller with the serial number, and
// disconnects it if it is connected. It reports whether there was one.
func (h *joyCon2Hub) ForgetSerial(serial string, now time.Duration) bool {
	return h.forget(func(k KnownJoyCon2) bool { return k.Serial == serial }, now)
}

// ForgetAddr is ForgetSerial for the address.
func (h *joyCon2Hub) ForgetAddr(addr uint64, now time.Duration) bool {
	return h.forget(func(k KnownJoyCon2) bool { return k.Addr == addr }, now)
}

func (h *joyCon2Hub) forget(match func(KnownJoyCon2) bool, now time.Duration) bool {
	found := false
	for _, k := range slices.Clone(h.known) {
		if match(k) {
			found = true
			h.known = slices.DeleteFunc(h.known, func(o KnownJoyCon2) bool { return o.Addr == k.Addr })
			h.disconnectKnown(k, now)
		}
	}
	h.layout(now)
	h.syncScan()
	return found
}

// disconnectKnown ends the link of the controller k, if it has one. A
// controller that is forgotten while it connects must not be remembered when
// it comes up, which end sees to: the link is no longer connecting.
func (h *joyCon2Hub) disconnectKnown(k KnownJoyCon2, now time.Duration) {
	for _, l := range slices.Clone(h.links) {
		if l.addr == k.Addr || (k.Serial != "" && l.serial == k.Serial) {
			h.end(l, now, true)
		}
	}
}

// Has reports whether id is a gamepad of the hub that is reported added now.
func (h *joyCon2Hub) Has(id DeviceID) bool {
	return h.live(id) != nil
}

// live returns the live gamepad with the id.
func (h *joyCon2Hub) live(id DeviceID) *jc2Device {
	if id < joyCon2IDBase {
		return nil
	}
	for _, dev := range h.devs {
		if dev.live && dev.id == id {
			return dev
		}
	}
	return nil
}

// Disconnect closes the links of the live gamepad id, both of a pair. It
// returns false if there is no such gamepad.
func (h *joyCon2Hub) Disconnect(id DeviceID, now time.Duration) bool {
	dev := h.live(id)
	if dev == nil {
		return false
	}
	for _, l := range slices.Clone(dev.links) {
		h.end(l, now, true)
	}
	h.layout(now)
	return true
}

// Readout returns the power and the player number of the live gamepad id.
func (h *joyCon2Hub) Readout(id DeviceID) (Readout, bool) {
	dev := h.live(id)
	if dev == nil {
		return Readout{}, false
	}
	r := Readout{Power: Power{Percent: -1}, Connection: "wireless", PlayerIndex: dev.links[0].player}
	if p, ok := devicePower(dev); ok {
		r.Power = p
	}
	for _, l := range dev.links[1:] {
		r.PlayerIndex = min(r.PlayerIndex, l.player)
	}
	return r, true
}

// joyCon2Snap is the state of one live gamepad, for the snapshot that a
// resume publishes.
type joyCon2Snap struct {
	Device DeviceID
	Info   *DeviceInfo
	State  jc2.State
}

// Snapshots returns the live gamepads with the state that was reported for
// each.
func (h *joyCon2Hub) Snapshots() []joyCon2Snap {
	var out []joyCon2Snap
	for _, dev := range h.devs {
		if dev.live {
			out = append(out, joyCon2Snap{Device: dev.id, Info: dev.info, State: dev.state})
		}
	}
	return out
}

// gamepadState converts the state of a Joy-Con 2 gamepad to the form of a
// resume snapshot: the names of the buttons that are down and all six axes.
func (s joyCon2Snap) gamepadState() *GamepadState {
	g := &GamepadState{Axes: make(map[string]int16, 6)}
	for c := jc2.Control(0); c <= jc2.ControlRightTrigger; c++ {
		switch {
		case c.IsAxis():
			g.Axes[c.Name()] = s.State.Axis(c)
		case s.State.Down(c):
			g.Buttons = append(g.Buttons, c.Name())
		}
	}
	return g
}

// JoyCon2Status is what the hub knows, for the tray and the UI.
type JoyCon2Status struct {
	// WindowOpen says that the pairing window is open; WindowLeft is how long
	// it stays so and WindowDeadline when it closes.
	WindowOpen     bool
	WindowLeft     time.Duration
	WindowDeadline time.Time
	// Scanning says that the transport scans for advertisements.
	Scanning bool
	// AutoConnect is the auto-connect setting.
	AutoConnect bool
	// Known are the remembered controllers.
	Known []JoyCon2KnownStatus
	// Links are the connections, in the order they were started.
	Links []JoyCon2LinkStatus
	// Dropped is the number of events the transport lost to a full channel.
	Dropped uint64
}

// JoyCon2KnownStatus is a remembered controller.
type JoyCon2KnownStatus struct {
	KnownJoyCon2
	// Connected says that a link to it is up.
	Connected bool
}

// JoyCon2LinkStatus is one connection.
type JoyCon2LinkStatus struct {
	Addr   uint64
	Side   jc2.Side
	Serial string
	Player int
	// Firmware is "major.minor.patch", or "" when unknown.
	Firmware string
	// ParamsRequest says what became of the request for the
	// throughput-optimized connection parameters: "accepted",
	// "unavailable (Windows 10)" or "failed: <why>"; "" until the link is up.
	ParamsRequest string
	// IntervalMs is the current connection interval in milliseconds, 0 when
	// unknown (Windows 10 cannot tell). Latency is the peripheral latency in
	// connection events and TimeoutMs the supervision timeout; both mean
	// something only when IntervalMs is not 0.
	IntervalMs float64
	Latency    int
	TimeoutMs  int
	// State is "connecting", "connected", "dormant" (silent, its gamepad
	// reported removed) or "closing".
	State string
	// Device is the DeviceID of its gamepad, or 0 when it has none reported.
	Device DeviceID
	// Known says that the controller is remembered.
	Known bool
}

// Status returns the hub's state at now. WindowDeadline is left for the
// caller, which knows the wall clock.
func (h *joyCon2Hub) Status(now time.Duration) JoyCon2Status {
	st := JoyCon2Status{
		WindowOpen:  h.windowActive(now),
		Scanning:    h.scanning,
		AutoConnect: h.auto,
	}
	if st.WindowOpen {
		st.WindowLeft = h.winEnd - now
	}
	for _, k := range h.known {
		l := h.findAddr(k.Addr)
		st.Known = append(st.Known, JoyCon2KnownStatus{KnownJoyCon2: k, Connected: l != nil && l.phase == linkConnected})
	}
	for _, l := range h.links {
		ls := JoyCon2LinkStatus{Addr: l.addr, Side: l.side, Serial: l.serial, Player: l.player, ParamsRequest: l.paramsRequest}
		if l.params.Known() {
			ls.IntervalMs, ls.Latency, ls.TimeoutMs = l.params.IntervalMs(), int(l.params.Latency), int(l.params.Timeout.Milliseconds())
		}
		switch l.phase {
		case linkConnecting:
			ls.State = "connecting"
		case linkClosing:
			ls.State = "closing"
		case linkConnected:
			ls.State = "connected"
			if l.dormant {
				ls.State = "dormant"
			}
			if l.firmware != [3]byte{} {
				ls.Firmware = fmt.Sprintf("%d.%d.%d", l.firmware[0], l.firmware[1], l.firmware[2])
			}
			if l.dev != nil && l.dev.live {
				ls.Device = l.dev.id
			}
		}
		_, ls.Known = h.knownAddr(l.addr)
		st.Links = append(st.Links, ls)
	}
	if d, ok := h.tr.(interface{ Dropped() uint64 }); ok {
		st.Dropped = d.Dropped()
	}
	return st
}

// Close ends the hub at now: every live gamepad is reported removed, so that
// its consumer releases everything, and then the transport is closed. The hub
// sends no event afterwards and ignores the transport's.
func (h *joyCon2Hub) Close(now time.Duration) {
	if h.closed {
		return
	}
	for _, dev := range h.devs {
		h.removeLive(dev, now)
	}
	h.closed = true
	h.devs, h.links = nil, nil
	h.tr.Close()
}
