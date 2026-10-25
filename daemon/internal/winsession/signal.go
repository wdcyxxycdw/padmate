package winsession

import (
	"fmt"
	"strings"
)

// Signal is one thing the session thread learned, from a window message,
// its timer or the start. Tracker.Step turns a sequence of them into the
// Reason set.
type Signal uint8

// The zero Signal is invalid; Tracker.Step ignores it and any value past
// SigTick.
const (
	// SigInit is the start: the reasons are read from the system.
	SigInit Signal = iota + 1
	// SigLock is WTS_SESSION_LOCK.
	SigLock
	// SigUnlock is WTS_SESSION_UNLOCK.
	SigUnlock
	// SigDisconnect is WTS_CONSOLE_DISCONNECT or WTS_REMOTE_DISCONNECT.
	SigDisconnect
	// SigConnect is WTS_CONSOLE_CONNECT or WTS_REMOTE_CONNECT.
	SigConnect
	// SigSleep is PBT_APMSUSPEND.
	SigSleep
	// SigWake is PBT_APMRESUMECRITICAL, PBT_APMRESUMESUSPEND or
	// PBT_APMRESUMEAUTOMATIC.
	SigWake
	// SigQueryEnd is WM_QUERYENDSESSION: the session may end.
	SigQueryEnd
	// SigEndCanceled is WM_ENDSESSION with wParam FALSE: it will not.
	SigEndCanceled
	// SigEnd is WM_ENDSESSION with wParam TRUE: it ends.
	SigEnd
	// SigDesktopSwitch is EVENT_SYSTEM_DESKTOPSWITCH: the input desktop
	// changed.
	SigDesktopSwitch
	// SigTick is the session thread's timer (Config.Poll).
	SigTick

	sigCount = iota + 1
)

// signalNames are the names of the signals, indexed by Signal. They are
// also the keywords of the supervisor's session.txt golden files.
var signalNames = [sigCount]string{
	SigInit:          "init",
	SigLock:          "lock",
	SigUnlock:        "unlock",
	SigDisconnect:    "disconnect",
	SigConnect:       "connect",
	SigSleep:         "sleep",
	SigWake:          "wake",
	SigQueryEnd:      "queryend",
	SigEndCanceled:   "endcanceled",
	SigEnd:           "end",
	SigDesktopSwitch: "desktop",
	SigTick:          "tick",
}

// String returns the signal's lowercase name, such as "unlock", or
// "Signal(N)" for an invalid one.
func (s Signal) String() string {
	if s.valid() {
		return signalNames[s]
	}
	return fmt.Sprintf("Signal(%d)", uint8(s))
}

// MarshalText returns String, so that s is a string in JSON.
func (s Signal) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// ParseSignal returns the Signal whose String is name, ignoring case.
func ParseSignal(name string) (Signal, bool) {
	for s := SigInit; s < sigCount; s++ {
		if strings.EqualFold(name, signalNames[s]) {
			return s, true
		}
	}
	return 0, false
}

func (s Signal) valid() bool { return s >= SigInit && s < sigCount }

// Clears reports a signal that clears a reason: SigUnlock, SigConnect,
// SigWake and SigEndCanceled. The session thread never applies them inside
// the message that brought them; it posts itself a recheck, which clears
// the reason and then queries the system, whose answer wins.
func (s Signal) Clears() bool {
	switch s {
	case SigUnlock, SigConnect, SigWake, SigEndCanceled:
		return true
	}
	return false
}

// Queries reports a signal whose step queries the system when it has a
// Querier: the clearing signals, SigInit, SigDesktopSwitch and SigTick.
// The signals that set a reason (SigLock, SigDisconnect, SigSleep,
// SigQueryEnd and SigEnd) never query, so the release they cause waits for
// no RPC.
func (s Signal) Queries() bool {
	switch s {
	case SigInit, SigDesktopSwitch, SigTick:
		return true
	}
	return s.Clears()
}

// Window messages FromMessage turns into signals.
const (
	WMQueryEndSession  = 0x0011 // WM_QUERYENDSESSION
	WMEndSession       = 0x0016 // WM_ENDSESSION
	WMPowerBroadcast   = 0x0218 // WM_POWERBROADCAST
	WMWTSSessionChange = 0x02B1 // WM_WTSSESSION_CHANGE
)

