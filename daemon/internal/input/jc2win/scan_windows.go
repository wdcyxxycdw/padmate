package jc2win

// scan_windows.go: the advertisement watcher. Scanning only listens; nothing
// here sends to a controller.

import (
	"fmt"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth"
	"github.com/saltosystems/winrt-go/windows/devices/bluetooth/advertisement"
	"github.com/saltosystems/winrt-go/windows/foundation"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
)

// scanRetry is how long after a failed or aborted scan the transport tries
// to start it again.
const scanRetry = 3 * time.Second

type scanner struct {
	watcher  *advertisement.BluetoothLEAdvertisementWatcher
	received *foundation.TypedEventHandler
	stopped  *foundation.TypedEventHandler
	recvTok  foundation.EventRegistrationToken
	stopTok  foundation.EventRegistrationToken
	hasRecv  bool
	hasStop  bool
}

// reconcileScan brings the watcher in line with what Scan asked for. It runs
// on the goroutine only.
func (t *Transport) reconcileScan() {
	want := t.scanWant.Load()
	if code := t.scanAbort.Swap(0); code != 0 && t.scan != nil {
		t.log.Warn("Windows stopped the advertisement scan", "error", code)
		t.stopScan()
		t.scanRetryAt = time.Now().Add(scanRetry)
	}
	switch {
	case want && t.scan == nil:
		if !t.scanRetryAt.IsZero() && time.Now().Before(t.scanRetryAt) {
			return
		}
		if err := t.startScan(); err != nil {
			if !t.scanFailed { // say it once; the retries are quiet
				t.log.Warn("cannot start the advertisement scan; will keep trying", "err", err)
			}
			t.scanFailed = true
			t.scanRetryAt = time.Now().Add(scanRetry)
			return
		}
		t.scanFailed = false
		t.scanRetryAt = time.Time{}
		t.log.Info("scanning", "active", t.opts.ActiveScan)
	case !want && t.scan != nil:
		t.stopScan()
		t.scanRetryAt = time.Time{}
		t.log.Info("scan stopped")
	case !want:
		t.scanRetryAt = time.Time{}
	}
}

func (t *Transport) startScan() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("jc2win: windows runtime call failed: %v", r)
		}
	}()
	w, err := advertisement.NewBluetoothLEAdvertisementWatcher()
	if err != nil {
		return err
	}
	s := &scanner{watcher: w}
	fail := func(err error) error {
		t.dropScanner(s)
		return err
	}
	mode := advertisement.BluetoothLEScanningModePassive
	if t.opts.ActiveScan {
		mode = advertisement.BluetoothLEScanningModeActive
	}
	if err := w.SetScanningMode(mode); err != nil {
		return fail(err)
	}

	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementReceivedEventArgs>.
	// Runs on a thread of the thread pool: see onAdvert.
	rguid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementReceivedEventArgs)
	s.received = foundation.NewTypedEventHandler(ole.NewGUID(rguid),
		func(_ *foundation.TypedEventHandler, _, args unsafe.Pointer) { t.onAdvert(args) })
	if s.recvTok, err = w.AddReceived(s.received); err != nil {
		return fail(err)
	}
	s.hasRecv = true

	// TypedEventHandler<BluetoothLEAdvertisementWatcher, BluetoothLEAdvertisementWatcherStoppedEventArgs>:
	// Windows stops a watcher by itself, for example when the radio is
	// switched off.
	sguid := winrt.ParameterizedInstanceGUID(foundation.GUIDTypedEventHandler,
		advertisement.SignatureBluetoothLEAdvertisementWatcher,
		advertisement.SignatureBluetoothLEAdvertisementWatcherStoppedEventArgs)
	s.stopped = foundation.NewTypedEventHandler(ole.NewGUID(sguid),
		func(_ *foundation.TypedEventHandler, _, args unsafe.Pointer) { t.onScanStopped(args) })
	if s.stopTok, err = w.AddStopped(s.stopped); err != nil {
		return fail(err)
	}
	s.hasStop = true

	if err := w.Start(); err != nil {
		return fail(err)
	}
	t.scan = s
	return nil
}

func (t *Transport) stopScan() {
	if t.scan == nil {
		return
	}
	s := t.scan
	t.scan = nil
	_ = try(s.watcher.Stop)
	t.dropScanner(s)
}

// dropScanner removes the handlers and releases the watcher.
func (t *Transport) dropScanner(s *scanner) {
	if s.hasRecv {
		_ = try(func() error { return s.watcher.RemoveReceived(s.recvTok) })
	}
	if s.hasStop {
		_ = try(func() error { return s.watcher.RemoveStopped(s.stopTok) })
	}
	if s.received != nil {
		s.received.Release()
	}
	if s.stopped != nil {
		s.stopped.Release()
	}
	s.watcher.Release()
}

// onAdvert handles one received advertisement, on a thread of the thread
// pool. It looks at the manufacturer data elements and passes on those of
// Nintendo's company id, with their bytes copied; it reads nothing else.
func (t *Transport) onAdvert(args unsafe.Pointer) {
	defer recoverCallback()
	a := (*advertisement.BluetoothLEAdvertisementReceivedEventArgs)(args)
	adv, err := a.GetAdvertisement()
	if err != nil || adv == nil {
		return
	}
	defer adv.Release()
	vec, err := adv.GetManufacturerData()
	if err != nil || vec == nil {
		return
	}
	defer vec.Release()
	n, err := vec.GetSize()
	if err != nil {
		return
	}
	for i := uint32(0); i < n; i++ {
		p, err := vec.GetAt(i)
		if err != nil || p == nil {
			continue
		}
		md := (*advertisement.BluetoothLEManufacturerData)(p)
		if id, err := md.GetCompanyId(); err == nil && id == jc2.CompanyID {
			t.advertFrom(a, md, id)
		}
		md.Release()
	}
}

// advertFrom emits the Advert event of one manufacturer data element.
func (t *Transport) advertFrom(a *advertisement.BluetoothLEAdvertisementReceivedEventArgs, md *advertisement.BluetoothLEManufacturerData, company uint16) {
	buf, err := md.GetData()
	if err != nil || buf == nil {
		return
	}
	data, ok := bufferBytes(buf)
	buf.Release()
	if !ok {
		return
	}
	addr, err := a.GetBluetoothAddress()
	if err != nil {
		return
	}
	rssi, _ := a.GetRawSignalStrengthInDBm()
	t.emit(Event{Kind: Advert, Addr: addr, RSSI: rssi, Company: company, Data: data})
}

// onScanStopped handles the watcher's Stopped event: a stop that is not a
// success was Windows' doing; the goroutine starts the scan again later.
func (t *Transport) onScanStopped(args unsafe.Pointer) {
	defer recoverCallback()
	code, err := (*advertisement.BluetoothLEAdvertisementWatcherStoppedEventArgs)(args).GetError()
	if err != nil || code == bluetooth.BluetoothErrorSuccess {
		return
	}
	t.scanAbort.Store(int32(code))
	t.signal()
}
