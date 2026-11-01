// Package jc2win is the Windows Bluetooth LE transport of the Joy-Con 2
// backend (ADR-0002). It scans for the controllers' advertisements, opens
// GATT connections to them through the Windows Runtime, runs the short
// start-up conversation that makes a controller send input reports, and
// delivers what it sees as Events on one channel. It knows nothing of
// gamepads: package input turns Reports into input.Events, and package jc2
// decodes them.
//
// # What it may send to a controller
//
// The package is built so that a review of what can reach a controller is a
// review of a few lines (guard_test.go keeps it that way):
//
//   - The only GATT writes to the command characteristic go through gate.send
//     (gate.go), which runs jc2.CheckRequest on the exact bytes just before
//     the single WriteValueWithOptionAsync call (connect_windows.go,
//     commandWriter). Writing without response is the only mode.
//   - The only other write is the notification switch (the CCCD) of the
//     characteristics of jc2.NotifyUUIDs(side), through one helper,
//     subscribe (connect_windows.go).
//   - There are no pairing calls of any kind, no registry access and no
//     change of system settings. The Windows Runtime opens an unpaired LE
//     device for GATT without pairing; the package never asks for more.
//
// # Threads and callbacks
//
// Start launches one goroutine. It locks itself to an OS thread, initializes
// the Windows Runtime for that thread (multithreaded apartment) and does
// every WinRT call that is not inside a callback; Scan, Connect, Disconnect
// and Close only queue a request for it. The same goroutine reads the
// connection parameters of every live link every 2 s (Windows 11) and sends a
// Params event when they changed; that is a plain call on the goroutine, not a
// callback.
//
// Three WinRT events call into Go on threads of the Windows thread pool: an
// advertisement was received, a characteristic value changed, a device's
// connection status changed. Their callbacks copy bytes, take a
// latency.Stamp, send on the events channel without blocking (a full channel
// counts a drop) and return. They never wait, never await and take no lock
// that a slow path holds.
//
// The package creates no callback itself (CLAUDE.md invariant 5: callbacks
// made by syscall.NewCallback are never freed and limited in number). The
// delegate objects of github.com/saltosystems/winrt-go share one fixed set of
// four such callbacks, created once when that library is initialized, and
// dispatch to Go closures by object address.
package jc2win

import (
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/latency"
)

// DefaultEvents is the capacity of the events channel when Options.Events
// is zero.
const DefaultEvents = 256

// Options configure Start.
type Options struct {
	// Log receives the connect steps, their timings and failures. Nil
	// discards them.
	Log *slog.Logger
	// Events is the capacity of the events channel; zero means
	// DefaultEvents.
	Events int
	// ActiveScan asks Windows to send scan requests, so that scan
	// responses are heard too. The default is passive scanning, which
	// costs the controllers no battery for the answers and hears
	// everything the advertisement itself carries.
	ActiveScan bool
}

// LinkID names one connection attempt and, if it succeeds, the connection.
// It is never reused within a Transport; zero is not a valid id.
type LinkID uint32

// EventKind says which fields of an Event are set.
type EventKind uint8

// The kinds of Event. A link produces Connected, then Reports (and, where
// Windows can tell, Params), then exactly one Lost; or exactly one Failed if
// it never gets to Connected.
const (
	// Advert is an advertisement with manufacturer data of Nintendo's
	// company id (Addr, RSSI, Company, Data).
	Advert EventKind = iota + 1
	// Connected means the link is up, notifications are on and the
	// start-up commands are done (Link, Addr, Info). Reports follow.
	Connected
	// Report is one native side input report (Link, Report, Stamp).
	Report
	// Lost ends a connected link (Link, Err). It is sent when Windows
	// reports the link down, after Disconnect, and best effort at Close.
	Lost
	// Failed ends a link that never became Connected (Link, Addr, Err).
	Failed
	// Params says that the connection parameters of a connected link
	// changed (Link, Params). It is sent only where Windows can tell the
	// current parameters (Windows 11), by the transport goroutine and never
	// by a callback.
	Params
)

