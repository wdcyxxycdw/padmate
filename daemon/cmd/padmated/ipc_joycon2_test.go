package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/input/jc2"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profilelib"
	"github.com/wdcyxxycdw/padmate/daemon/internal/supervisor"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// joySource is a fakeSource with a Joy-Con 2 backend of its own: it keeps a
// status that the test changes, and records what it was asked. Nothing reaches
// Bluetooth.
type joySource struct {
	*fakeSource
	mu     sync.Mutex
	avail  error
	status input.JoyCon2Status
	calls  []string
	// opts is the Options.JoyCon2 that startLive passed to open, nil if it
	// passed none.
	opts *input.JoyCon2Options
	next input.DeviceID
}

func newJoySource() *joySource {
	return &joySource{fakeSource: newFakeSource(), status: input.JoyCon2Status{AutoConnect: true}, next: 5000}
}

func (s *joySource) call(format string, args ...any) {
	s.calls = append(s.calls, fmt.Sprintf(format, args...))
}

func (s *joySource) log() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *joySource) JoyCon2Available() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.avail
}

func (s *joySource) JoyCon2Pair(d time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("pair %s", d)
	s.status.WindowOpen, s.status.WindowLeft, s.status.WindowDeadline = true, d, time.Now().Add(d)
	return nil
}

func (s *joySource) JoyCon2CancelPair() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("cancel")
	s.status.WindowOpen = false
	return nil
}

func (s *joySource) JoyCon2SetAutoConnect(on bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("auto %v", on)
	s.status.AutoConnect = on
	return nil
}

func (s *joySource) JoyCon2Forget(serial string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("forget %s", serial)
	n := len(s.status.Known)
	s.status.Known = slices.DeleteFunc(s.status.Known, func(k input.JoyCon2KnownStatus) bool { return k.Serial == serial })
	s.status.Links = slices.DeleteFunc(s.status.Links, func(l input.JoyCon2LinkStatus) bool { return l.Serial == serial })
	if len(s.status.Known) == n {
		return fmt.Errorf("%w: serial %q", input.ErrJoyCon2Unknown, serial)
	}
	return nil
}

func (s *joySource) JoyCon2Disconnect(id input.DeviceID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.call("disconnect %d", id)
	for i, l := range s.status.Links {
		if l.Device == id {
			s.status.Links = slices.Delete(s.status.Links, i, i+1)
			return nil
		}
	}
	return fmt.Errorf("%w: %d", input.ErrNoDevice, id)
}

func (s *joySource) JoyCon2Status() (input.JoyCon2Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Known, st.Links = slices.Clone(st.Known), slices.Clone(st.Links)
	if st.WindowOpen {
		st.WindowLeft = time.Until(st.WindowDeadline)
	}
	return st, nil
}

// closeWindow ends the window as the hub does when its time is up.
func (s *joySource) closeWindow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.WindowOpen = false
}

// pairController plays the hub when a controller holds SYNC in the window: it
// connects, calls OnRemember on the caller's goroutine (T-input, in the
// real one), and closes the window.
func (s *joySource) pairController(side jc2.Side, serial string, addr uint64) input.KnownJoyCon2 {
	k := input.KnownJoyCon2{Addr: addr, Serial: serial, Side: side}
	s.mu.Lock()
	onRemember := s.opts.OnRemember
	s.mu.Unlock()
	// As in the hub's turn of T-input: the callback first, then the state a
	// status request sees, all at once.
	onRemember(k)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.next++
	s.status.Known = append(s.status.Known, input.JoyCon2KnownStatus{KnownJoyCon2: k, Connected: true})
	s.status.Links = append(s.status.Links, input.JoyCon2LinkStatus{Addr: addr, Side: side, Serial: serial, Player: 1, State: "connected", Device: s.next, Known: true})
	s.status.WindowOpen = false
	return k
}

// joyLive starts live mode on a library with a joySource. The source gets
// Options.JoyCon2, as the production one does (liveConfig.joyCon2).
func joyLive(t *testing.T, config string, setup func(*joySource)) (*testLive, *joySource, string) {
	t.Helper()
	dir := t.TempDir()
	src := newJoySource()
	if setup != nil {
		setup(src)
	}
	tl, _ := startLibraryLive(t, config, options{dataDir: dir, dryRun: true, watch: false}, func(cfg *liveConfig) {
		cfg.joyCon2 = true
		cfg.open = func(o input.Options) (eventSource, error) {
			src.mu.Lock()
			src.opts = o.JoyCon2
			src.mu.Unlock()
			return src, nil
		}
	})
	return tl, src, dir
}

func rpcCodeOf(t *testing.T, err error) jrpc2.Code {
	t.Helper()
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("not an RPC error: %v", err)
	}
	return rpcErr.Code
}

const joyLeftAddr, joyRightAddr = 0xC84805000001, 0xC84805000002

