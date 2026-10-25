package winsession_test

import (
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/wdcyxxycdw/padmate/daemon/internal/winfocus"
	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsessiontest"
)

func TestFocusSharesSessionThreadAndRestarts(t *testing.T) {
	for range 2 {
		var current atomic.Pointer[winfocus.App]
		first := winfocus.App{Window: 1, PID: 123, Exe: "paint.exe", Path: `C:\apps\paint.exe`}
		current.Store(&first)
		changes := make(chan winfocus.App, 8)
		var queryThread, notifyThread atomic.Uint32
		var queryInSend atomic.Bool
		w, err := ws.Start(ws.Config{Query: newFake(), Focus: &winfocus.Config{
			Query: func() (winfocus.App, error) {
				if winsessiontest.InSendMessage() {
					queryInSend.Store(true)
				}
				queryThread.Store(windows.GetCurrentThreadId())
				return *current.Load(), nil
			},
			OnChange: func(app winfocus.App) {
				notifyThread.Store(windows.GetCurrentThreadId())
				changes <- app
			},
		}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = w.Close() })
		await := func(want winfocus.App) {
			t.Helper()
			select {
			case got := <-changes:
				if got != want {
					t.Fatalf("got %+v, want %+v", got, want)
				}
			case <-time.After(wait):
				t.Fatal("foreground change not delivered")
			}
		}
		await(first)
		if queryThread.Load() == 0 || queryThread.Load() != notifyThread.Load() {
			t.Fatal("query and delivery did not share the session thread")
		}
		second := winfocus.App{Window: 2, PID: 456, Exe: "other.exe", Path: `C:\apps\other.exe`}
		current.Store(&second)
		winsessiontest.Send(w.Window(), 0x8008, 0, 0)
		await(second)
		if queryInSend.Load() {
			t.Fatal("foreground query blocked a SendMessage caller")
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
