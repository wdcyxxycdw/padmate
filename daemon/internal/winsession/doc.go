// Package winsession watches the Windows session for what must suspend the
// mapping (DMN-09): the workstation locked, the session disconnected, the
// input desktop not ours (the secure desktop of UAC or Ctrl+Alt+Del), the
// machine going to sleep and the session ending.
//
// The pure part builds and tests on every OS: Reason, the set of reasons;
// Signal and FromMessage, which turn the window messages of the session
// thread into signals; EndKind; and Tracker, the state machine that turns
// signals into reasons, correcting them with a Querier. Signals that set a
// reason apply at once and query nothing, so the release waits for no
// RPC; signals that clear one are rechecked against the system, whose
// answer wins, and a timer queries it too, so that a missed or reordered
// message is corrected within seconds. Sleep and End, which no query can
// see, go stale after a minute of awake time.
//
// On Windows, Start runs the session thread T-session and returns its
// Watcher: a hidden top-level window (not a message-only one, which misses
// broadcasts) for the session, power and end-of-session messages, a
// WinEvent hook for desktop switches and a Poll timer. It queries only in
// posted messages, never inside one another thread sent and waits for;
// the sleep and end-of-session handlers wait for Config.Barrier before
// they return to the system. The window class is registered on every
// Start, since Windows destroys a process's classes when its last GUI
// thread exits. When the thread ends by itself, a fallback clears Sleep
// and End, which no query sees, keeps the other reasons, and polls the
// system every Poll until Close. Elsewhere Start
// returns errors.ErrUnsupported. The package reports reasons only:
// padmated wires them to the supervisor and the output.
package winsession
