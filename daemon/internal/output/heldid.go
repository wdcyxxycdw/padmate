package output

import (
	"encoding/binary"
	"fmt"
	"time"
)

// HeldIdentity is the logon session a held-key log was written in
// (OUT-14, ADR-0027). The startup sweep takes the keys of a log only when
// it is of the same logon session as the run: the keys of another's were
// let go of when that session ended, and a key the system has down now is
// not the last run's but the user's or another program's.
//
// No field comes from the clock: a clock-derived boot time moves inside
// one logon session when the system clock is stepped or drifts from the
// tick count, which would take a log of this session for another's and
// leave its keys stuck.
type HeldIdentity struct {
	// Session is the process's Remote Desktop Services session
	// (ProcessIdToSessionId).
	Session uint32
	// Logon is the LUID of the logon session of the process token
	// (TokenStatistics.AuthenticationId), HighPart in the high 32 bits; for
	// the elevated token of a split admin token, the LUID of its linked
	// token, so that an elevated padmated and one that is not share it.
	Logon uint64
	// LogonTime is when the logon session Logon began, as
	// LsaGetLogonSessionData reports it: a FILETIME, in 100 ns since 1601
	// UTC. LSA keeps the time of the logon, so it stays the same for the
	// whole logon session whatever the system clock does later; it tells
	// the logon session from one of another boot with the same LUID, as
	// LUIDs are allocated anew at every boot.
	LogonTime int64
}

// heldIdentitySize is the size of a HeldIdentity in the log's header.
const heldIdentitySize = 8 + 4 + 8

// filetimeUnixSeconds is the Unix epoch in seconds since 1601, where
// FILETIME starts.
const filetimeUnixSeconds = 11_644_473_600

// same reports whether a and b are one logon session: all three fields
// equal, with no slack.
func (a HeldIdentity) same(b HeldIdentity) bool { return a == b }

// String names the logon time in UTC, to the second, as the messages of
// the sweep do.
func (a HeldIdentity) String() string {
	// Seconds first: a log may have any LogonTime, and the nanoseconds of
	// one would not fit an int64.
	at := time.Unix(a.LogonTime/10_000_000-filetimeUnixSeconds, a.LogonTime%10_000_000*100).UTC()
	return fmt.Sprintf("session %d, logon %#x at %s", a.Session, a.Logon, at.Format(time.RFC3339))
}

// put writes a at the start of b, little-endian: LogonTime, Session,
// Logon.
func (a HeldIdentity) put(b []byte) {
	binary.LittleEndian.PutUint64(b[0:8], uint64(a.LogonTime))
	binary.LittleEndian.PutUint32(b[8:12], a.Session)
	binary.LittleEndian.PutUint64(b[12:20], a.Logon)
}

// heldIdentityAt reads the HeldIdentity put wrote at the start of b.
func heldIdentityAt(b []byte) HeldIdentity {
	return HeldIdentity{
		LogonTime: int64(binary.LittleEndian.Uint64(b[0:8])),
		Session:   binary.LittleEndian.Uint32(b[8:12]),
		Logon:     binary.LittleEndian.Uint64(b[12:20]),
	}
}
