// Package emergency provides the out-of-band release signal of DMN-20
// (ADR-0028): padmatectl release-all signals a named event of the
// interactive session, which a running padmated waits on. It does not
// depend on IPC.
//
// Listen creates the event at once, so that a Trigger after Listen
// returns reaches the Listener even before Serve runs; Serve then waits on
// it. The event is auto-reset: signals before a wait coalesce into one,
// and one signal wakes one waiter only.
package emergency

import "errors"

// Name is the per-interactive-session event of padmated and padmatectl.
// Tests use a name of their own.
const Name = `Local\padmate.panic`

// ErrUnsupported reports that the named event is unavailable: it is
// Windows only.
var ErrUnsupported = errors.New("emergency release is only supported on Windows")
