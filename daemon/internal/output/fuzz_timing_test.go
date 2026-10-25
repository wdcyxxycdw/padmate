package output_test

import (
	"cmp"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/keys"
	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

type physicalTiming struct {
	at              clock.Time
	global, binding time.Duration
	// takenIn is the op whose DownFor gave binding its minimum when the
	// press came in that op; base is the minimum without it. A press the
	// same op also releases is not the DownFor's own: a queued tap that
	// runReady started first.
	takenIn int
	base    time.Duration
	seq     int  // physical press order
	freed   int  // the op after which no one referenced the press, or 0
	old     bool // a newer press of the key was queued when this one went out
}

// FuzzArbiterTiming checks physical timing independently of Arbiter's
// deadlines, plus completion uniqueness, per-key backlog and cancellation.
// An op byte from 238 up sets the event gap (OUT-17), so the op bytes of
// the older corpus keep their meaning; with a gap every Send that is not
// forced (ReleaseOwner, ReleaseAll) or a resend of failed releases has one
// event and comes at least the gap after the Send before it, ReleaseOwner
// drops no press another owner queued, and no key has more than one extra
// press (Repeat, a tap on a key held by Down) queued, however fast they
// come. When the gap is set before anything else and never back to 0, a
// modifier does not go up, but in a forced or retried Send, while a key
// pressed after it that no one has referenced since an earlier op is still
// down: the key goes up first, as in a chord. At the end, before the final
// ReleaseAll, the holds go up and the Arbiter settles: every tap and
// sequence must then complete, and nothing stays held.
func FuzzArbiterTiming(f *testing.F) {
	f.Add([]byte{1, 1, 0, 20, 1, 1, 0, 1, 2, 2, 0, 1, 5, 0, 40})
	f.Add([]byte{0, 0, 20, 20, 7, 0, 50, 8, 0, 0, 5, 0, 60})
	f.Add([]byte{1, 4, 0, 0, 5, 0, 20, 10, 0, 1, 11, 0, 0})
	f.Add([]byte{1, 1, 0, 20, 1, 1, 0, 20, 12, 0, 0, 5, 0, 40})
	f.Add([]byte{0, 238, 2, 0, 7, 0, 1, 5, 0, 5, 8, 0, 1, 5, 0, 30, 1, 1, 2, 5, 0, 60})
	f.Add([]byte{1, 238, 1, 0, 1, 0, 2, 2, 1, 2, 10, 0, 0, 5, 0, 40, 238, 0, 0, 5, 0, 60})
	f.Add([]byte{0, 238, 3, 0, 7, 0, 3, 0, 30, 3, 8, 0, 3, 13, 1, 0, 5, 0, 40, 13, 0, 0, 5, 0, 100})
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) == 0 {
			return
		}
		if len(data) > 601 {
			data = data[:601]
		}
		clk := clock.NewFake()
		rec := simtest.NewRecorder(clk)
		arb := output.NewArbiter(clk, rec, output.ArbiterOptions{MinPress: 20 * ms, MinGap: 20 * ms, RetryInterval: 7 * ms})
		physical := map[output.KeyID]physicalTiming{}
		ready := map[output.KeyID]clock.Time{}
		binding := map[output.KeyID]time.Duration{}
		// rebind holds the minimum of a DownFor whose key goes up and down
		// again behind queued releases (OUT-17): it belongs to the press
		// after the last of them, not to the one down now. n counts the
		// releases still to go out before it.
		type rebound struct {
			minimum time.Duration
			n       int
			o       output.Owner
		}
		force, canCancel, failing := false, false, false
		// releasedOwner: the owners the op's forced release released.
		releasedOwner := func(output.Owner) bool { return false }
		// upBefore counts the queued releases of each key before the op,
		// offered the releases the op offered.
		upBefore, offered := map[output.KeyID]int{}, map[output.KeyID]int{}
		rebind := map[output.KeyID][]rebound{}
		// unbind ends the logical press of id with its release offered: a
		// later press is a new one, with a DownFor's minimum that waited for
		// this release, unless a forced release dropped the DownFor's press.
		unbind := func(id output.KeyID) {
			delete(binding, id)
			kept := rebind[id][:0]
			for _, r := range rebind[id] {
				switch r.n--; {
				case r.n > 0:
					kept = append(kept, r)
				case !force || !releasedOwner(r.o):
					binding[id] = max(binding[id], r.minimum)
				}
			}
			rebind[id] = kept
		}
		holds := map[holdKey]bool{}
		mods := map[output.KeyID]bool{}
		for _, k := range keys.Table() {
			if k.IsModifier() {
				if e, err := output.KeyEventFor(k, false); err == nil {
					mods[e.ID()] = true
				}
			}
		}
		onlyTap := data[0]&1 != 0
		var taking []output.KeyID
		var takingMinimum time.Duration
		opIndex := 0
		completed := []int{}
		// capped holds, for each press of a key queued when the profile
		// minimum changed, in queue order, the lowest minimum since it was
		// queued: a release queued behind it keeps the minimum of its time.
		// A key may have more than one press queued (OUT-17, pressAgain).
		capped := map[output.KeyID][]time.Duration{}
		failedUp := map[output.KeyID]bool{} // releases offered and not injected
		// resendable: the keys whose last offered event is such a release. A
		// press offered after it, injected or not, ends the Arbiter's resends
		// of it, so the next release of the key is a new one (markResends,
		// which updates it in the order of each Send's events).
		resendable := map[output.KeyID]bool{}
		var lastSend clock.Time
		sentAny := false
		// ordered: the gap was set before any command and never back to 0.
		ordered, pressSeq := false, 0
		pending := func(id output.KeyID, held []output.HeldKey) bool {
			return slices.ContainsFunc(held, func(h output.HeldKey) bool { return h.Pending && h.Press.ID() == id })
		}
		queued := func(id output.KeyID, up bool) bool {
			return slices.ContainsFunc(output.PacedEvents(arb), func(e output.KeyEvent) bool { return e.Up == up && e.ID() == id })
		}
		// again reports whether a press of id other than an extra one is
		// queued: the key goes up and down again.
		again := func(id output.KeyID) bool {
			extra := output.PacedExtras(arb)
			for j, e := range output.PacedEvents(arb) {
				if !e.Up && !extra[j] && e.ID() == id {
					return true
				}
			}
			return false
		}
		// A logical press is free when no one references it any more: its
		// key is pending release, or a release of it is queued while the key
		// is pressed again behind it. keyFree is the op after which the
		// key's current logical press was first seen pending; a physical
		// press of it takes that over (physicalTiming.freed). An older one,
		// sent while a newer press of the key was queued, takes the op after
		// which it was first seen free, and checks no order as a modifier.
		free := func(id output.KeyID, held []output.HeldKey) bool { return pending(id, held) || queued(id, true) }
		keyFree := map[output.KeyID]int{}
		// upQueued is the op after which a release of the key was first seen
		// queued, while one is: the op that decided when it goes out.
		upQueued := map[output.KeyID]int{}
		stamp := func() {
			for id := range upQueued {
				if !queued(id, true) {
					delete(upQueued, id)
				}
			}
			for _, e := range output.PacedEvents(arb) {
				if e.Up && upQueued[e.ID()] == 0 {
					upQueued[e.ID()] = opIndex
				}
			}
			held := arb.Held()
			for id := range keyFree {
				if !pending(id, held) {
					delete(keyFree, id)
				}
			}
			for _, h := range held {
				if id := h.Press.ID(); h.Pending && keyFree[id] == 0 {
					keyFree[id] = opIndex
				}
			}
			for id, p := range physical {
				switch {
				case !free(id, held):
					p.freed = 0
				case p.freed == 0 && p.old:
					p.freed = opIndex
				case p.freed == 0:
					p.freed = cmp.Or(keyFree[id], opIndex)
				}
				physical[id] = p
			}
		}
		// forget ends the binding minimum of a logical press that never
		// reached the keyboard and has nothing queued, left without a
		// reference or gone from the ledger: its key goes up, or a new press
		// replaces it before its release is due, with a minimum of its own.
		forget := func() {
			held := arb.Held()
			for id := range binding {
				i := slices.IndexFunc(held, func(h output.HeldKey) bool { return h.Press.ID() == id })
				_, down := physical[id]
				if !down && (i < 0 || held[i].Pending) && !queued(id, false) && !queued(id, true) {
					delete(binding, id)
				}
			}
		}
		maskID := output.MenuMaskEvent(false).ID()
		// checkOrder fails when the release of the modifier mod goes out
		// while a key pressed after it is down that was free since an op
		// before the one in which mod's release went into the queue: that
		// key goes up first, as in a chord, also when it was let go of after
		// mod. Pressed after it both physically and in the Arbiter's press
		// order (Held): when a press failed, a later extra press may be the
		// first to go out.
		checkOrder := func(mod output.KeyID, held []output.HeldKey) {
			m, down := physical[mod]
			if !down || m.old {
				return
			}
			order := func(id output.KeyID) int {
				return slices.IndexFunc(held, func(h output.HeldKey) bool { return h.Press.ID() == id })
			}
			modOp := cmp.Or(upQueued[mod], opIndex)
			for id, p := range physical {
				if mods[id] || id == maskID || p.seq < m.seq || order(id) < order(mod) || failedUp[id] || p.freed == 0 || !free(id, held) {
					continue
				}
				if p.freed < modOp {
					t.Fatalf("modifier %v released at %v before key %v pressed after it, free since op %d before op %d, when its release went into the queue",
						mod, clk.Now(), id, p.freed, modOp)
				}
			}
		}
		// released forgets the key's free stamp with its release, unless a
		// newer press of the key is queued, whose stamp it is.
		released := func(id output.KeyID) {
			if !queued(id, false) {
				delete(keyFree, id)
			}
		}
		rec.Inject = func(events []output.KeyEvent) (int, error) {
			retry := true
			for _, e := range events {
				retry = retry && e.Up && failedUp[e.ID()]
			}
			if gap := arb.EventGap(); gap > 0 && !force && !retry {
				if len(events) != 1 {
					t.Fatalf("paced Send of %d events", len(events))
				}
				if sentAny && clk.Now() < lastSend.Add(gap) {
					t.Fatalf("Send at %v, %v after the last; gap %v", clk.Now(), clk.Now().Sub(lastSend), gap)
				}
			}
			lastSend, sentAny = clk.Now(), true
			for _, e := range events {
				if e.Up {
					offered[e.ID()]++
				}
			}
			n := len(events)
			if failing {
				n = min(1, n)
				if n == len(events) {
					n = 0
				}
			}
			// resent[i]: events[i] resends a release offered before and not
			// injected (the one resend of a failed batch, a retry of the
			// pending releases, a forced batch that carries them). The logical
			// press it ends ended with its first offer: unbind must not count
			// it again, or a DownFor minimum that waits for the releases
			// queued before it would reach the press before it (OUT-17).
			resent := markResends(events, n, resendable)
			minPress, minGap := arb.Timing()
			var held []output.HeldKey
			for i, e := range events[:n] {
				id := e.ID()
				if ordered && e.Up && mods[id] && !retry && !force {
					if held == nil {
						held = arb.Held()
					}
					checkOrder(id, held)
				}
				delete(failedUp, id)
				p, down := physical[id]
				if e.Up {
					released(id)
					need := max(p.global, p.binding)
					if p.takenIn == opIndex {
						need = max(p.global, p.base)
					}
					if down && !force && clk.Now().Sub(p.at) < need {
						t.Fatalf("%s released after %v; minimum %v", keyName(e), clk.Now().Sub(p.at), need)
					}
					delete(physical, id)
					if !resent[i] {
						unbind(id)
					}
					if onlyTap {
						ready[id] = clk.Now().Add(minGap)
					}
					continue
				}
				global := minPress
				if cs := capped[id]; len(cs) > 0 {
					global = min(global, cs[0])
					capped[id] = cs[1:]
				}
				if !down {
					if onlyTap && !mods[id] && !output.IsMenuMask(e) && clk.Now() < ready[id] {
						t.Fatalf("%s pressed at %v before gap %v", keyName(e), clk.Now(), ready[id])
					}
					pressSeq++
					p := physicalTiming{at: clk.Now(), global: global, binding: binding[id], takenIn: -1, base: binding[id], seq: pressSeq}
					if p.old = again(id); !p.old && pending(id, arb.Held()) {
						p.freed = keyFree[id]
					}
					if slices.Contains(taking, id) {
						p.binding, p.takenIn = max(p.binding, takingMinimum), opIndex
					}
					physical[id] = p
				}
			}
			for i, e := range events[n:] {
				if e.Up {
					// The Arbiter offered the release: the logical press is
					// over, its minimum kept or canceled by a forced release,
					// even when the press never reached the keyboard. A later
					// press of the key is a new one, without its binding
					// minimum, also while the key stays physically down.
					id := e.ID()
					failedUp[id] = true
					released(id)
					if !resent[n+i] {
						unbind(id)
					}
					if p, ok := physical[id]; ok {
						p.global, p.binding, p.base = 0, 0, 0
						physical[id] = p
					}
				}
			}
			if force {
				// The presses the forced Send dropped never reached the
				// keyboard; a press of the key the op's runReady starts after
				// it is a new one.
				for id := range binding {
					if _, down := physical[id]; !down {
						delete(binding, id)
					}
				}
			}
			if failing {
				return n, errBlocked
			}
			return n, nil
		}
		for i := 1; i+2 < len(data); i += 3 {
			op, x, y := data[i]%14, data[i+1], data[i+2]
			if data[i] >= 238 {
				op = 14
			}
			o := fuzzOwners[int(x)%len(fuzzOwners)]
			c := keys.MustParse(fuzzChords[int(y)%len(fuzzChords)])
			hold := time.Duration(y%31) * ms
			force, canCancel = false, false
			taking, takingMinimum = nil, 0
			opIndex++
			clear(upBefore)
			clear(offered)
			for _, e := range output.PacedEvents(arb) {
				if e.Up {
					upBefore[e.ID()]++
				}
			}
			forget()
			switch op {
			case 0:
				press, gap := time.Duration(x%31)*ms, time.Duration(y%31)*ms
				for id, p := range physical {
					p.global = min(p.global, press)
					physical[id] = p
				}
				old, _ := arb.Timing()
				presses := map[output.KeyID]int{}
				for _, e := range output.PacedEvents(arb) {
					if !e.Up {
						presses[e.ID()]++
					}
				}
				for id, n := range presses {
					cs := capped[id]
					for j := range cs {
						cs[j] = min(cs[j], old)
					}
					for len(cs) < n {
						cs = append(cs, old)
					}
					capped[id] = cs
				}
				_ = arb.SetTiming(press, gap)
			case 1, 2:
				idx := len(completed)
				completed = append(completed, 0)
				canCancel = arb.Blocked()
				_ = arb.TapWithCompletion(o, c, hold, func(result output.TapCompletion) {
					completed[idx]++
					if completed[idx] != 1 {
						t.Fatal("tap completed more than once")
					}
					if result.Canceled && !canCancel {
						t.Fatal("tap canceled outside a release or gate")
					}
				})
			case 3:
				_ = arb.RepeatTap(o, c, hold)
			case 4:
				_ = arb.TapSequence(o, []keys.Chord{c, keys.MustParse("X"), c}, hold, time.Duration(x%7)*ms)
			case 5:
				clk.Advance(time.Duration(y%61) * ms)
				_ = arb.Advance()
				if at, ok := arb.NextDeadline(); ok && at <= clk.Now() {
					t.Fatalf("deadline %v still due at %v", at, clk.Now())
				}
			case 6:
				_ = arb.StopRepeat(o, c)
			case 7:
				if onlyTap {
					continue
				}
				minimum := time.Duration(x%61) * ms
				hk := holdKey{o, c.Sided()}
				if !arb.Blocked() && !holds[hk] {
					takingMinimum = minimum
					stroke, _ := output.ChordStroke(c)
					for _, e := range stroke.Down {
						id := e.ID()
						taking = append(taking, id)
					}
					holds[hk] = true
				}
				_ = arb.DownFor(o, c, minimum)
				if kept, ok := output.MergedKeys(arb, o, c); ok {
					// A Down merged into an earlier press of its main key leaves
					// out the modifiers it would have to press (OUT-17): its
					// minimum is not theirs, also for a press of theirs this op
					// sent from the queue.
					for _, id := range taking {
						if p, ok := physical[id]; ok && !slices.Contains(kept, id) && p.takenIn == opIndex {
							p.binding, p.takenIn = p.base, -1
							physical[id] = p
						}
					}
					taking = slices.DeleteFunc(taking, func(id output.KeyID) bool { return !slices.Contains(kept, id) })
				}
				paced := output.PacedEvents(arb)
				for _, id := range taking {
					if n := len(slices.DeleteFunc(slices.Clone(paced), func(e output.KeyEvent) bool { return !e.Up || e.ID() != id })); n > 0 {
						// Releases still queued: the key goes up and down again.
						rebind[id] = append(rebind[id], rebound{minimum, n, o})
						continue
					}
					binding[id] = max(binding[id], minimum)
					if p, ok := physical[id]; ok {
						p.binding, p.takenIn = max(p.binding, minimum), -1
						physical[id] = p
					}
				}
			case 8:
				if onlyTap {
					continue
				}
				delete(holds, holdKey{o, c.Sided()})
				_ = arb.Up(o, c)
			case 9:
				if onlyTap {
					continue
				}
				_ = arb.Repeat(o, c)
			case 10:
				force, canCancel = true, true
				releasedOwner = func(x output.Owner) bool { return x == o }
				// The presses other owners queued all go out: their chords
				// may rely on them (OUT-17).
				var others []output.KeyEvent
				owners := output.PacedOwners(arb)
				for j, e := range output.PacedEvents(arb) {
					if !e.Up && owners[j] != o {
						others = append(others, e)
					}
				}
				from := len(rec.Records())
				_ = arb.ReleaseOwner(o)
				k := 0
				for _, r := range rec.Records()[from:] {
					if k < len(others) && !r.Event.Up && r.Event.ID() == others[k].ID() {
						k++
					}
				}
				if k < len(others) {
					t.Fatalf("queued press %s of another owner dropped", keyName(others[k]))
				}
				for hk := range holds {
					if hk.o == o {
						delete(holds, hk)
					}
				}
			case 11:
				force, canCancel = true, true
				releasedOwner = func(output.Owner) bool { return true }
				_ = arb.ReleaseAll()
				clear(holds)
			case 12:
				canCancel = true
				arb.SetBlocked(x&1 == 0)
			case 13:
				failing = x&1 != 0
			case 14:
				gap := time.Duration(x%4) * 5 * ms
				ordered = gap > 0 && (ordered || len(rec.Records()) == 0 && len(arb.Held()) == 0 && arb.Queued() == 0)
				_ = arb.SetEventGap(gap)
			}
			stamp()
			// A failed forced release stays forced on its later retries; the
			// canceled logical press has no minimum duration to preserve.
			if force {
				for _, h := range arb.Held() {
					if h.Pending {
						// The canceled logical press may never have reached the
						// keyboard. Its binding minimum must not survive in
						// the oracle and attach to a later physical press.
						delete(binding, h.Press.ID())
						if p, ok := physical[h.Press.ID()]; ok {
							p.global, p.binding, p.base = 0, 0, 0
							physical[h.Press.ID()] = p
						}
					}
				}
				// A press the event gap held back and the release dropped
				// never reached the keyboard either; the forced Send carried
				// the other queued events, and a later press of the key,
				// even one queued now by the release's runReady, is a new
				// logical press.
				for id := range binding {
					if _, down := physical[id]; !down {
						delete(binding, id)
					}
				}
			}
			pressesQueued := map[output.KeyID]int{}
			upQueued := map[output.KeyID]int{}
			extras := map[output.KeyID]bool{}
			paced, isExtra := output.PacedEvents(arb), output.PacedExtras(arb)
			for j, e := range paced {
				if e.Up {
					upQueued[e.ID()]++
				} else {
					pressesQueued[e.ID()]++
				}
				if isExtra[j] {
					if extras[e.ID()] {
						t.Fatalf("%s has more than one extra press queued: %v", keyName(e), paced)
					}
					extras[e.ID()] = true
				}
			}
			// A forced release that drops queued releases without offering
			// them drops the presses before them, which never went out, with
			// their minimums: the press of the key left, if any, is a later
			// one.
			if force {
				for id, n := range upBefore {
					if offered[id] >= n {
						continue
					}
					delete(binding, id)
					if p, ok := physical[id]; ok {
						p.binding, p.base, p.takenIn = 0, 0, -1
						physical[id] = p
					}
				}
			}
			// A queued release dropped leaves fewer to wait for. With none
			// left the DownFor's press is the one down or next: a press taken
			// over keeps going with both minimums. A forced release drops the
			// DownFor's own when it releases its owner.
			for id, rs := range rebind {
				gone, kept := false, time.Duration(0)
				for j := range rs {
					if rs[j].n = min(rs[j].n, upQueued[id]); rs[j].n == 0 && (!force || !releasedOwner(rs[j].o)) {
						gone, kept = true, max(kept, rs[j].minimum)
					}
				}
				if gone {
					binding[id] = max(binding[id], kept)
					if p, ok := physical[id]; ok {
						p.binding = max(p.binding, kept)
						physical[id] = p
					}
				}
				rebind[id] = slices.DeleteFunc(rs, func(r rebound) bool { return r.n == 0 })
			}
			// The presses that left the queue without a Send (dropped by a
			// forced release, a Repeat dropped by Up) leave fewer: the ones
			// left keep the lowest minimum of those before them, which is
			// never above their own.
			for id, cs := range capped {
				n := pressesQueued[id]
				if k := len(cs) - n; n > 0 && k > 0 {
					cs[k] = slices.Min(cs[:k+1])
					capped[id] = cs[k:]
				} else if n == 0 {
					delete(capped, id)
				}
			}
			q := output.QueuedTaps(arb)
			for j, item := range q {
				if slices.ContainsFunc(q[j+1:], func(other output.QueuedTap) bool { return other.Main == item.Main }) {
					t.Fatal("more than one queued tap per main key")
				}
			}
		}
		// Let go of the holds and settle: no tap or sequence may stall, nor
		// may a key stay held. A release cancels nothing here.
		failing, force, canCancel = false, false, false
		taking, takingMinimum = nil, 0
		if !onlyTap {
			ups := slices.Collect(maps.Keys(holds))
			slices.SortFunc(ups, func(x, y holdKey) int {
				return cmp.Or(cmp.Compare(x.o.String(), y.o.String()), cmp.Compare(x.c.String(), y.c.String()))
			})
			for _, hk := range ups {
				opIndex++
				_ = arb.Up(hk.o, hk.c)
				stamp()
			}
		}
		limit := clk.Now().Add(time.Minute)
		for {
			at, ok := arb.NextDeadline()
			if !ok {
				break
			}
			if at > limit {
				t.Fatalf("still busy at %v", at)
			}
			clk.Set(max(at, clk.Now()))
			opIndex++
			forget()
			_ = arb.Advance()
			stamp()
		}
		if n := arb.Queued(); n != 0 {
			t.Fatalf("%d taps or sequences left queued after settling", n)
		}
		for i, n := range completed {
			if n != 1 {
				t.Fatalf("tap %d completed %d times after settling", i, n)
			}
		}
		simtest.AssertNoneHeld(t, arb, rec)
		force, canCancel = true, true
		if err := arb.ReleaseAll(); err != nil {
			t.Fatal(err)
		}
		if at, ok := arb.NextDeadline(); ok {
			t.Fatalf("deadline after release: %v", at)
		}
		simtest.AssertNoneHeld(t, arb, rec)
	})
}
