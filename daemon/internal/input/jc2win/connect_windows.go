package jc2win

// connect_windows.go: one link, from FromBluetoothAddressAsync to the
// Connected event, and its callbacks.
//
// This file holds every write to a controller:
//   - commandWriter: WriteValueWithOptionAsync on the command characteristic,
//     reachable only through a gate (gate.go).
//   - subscribe: the CCCD write, for the characteristics of jc2.NotifyUUIDs.
//
// guard_test.go checks that each of the two WinRT calls appears exactly once
// and nowhere else.

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/genericattributeprofile"
	"github.com/saltosystems/winrt-go/windows/foundation"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

const (
	openTimeout      = 10 * time.Second // FromBluetoothAddressAsync, session
	upTimeout        = 10 * time.Second // MaintainConnection until Windows says connected
	discoverTimeout  = 10 * time.Second // services, characteristics
	subscribeTimeout = 5 * time.Second
	writeTimeout     = 5 * time.Second
	replyTimeout     = 2 * time.Second       // per start-up command
	commandSpacing   = 10 * time.Millisecond // between start-up commands
	discoverTries    = 3
	intervalWait     = 500 * time.Millisecond // for Windows to apply the preferred parameters
)

// Link states. A link is connecting until Connected is sent, live until it
// ends, ended after. Callbacks read the state and may move live to ended;
// the goroutine does the rest.
const (
	stConnecting int32 = iota
	stLive
	stEnded
)

// minReport is the shortest report whose fields package jc2 reads (through the
// second stick byte and the format byte).
const minReport = 9

type link struct {
	id     LinkID
	addr   uint64
	side   jc2.Side
	player int
	log    *slog.Logger

	cancel     chan struct{} // closed by Disconnect and Close
	cancelOnce sync.Once
	down       chan struct{} // closed by the status callback once the link was up and went down
	downFlag   atomic.Bool
	up         chan struct{} // capacity 1: a callback saw Windows report connected
	seenUp     atomic.Bool
	state      atomic.Int32
	lostUnsent atomic.Bool // the callback's Lost did not fit in the channel
	replies    chan []byte // notifications of the command reply characteristic

	// Owned by the transport goroutine.
	finished bool
	released bool
	dev      *bluetooth.BluetoothLEDevice
	sess     *genericattributeprofile.GattSession
	status   *handlerReg
	svc      *genericattributeprofile.GattDeviceService
	chars    map[string]*genericattributeprofile.GattCharacteristic
	props    map[string]genericattributeprofile.GattCharacteristicProperties
	subs     []*handlerReg
	request  *bluetooth.BluetoothLEPreferredConnectionParametersRequest
}

// handlerReg is an event handler and what is needed to remove it again.
type handlerReg struct {
	h      *foundation.TypedEventHandler
	remove func() error
}

func newLink(id LinkID, addr uint64, side jc2.Side, player int, lg *slog.Logger) *link {
	return &link{
		id: id, addr: addr, side: side, player: player,
		log:     lg.With("link", uint32(id), "addr", FormatAddr(addr), "side", side.String()),
		cancel:  make(chan struct{}),
		down:    make(chan struct{}),
		up:      make(chan struct{}, 1),
		replies: make(chan []byte, 16),
		chars:   map[string]*genericattributeprofile.GattCharacteristic{},
		props:   map[string]genericattributeprofile.GattCharacteristicProperties{},
	}
}

func (l *link) cancelNow() { l.cancelOnce.Do(func() { close(l.cancel) }) }

