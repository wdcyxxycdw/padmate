package input

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"sort"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// FuzzJoyCon2Hub feeds the hub random transport events, reports and time
// steps, and requires after every step that the hub's gamepads and the events
// it sent agree (CLAUDE.md invariant 1): a DeviceID is added once, only live
// devices have events, a button goes up only after it went down, what a live
// gamepad holds is exactly the state its reports make, no link is in two
// gamepads, at most one pair exists, and after Close nothing is live.
func FuzzJoyCon2Hub(f *testing.F) {
	f.Add([]byte{0x0b, 0, 0, 0, 1, 0, 0, 1, 1, 2, 5, 3, 0x40, 0x00, 0x08, 0x00, 7, 5, 20, 5, 200, 5, 200})
	f.Add([]byte{0x0f, 0, 0, 0, 1, 0, 1, 1, 1, 2, 2, 1, 5, 2, 2, 0xff, 0xff, 0xff, 0xf0, 1, 5, 5, 30, 3, 2, 12, 5, 150, 11, 0})
	f.Add([]byte{0x01, 6, 30, 0, 2, 0, 0, 1, 1, 0, 1, 2, 1, 3, 9, 4, 5, 255, 10, 0, 7, 5, 255, 6, 3, 0, 3, 1, 4, 0, 8, 1})
	f.Add([]byte{0x0d, 0, 0, 0, 0, 0, 2, 1, 1, 0, 1, 2, 3, 2, 3, 1, 2, 0x01, 0x02, 0x03, 0x04, 0x05, 5, 100, 2, 1, 0, 0, 0, 0, 0, 5, 100})
	f.Fuzz(func(t *testing.T, data []byte) {
		fz := &hubFuzz{data: data}
		fz.rig = newRig(t, func(c *joyCon2Config) {
			b := fz.next()
			c.AutoConnect = b&1 != 0
			c.Vertical = b&2 != 0
			c.Power = b&4 != 0
			if b&8 != 0 {
				knownAuto(c)
				c.AutoConnect = b&1 != 0
			}
		})
		fz.run(t)
	})
}

// TestHubRandomWalk runs the fuzz interpreter on pseudo-random inputs, so
// that an ordinary test run covers the deep states too, and checks that the
// walks do reach pairs, silent controllers and revivals.
func TestHubRandomWalk(t *testing.T) {
	var pairs, revived, removed int
	rng := rand.New(rand.NewPCG(1, 2))
	for range 300 {
		data := make([]byte, 2400)
		for i := range data {
			data[i] = byte(rng.Uint32())
		}
		data[0] |= 9 // the controllers are remembered and connected to
		z := &hubFuzz{data: data}
		z.rig = newRig(t, func(c *joyCon2Config) {
			b := z.next()
			c.AutoConnect, c.Vertical, c.Power = b&1 != 0, b&2 != 0, b&4 != 0
			knownAuto(c)
			c.AutoConnect = b&1 != 0
		})
		z.run(t)
		names := map[DeviceKey]int{}
		for _, e := range z.rig.evs {
			switch e.Kind {
			case DeviceAdded:
				if e.Info.Type == "joyconpair" {
					pairs++
				}
				names[KeyOf(e.Info)]++
				if names[KeyOf(e.Info)] > 1 {
					revived++
				}
			case DeviceRemoved:
				removed++
			}
		}
	}
	if pairs < 20 || revived < 20 || removed < 100 {
		t.Errorf("the walks made %d pairs, %d revivals (or re-pairings), %d removals: too shallow", pairs, revived, removed)
	}
}

// hubFuzz reads operations from the fuzz input.
type hubFuzz struct {
	data []byte
	pos  int
	rig  *hubRig
}

// next returns the next input byte, 0 when the input is used up.
func (z *hubFuzz) next() byte {
	if z.pos >= len(z.data) {
		return 0
	}
	b := z.data[z.pos]
	z.pos++
	return b
}

