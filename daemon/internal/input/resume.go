package input

import (
	"context"
	"fmt"
	"slices"

	"github.com/Zyko0/go-sdl3/sdl"
)

type publication struct {
	ctx  context.Context
	done chan struct{}
}

// publication puts a fence behind every critical event queued so far.
// Closing it means those events entered Events, not that a router read them.
// Only T-input calls it and flush. Canceled fences have no waiting owner.
func (s *Source) publication(ctx context.Context) <-chan struct{} {
	s.backlog = slices.DeleteFunc(s.backlog, func(e Event) bool { return e.publication != nil && e.publication.ctx.Err() != nil })
	p := &publication{ctx: ctx, done: make(chan struct{})}
	s.backlog = append(s.backlog, Event{publication: p})
	s.flush()
	return p.done
}

// Reenumerate pumps SDL, reconciles connected devices and publishes a full
// button/axis snapshot of each live gamepad. Every SDL call runs on T-input.
// It returns after all critical events reached Events. The caller must keep
// mapping held, then synchronize the router and slots before resuming.
// Cancellation bounds waiting even if an SDL driver is slow; no SDL calls
// are moved to the caller and T-input is never blocked by a full Events.
func (s *Source) Reenumerate(ctx context.Context) error {
	result := make(chan error, 1)
	var published <-chan struct{}
	select {
	case s.cmds <- func() {
		if err := ctx.Err(); err != nil {
			result <- err
			return
		}
		err := s.reenumerate(ctx)
		if err == nil {
			published = s.publication(ctx)
		}
		result <- err
	}:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
	select {
	case err := <-result:
		if err != nil {
			return err
		}
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
	select {
	case <-published:
		return ctx.Err()
	case <-ctx.Done():
		return ctx.Err()
	case <-s.done:
		return ErrClosed
	}
}

func (s *Source) reenumerate(ctx context.Context) error {
	if s.watch != nil {
		for _, d := range s.devices {
			if !d.dormant {
				s.watch.Observe(Event{Device: d.pub, Kind: DeviceAdded, Info: s.describe(d)})
			}
		}
	}
	sdl.PumpEvents()
	// What waited in SDL's queue across the sleep was queued at an unknown
	// time, and handle stamps events with s.stamp, the reading of the last
	// event of loop, which may be hours old. These events carry none (no
	// latency sample, Event.Stamp), and the next event of loop reads afresh.
	s.stamp = 0
	var ev sdl.Event
	for sdl.PollEvent(&ev) {
		if err := ctx.Err(); err != nil {
			return err
		}
		s.handle(&ev)
	}
	ids, err := sdl.GetJoysticks()
	if err != nil {
		return fmt.Errorf("SDL_GetJoysticks: %w", err)
	}
	now := sdl.TicksNS()
	for id := range s.devices {
		if !slices.Contains(ids, id) {
			s.remove(id, now)
		}
	}
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !id.IsGamepad() {
			switch d := s.devices[id]; {
			case d == nil:
				s.devices[id] = &device{id: id, pub: DeviceID(id), kind: kindRaw}
				s.emit(Event{Time: now, Device: DeviceID(id), Kind: DeviceAdded, Info: deviceInfo(id, false)})
			case d.kind != kindRaw:
				// A mapping was taken away while the events waited in SDL's
				// queue that say so (IN-18).
				s.downgrade(d, now)
			}
			continue
		}
		s.addGamepad(id, now)
		d := s.devices[id]
		if d == nil || d.dormant {
			continue
		} // Do not revive a silent receiver from stale SDL state.
		if d.kind == kindFailed {
			// It failed to open when it was reported (addGamepad); it gets
			// there what an open one has, its power and its effects.
			gp, openErr := id.OpenGamepad()
			if openErr != nil {
				return fmt.Errorf("SDL_OpenGamepad(%d): %w", id, openErr)
			}
			s.attachGamepad(d, gp, s.describe)
			s.readPower(d, now)
		}
		state := &GamepadState{Axes: make(map[string]int16)}
		for b := sdl.GamepadButton(0); b < sdl.GAMEPAD_BUTTON_COUNT; b++ {
			if d.gp.Button(b) {
				state.Buttons = append(state.Buttons, ButtonName(uint8(b)))
			}
		}
		for a := range d.axes {
			d.axes[a] = d.gp.Axis(sdl.GamepadAxis(a))
			state.Axes[AxisName(uint8(a))] = d.axes[a]
		}
		if s.watch != nil {
			// Missed releases must not leave an old emergency hold armed.
			// A combo still physically held starts a fresh hold at this scan.
			s.watch.Observe(Event{Time: now, Device: d.pub, Kind: DeviceAdded, Info: s.describe(d)})
			for _, c := range state.Buttons {
				s.watch.Observe(Event{Time: now, Device: d.pub, Kind: ButtonDown, Control: c})
			}
		}
		state.Dropped = s.dropped.Load()
		s.send(Event{Time: now, Device: d.pub, Kind: DeviceState, Snapshot: state})
	}
	return nil
}