// doConnect runs the connect sequence of l and ends in Connected or Failed.
func (t *Transport) doConnect(l *link) {
	select {
	case <-l.cancel:
		t.finish(l, ErrCanceled)
		return
	default:
	}
	if l.side != jc2.SideL && l.side != jc2.SideR {
		t.finish(l, fmt.Errorf("%w: side %v", ErrBadRequest, l.side))
		return
	}
	t.mu.Lock()
	for _, o := range t.links {
		if o != l && o.addr == l.addr && !o.finished && o.state.Load() != stEnded {
			t.mu.Unlock()
			t.finish(l, ErrBusy)
			return
		}
	}
	t.mu.Unlock()

	start := time.Now()
	info, err := t.open(l)
	if err != nil {
		l.log.Warn("connect failed", "after", time.Since(start).Round(time.Millisecond), "err", err)
		t.finish(l, err)
		return
	}
	l.log.Info("connected", "after", time.Since(start).Round(time.Millisecond),
		"firmware", fmt.Sprintf("%d.%d.%d", info.Firmware[0], info.Firmware[1], info.Firmware[2]),
		"interval", info.Interval)

	// Connected comes before the first Report: callbacks drop reports until
	// the state is live.
	if !t.emitWait(Event{Kind: Connected, Link: l.id, Addr: l.addr, Info: info}) {
		// Nobody would learn of this link, so there is none.
		t.finish(l, errors.New("the consumer does not read events"))
		return
	}
	l.state.CompareAndSwap(stConnecting, stLive)
	// The link may have gone down while Connected was on its way; then no
	// callback saw it live.
	select {
	case <-l.down:
		t.finish(l, nil)
	default:
	}
}

// open does the steps up to and including the preferred connection
// parameters. On error the caller releases whatever was opened.
func (t *Transport) open(l *link) (info LinkInfo, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("jc2win: windows runtime call failed: %v", r)
		}
	}()

	step := func(name string, f func() error) error {
		s := time.Now()
		if err := f(); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		l.log.Debug("step", "name", name, "took", time.Since(s).Round(time.Millisecond))
		return nil
	}
	for _, s := range []struct {
		name string
		f    func() error
	}{
		{"open device", func() error { return t.openDevice(l) }},
		{"keep connection", func() error { return t.keepConnection(l) }},
		{"wait for link", func() error { return t.waitUp(l) }},
		{"discover", func() error { return t.discover(l) }},
		{"subscribe", func() error { return t.subscribeAll(l) }},
	} {
		if err := step(s.name, s.f); err != nil {
			return info, err
		}
	}

	g, err := t.commandGate(l)
	if err != nil {
		return info, err
	}
	s := time.Now()
	h := &handshake{
		gate: g, replies: l.replies, cancel: l.cancel, down: l.down, log: l.log,
		side: l.side, player: l.player, timeout: replyTimeout, spacing: commandSpacing,
	}
	if info, err = h.run(); err != nil {
		return info, fmt.Errorf("start-up commands: %w", err)
	}
	l.log.Debug("step", "name", "start-up commands", "took", time.Since(s).Round(time.Millisecond))

	info.Interval = t.preferThroughput(l)
	return info, nil
}

// openDevice gets the BluetoothLEDevice of the address. For an address that
// Windows does not know it makes the object; it does not pair.
func (t *Transport) openDevice(l *link) error {
	op, err := bluetooth.BluetoothLEDeviceFromBluetoothAddressAsync(l.addr)
	if err != nil {
		return err
	}
	defer op.Release()
	res, err := t.await(op, bluetooth.SignatureBluetoothLEDevice, "from address", openTimeout, l.cancel, nil)
	if err != nil {
		return err
	}
	if res == nil {
		return errors.New("windows has no device for the address")
	}
	l.dev = (*bluetooth.BluetoothLEDevice)(res)
	return nil
}

// keepConnection opens a GATT session and asks it to keep the link up, which
// is what makes Windows connect to the advertising controller.
func (t *Transport) keepConnection(l *link) error {
	id, err := l.dev.GetBluetoothDeviceId()
	if err != nil {
		return err
	}
	defer id.Release()
	op, err := genericattributeprofile.GattSessionFromDeviceIdAsync(id)
	if err != nil {
		return err
	}
	defer op.Release()
	res, err := t.await(op, genericattributeprofile.SignatureGattSession, "gatt session", openTimeout, l.cancel, nil)
	if err != nil {
		return err
	}
	if res == nil {
		return errors.New("no GATT session")
	}
	l.sess = (*genericattributeprofile.GattSession)(res)
	return l.sess.SetMaintainConnection(true)
}

