package winsession_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// allSignals are the valid signals, in order.
var allSignals = []ws.Signal{
	ws.SigInit, ws.SigLock, ws.SigUnlock, ws.SigDisconnect, ws.SigConnect, ws.SigSleep, ws.SigWake,
	ws.SigQueryEnd, ws.SigEndCanceled, ws.SigEnd, ws.SigDesktopSwitch, ws.SigTick,
}

func TestSignalString(t *testing.T) {
	names := []string{
		"init", "lock", "unlock", "disconnect", "connect", "sleep", "wake",
		"queryend", "endcanceled", "end", "desktop", "tick",
	}
	if len(names) != len(allSignals) {
		t.Fatalf("%d names for %d signals", len(names), len(allSignals))
	}
	for i, s := range allSignals {
		if got := s.String(); got != names[i] {
			t.Errorf("Signal(%d).String() = %q, want %q", uint8(s), got, names[i])
		}
		for _, name := range []string{names[i], "  " + names[i]} {
			got, ok := ws.ParseSignal(name)
			if want := name == names[i]; ok != want || (ok && got != s) {
				t.Errorf("ParseSignal(%q) = %v, %v", name, got, ok)
			}
		}
		if got, ok := ws.ParseSignal(strings.ToUpper(names[i])); !ok || got != s {
			t.Errorf("ParseSignal(%q) = %v, %v", strings.ToUpper(names[i]), got, ok)
		}
	}
	if got, ok := ws.ParseSignal("UnLock"); !ok || got != ws.SigUnlock {
		t.Errorf("ParseSignal(UnLock) = %v, %v; want unlock, true", got, ok)
	}
	for _, name := range []string{"", "Signal(0)", "locked", "desktopswitch"} {
		if got, ok := ws.ParseSignal(name); ok {
			t.Errorf("ParseSignal(%q) = %v, true; want false", name, got)
		}
	}
	for _, s := range []ws.Signal{0, ws.SigTick + 1, 255} {
		if got, want := s.String(), fmt.Sprintf("Signal(%d)", uint8(s)); got != want {
			t.Errorf("Signal(%d).String() = %q, want %q", uint8(s), got, want)
		}
	}
	b, err := json.Marshal([]ws.Signal{ws.SigDesktopSwitch, 0})
	if err != nil || string(b) != `["desktop","Signal(0)"]` {
		t.Errorf("json.Marshal = %s, %v", b, err)
	}
}

func TestSignalQueries(t *testing.T) {
	for _, tc := range []struct {
		s               ws.Signal
		clears, queries bool
	}{
		{ws.SigInit, false, true},
		{ws.SigLock, false, false},
		{ws.SigUnlock, true, true},
		{ws.SigDisconnect, false, false},
		{ws.SigConnect, true, true},
		{ws.SigSleep, false, false},
		{ws.SigWake, true, true},
		{ws.SigQueryEnd, false, false},
		{ws.SigEndCanceled, true, true},
		{ws.SigEnd, false, false},
		{ws.SigDesktopSwitch, false, true},
		{ws.SigTick, false, true},
		{0, false, false},
		{ws.SigTick + 1, false, false},
	} {
		if got := tc.s.Clears(); got != tc.clears {
			t.Errorf("%v.Clears() = %v, want %v", tc.s, got, tc.clears)
		}
		if got := tc.s.Queries(); got != tc.queries {
			t.Errorf("%v.Queries() = %v, want %v", tc.s, got, tc.queries)
		}
	}
}

func TestFromMessage(t *testing.T) {
	type row struct {
		msg    uint32
		wParam uintptr
		want   ws.Signal // 0: ignored
	}
	rows := []row{
		{ws.WMWTSSessionChange, ws.WTSConsoleConnect, ws.SigConnect},
		{ws.WMWTSSessionChange, ws.WTSRemoteConnect, ws.SigConnect},
		{ws.WMWTSSessionChange, ws.WTSConsoleDisconnect, ws.SigDisconnect},
		{ws.WMWTSSessionChange, ws.WTSRemoteDisconnect, ws.SigDisconnect},
		{ws.WMWTSSessionChange, ws.WTSSessionLock, ws.SigLock},
		{ws.WMWTSSessionChange, ws.WTSSessionUnlock, ws.SigUnlock},
		{ws.WMWTSSessionChange, ws.WTSSessionLogon, 0},
		{ws.WMWTSSessionChange, ws.WTSSessionLogoff, 0},
		{ws.WMWTSSessionChange, ws.WTSRemoteControl, 0},
		{ws.WMWTSSessionChange, ws.WTSSessionCreate, 0},
		{ws.WMWTSSessionChange, ws.WTSSessionTerminate, 0},
		{ws.WMWTSSessionChange, 0, 0},
		{ws.WMWTSSessionChange, 0x42, 0},
		{ws.WMWTSSessionChange, 0x100 | ws.WTSSessionLock, 0},
		{ws.WMPowerBroadcast, ws.PBTAPMSuspend, ws.SigSleep},
		{ws.WMPowerBroadcast, ws.PBTAPMResumeCritical, ws.SigWake},
		{ws.WMPowerBroadcast, ws.PBTAPMResumeSuspend, ws.SigWake},
		{ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, ws.SigWake},
		{ws.WMPowerBroadcast, ws.PBTAPMPowerStatusChange, 0},
		{ws.WMPowerBroadcast, ws.PBTPowerSettingChange, 0},
		{ws.WMPowerBroadcast, 0, 0},
		{ws.WMPowerBroadcast, 0x5, 0},
		{ws.WMQueryEndSession, 0, ws.SigQueryEnd},
		{ws.WMQueryEndSession, 1, ws.SigQueryEnd},
		{ws.WMEndSession, 0, ws.SigEndCanceled},
		{ws.WMEndSession, 1, ws.SigEnd},
		{ws.WMEndSession, 0x100, ws.SigEnd},
		{0, ws.WTSSessionLock, 0},
		{0x0010, 0, 0}, // WM_CLOSE
		{0x0113, 1, 0}, // WM_TIMER
		{0x8001, 0, 0}, // WM_APP+1
		{ws.WMWTSSessionChange + 1, ws.WTSSessionLock, 0},
	}
	for _, r := range rows {
		for _, lParam := range []uintptr{0, 1, ws.EndSessionLogoff, ^uintptr(0)} {
			got, ok := ws.FromMessage(r.msg, r.wParam, lParam)
			if ok != (r.want != 0) || got != r.want {
				t.Errorf("FromMessage(%#x, %#x, %#x) = %v, %v; want %v, %v",
					r.msg, r.wParam, lParam, got, ok, r.want, r.want != 0)
			}
		}
	}
}