// TestJoyCon2StartupOptions: live mode gives the backend the remembered
// controllers of state.json and the switch of config.json, and leaves it out
// where a fake would scan (no cfg.joyCon2, no Options.JoyCon2).
func TestJoyCon2StartupOptions(t *testing.T) {
	state := `{"schemaVersion":0,"joycon2":[
		{"addr":"C8:48:05:00:00:01","serial":"XKW1","side":"L"},
		{"addr":"C8:48:05:00:00:02","serial":"XKW2","side":"R"}]}`
	tests := []struct {
		name       string
		config     string
		state      string
		enable     bool
		wantNil    bool
		wantAuto   bool
		wantSerial []string
	}{
		{"old state file, default switch", `{"schemaVersion":0}`, `{"schemaVersion":0,"lastProfile":"default"}`, true, false, true, nil},
		{"remembered", `{"schemaVersion":0}`, state, true, false, true, []string{"XKW1", "XKW2"}},
		{"switch off", `{"schemaVersion":0,"joycon2":{"autoConnect":false}}`, state, true, false, false, []string{"XKW1", "XKW2"}},
		{"switch explicitly on", `{"schemaVersion":0,"joycon2":{"autoConnect":true}}`, state, true, false, true, []string{"XKW1", "XKW2"}},
		{"a test's fake gets none", `{"schemaVersion":0}`, state, false, true, false, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(tc.state), 0o600); err != nil {
				t.Fatal(err)
			}
			src := newJoySource()
			startLibraryLive(t, tc.config, options{dataDir: dir, dryRun: true, watch: false}, func(cfg *liveConfig) {
				cfg.joyCon2 = tc.enable
				cfg.open = func(o input.Options) (eventSource, error) {
					src.opts = o.JoyCon2
					return src, nil
				}
			})
			if (src.opts == nil) != tc.wantNil {
				t.Fatalf("Options.JoyCon2 = %+v", src.opts)
			}
			if tc.wantNil {
				return
			}
			var serials []string
			for _, k := range src.opts.Known {
				serials = append(serials, k.Serial)
			}
			if src.opts.AutoConnect != tc.wantAuto || !slices.Equal(serials, tc.wantSerial) {
				t.Fatalf("AutoConnect=%v Known=%v", src.opts.AutoConnect, serials)
			}
			if len(src.opts.Known) == 2 && (src.opts.Known[0].Addr != joyLeftAddr || src.opts.Known[0].Side != jc2.SideL || src.opts.Known[1].Side != jc2.SideR) {
				t.Fatalf("Known = %+v", src.opts.Known)
			}
			// The watcher gives the backend the config's switch as well.
			deadline := time.Now().Add(liveWait)
			want := fmt.Sprintf("auto %v", tc.wantAuto)
			for !slices.Contains(src.log(), want) && time.Now().Before(deadline) {
				time.Sleep(5 * time.Millisecond)
			}
			if !slices.Contains(src.log(), want) {
				t.Fatalf("calls %v, want %q", src.log(), want)
			}
		})
	}
}

func TestParseJoyCon2State(t *testing.T) {
	for _, tc := range []struct {
		addr string
		want uint64
		ok   bool
	}{
		{"C8:48:05:00:00:01", 0xC84805000001, true},
		{"c8:48:05:00:00:01", 0xC84805000001, true},
		{"00:00:00:00:00:00", 0, false},
		{"C8:48:05:00:00", 0, false},
		{"C8:48:05:00:00:0", 0, false},
		{"C8:48:05:00:00:GG", 0, false},
		{"C8-48-05-A9-C9-55", 0, false},
		{"", 0, false},
	} {
		if got, ok := parseJoyCon2Addr(tc.addr); got != tc.want || ok != tc.ok {
			t.Errorf("parseJoyCon2Addr(%q) = %x, %v", tc.addr, got, ok)
		}
	}
	// The text the backend writes is the text read back.
	k := input.KnownJoyCon2{Addr: 0xC84805000001}
	if got, ok := parseJoyCon2Addr(k.AddrText()); !ok || got != k.Addr {
		t.Fatalf("round trip: %x %v", got, ok)
	}
}

