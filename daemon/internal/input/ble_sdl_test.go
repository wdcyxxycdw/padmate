package input_test

import (
	"encoding/binary"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/inputtest"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2win"
)

// These tests need the SDL library, as those of source_test.go do. The
// Joy-Con 2 comes from a fake transport; the Switch 1 Joy-Con is an SDL
// virtual gamepad with the ids and controls of one.

// extBLE is a input.JoyCon2Transport for the tests of this package.
type extBLE struct {
	mu       sync.Mutex
	events   chan jc2win.Event
	connects []jc2win.LinkID
	closed   bool
}

func newExtBLE() *extBLE { return &extBLE{events: make(chan jc2win.Event, 256)} }

func (f *extBLE) Events() <-chan jc2win.Event { return f.events }
func (f *extBLE) Scan(bool)                   {}
func (f *extBLE) Disconnect(jc2win.LinkID)    {}

func (f *extBLE) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
}

func (f *extBLE) Connect(uint64, jc2.Side, int) jc2win.LinkID {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := jc2win.LinkID(len(f.connects) + 1)
	f.connects = append(f.connects, id)
	return id
}

func (f *extBLE) connected() []jc2win.LinkID {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]jc2win.LinkID(nil), f.connects...)
}

func (f *extBLE) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

// bleAdvert is the standard advertisement of a Joy-Con 2.
func bleAdvert(addr uint64, side jc2.Side) jc2win.Event {
	data := make([]byte, 24)
	copy(data, []byte{0x01, 0x00, 0x03, 0x7e, 0x05})
	product := uint16(jc2.ProductL)
	if side == jc2.SideR {
		product = jc2.ProductR
	}
	binary.LittleEndian.PutUint16(data[5:], product)
	data[8], data[16] = 0x01, 0x0f
	return jc2win.Event{Kind: jc2win.Advert, Addr: addr, Company: jc2.CompanyID, Data: data}
}

// bleConnected is the Connected event of a link whose serial number is serial.
func bleConnected(id jc2win.LinkID, side jc2.Side, serial string) jc2win.Event {
	block := make([]byte, 0x40)
	copy(block[2:], serial)
	return jc2win.Event{Kind: jc2win.Connected, Link: id, Info: jc2win.LinkInfo{Side: side, InfoBlock: block}}
}

// bleReport is a report of a Joy-Con 2 with the given button bits and the
// stick at its default center.
func bleReport(id jc2win.LinkID, bits uint16) jc2win.Event {
	ev := jc2win.Event{Kind: jc2win.Report, Link: id}
	b := &ev.Report
	b[2], b[3] = byte(bits), byte(bits>>8)
	b[5], b[6], b[7] = 0x00, 0x08, 0x80 // x = y = 2048
	return ev
}

// TestJoyCon2NeverPairsWithSwitch1 checks that a Switch 1 Joy-Con that SDL
// reports and a Joy-Con 2 of the other side stay two single gamepads: only
// the Joy-Cons 2 of the Bluetooth LE backend form a pair.
func TestJoyCon2NeverPairsWithSwitch1(t *testing.T) {
	const (
		addr   = uint64(0xC84805000001)
		serial = "XJX10000000001"
	)
	for _, tc := range []struct {
		name    string
		desc    input.VirtualDesc
		sdlType string
		side    jc2.Side
		bleType string
		bits    uint16
		control string
	}{
		{"Switch 1 L and Joy-Con 2 R", inputtest.JoyConLeft("padmate sdl L"), "joyconleft", jc2.SideR, "joyconright", 1 << 1, "east"},
		{"Switch 1 R and Joy-Con 2 L", inputtest.JoyConRight("padmate sdl R"), "joyconright", jc2.SideL, "joyconleft", 1 << 2, "dpad.left"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := newExtBLE()
			s, err := input.Open(input.Options{
				Library: inputtest.Library(t),
				JoyCon2: &input.JoyCon2Options{
					Known:       []input.KnownJoyCon2{{Addr: addr, Serial: serial, Side: tc.side}},
					AutoConnect: true,
					Transport:   func(*slog.Logger) (input.JoyCon2Transport, error) { return fake, nil },
				},
			})
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			closed := false
			t.Cleanup(func() {
				if !closed {
					s.Close()
				}
			})
			if err := s.JoyCon2Available(); err != nil {
				t.Fatal(err)
			}
			v, err := s.AttachVirtualDesc(tc.desc)
			if err != nil {
				t.Fatalf("AttachVirtualDesc: %v", err)
			}
			fake.events <- bleAdvert(addr, tc.side)
			var link jc2win.LinkID
			for deadline := time.Now().Add(5 * time.Second); link == 0; time.Sleep(time.Millisecond) {
				if c := fake.connected(); len(c) > 0 {
					link = c[0]
				} else if time.Now().After(deadline) {
					t.Fatal("the Joy-Con 2 was not connected")
				}
			}
			fake.events <- bleConnected(link, tc.side, serial)

			// Collect what the Source reports: the two single gamepads, and the
			// press of the Joy-Con 2.
			var sdlID, bleID input.DeviceID
			added := map[input.DeviceID]*input.DeviceInfo{}
			deadline := time.After(5 * time.Second)
			sentReport := false
		collect:
			for {
				select {
				case e := <-s.Events():
					switch {
					case e.Kind == input.DeviceAdded && e.Device == v.ID():
						sdlID = e.Device
						added[e.Device] = e.Info
					case e.Kind == input.DeviceAdded && e.Info != nil && e.Info.Driver == "ble":
						bleID = e.Device
						added[e.Device] = e.Info
					case e.Kind == input.DeviceAdded && e.Info != nil && e.Info.Type == "joyconpair":
						t.Errorf("a pair was reported: %+v", e.Info)
					case e.Kind == input.ButtonDown && e.Device == bleID && bleID != 0:
						if e.Control != tc.control {
							t.Errorf("Joy-Con 2 press %q, want %q", e.Control, tc.control)
						}
						break collect
					}
					if sdlID != 0 && bleID != 0 && !sentReport {
						sentReport = true
						fake.events <- bleReport(link, tc.bits)
					}
				case <-deadline:
					t.Fatalf("gamepads %v after 5 s", added)
				}
			}
			// Nothing combines them afterwards either.
			for quiet := time.After(300 * time.Millisecond); ; {
				select {
				case e := <-s.Events():
					if e.Kind == input.DeviceAdded && e.Info != nil && e.Info.Type == "joyconpair" {
						t.Errorf("a pair was reported: %+v", e.Info)
					}
					continue
				case <-quiet:
				}
				break
			}
			if sdlID == 0 || bleID == 0 || sdlID == bleID {
				t.Fatalf("SDL gamepad %d, Joy-Con 2 %d", sdlID, bleID)
			}
			if got := added[sdlID].Type; got != tc.sdlType {
				t.Errorf("SDL gamepad type %q, want %q", got, tc.sdlType)
			}
			if got := added[bleID]; got.Type != tc.bleType || got.Serial != serial {
				t.Errorf("Joy-Con 2 info %+v, want %s with serial %s", got, tc.bleType, serial)
			}
			// The Joy-Con 2's id is never one of SDL's: its per-device calls do
			// not reach SDL.
			if _, err := s.RawStart(bleID); err == nil {
				t.Error("RawStart of a Joy-Con 2 succeeded")
			}
			// The transport is closed with the Source.
			s.Close()
			closed = true
			if !fake.isClosed() {
				t.Error("the transport was not closed by Close")
			}
		})
	}
}
