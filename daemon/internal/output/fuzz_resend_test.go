package output_test

import (
	"slices"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestMarkResends pins FuzzArbiterTiming's reading of a Send: which of its
// events resend a release offered before and not injected. The key's last
// offered event decides, in the order of the batch: a press of the key before
// the release in the same Send (a forced batch, or an unpaced tap with no
// hold) makes that release a new one, as a press in an earlier Send does.
func TestMarkResends(t *testing.T) {
	ev := func(name string, up bool) output.KeyEvent {
		t.Helper()
		e, err := output.KeyEventFor(keys.MustParse(name).Key(), up)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	ctrl, shift := ev("LCtrl", false).ID(), ev("LShift", false).ID()
	for _, tc := range []struct {
		name   string
		before []output.KeyID // resendable before the Send
		events []output.KeyEvent
		n      int // events the Send injects
		resent []bool
		after  []output.KeyID // resendable after it
	}{
		{"a new release that fails", nil,
			[]output.KeyEvent{ev("LCtrl", true)}, 0,
			[]bool{false}, []output.KeyID{ctrl}},
		{"its resend fails too", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", true)}, 0,
			[]bool{true}, []output.KeyID{ctrl}},
		{"its resend goes out", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", true)}, 1,
			[]bool{true}, nil},
		{"a new release that goes out", nil,
			[]output.KeyEvent{ev("LCtrl", true)}, 1,
			[]bool{false}, nil},
		{"a press that fails ends the resends", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", false)}, 0,
			[]bool{false}, nil},
		{"only the key's own release is a resend", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LShift", true), ev("LCtrl", true)}, 0,
			[]bool{false, true}, []output.KeyID{ctrl, shift}},
		{"a press in the Send makes the release after it a new one", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", false), ev("LShift", false), ev("LShift", true), ev("LCtrl", true)}, 4,
			[]bool{false, false, false, false}, nil},
		{"the same with the press not injected", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", false), ev("LCtrl", true)}, 0,
			[]bool{false, false}, []output.KeyID{ctrl}},
		{"a release after a resend of the same key is a new one", []output.KeyID{ctrl},
			[]output.KeyEvent{ev("LCtrl", true), ev("LCtrl", false), ev("LCtrl", true)}, 3,
			[]bool{true, false, false}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resendable := map[output.KeyID]bool{}
			for _, id := range tc.before {
				resendable[id] = true
			}
			resent := markResends(tc.events, tc.n, resendable)
			if !slices.Equal(resent, tc.resent) {
				t.Errorf("resent = %v, want %v", resent, tc.resent)
			}
			var after []output.KeyID
			for id := range resendable {
				after = append(after, id)
			}
			if len(after) != len(tc.after) {
				t.Errorf("resendable after = %v, want %v", after, tc.after)
			}
			for _, id := range tc.after {
				if !resendable[id] {
					t.Errorf("%v is not resendable after the Send, want it", id)
				}
			}
		})
	}
}

// markResends tells which events of a Send resend a release offered before
// and not injected, in the order of the batch, and updates resendable, the
// keys whose last offered event is such a release: n events are injected,
// the rest fail. A release of a marked key is a resend, one not marked is a
// new release, which fails (and is marked) when it is not injected. A press
// of the key, injected or not, ends the Arbiter's resends of the release
// before it, also inside the same batch. A resend is not counted as a release
// again (FuzzArbiterTiming's unbind).
func markResends(events []output.KeyEvent, n int, resendable map[output.KeyID]bool) []bool {
	resent := make([]bool, len(events))
	for i, e := range events {
		id := e.ID()
		resent[i] = e.Up && resendable[id]
		if e.Up && i >= n {
			resendable[id] = true
		} else {
			delete(resendable, id)
		}
	}
	return resent
}
