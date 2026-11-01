package jc2win

// gate.go: the allowlist gate and the start-up conversation. Both are free
// of the Windows Runtime so that they run (and are tested) on any OS; the
// Windows files supply the function that really writes.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
)

// gate is the only way to the command characteristic. send encodes a request,
// has jc2.CheckRequest approve the exact bytes, and only then calls write. A
// refused request is logged and never reaches write.
//
// write is set once, where the command characteristic is found
// (commandWriter in connect_windows.go); nothing else holds it.
type gate struct {
	write func(b []byte) error
	log   *slog.Logger
}

// send writes r if the allowlist accepts it.
func (g *gate) send(r jc2.Request) error {
	b := r.Bytes()
	if err := jc2.CheckRequest(b); err != nil {
		g.log.Error("refused a command that is not on the allowlist", "note", r.Note, "bytes", fmt.Sprintf("% x", b), "err", err)
		return err
	}
	g.log.Debug("command", "note", r.Note, "bytes", fmt.Sprintf("% x", b))
	return g.write(b)
}

// checkNotify returns nil only if padmate may switch on the notifications of
// the characteristic uuid on a controller of side s.
func checkNotify(s jc2.Side, uuid string) error {
	if s != jc2.SideL && s != jc2.SideR {
		return fmt.Errorf("%w: notifications for side %v", jc2.ErrNotAllowed, s)
	}
	if !jc2.MayNotify(s, uuid) {
		return fmt.Errorf("%w: notifications on %s", jc2.ErrNotAllowed, uuid)
	}
	return nil
}

// The shape of a reply on jc2.UUIDCommandResp (jc2.Request's doc has the
// header): byte 0 is the command id, byte 3 the subcommand, bytes 4 and 5 the
// status, and the data starts at byte 8.
const (
	replyHeaderLen = 8
	statusOK0      = 0x10
	statusOK1      = 0x78
)

// replyTo reports whether b is the reply to r.
func replyTo(b []byte, r jc2.Request) bool {
	return len(b) >= replyHeaderLen && b[0] == r.ID && b[3] == r.Sub
}

// replyOK reports whether the status of the reply b says success.
func replyOK(b []byte) bool {
	return len(b) >= replyHeaderLen && b[4] == statusOK0 && b[5] == statusOK1
}

var errBadReply = errors.New("jc2win: malformed reply")

// flashData returns the bytes a flash read reply carries. The reply data is:
// the length (1 byte), 3 zero bytes, the address (4 bytes, little endian),
// then the flash content.
func flashData(b []byte, fr jc2.FlashRange) ([]byte, error) {
	if !replyOK(b) {
		return nil, fmt.Errorf("%w: flash %#x status % x", errBadReply, fr.Addr, b[min(4, len(b)):min(6, len(b))])
	}
	d := b[replyHeaderLen:]
	if len(d) < 8 || d[0] != fr.Len || d[1] != 0 || d[2] != 0 || d[3] != 0 {
		return nil, fmt.Errorf("%w: flash %#x data header % x", errBadReply, fr.Addr, d[:min(8, len(d))])
	}
	if addr := binary.LittleEndian.Uint32(d[4:]); addr != fr.Addr {
		return nil, fmt.Errorf("%w: flash read of %#x answered with address %#x", errBadReply, fr.Addr, addr)
	}
	content := d[8:]
	if len(content) < int(fr.Len) {
		return nil, fmt.Errorf("%w: flash %#x has %d of %d bytes", errBadReply, fr.Addr, len(content), fr.Len)
	}
	return bytes.Clone(content[:fr.Len]), nil
}

// firmwareOf reads the firmware version out of the reply to
// jc2.FirmwareInfo: major, minor, patch, then the controller type (0 = L,
// 1 = R).
func firmwareOf(b []byte) (v [3]byte, typ byte, err error) {
	if len(b) < replyHeaderLen+4 {
		return v, 0, fmt.Errorf("%w: firmware info has %d bytes", errBadReply, len(b))
	}
	d := b[replyHeaderLen:]
	copy(v[:], d[:3])
	return v, d[3], nil
}

// errNoReply is a command whose reply did not come in time.
var errNoReply = errors.New("jc2win: no reply")