// TestIPCJoyCon2Methods: the methods validate their parameters before they
// touch the backend, and map its errors to the codes.
func TestIPCJoyCon2Methods(t *testing.T) {
	tl, src, _ := joyLive(t, `{"schemaVersion":0}`, func(s *joySource) {
		s.status.Known = []input.JoyCon2KnownStatus{{KnownJoyCon2: input.KnownJoyCon2{Addr: joyLeftAddr, Serial: "XKW1", Side: jc2.SideL}, Connected: true}}
		s.status.Links = []input.JoyCon2LinkStatus{{Addr: joyLeftAddr, Side: jc2.SideL, Serial: "XKW1", Player: 1, State: "connected", Device: 7001, Known: true, Firmware: "2.1.4"}}
	})
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tl.waitUntil(t, "the watcher's first call", func() bool { return slices.Contains(src.log(), "auto true") })

	tests := []struct {
		name    string
		method  string
		params  any
		code    jrpc2.Code // 0: success
		calls   []string   // what the backend was asked, after "auto true"
		openNow bool       // the window is open in the result
	}{
		{"status", "joycon2.status", nil, 0, nil, false},
		{"status takes nothing", "joycon2.status", map[string]any{"x": 1}, jrpc2.InvalidParams, nil, false},
		{"pair, default 30 s", "joycon2.pair", nil, 0, []string{"pair 30s"}, true},
		{"pair, empty params", "joycon2.pair", map[string]any{}, 0, []string{"pair 30s"}, true},
		{"pair 5 s", "joycon2.pair", map[string]any{"seconds": 5}, 0, []string{"pair 5s"}, true},
		{"pair 600 s", "joycon2.pair", map[string]any{"seconds": 600}, 0, []string{"pair 10m0s"}, true},
		{"pair 0 s", "joycon2.pair", map[string]any{"seconds": 0}, jrpc2.InvalidParams, nil, false},
		{"pair -1 s", "joycon2.pair", map[string]any{"seconds": -1}, jrpc2.InvalidParams, nil, false},
		{"pair 601 s", "joycon2.pair", map[string]any{"seconds": 601}, jrpc2.InvalidParams, nil, false},
		{"pair text", "joycon2.pair", map[string]any{"seconds": "30"}, jrpc2.InvalidParams, nil, false},
		{"pair fraction", "joycon2.pair", map[string]any{"seconds": 1.5}, jrpc2.InvalidParams, nil, false},
		{"pair unknown member", "joycon2.pair", map[string]any{"seconds": 30, "side": "L"}, jrpc2.InvalidParams, nil, false},
		{"cancel", "joycon2.cancelPair", nil, 0, []string{"cancel"}, false},
		{"cancel takes nothing", "joycon2.cancelPair", map[string]any{"x": 1}, jrpc2.InvalidParams, nil, false},
		{"forget needs a serial", "joycon2.forget", nil, jrpc2.InvalidParams, nil, false},
		{"forget empty serial", "joycon2.forget", map[string]any{"serial": ""}, jrpc2.InvalidParams, nil, false},
		{"forget serial of a number", "joycon2.forget", map[string]any{"serial": 5}, jrpc2.InvalidParams, nil, false},
		{"forget too long a serial", "joycon2.forget", map[string]any{"serial": strings.Repeat("x", 65)}, jrpc2.InvalidParams, nil, false},
		{"forget unknown", "joycon2.forget", map[string]any{"serial": "NOPE"}, -32004, []string{"forget NOPE"}, false},
		{"disconnect needs an id", "joycon2.disconnect", nil, jrpc2.InvalidParams, nil, false},
		{"disconnect id 0", "joycon2.disconnect", map[string]any{"id": 0}, jrpc2.InvalidParams, nil, false},
		{"disconnect negative id", "joycon2.disconnect", map[string]any{"id": -3}, jrpc2.InvalidParams, nil, false},
		{"disconnect id of text", "joycon2.disconnect", map[string]any{"id": "abc"}, jrpc2.InvalidParams, nil, false},
		{"disconnect id null", "joycon2.disconnect", map[string]any{"id": nil}, jrpc2.InvalidParams, nil, false},
		{"disconnect unknown gamepad", "joycon2.disconnect", map[string]any{"id": 9999}, -32004, []string{"disconnect 9999"}, false},
		{"disconnect", "joycon2.disconnect", map[string]any{"id": 7001}, 0, []string{"disconnect 7001"}, false},
		{"disconnect twice", "joycon2.disconnect", map[string]any{"id": "7001"}, -32004, []string{"disconnect 7001"}, false},
		{"forget", "joycon2.forget", map[string]any{"serial": "XKW1"}, 0, []string{"forget XKW1"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src.mu.Lock()
			src.calls = nil
			src.mu.Unlock()
			var st rpcJoyCon2Status
			err := client.CallResult(ctx, tc.method, tc.params, &st)
			switch {
			case tc.code != 0:
				if err == nil || rpcCodeOf(t, err) != tc.code {
					t.Fatalf("err = %v, want code %d", err, tc.code)
				}
			case err != nil:
				t.Fatal(err)
			case !st.Available || st.WindowOpen != tc.openNow:
				t.Fatalf("result %+v", st)
			}
			if got := src.log(); !slices.Equal(got, tc.calls) {
				t.Fatalf("backend calls %v, want %v", got, tc.calls)
			}
		})
	}
}

