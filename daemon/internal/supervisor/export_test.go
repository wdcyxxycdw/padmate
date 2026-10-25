package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/slot"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winsession"
)

// RetryEvery is how often the router retries a slot that is behind.
const RetryEvery = retryEvery

// SuspendQuiet is Suspend without waking the router: it applies the
// suspension at its next Sync, Reload or Pause, so a test can set and
// clear reasons before the router looks.
func (s *Supervisor) SuspendQuiet(r winsession.Reason) { s.suspend(r) }

// SyncState is what the Sync barrier of one slot saw. The slot loop
// handles the edges queued before it runs a job, so Got includes the edges
// of events Run takes during the Sync; with none, Got equals Want.
type SyncState struct {
	Ran      bool       // the slot loop ran it, not a disabled slot
	Assigned bool       // a gamepad was assigned when the router sent it
	Got      slot.State // the slot's state when it ran
	Want     slot.State // the router's state of the gamepad when it sent it
}

// SyncStates is Sync, returning what each slot's barrier saw.
func (s *Supervisor) SyncStates(ctx context.Context) ([]SyncState, error) {
	bs, err := s.sync(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]SyncState, len(bs))
	for i, b := range bs {
		out[i] = SyncState{Ran: b.ran, Assigned: b.assigned, Got: b.got, Want: b.want}
	}
	return out, nil
}

// StartReload is Reload in two steps: it returns once the router took the
// request, with the generation, and wait waits for the slots and Output as
// Reload does. Unlike Reload, it takes no lock.
func (s *Supervisor) StartReload(ctx context.Context, prog *engine.Program) (gen uint32, wait func(context.Context) error, err error) {
	gen, bs, err := s.startReload(ctx, prog)
	if err != nil {
		return gen, nil, err
	}
	return gen, func(ctx context.Context) error { return s.awaitAll(ctx, bs, "reload") }, nil
}

// CheckInvariants checks the router's invariants (IN-08) on the slots of a
// Status, given the keys of the connected gamepads that match
// Config.Device and how many gamepads wait:
//   - (i) the homes of the assigned slots are pairwise distinct;
//   - (ii) a slot whose home is not its holder's key is provisional;
//   - (iii) the home of such a slot is the key of no matching connected
//     gamepad: it would have taken the slot back, or a free one;
//   - (iv) while a gamepad waits, no slot is free;
//   - (v) a free slot has no home and is not provisional.
//
// A gamepad that does not match may have the key of a home, since a
// DeviceKey has no name, so (iii) is only over those that match.
func CheckInvariants(slots []SlotStatus, matching []input.DeviceKey, waiting int) error {
	var errs []error
	homes := map[input.DeviceKey]uint8{}
	for _, st := range slots {
		if st.ID == 0 {
			if st.Home != (input.DeviceKey{}) || st.Provisional {
				errs = append(errs, fmt.Errorf("(v) free slot %d has home %v, provisional %v", st.Index, st.Home, st.Provisional))
			}
			if waiting > 0 {
				errs = append(errs, fmt.Errorf("(iv) slot %d is free while %d gamepads wait", st.Index, waiting))
			}
			continue
		}
		if i, ok := homes[st.Home]; ok {
			errs = append(errs, fmt.Errorf("(i) slots %d and %d have the home %v", i, st.Index, st.Home))
		}
		homes[st.Home] = st.Index
		if st.Home == st.Key {
			continue
		}
		if !st.Provisional {
			errs = append(errs, fmt.Errorf("(ii) slot %d has the home %v, not its holder's %v, and is final", st.Index, st.Home, st.Key))
		}
		if slices.Contains(matching, st.Home) {
			errs = append(errs, fmt.Errorf("(iii) the home %v of slot %d, held by %v, is connected", st.Home, st.Index, st.Key))
		}
	}
	return errors.Join(errs...)
}