// WTS_* status codes, the wParam of WM_WTSSESSION_CHANGE.
const (
	WTSConsoleConnect    = 0x1 // WTS_CONSOLE_CONNECT
	WTSConsoleDisconnect = 0x2 // WTS_CONSOLE_DISCONNECT
	WTSRemoteConnect     = 0x3 // WTS_REMOTE_CONNECT
	WTSRemoteDisconnect  = 0x4 // WTS_REMOTE_DISCONNECT
	WTSSessionLogon      = 0x5 // WTS_SESSION_LOGON
	WTSSessionLogoff     = 0x6 // WTS_SESSION_LOGOFF
	WTSSessionLock       = 0x7 // WTS_SESSION_LOCK
	WTSSessionUnlock     = 0x8 // WTS_SESSION_UNLOCK
	WTSRemoteControl     = 0x9 // WTS_SESSION_REMOTE_CONTROL
	WTSSessionCreate     = 0xA // WTS_SESSION_CREATE
	WTSSessionTerminate  = 0xB // WTS_SESSION_TERMINATE
)

// PBT_* power events, the wParam of WM_POWERBROADCAST.
const (
	PBTAPMSuspend           = 0x4    // PBT_APMSUSPEND
	PBTAPMResumeCritical    = 0x6    // PBT_APMRESUMECRITICAL
	PBTAPMResumeSuspend     = 0x7    // PBT_APMRESUMESUSPEND
	PBTAPMPowerStatusChange = 0xA    // PBT_APMPOWERSTATUSCHANGE
	PBTAPMResumeAutomatic   = 0x12   // PBT_APMRESUMEAUTOMATIC
	PBTPowerSettingChange   = 0x8013 // PBT_POWERSETTINGCHANGE
)

// ENDSESSION_* flags, the lParam of WM_QUERYENDSESSION and WM_ENDSESSION.
const (
	EndSessionCloseApp = 0x00000001 // ENDSESSION_CLOSEAPP: Restart Manager
	EndSessionCritical = 0x40000000 // ENDSESSION_CRITICAL: forced
	EndSessionLogoff   = 0x80000000 // ENDSESSION_LOGOFF: sign-out
)

// FromMessage returns the Signal of a window message, or false for a
// message or code that means nothing to the session: the WTS logon,
// logoff, remote-control, create and terminate codes, the power status and
// power setting changes, unknown codes and every other message. The
// signal depends only on msg and wParam; lParam is taken so that a window
// procedure passes its arguments through unchanged.
func FromMessage(msg uint32, wParam, lParam uintptr) (Signal, bool) {
	switch msg {
	case WMWTSSessionChange:
		switch wParam {
		case WTSConsoleConnect, WTSRemoteConnect:
			return SigConnect, true
		case WTSConsoleDisconnect, WTSRemoteDisconnect:
			return SigDisconnect, true
		case WTSSessionLock:
			return SigLock, true
		case WTSSessionUnlock:
			return SigUnlock, true
		}
	case WMPowerBroadcast:
		switch wParam {
		case PBTAPMSuspend:
			return SigSleep, true
		case PBTAPMResumeCritical, PBTAPMResumeSuspend, PBTAPMResumeAutomatic:
			return SigWake, true
		}
	case WMQueryEndSession:
		return SigQueryEnd, true
	case WMEndSession:
		if wParam == 0 {
			return SigEndCanceled, true
		}
		return SigEnd, true
	}
	return 0, false
}

// EndKind names why the session ends from the lParam of WM_ENDSESSION:
// "closeapp", "critical" and "logoff" for the ENDSESSION_* flags, joined
// with "," in that order when several are set, or "shutdown" when none is.
// Other bits are ignored.
func EndKind(lParam uintptr) string {
	var kinds []string
	if lParam&EndSessionCloseApp != 0 {
		kinds = append(kinds, "closeapp")
	}
	if lParam&EndSessionCritical != 0 {
		kinds = append(kinds, "critical")
	}
	if lParam&EndSessionLogoff != 0 {
		kinds = append(kinds, "logoff")
	}
	if len(kinds) == 0 {
		return "shutdown"
	}
	return strings.Join(kinds, ",")
}