// TestIPCJoyCon2StatusShape: the result carries the window, the remembered
// controllers and the connections.
func TestIPCJoyCon2StatusShape(t *testing.T) {
	tl, src, _ := joyLive(t, `{"schemaVersion":0}`, func(s *joySource) {
		s.status.Known = []input.JoyCon2KnownStatus{
			{KnownJoyCon2: input.KnownJoyCon2{Addr: joyLeftAddr, Serial: "XKW1", Side: jc2.SideL}, Connected: true},
			{KnownJoyCon2: input.KnownJoyCon2{Addr: joyRightAddr, Serial: "XKW2", Side: jc2.SideR}},
		}
		s.status.Links = []input.JoyCon2LinkStatus{{Addr: joyLeftAddr, Side: jc2.SideL, Serial: "XKW1", Player: 2, State: "connected", Device: 7001, Known: true, Firmware: "2.1.4", Interval: "15ms"}}
		s.status.Scanning = true
	})
	_ = src
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var st rpcJoyCon2Status
	if err := client.CallResult(ctx, "joycon2.status", nil, &st); err != nil {
		t.Fatal(err)
	}
	want := rpcJoyCon2Status{Available: true, AutoConnect: true, Scanning: true,
		Known: []rpcJoyCon2Known{{Addr: "C8:48:05:00:00:01", Serial: "XKW1", Side: "L", Connected: true}, {Addr: "C8:48:05:00:00:02", Serial: "XKW2", Side: "R"}},
		Links: []rpcJoyCon2Link{{Addr: "C8:48:05:00:00:01", Serial: "XKW1", Side: "L", Player: 2, Firmware: "2.1.4", Interval: "15ms", State: "connected", Device: 7001, Known: true}}}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("status\n got %+v\nwant %+v", st, want)
	}
	var raw map[string]any
	if err := client.CallResult(ctx, "joycon2.pair", map[string]any{"seconds": 20}, &raw); err != nil {
		t.Fatal(err)
	}
	ends, left := raw["windowEndsAt"].(float64), raw["windowLeftMs"].(float64)
	if raw["windowOpen"] != true || left <= 0 || left > 20000 || time.Until(time.UnixMilli(int64(ends))) > 20*time.Second || ends <= float64(time.Now().UnixMilli()) {
		t.Fatalf("window: %v", raw)
	}
}

// TestIPCJoyCon2Unavailable: a source without the backend answers status with
// available:false, and every other method with -32027.
func TestIPCJoyCon2Unavailable(t *testing.T) {
	plain := func(tl *testLive) {
		client := rpcTestClient(t, tl.liveRun)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var st rpcJoyCon2Status
		if err := client.CallResult(ctx, "joycon2.status", nil, &st); err != nil || st.Available || st.Unavailable == "" || st.Known == nil || st.Links == nil {
			t.Fatalf("status = %+v, %v", st, err)
		}
		for method, params := range map[string]any{"joycon2.pair": nil, "joycon2.cancelPair": nil,
			"joycon2.forget": map[string]any{"serial": "X"}, "joycon2.disconnect": map[string]any{"id": 5}} {
			if err := client.CallResult(ctx, method, params, new(any)); err == nil || rpcCodeOf(t, err) != rpcCodeJoyCon2Unavailable {
				t.Errorf("%s: %v, want -32027", method, err)
			}
		}
		// Parameters are checked first, so a bad request is a bad request.
		if err := client.CallResult(ctx, "joycon2.pair", map[string]any{"seconds": 0}, new(any)); err == nil || rpcCodeOf(t, err) != jrpc2.InvalidParams {
			t.Errorf("pair with 0 s: %v", err)
		}
	}
	t.Run("a source without it", func(t *testing.T) {
		tl, _ := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
		plain(tl)
	})
	t.Run("a backend that did not start", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, func(s *joySource) {
			s.avail = fmt.Errorf("%w: the transport did not start", input.ErrJoyCon2Unavailable)
		})
		plain(tl)
		if len(src.log()) != 0 {
			t.Fatalf("an unavailable backend was called: %v", src.log())
		}
	})
}

// TestJoyCon2RememberedPersists: OnRemember hands the entry to state.json
// through the persistence worker, and joycon2.forget takes it out again.
func TestJoyCon2RememberedPersists(t *testing.T) {
	tl, src, dir := joyLive(t, `{"schemaVersion":0}`, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	lib := profilelib.New(dir)
	saved := func() []string {
		state, err := lib.ReadState()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range state.JoyCon2 {
			out = append(out, c.Addr+" "+c.Serial+" "+c.Side)
		}
		return out
	}
	if err := client.CallResult(ctx, "joycon2.pair", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	src.pairController(jc2.SideL, "XKW1", joyLeftAddr)
	tl.waitUntil(t, "state.json to hold the left Joy-Con", func() bool {
		return slices.Equal(saved(), []string{"C8:48:05:00:00:01 XKW1 L"})
	})
	// The last profile, which the same worker writes, is still there.
	if state, err := lib.ReadState(); err != nil || state.LastProfile != "default" && state.LastProfile != "" {
		t.Fatalf("state %+v, %v", state, err)
	}
	src.pairController(jc2.SideR, "XKW2", joyRightAddr)
	tl.waitUntil(t, "state.json to hold both", func() bool {
		return slices.Equal(saved(), []string{"C8:48:05:00:00:01 XKW1 L", "C8:48:05:00:00:02 XKW2 R"})
	})
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "XKW1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	tl.waitUntil(t, "state.json to forget the left one", func() bool {
		return slices.Equal(saved(), []string{"C8:48:05:00:00:02 XKW2 R"})
	})
	// An entry state.json has and the backend does not is forgotten too; one
	// that neither has is -32004.
	tl.cfg.opts.memory.RememberJoyCon2(profile.JoyCon2Controller{Addr: "C8:48:05:00:00:01", Serial: "ONLYSTATE", Side: "L"})
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "ONLYSTATE"}, new(any)); err != nil {
		t.Fatalf("forget of an entry only state.json has: %v", err)
	}
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "ONLYSTATE"}, new(any)); err == nil || rpcCodeOf(t, err) != -32004 {
		t.Fatalf("second forget: %v", err)
	}
	if got := tl.cfg.opts.memory.JoyCon2(); len(got) != 1 || got[0].Serial != "XKW2" {
		t.Fatalf("memory %+v", got)
	}
}