// waitUp listens for the connection status and waits until Windows says the
// link is connected.
func (t *Transport) waitUp(l *link) error {
	guid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		bluetooth.SignatureBluetoothLEDevice, "cinterface(IInspectable)")
	// Runs on a thread of the thread pool: see onStatus.
	h := foundation.NewTypedEventHandler(ole.NewGUID(guid),
		func(_ *foundation.TypedEventHandler, sender, _ unsafe.Pointer) { t.onStatus(l, sender) })
	tok, err := l.dev.AddConnectionStatusChanged(h)
	if err != nil {
		h.Release()
		return err
	}
	dev := l.dev
	l.status = &handlerReg{h: h, remove: func() error { return dev.RemoveConnectionStatusChanged(tok) }}

	deadline := time.NewTimer(upTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(100 * time.Millisecond)
	defer poll.Stop()
	for {
		st, err := l.dev.GetConnectionStatus()
		if err != nil {
			return err
		}
		if st == bluetooth.BluetoothConnectionStatusConnected {
			l.seenUp.Store(true)
			return nil
		}
		select {
		case <-l.up:
		case <-poll.C:
		case <-deadline.C:
			return fmt.Errorf("windows did not report the link up within %s", upTimeout)
		case <-l.cancel:
			return ErrCanceled
		case <-t.stop:
			return ErrCanceled
		case <-t.wake:
			t.reconcileScan()
		}
	}
}

// discover finds the controller service and the characteristics padmate uses,
// trying a few times: right after the link comes up the first uncached
// discovery sometimes fails or finds nothing.
func (t *Transport) discover(l *link) error {
	var err error
	for try := 1; try <= discoverTries; try++ {
		if err = t.discoverOnce(l); err == nil {
			return nil
		}
		if errors.Is(err, ErrCanceled) || errors.Is(err, ErrLinkDropped) || try == discoverTries {
			break
		}
		l.log.Warn("discovery failed, trying again", "try", try, "err", err)
		l.dropDiscovered()
		timer := time.NewTimer(time.Duration(try) * 300 * time.Millisecond)
		select {
		case <-timer.C:
		case <-l.cancel:
			timer.Stop()
			return ErrCanceled
		case <-l.down:
			timer.Stop()
			return ErrLinkDropped
		case <-t.stop:
			timer.Stop()
			return ErrCanceled
		}
	}
	return err
}

