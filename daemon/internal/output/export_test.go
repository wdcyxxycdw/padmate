package output

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// QueuedTap is one physical main key's pending tap, in arrival order.
type QueuedTap struct {
	Owner Owner
	Chord keys.Chord
	Main  KeyID
}

// QueuedTaps exposes only the queue to the independent fuzz model.
func QueuedTaps(a *Arbiter) []QueuedTap {
	q := make([]QueuedTap, len(a.queued))
	for i, item := range a.queued {
		q[i] = QueuedTap{item.o, item.c, item.main}
	}
	return q
}

// PacedEvents returns the events waiting for the event gap, in order.
func PacedEvents(a *Arbiter) []KeyEvent {
	events := make([]KeyEvent, len(a.outbox))
	for i, q := range a.outbox {
		events[i] = q.e
	}
	return events
}

// PacedExtras reports which PacedEvents are extra presses: Repeat, or a
// tap on a key held by Down.
func PacedExtras(a *Arbiter) []bool {
	extras := make([]bool, len(a.outbox))
	for i, q := range a.outbox {
		extras[i] = q.extra
	}
	return extras
}

// PacedOwners returns the owners whose commands queued PacedEvents.
func PacedOwners(a *Arbiter) []Owner {
	owners := make([]Owner, len(a.outbox))
	for i, q := range a.outbox {
		owners[i] = q.o
	}
	return owners
}

// MergedKeys returns the keys o's hold of c references when it is a Down
// merged into an earlier press of its main key that left a modifier out
// (mergeDown), and false otherwise.
func MergedKeys(a *Arbiter, o Owner, c keys.Chord) ([]KeyID, bool) {
	r, ok := a.holds[holdID{o, c.Sided()}]
	if !ok || !r.merged {
		return nil, false
	}
	return slices.Clone(r.keys), true
}

// ReleaseMask is m.releaseMask, which the Loop's ledger calls after a
// panic when it has no key down.
func ReleaseMask(m *MenuMask) error { return m.releaseMask() }

// ErrHeldCorrupt is errHeldCorrupt, which the errors of a corrupt held-key
// log wrap.
var ErrHeldCorrupt = errHeldCorrupt

// HeldSize is the size of a held-key log.
const HeldSize = heldSize

// EncodeHeld returns a held-key log with the keys of events, of this
// logon session (SystemHeldIdentity), as a Loop without
// LoopOptions.Identity writes it.
func EncodeHeld(events ...KeyEvent) []byte {
	id, err := SystemHeldIdentity()
	if err != nil {
		panic(err)
	}
	return EncodeHeldAs(id, events...)
}

// EncodeHeldAs returns a held-key log with the keys of events, of the
// logon session id.
func EncodeHeldAs(id HeldIdentity, events ...KeyEvent) []byte {
	var bits heldBits
	for _, e := range events {
		bits.set(e.ID())
	}
	b := make([]byte, heldSize)
	copy(b, heldMagic)
	b[4] = heldVersion
	id.put(b[8:heldHeader])
	copy(b[heldHeader:], bits[:])
	return b
}

// HeldIdentityOf returns the logon session in the header of the
// held-key log b.
func HeldIdentityOf(b []byte) (HeldIdentity, error) {
	id, _, err := decodeHeld(b)
	return id, err
}

// WithHeldVK returns a copy of the held-key log b with the bits of the
// VKs vks set in the bitmap of the keys sent by VK, also those no key has
// (VK 0, MenuMaskVK), as corruption or another writer may set them.
func WithHeldVK(b []byte, vks ...uint8) []byte {
	b = slices.Clone(b)
	for _, vk := range vks {
		b[heldHeader+512/8+int(vk)/8] |= 1 << (vk % 8)
	}
	return b
}

// DecodeHeld returns the KeyIDs of every bit a held-key log has set, in
// bit order: also those of the bits no key has (VK 0, MenuMaskVK), which
// the sweep drops.
func DecodeHeld(b []byte) ([]KeyID, error) {
	if _, _, err := decodeHeld(b); err != nil {
		return nil, err
	}
	var ids []KeyID
	for i := range len(heldBits{}) * 8 {
		if b[heldHeader+i/8]&(1<<(i%8)) != 0 {
			ids = append(ids, bitID(i))
		}
	}
	return ids, nil
}

