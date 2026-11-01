package jc2win

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
)

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

// recorder is a gate writer that keeps what it was given.
type recorder struct {
	writes [][]byte
	err    error
	after  func(b []byte) // runs after a successful write
}

func (r *recorder) write(b []byte) error {
	r.writes = append(r.writes, bytes.Clone(b))
	if r.err != nil {
		return r.err
	}
	if r.after != nil {
		r.after(b)
	}
	return nil
}

func TestGateWritesWhatTheConstructorsMake(t *testing.T) {
	reqs := []jc2.Request{
		jc2.FirmwareInfo(),
		jc2.ReadFlash(jc2.FlashInfo),
		jc2.ReadFlash(jc2.FlashFactoryStick),
		jc2.ReadFlash(jc2.FlashUserStick),
		jc2.SetFeatureMask(),
		jc2.EnableFeatures(),
	}
	for n := range 8 {
		reqs = append(reqs, jc2.PlayerLEDs(n))
	}
	for _, r := range reqs {
		rec := &recorder{}
		g := &gate{write: rec.write, log: discard()}
		if err := g.send(r); err != nil {
			t.Errorf("%s: %v", r.Note, err)
			continue
		}
		if len(rec.writes) != 1 || !bytes.Equal(rec.writes[0], r.Bytes()) {
			t.Errorf("%s: wrote % x, want % x", r.Note, rec.writes, r.Bytes())
		}
	}
}

func TestGateRefusesWhatIsNotOnTheAllowlist(t *testing.T) {
	flash := func(addr uint32, n byte) jc2.Request {
		d := make([]byte, 8)
		d[0], d[1] = n, 0x7e
		binary.LittleEndian.PutUint32(d[4:], addr)
		return jc2.Request{ID: 0x02, Sub: 0x04, Data: d}
	}
	cases := []struct {
		name string
		req  jc2.Request
	}{
		{"nintendo pairing", jc2.Request{ID: 0x15, Sub: 0x01, Data: make([]byte, 0x11)}},
		{"pairing info", jc2.Request{ID: 0x15, Sub: 0x04}},
		{"flash write", jc2.Request{ID: 0x02, Sub: 0x05, Data: make([]byte, 0x48)}},
		{"flash erase", jc2.Request{ID: 0x02, Sub: 0x06, Data: make([]byte, 8)}},
		{"flash read of the pairing area", flash(0x1FA000, 0x40)},
		{"flash read, wrong length", flash(jc2.FlashInfo.Addr, 0x20)},
		{"firmware update", jc2.Request{ID: 0x0d, Sub: 0x01}},
		{"shutdown", jc2.Request{ID: 0x06, Sub: 0x01}},
		{"vibration", jc2.Request{ID: 0x0a, Sub: 0x02, Data: make([]byte, 0x14)}},
		{"firmware info with data", jc2.Request{ID: 0x10, Sub: 0x01, Data: []byte{1}}},
		{"other feature mask", jc2.Request{ID: 0x0c, Sub: 0x02, Data: []byte{0xff, 0, 0, 0}}},
		{"unknown lights", jc2.Request{ID: 0x09, Sub: 0x07, Data: append([]byte{0xff}, make([]byte, 7)...)}},
		{"empty", jc2.Request{}},
	}
	for _, c := range cases {
		rec := &recorder{}
		g := &gate{write: rec.write, log: discard()}
		err := g.send(c.req)
		if !errors.Is(err, jc2.ErrNotAllowed) {
			t.Errorf("%s: err = %v, want ErrNotAllowed", c.name, err)
		}
		if len(rec.writes) != 0 {
			t.Errorf("%s: the writer was called with % x", c.name, rec.writes)
		}
	}
}

