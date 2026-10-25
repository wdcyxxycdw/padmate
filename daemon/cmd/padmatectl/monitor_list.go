package main

import (
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// "padmatectl monitor --list" prints, besides the device lines, a block per
// gamepad with what SDL says about it that DeviceInfo does not carry:
// capabilities, power, connection state and player index (IN-12). Directly
// they come from SDL's read-only getters (input.Source.Readout), which send
// the pad nothing; through the daemon only the capabilities and the
// battery of devices.list are known. The owner pastes the blocks of the
// Nova 2 Lite in each of its modes into docs/hardware-matrix.md.

// readouter is what the direct list reads the getters from: an
// *input.Source, or a fake in the tests.
type readouter interface {
	Readout(input.DeviceID) (input.Readout, error)
}

// ipcDevice is the part of a devices.list row that --list shows.
type ipcDevice struct {
	Info    input.DeviceInfo `json:"info"`
	Battery *input.Power     `json:"battery,omitempty"`
}

// padDetails is one block of the list.
type padDetails struct {
	info input.DeviceInfo
	// power is nil when none was read. connection is "" and player is nil
	// when the source does not report them (the daemon does not).
	power      *input.Power
	connection string
	player     *int
	// err is why the getters could not be read, when they could not.
	err error
}

// padDetailsFor reads the getters of every gamepad of infos from r. A
// joystick without a gamepad mapping has no gamepad to read and gets no
// block.
func padDetailsFor(r readouter, infos []input.DeviceInfo) []padDetails {
	var out []padDetails
	for _, info := range infos {
		if !info.IsGamepad {
			continue
		}
		d := padDetails{info: info}
		ro, err := r.Readout(info.ID)
		if err != nil {
			d.err = err
		} else {
			p, idx := ro.Power, ro.PlayerIndex
			d.power, d.connection, d.player = &p, ro.Connection, &idx
		}
		out = append(out, d)
	}
	return out
}

// padDetailsFromIPC makes the blocks of devices.list rows.
func padDetailsFromIPC(devices []ipcDevice) []padDetails {
	var out []padDetails
	for _, dev := range devices {
		if !dev.Info.IsGamepad {
			continue
		}
		d := padDetails{info: dev.Info}
		if dev.Battery != nil {
			p := *dev.Battery
			d.power = &p
		}
		out = append(out, d)
	}
	return out
}

// notReported is what a block says of a value its source does not give.
const notReported = "not reported by the daemon; use --direct"

// printPadDetails prints the blocks:
//
//	pad 3 "XInput Controller #1"
//	  capabilities  rumble
//	  power         charging 100%
//	  connection    wired
//	  player index  0
func printPadDetails(w io.Writer, pads []padDetails) error {
	var b strings.Builder
	row := func(label, value string) { fmt.Fprintf(&b, "  %-12s  %s\n", label, value) }
	for _, d := range pads {
		fmt.Fprintf(&b, "pad %d %q\n", d.info.ID, d.info.Name)
		row("capabilities", formatCapabilities(d.info.Capabilities))
		if d.err != nil {
			row("readout", "failed: "+d.err.Error())
			continue
		}
		row("power", formatPower(d.power))
		if d.connection == "" {
			row("connection", notReported)
		} else {
			row("connection", d.connection)
		}
		switch {
		case d.player == nil:
			row("player index", notReported)
		case *d.player < 0:
			row("player index", "none")
		default:
			row("player index", strconv.Itoa(*d.player))
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// formatCapabilities lists what SDL reports the gamepad can do, with the
// names of devices.list. rgbLed implies a led, so a pad with it shows only
// rgbLed, and led alone is a single-color LED. "unknown" is a gamepad whose
// capabilities were not read (an old trace, a gamepad that failed to open).
func formatCapabilities(c input.Capabilities) string {
	if !c.Known {
		return "unknown"
	}
	var names []string
	add := func(on bool, name string) {
		if on {
			names = append(names, name)
		}
	}
	add(c.Rumble, "rumble")
	add(c.TriggerRumble, "triggerRumble")
	add(c.LED && !c.RGBLED, "led")
	add(c.RGBLED, "rgbLed")
	add(c.PlayerLED, "playerLed")
	add(c.Gyro, "gyro")
	add(c.Accelerometer, "accelerometer")
	if c.Touchpads > 0 {
		names = append(names, "touchpads="+strconv.Itoa(int(c.Touchpads)))
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// formatPower is the state and, when the gamepad reports one, the charge:
// "discharging 80%", "wired", "unknown". p is nil for a battery that no one
// read.
func formatPower(p *input.Power) string {
	switch {
	case p == nil:
		return "not read"
	case p.State == input.PowerUnknown || p.Percent < 0:
		return p.State.String()
	}
	return fmt.Sprintf("%s %d%%", p.State, p.Percent)
}

// padList follows the DeviceAdded and DeviceRemoved events of a direct
// --list, to know which gamepads are connected when the wait ends.
type padList struct {
	pads []input.DeviceInfo
}

// observe notes e.
func (l *padList) observe(e input.Event) {
	switch e.Kind {
	case input.DeviceAdded:
		if e.Info == nil {
			return
		}
		if i := slices.IndexFunc(l.pads, func(p input.DeviceInfo) bool { return p.ID == e.Device }); i >= 0 {
			l.pads[i] = *e.Info
			return
		}
		l.pads = append(l.pads, *e.Info)
	case input.DeviceRemoved:
		l.pads = slices.DeleteFunc(l.pads, func(p input.DeviceInfo) bool { return p.ID == e.Device })
	}
}

// infos returns the connected devices, in the order they were added.
func (l *padList) infos() []input.DeviceInfo { return slices.Clone(l.pads) }