func (t *Transport) discoverOnce(l *link) error {
	// Services, uncached.
	op, err := l.dev.GetGattServicesWithCacheModeAsync(bluetooth.BluetoothCacheModeUncached)
	if err != nil {
		return err
	}
	defer op.Release()
	res, err := t.await(op, genericattributeprofile.SignatureGattDeviceServicesResult, "services", discoverTimeout, l.cancel, l.down)
	if err != nil {
		return err
	}
	sr := (*genericattributeprofile.GattDeviceServicesResult)(res)
	defer sr.Release()
	if st, err := sr.GetStatus(); err != nil {
		return err
	} else if st != genericattributeprofile.GattCommunicationStatusSuccess {
		return fmt.Errorf("services: GATT status %d", st)
	}
	svcs, err := sr.GetServices()
	if err != nil {
		return err
	}
	defer svcs.Release()
	n, err := svcs.GetSize()
	if err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		p, err := svcs.GetAt(i)
		if err != nil {
			return err
		}
		s := (*genericattributeprofile.GattDeviceService)(p)
		g, err := s.GetUuid()
		if err == nil && l.svc == nil && uuidString(g) == jc2.UUIDService {
			l.svc = s
			continue
		}
		s.Release()
	}
	if l.svc == nil {
		return fmt.Errorf("the controller service %s is missing (%d services)", jc2.UUIDService, n)
	}

	// Characteristics, uncached. Only the three padmate uses are kept.
	want := map[string]bool{jc2.UUIDCommand: true, jc2.UUIDCommandResp: true, l.side.SideInputUUID(): true}
	cop, err := l.svc.GetCharacteristicsWithCacheModeAsync(bluetooth.BluetoothCacheModeUncached)
	if err != nil {
		return err
	}
	defer cop.Release()
	cres, err := t.await(cop, genericattributeprofile.SignatureGattCharacteristicsResult, "characteristics", discoverTimeout, l.cancel, l.down)
	if err != nil {
		return err
	}
	cr := (*genericattributeprofile.GattCharacteristicsResult)(cres)
	defer cr.Release()
	if st, err := cr.GetStatus(); err != nil {
		return err
	} else if st != genericattributeprofile.GattCommunicationStatusSuccess {
		return fmt.Errorf("characteristics: GATT status %d", st)
	}
	cs, err := cr.GetCharacteristics()
	if err != nil {
		return err
	}
	defer cs.Release()
	cn, err := cs.GetSize()
	if err != nil {
		return err
	}
	for i := uint32(0); i < cn; i++ {
		p, err := cs.GetAt(i)
		if err != nil {
			return err
		}
		c := (*genericattributeprofile.GattCharacteristic)(p)
		g, gerr := c.GetUuid()
		id := uuidString(g)
		if gerr != nil || !want[id] || l.chars[id] != nil {
			c.Release()
			continue
		}
		pr, err := c.GetCharacteristicProperties()
		if err != nil {
			c.Release()
			return err
		}
		l.chars[id] = c
		l.props[id] = pr
		l.log.Debug("characteristic", "uuid", id, "properties", fmt.Sprintf("%#x", uint32(pr)))
	}
	for id := range want {
		if l.chars[id] == nil {
			return fmt.Errorf("characteristic %s is missing", id)
		}
	}
	return nil
}

// dropDiscovered releases what a failed discovery found.
func (l *link) dropDiscovered() {
	for id, c := range l.chars {
		c.Release()
		delete(l.chars, id)
		delete(l.props, id)
	}
	if l.svc != nil {
		_ = try(l.svc.Close)
		l.svc.Release()
		l.svc = nil
	}
}

// subscribeAll switches the notifications on, command replies first so that
// the start-up conversation can be heard.
func (t *Transport) subscribeAll(l *link) error {
	for _, uuid := range jc2.NotifyUUIDs(l.side) {
		on := t.onReport
		if uuid == jc2.UUIDCommandResp {
			on = t.onReply
		}
		if err := t.subscribe(l, uuid, func(args unsafe.Pointer) { on(l, args) }); err != nil {
			return fmt.Errorf("%s: %w", uuid, err)
		}
	}
	return nil
}

// subscribe listens to the characteristic uuid and writes its CCCD to turn
// notifications on. It is the only function that writes a CCCD, and it
// refuses every uuid that jc2.MayNotify does not allow for the side of l.
func (t *Transport) subscribe(l *link, uuid string, on func(args unsafe.Pointer)) error {
	if err := checkNotify(l.side, uuid); err != nil {
		return err
	}
	c := l.chars[uuid]
	if c == nil {
		return fmt.Errorf("characteristic %s was not discovered", uuid)
	}
	if l.props[uuid]&genericattributeprofile.GattCharacteristicPropertiesNotify == 0 {
		return errors.New("the characteristic cannot notify")
	}
	guid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		genericattributeprofile.SignatureGattCharacteristic,
		genericattributeprofile.SignatureGattValueChangedEventArgs)
	// Runs on a thread of the thread pool: see onReport and onReply.
	h := foundation.NewTypedEventHandler(ole.NewGUID(guid),
		func(_ *foundation.TypedEventHandler, _, args unsafe.Pointer) { on(args) })
	tok, err := c.AddValueChanged(h)
	if err != nil {
		h.Release()
		return err
	}
	l.subs = append(l.subs, &handlerReg{h: h, remove: func() error { return c.RemoveValueChanged(tok) }})

	op, err := c.WriteClientCharacteristicConfigurationDescriptorAsync(genericattributeprofile.GattClientCharacteristicConfigurationDescriptorValueNotify)
	if err != nil {
		return err
	}
	defer op.Release()
	res, err := t.await(op, genericattributeprofile.SignatureGattCommunicationStatus, "enable notifications", subscribeTimeout, l.cancel, l.down)
	if err != nil {
		return err
	}
	if st := genericattributeprofile.GattCommunicationStatus(uintptr(res)); st != genericattributeprofile.GattCommunicationStatusSuccess {
		return fmt.Errorf("GATT status %d", st)
	}
	return nil
}