func TestGatePassesWriteErrors(t *testing.T) {
	want := errors.New("link is gone")
	g := &gate{write: (&recorder{err: want}).write, log: discard()}
	if err := g.send(jc2.FirmwareInfo()); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestCheckNotify(t *testing.T) {
	for _, s := range []jc2.Side{jc2.SideL, jc2.SideR} {
		for _, u := range jc2.NotifyUUIDs(s) {
			if err := checkNotify(s, u); err != nil {
				t.Errorf("side %v %s: %v", s, u, err)
			}
		}
		for _, u := range []string{jc2.UUIDCommand, jc2.UUIDService, "00002a19-0000-1000-8000-00805f9b34fb"} {
			if err := checkNotify(s, u); !errors.Is(err, jc2.ErrNotAllowed) {
				t.Errorf("side %v %s: err = %v, want ErrNotAllowed", s, u, err)
			}
		}
	}
	// The other side's input characteristic is not allowed.
	if err := checkNotify(jc2.SideL, jc2.UUIDSideInputR); !errors.Is(err, jc2.ErrNotAllowed) {
		t.Errorf("L may not listen to R's input: %v", err)
	}
	for _, s := range []jc2.Side{0, 3} {
		if err := checkNotify(s, jc2.UUIDCommandResp); !errors.Is(err, jc2.ErrNotAllowed) {
			t.Errorf("side %v: err = %v, want ErrNotAllowed", s, err)
		}
	}
}

func TestFlashData(t *testing.T) {
	fr := jc2.FlashFactoryStick
	good := fakeReply(jc2.ReadFlash(fr).Bytes())
	data, err := flashData(good, fr)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != int(fr.Len) || data[0] != byte(fr.Addr>>8) {
		t.Errorf("data = % x", data)
	}

	bad := map[string][]byte{
		"short":         good[:10],
		"status":        append(append([]byte{}, good[:4]...), append([]byte{0x00, 0x00}, good[6:]...)...),
		"length":        func() []byte { b := bytes.Clone(good); b[replyHeaderLen] = 0x10; return b }(),
		"address":       func() []byte { b := bytes.Clone(good); b[replyHeaderLen+4] ^= 1; return b }(),
		"short content": good[:len(good)-1],
		"empty":         nil,
	}
	for name, b := range bad {
		if _, err := flashData(b, fr); !errors.Is(err, errBadReply) {
			t.Errorf("%s: err = %v, want errBadReply", name, err)
		}
	}
}

func TestReplyTo(t *testing.T) {
	r := jc2.ReadFlash(jc2.FlashInfo)
	if !replyTo(fakeReply(r.Bytes()), r) {
		t.Error("a reply does not answer its request")
	}
	if replyTo(fakeReply(jc2.FirmwareInfo().Bytes()), r) {
		t.Error("the firmware reply answers a flash read")
	}
	if replyTo([]byte{0x02, 0x01, 0x01, 0x04}, r) {
		t.Error("a truncated header counts as a reply")
	}
}

// fakeReply is the reply a controller would send to the request bytes b.
func fakeReply(b []byte) []byte {
	out := []byte{b[0], 0x01, 0x01, b[3], statusOK0, statusOK1, 0, 0}
	switch {
	case b[0] == 0x10 && b[3] == 0x01: // firmware 2.1.4, a right controller
		out = append(out, 2, 1, 4, 1)
	case b[0] == 0x02 && b[3] == 0x04: // flash
		n := b[replyHeaderLen]
		out = append(out, n, 0, 0, 0)
		out = append(out, b[replyHeaderLen+4:replyHeaderLen+8]...)
		addr := binary.LittleEndian.Uint32(b[replyHeaderLen+4:])
		for i := range n {
			out = append(out, byte(addr>>8)+i)
		}
	}
	return out
}

// newHandshake builds a handshake over a recorder that answers like a
// controller; mutate changes what the controller answers.
func newHandshake(mutate func(req []byte, reply []byte, push func([]byte))) (*handshake, *recorder, chan struct{}) {
	replies := make(chan []byte, 16)
	rec := &recorder{}
	rec.after = func(b []byte) {
		r := fakeReply(b)
		if mutate == nil {
			replies <- r
			return
		}
		mutate(b, r, func(x []byte) { replies <- x })
	}
	cancel := make(chan struct{})
	return &handshake{
		gate:    &gate{write: rec.write, log: discard()},
		replies: replies,
		cancel:  cancel,
		down:    make(chan struct{}),
		log:     discard(),
		side:    jc2.SideR,
		player:  2,
		timeout: 50 * time.Millisecond,
		spacing: time.Millisecond,
	}, rec, cancel
}

func TestHandshake(t *testing.T) {
	h, rec, _ := newHandshake(nil)
	info, err := h.run()
	if err != nil {
		t.Fatal(err)
	}
	want := []jc2.Request{
		jc2.FirmwareInfo(),
		jc2.ReadFlash(jc2.FlashInfo),
		jc2.ReadFlash(jc2.FlashFactoryStick),
		jc2.ReadFlash(jc2.FlashUserStick),
		jc2.SetFeatureMask(),
		jc2.EnableFeatures(),
		jc2.PlayerLEDs(2),
	}
	if len(rec.writes) != len(want) {
		t.Fatalf("%d commands, want %d", len(rec.writes), len(want))
	}
	for i, w := range want {
		if !bytes.Equal(rec.writes[i], w.Bytes()) {
			t.Errorf("command %d = % x, want % x (%s)", i, rec.writes[i], w.Bytes(), w.Note)
		}
	}
	if info.Side != jc2.SideR || info.Firmware != [3]byte{2, 1, 4} {
		t.Errorf("info = %+v", info)
	}
	for name, got := range map[string][]byte{"info": info.InfoBlock, "factory": info.FactoryStick, "user": info.UserStick} {
		if len(got) != 0x40 {
			t.Errorf("%s block has %d bytes", name, len(got))
		}
	}
	if info.FactoryStick[0] != byte(jc2.FlashFactoryStick.Addr>>8) {
		t.Errorf("the factory stick block is % x", info.FactoryStick)
	}
}

func TestHandshakeNeedsTheFeatureReplies(t *testing.T) {
	for _, silent := range []byte{0x02, 0x04} { // the sub-command of the mask and of the enable
		h, rec, _ := newHandshake(func(req, reply []byte, push func([]byte)) {
			if req[0] == 0x0c && req[3] == silent {
				return
			}
			push(reply)
		})
		_, err := h.run()
		if !errors.Is(err, errNoReply) {
			t.Errorf("silent %#x: err = %v, want errNoReply", silent, err)
		}
		for _, w := range rec.writes {
			if w[0] == 0x09 {
				t.Errorf("silent %#x: the player lights were set after the failure", silent)
			}
		}
	}
}

func TestHandshakeToleratesMissingOptionalReplies(t *testing.T) {
	h, rec, _ := newHandshake(func(req, reply []byte, push func([]byte)) {
		switch {
		case req[0] == 0x10: // firmware: no reply
		case req[0] == 0x02 && req[replyHeaderLen+4] == byte(jc2.FlashFactoryStick.Addr): // factory stick: error status
			reply[4], reply[5] = 0x00, 0x00
			push(reply)
		case req[0] == 0x09: // lights: no reply
		default:
			push(reply)
		}
	})
	info, err := h.run()
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.writes) != 7 {
		t.Errorf("%d commands, want 7", len(rec.writes))
	}
	if info.Firmware != [3]byte{} || info.FactoryStick != nil {
		t.Errorf("fields of failed reads are set: %+v", info)
	}
	if info.InfoBlock == nil || info.UserStick == nil {
		t.Errorf("fields of good reads are empty: %+v", info)
	}
}

