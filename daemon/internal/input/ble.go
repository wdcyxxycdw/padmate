package input

import (
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// This file connects the Joy-Con 2 hub (jc2hub.go) to Source. The hub is
// owned by T-input like everything else: it is made in init, driven from the
// loop (pumpJoyCon2), and every public method here goes through s.do.
//
// The loop drains the transport's channel in every turn, without blocking,
// and runs the hub's timers in the same turn, like watchReceivers. A turn
// takes at most waitMS (2 ms) when SDL has no event, so a report waits at most
// that long on top of the 15 ms (Windows 11) or 60 ms the controller sends
// them at, and the 300 ms silence watchdog is checked 2 ms late at worst:
// that is fast enough, and needs no wake-up channel between the transport
// and T-input.

var (
	// ErrJoyCon2Unavailable is returned by the Joy-Con 2 methods of a Source
	// that has no Bluetooth LE support: it was opened without
	// Options.JoyCon2, or the transport did not start, or this is not
	// Windows. It is wrapped, so use errors.Is.
	ErrJoyCon2Unavailable = errors.New("input: Joy-Con 2 over Bluetooth LE is not available")
	// ErrJoyCon2Unknown is returned for a serial number that no remembered
	// Joy-Con 2 has.
	ErrJoyCon2Unknown = errors.New("input: no such remembered Joy-Con 2")
	// ErrUnsupported is returned for a request that a device cannot serve: the
	// raw stream and the SDL mapping of a Joy-Con 2, which SDL does not read.
	ErrUnsupported = errors.New("input: not supported for this device")
)

// JoyCon2Options turns on the Joy-Con 2 backend (Options.JoyCon2).
type JoyCon2Options struct {
	// Known are the remembered controllers (the settings of padmated).
	Known []KnownJoyCon2
	// AutoConnect connects to the remembered controllers whenever one
	// advertises with its SYNC button held; with it off, or with nothing
	// remembered, nothing is scanned outside the pairing window.
	AutoConnect bool
	// OnRemember is called on T-input when the pairing window has connected
	// a controller: the entry to keep. It must return at once and must not
	// call the Source.
	OnRemember func(KnownJoyCon2)
	// Transport starts the Bluetooth LE transport; nil means the platform's
	// (package jc2win on Windows, an error elsewhere). Tests give a fake. It
	// runs on T-input. Source closes the transport it returns.
	Transport func(log *slog.Logger) (JoyCon2Transport, error)
	// Silence and GiveUp replace jc2.DefaultSilence and jc2.DefaultLinkGiveUp
	// when positive, for tests.
	Silence, GiveUp time.Duration
}

// startJoyCon2 starts the transport and makes the hub, when Options.JoyCon2
// asks for it. A transport that does not start is not an error of Open: the
// gamepads of SDL go on, and the Joy-Con 2 methods return it.
func (s *Source) startJoyCon2() {
	o := s.joyOpts
	if o == nil {
		return
	}
	start := o.Transport
	if start == nil {
		start = startJoyCon2Transport
	}
	tr, err := start(s.log)
	if err != nil {
		s.joyErr = err
		s.log.Warn("joy-con 2 over bluetooth le unavailable", "err", err)
		return
	}
	s.joy = newJoyCon2Hub(joyCon2Config{
		Transport:   tr,
		Emit:        s.emit,
		Known:       o.Known,
		AutoConnect: o.AutoConnect,
		OnRemember:  o.OnRemember,
		Vertical:    s.VerticalJoyCons(),
		Power:       s.power,
		Silence:     o.Silence,
		GiveUp:      o.GiveUp,
		Log:         s.log,
	})
}

// joyNow is the time the hub is given: SDL's clock, as Event.Time has it.
func (s *Source) joyNow() time.Duration { return time.Duration(s.ticks()) }

// pumpJoyCon2 is the hub's share of a turn of the loop: the transport's
// events, then the timers. The order matters: a link whose reports waited
// in the channel is seen alive before its silence is looked at.
func (s *Source) pumpJoyCon2() {
	if s.joy == nil {
		return
	}
	now := s.joyNow()
	s.joy.Drain(now)
	s.joy.Tick(now)
}

// closeJoyCon2 reports the Joy-Con 2 gamepads removed and closes the
// transport, at shutdown and before SDL quits. A panic of the transport is
// contained so that SDL is still released.
func (s *Source) closeJoyCon2() {
	if s.joy == nil {
		return
	}
	defer func() { _ = recover() }()
	s.joy.Close(s.joyNow())
}

// isJoyCon2 reports whether id is a Joy-Con 2 gamepad that is reported
// added now. SDL knows nothing of it: no call may be made for it.
func (s *Source) isJoyCon2(id DeviceID) bool { return s.joy != nil && s.joy.Has(id) }

// reenumerateJoyCon2 is the Joy-Con 2 part of a resume scan: the transport's
// events and the timers first, so that a link that died during the sleep is
// gone, then a DeviceState of every gamepad that is live, from the state last
// reported, and the combo watch made to know what is held.
func (s *Source) reenumerateJoyCon2() {
	if s.joy == nil {
		return
	}
	now := s.joyNow()
	s.joy.Drain(now)
	s.joy.Tick(now)
	for _, sn := range s.joy.Snapshots() {
		state := sn.gamepadState()
		if s.watch != nil {
			// As for an SDL gamepad: a combo that is held starts its hold afresh.
			s.watch.Observe(Event{Time: uint64(now), Device: sn.Device, Kind: DeviceAdded, Info: sn.Info})
			for _, c := range state.Buttons {
				s.watch.Observe(Event{Time: uint64(now), Device: sn.Device, Kind: ButtonDown, Control: c})
			}
		}
		state.Dropped = s.dropped.Load()
		s.send(Event{Time: uint64(now), Device: sn.Device, Kind: DeviceState, Snapshot: state})
	}
}

// joyOff returns the error of the Joy-Con 2 methods of a Source without the
// backend.
func (s *Source) joyOff() error {
	if s.joyErr != nil {
		return fmt.Errorf("%w: %w", ErrJoyCon2Unavailable, s.joyErr)
	}
	return fmt.Errorf("%w: the Source was opened without it", ErrJoyCon2Unavailable)
}

// JoyCon2Available returns nil when the Source has the Joy-Con 2 backend, and
// otherwise why not (it wraps ErrJoyCon2Unavailable). It is settled when Open
// returns.
func (s *Source) JoyCon2Available() error {
	if s.joy != nil {
		return nil
	}
	return s.joyOff()
}

// doJoyCon2 runs f on T-input with the hub and the time, or returns why the
// backend is off.
func (s *Source) doJoyCon2(f func(h *joyCon2Hub, now time.Duration) error) error {
	return s.do(func() error {
		if s.joy == nil {
			return s.joyOff()
		}
		return f(s.joy, s.joyNow())
	})
}

// JoyCon2Pair opens the pairing window for window (DefaultJoyCon2Window when
// 0; at most MaxJoyCon2Window): the first Joy-Con 2 that advertises while its
// SYNC button is held is connected and remembered (OnRemember). Opening it
// again restarts the time.
func (s *Source) JoyCon2Pair(window time.Duration) error {
	if window < 0 || window > MaxJoyCon2Window {
		return fmt.Errorf("input: pairing window %v is not in 0..%v", window, MaxJoyCon2Window)
	}
	return s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		h.OpenWindow(now, window)
		return nil
	})
}