func (k EventKind) String() string {
	switch k {
	case Advert:
		return "advert"
	case Connected:
		return "connected"
	case Report:
		return "report"
	case Lost:
		return "lost"
	case Failed:
		return "failed"
	case Params:
		return "params"
	}
	return fmt.Sprintf("EventKind(%d)", uint8(k))
}

// ReportLen is the length of a native side input report.
const ReportLen = 63

// Event is one thing the transport saw. Which fields are set depends on
// Kind (see the kinds).
type Event struct {
	Kind EventKind
	Link LinkID
	// Addr is the Bluetooth address as Windows gives it: the six address
	// bytes as the low 48 bits, first byte on top (see FormatAddr).
	Addr uint64
	RSSI int16
	// Company and Data are one manufacturer data element of an
	// advertisement: the company id and the bytes after it. Only
	// jc2.CompanyID reaches the consumer; Data is the consumer's own copy.
	Company uint16
	Data    []byte
	// Report is a copy of the report; Stamp was read in the callback that
	// received it.
	Report [ReportLen]byte
	Stamp  latency.Stamp
	Info   LinkInfo
	// Params is the new connection parameters of a Params event.
	Params ConnParams
	// Err is why a link ended: Lost may carry nil, Failed always carries
	// an error.
	Err error
}

// LinkInfo is what the start-up conversation learned about a controller.
type LinkInfo struct {
	Side     jc2.Side
	Firmware [3]byte // major, minor, patch; zero when the reply never came
	// InfoBlock, FactoryStick and UserStick are the data of the flash
	// reads jc2.FlashInfo, jc2.FlashFactoryStick and jc2.FlashUserStick,
	// unparsed. Nil when that read failed.
	InfoBlock, FactoryStick, UserStick []byte
	// ParamsRequest is the result of the request for the throughput-optimized
	// connection parameters: ParamsAccepted, ParamsUnavailable or
	// "failed: <why>".
	ParamsRequest string
	// Params is the first read of the connection parameters, the zero value
	// where they cannot be read.
	Params ConnParams
}

// The values of LinkInfo.ParamsRequest, other than a failure.
const (
	// ParamsAccepted says that Windows 11 took the request for the
	// throughput-optimized parameters.
	ParamsAccepted = "accepted"
	// ParamsUnavailable says that this is Windows 10, which has neither the
	// request nor a way to read the parameters.
	ParamsUnavailable = "unavailable (Windows 10)"
)

// ConnParams are the current parameters of a Bluetooth LE connection, as
// Windows reports them. The zero value means unknown.
type ConnParams struct {
	// Interval is the connection interval, a multiple of 1.25 ms; zero when
	// unknown.
	Interval time.Duration
	// Latency is the peripheral latency in connection events.
	Latency uint16
	// Timeout is the supervision timeout, a multiple of 10 ms.
	Timeout time.Duration
}

// Known reports whether the parameters were read.
func (p ConnParams) Known() bool { return p.Interval > 0 }

// IntervalMs returns Interval in milliseconds (7.5, 11.25, 15, ...), or 0
// when unknown.
func (p ConnParams) IntervalMs() float64 { return float64(p.Interval) / float64(time.Millisecond) }

// Errors of failed and lost links. They are wrapped, so use errors.Is.
var (
	// ErrCanceled ends a link that Disconnect or Close canceled while it
	// was still connecting.
	ErrCanceled = errors.New("jc2win: canceled")
	// ErrLinkDropped ends a link that Windows reported down while it was
	// still connecting.
	ErrLinkDropped = errors.New("jc2win: link dropped while connecting")
	// ErrBusy fails a Connect to an address that already has a link.
	ErrBusy = errors.New("jc2win: address already has a link")
	// ErrBadRequest fails a Connect with an unknown side.
	ErrBadRequest = errors.New("jc2win: bad connect request")
)

// FormatAddr formats a Bluetooth address of an Event as AA:BB:CC:DD:EE:FF.
func FormatAddr(addr uint64) string {
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X",
		byte(addr>>40), byte(addr>>32), byte(addr>>24), byte(addr>>16), byte(addr>>8), byte(addr))
}