func TestHandshakeIgnoresLateRepliesOfOtherCommands(t *testing.T) {
	h, _, _ := newHandshake(func(req, reply []byte, push func([]byte)) {
		push(fakeReply(jc2.PlayerLEDs(0).Bytes())) // a stray reply first
		push(reply)
	})
	info, err := h.run()
	if err != nil {
		t.Fatal(err)
	}
	if info.Firmware != [3]byte{2, 1, 4} || info.UserStick == nil {
		t.Errorf("info = %+v", info)
	}
}

func TestHandshakeWriteError(t *testing.T) {
	h, rec, _ := newHandshake(nil)
	want := errors.New("link is gone")
	rec.err = want
	if _, err := h.run(); !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
	if len(rec.writes) != 1 {
		t.Errorf("%d writes after a failed one", len(rec.writes))
	}
}

func TestHandshakeCancelAndDrop(t *testing.T) {
	h, rec, cancel := newHandshake(func(req, reply []byte, push func([]byte)) {})
	h.timeout = time.Hour
	time.AfterFunc(20*time.Millisecond, func() { close(cancel) })
	if _, err := h.run(); !errors.Is(err, ErrCanceled) {
		t.Errorf("err = %v, want ErrCanceled", err)
	}
	if len(rec.writes) != 1 {
		t.Errorf("%d commands after the cancel", len(rec.writes))
	}

	h, _, _ = newHandshake(func(req, reply []byte, push func([]byte)) {})
	h.timeout = time.Hour
	down := make(chan struct{})
	h.down = down
	time.AfterFunc(20*time.Millisecond, func() { close(down) })
	if _, err := h.run(); !errors.Is(err, ErrLinkDropped) {
		t.Errorf("err = %v, want ErrLinkDropped", err)
	}
}

func TestEventKindStrings(t *testing.T) {
	for k, want := range map[EventKind]string{Advert: "advert", Connected: "connected", Report: "report", Lost: "lost", Failed: "failed"} {
		if k.String() != want {
			t.Errorf("%d = %q, want %q", k, k.String(), want)
		}
	}
	if !strings.HasPrefix(EventKind(0).String(), "EventKind(") {
		t.Error("an unknown kind has no number in its name")
	}
}

func TestFormatAddr(t *testing.T) {
	if got := FormatAddr(0xC84805123456); got != "C8:48:05:12:34:56" {
		t.Errorf("FormatAddr = %q", got)
	}
}
