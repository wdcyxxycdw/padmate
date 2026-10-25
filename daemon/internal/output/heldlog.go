package output

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// The held-key log of OUT-14 (ADR-0027) is a file of heldSize bytes: the
// magic "PDMH", a little-endian uint32 version, the HeldIdentity of the
// run that wrote it (HeldIdentity.put), and three bitmaps of 256 bits, one
// bit per KeyID. Bitmap 0 has the scan codes without E0, bitmap 1 those
// with E0 and bitmap 2 the VKs of the keys sent by VK, and in the slots of
// VK_LBUTTON (0x01), VK_RBUTTON (0x02), VK_MBUTTON (0x04), VK_XBUTTON1 (0x05)
// and VK_XBUTTON2 (0x06) the mouse buttons (ADR-0062), which no key is sent
// by. The menu mask key (ADR-0021) and the wheel notches, which leave nothing
// down (ADR-0062), are never in it. Version 2 had no identity and version 3 a
// boot time where LogonTime is: the sweep resets both as corrupt. The buttons
// did not change the format or the version: an older padmated reads their
// bits as keys sent by those VKs. The media and volume keys (OUT-20) are sent
// by VK as well and so have a bit of bitmap 2 like Pause; the Unicode events of
// a typed text (OUT-21) never have one.
const (
	heldMagic   = "PDMH"
	heldVersion = 4
	heldHeader  = 8 + heldIdentitySize
	heldSize    = heldHeader + len(heldBits{})
)

// heldOpenWait is how long openHeldLog tries again while another process
// has the log open; tests shorten it.
var heldOpenWait = time.Second

// heldOpenRetry is how often openHeldLog tries again.
const heldOpenRetry = 50 * time.Millisecond

// ErrHeldInUse is the error of the startup sweep when another process has
// the held-key log open, such as a second padmated with --inject, still
// after heldOpenWait: that run keeps no log (SweepResult.Off).
var ErrHeldInUse = errors.New("output: the held-key log is open in another process")

// errHeldCorrupt is the error of a held-key log that is not one: too
// short, or with the wrong magic or version. The sweep resets it.
var errHeldCorrupt = errors.New("output: the held-key log is corrupt")

// heldBits are the three bitmaps of the held-key log.
type heldBits [3 * 256 / 8]byte

// heldBit returns the byte and the bit of id in heldBits. ok is false for
// the menu mask key, which is never logged, for a wheel notch (no state that
// outlives the process), for a Unicode event (a unit of a typed text, which is
// never held, OUT-21), and for a VK above 0xFF or 0. A mouse button has the
// bit of its VK (buttonVK).
func heldBit(id KeyID) (idx int, mask byte, ok bool) {
	var bit int
	switch {
	case id == menuMaskID:
		return 0, 0, false
	case id.Unicode:
		return 0, 0, false
	case id.Mouse != keys.MouseNone:
		vk, isButton := buttonVK(id.Mouse)
		if !isButton {
			return 0, 0, false
		}
		bit = 512 + int(vk)
	case id.ByVK:
		if id.VK == 0 || id.VK > 0xFF {
			return 0, 0, false
		}
		bit = 512 + int(id.VK)
	case id.Ext:
		bit = 256 + int(id.Scan)
	default:
		bit = int(id.Scan)
	}
	return bit / 8, 1 << (bit % 8), true
}

// buttonVK returns the virtual-key code a mouse button has in the held-key
// log and in GetAsyncKeyState: VK_LBUTTON 0x01, VK_RBUTTON 0x02, VK_MBUTTON
// 0x04, VK_XBUTTON1 0x05 and VK_XBUTTON2 0x06. ok is false for every other
// mouse input.
func buttonVK(m keys.Mouse) (vk uint16, ok bool) {
	switch m {
	case keys.MouseLeft:
		return 0x01, true
	case keys.MouseRight:
		return 0x02, true
	case keys.MouseMiddle:
		return 0x04, true
	case keys.MouseX1:
		return 0x05, true
	case keys.MouseX2:
		return 0x06, true
	}
	return 0, false
}

// vkButton is the mouse button that has vk (buttonVK), if any.
func vkButton(vk uint16) (keys.Mouse, bool) {
	for m := keys.MouseLeft; m <= keys.MouseX2; m++ {
		if v, _ := buttonVK(m); v == vk {
			return m, true
		}
	}
	return keys.MouseNone, false
}

// bitID is the KeyID of bit i of heldBits.
func bitID(i int) KeyID {
	switch {
	case i >= 512:
		if m, ok := vkButton(uint16(i - 512)); ok {
			return KeyID{Mouse: m}
		}
		return KeyID{ByVK: true, VK: uint16(i - 512)}
	case i >= 256:
		return KeyID{Scan: uint8(i - 256), Ext: true}
	default:
		return KeyID{Scan: uint8(i)}
	}
}

