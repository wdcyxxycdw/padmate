package winsession

import (
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	wtsCurrentServer   = 0          // WTS_CURRENT_SERVER_HANDLE
	wtsCurrentSession  = 0xFFFFFFFF // WTS_CURRENT_SESSION
	wtsSessionInfoEx   = 25         // WTSSessionInfoEx
	uoiName            = 2          // UOI_NAME
	uoiIO              = 6          // UOI_IO
	desktopReadObjects = 0x0001     // DESKTOP_READOBJECTS
	nameChars          = 64         // the first try of objectName's buffer
)

// systemQuerier is the system's Querier. It is safe for concurrent use:
// the session thread and, after the thread ended, the fallback call it.
type systemQuerier struct {
	log *slog.Logger

	mu     sync.Mutex
	warned map[string]bool // the queries whose failure was logged at Warn
}

func newSystemQuerier(log *slog.Logger) *systemQuerier {
	return &systemQuerier{log: log, warned: map[string]bool{}}
}

// Session reads this process's session: WTSQuerySessionInformationW with
// WTSSessionInfoEx.
func (q *systemQuerier) Session() (SessionInfo, error) {
	info, err := readSession()
	q.result("session", err)
	return info, err
}

// Desktop reports whether the input desktop is not this thread's.
func (q *systemQuerier) Desktop() (bool, error) {
	other, err := readDesktop()
	q.result("desktop", err)
	return other, err
}

// result logs a failed query: at Warn the first time since the query last
// succeeded, at Debug after that, so that a query that keeps failing on
// every tick does not flood the log.
func (q *systemQuerier) result(query string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err == nil {
		delete(q.warned, query)
		return
	}
	if q.warned[query] {
		q.log.Debug("session query failed", "query", query, "err", err)
		return
	}
	q.warned[query] = true
	q.log.Warn("session query failed", "query", query, "err", err)
}

func readSession() (SessionInfo, error) {
	var buf *byte
	var n uint32
	if err := wtsQuerySessionInformation(wtsCurrentServer, wtsCurrentSession, wtsSessionInfoEx, &buf, &n); err != nil {
		return SessionInfo{}, fmt.Errorf("winsession: WTSQuerySessionInformationW: %w", err)
	}
	if buf == nil {
		return SessionInfo{}, errors.New("winsession: WTSQuerySessionInformationW returned no buffer")
	}
	defer windows.WTSFreeMemory(uintptr(unsafe.Pointer(buf)))
	return parseInfoEx(unsafe.Slice(buf, n))
}

// readDesktop compares the input desktop with the calling thread's. It
// locks the goroutine to its thread for the call, so that "the calling
// thread" stays one thread from any goroutine.
//
// UOI_IO of the thread's desktop says whether it receives input. A TRUE
// answer is trusted; a FALSE one, or a failure, is confirmed by opening the
// input desktop (ADR-0026): access denied means a desktop this process may
// not open, the secure desktop of UAC or Ctrl+Alt+Del; otherwise the names
// decide.
func readDesktop() (other bool, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	mine, err := getThreadDesktop(windows.GetCurrentThreadId())
	if mine == 0 {
		return false, fmt.Errorf("winsession: GetThreadDesktop: %w", err)
	}
	var io, needed uint32
	if err := getUserObjectInformation(mine, uoiIO, unsafe.Pointer(&io), uint32(unsafe.Sizeof(io)), &needed); err == nil && io != 0 {
		return false, nil
	}
	input, err := openInputDesktop(0, false, desktopReadObjects)
	if input == 0 {
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			return true, nil
		}
		return false, fmt.Errorf("winsession: OpenInputDesktop: %w", err)
	}
	defer func() { _ = closeDesktop(input) }()
	mineName, err := objectName(mine)
	if err != nil {
		return false, err
	}
	inputName, err := objectName(input)
	if err != nil {
		return false, err
	}
	return !strings.EqualFold(mineName, inputName), nil
}

// objectName returns the UOI_NAME of a desktop.
func objectName(h windows.Handle) (string, error) {
	buf := make([]uint16, nameChars)
	for {
		var needed uint32
		err := getUserObjectInformation(h, uoiName, unsafe.Pointer(&buf[0]), uint32(len(buf)*2), &needed)
		if err == nil {
			return windows.UTF16ToString(buf), nil
		}
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) || int(needed) <= len(buf)*2 {
			return "", fmt.Errorf("winsession: GetUserObjectInformationW(UOI_NAME): %w", err)
		}
		buf = make([]uint16, (needed+1)/2)
	}
}