// link picks a link id and its side: mostly one the hub has, sometimes a
// stray one.
func (z *hubFuzz) link() (jc2win.LinkID, jc2.Side) {
	b := z.next()
	if links := z.rig.h.links; b%5 != 0 && len(links) > 0 {
		l := links[int(b)/5%len(links)]
		return l.id, l.side
	}
	return jc2win.LinkID(b%10 + 1), jc2.Side(b/10%2 + 1)
}

func (z *hubFuzz) more() bool { return z.pos < len(z.data) }

var fuzzAddrs = []struct {
	addr uint64
	side jc2.Side
	ser  string
}{{addrL1, jc2.SideL, serL1}, {addrR1, jc2.SideR, serR1}, {addrL2, jc2.SideL, serL2}, {addrR2, jc2.SideR, serR2}}

func (z *hubFuzz) run(t *testing.T) {
	r := z.rig
	for ops := 0; z.more() && ops < 400; ops++ {
		before := len(r.evs)
		switch z.next() % 14 {
		case 0, 1: // an advertisement, usually standard
			a := fuzzAddrs[z.next()%4]
			r.advertise(a.addr, a.side, z.next()%8 != 0)
		case 2: // a link comes up
			id, side := z.link()
			serial := []string{serL1, serR1, serL2, serR2, "", "X"}[z.next()%6]
			r.feed(connectedEvent(id, side, serial, nil))
		case 3, 4, 5: // a report
			id, _ := z.link()
			bits := uint16(z.next()) | uint16(z.next())<<8
			x, y := uint16(z.next())<<4, uint16(z.next())<<4
			r.counter++
			r.feed(reportEvent(id, r.counter, bits, x, y, z.next()))
		case 6: // a link ends
			id, _ := z.link()
			if z.next()%2 == 0 {
				r.feed(jc2win.Event{Kind: jc2win.Lost, Link: id})
			} else {
				r.feed(jc2win.Event{Kind: jc2win.Failed, Link: id, Err: jc2win.ErrCanceled})
			}
		case 7, 8: // time passes
			r.step(time.Duration(z.next()) * 20 * time.Millisecond)
		case 9:
			r.h.OpenWindow(r.now, time.Duration(z.next()%40)*time.Second)
		case 10:
			if z.next()%2 == 0 {
				r.h.CancelWindow(r.now)
			} else {
				r.h.SetAutoConnect(z.next()%2 == 0)
			}
		case 11:
			switch z.next() % 3 {
			case 0:
				r.h.ForgetSerial(fuzzAddrs[z.next()%4].ser, r.now)
			case 1:
				var list []KnownJoyCon2
				mask := z.next()
				for i, a := range fuzzAddrs {
					if mask&(1<<i) != 0 {
						list = append(list, KnownJoyCon2{a.addr, a.ser, a.side})
					}
				}
				r.h.SetKnown(list, r.now)
			default:
				r.h.ForgetAddr(fuzzAddrs[z.next()%4].addr, r.now)
			}
		case 12: // the consumer disconnects a live gamepad
			ids := make([]DeviceID, 0, len(r.trk.live))
			for id := range r.trk.live {
				ids = append(ids, id)
			}
			slices.Sort(ids)
			pick := int(z.next())
			if len(ids) > 0 {
				r.h.Disconnect(ids[pick%len(ids)], r.now)
			}
		case 13: // events through the channel, and a snapshot
			r.counter++
			id, _ := z.link()
			r.tr.push(reportEvent(id, r.counter, uint16(z.next()), 2048, 2048, 0))
			r.h.Drain(r.now)
			for _, sn := range r.h.Snapshots() {
				if _, ok := r.trk.live[sn.Device]; !ok || sn.gamepadState() == nil {
					t.Fatalf("snapshot of %d, which is not live", sn.Device)
				}
			}
		}
		z.check(t, before)
	}

	r.h.Close(r.now)
	if len(r.trk.live) != 0 {
		t.Fatalf("live after Close: %v", r.trk.live)
	}
	n := len(r.evs)
	r.report(1, 0xffff)
	r.advertise(addrL1, jc2.SideL, true)
	r.step(time.Hour)
	if len(r.evs) != n {
		t.Fatalf("%d events after Close", len(r.evs)-n)
	}
}

