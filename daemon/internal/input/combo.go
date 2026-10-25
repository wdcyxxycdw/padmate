package input

import (
	"cmp"
	"math"
	"slices"
	"time"
)

// ComboHold is how long the buttons of a combo must be held together
// before it fires (DMN-07).
const ComboHold = 2 * time.Second

// Combo is a set of gamepad buttons that fires a ComboHeld event once all
// of them have been held together for Hold (DMN-07): padmated toggles the
// pause with it. Other buttons held at the same time do not matter.
type Combo struct {
	// Buttons are the members, named as in ButtonName. A combo with no
	// members, or with a name that is not a button, never fires.
	Buttons []string
	// Hold is how long the members must be held together; below 0 counts
	// as 0, which fires at the press of the last member.
	Hold time.Duration
}

// DefaultCombo returns the combo of the gamepad info with the hold of
// ComboHold: back+start, or for a single Joy-Con (joyconleft, joyconright)
// SL+SR; the families standard and joycon of ARCHITECTURE §10.4, which
// config.json may set later (M8). SDL reports SL and SR as
// leftshoulder and rightshoulder when the Joy-Con is held sideways, and as
// paddles when vertical is set, as it is when the hint
// SDL_JOYSTICK_HIDAPI_VERTICAL_JOY_CONS is in effect: paddle2 and paddle4
// on the left one, paddle3 and paddle1 on the right one (ARCHITECTURE
// §7.1). A pair of Joy-Cons has back and start.
func DefaultCombo(info DeviceInfo, vertical bool) Combo {
	c := Combo{Hold: ComboHold}
	switch {
	case info.Type == "joyconleft" && vertical:
		c.Buttons = []string{"paddle2", "paddle4"}
	case info.Type == "joyconright" && vertical:
		c.Buttons = []string{"paddle3", "paddle1"}
	case info.Type == "joyconleft" || info.Type == "joyconright":
		c.Buttons = []string{"leftshoulder", "rightshoulder"}
	default:
		c.Buttons = []string{"back", "start"}
	}
	return c
}

// ComboWatch finds the combos that the gamepads hold (DMN-07). It tracks
// the members held of each device from the Events it observes, without
// asking SDL, so it works on what T-input saw even when Events was full.
// Its methods must be called from one goroutine.
//
// A combo arms when the down of its last member makes all of them held,
// and fires when it has been held for its Hold: at its due time, which
// Due gives and Fire reports. So it latches: held on, it fires once, and
// again only after a member is released and pressed again. Releasing a
// member, or removing the device, disarms it.
type ComboWatch struct {
	pick  func(DeviceInfo) Combo
	pads  map[DeviceID]*comboPad
	armed int // pads whose combo is armed
}

// comboPad is the combo of one device and the state of its members.
type comboPad struct {
	info    DeviceInfo // what pick got; Repick gives it again
	members []string   // distinct; nil when the combo never fires
	all     uint32     // one bit per member
	held    uint32     // the bits of the members held
	hold    uint64     // ns
	since   uint64     // when the last member went down, while armed
	armed   bool
}

// NewComboWatch returns a watch that gives each device the combo pick
// returns for it. pick gets the Info of the device's DeviceAdded event, or
// a DeviceInfo with only the ID for a device whose first button event
// comes without one, such as in a hand-written trace; it picks again at
// each DeviceAdded, and for every device at Repick.
func NewComboWatch(pick func(DeviceInfo) Combo) *ComboWatch {
	return &ComboWatch{pick: pick, pads: make(map[DeviceID]*comboPad)}
}

// Repick gives every device the combo pick returns for it now, with the
// Info it got before: padmated calls it after config.json changed the
// combos (UI-19). It is conservative: it forgets which members are held
// and disarms every combo, also one whose members stay the same, so a
// combo fires only after all its members have been pressed again; a hold
// under way never fires early or with the old members.
func (w *ComboWatch) Repick() {
	for id, p := range w.pads {
		w.disarm(p)
		w.pads[id] = w.newPad(p.info)
	}
}

// newPad returns the pad of the combo pick gives info.
func (w *ComboWatch) newPad(info DeviceInfo) *comboPad {
	p := comboPadOf(w.pick(info))
	p.info = info
	return p
}

