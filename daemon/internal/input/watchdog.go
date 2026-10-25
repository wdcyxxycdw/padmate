package input

import (
	"fmt"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"
)

// This file is the pure logic of the receiver watchdog (IN-30, ADR-0029).
// A 2.4G receiver of the Switch Pro protocol, such as GameSir's Nova 2
// Lite in Switch mode, stays listed by SDL when its gamepad is switched
// off, and SDL reports no removal, so the keys held then stay held. SDL
// only sends events when the state changes, so no SDL event tells that
// the gamepad still reports; T-input reads its HID reports itself
// (hidbeat.go) and counts each one whose timer moved as a heartbeat, and
// one whose buttons and sticks repeat the report before as idle.
//
// A watchdog arms once its gamepad has sent idle heartbeats for prove, at
// most gap apart, with its controls at rest: only then is it known that
// the gamepad reports while nothing changes, and so that silence means it
// has gone. A gamepad that only reports when its input changes sends no
// idle heartbeat, and never arms, however small its changes. An armed
// watchdog is lost once neither a heartbeat nor a control event has come
// for timeout, held controls or not: a latch or a script may hold keys
// with no control held. It disarms when simple reports (0x3F) come in
// place of the heartbeat for gap (switched): the gamepad has been put into
// another input mode, which SDL reads as well, and which reports only on
// changes. It then proves again once the heartbeat is back, as it does
// when the gamepad comes back after a loss (start). When T-input itself
// has stopped, a silent armed device gets a new timeout (grace), once a
// silence.
//
// Times are SDL ticks in nanoseconds (SDL_GetTicksNS, Event.Time); the
// file reads no clock.

// DefaultReceiverTimeout is the silence after which padmated reports an
// armed receiver removed (Options.ReceiverTimeout).
const DefaultReceiverTimeout = 2 * time.Second

const (
	// minReceiverTimeout is the shortest ReceiverTimeout: its gap, an
	// eighth, must stay above T-input's watch (receiverWatchEvery).
	minReceiverTimeout = 100 * time.Millisecond
	// receiverWatchEvery is how often T-input drains the heartbeat sources
	// at most.
	receiverWatchEvery = 5 * time.Millisecond
	// receiverBand is how far an axis may move from where it rested at the
	// start of a run of idle heartbeats for the gamepad to still be at
	// rest: SDL's Switch calibration has no dead zone, so a stick at rest
	// jitters.
	receiverBand = 2048
	// receiverStatsEvery is how often T-input logs a watched device's
	// heartbeat statistics, at debug level.
	receiverStatsEvery = time.Second
)

// receiverTiming holds the watchdog's parameters in nanoseconds, all
// derived from Options.ReceiverTimeout (newReceiverTiming). The zero
// value turns the watchdog off.
type receiverTiming struct {
	// timeout is the silence after which an armed device is lost.
	timeout uint64
	// prove is how long idle heartbeats must come at rest to arm a device.
	prove uint64
	// gap is the longest time between two idle heartbeats of one run, a
	// quarter of prove, so that they come in every quarter of it; and how
	// long simple reports must come without a heartbeat to disarm.
	gap uint64
	// stall is the longest time between two watches after which T-input
	// grants a silent armed device a new timeout: T-input, and the reports
	// with it, may have stopped, as over a sleep.
	stall uint64
}

// newReceiverTiming derives the watchdog's parameters from its timeout d:
// prove d/2, gap d/8 and stall d/4. 0 turns it off; d must otherwise be at
// least minReceiverTimeout.
func newReceiverTiming(d time.Duration) (receiverTiming, error) {
	if d == 0 {
		return receiverTiming{}, nil
	}
	if d < minReceiverTimeout {
		return receiverTiming{}, fmt.Errorf("input: ReceiverTimeout %v: it must be 0 (off) or at least %v", d, minReceiverTimeout)
	}
	n := uint64(d)
	return receiverTiming{timeout: n, prove: n / 2, gap: n / 8, stall: n / 4}, nil
}

// on reports whether the watchdog is on.
func (r receiverTiming) on() bool { return r.timeout > 0 }

// The USB ids of the Switch Pro protocol (SDL's usb_ids.h).
const (
	vendorNintendo   = 0x057e
	productSwitchPro = 0x2009
)

// watchable reports whether T-input watches the device info, whose SDL
// connection state is state (IN-30): a device of the Switch Pro protocol
// that SDL's HIDAPI driver reads over USB, with a HID path to open; or a
// virtual one of that protocol with a heartbeat (VirtualDesc.Heartbeat).
// Over Bluetooth SDL itself reopens a device silent for 3 s.
func watchable(info *DeviceInfo, state sdl.JoystickConnectionState, heartbeat bool) bool {
	if info.Vendor != vendorNintendo || info.Product != productSwitchPro {
		return false
	}
	if info.Virtual {
		return heartbeat
	}
	return info.Driver == "hidapi" && state == sdl.JOYSTICK_CONNECTION_WIRED && info.Path != ""
}

