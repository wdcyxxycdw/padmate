package winsession

import (
	"errors"
	"testing"
	"time"
)

type resumeQuerier struct {
	session func() (SessionInfo, error)
	desktop func() (bool, error)
}

func (q resumeQuerier) Session() (SessionInfo, error) { return q.session() }
func (q resumeQuerier) Desktop() (bool, error)        { return q.desktop() }

func TestResumeOldRecheckCannotCompleteNewWake(t *testing.T) {
	var checked, queries int
	w := newWatcher(Config{
		awake:  func() time.Duration { return 0 },
		OnWake: func(Change) { checked++ },
		Query: resumeQuerier{
			session: func() (SessionInfo, error) { queries++; return SessionInfo{Active: true}, nil },
			desktop: func() (bool, error) { return false, nil },
		},
	})
	w.rawSignal(SigWake)
	old := w.wakeID
	w.rawSignal(SigWake)
	w.recheckStep(SigWake, 0, old)
	if checked != 0 || queries != 0 || !w.wakePending {
		t.Fatalf("old recheck completed new wake: checked=%d, queries=%d, pending=%v", checked, queries, w.wakePending)
	}
	w.recheckStep(SigWake, 0, w.wakeID)
	if checked != 1 || w.wakePending {
		t.Fatalf("latest recheck not completed: checked=%d, pending=%v", checked, w.wakePending)
	}
}

func TestResumeWakeDuringQueryRejectsOldReady(t *testing.T) {
	var checked int
	w := newWatcher(Config{awake: func() time.Duration { return 0 }, OnWake: func(Change) { checked++ }})
	w.q = resumeQuerier{
		session: func() (SessionInfo, error) { w.rawSignal(SigWake); return SessionInfo{Active: true}, nil },
		desktop: func() (bool, error) { return false, nil },
	}
	w.rawSignal(SigWake)
	w.recheckStep(SigWake, 0, w.wakeID)
	if checked != 0 || !w.wakePending {
		t.Fatalf("reentrant wake accepted old query: checked=%d, pending=%v", checked, w.wakePending)
	}
	w.q = resumeQuerier{
		session: func() (SessionInfo, error) { return SessionInfo{Active: true}, nil },
		desktop: func() (bool, error) { return false, nil },
	}
	w.step(SigTick, w.q)
	if checked != 1 || w.wakePending {
		t.Fatalf("tick did not check latest wake: checked=%d, pending=%v", checked, w.wakePending)
	}
}

func TestResumeQueryErrorDefersReadyUntilRetry(t *testing.T) {
	for _, failed := range []string{"session", "desktop"} {
		t.Run(failed, func(t *testing.T) {
			var checked int
			broken := true
			queryErr := errors.New("query unavailable")
			w := newWatcher(Config{
				awake:  func() time.Duration { return 0 },
				OnWake: func(Change) { checked++ },
				Query: resumeQuerier{
					session: func() (SessionInfo, error) {
						if broken && failed == "session" {
							return SessionInfo{}, queryErr
						}
						return SessionInfo{Active: true}, nil
					},
					desktop: func() (bool, error) {
						if broken && failed == "desktop" {
							return false, queryErr
						}
						return false, nil
					},
				},
			})
			w.rawSignal(SigWake)
			w.recheckStep(SigWake, 0, w.wakeID)
			if checked != 0 || !w.wakePending {
				t.Fatalf("failed query completed wake: checked=%d, pending=%v", checked, w.wakePending)
			}
			broken = false
			w.step(SigTick, w.q)
			if checked != 1 || w.wakePending {
				t.Fatalf("retry not completed: checked=%d, pending=%v", checked, w.wakePending)
			}
		})
	}
}

func TestResumeClosingSignalDuringQueryKeepsNewReason(t *testing.T) {
	for _, tc := range []struct {
		sig    Signal
		reason Reason
	}{
		{SigSleep, Sleep}, {SigLock, Lock}, {SigDisconnect, Disconnect}, {SigQueryEnd, End}, {SigEnd, End},
	} {
		for _, phase := range []string{"session", "desktop"} {
			for _, prior := range []struct {
				name   string
				preset bool
			}{{"new", false}, {"repeated", true}} {
				t.Run(tc.sig.String()+"/"+phase+"/"+prior.name, func(t *testing.T) {
					var checked int
					w := newWatcher(Config{awake: func() time.Duration { return 0 }, OnWake: func(Change) { checked++ }})
					w.rawSignal(SigSleep)
					w.step(SigSleep, nil)
					if prior.preset {
						w.rawSignal(tc.sig)
						w.step(tc.sig, nil)
					}
					closeDuringRPC := func() { w.rawSignal(tc.sig); w.step(tc.sig, nil) }
					w.q = resumeQuerier{
						session: func() (SessionInfo, error) {
							if phase == "session" {
								closeDuringRPC()
							}
							return SessionInfo{Active: true, LockKnown: true}, nil
						},
						desktop: func() (bool, error) {
							if phase == "desktop" {
								closeDuringRPC()
							}
							return false, nil
						},
					}
					w.rawSignal(SigWake)
					w.recheckStep(SigWake, w.wakeGen, w.wakeID)
					if checked != 0 || !w.wakePending || w.tracker.Reasons()&tc.reason == 0 || w.Reasons()&tc.reason == 0 {
						t.Fatalf("old query erased new reason or signaled ready: checked=%d, pending=%v, tracker=%s, reported=%s", checked, w.wakePending, w.tracker.Reasons(), w.Reasons())
					}
					// A later fresh query can finish checking this wake while
					// retaining the new closing reason; recovery stays held.
					w.q = resumeQuerier{
						session: func() (SessionInfo, error) {
							return SessionInfo{Active: tc.reason != Disconnect, LockKnown: true, Locked: tc.reason == Lock}, nil
						},
						desktop: func() (bool, error) { return false, nil },
					}
					w.step(SigTick, w.q)
					if checked != 1 || w.wakePending || w.Reasons()&tc.reason == 0 {
						t.Fatalf("fresh retry did not retain the reason: checked=%d, pending=%v, reasons=%s", checked, w.wakePending, w.Reasons())
					}
				})
			}
		}
	}
}