// TestJoyCon2RememberedLastsAcrossRestart: the next start hands what was
// remembered to the backend.
func TestJoyCon2RememberedLastsAcrossRestart(t *testing.T) {
	dir := t.TempDir()
	run := func() *joySource {
		src := newJoySource()
		tl, _ := startLibraryLive(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true, watch: false}, func(cfg *liveConfig) {
			cfg.joyCon2 = true
			cfg.open = func(o input.Options) (eventSource, error) { src.opts = o.JoyCon2; return src, nil }
		})
		if len(src.opts.Known) == 0 {
			src.mu.Lock()
			src.status.WindowOpen = true
			src.mu.Unlock()
			src.pairController(jc2.SideR, "XKW9", joyRightAddr)
		}
		tl.cancel() // the exit path flushes state.json
		tl.waitCode(t)
		return src
	}
	run()
	second := run()
	if len(second.opts.Known) != 1 || second.opts.Known[0].Serial != "XKW9" || second.opts.Known[0].Side != jc2.SideR || second.opts.Known[0].Addr != joyRightAddr {
		t.Fatalf("second start Known = %+v", second.opts.Known)
	}
}

// TestIPCJoyCon2ChangedEvent: a change of the status is pushed as
// joycon2.changed: a window that opens, a controller that connects and is
// remembered, a window that runs out, a forgotten controller. Nothing is
// pushed when nothing changed.
func TestIPCJoyCon2ChangedEvent(t *testing.T) {
	tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
	events := make(chan rpcJoyCon2Status, 32)
	client := rpcTestClient(t, tl.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		if r.Method() != joyCon2Topic {
			return
		}
		var st rpcJoyCon2Status
		if err := r.UnmarshalParams(&st); err != nil {
			t.Error(err)
		}
		events <- st
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := client.CallResult(ctx, "subscribe", map[string]any{"topics": []string{joyCon2Topic}}, new(any)); err != nil {
		t.Fatal(err)
	}
	next := func(what string) rpcJoyCon2Status {
		t.Helper()
		select {
		case st := <-events:
			return st
		case <-ctx.Done():
			t.Fatal("no event:", what)
			return rpcJoyCon2Status{}
		}
	}
	quiet := func(what string) {
		t.Helper()
		select {
		case st := <-events:
			t.Fatalf("%s: event %+v", what, st)
		case <-time.After(700 * time.Millisecond):
		}
	}
	tl.waitUntil(t, "the watcher's first status", func() bool { return slices.Contains(src.log(), "auto true") })
	quiet("at rest")

	// The window opens (by a method).
	if err := client.CallResult(ctx, "joycon2.pair", map[string]any{"seconds": 30}, new(any)); err != nil {
		t.Fatal(err)
	}
	if st := next("window opened"); !st.WindowOpen || st.WindowEndsAt == 0 || len(st.Known) != 0 {
		t.Fatalf("window opened: %+v", st)
	}
	quiet("window open, nothing changes")

	// A controller connects: the gamepad's DeviceAdded is what kicks the
	// watcher.
	src.pairController(jc2.SideL, "XKW1", joyLeftAddr)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: 5001, Info: &input.DeviceInfo{ID: 5001, Name: "Joy-Con 2 (L)", Driver: "ble", Type: "joyconleft", Serial: "XKW1", Path: "ble:C8:48:05:00:00:01", GUID: strings.Repeat("b1", 16), IsGamepad: true}}
	st := next("controller remembered")
	if st.WindowOpen || len(st.Known) != 1 || st.Known[0].Serial != "XKW1" || !st.Known[0].Connected || len(st.Links) != 1 || st.Links[0].Device != 5001 {
		t.Fatalf("remembered: %+v", st)
	}

	// The window of a second try runs out; nothing kicks the watcher, which
	// polls while a window is open.
	if err := client.CallResult(ctx, "joycon2.pair", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	if st := next("second window"); !st.WindowOpen {
		t.Fatalf("second window: %+v", st)
	}
	src.closeWindow()
	if st := next("window ran out"); st.WindowOpen {
		t.Fatalf("window ran out: %+v", st)
	}

	// The link drops by itself.
	src.mu.Lock()
	src.status.Links, src.status.Known[0].Connected = nil, false
	src.mu.Unlock()
	src.events <- input.Event{Kind: input.DeviceRemoved, Device: 5001}
	if st := next("link dropped"); st.Known[0].Connected || len(st.Links) != 0 {
		t.Fatalf("link dropped: %+v", st)
	}

	// Forgetting (by a method) pushes too, once.
	if err := client.CallResult(ctx, "joycon2.forget", map[string]any{"serial": "XKW1"}, new(any)); err != nil {
		t.Fatal(err)
	}
	if st := next("forgotten"); len(st.Known) != 0 {
		t.Fatalf("forgotten: %+v", st)
	}
	quiet("after the last change")
	// A status request that finds nothing new publishes nothing.
	if err := client.CallResult(ctx, "joycon2.status", nil, new(any)); err != nil {
		t.Fatal(err)
	}
	quiet("status request")
}

// TestSameJoyCon2: the time left and the clock's jitter are no change; a
// window that is opened again is.
func TestSameJoyCon2(t *testing.T) {
	base := rpcJoyCon2Status{Available: true, WindowOpen: true, WindowEndsAt: 1_000_000, WindowLeftMs: 30000, Known: []rpcJoyCon2Known{}, Links: []rpcJoyCon2Link{}}
	tests := []struct {
		name string
		edit func(*rpcJoyCon2Status)
		same bool
	}{
		{"time left", func(s *rpcJoyCon2Status) { s.WindowLeftMs = 12000 }, true},
		{"clock jitter", func(s *rpcJoyCon2Status) { s.WindowEndsAt += 3 }, true},
		{"window opened again", func(s *rpcJoyCon2Status) { s.WindowEndsAt += 20_000 }, false},
		{"window closed", func(s *rpcJoyCon2Status) { s.WindowOpen, s.WindowEndsAt, s.WindowLeftMs = false, 0, 0 }, false},
		{"scan", func(s *rpcJoyCon2Status) { s.Scanning = true }, false},
		{"switch", func(s *rpcJoyCon2Status) { s.AutoConnect = true }, false},
		{"known", func(s *rpcJoyCon2Status) { s.Known = []rpcJoyCon2Known{{Serial: "x"}} }, false},
		{"link state", func(s *rpcJoyCon2Status) { s.Links = []rpcJoyCon2Link{{State: "connecting"}} }, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			other := base
			tc.edit(&other)
			if got := sameJoyCon2(base, other); got != tc.same {
				t.Fatalf("sameJoyCon2 = %v", got)
			}
		})
	}
}