// commandGate returns the gate to the command characteristic of l.
func (t *Transport) commandGate(l *link) (*gate, error) {
	c := l.chars[jc2.UUIDCommand]
	if c == nil {
		return nil, errors.New("the command characteristic was not discovered")
	}
	if l.props[jc2.UUIDCommand]&genericattributeprofile.GattCharacteristicPropertiesWriteWithoutResponse == 0 {
		return nil, errors.New("the command characteristic cannot be written without response")
	}
	// commandWriter is the only code that writes to the command
	// characteristic; it is handed to the gate and to nothing else.
	return &gate{write: t.commandWriter(l, c), log: l.log}, nil
}

// commandWriter returns the function that writes a command to c without
// response. The gate calls it after jc2.CheckRequest; it checks once more,
// on the bytes it is about to send.
func (t *Transport) commandWriter(l *link, c *genericattributeprofile.GattCharacteristic) func(b []byte) error {
	return func(b []byte) error {
		if err := jc2.CheckRequest(b); err != nil {
			return err
		}
		buf, err := newBuffer(b)
		if err != nil {
			return err
		}
		defer buf.Release()
		op, err := c.WriteValueWithOptionAsync(buf, genericattributeprofile.GattWriteOptionWriteWithoutResponse)
		if err != nil {
			return err
		}
		defer op.Release()
		res, err := t.await(op, genericattributeprofile.SignatureGattCommunicationStatus, "write command", writeTimeout, l.cancel, l.down)
		if err != nil {
			return err
		}
		if st := genericattributeprofile.GattCommunicationStatus(uintptr(res)); st != genericattributeprofile.GattCommunicationStatusSuccess {
			return fmt.Errorf("write command: GATT status %d", st)
		}
		return nil
	}
}

// preferThroughput asks Windows for the throughput-optimized connection
// parameters, which keeps the connection interval short, and describes what
// it learned for the log. This is Windows 11 only: where the classes are
// missing (Windows 10) it says so and goes on. The request must stay open
// for Windows to keep applying it; release closes it.
func (t *Transport) preferThroughput(l *link) string {
	var desc string
	err := try(func() error {
		read := func() (text string, interval uint16) {
			text = "unknown"
			defer func() { // the getter does not exist before Windows 11
				if recover() != nil {
					text, interval = "unknown", 0
				}
			}()
			p, err := l.dev.GetConnectionParameters()
			if err != nil {
				return text, 0
			}
			defer p.Release()
			iv, ierr := p.GetConnectionInterval() // units of 1.25 ms
			lat, _ := p.GetConnectionLatency()
			to, _ := p.GetLinkTimeout() // units of 10 ms
			if ierr != nil {
				return text, 0
			}
			return fmt.Sprintf("%.2f ms, latency %d, timeout %d ms", float64(iv)*1.25, lat, int(to)*10), iv
		}
		before, ivBefore := read()
		pref, err := bluetooth.BluetoothLEPreferredConnectionParametersGetThroughputOptimized()
		if err != nil {
			if isClassNotRegistered(err) {
				desc = "not requested (needs Windows 11); interval " + before
				return nil
			}
			return err
		}
		defer pref.Release()
		req, err := l.dev.RequestPreferredConnectionParameters(pref)
		if err != nil {
			return err
		}
		st, err := req.GetStatus()
		if err != nil || st != bluetooth.BluetoothLEPreferredConnectionParametersRequestStatusSuccess {
			_ = req.Close()
			req.Release()
			if err == nil {
				err = fmt.Errorf("status %d", st)
			}
			return err
		}
		l.request = req

		// The change comes some time after the request; look for it.
		after, ivAfter := before, ivBefore
		deadline := time.NewTimer(intervalWait)
		defer deadline.Stop()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
	wait:
		for ivAfter == ivBefore {
			select {
			case <-tick.C:
				after, ivAfter = read()
			case <-deadline.C:
				break wait
			case <-l.cancel:
				break wait
			case <-l.down:
				break wait
			case <-t.stop:
				break wait
			}
		}
		desc = "throughput-optimized requested; interval " + before + " -> " + after
		return nil
	})
	if err != nil {
		l.log.Warn("could not request the preferred connection parameters", "err", err)
		return "request failed: " + err.Error()
	}
	return desc
}

