package input

import (
	"errors"
	"fmt"
	"runtime"
	"strconv"
)

// This file reads the heartbeat of a receiver (IN-30, watchdog.go): the
// HID input reports of its device, through a second handle that T-input
// opens beside SDL's. Windows gives each open handle every input report,
// in a buffer of its own (hidapi's HidD_SetNumInputBuffers, 64 reports),
// and SDL opens the device shared (win_hid.c), so reading them takes
// nothing from SDL.
//
// Over USB SDL puts a Switch Pro protocol device into full reports, ID
// 0x30 (SDL_hidapi_switch.c, GetDefaultInputMode), which a live gamepad
// sends on and on. After the ID, a full report (0x30, or 0x31 with MCU
// data) holds SDL's SwitchControllerStatePacket_t: byte 1 is a timer that
// moves with each report, byte 2 the battery and connection, bytes 3 to 5
// the buttons, 6 to 8 and 9 to 11 the left and right sticks (two 12-bit
// values each), byte 12 the vibration code, and from byte 13 the IMU
// samples (SwitchControllerIMUState_t). A report counts as a heartbeat
// only when its timer moved: a report that repeats is no sign of life,
// and the other reports are replies (0x21 to subcommands, 0x81 to USB
// commands, which a receiver may give by itself) or the simple reports
// (0x3F) that carry no timer.
//
// A heartbeat is idle when its buttons and sticks, bytes 3 to 11, repeat
// those of the full report before it: the gamepad reported while its
// input did not change. Only idle heartbeats prove that a device reports
// while nothing changes (watchdog.beat); a device that only reports when
// its input changes sends none, however small its changes. The battery,
// the vibration code and the IMU samples are left out: they may change
// in every report of a gamepad at rest.
//
// Simple reports in place of the heartbeat mean that the gamepad has been
// put into another input mode, as another program may do; SDL then reads
// them instead (SDL_hidapi_switch.c, HIDAPI_DriverSwitch_UpdateDevice),
// and the watchdog disarms (watchdog.switched).

// hidDrainMax bounds the reports one drain reads, so that a flood cannot
// hold up T-input; the rest wait in the handle's buffer.
const hidDrainMax = 64

// The bytes of a full report: the timer, the battery and connection, and
// the buttons and sticks from fullInput up to fullInputEnd.
const (
	fullTimer    = 1
	fullStatus   = 2
	fullInput    = 3
	fullInputEnd = 12
)

// The report IDs of the Switch Pro protocol that the statistics tell
// apart (SDL_hidapi_switch.c, ESwitchInputReportIDs).
const (
	reportFull       = 0x30
	reportFullMCU    = 0x31
	reportSubcommand = 0x21
	reportSimple     = 0x3f
	reportUSB        = 0x81
)

var (
	// errHIDRead is returned by a drain whose read failed, as it does when
	// the device has gone.
	errHIDRead = errors.New("reading the HID reports failed")
	// errNoHIDBinding is returned by openHIDPath where SDL_hid_* is not
	// bound (Windows on arm64 or 386).
	errNoHIDBinding = errors.New("no binding of SDL_hid_open_path")
)

// hidBeat is the heartbeat source of a watched device, owned by T-input.
type hidBeat struct {
	// read reads one report into its argument without waiting and returns
	// its length, 0 when none waits, or less than 0 on an error.
	read func([]byte) int
	// closeFn closes the source; nil for nothing to close.
	closeFn func()

	buf       [128]byte
	prevTimer byte
	havePrev  bool
	// input holds the buttons and sticks of the last full report, when
	// haveInput is set: that report was long enough to hold them.
	input     [fullInputEnd - fullInput]byte
	haveInput bool

	// The statistics, from the source's opening, for the log: reports
	// read, 0x30 and 0x31 that repeated their timer (still), idle
	// heartbeats (idle), reports of other IDs (other), and the reports by
	// ID class, in the order of idNames.
	reports, still, idle, other uint64
	ids                         [5]uint64
	// status is byte 2 of the last 0x30 or 0x31, and changedAt the time of
	// the drain in which its buttons and sticks last changed; haveContent
	// is set once a report held them.
	status      byte
	haveContent bool
	changedAt   uint64
	// beatGap and idleGap time the drains with a heartbeat, and with an
	// idle one.
	beatGap, idleGap gapStat
}

// gapStat times events, such as the drains with a heartbeat: last is the
// time of the last one, when seen is set, and max the longest time
// between two since the last statistics record, which clears it.
type gapStat struct {
	last, max uint64
	seen      bool
}

