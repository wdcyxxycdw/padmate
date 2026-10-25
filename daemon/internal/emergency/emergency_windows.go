//go:build windows

package emergency

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// waitInterval is how long Serve waits on the event at a time, so that it
// sees the end of its context and Close.
const waitInterval = 100 * time.Millisecond

// eventAllAccess is EVENT_ALL_ACCESS, what the DACL of the event grants
// the user.
const eventAllAccess = windows.EVENT_ALL_ACCESS

// Listener holds the named event open for Serve.
type Listener struct {
	name string

	mu      sync.Mutex
	h       windows.Handle
	serving bool
	closed  bool
}

// Listen creates the auto-reset event name, or opens it when it exists,
// and returns the Listener that holds it. A Trigger after Listen returns
// signals it, even before Serve runs. A new event gets the security
// descriptor of eventSecurity.
func Listen(name string) (*Listener, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("emergency: event name %q: %w", name, err)
	}
	sa, err := eventSecurity()
	if err != nil {
		return nil, fmt.Errorf("emergency: security of %s: %w", name, err)
	}
	h, err := windows.CreateEvent(sa, 0, 0, p)
	switch {
	case h != 0:
		// x/sys returns ERROR_ALREADY_EXISTS as an error with a valid
		// handle to the existing event: keep the handle.
	case errors.Is(err, windows.ERROR_ACCESS_DENIED):
		// The event exists with a security descriptor that denies
		// EVENT_ALL_ACCESS, as CreateEvent asks: waiting is enough.
		h, err = windows.OpenEvent(windows.SYNCHRONIZE, false, p)
		if err != nil {
			return nil, fmt.Errorf("emergency: open %s: %w", name, err)
		}
	default:
		return nil, fmt.Errorf("emergency: create %s: %w", name, err)
	}
	return &Listener{name: name, h: h}, nil
}

// eventSecurity returns the security attributes of the event: a DACL
// that grants EVENT_ALL_ACCESS to the user of the process token only, and
// a Medium mandatory label with no-write-up. The default DACL of an
// elevated token grants the Administrators group, which is deny-only in
// the same user's unelevated token, so that padmatectl release-all could
// not open the event of an elevated padmated to signal it (by UAC's
// documented defaults; not measured). The user's SID is in both tokens,
// and the Medium label lets medium-integrity processes signal the event
// and keeps lower ones out. It is padmate's own kernel object: no UIPI
// is bypassed.
func eventSecurity() (*windows.SecurityAttributes, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("the process token's user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:(A;;%#x;;;%s)S:(ML;;NW;;;ME)", eventAllAccess, user.User.Sid))
	if err != nil {
		return nil, err
	}
	return &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}, nil
}

// Serve waits on the event and calls on once per signal, on its own
// goroutine, until ctx is done or Close is called; it sees either within
// waitInterval. It closes the event when it returns, and returns nil then,
// or the error of a failed wait. It may be called once.
func (l *Listener) Serve(ctx context.Context, on func()) error {
	if on == nil {
		return errors.New("emergency: nil callback")
	}
	l.mu.Lock()
	if l.closed || l.serving {
		l.mu.Unlock()
		return fmt.Errorf("emergency: %s is closed or served already", l.name)
	}
	l.serving = true
	h := l.h
	l.mu.Unlock()
	defer func() {
		l.mu.Lock()
		l.closed = true
		l.mu.Unlock()
		_ = windows.CloseHandle(h)
	}()
	for {
		l.mu.Lock()
		closed := l.closed
		l.mu.Unlock()
		if closed || ctx.Err() != nil {
			return nil
		}
		result, err := windows.WaitForSingleObject(h, uint32(waitInterval.Milliseconds()))
		switch {
		case err != nil:
			return fmt.Errorf("emergency: wait %s: %w", l.name, err)
		case result == windows.WAIT_OBJECT_0:
			on()
		case result == uint32(windows.WAIT_TIMEOUT):
		default:
			return fmt.Errorf("emergency: wait %s returned %#x", l.name, result)
		}
	}
}

// Close closes the event of a Listener that Serve is not running on, and
// makes Serve return otherwise; Serve closes it then. It may be called more
// than once.
func (l *Listener) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	if l.serving {
		return nil
	}
	return windows.CloseHandle(l.h)
}

// Trigger signals the event name, which a padmated in this interactive
// session listens on. It fails when no process holds the event open.
func Trigger(name string) error {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("emergency: event name %q: %w", name, err)
	}
	h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, p)
	if err != nil {
		return fmt.Errorf("emergency: open %s: %w", name, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.SetEvent(h); err != nil {
		return fmt.Errorf("emergency: signal %s: %w", name, err)
	}
	return nil
}