// JoyCon2CancelPair closes the pairing window, and ends the connection that
// it has started if that is not up yet.
func (s *Source) JoyCon2CancelPair() error {
	return s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		h.CancelWindow(now)
		return nil
	})
}

// JoyCon2SetKnown replaces the remembered controllers; one that is dropped is
// disconnected, as JoyCon2Forget does.
func (s *Source) JoyCon2SetKnown(known []KnownJoyCon2) error {
	return s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		h.SetKnown(known, now)
		return nil
	})
}

// JoyCon2SetAutoConnect turns the connection to remembered controllers on or
// off; the scan follows (nothing is scanned with it off, outside the pairing
// window). Controllers that are connected stay so.
func (s *Source) JoyCon2SetAutoConnect(on bool) error {
	return s.doJoyCon2(func(h *joyCon2Hub, _ time.Duration) error {
		h.SetAutoConnect(on)
		return nil
	})
}

// JoyCon2Forget forgets the remembered Joy-Con 2 with the serial number, and
// disconnects it if it is connected. The caller drops the entry from its
// settings. It wraps ErrJoyCon2Unknown when there is no such controller.
func (s *Source) JoyCon2Forget(serial string) error {
	return s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		if !h.ForgetSerial(serial, now) {
			return fmt.Errorf("%w: serial %q", ErrJoyCon2Unknown, serial)
		}
		return nil
	})
}

// JoyCon2Disconnect closes the connection of the Joy-Con 2 gamepad id (both
// Joy-Cons of a pair). The controller stays remembered; its address is left
// alone for a few seconds. It wraps ErrNoDevice for another id.
func (s *Source) JoyCon2Disconnect(id DeviceID) error {
	return s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		if !h.Disconnect(id, now) {
			return fmt.Errorf("%w: %d", ErrNoDevice, id)
		}
		return nil
	})
}

// JoyCon2Status returns the state of the Joy-Con 2 backend: the pairing
// window, the scan, the remembered controllers and the connections.
func (s *Source) JoyCon2Status() (JoyCon2Status, error) {
	var st JoyCon2Status
	err := s.doJoyCon2(func(h *joyCon2Hub, now time.Duration) error {
		st = h.Status(now)
		if st.WindowOpen {
			st.WindowDeadline = time.Now().Add(st.WindowLeft)
		}
		return nil
	})
	return st, err
}
