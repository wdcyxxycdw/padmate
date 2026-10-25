package winsession

import (
	"fmt"
	"log/slog"
	"strings"
)

// Reason is a set of reasons the system suspends the mapping for, one bit
// each. The zero Reason is none: the mapping is not suspended.
type Reason uint8

const (
	// Lock is the workstation locked (WTS_SESSION_LOCK).
	Lock Reason = 1 << iota
	// Disconnect is the session disconnected from its console or remote
	// client (WTS_*_DISCONNECT), or not the active one.
	Disconnect
	// Desktop is the input desktop not being this thread's: the secure
	// desktop of UAC, Ctrl+Alt+Del or a secure screen saver.
	Desktop
	// Sleep is the machine going to sleep (PBT_APMSUSPEND) until it wakes.
	Sleep
	// End is the session ending: sign-out, shutdown or a Restart Manager
	// close (WM_QUERYENDSESSION, WM_ENDSESSION).
	End

	// AllReasons is every Reason bit; the others are ignored.
	AllReasons = Lock | Disconnect | Desktop | Sleep | End
)

// reasonNames are the names of the Reason bits, in bit order.
var reasonNames = [...]string{"lock", "disconnect", "desktop", "sleep", "end"}

// Names returns the names of the reasons in r, in bit order: lock,
// disconnect, desktop, sleep, end. Bits outside AllReasons have no name
// and are left out; none gives an empty slice.
func (r Reason) Names() []string {
	names := make([]string, 0, len(reasonNames))
	for i, name := range reasonNames {
		if r&(1<<i) != 0 {
			names = append(names, name)
		}
	}
	return names
}

// String returns the names of r joined with ",", such as "lock,desktop",
// or "none". Bits outside AllReasons follow in hexadecimal.
func (r Reason) String() string {
	names := r.Names()
	if rest := r &^ AllReasons; rest != 0 {
		names = append(names, fmt.Sprintf("%#x", uint8(rest)))
	}
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ",")
}

// MarshalText returns String, so that r is a string in JSON.
func (r Reason) MarshalText() ([]byte, error) { return []byte(r.String()), nil }

// LogValue returns String as a slog string.
func (r Reason) LogValue() slog.Value { return slog.StringValue(r.String()) }