// check holds the hub and the events since before to the invariants.
func (z *hubFuzz) check(t *testing.T, before int) {
	t.Helper()
	r, h := z.rig, z.rig.h
	live := map[DeviceID]*jc2Device{}
	inDevice := map[*jc2Link]bool{}
	pairs := 0
	for _, dev := range h.devs {
		if len(dev.links) == 2 {
			pairs++
			if dev.links[0].side != jc2.SideL || dev.links[1].side != jc2.SideR {
				t.Fatalf("pair of %v and %v", dev.links[0].side, dev.links[1].side)
			}
		}
		for _, l := range dev.links {
			if inDevice[l] {
				t.Fatalf("link %d is in two gamepads", l.id)
			}
			inDevice[l] = true
			if l.phase != linkConnected || l.dormant || l.dev != dev {
				t.Fatalf("link %d in a gamepad is %v / %p", l.id, l.phase, l.dev)
			}
		}
		if dev.live {
			live[dev.id] = dev
			for _, l := range dev.links {
				if l.dormant {
					t.Fatalf("gamepad %d is live with a dormant link", dev.id)
				}
			}
		}
	}
	if pairs > 1 {
		t.Fatalf("%d pairs", pairs)
	}
	players := map[int]bool{}
	var lefts, rights int // connected links that report
	for _, l := range h.links {
		if l.phase == linkConnected && !l.dormant && !inDevice[l] {
			t.Fatalf("connected link %d is in no gamepad", l.id)
		}
		if l.phase == linkConnected && l.dormant && l.dev != nil {
			t.Fatalf("link %d is silent and still in a gamepad", l.id)
		}
		if l.phase == linkConnected && !l.dormant {
			if l.side == jc2.SideL {
				lefts++
			} else {
				rights++
			}
		}
		if l.phase != linkClosing {
			if players[l.player] {
				t.Fatalf("player %d twice", l.player)
			}
			players[l.player] = true
		}
	}
	// A left and a right Joy-Con that both report are a pair, whatever else
	// went silent before.
	if lefts > 0 && rights > 0 && pairs != 1 {
		t.Fatalf("%d left and %d right Joy-Cons report, and there are %d pairs", lefts, rights, pairs)
	}
	if len(h.links) > joyCon2MaxLinks {
		t.Fatalf("%d links", len(h.links))
	}
	if len(live) != len(r.trk.live) {
		t.Fatalf("hub has %d live gamepads, the events %d", len(live), len(r.trk.live))
	}
	for id, dev := range live {
		if _, ok := r.trk.live[id]; !ok {
			t.Fatalf("gamepad %d is live in the hub but not in the events", id)
		}
		if want, got := heldOf(dev.state), r.trk.heldNames(id); !slices.Equal(want, got) {
			t.Fatalf("gamepad %d holds %v by the events, its state says %v", id, got, want)
		}
	}
	// The timers' deadline: nothing runs past its silence.
	for _, l := range h.links {
		if l.phase == linkConnected && !l.dormant && l.silence.GaveUp(r.now) {
			t.Fatalf("link %d is past its give-up time and not closed", l.id)
		}
	}
	_ = before
}

// heldOf lists the held controls of s the way tracker.heldNames does.
func heldOf(s jc2.State) []string {
	var out []string
	for c := jc2.Control(0); c <= jc2.ControlRightTrigger; c++ {
		switch {
		case c.IsAxis():
			if v := s.Axis(c); v != 0 {
				out = append(out, fmt.Sprintf("%s=%d", c.Name(), v))
			}
		case s.Down(c):
			out = append(out, c.Name())
		}
	}
	sort.Strings(out)
	return out
}