// TestJoyCon2ConfigHotApply: joycon2.autoConnect applies at once, needs no
// restart, and reaches the backend only when it changed.
func TestJoyCon2ConfigHotApply(t *testing.T) {
	tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	tl.waitUntil(t, "the watcher's first call", func() bool { return slices.Contains(src.log(), "auto true") })
	put := func(text string) rpcSaved {
		t.Helper()
		var doc rpcDocument
		if err := client.CallResult(ctx, "config.get", nil, &doc); err != nil {
			t.Fatal(err)
		}
		var saved rpcSaved
		if err := client.CallResult(ctx, "config.put", map[string]string{"text": text, "revision": doc.Revision}, &saved); err != nil {
			t.Fatal(err)
		}
		return saved
	}
	reset := func() { src.mu.Lock(); src.calls = nil; src.mu.Unlock() }

	reset()
	saved := put(`{"schemaVersion":0,"joycon2":{"autoConnect":false}}`)
	if !saved.Applied || saved.RestartRequired {
		t.Fatalf("saved %+v", saved)
	}
	if got := src.log(); !slices.Equal(got, []string{"auto false"}) {
		t.Fatalf("calls %v", got)
	}
	if tl.selectionConfig().JoyCon2.AutoConnect {
		t.Fatal("the effective config still has auto-connect on")
	}
	// Another change leaves it alone.
	reset()
	if saved := put(`{"schemaVersion":0,"joycon2":{"autoConnect":false},"feedback":{"rumble":false}}`); !saved.Applied {
		t.Fatalf("saved %+v", saved)
	}
	if got := src.log(); len(got) != 0 {
		t.Fatalf("calls %v", got)
	}
	// Back to the default: the member may be left out.
	reset()
	if saved := put(`{"schemaVersion":0}`); !saved.Applied || saved.RestartRequired {
		t.Fatalf("saved %+v", saved)
	}
	if got := src.log(); !slices.Equal(got, []string{"auto true"}) {
		t.Fatalf("calls %v", got)
	}
	var st rpcJoyCon2Status
	if err := client.CallResult(ctx, "joycon2.status", nil, &st); err != nil || !st.AutoConnect {
		t.Fatalf("status %+v, %v", st, err)
	}
}

// TestJoyCon2AutoConnectNeedsNoRestart: the field is no startup field.
func TestJoyCon2AutoConnectNeedsNoRestart(t *testing.T) {
	a, b := profileConfig(t, `{"schemaVersion":0}`), profileConfig(t, `{"schemaVersion":0,"joycon2":{"autoConnect":false}}`)
	sa, sb := startupFields(a), startupFields(b)
	if !reflect.DeepEqual(sa, sb) {
		t.Fatal("joycon2.autoConnect is a startup field")
	}
	next := liveSettings(a, b)
	if next.JoyCon2.AutoConnect {
		t.Fatal("liveSettings keeps the startup value")
	}
}