// watchdog is the watchdog of one device's life: from its connection, or
// from its return after a loss, to its next loss or removal.
type watchdog struct {
	tm receiverTiming
	// armed is set once idle heartbeats have come at rest for prove.
	armed bool
	// running is set while a run of idle heartbeats at rest goes on, since
	// runStart, the last at lastIdle; rest holds the axes at its start.
	running            bool
	runStart, lastIdle uint64
	rest               [sdl.GAMEPAD_AXIS_COUNT]int16
	// lastBeat is the time of the last heartbeat; lastControl of the last
	// control event, of the start, or of a grace.
	lastBeat, lastControl uint64
	// other is set when a simple report has come since the last
	// heartbeat.
	other bool
	// graced is set by a grace and cleared by a heartbeat or a control
	// event: a silence gets one grace at most.
	graced bool
}

// start starts a new life at t: unarmed, as if a control moved at t.
func (w *watchdog) start(t uint64) { *w = watchdog{tm: w.tm, lastControl: t} }

// beat records heartbeats at t, idle ones among them when idle is set,
// with the axes at axes. It reports whether they armed the watchdog. Any
// heartbeat is a sign of life; only idle ones prove, and a run of them
// breaks at a gap longer than gap.
func (w *watchdog) beat(t uint64, idle bool, axes [sdl.GAMEPAD_AXIS_COUNT]int16) bool {
	w.lastBeat = max(w.lastBeat, t)
	w.graced, w.other = false, false
	if w.armed || !idle {
		return false
	}
	if !w.running || t < w.lastIdle || t-w.lastIdle > w.tm.gap {
		w.running, w.runStart, w.rest = true, t, axes
	}
	w.lastIdle = max(w.lastIdle, t)
	if t-w.runStart >= w.tm.prove {
		w.armed = true
		return true
	}
	return false
}

// control records a control event at t: a button's when button is set,
// else an axis's, with the axes at axes. Before the watchdog arms, a
// button or an axis beyond receiverBand from the rest ends the run of
// idle heartbeats: the gamepad is in use, not at rest.
func (w *watchdog) control(t uint64, button bool, axes [sdl.GAMEPAD_AXIS_COUNT]int16) {
	w.lastControl = max(w.lastControl, t)
	w.graced = false
	if w.armed || !w.running {
		return
	}
	if button || moved(axes, w.rest) {
		w.running = false
	}
}

// otherMode records a simple report (0x3F) after the last heartbeat: a
// report of another input mode, which proves nothing and is no heartbeat.
// The receivers' replies (0x21, 0x81) are not recorded: a receiver may
// send them by itself once its gamepad has gone, in reply to SDL's
// ForceUSB.
func (w *watchdog) otherMode() { w.other = true }

// switched reports whether the armed watchdog's gamepad has switched to
// another input mode by t: a simple report has come after the last
// heartbeat, and no heartbeat for gap. It then disarms: silence no longer
// means that the gamepad has gone, since it reports only on changes, and
// it must prove its heartbeat again.
func (w *watchdog) switched(t uint64) bool {
	if !w.armed || !w.other || t < w.lastBeat || t-w.lastBeat < w.tm.gap {
		return false
	}
	w.armed, w.running = false, false
	return true
}

// moved reports whether an axis of a is more than receiverBand from b's.
func moved(a, b [sdl.GAMEPAD_AXIS_COUNT]int16) bool {
	for i := range a {
		if d := int32(a[i]) - int32(b[i]); d > receiverBand || d < -receiverBand {
			return true
		}
	}
	return false
}

// grace grants the device a new timeout from t, when T-input has stopped
// and the reports may have stopped with it, as over a sleep, and reports
// whether it did. A silence gets one grace at most: none is granted while
// neither a heartbeat nor a control event has come since the last one.
// So stalls that come again and again, as while SDL blocks T-input on
// writes to a silent USB Switch device (ForceUSB, SDL_hidapi_switch.c),
// delay a loss by one timeout at most, and do not keep the device.
func (w *watchdog) grace(t uint64) bool {
	if w.graced {
		return false
	}
	w.graced = true
	w.lastControl = max(w.lastControl, t)
	return true
}

// last returns the later of the last heartbeat and the last control
// event or grace.
func (w *watchdog) last() uint64 { return max(w.lastBeat, w.lastControl) }

// lost reports whether the watchdog is armed and nothing has come for
// timeout at t.
func (w *watchdog) lost(t uint64) bool {
	last := w.last()
	return w.armed && t > last && t-last >= w.tm.timeout
}
