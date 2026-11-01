package jc2win

// winrt_windows.go: small helpers over github.com/saltosystems/winrt-go that
// the other Windows files share: starting and ending the Windows Runtime on
// a thread, awaiting an IAsyncOperation, moving bytes in and out of IBuffer,
// and turning panics of the binding into errors.

import (
	"errors"
	"fmt"
	"syscall"
	"time"
	"unsafe"

	"github.com/go-ole/go-ole"
	"github.com/saltosystems/winrt-go"
	"github.com/saltosystems/winrt-go/windows/foundation"
	"github.com/saltosystems/winrt-go/windows/storage/streams"
	"golang.org/x/sys/windows"
)

const (
	roInitMultithreaded = 1 // RO_INIT_MULTITHREADED
	sFalse              = 1 // S_FALSE: this thread was already initialized
	rpcEChangedMode     = 0x80010106
	errClassNotReg      = 0x80040154 // REGDB_E_CLASSNOTREG
)

var procRoUninitialize = windows.NewLazySystemDLL("combase.dll").NewProc("RoUninitialize")

// roInitialize enters the multithreaded apartment on the calling thread.
// Callbacks of the Windows thread pool can then run without the thread
// pumping messages.
func roInitialize() error {
	err := ole.RoInitialize(roInitMultithreaded)
	var oe *ole.OleError
	if errors.As(err, &oe) {
		switch uint32(oe.Code()) {
		case sFalse:
			return nil // initialized before; RoUninitialize still balances it
		case rpcEChangedMode:
			return fmt.Errorf("jc2win: this thread already entered a single-threaded apartment: %w", err)
		}
	}
	if err != nil {
		return fmt.Errorf("jc2win: initialize the Windows Runtime: %w", err)
	}
	return nil
}

func roUninitialize() {
	if procRoUninitialize.Find() == nil {
		_, _, _ = procRoUninitialize.Call()
	}
}

// recoverCallback is deferred first in every callback: a panic on a thread of
// the Windows thread pool would end the process.
func recoverCallback() { _ = recover() }

// try runs f and turns a panic of the binding into an error. The generated
// wrappers panic (MustQueryInterface) when an object lacks an interface, as
// older versions of Windows do for the newer ones.
func try(f func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("jc2win: windows runtime call failed: %v", r)
		}
	}()
	return f()
}

// cancelOp asks the operation to stop.
func cancelOp(op *foundation.IAsyncOperation) {
	_ = try(func() error {
		info, err := op.QueryInterface(ole.NewGUID(foundation.GUIDIAsyncInfo))
		if err != nil {
			return err
		}
		defer info.Release()
		return (*foundation.IAsyncInfo)(unsafe.Pointer(info)).Cancel()
	})
}

// asyncError describes how an operation ended other than by completing.
func asyncError(op *foundation.IAsyncOperation, st foundation.AsyncStatus) error {
	var code int32
	err := try(func() error {
		info, err := op.QueryInterface(ole.NewGUID(foundation.GUIDIAsyncInfo))
		if err != nil {
			return err
		}
		defer info.Release()
		hr, err := (*foundation.IAsyncInfo)(unsafe.Pointer(info)).GetErrorCode()
		code = hr.Value
		return err
	})
	if err != nil || code == 0 {
		return fmt.Errorf("async operation ended with status %d", st)
	}
	return fmt.Errorf("async operation ended with status %d, HRESULT %#08x", st, uint32(code))
}

// await waits for op, whose result type has the WinRT signature sig, and
// returns its result. It gives up when the timeout passes, when cancel or
// down (either may be nil) closes, or when the transport stops; the
// operation is then asked to cancel. While it waits it keeps the scanner in
// step with Scan, because the transport goroutine is the only one that may
// touch it. The caller releases op.
//
// The completion handler is a delegate of winrt-go (see the package doc on
// callbacks); it only does a non-blocking send.
func (t *Transport) await(op *foundation.IAsyncOperation, sig, what string, timeout time.Duration, cancel, down <-chan struct{}) (unsafe.Pointer, error) {
	done := make(chan foundation.AsyncStatus, 1)
	iid := winrt.ParameterizedInstanceGUID(foundation.GUIDAsyncOperationCompletedHandler, sig)
	h := foundation.NewAsyncOperationCompletedHandler(ole.NewGUID(iid),
		func(_ *foundation.AsyncOperationCompletedHandler, _ *foundation.IAsyncOperation, st foundation.AsyncStatus) {
			select {
			case done <- st:
			default:
			}
		})
	defer h.Release()
	if err := op.SetCompleted(h); err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case st := <-done:
			if st != foundation.AsyncStatusCompleted {
				return nil, fmt.Errorf("%s: %w", what, asyncError(op, st))
			}
			res, err := op.GetResults()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", what, err)
			}
			return res, nil
		case <-timer.C:
			cancelOp(op)
			return nil, fmt.Errorf("%s: timed out after %s", what, timeout)
		case <-cancel:
			cancelOp(op)
			return nil, ErrCanceled
		case <-down:
			cancelOp(op)
			return nil, ErrLinkDropped
		case <-t.stop:
			cancelOp(op)
			return nil, ErrCanceled
		case <-t.wake:
			t.reconcileScan()
		}
	}
}

// bufferBytes returns a copy of the content of buf.
func bufferBytes(buf *streams.IBuffer) ([]byte, bool) {
	n, err := buf.GetLength()
	if err != nil {
		return nil, false
	}
	if n == 0 {
		return nil, true
	}
	r, err := streams.DataReaderFromBuffer(buf)
	if err != nil {
		return nil, false
	}
	defer r.Release()
	data, err := r.ReadBytes(n)
	if err != nil {
		return nil, false
	}
	return data, true
}

// newBuffer makes an IBuffer that holds a copy of b. The caller releases it.
func newBuffer(b []byte) (*streams.IBuffer, error) {
	w, err := streams.NewDataWriter()
	if err != nil {
		return nil, err
	}
	defer w.Release()
	if err := w.WriteBytes(uint32(len(b)), b); err != nil {
		return nil, err
	}
	return w.DetachBuffer()
}

// uuidString formats a GUID as lowercase canonical text, the form of the
// UUIDs in package jc2.
func uuidString(g syscall.GUID) string {
	return fmt.Sprintf("%08x-%04x-%04x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		g.Data1, g.Data2, g.Data3,
		g.Data4[0], g.Data4[1], g.Data4[2], g.Data4[3], g.Data4[4], g.Data4[5], g.Data4[6], g.Data4[7])
}

// isClassNotRegistered reports whether err says that a Windows Runtime class
// does not exist on this version of Windows.
func isClassNotRegistered(err error) bool {
	var oe *ole.OleError
	return errors.As(err, &oe) && uint32(oe.Code()) == errClassNotReg
}