// TestJoyCon2TrayFlow: a click on the tray's item opens the 30 s window and
// says so; the notices follow what becomes of it. A window another client
// opened gets none.
func TestJoyCon2TrayFlow(t *testing.T) {
	notices := func(tl *testLive) []string {
		var out []string
		for {
			n, ok := tl.notices.Take()
			if !ok {
				return out
			}
			if n.Title != "Joy-Con 2" {
				t.Errorf("title %q", n.Title)
			}
			out = append(out, n.Body)
		}
	}
	// waitNotice waits for the next notice, which the watcher posts.
	waitNotice := func(tl *testLive) []string {
		var out []string
		deadline := time.Now().Add(liveWait)
		for len(out) == 0 && time.Now().Before(deadline) {
			if out = notices(tl); len(out) == 0 {
				time.Sleep(10 * time.Millisecond)
			}
		}
		return out
	}
	const start = "请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。"
	t.Run("left remembered", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		tl.trayJoyCon2()
		if got := src.log(); !slices.Contains(got, "pair 30s") {
			t.Fatalf("calls %v", got)
		}
		if got := notices(tl); !slices.Equal(got, []string{start}) {
			t.Fatalf("notices %q", got)
		}
		src.pairController(jc2.SideL, "XKW1", joyLeftAddr)
		if got := notices(tl); !slices.Equal(got, []string{"已连接并记住 Joy-Con 2（左）。"}) {
			t.Fatalf("notices %q", got)
		}
		// The window closing after that says nothing more.
		time.Sleep(600 * time.Millisecond)
		if got := notices(tl); len(got) != 0 {
			t.Fatalf("extra notices %q", got)
		}
	})
	t.Run("right remembered", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		tl.trayJoyCon2()
		_ = notices(tl)
		src.pairController(jc2.SideR, "XKW2", joyRightAddr)
		if got := notices(tl); !slices.Equal(got, []string{"已连接并记住 Joy-Con 2（右）。"}) {
			t.Fatalf("notices %q", got)
		}
	})
	t.Run("nothing found", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		tl.trayJoyCon2()
		_ = notices(tl)
		src.closeWindow() // the time is up
		if got := waitNotice(tl); !slices.Equal(got, []string{"30 秒内没有找到 Joy-Con 2。"}) {
			t.Fatalf("notices %q", got)
		}
	})
	t.Run("a window of the IPC", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		client := rpcTestClient(t, tl.liveRun)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := client.CallResult(ctx, "joycon2.pair", nil, new(any)); err != nil {
			t.Fatal(err)
		}
		src.pairController(jc2.SideL, "XKW1", joyLeftAddr)
		time.Sleep(600 * time.Millisecond)
		if got := notices(tl); len(got) != 0 {
			t.Fatalf("notices %q for a window the tray did not open", got)
		}
	})
	t.Run("a tray window the IPC then took over or canceled", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		client := rpcTestClient(t, tl.liveRun)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		tl.trayJoyCon2()
		_ = notices(tl)
		if err := client.CallResult(ctx, "joycon2.cancelPair", nil, new(any)); err != nil {
			t.Fatal(err)
		}
		time.Sleep(600 * time.Millisecond)
		if got := notices(tl); len(got) != 0 {
			t.Fatalf("notices %q after a cancel", got)
		}
		_ = src
	})
	t.Run("no Joy-Con 2 support", func(t *testing.T) {
		tl, _ := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
		tl.trayJoyCon2()
		if got := notices(tl); len(got) != 0 {
			t.Fatalf("notices %q", got)
		}
	})
}

// TestJoyCon2TrayItem: the tray offers the item exactly when the daemon has
// Joy-Con 2 support, and a click on it opens the window and shows the notice.
func TestJoyCon2TrayItem(t *testing.T) {
	run := func(t *testing.T, tl *testLive) (shown func() []bool, click func(), notified <-chan tray.Notification) {
		t.Helper()
		var mu sync.Mutex
		var seen []bool
		clicks := make(chan struct{}, 1)
		notes := make(chan tray.Notification, 8)
		closed := make(chan struct{})
		var once sync.Once
		ui := &trayUI{pause: make(chan struct{}), quit: make(chan struct{}), joycon2: clicks,
			update: func(p tray.Presentation, _, _ bool) {
				mu.Lock()
				defer mu.Unlock()
				seen = append(seen, p.JoyCon2)
			},
			notify: func(n tray.Notification) error { notes <- n; return nil },
			close:  func() { once.Do(func() { close(closed) }) },
			run:    func(ready, exit func(), _ func(error)) { ready(); <-closed; exit() },
		}
		done := make(chan int, 1)
		go func() { done <- tl.driveTray(ui) }()
		t.Cleanup(func() { tl.cancel(); <-done })
		return func() []bool { mu.Lock(); defer mu.Unlock(); return slices.Clone(seen) },
			func() { clicks <- struct{}{} }, notes
	}
	t.Run("with support", func(t *testing.T) {
		tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
		shown, click, notified := run(t, tl)
		tl.waitUntil(t, "a presentation", func() bool { return len(shown()) > 0 })
		for _, v := range shown() {
			if !v {
				t.Fatal("the item is hidden although Joy-Con 2 is available")
			}
		}
		click()
		select {
		case n := <-notified:
			if n.Body != "请在 30 秒内按住 Joy-Con 2 侧面的 SYNC 键。" {
				t.Fatalf("notice %+v", n)
			}
		case <-time.After(liveWait):
			t.Fatal("no notice after the click")
		}
		if !slices.Contains(src.log(), "pair 30s") {
			t.Fatalf("calls %v", src.log())
		}
	})
	t.Run("backend did not start", func(t *testing.T) {
		tl, _, _ := joyLive(t, `{"schemaVersion":0}`, func(s *joySource) {
			s.avail = fmt.Errorf("%w: no adapter", input.ErrJoyCon2Unavailable)
		})
		shown, _, _ := run(t, tl)
		tl.waitUntil(t, "a presentation", func() bool { return len(shown()) > 0 })
		for _, v := range shown() {
			if v {
				t.Fatal("the item is shown although Joy-Con 2 is unavailable")
			}
		}
	})
	t.Run("a source without it", func(t *testing.T) {
		tl, _ := sessionLive(t, nil, nil, "--dry-run", "--watch=false")
		shown, _, _ := run(t, tl)
		tl.waitUntil(t, "a presentation", func() bool { return len(shown()) > 0 })
		for _, v := range shown() {
			if v {
				t.Fatal("the item is shown for a source without Joy-Con 2")
			}
		}
	})
}

