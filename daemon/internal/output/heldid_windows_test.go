//go:build windows

package output

import (
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// TestSystemHeldIdentity: the logon session of the process is the same on
// every call, to the bit, as nothing of it comes from the clock; the
// session is ProcessIdToSessionId's; the logon session is the token's own,
// or its linked token's when it is the elevated one of a split admin
// token; and the logon time is LsaGetLogonSessionData's for that logon
// session. The LogonId and the Sid of what LSA returns check the layout of
// securityLogonSessionData up to LogonTime.
func TestSystemHeldIdentity(t *testing.T) {
	a, err := SystemHeldIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := SystemHeldIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if a != b || !a.same(b) {
		t.Errorf("SystemHeldIdentity = %v, then %v; want the same", a, b)
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		t.Fatal(err)
	}
	if a.Session != session {
		t.Errorf("Session = %d, want %d", a.Session, session)
	}
	token := windows.GetCurrentProcessToken()
	var kind, n uint32
	if err := windows.GetTokenInformation(token, windows.TokenElevationType, (*byte)(unsafe.Pointer(&kind)), 4, &n); err != nil {
		t.Fatal(err)
	}
	if kind == tokenElevationTypeFull {
		linked, err := token.GetLinkedToken()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = linked.Close() }()
		token = linked
	}
	luid, err := authenticationID(token)
	if err != nil {
		t.Fatal(err)
	}
	if a.Logon != luidValue(luid) || a.Logon == 0 {
		t.Errorf("Logon = %#x, want %#x (elevation type %d), not 0", a.Logon, luidValue(luid), kind)
	}

	var data *securityLogonSessionData
	if err := lsaGetLogonSessionData(&luid, &data); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lsaFreeReturnBuffer(data) }()
	user, err := token.GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	if data.LogonID != luid || data.Sid == nil || !data.Sid.Equals(user.User.Sid) {
		t.Errorf("LSA's logon session data has LogonId %+v and Sid %v, want %+v and %v", data.LogonID, data.Sid, luid, user.User.Sid)
	}
	if end := unsafe.Offsetof(data.LogonTime) + unsafe.Sizeof(data.LogonTime); uintptr(data.Size) < end {
		t.Errorf("LSA's logon session data is %d bytes, want at least %d", data.Size, end)
	}
	if a.LogonTime != data.LogonTime {
		t.Errorf("LogonTime = %d, want LSA's %d", a.LogonTime, data.LogonTime)
	}
	now := time.Now()
	logon := time.Unix(0, 0).Add(time.Duration(a.LogonTime-116_444_736_000_000_000) * 100)
	if a.LogonTime <= 0 || logon.After(now) {
		t.Errorf("LogonTime = %d (%v), want a time before now (%v)", a.LogonTime, logon, now)
	}
	t.Logf("%v (elevation type %d); boot about %v by the clock and the tick count", a, kind,
		now.Add(-windows.DurationSinceBoot()).UTC().Format(time.RFC3339))
}
