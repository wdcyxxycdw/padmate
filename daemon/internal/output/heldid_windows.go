//go:build windows

package output

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tokenElevationTypeFull is TOKEN_ELEVATION_TYPE's TokenElevationTypeFull:
// the elevated token of a split admin token.
const tokenElevationTypeFull = 2

// tokenStatistics is TOKEN_STATISTICS, which x/sys does not have.
type tokenStatistics struct {
	TokenID            windows.LUID
	AuthenticationID   windows.LUID
	ExpirationTime     int64
	TokenType          uint32
	ImpersonationLevel uint32
	DynamicCharged     uint32
	DynamicAvailable   uint32
	GroupCount         uint32
	PrivilegeCount     uint32
	ModifiedID         windows.LUID
}

// securityLogonSessionData is SECURITY_LOGON_SESSION_DATA (ntsecapi.h) up
// to LogonTime, which x/sys does not have. LSA returns the whole of it;
// Size is how long that is. TestSystemHeldIdentity checks the layout by
// LogonID and Sid.
type securityLogonSessionData struct {
	Size                  uint32
	LogonID               windows.LUID
	UserName              windows.NTUnicodeString
	LogonDomain           windows.NTUnicodeString
	AuthenticationPackage windows.NTUnicodeString
	LogonType             uint32
	Session               uint32
	Sid                   *windows.SID
	LogonTime             int64
}

// SystemHeldIdentity returns the logon session of this process, the
// default LoopOptions.Identity. The logon session is that of the process
// token, or of its linked token when the process runs elevated with a
// split admin token, whose elevated token is in a logon session of its own
// (UAC; not measured); when the linked token cannot be read, it is the
// elevated token's own. Its logon time is LsaGetLogonSessionData's, which
// a process may read for its own user's logon sessions. Nothing of it is
// read from the clock.
func SystemHeldIdentity() (HeldIdentity, error) {
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return HeldIdentity{}, fmt.Errorf("output: the process's session: %w", err)
	}
	logon, err := logonSession(windows.GetCurrentProcessToken())
	if err != nil {
		return HeldIdentity{}, err
	}
	at, err := logonTime(logon)
	if err != nil {
		return HeldIdentity{}, err
	}
	return HeldIdentity{Session: session, Logon: luidValue(logon), LogonTime: at}, nil
}

// logonSession returns the logon session of t, or of its linked token
// when t is the elevated token of a split admin token.
func logonSession(t windows.Token) (windows.LUID, error) {
	var kind, n uint32
	err := windows.GetTokenInformation(t, windows.TokenElevationType, (*byte)(unsafe.Pointer(&kind)), uint32(unsafe.Sizeof(kind)), &n)
	if err == nil && kind == tokenElevationTypeFull {
		if linked, err := t.GetLinkedToken(); err == nil {
			id, err := authenticationID(linked)
			_ = linked.Close()
			if err == nil {
				return id, nil
			}
		}
	}
	return authenticationID(t)
}

// authenticationID returns TokenStatistics.AuthenticationId of t.
func authenticationID(t windows.Token) (windows.LUID, error) {
	var st tokenStatistics
	var n uint32
	if err := windows.GetTokenInformation(t, windows.TokenStatistics, (*byte)(unsafe.Pointer(&st)), uint32(unsafe.Sizeof(st)), &n); err != nil {
		return windows.LUID{}, fmt.Errorf("output: the token's logon session: %w", err)
	}
	return st.AuthenticationID, nil
}

// luidValue is id as HeldIdentity.Logon has it, HighPart in the high 32
// bits.
func luidValue(id windows.LUID) uint64 {
	return uint64(uint32(id.HighPart))<<32 | uint64(id.LowPart)
}

// logonTime returns the LogonTime of the logon session id.
func logonTime(id windows.LUID) (int64, error) {
	var data *securityLogonSessionData
	if err := lsaGetLogonSessionData(&id, &data); err != nil {
		return 0, fmt.Errorf("output: the logon session's logon time: %w", err)
	}
	if data == nil {
		return 0, errors.New("output: the logon session's logon time: LSA returned no data")
	}
	defer func() { _ = lsaFreeReturnBuffer(data) }()
	if end := unsafe.Offsetof(data.LogonTime) + unsafe.Sizeof(data.LogonTime); uintptr(data.Size) < end {
		return 0, fmt.Errorf("output: the logon session's logon time: LSA returned %d bytes, want at least %d", data.Size, end)
	}
	return data.LogonTime, nil
}