// release closes everything l holds, in the order that lets Windows drop
// the link: handlers off first, then the request, the GATT objects, the
// session and the device. It is safe to call twice.
func (l *link) release() {
	if l.released {
		return
	}
	l.released = true
	for _, s := range l.subs {
		_ = try(s.remove)
		s.h.Release()
	}
	l.subs = nil
	if l.status != nil {
		_ = try(l.status.remove)
		l.status.h.Release()
		l.status = nil
	}
	if l.request != nil {
		_ = try(l.request.Close)
		l.request.Release()
		l.request = nil
	}
	l.dropDiscovered()
	if l.sess != nil {
		_ = try(func() error { return l.sess.SetMaintainConnection(false) })
		_ = try(l.sess.Close)
		l.sess.Release()
		l.sess = nil
	}
	if l.dev != nil {
		_ = try(l.dev.Close)
		l.dev.Release()
		l.dev = nil
	}
}

// The callbacks. They run on threads of the Windows thread pool, called by
// the delegates of winrt-go (package doc). Each one recovers from a panic,
// copies what it needs, sends without blocking and returns.

// onStatus handles ConnectionStatusChanged. The sender is the device itself,
// valid for the call, so the callback does not touch l.dev, which the
// goroutine may be releasing.
func (t *Transport) onStatus(l *link, sender unsafe.Pointer) {
	defer recoverCallback()
	st, err := (*bluetooth.BluetoothLEDevice)(sender).GetConnectionStatus()
	if err != nil {
		return
	}
	if st == bluetooth.BluetoothConnectionStatusConnected {
		l.seenUp.Store(true)
		select {
		case l.up <- struct{}{}:
		default:
		}
		return
	}
	t.linkDown(l)
}

// onReport handles ValueChanged of the side input characteristic.
func (t *Transport) onReport(l *link, args unsafe.Pointer) {
	defer recoverCallback()
	stamp := latency.Now()
	if l.state.Load() != stLive {
		return
	}
	data, ok := valueBytes(args)
	if !ok || len(data) < minReport {
		return
	}
	ev := Event{Kind: Report, Link: l.id, Stamp: stamp}
	copy(ev.Report[:], data)
	t.emit(ev)
}

// onReply handles ValueChanged of the command reply characteristic.
func (t *Transport) onReply(l *link, args unsafe.Pointer) {
	defer recoverCallback()
	data, ok := valueBytes(args)
	if !ok {
		return
	}
	select {
	case l.replies <- data:
	default:
	}
}

// valueBytes copies the value a ValueChanged event carries.
func valueBytes(args unsafe.Pointer) ([]byte, bool) {
	buf, err := (*genericattributeprofile.GattValueChangedEventArgs)(args).GetCharacteristicValue()
	if err != nil || buf == nil {
		return nil, false
	}
	defer buf.Release()
	return bufferBytes(buf)
}