// comboPadOf returns the pad of combo c.
func comboPadOf(c Combo) *comboPad {
	p := &comboPad{hold: uint64(max(c.Hold, 0))}
	for _, b := range c.Buttons {
		if _, ok := buttonIndex(b); !ok {
			return &comboPad{}
		}
		if !slices.Contains(p.members, b) {
			p.members = append(p.members, b)
		}
	}
	if len(p.members) > 32 { // cannot happen: there are fewer buttons
		return &comboPad{}
	}
	p.all = uint32(1)<<len(p.members) - 1
	return p
}

// Observe updates the watch with e, which comes after the events it has
// observed. Only DeviceAdded, DeviceRemapped, DeviceRemoved, ButtonDown and
// ButtonUp matter; a removal forgets the device.
func (w *ComboWatch) Observe(e Event) {
	switch e.Kind {
	case DeviceAdded, DeviceRemapped:
		// A remapped gamepad may have other buttons now: it is picked again, and
		// what was held counts as released.
		info := DeviceInfo{ID: e.Device}
		if e.Info != nil {
			info = *e.Info
		}
		w.forget(e.Device)
		w.pads[e.Device] = w.newPad(info)
	case DeviceRemoved:
		w.forget(e.Device)
	case ButtonDown, ButtonUp:
		p := w.pads[e.Device]
		if p == nil {
			p = w.newPad(DeviceInfo{ID: e.Device})
			w.pads[e.Device] = p
		}
		i := slices.Index(p.members, e.Control)
		if i < 0 {
			return
		}
		bit := uint32(1) << i
		if e.Kind == ButtonUp {
			p.held &^= bit
			w.disarm(p)
			return
		}
		if p.held&bit != 0 {
			return // a repeated down neither restarts the hold nor re-arms
		}
		p.held |= bit
		if p.held == p.all {
			p.armed, p.since = true, e.Time
			w.armed++
		}
	}
}

// forget drops the device id.
func (w *ComboWatch) forget(id DeviceID) {
	if p := w.pads[id]; p != nil {
		w.disarm(p)
		delete(w.pads, id)
	}
}

func (w *ComboWatch) disarm(p *comboPad) {
	if p.armed {
		p.armed = false
		w.armed--
	}
}

// due returns when the armed combo of p fires.
func (p *comboPad) due() uint64 {
	if p.since > math.MaxUint64-p.hold {
		return math.MaxUint64
	}
	return p.since + p.hold
}

// Due returns the earliest due time of the armed combos, on the clock of
// Event.Time, and false when none is armed.
func (w *ComboWatch) Due() (uint64, bool) {
	if w.armed == 0 {
		return 0, false
	}
	var first uint64 = math.MaxUint64
	for _, p := range w.pads {
		if p.armed {
			first = min(first, p.due())
		}
	}
	return first, true
}

// Fire fires the armed combos due at or before now, which disarms them. It
// returns a ComboHeld event for each, with Time its due time, in the order
// of due time, then device; nil when none is due.
func (w *ComboWatch) Fire(now uint64) []Event {
	if w.armed == 0 {
		return nil
	}
	var fired []Event
	for id, p := range w.pads {
		if p.armed && p.due() <= now {
			fired = append(fired, Event{Time: p.due(), Device: id, Kind: ComboHeld})
			w.disarm(p)
		}
	}
	slices.SortFunc(fired, func(a, b Event) int {
		return cmp.Or(cmp.Compare(a.Time, b.Time), cmp.Compare(a.Device, b.Device))
	})
	return fired
}

// ExpandCombos returns evs with the ComboHeld events that a Source would
// send among them, from a ComboWatch with pick: before each event, the
// combos due at or before its time, and after the last event, the combos
// still armed. It lets a replay, which has no Source, pause as padmated
// would. Where the times of evs decrease, as they may between devices, a
// combo fires before the first event at or after its due time.
// ExpandCombos does not change evs.
func ExpandCombos(evs []Event, pick func(DeviceInfo) Combo) []Event {
	w := NewComboWatch(pick)
	out := make([]Event, 0, len(evs))
	for _, e := range evs {
		out = append(out, w.Fire(e.Time)...)
		w.Observe(e)
		out = append(out, e)
	}
	return append(out, w.Fire(math.MaxUint64)...)
}
