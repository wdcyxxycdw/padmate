package winsession_test

import (
	"sync/atomic"
	"testing"

	ws "github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

func TestResumeRawWakeFencesBeforeDeferredQuery(t *testing.T) {
	var raw, checked atomic.Int32
	h := start(t, nil, func(c *ws.Config) {
		c.OnSignal = func(ws.Signal) { raw.Add(1) }
		c.OnWake = func(ws.Change) { checked.Add(1) }
	})
	hold := make(chan struct{})
	h.f.set(func(f *fakeSystem) { f.hold = hold })
	h.send(ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, 0)
	if raw.Load() != 1 || checked.Load() != 0 {
		close(hold)
		t.Fatalf("raw=%d,checked=%d before query returned", raw.Load(), checked.Load())
	}
	close(hold)
	h.flush()
	if checked.Load() != 1 {
		t.Fatal("wake did not finish after deferred query")
	}
}

func TestResumeQueryPanicDefersReadyUntilRetry(t *testing.T) {
	var checked atomic.Int32
	h := start(t, nil, func(c *ws.Config) { c.OnWake = func(ws.Change) { checked.Add(1) } })
	h.f.set(func(f *fakeSystem) { f.panicking = true })
	h.send(ws.WMPowerBroadcast, ws.PBTAPMResumeAutomatic, 0)
	h.flush()
	if checked.Load() != 0 {
		t.Errorf("failed wake query signaled ready: %d", checked.Load())
	}
	h.f.set(func(f *fakeSystem) { f.panicking = false })
	h.post(ws.WMTimer, ws.TimerTick, 0)
	h.flush()
	if checked.Load() != 1 {
		t.Fatalf("next query did not recover wake: %d", checked.Load())
	}
}

func TestResumeRepeatedRawSignalsAndCheckedWakes(t *testing.T) {
	var raw, checked atomic.Int32
	h := start(t, nil, func(c *ws.Config) {
		c.OnSignal = func(s ws.Signal) {
			if s == ws.SigWake {
				raw.Add(1)
			}
		}
		c.OnWake = func(change ws.Change) {
			if change.Now != 0 {
				t.Errorf("wake reasons: %s", change.Now)
			}
			checked.Add(1)
		}
	})
	for _, message := range []uintptr{ws.PBTAPMResumeAutomatic, ws.PBTAPMResumeSuspend, ws.PBTAPMResumeCritical} {
		h.send(ws.WMPowerBroadcast, message, 0)
		h.flush()
	}
	if raw.Load() != 3 || checked.Load() != 3 {
		t.Fatalf("raw=%d, checked=%d", raw.Load(), checked.Load())
	}
	if h.w.Reasons() != 0 {
		t.Fatalf("wake without sleep changed reasons: %s", h.w.Reasons())
	}
}
