package input

import (
	"context"
	"log/slog"
	"math"
	"strconv"
	"time"

	"github.com/Zyko0/go-sdl3/sdl"
)

// This file is the receiver watchdog on T-input (IN-30, ADR-0029); its
// pure logic is in watchdog.go, its heartbeat sources in hidbeat.go.
//
// A watched device lives until its watchdog is lost: T-input then reports
// it removed (lose), so that its slot releases every key, and keeps it
// dormant, with its gamepad and heartbeat source open: closing the
// gamepad would put the device into simple reports, and SDL would send
// nothing of it again. Its next SDL event brings it back (wake) as a new
// device, with a new DeviceID, since a DeviceID is never reused.

// openBeat opens the heartbeat source of the gamepad d, whose DeviceInfo
// is info and SDL connection state state, connected at t, when the
// watchdog is on and watches it. A source that does not open leaves the
// device unwatched, as it was before IN-30.
func (s *Source) openBeat(d *device, info *DeviceInfo, state sdl.JoystickConnectionState, t uint64) {
	if !s.receiver.on() {
		return
	}
	v := s.virtuals[d.id]
	if !watchable(info, state, v != nil && v.reports != nil) {
		return
	}
	var b *hidBeat
	if info.Virtual {
		b = v.beatSource()
	} else {
		open := s.openHID
		if open == nil {
			open = func(string) (*hidBeat, error) { return nil, errNoHIDBinding }
		}
		var err error
		if b, err = open(info.Path); err != nil {
			s.log.Info("receiver heartbeat unavailable", "id", d.pub, "err", err)
			return
		}
	}
	d.beat = b
	d.dog = watchdog{tm: s.receiver}
	d.dog.start(t)
	d.statsAt = t
	s.watched++
	s.log.Info("receiver heartbeat watching", "id", d.pub, "name", info.Name, "path", info.Path)
}

// endBeat closes the heartbeat source of d, which SDL has removed.
func (s *Source) endBeat(d *device) {
	b := d.beat
	b.close()
	d.beat = nil
	s.watched--
	s.log.Info("receiver heartbeat ended", "id", d.pub, "reports", b.reports, "still", b.still, "idle", b.idle, "other", b.other, "ids", b.idCounts())
}

// watchReceivers drains the heartbeat sources, at most every
// receiverWatchEvery, at now, T-input's SDL_GetTicksNS, and reports the
// devices whose watchdog is lost at safe removed at safe: safe is the
// time that this turn of the loop has given fireDue, so the removal fires
// no combo early (loop).
//
// now is read before the drain, so every report that came before it is
// read: a heartbeat source buffers its reports, and when its buffer is
// full it drops the oldest. So however long T-input was held up, reports
// that it has not read yet do not count as silence. When T-input itself
// has not watched for longer than stall, as over a sleep, the reports
// may have stopped with it, and an armed device that drained no
// heartbeat gets a new timeout (grace), once a silence (watchdog.grace).
func (s *Source) watchReceivers(now, safe uint64) {
	if s.watched == 0 || now < s.lastWatch+uint64(receiverWatchEvery) {
		return
	}
	var stalled uint64 // how long T-input did not watch, when over stall
	if s.lastWatch != 0 && now-s.lastWatch > s.receiver.stall {
		stalled = now - s.lastWatch
	}
	s.lastWatch = now
	for _, d := range s.devices {
		if d.beat == nil {
			continue
		}
		dr, err := d.beat.drain(now)
		if err != nil {
			s.log.Warn("receiver heartbeat failed", "id", d.pub, "err", err)
			d.beat.close()
			d.beat = nil
			s.watched--
			continue
		}
		s.logStats(d, now)
		if d.dormant {
			continue // it comes back by an SDL event only
		}
		switch {
		case dr.beats > 0:
			if d.dog.beat(now, dr.idle > 0, d.axes) {
				b := d.beat
				s.log.Info("receiver heartbeat armed", "id", d.pub, "reports", b.reports, "still", b.still, "idle", b.idle, "other", b.other)
			}
		case stalled > 0 && d.dog.armed:
			s.grace(d, now, stalled)
		}
		if dr.simple {
			d.dog.otherMode()
		}
		if d.dog.switched(now) {
			s.log.Info("receiver heartbeat mode changed", "id", d.pub, "ids", d.beat.idCounts())
		}
		if d.dog.lost(safe) {
			s.lose(d, safe)
		}
	}
}