// handshake is the start-up conversation: it asks what the controller is,
// reads its calibration, turns on the reports it needs and lights the player
// lights. Every request goes through gate, one at a time, each waiting for
// its reply.
type handshake struct {
	gate    *gate
	replies <-chan []byte // notifications of jc2.UUIDCommandResp
	cancel  <-chan struct{}
	down    <-chan struct{}
	log     *slog.Logger
	side    jc2.Side
	player  int
	timeout time.Duration // per reply
	spacing time.Duration // between commands
}

// run does the conversation. Failing to get a reply to the two commands that
// turn the reports on is an error; the rest only leaves its field of LinkInfo
// empty. Cancellation, a dropped link and a failed write end it at once.
func (h *handshake) run() (LinkInfo, error) {
	info := LinkInfo{Side: h.side}

	if r, err := h.step(jc2.FirmwareInfo(), false); err != nil {
		return info, err
	} else if r != nil {
		v, typ, ferr := firmwareOf(r)
		switch {
		case ferr != nil:
			h.log.Warn("firmware info reply is malformed", "err", ferr)
		default:
			info.Firmware = v
			if want := byte(h.side) - 1; typ != want {
				h.log.Warn("the controller reports another type than its advertisement", "type", typ, "side", h.side)
			}
		}
	}

	for _, rd := range []struct {
		fr  jc2.FlashRange
		dst *[]byte
	}{
		{jc2.FlashInfo, &info.InfoBlock},
		{jc2.FlashFactoryStick, &info.FactoryStick},
		{jc2.FlashUserStick, &info.UserStick},
	} {
		r, err := h.step(jc2.ReadFlash(rd.fr), false)
		if err != nil {
			return info, err
		}
		if r == nil {
			continue
		}
		data, ferr := flashData(r, rd.fr)
		if ferr != nil {
			h.log.Warn("flash read failed", "addr", fmt.Sprintf("%#x", rd.fr.Addr), "err", ferr)
			continue
		}
		*rd.dst = data
	}

	for _, req := range []jc2.Request{jc2.SetFeatureMask(), jc2.EnableFeatures()} {
		r, err := h.step(req, true)
		if err != nil {
			return info, err
		}
		if !replyOK(r) {
			h.log.Warn("unexpected status in a reply", "note", req.Note, "reply", fmt.Sprintf("% x", r))
		}
	}

	if _, err := h.step(jc2.PlayerLEDs(h.player), false); err != nil {
		return info, err
	}
	return info, nil
}

// step sends r and waits for its reply. It returns the reply, or nil with no
// error when none came and required is false.
func (h *handshake) step(r jc2.Request, required bool) ([]byte, error) {
	// Forget replies that arrived late for an earlier command.
	for drained := false; !drained; {
		select {
		case <-h.replies:
		default:
			drained = true
		}
	}
	start := time.Now()
	if err := h.gate.send(r); err != nil {
		return nil, fmt.Errorf("%s: %w", r.Note, err)
	}
	reply, err := h.wait(r)
	switch {
	case err == nil:
		h.log.Debug("reply", "note", r.Note, "after", time.Since(start).Round(100*time.Microsecond), "ok", replyOK(reply))
	case errors.Is(err, errNoReply):
		if required {
			return nil, fmt.Errorf("%s: %w", r.Note, err)
		}
		h.log.Warn("no reply", "note", r.Note, "after", h.timeout)
		reply = nil
	default:
		return nil, err
	}
	if err := h.pause(); err != nil {
		return nil, err
	}
	return reply, nil
}

// wait returns the reply to r.
func (h *handshake) wait(r jc2.Request) ([]byte, error) {
	timer := time.NewTimer(h.timeout)
	defer timer.Stop()
	for {
		select {
		case b := <-h.replies:
			if replyTo(b, r) {
				return b, nil
			}
			h.log.Debug("ignored a reply to another command", "waiting", r.Note, "reply", fmt.Sprintf("% x", b[:min(len(b), replyHeaderLen)]))
		case <-timer.C:
			return nil, errNoReply
		case <-h.cancel:
			return nil, ErrCanceled
		case <-h.down:
			return nil, ErrLinkDropped
		}
	}
}

// pause waits the spacing between two commands.
func (h *handshake) pause() error {
	if h.spacing <= 0 {
		return nil
	}
	timer := time.NewTimer(h.spacing)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-h.cancel:
		return ErrCanceled
	case <-h.down:
		return ErrLinkDropped
	}
}
