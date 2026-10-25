package winsession_test

import (
	"encoding/binary"
	"testing"

	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// infoEx builds a WTSINFOEXW of n bytes (at least 20) with the given
// level, session ID, SessionState and SessionFlags.
func infoEx(n int, level, id, state uint32, flags int32) []byte {
	buf := make([]byte, n)
	le := binary.LittleEndian
	le.PutUint32(buf[0:], level)
	le.PutUint32(buf[4:], 0xdeadbeef) // padding before the union
	le.PutUint32(buf[8:], id)
	le.PutUint32(buf[12:], state)
	le.PutUint32(buf[16:], uint32(flags))
	return buf
}

func TestParseInfoEx(t *testing.T) {
	// sizeof(WTSINFOEXW) on amd64: the Level and its padding, then
	// WTSINFOEX_LEVEL1_W: three 32-bit fields, the WinStationName,
	// UserName and DomainName arrays (33, 21 and 18 WCHARs), padding to 8,
	// five LARGE_INTEGER times and six 32-bit counters.
	const full = 8 + 12 + 2*(33+21+18) + 4 + 5*8 + 6*4
	for _, tc := range []struct {
		name string
		buf  []byte
		want ws.SessionInfo
	}{
		{"locked", infoEx(full, 1, 3, 0, 0), ws.SessionInfo{ID: 3, Active: true, LockKnown: true, Locked: true}},
		{"unlocked", infoEx(full, 1, 3, 0, 1), ws.SessionInfo{ID: 3, Active: true, LockKnown: true}},
		{"unknown", infoEx(full, 1, 7, 0, -1), ws.SessionInfo{ID: 7, Active: true}},
		{"odd flags", infoEx(full, 1, 7, 0, 2), ws.SessionInfo{ID: 7, Active: true}},
		{"disconnected", infoEx(full, 1, 2, 4, 1), ws.SessionInfo{ID: 2, LockKnown: true}},
		{"connected", infoEx(full, 1, 2, 1, 0), ws.SessionInfo{ID: 2, LockKnown: true, Locked: true}},
		{"shortest", infoEx(20, 1, 1, 0, 1), ws.SessionInfo{ID: 1, Active: true, LockKnown: true}},
	} {
		got, err := ws.ParseInfoEx(tc.buf)
		if err != nil || got != tc.want {
			t.Errorf("%s: ParseInfoEx = %+v, %v; want %+v", tc.name, got, err, tc.want)
		}
	}
	for _, tc := range []struct {
		name string
		buf  []byte
	}{
		{"nil", nil},
		{"19 bytes", infoEx(20, 1, 1, 0, 1)[:19]},
		{"level 0", infoEx(full, 0, 1, 0, 1)},
		{"level 2", infoEx(full, 2, 1, 0, 1)},
	} {
		if got, err := ws.ParseInfoEx(tc.buf); err == nil {
			t.Errorf("%s: ParseInfoEx = %+v, nil; want an error", tc.name, got)
		}
	}
}
