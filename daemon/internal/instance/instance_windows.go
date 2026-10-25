//go:build windows

package instance

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Lease keeps the named mutex alive until Close or process termination.
// Exclusivity comes from atomic creation, not thread-bound mutex ownership:
// no Go goroutine needs to remain on the creating OS thread.
type Lease struct {
	h    windows.Handle
	once sync.Once
	err  error
}

func identityName(sid string, session uint32) string {
	return fmt.Sprintf(`Local\padmate.instance.%s.%d`, sid, session)
}

// SystemName isolates users and interactive sessions, including when the
// same user runs one process elevated and another unelevated.
func SystemName() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("instance: process token user: %w", err)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		return "", fmt.Errorf("instance: process session: %w", err)
	}
	return identityName(user.User.Sid.String(), session), nil
}

// Acquire atomically creates a lease. An empty name uses SystemName; tests
// pass their own random names so that they never block a real padmated.
// An existing mutex is rejected immediately, without waiting or retaining
// its handle. The process token's user gets MUTEX_ALL_ACCESS and a Medium
// no-write-up label permits that user's unelevated and elevated processes
// to detect one another, even when the elevated process created it first.
func Acquire(name string) (*Lease, error) {
	if name == "" {
		var err error
		name, err = SystemName()
		if err != nil {
			return nil, err
		}
	}
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("instance: mutex name: %w", err)
	}
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("instance: process token user: %w", err)
	}
	sd, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:(A;;%#x;;;%s)S:(ML;;NW;;;ME)", windows.MUTEX_ALL_ACCESS, user.User.Sid))
	if err != nil {
		return nil, fmt.Errorf("instance: mutex security: %w", err)
	}
	sa := &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	h, err := windows.CreateMutex(sa, false, p)
	if h == 0 {
		return nil, fmt.Errorf("instance: create mutex: %w", err)
	}
	if errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, errors.Join(ErrAlreadyRunning, windows.CloseHandle(h))
	}
	return &Lease{h: h}, nil
}

// Close releases the lease after padmated's ordered cleanup. It is safe to
// call more than once; process termination also closes the kernel handle.
func (l *Lease) Close() error {
	l.once.Do(func() { l.err = windows.CloseHandle(l.h) })
	return l.err
}