func (b *heldBits) set(id KeyID) {
	if i, m, ok := heldBit(id); ok {
		b[i] |= m
	}
}

func (b *heldBits) clear(id KeyID) {
	if i, m, ok := heldBit(id); ok {
		b[i] &^= m
	}
}

// keys returns the releases of the keys in b, as the startup sweep sends
// them: the other keys first, then the modifiers, each group in bit order.
// A key of the US table has its Key, the sided one for a modifier, so that
// messages name it (LCtrl); a raw scan code has none.
func (b *heldBits) keys() []KeyEvent {
	var out []KeyEvent
	for _, mods := range []bool{false, true} {
		for i := range len(b) * 8 {
			if b[i/8]&(1<<(i%8)) == 0 {
				continue
			}
			id := bitID(i)
			if modifierIDs[id] != mods {
				continue
			}
			e, ok := heldKeyEvents[id]
			if !ok {
				e = KeyEvent{Scan: id.Scan, Ext: id.Ext, ByVK: id.ByVK, VK: id.VK}
			}
			e.Up = true
			out = append(out, e)
		}
	}
	return out
}

// heldKeyEvents are the presses of the keys of the US table by KeyID: the
// first key of the table on each KeyID, except that a sided modifier wins
// over its generic one. The wheel keys are not in it, as they are never
// logged; the mouse buttons are.
var heldKeyEvents = func() map[KeyID]KeyEvent {
	const generic = keys.ModCtrl | keys.ModShift | keys.ModAlt | keys.ModWin
	m := map[KeyID]KeyEvent{}
	for _, k := range keys.Table() {
		e, err := KeyEventFor(k, false)
		if err != nil || e.momentary() {
			continue
		}
		if old, ok := m[e.ID()]; !ok || old.Key.Mod()&generic != 0 {
			m[e.ID()] = e
		}
	}
	return m
}()

// heldKeyless has the bits of heldBits that no key has, those of VK 0 and
// of the menu mask key: heldBit never sets or clears them.
var heldKeyless = func() (m heldBits) {
	for i := range len(m) * 8 {
		if _, _, ok := heldBit(bitID(i)); !ok {
			m[i/8] |= 1 << (i % 8)
		}
	}
	return m
}()

// decodeHeld decodes the content of a held-key log: the HeldIdentity of
// the run that wrote it and its keys. Its errors wrap errHeldCorrupt. It
// drops the bits of heldKeyless, which only corruption or another writer
// sets: the sweep could neither release a key for them nor ever take them
// off, so the log would never be removed again.
func decodeHeld(b []byte) (HeldIdentity, heldBits, error) {
	var bits heldBits
	switch {
	case len(b) < heldSize:
		return HeldIdentity{}, bits, fmt.Errorf("%w: %d bytes, want %d", errHeldCorrupt, len(b), heldSize)
	case string(b[:4]) != heldMagic:
		return HeldIdentity{}, bits, fmt.Errorf("%w: magic %q, want %q", errHeldCorrupt, b[:4], heldMagic)
	case binary.LittleEndian.Uint32(b[4:8]) != heldVersion:
		return HeldIdentity{}, bits, fmt.Errorf("%w: version %d, want %d", errHeldCorrupt, binary.LittleEndian.Uint32(b[4:8]), heldVersion)
	}
	copy(bits[:], b[heldHeader:heldSize])
	for i := range bits {
		bits[i] &^= heldKeyless[i]
	}
	return heldIdentityAt(b[8:heldHeader]), bits, nil
}

// heldFileHandle is the open held-key log: heldfile_windows.go opens it
// with CreateFile, shared for reading only, and writes it with WriteFile at
// an offset; elsewhere it is an *os.File, not locked.
type heldFileHandle interface {
	ReadAt(p []byte, off int64) (int, error)
	WriteAt(p []byte, off int64) (int, error)
	Close() error
}

// Test hooks: how the log is opened and removed.
var (
	openHeldFile = openHeldFileOS
	removeHeld   = os.Remove
)

// heldLog is the held-key log of a Loop (OUT-14, ADR-0027), open for its
// whole run. The ledger marks the presses of a batch before it sends them
// and settles the log after, so that the file has every key that may be
// down whenever the process is killed. Only the loop goroutine uses it.
type heldLog struct {
	path string
	f    heldFileHandle // nil once finished
	// bits is what the file has; stale is set after a failed write, when
	// the file may have anything, so that the next write goes out even if
	// it asks for bits.
	bits  heldBits
	stale bool
	// carried are the keys the startup sweep found down and could not
	// release: they stay in the log until their release goes out.
	carried heldBits
	// want and buf are scratch for the next write, in the heldLog so that
	// the writes do not allocate.
	want heldBits
	buf  [heldSize]byte
}

