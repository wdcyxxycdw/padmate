package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"github.com/wdcyxxycdw/padmate/daemon/internal/winhook"
)

func TestRecordFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"--swallow", "all"}, `--swallow must be none, padmate or injected, not "all"`},
		{[]string{"--duration", "-1s"}, "--duration must not be negative"},
		{[]string{"--count", "-3"}, "--count must not be negative"},
		// A mouse hook swallows by nonce: all of these stop before any hook is installed.
		{[]string{"--mouse", "--swallow", "padmate"}, "--mouse with --swallow padmate needs --nonce N"},
		{[]string{"--nonce", "5"}, "--nonce only goes with --mouse and --swallow padmate"},
		{[]string{"--mouse", "--nonce", "5"}, "--nonce only goes with --mouse and --swallow padmate"},
		{[]string{"--mouse", "--swallow", "injected", "--nonce", "5"}, "--nonce only goes with --mouse and --swallow padmate"},
		{[]string{"--swallow", "padmate", "--nonce", "5"}, "--nonce only goes with --mouse and --swallow padmate"},
		{[]string{"--mouse", "--swallow", "padmate", "--nonce", "4294967296"}, "--nonce must fit in 32 bits"},
		{[]string{"extra"}, `unexpected argument "extra"`},
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"--duration", "soon"}, `invalid value "soon" for flag -duration`},
	} {
		code, stdout, stderr := runProbe(t, append([]string{"record"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: padmate-probe record") {
			t.Errorf("record %q = %d, stdout %q, stderr %q; want 2 and %q with the usage", tc.args, code, stdout, stderr, tc.want)
		}
	}
}

func TestSwallowModes(t *testing.T) {
	padmate := winhook.Event{Flags: winhook.FlagInjected, Extra: output.ExtraInfo(42)}
	other := winhook.Event{Flags: winhook.FlagInjected, Extra: 0x1234}
	physical := winhook.Event{Extra: output.ExtraInfo(42)} // not injected, tagged anyway
	if swallowModes["none"] != nil {
		t.Error("--swallow none installs a predicate")
	}
	for _, tc := range []struct {
		mode string
		e    winhook.Event
		want bool
	}{
		{"padmate", padmate, true},
		{"padmate", other, false},
		{"padmate", winhook.Event{}, false},
		{"injected", padmate, true},
		{"injected", other, true},
		{"injected", winhook.Event{}, false},
		{"injected", physical, false},
	} {
		if got := swallowModes[tc.mode](tc.e); got != tc.want {
			t.Errorf("swallow %s(%+v) = %v, want %v", tc.mode, tc.e, got, tc.want)
		}
	}
}

func TestFormatEvent(t *testing.T) {
	for _, tc := range []struct {
		e    winhook.Event
		want string
	}{
		{
			winhook.Event{Time: 52830015, VK: 0xA3, Scan: 0x1D, Flags: 0x11, Extra: output.ExtraInfo(8812), Msg: winhook.WMKeyDown, Swallowed: true},
			"  52830015 down sc=E0 1D vk=0xA3 injected padmate nonce=8812 swallowed",
		},
		{
			winhook.Event{Time: 7, VK: 0xA5, Scan: 0x38, Flags: 0xA1, Msg: winhook.WMSysKeyUp},
			"         7 up   sc=E0 38 vk=0xA5 sys alt",
		},
		{
			winhook.Event{Time: 100, VK: 0x5A, Scan: 0x2C, Flags: 0x12, Extra: 0x1234, Msg: winhook.WMKeyDown},
			"       100 down sc=0x2C vk=0x5A injected lowerIL extra=0x1234",
		},
		{
			winhook.Event{Time: 1, VK: 0x41, Scan: 0x1E, Msg: 0x0200},
			"         1 down sc=0x1E vk=0x41 msg=0x0200",
		},
		{
			winhook.Event{Time: 12, VK: 0xE7, Scan: 0x4F60, Flags: 0x10, Extra: output.ExtraInfo(5), Msg: winhook.WMKeyDown, Swallowed: true},
			"        12 down sc=0x4F60 vk=0xE7 text U+4F60 injected padmate nonce=5 swallowed",
		},
		{
			winhook.Event{Time: 13, VK: 0xE7, Scan: 0xD83D, Flags: 0x90, Msg: winhook.WMKeyUp},
			"        13 up   sc=0xD83D vk=0xE7 text U+D83D injected",
		},
	} {
		if got := formatEvent(tc.e); got != tc.want {
			t.Errorf("formatEvent(%+v)\n got %q\nwant %q", tc.e, got, tc.want)
		}
	}
}

func TestEventJSON(t *testing.T) {
	e := winhook.Event{Time: 5, VK: 0x7C, Scan: 0x64, Flags: winhook.FlagInjected | winhook.FlagUp, Extra: output.ExtraInfo(77), Msg: winhook.WMKeyUp, Swallowed: true}
	b, err := json.Marshal(newEventJSON(3, e))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"seq":3,"time":5,"msg":"WM_KEYUP","vk":124,"scan":100,"ext":false,"up":true,"injected":true,` +
		`"lowerIL":false,"altDown":false,"extra":"0x7061646D0000004D","padmate":true,"nonce":77,"swallowed":true}`
	if string(b) != want {
		t.Errorf("JSON\n got %s\nwant %s", b, want)
	}
	other := newEventJSON(1, winhook.Event{Extra: 77})
	if other.Padmate || other.Nonce != 0 || other.Extra != "0x4D" {
		t.Errorf("untagged event: %+v, want padmate false, nonce 0, extra 0x4D", other)
	}
}

// fakeSource is an eventSource fed by the test. Its mouse channel is nil, as
// without --mouse, unless the test makes it.
type fakeSource struct {
	ch       chan winhook.Event
	mouse    chan winhook.MouseEvent
	dropped  uint64
	closeErr error
	closes   int
}

func newFakeSource(events ...winhook.Event) *fakeSource {
	s := &fakeSource{ch: make(chan winhook.Event, len(events)+1)}
	for _, e := range events {
		s.ch <- e
	}
	return s
}

func (s *fakeSource) Events() <-chan winhook.Event { return s.ch }
func (s *fakeSource) Dropped() uint64              { return s.dropped }

func (s *fakeSource) MouseEvents() <-chan winhook.MouseEvent { return s.mouse }

// Close closes the channels unless it fails, as winhook.Hook.Close does.
func (s *fakeSource) Close() error {
	if s.closes == 0 && s.closeErr == nil {
		close(s.ch)
		if s.mouse != nil {
			close(s.mouse)
		}
	}
	s.closes++
	return s.closeErr
}

func keyEvents(n int) []winhook.Event {
	out := make([]winhook.Event, n)
	for i := range out {
		out[i] = winhook.Event{Time: uint32(i + 1), VK: 0x41, Scan: 0x1E, Msg: winhook.WMKeyDown}
	}
	return out
}

func TestRecordLoop(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name       string
		ctx        context.Context
		src        func() *fakeSource
		opts       recordOptions
		wantLines  int
		wantCode   int
		wantStderr string
	}{
		{
			name:      "count stops early",
			ctx:       context.Background(),
			src:       func() *fakeSource { return newFakeSource(keyEvents(5)...) },
			opts:      recordOptions{count: 3},
			wantLines: 3,
		},
		{
			name: "closed channel ends",
			ctx:  context.Background(),
			src: func() *fakeSource {
				s := newFakeSource(keyEvents(2)...)
				close(s.ch)
				s.closes = 1 // already closed by the "hook"
				return s
			},
			wantLines: 2,
		},
		{
			name:      "cancel prints what the hook saw",
			ctx:       canceled,
			src:       func() *fakeSource { return newFakeSource(keyEvents(4)...) },
			wantLines: 4,
		},
		{
			name:      "count also bounds the drain",
			ctx:       canceled,
			src:       func() *fakeSource { return newFakeSource(keyEvents(4)...) },
			opts:      recordOptions{count: 2},
			wantLines: 2,
		},
		{
			name:      "duration ends an idle recording",
			ctx:       context.Background(),
			src:       func() *fakeSource { return newFakeSource() },
			opts:      recordOptions{duration: 20 * time.Millisecond},
			wantLines: 0,
		},
		{
			name: "dropped events are reported",
			ctx:  canceled,
			src: func() *fakeSource {
				s := newFakeSource(keyEvents(1)...)
				s.dropped = 9
				return s
			},
			wantLines:  1,
			wantStderr: "the hook dropped 9 events",
		},
		{
			// The channel stays open after a failed Close: record must not
			// wait for it to end.
			name: "close error fails",
			ctx:  canceled,
			src: func() *fakeSource {
				s := newFakeSource()
				s.closeErr = errors.New("unhook failed")
				return s
			},
			wantCode:   1,
			wantStderr: "padmate-probe record: unhook failed",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := tc.src()
			var out, errOut bytes.Buffer
			done := make(chan int)
			go func() { done <- record(tc.ctx, src, tc.opts, &out, &errOut) }()
			var code int
			select {
			case code = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("record did not return")
			}
			if code != tc.wantCode {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, tc.wantCode, errOut.String())
			}
			if n := strings.Count(out.String(), "\n"); n != tc.wantLines {
				t.Errorf("printed %d lines, want %d:\n%s", n, tc.wantLines, out.String())
			}
			if tc.wantStderr == "" && errOut.Len() > 0 || !strings.Contains(errOut.String(), tc.wantStderr) {
				t.Errorf("stderr = %q, want %q", errOut.String(), tc.wantStderr)
			}
			if src.closes == 0 {
				t.Error("record did not close the source")
			}
		})
	}
}

func TestRecordJSONLines(t *testing.T) {
	src := newFakeSource(keyEvents(3)...)
	var out, errOut bytes.Buffer
	if code := record(context.Background(), src, recordOptions{json: true, count: 3}, &out, &errOut); code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines:\n%s", len(lines), out.String())
	}
	for i, line := range lines {
		var e eventJSON
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d: %v", i, err)
		}
		if e.Seq != uint64(i+1) || e.Time != uint32(i+1) || e.Msg != "WM_KEYDOWN" {
			t.Errorf("line %d = %+v", i, e)
		}
	}
}

// failWriter fails every write.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("pipe closed") }

func TestRecordWriteError(t *testing.T) {
	src := newFakeSource(keyEvents(3)...)
	var errOut bytes.Buffer
	code := record(context.Background(), src, recordOptions{}, failWriter{}, &errOut)
	if code != 1 || strings.Count(errOut.String(), "pipe closed") != 1 {
		t.Errorf("exit code %d, stderr %q; want 1 and one error", code, errOut.String())
	}
	if src.closes == 0 {
		t.Error("record did not close the source")
	}
}