// TestDevicesListJoyCon2: a Joy-Con 2 gamepad is an ordinary device of
// devices.list: its power comes as DevicePower and shows as battery, its
// removal (a silent controller, a lost link) takes it and its battery away,
// and the same controller coming back under a new connection ID has the same
// DeviceKey and no battery until it reads one.
func TestDevicesListJoyCon2(t *testing.T) {
	tl, src, _ := joyLive(t, `{"schemaVersion":0}`, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	list := func() map[input.DeviceID]supervisor.DeviceView {
		t.Helper()
		var rows []supervisor.DeviceView
		if err := client.CallResult(ctx, "devices.list", nil, &rows); err != nil {
			t.Fatal(err)
		}
		out := map[input.DeviceID]supervisor.DeviceView{}
		for _, v := range rows {
			out[v.Info.ID] = v
		}
		return out
	}
	info := func(id input.DeviceID) *input.DeviceInfo {
		return &input.DeviceInfo{ID: id, Name: "Joy-Con 2 (L)", Driver: "ble", Type: "joyconleft", Serial: "XKW1", Path: "ble:C8:48:05:00:00:01",
			GUID: strings.Repeat("b1", 16), IsGamepad: true, Product: jc2.ProductL}
	}
	const first, second = input.DeviceID(5001), input.DeviceID(5002)
	src.events <- input.Event{Kind: input.DeviceAdded, Device: first, Info: info(first)}
	tl.waitUntil(t, "the Joy-Con 2 in devices.list", func() bool { return list()[first].Info.ID == first })
	v := list()[first]
	if v.Status != "mapped" || v.Slot < 0 || v.Battery != nil || v.Info.Type != "joyconleft" || v.Info.Driver != "ble" {
		t.Fatalf("device %+v", v)
	}
	key := v.Key

	for _, p := range []struct {
		power input.Power
		want  string
	}{
		{input.Power{State: input.PowerDischarging, Percent: 66}, `{discharging 66}`},
		{input.Power{State: input.PowerCharging, Percent: 100}, `{charging 100}`},
	} {
		src.events <- input.Event{Kind: input.DevicePower, Device: first, Power: &p.power}
		tl.waitUntil(t, "the battery "+p.want, func() bool {
			b := list()[first].Battery
			return b != nil && *b == p.power
		})
	}

	// The link goes silent: the gamepad is removed and takes its battery along.
	src.events <- input.Event{Kind: input.DeviceRemoved, Device: first}
	tl.waitUntil(t, "the removal", func() bool { _, ok := list()[first]; return !ok })
	// It resumes: a new connection ID, the same DeviceKey, no battery yet.
	src.events <- input.Event{Kind: input.DeviceAdded, Device: second, Info: info(second)}
	tl.waitUntil(t, "the controller back", func() bool { _, ok := list()[second]; return ok })
	back := list()[second]
	if back.Key != key || back.Battery != nil || back.Status != "mapped" {
		t.Fatalf("back %+v, key before %s", back, key)
	}
	low := input.Power{State: input.PowerDischarging, Percent: 15}
	src.events <- input.Event{Kind: input.DevicePower, Device: second, Power: &low}
	tl.waitUntil(t, "the battery after the return", func() bool { b := list()[second].Battery; return b != nil && *b == low })

	// status.get shows the same devices.
	var st rpcStatus
	if err := client.CallResult(ctx, "status.get", nil, &st); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range st.Devices {
		if d.Info.ID == second && d.Battery != nil && d.Battery.Percent == 15 {
			found = true
		}
	}
	if !found {
		t.Fatalf("status.get devices %+v", st.Devices)
	}
}

func profileConfig(t *testing.T, text string) *profile.Config {
	t.Helper()
	c, ds := profile.ParseConfig([]byte(text))
	if c == nil {
		t.Fatal(ds)
	}
	return c
}