// at records an event at now.
func (g *gapStat) at(now uint64) {
	if g.seen && now > g.last {
		g.max = max(g.max, now-g.last)
	}
	g.last, g.seen = now, true
}

// idNames name the ID classes of hidBeat.ids.
var idNames = [...]string{"30", "21", "3f", "81", "other"}

// drained is what a drain read: its heartbeats, and the idle ones among
// them; simple is set when a simple report (0x3F), one of another input
// mode, came after the drain's last heartbeat, or in a drain without one.
type drained struct {
	beats, idle int
	simple      bool
}

// drain reads the reports that wait, at most hidDrainMax, at now, and
// returns what they were. The drain itself allocates nothing, and nor
// does hidSource's read on Windows/amd64 (TestHIDBindings); elsewhere the
// binding may (sdlext_other.go).
func (h *hidBeat) drain(now uint64) (d drained, err error) {
	for range hidDrainMax {
		n := h.read(h.buf[:])
		if n < 0 {
			err = errHIDRead
			break
		}
		if n == 0 {
			break
		}
		n = min(n, len(h.buf))
		h.reports++
		switch id := h.buf[0]; {
		case (id == reportFull || id == reportFullMCU) && n > fullTimer:
			h.ids[0]++
			h.full(n, now, &d)
		default:
			h.other++
			switch id {
			case reportSubcommand:
				h.ids[1]++
			case reportSimple:
				h.ids[2]++
				d.simple = true
			case reportUSB:
				h.ids[3]++
			default:
				h.ids[4]++
			}
		}
	}
	if d.beats > 0 {
		h.beatGap.at(now)
	}
	if d.idle > 0 {
		h.idleGap.at(now)
	}
	return d, err
}

// full counts the full report of n bytes in h.buf, read at now, into d.
func (h *hidBeat) full(n int, now uint64, d *drained) {
	timer := h.buf[fullTimer]
	beat := !h.havePrev || timer != h.prevTimer
	h.prevTimer, h.havePrev = timer, true
	if n > fullStatus {
		h.status = h.buf[fullStatus]
	}
	same := false
	if n >= fullInputEnd {
		in := [fullInputEnd - fullInput]byte(h.buf[fullInput:fullInputEnd])
		same = h.haveInput && in == h.input
		if !same {
			h.changedAt = now
		}
		h.input, h.haveInput, h.haveContent = in, true, true
	} else {
		h.haveInput = false // the next report has nothing to repeat
	}
	switch {
	case !beat:
		h.still++
		return
	case same:
		d.idle++
		h.idle++
	}
	d.beats++
	d.simple = false // a heartbeat after the simple reports
}

// idCounts returns the reports by ID class, such as
// "30:118,21:0,3f:0,81:2,other:0".
func (h *hidBeat) idCounts() string {
	b := make([]byte, 0, 48)
	for i, name := range idNames {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, name...)
		b = append(b, ':')
		b = strconv.AppendUint(b, h.ids[i], 10)
	}
	return string(b)
}

// close closes the source.
func (h *hidBeat) close() {
	if h.closeFn != nil {
		h.closeFn()
	}
}

// openHIDPath opens the HID device at path, the DeviceInfo.Path of a
// device of SDL's HIDAPI driver, as a heartbeat source, with SDL's public
// HID API: SDL_hid_open_path opens it shared, SDL_hid_read_timeout with a
// timeout of 0 reads without waiting, and SDL_hid_close closes it. SDL's
// HIDAPI driver has initialized SDL's HID layer, and it closes every
// handle at SDL_Quit. The open queries the device synchronously, once
// per connection.
func openHIDPath(path string) (*hidBeat, error) {
	if hidOpenPath == nil || hidReadTimeout == nil || hidClose == nil {
		return nil, errNoHIDBinding
	}
	dev := hidOpenPath(path)
	if dev == 0 {
		return nil, fmt.Errorf("SDL_hid_open_path(%s) failed", path)
	}
	return hidSource(dev), nil
}

// hidSource returns the heartbeat source that reads the SDL HID device
// dev without waiting and closes it.
func hidSource(dev uintptr) *hidBeat {
	h := &hidBeat{closeFn: func() { hidClose(dev) }}
	h.read = func(b []byte) int {
		n := hidReadTimeout(dev, b, 0)
		runtime.KeepAlive(b)
		return int(n)
	}
	return h
}