// grace grants the silent armed device d a new timeout from now, after
// T-input did not watch for stalled, and logs it: at info when it is
// granted, at debug when it is refused, since the silence had one.
func (s *Source) grace(d *device, now, stalled uint64) {
	granted := d.dog.grace(now)
	level := slog.LevelDebug
	if granted {
		level = slog.LevelInfo
	}
	s.log.Log(context.Background(), level, "receiver heartbeat grace", "id", d.pub, "stalled", time.Duration(stalled).String(), "granted", granted)
}

// logStats logs the heartbeat statistics of d at debug level, once every
// receiverStatsEvery.
func (s *Source) logStats(d *device, now uint64) {
	if now < d.statsAt+uint64(receiverStatsEvery) {
		return
	}
	d.statsAt = now
	if !s.log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	b := d.beat
	since := time.Duration(0)
	if b.haveContent && now > b.changedAt {
		since = time.Duration(now - b.changedAt)
	}
	s.log.Debug("receiver heartbeat stats", "id", d.pub, "reports", b.reports, "still", b.still, "idle", b.idle, "other", b.other,
		"ids", b.idCounts(), "timer", b.prevTimer, "status", "0x"+strconv.FormatUint(uint64(b.status), 16),
		"since", since.String(), "maxGap", time.Duration(b.beatGap.max).String(), "maxIdleGap", time.Duration(b.idleGap.max).String(),
		"armed", d.dog.armed, "dormant", d.dormant)
	b.beatGap.max, b.idleGap.max = 0, 0
}

// lose reports the watched device d removed at t, and keeps it dormant.
func (s *Source) lose(d *device, t uint64) {
	s.stopEffects(d)
	d.dormant = true
	silent := time.Duration(0)
	if last := d.dog.last(); t > last {
		silent = time.Duration(t - last)
	}
	s.emit(Event{Time: t, Device: d.pub, Kind: DeviceRemoved})
	b := d.beat
	s.log.Warn("receiver silent: reported removed", "id", d.pub, "silent", silent.String(), "reports", b.reports,
		"still", b.still, "idle", b.idle, "other", b.other, "ids", b.idCounts(), "status", "0x"+strconv.FormatUint(uint64(b.status), 16))
}

// wake brings the dormant device d back at t, the time of an SDL event of
// its own, and reports whether it did. It does not when SDL no longer
// lists the device: SDL is removing it, and the event is the release or
// the recentering that SDL sends before the removal.
func (s *Source) wake(d *device, t uint64) bool {
	if !listed(d.id) {
		return false
	}
	s.revive(d, t)
	return true
}

// revive reports the dormant device d as a new device, at t, with a new
// id. As when a gamepad connects, the buttons held then are not reported
// down; their releases come as orphan ups. It must prove its heartbeat
// again.
//
// It keeps d.axes, the values last reported: SDL already holds the whole
// report that woke the device, and the events of its other axes still
// wait in SDL's queue, so reading SDL's values now would drop them as
// repeats.
func (s *Source) revive(d *device, t uint64) {
	old := d.pub
	d.pub = s.alias()
	d.dormant = false
	info := s.describe(d)
	s.registerEffects(d, info) // under the new id: effects reach it by that id only
	d.dog.start(t)
	s.emit(Event{Time: t, Device: d.pub, Kind: DeviceAdded, Info: info})
	s.rereadPower(d, t) // the consumer dropped the old id's value with the removal
	s.log.Info("receiver back: reported as a new device", "id", d.pub, "replaces", old)
}

// alias returns a new id for a device that comes back: 2³²−1, then down.
// SDL counts its instance ids up from 1, so the two never meet.
func (s *Source) alias() DeviceID {
	id := DeviceID(math.MaxUint32 - s.aliases)
	s.aliases++
	return id
}