// HeldReleases returns the releases the startup sweep would send for the
// keys of a held-key log, in its order.
func HeldReleases(b []byte) ([]KeyEvent, error) {
	_, bits, err := decodeHeld(b)
	if err != nil {
		return nil, err
	}
	return bits.keys(), nil
}

// SetHeldOpenWait sets how long the sweep waits for a held-key log another
// process has open, for the rest of the test.
func SetHeldOpenWait(t testing.TB, d time.Duration) {
	old := heldOpenWait
	heldOpenWait = d
	t.Cleanup(func() { heldOpenWait = old })
}

// HeldFaults makes the held-key logs opened for the rest of the test fail
// on demand. Its methods are safe for concurrent use: the Loop writes on
// its goroutine.
type HeldFaults struct {
	mu sync.Mutex
	// write, close and remove are the errors of the coming calls, in
	// order; a nil entry lets that call through.
	write, close, remove []error
	panicClose           bool
	closes               int
}

// FaultHeld installs HeldFaults for the rest of the test.
func FaultHeld(t testing.TB) *HeldFaults {
	hf := &HeldFaults{}
	oldOpen, oldRemove := openHeldFile, removeHeld
	openHeldFile = func(path string) (heldFileHandle, error) {
		f, err := oldOpen(path)
		if err != nil {
			return nil, err
		}
		return &faultFile{f: f, hf: hf}, nil
	}
	removeHeld = func(path string) error {
		if err := hf.next(&hf.remove); err != nil {
			return err
		}
		return oldRemove(path)
	}
	t.Cleanup(func() { openHeldFile, removeHeld = oldOpen, oldRemove })
	return hf
}

// FailWrites makes the coming writes return errs in turn.
func (hf *HeldFaults) FailWrites(errs ...error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.write = append(hf.write, errs...)
}

// FailClose makes the next Close return err after closing.
func (hf *HeldFaults) FailClose(err error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.close = append(hf.close, err)
}

// FailRemove makes the next removal return err, removing nothing.
func (hf *HeldFaults) FailRemove(err error) {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.remove = append(hf.remove, err)
}

// PanicClose makes the next Close panic after closing.
func (hf *HeldFaults) PanicClose() {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	hf.panicClose = true
}

// Closes returns how many times a log was closed.
func (hf *HeldFaults) Closes() int {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	return hf.closes
}

func (hf *HeldFaults) next(q *[]error) error {
	hf.mu.Lock()
	defer hf.mu.Unlock()
	if len(*q) == 0 {
		return nil
	}
	err := (*q)[0]
	*q = slices.Delete(*q, 0, 1)
	return err
}

type faultFile struct {
	f  heldFileHandle
	hf *HeldFaults
}

func (f *faultFile) ReadAt(p []byte, off int64) (int, error) { return f.f.ReadAt(p, off) }

func (f *faultFile) WriteAt(p []byte, off int64) (int, error) {
	if err := f.hf.next(&f.hf.write); err != nil {
		return 0, err
	}
	return f.f.WriteAt(p, off)
}

func (f *faultFile) Close() error {
	err := f.f.Close()
	f.hf.mu.Lock()
	f.hf.closes++
	boom := f.hf.panicClose
	f.hf.panicClose = false
	f.hf.mu.Unlock()
	if boom {
		panic("test: panic closing the held-key log")
	}
	if cerr := f.hf.next(&f.hf.close); cerr != nil {
		return cerr
	}
	return err
}

// HeldLog is an open held-key log, for the tests of its writes.
type HeldLog struct{ h *heldLog }

// OpenHeldLog opens the held-key log at path, of the zero HeldIdentity.
func OpenHeldLog(path string) (*HeldLog, error) {
	h, _, _, err := openHeldLog(path, HeldIdentity{})
	if h == nil {
		return nil, err
	}
	return &HeldLog{h: h}, err
}

// Mark is the ledger's write before a Send.
func (l *HeldLog) Mark(events []KeyEvent) error { return l.h.mark(events) }

// Settle is the ledger's write after a Send.
func (l *HeldLog) Settle(down, sent []KeyEvent) error { return l.h.settle(down, sent) }

// Finish finishes the log with down left down.
func (l *HeldLog) Finish(down []KeyEvent) error { return l.h.finish(down) }

// SetInputMark sets the mark that Loop.run would set on a for a command
// (DMN-15).
func SetInputMark(a *Arbiter, m *InputMark) { a.mark = m }
