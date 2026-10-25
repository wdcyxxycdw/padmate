package main

import (
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// arbiterTiming is shared by real, dry-run and virtual-clock replay paths:
// the profile's tap timing, and the event gap of settings (OUT-17), nil
// without a library.
func arbiterTiming(prog *engine.Program, settings *profile.Config) output.ArbiterOptions {
	timing := prog.Timing()
	return output.ArbiterOptions{
		MinPress: time.Duration(timing.TapPressMs) * time.Millisecond,
		MinGap:   time.Duration(timing.TapGapMs) * time.Millisecond,
		EventGap: eventGap(settings),
	}
}

// eventGap is config.json's output.eventGapMs (OUT-17); 0 without one.
func eventGap(settings *profile.Config) time.Duration {
	if settings == nil {
		return 0
	}
	return time.Duration(settings.Output.EventGapMs) * time.Millisecond
}
