package winsession

import (
	"encoding/binary"
	"fmt"
)

// WTSINFOEXW, as WTSQuerySessionInformationW returns it for
// WTSSessionInfoEx: a DWORD Level, then a union whose only member,
// WTSINFOEX_LEVEL1_W, holds a LARGE_INTEGER and so is 8-byte aligned. The
// fields padmate reads are therefore at these offsets.
const (
	infoExLevel = 0  // DWORD Level, which must be 1
	infoExID    = 8  // ULONG SessionId
	infoExState = 12 // WTS_CONNECTSTATE_CLASS SessionState
	infoExFlags = 16 // LONG SessionFlags
	infoExMin   = 20 // bytes up to and including SessionFlags
)

// SessionState and SessionFlags values. SessionFlags may also be
// WTS_SESSIONSTATE_UNKNOWN (-1).
const (
	wtsActive        = 0 // WTSActive
	wtsSessionLock   = 0 // WTS_SESSIONSTATE_LOCK
	wtsSessionUnlock = 1 // WTS_SESSIONSTATE_UNLOCK
)

// parseInfoEx reads a WTSINFOEXW. SessionFlags 0 means locked and 1
// unlocked; anything else, WTS_SESSIONSTATE_UNKNOWN (-1) included, leaves
// the lock unknown. On Windows 7 and Windows Server 2008 R2 the two known
// values mean the opposite; padmate does not run there.
func parseInfoEx(buf []byte) (SessionInfo, error) {
	if len(buf) < infoExMin {
		return SessionInfo{}, fmt.Errorf("winsession: WTSINFOEX is %d bytes, want at least %d", len(buf), infoExMin)
	}
	le := binary.LittleEndian
	if level := le.Uint32(buf[infoExLevel:]); level != 1 {
		return SessionInfo{}, fmt.Errorf("winsession: WTSINFOEX level %d, want 1", level)
	}
	info := SessionInfo{
		ID:     le.Uint32(buf[infoExID:]),
		Active: le.Uint32(buf[infoExState:]) == wtsActive,
	}
	switch int32(le.Uint32(buf[infoExFlags:])) {
	case wtsSessionLock:
		info.LockKnown, info.Locked = true, true
	case wtsSessionUnlock:
		info.LockKnown = true
	}
	return info, nil
}
