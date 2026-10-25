package inputtest

import (
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

// ProductSwitchPro is the USB product id of the Switch Pro protocol
// (SDL's usb_ids.h): the Switch Pro Controller, and GameSir's Nova 2 Lite
// in Switch mode, behind its 2.4G receiver.
const ProductSwitchPro = 0x2009

// SwitchPro describes a virtual gamepad with the ids and controls that
// SDL 3.4.16 gives a Switch Pro Controller (the HIDAPI Switch mapping, as
// read from the source), with a heartbeat (VirtualDesc.Heartbeat), so that
// a Source with Options.ReceiverTimeout watches it as it watches a
// receiver (IN-30). It models SDL's mapping and the heartbeat reports,
// not the device.
func SwitchPro(name string) input.VirtualDesc {
	return input.VirtualDesc{
		Name:    name,
		Vendor:  VendorNintendo,
		Product: ProductSwitchPro,
		Buttons: []string{
			"south", "east", "west", "north", "back", "guide", "start",
			"leftstick", "rightstick", "leftshoulder", "rightshoulder",
			"dpad.up", "dpad.down", "dpad.left", "dpad.right", "misc1",
		},
		Axes:      []string{"leftx", "lefty", "rightx", "righty", "lefttrigger", "righttrigger"},
		Heartbeat: true,
	}
}

// Beats calls v.Beat every interval on a goroutine of its own, starting
// at once, until stop is called, Beat fails, or the test ends. stop waits
// for the goroutine to end, so no heartbeat follows it, and returns the
// time taken just before the last Beat that succeeded, zero for none:
// T-input reads that heartbeat no earlier, so a loss comes at least a
// timeout after it, however late the goroutine was. Calling stop again
// returns the same.
func Beats(t testing.TB, v *input.Virtual, every time.Duration) (stop func() (last time.Time)) {
	t.Helper()
	quit, done := make(chan struct{}), make(chan struct{})
	var lastBeat time.Time // the goroutine's until done is closed
	go func() {
		defer close(done)
		tick := time.NewTicker(every)
		defer tick.Stop()
		for {
			at := time.Now()
			if v.Beat() != nil {
				return
			}
			lastBeat = at
			select {
			case <-quit:
				return
			case <-tick.C:
			}
		}
	}()
	var once sync.Once
	stop = func() time.Time {
		once.Do(func() { close(quit) })
		<-done
		return lastBeat
	}
	t.Cleanup(func() { stop() })
	return stop
}
