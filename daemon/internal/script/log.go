package script

import (
	"strings"
	"sync/atomic"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
)

const (
	logCapacity      = 256
	logMessageBytes  = 2048
	logArgumentLimit = 16
)

// LogEntry is a fully formatted script message. Formatting happens inside
// the JavaScript call and therefore inside its watchdog budget.
type LogEntry struct {
	Level              string
	Message            string
	Time               clock.Time
	Slot               uint8
	Generation         uint32
	Device             input.DeviceID
	DeviceKey, Profile string
	// Macro is the file of the macro that logged, empty for the legacy
	// script (SCR-50).
	Macro string
}

// LogBuffer is a bounded, nonblocking queue shared by a slot and its script
// workers. It carries only Go values; draining never accesses a goja runtime.
type LogBuffer struct {
	entries chan LogEntry
	wake    chan<- struct{}
	dropped atomic.Uint64
}

func NewLogBuffer(wake chan<- struct{}) *LogBuffer {
	return &LogBuffer{entries: make(chan LogEntry, logCapacity), wake: wake}
}

func (b *LogBuffer) push(entry LogEntry) {
	select {
	case b.entries <- entry:
	default:
		b.dropped.Add(1)
	}
	if b.wake != nil {
		select {
		case b.wake <- struct{}{}:
		default:
		}
	}
}

// Drain returns at most one queueful of records and the number dropped since
// the last drain. Producers may continue to append while it runs.
func (b *LogBuffer) Drain() ([]LogEntry, uint64) {
	if b == nil {
		return nil, 0
	}
	entries := make([]LogEntry, 0, len(b.entries))
	for range logCapacity {
		select {
		case entry := <-b.entries:
			entries = append(entries, entry)
		default:
			return entries, b.dropped.Swap(0)
		}
	}
	return entries, b.dropped.Swap(0)
}

func formatLogArgs(args []goja.Value) string {
	var b strings.Builder
	for i, arg := range args {
		if i >= logArgumentLimit {
			appendLogPart(&b, " …")
			break
		}
		if i > 0 {
			appendLogPart(&b, " ")
		}
		// Value.String may call user toString. This call must stay in the
		// host callback, while the setup/handler/teardown watchdog is active.
		appendLogPart(&b, arg.String())
		if b.Len() >= logMessageBytes {
			break
		}
	}
	return b.String()
}

func appendLogPart(b *strings.Builder, value string) {
	for _, r := range value {
		if b.Len()+len(string(r)) > logMessageBytes {
			return
		}
		b.WriteRune(r)
	}
}
