package output

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
)

// Sender injects keyboard and mouse events (a mouse event is one with
// KeyEvent.Mouse set: a wheel notch, or a button down or up) and the Unicode
// events of a text (KeyEvent.Unicode: one UTF-16 code unit typed by
// KEYEVENTF_UNICODE, OUT-21). A Sender that cannot type a Unicode event must
// say so in its count, as for any event it did not inject.
type Sender interface {
	// Send injects events in order as one batch, which other input cannot
	// interleave with, and returns how many it injected. The injected
	// events are always a prefix of events; when fewer than len(events)
	// were injected, the error says why. An event that injects nothing
	// (KeyEvent.Silent, the release of a wheel key) counts as injected when
	// every event before it was, and a batch of such events alone injects
	// nothing and returns len(events). An error with the full count is a
	// warning: Tap carries on as if it were nil and returns it at the end.
	// Send must not retain events after it returns.
	Send(events []KeyEvent) (int, error)
}

// Tap presses and releases st through s. With hold 0 the presses and the
// releases go out in a single Send call, so real input cannot interleave
// with them. With hold > 0 Tap sends the presses, waits for hold or until
// ctx is done, then sends the releases.
//
// Tap never leaves a key held (CLAUDE.md invariant 1):
//   - If Send injects only some of the presses, Tap releases exactly the
//     keys that went down, in reverse order, and returns an error.
//   - If the releases are injected only in part, Tap sends the rest once
//     more and returns an error, which names the keys that may be held if
//     the retry fails too.
//   - If ctx is done during the hold, Tap still sends the releases and
//     returns ctx.Err().
//
// If ctx is already done Tap sends nothing and returns ctx.Err().
func Tap(ctx context.Context, s Sender, st Stroke, hold time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(st.Down) == 0 && len(st.Up) == 0 {
		return nil
	}
	if hold <= 0 {
		batch := make([]KeyEvent, 0, len(st.Down)+len(st.Up))
		batch = append(batch, st.Down...)
		batch = append(batch, st.Up...)
		n, err := send(s, batch)
		if n == len(batch) {
			return err
		}
		err = partial(n, len(batch), err)
		if n < len(st.Down) {
			return errors.Join(err, releaseDowns(s, st.Down[:n]))
		}
		return errors.Join(err, retry(s, st.Up[n-len(st.Down):]))
	}

	n, downErr := send(s, st.Down)
	if n < len(st.Down) {
		return errors.Join(partial(n, len(st.Down), downErr), releaseDowns(s, st.Down[:n]))
	}
	var canceled error
	t := time.NewTimer(hold)
	select {
	case <-t.C:
	case <-ctx.Done():
		t.Stop()
		canceled = ctx.Err()
	}
	n, err := send(s, st.Up)
	if n == len(st.Up) {
		return errors.Join(downErr, canceled, err)
	}
	return errors.Join(downErr, canceled, partial(n, len(st.Up), err), retry(s, st.Up[n:]))
}

// send calls s.Send, clamping its count to [0, len(events)] and making
// sure a short count comes with an error.
func send(s Sender, events []KeyEvent) (int, error) {
	if len(events) == 0 {
		return 0, nil
	}
	n, err := s.Send(events)
	n = min(max(n, 0), len(events))
	if n < len(events) && err == nil {
		err = errors.New("output: sender injected fewer events than given")
	}
	return n, err
}

// releaseDowns releases the keys of downs, the presses that went out, in
// reverse order. It retries a partial release once.
func releaseDowns(s Sender, downs []KeyEvent) error {
	if len(downs) == 0 {
		return nil
	}
	ups := make([]KeyEvent, len(downs))
	for i, d := range downs {
		ups[len(downs)-1-i] = d.release()
	}
	n, err := send(s, ups)
	if n == len(ups) {
		return err
	}
	return errors.Join(partial(n, len(ups), err), retry(s, ups[n:]))
}

// retry sends releases that a partial batch left out, once more. When they
// all go out it returns the sender's warning, if any; otherwise an error
// naming the keys that may still be held.
func retry(s Sender, ups []KeyEvent) error {
	n, err := send(s, ups)
	if n == len(ups) {
		return err
	}
	return fmt.Errorf("output: releasing %s failed twice, the keys may be held: %w", names(ups[n:]), err)
}

// partial describes a batch that was injected only in part.
func partial(n, total int, err error) error {
	return fmt.Errorf("output: injected %d of %d key events: %w", n, total, err)
}

// name names the key of e in error messages: its KeySpec name, or else the
// one code that identifies it. Unlike String it leaves out the direction
// and the other codes, because a message lists several keys in a row.
func (e KeyEvent) name() string {
	switch {
	case e.Key.IsValid():
		return e.Key.String()
	case e.Mouse != keys.MouseNone:
		return e.Mouse.String()
	case e.Unicode:
		return e.ID().String()
	case e.ByVK:
		return fmt.Sprintf("vk=0x%02X", e.VK)
	case e.Ext:
		return fmt.Sprintf("sc=E0 %02X", e.Scan)
	default:
		return fmt.Sprintf("sc=0x%02X", e.Scan)
	}
}