// openHeldLog creates the log's directory and opens the log for this run
// of the logon session id, trying again every heldOpenRetry for
// heldOpenWait while another process has it open, then returns what the
// last run left in it: from, the logon session of that run, and
// prev, its keys. Every write of this run puts id in the header, the next
// one included. It writes a new file's header, and returns id as from. It
// resets a corrupt log to no key and returns it with id as from and an
// error that wraps errHeldCorrupt. It returns no heldLog when it cannot
// open or write the log; the error then wraps ErrHeldInUse when another
// process has it open.
func openHeldLog(path string, id HeldIdentity) (h *heldLog, from HeldIdentity, prev heldBits, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, id, heldBits{}, fmt.Errorf("output: create the held-key log's directory: %w", err)
	}
	f, err := openHeldFile(path)
	for wait := time.Duration(0); errors.Is(err, ErrHeldInUse) && wait < heldOpenWait; wait += heldOpenRetry {
		time.Sleep(heldOpenRetry)
		f, err = openHeldFile(path)
	}
	if err != nil {
		return nil, id, heldBits{}, err
	}
	h = &heldLog{path: path, f: f}
	copy(h.buf[:4], heldMagic)
	binary.LittleEndian.PutUint32(h.buf[4:8], heldVersion)
	id.put(h.buf[8:heldHeader])
	var old [heldSize]byte
	n, err := f.ReadAt(old[:], 0)
	if err != nil && !errors.Is(err, io.EOF) {
		_ = f.Close()
		return nil, id, heldBits{}, fmt.Errorf("output: read the held-key log %s: %w", path, err)
	}
	from, prev, corrupt := decodeHeld(old[:n])
	if n == 0 {
		corrupt = nil // a new file
	}
	// The next write puts this run's header, whatever the file had.
	h.stale = true
	if n == 0 || corrupt != nil {
		if err := h.flush(); err != nil { // the header and no key
			_ = f.Close()
			return nil, id, heldBits{}, err
		}
		return h, id, heldBits{}, corrupt
	}
	// bits is what the file has, the bits prev dropped included, so that
	// the next write clears them.
	copy(h.bits[:], old[heldHeader:heldSize])
	return h, from, prev, nil
}

// mark adds the keys that events press to the log, before they are sent:
// all of them, even those a later event of the batch releases, as the
// Sender may inject any prefix.
func (h *heldLog) mark(events []KeyEvent) error {
	if h.f == nil {
		return nil
	}
	h.want = h.bits
	for _, e := range events {
		if !e.Up {
			h.want.set(e.ID())
		}
	}
	return h.flush()
}

// settle sets the log to the keys of down, the ledger's after a Send, and
// the carried keys, after taking off the carried keys sent released.
func (h *heldLog) settle(down, sent []KeyEvent) error {
	if h.f == nil {
		return nil
	}
	for _, e := range sent {
		if e.Up {
			h.carried.clear(e.ID())
		}
	}
	h.want = h.carried
	for _, e := range down {
		h.want.set(e.ID())
	}
	return h.flush()
}

// flush writes want when the file may have something else. bits changes
// only when the write succeeds.
func (h *heldLog) flush() error {
	if h.want == h.bits && !h.stale {
		return nil
	}
	copy(h.buf[heldHeader:], h.want[:])
	n, err := h.f.WriteAt(h.buf[:], 0)
	if err == nil && n < len(h.buf) {
		err = io.ErrShortWrite
	}
	if err != nil {
		h.stale = true
		return fmt.Errorf("output: write the held-key log %s: %w", h.path, err)
	}
	h.bits, h.stale = h.want, false
	return nil
}

// finish settles the log to down and the carried keys, and closes it: at
// the end of a run, down is what the ledger may have left down. It
// removes the file when no key is in it, and otherwise leaves it for the
// next start. It does nothing
// on a nil heldLog, or after it has run once, a panic in it included.
func (h *heldLog) finish(down []KeyEvent) error {
	if h == nil || h.f == nil {
		return nil
	}
	err := h.settle(down, nil)
	f := h.f
	h.f = nil
	if cerr := f.Close(); cerr != nil {
		return errors.Join(err, fmt.Errorf("output: close the held-key log %s: %w", h.path, cerr))
	}
	if err != nil || h.stale || h.bits != (heldBits{}) {
		return err
	}
	if err := removeHeld(h.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("output: remove the held-key log: %w", err)
	}
	return nil
}