func TestEndKind(t *testing.T) {
	for _, tc := range []struct {
		lParam uintptr
		want   string
	}{
		{0, "shutdown"},
		{ws.EndSessionCloseApp, "closeapp"},
		{ws.EndSessionCritical, "critical"},
		{ws.EndSessionLogoff, "logoff"},
		{ws.EndSessionLogoff | ws.EndSessionCritical, "critical,logoff"},
		{ws.EndSessionLogoff | ws.EndSessionCloseApp, "closeapp,logoff"},
		{ws.EndSessionCloseApp | ws.EndSessionCritical | ws.EndSessionLogoff, "closeapp,critical,logoff"},
		{0x2, "shutdown"},
		{0x2 | ws.EndSessionLogoff, "logoff"},
	} {
		if got := ws.EndKind(tc.lParam); got != tc.want {
			t.Errorf("EndKind(%#x) = %q, want %q", tc.lParam, got, tc.want)
		}
	}
}

// FuzzFromMessage checks that FromMessage never panics, that it accepts
// only the four session messages, that the signal never depends on lParam,
// that WM_ENDSESSION's depends only on whether wParam is zero, and that
// every accepted signal is valid.
func FuzzFromMessage(f *testing.F) {
	for _, seed := range []struct {
		msg            uint32
		wParam, lParam uint64
	}{
		{ws.WMWTSSessionChange, ws.WTSSessionLock, 0},
		{ws.WMWTSSessionChange, ws.WTSSessionUnlock, 0x1},
		{ws.WMWTSSessionChange, ws.WTSSessionLogon, 0},
		{ws.WMPowerBroadcast, ws.PBTAPMSuspend, 0},
		{ws.WMPowerBroadcast, ws.PBTPowerSettingChange, 0x1234},
		{ws.WMQueryEndSession, 0, ws.EndSessionLogoff},
		{ws.WMEndSession, 1, ws.EndSessionCritical},
		{ws.WMEndSession, 0, 0},
		{0x0010, 0, 0},
		{0xffffffff, 1 << 63, 1 << 63},
	} {
		f.Add(seed.msg, seed.wParam, seed.lParam)
	}
	f.Fuzz(func(t *testing.T, msg uint32, wParam, lParam uint64) {
		w, l := uintptr(wParam), uintptr(lParam)
		sig, ok := ws.FromMessage(msg, w, l)
		if again, ok2 := ws.FromMessage(msg, w, 0); again != sig || ok2 != ok {
			t.Fatalf("FromMessage(%#x, %#x, %#x) = %v, %v but with lParam 0 %v, %v", msg, w, l, sig, ok, again, ok2)
		}
		if !ok {
			if sig != 0 {
				t.Fatalf("FromMessage(%#x, %#x) = %v, false; want 0", msg, w, sig)
			}
			switch msg {
			case ws.WMQueryEndSession, ws.WMEndSession:
				t.Fatalf("FromMessage(%#x, %#x) ignored a session end message", msg, w)
			}
			return
		}
		if _, valid := ws.ParseSignal(sig.String()); !valid || sig == ws.SigInit || sig == ws.SigTick || sig == ws.SigDesktopSwitch {
			t.Fatalf("FromMessage(%#x, %#x) = %v, not a message signal", msg, w, sig)
		}
		switch msg {
		case ws.WMQueryEndSession:
			if sig != ws.SigQueryEnd {
				t.Fatalf("WM_QUERYENDSESSION(%#x) = %v", w, sig)
			}
		case ws.WMEndSession:
			if want := map[bool]ws.Signal{false: ws.SigEndCanceled, true: ws.SigEnd}[w != 0]; sig != want {
				t.Fatalf("WM_ENDSESSION(%#x) = %v, want %v", w, sig, want)
			}
		case ws.WMWTSSessionChange, ws.WMPowerBroadcast:
			if w > 0x20 {
				t.Fatalf("FromMessage(%#x, %#x) = %v for an unknown code", msg, w, sig)
			}
		default:
			t.Fatalf("FromMessage(%#x, %#x) = %v for another message", msg, w, sig)
		}
	})
}
