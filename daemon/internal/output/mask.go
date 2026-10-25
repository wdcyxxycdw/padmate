package output

import (
	"fmt"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// MenuMaskVK is the virtual-key code of the menu mask key (OUT-08,
// ADR-0021): 0xE8, which Microsoft's virtual-key table lists as
// unassigned.
const MenuMaskVK = 0xE8

// MenuMaskEvent returns the press of the menu mask key, or with up its
// release: sent by VK, with no Key and scan code 0, since no physical
// key has it.
func MenuMaskEvent(up bool) KeyEvent { return KeyEvent{ByVK: true, VK: MenuMaskVK, Up: up} }

// IsMenuMask reports whether e is an event of the menu mask key.
func IsMenuMask(e KeyEvent) bool { return e.ID() == menuMaskID }

var menuMaskID = MenuMaskEvent(false).ID()

// menuKeys are the keys whose lone release opens a menu: LAlt, RAlt, LWin
// and RWin. The raw sc:0x38 is LAlt's KeyID too.
var menuKeys = [...]KeyID{
	keyID(laltKey), keyID(mustLookup("RAlt")), keyID(mustLookup("LWin")), keyID(mustLookup("RWin")),
}

// allMenuKeys has the bit of every menu key.
const allMenuKeys = 1<<len(menuKeys) - 1

func keyID(k keys.Key) KeyID {
	e, err := KeyEventFor(k, false)
	if err != nil {
		panic(err)
	}
	return e.ID()
}

// menuKey returns the index of e's key in menuKeys, or -1.
func menuKey(e KeyEvent) int {
	id := e.ID()
	for i, k := range menuKeys {
		if k == id {
			return i
		}
	}
	return -1
}

// MenuMask is a Sender that keeps a lone Alt or Win from opening the menu
// bar or the Start menu (OUT-08, ADR-0021). Before the release of LAlt,
// RAlt, LWin or RWin, when no other key was pressed since that key's last
// press, it inserts a press and a release of the mask key, in the same
// batch. It sees only the events it forwards, and only those the Sender
// injected change its state.
//
// Send returns how many of the caller's events were injected: always a
// prefix, since the last event of a forwarded batch is always the
// caller's. When the mask key's press went out without its release, the
// next Send starts with the release. A panic in the Sender leaves the
// state conservative (more masks, and the mask counted as down) and goes
// on.
//
// A MenuMask is used from one goroutine, like the Arbiter above it; it is
// not locked. Two stacked MenuMasks behave as one: the outer one's mask
// press counts as another key for the inner one.
type MenuMask struct {
	s        Sender
	dirty    uint8 // bit i: another key was pressed since menuKeys[i]'s last press
	maskDown bool  // an inserted mask press went out without its release
	out      []KeyEvent
	inserted []bool // parallel to out: the event is the MenuMask's own
}

// NewMenuMask returns a MenuMask that sends through s. It panics if s is
// nil.
func NewMenuMask(s Sender) *MenuMask {
	if s == nil {
		panic("output: NewMenuMask needs a sender")
	}
	return &MenuMask{s: s}
}

// Send implements Sender.
func (m *MenuMask) Send(events []KeyEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	out, masked := m.plan(events)
	ok := false
	defer func() {
		if !ok {
			m.afterPanic(events, out, masked)
		}
	}()
	n, err := send(m.s, out)
	ok = true
	c := 0
	for i, e := range out[:n] {
		if masked && m.inserted[i] {
			m.maskDown = !e.Up
		} else {
			c++
		}
		m.dirty = dirtyAfter(m.dirty, e)
	}
	if n < len(out) {
		switch {
		case m.maskDown:
			err = maskError("left down", err)
		case masked && m.inserted[n]:
			err = maskError("not injected", err)
		}
	}
	return c, err
}

// releaseMask sends the mask key's release when its press went out
// without it, for a caller with no event of its own to carry it: the
// Loop's ledger after a panic, when it has no key down. The Arbiter sends
// the release of a press that failed, and a masked one can leave the mask
// down with nothing down in the ledger. A panic in the Sender goes on, and
// the mask stays counted as down.
func (m *MenuMask) releaseMask() error {
	if !m.maskDown {
		return nil
	}
	n, err := send(m.s, []KeyEvent{MenuMaskEvent(true)})
	if n == 0 {
		return maskError("left down", err)
	}
	m.maskDown = false
	return err
}

// maskError wraps err, the error of a Send that left the mask key in
// state.
func maskError(state string, err error) error {
	return fmt.Errorf("output: menu mask %s %s: %w", MenuMaskEvent(false).name(), state, err)
}

// plan returns the batch that sends events: events itself when it needs
// no mask, else m.out, with m.inserted marking the MenuMask's own events.
func (m *MenuMask) plan(events []KeyEvent) (out []KeyEvent, masked bool) {
	m.out, m.inserted = m.out[:0], m.inserted[:0]
	dirty := m.dirty
	if m.maskDown {
		m.insert(MenuMaskEvent(true))
		masked = true
	}
	for i, e := range events {
		if k := menuKey(e); k >= 0 && e.Up && dirty&(1<<k) == 0 {
			if !masked {
				for _, c := range events[:i] {
					m.forward(c)
				}
				masked = true
			}
			m.insert(MenuMaskEvent(false))
			m.insert(MenuMaskEvent(true))
			dirty = allMenuKeys
		}
		if masked {
			m.forward(e)
		}
		dirty = dirtyAfter(dirty, e)
	}
	if !masked {
		return events, false
	}
	return m.out, true
}

func (m *MenuMask) insert(e KeyEvent) {
	m.out = append(m.out, e)
	m.inserted = append(m.inserted, true)
}

func (m *MenuMask) forward(e KeyEvent) {
	m.out = append(m.out, e)
	m.inserted = append(m.inserted, false)
}

// dirtyAfter returns the dirty bits after e: a press of a menu key makes
// it clean and the others dirty, any other press makes every menu key
// dirty, and a release of a menu key makes it clean. A mouse event, a wheel
// notch or a button press or release, is not a key press for this: a lone Alt
// released after Alt+WheelUp or Alt+MouseLeft still gets the mask key, which
// is harmless, and safe whatever Windows makes of it. Neither is a Unicode
// event, a unit of a typed text (OUT-21): it is the character that counts, not
// a key, and the extra mask key does no harm.
func dirtyAfter(dirty uint8, e KeyEvent) uint8 {
	k := menuKey(e)
	switch {
	case e.isMouse(), e.Unicode:
		return dirty
	case !e.Up && k >= 0:
		return allMenuKeys &^ (1 << k)
	case !e.Up:
		return allMenuKeys
	case k >= 0:
		return dirty &^ (1 << k)
	}
	return dirty
}

// afterPanic assumes the worst for masking after the Sender panicked: any
// menu key pressed in events may have gone down alone, and the mask key
// may be down if out pressed it.
func (m *MenuMask) afterPanic(events, out []KeyEvent, masked bool) {
	for _, e := range events {
		if k := menuKey(e); k >= 0 && !e.Up {
			m.dirty &^= 1 << k
		}
	}
	if !masked {
		return
	}
	for i, e := range out {
		if m.inserted[i] && !e.Up {
			m.maskDown = true
		}
	}
}
