package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/output"
)

// TestMain disables the real sender: no unit test of this package may
// inject keys. Tests that exercise the sending path install a fakeSender;
// the injection test builds and runs the binary instead. release-all
// signals an event of the tests' own, never a running padmated's (DMN-20).
func TestMain(m *testing.M) {
	newSender = func() (output.Sender, error) {
		return nil, errors.New("test: the real sender is disabled in unit tests")
	}
	releaseAllEvent = fmt.Sprintf(`Local\padmate.padmatectl-test-%d`, os.Getpid())
	ipcDiscoveryDir = filepath.Join(os.TempDir(), fmt.Sprintf("padmatectl-unit-tests-%d-no-daemon", os.Getpid()))
	os.Exit(m.Run())
}

// fakeSender records the batches it is given.
type fakeSender struct {
	batches [][]output.KeyEvent
	// onSend, if set, runs before each batch is recorded; a non-nil error
	// fails the batch with nothing injected.
	onSend func(batch int) error
}

func (f *fakeSender) Send(events []output.KeyEvent) (int, error) {
	if f.onSend != nil {
		if err := f.onSend(len(f.batches)); err != nil {
			return 0, err
		}
	}
	f.batches = append(f.batches, append([]output.KeyEvent(nil), events...))
	return len(events), nil
}

// lines formats the recorded events like --dry-run.
func (f *fakeSender) lines() string {
	var b strings.Builder
	for _, batch := range f.batches {
		for _, e := range batch {
			b.WriteString(e.String() + "\n")
		}
	}
	return b.String()
}

// useFakeSender makes the send command use f for the rest of the test.
func useFakeSender(t *testing.T, f *fakeSender) {
	t.Helper()
	prev := newSender
	newSender = func() (output.Sender, error) { return f, nil }
	t.Cleanup(func() { newSender = prev })
}

func runSendCtx(ctx context.Context, args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = sendMain(ctx, args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSendDryRun(t *testing.T) {
	for _, tc := range []struct {
		spec string
		want string
	}{
		{"Ctrl+Shift+Z", "" +
			"down Ctrl sc=0x1D\n" +
			"down Shift sc=0x2A\n" +
			"down Z sc=0x2C\n" +
			"up   Z sc=0x2C\n" +
			"up   Shift sc=0x2A\n" +
			"up   Ctrl sc=0x1D\n"},
		{"f13", "" +
			"down F13 sc=0x64\n" +
			"up   F13 sc=0x64\n"},
		// Plus is Shift+= on the US layout: LShift is pressed around it.
		{"Plus", "" +
			"down LShift sc=0x2A\n" +
			"down Plus sc=0x0D\n" +
			"up   Plus sc=0x0D\n" +
			"up   LShift sc=0x2A\n"},
		{"RAlt", "" +
			"down RAlt sc=E0 38\n" +
			"up   RAlt sc=E0 38\n"},
		// Pause (E1 1D 45) goes by VK.
		{"Pause", "" +
			"down Pause vk=0x13 sc=0x45\n" +
			"up   Pause vk=0x13 sc=0x45\n"},
		{"sc:0xe01d", "" +
			"down sc:0xE01D sc=E0 1D\n" +
			"up   sc:0xE01D sc=E0 1D\n"},
		// A modifier-only chord presses each modifier once.
		{"Shift+Ctrl", "" +
			"down Ctrl sc=0x1D\n" +
			"down Shift sc=0x2A\n" +
			"up   Shift sc=0x2A\n" +
			"up   Ctrl sc=0x1D\n"},
		// The numpad digits go by VK so NumLock does not change them.
		{"Num7", "" +
			"down Num7 vk=0x67 sc=0x47\n" +
			"up   Num7 vk=0x67 sc=0x47\n"},
	} {
		code, stdout, stderr := runCmd(t, "send", "--dry-run", tc.spec)
		if code != 0 || stdout != tc.want || stderr != "" {
			t.Errorf("send --dry-run %q = %d, stderr %q, stdout\n%s\nwant 0, empty stderr and\n%s", tc.spec, code, stderr, stdout, tc.want)
		}
	}
}

func TestSendDryRunSequence(t *testing.T) {
	// Chords are tapped in order; --hold only adds a wait in the middle.
	for _, args := range [][]string{
		{"send", "--dry-run", "Ctrl+Z", "F13"},
		{"send", "--dry-run", "--hold", "1ms", "Ctrl+Z", "F13"},
	} {
		code, stdout, stderr := runCmd(t, args...)
		want := "down Ctrl sc=0x1D\ndown Z sc=0x2C\nup   Z sc=0x2C\nup   Ctrl sc=0x1D\ndown F13 sc=0x64\nup   F13 sc=0x64\n"
		if code != 0 || stdout != want || stderr != "" {
			t.Errorf("%q = %d, stdout %q, stderr %q", args, code, stdout, stderr)
		}
	}
}

func TestSendDryRunJSON(t *testing.T) {
	code, stdout, stderr := runCmd(t, "send", "--dry-run", "--json", "Ctrl+Z", "Pause", "Ctrl")
	want := strings.Join([]string{
		`{"chord":"Ctrl+Z","up":false,"key":"Ctrl","scan":29,"ext":false,"byVK":false,"vk":0}`,
		`{"chord":"Ctrl+Z","up":false,"key":"Z","scan":44,"ext":false,"byVK":false,"vk":0}`,
		`{"chord":"Ctrl+Z","up":true,"key":"Z","scan":44,"ext":false,"byVK":false,"vk":0}`,
		`{"chord":"Ctrl+Z","up":true,"key":"Ctrl","scan":29,"ext":false,"byVK":false,"vk":0}`,
		`{"chord":"Pause","up":false,"key":"Pause","scan":69,"ext":false,"byVK":true,"vk":19}`,
		`{"chord":"Pause","up":true,"key":"Pause","scan":69,"ext":false,"byVK":true,"vk":19}`,
		`{"chord":"Ctrl","up":false,"key":"Ctrl","scan":29,"ext":false,"byVK":false,"vk":0}`,
		`{"chord":"Ctrl","up":true,"key":"Ctrl","scan":29,"ext":false,"byVK":false,"vk":0}`,
		``,
	}, "\n")
	if code != 0 || stdout != want || stderr != "" {
		t.Errorf("send --dry-run --json = %d, stderr %q, stdout\n%s\nwant\n%s", code, stderr, stdout, want)
	}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		var e sendEventJSON
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Errorf("%s: %v", line, err)
		}
	}
}

func TestSendFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, "padmatectl send: no key specs given"},
		{[]string{"--dry-run"}, "padmatectl send: no key specs given"},
		{[]string{"--json", "Ctrl+Z"}, "padmatectl send: --json needs --dry-run"},
		{[]string{"--delay", "-1s", "Ctrl+Z"}, "padmatectl send: --delay must not be negative"},
		{[]string{"--hold", "-5ms", "Ctrl+Z"}, "padmatectl send: --hold must not be negative"},
		{[]string{"--hold", "long", "Ctrl+Z"}, `invalid value "long" for flag -hold`},
		{[]string{"--bogus", "Ctrl+Z"}, "flag provided but not defined: -bogus"},
	} {
		f := &fakeSender{}
		useFakeSender(t, f)
		code, stdout, stderr := runCmd(t, append([]string{"send"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: padmatectl send") {
			t.Errorf("send %q = %d, stdout %q, stderr %q; want 2 and %q with the usage", tc.args, code, stdout, stderr, tc.want)
		}
		if len(f.batches) != 0 {
			t.Errorf("send %q sent %d batches", tc.args, len(f.batches))
		}
	}
	for _, help := range []string{"-h", "--help"} {
		code, stdout, stderr := runCmd(t, "send", help)
		if code != 0 || !strings.HasPrefix(stdout, "usage: padmatectl send") || stderr != "" {
			t.Errorf("send %s = %d, stdout %q, stderr %q", help, code, stdout, stderr)
		}
	}
}

func TestSendParseErrors(t *testing.T) {
	// Every spec is parsed before anything is sent; the errors use the
	// format of "keys parse" with the send prefix.
	for _, dryRun := range []bool{true, false} {
		f := &fakeSender{}
		useFakeSender(t, f)
		args := []string{"send", "Ctrl+Z", "Ctrl+Foo", "F13", "Z+Ctrl"}
		if dryRun {
			args = append(args[:1], append([]string{"--dry-run"}, args[1:]...)...)
		}
		code, stdout, stderr := runCmd(t, args...)
		want := strings.Join([]string{
			`padmatectl send: Ctrl+Foo: unknown key "Foo"`,
			`                      ^^^`,
			`padmatectl send: Z+Ctrl: "Z" is not a modifier; only the last key of a chord can be a non-modifier (write a sequence as an array of key specs)`,
			`                 ^`,
			``,
		}, "\n")
		if code != 1 || stdout != "" || stderr != want {
			t.Errorf("%q = %d, stdout %q, stderr\n%s\nwant 1, empty stdout and\n%s", args, code, stdout, stderr, want)
		}
		if len(f.batches) != 0 {
			t.Errorf("%q sent %d batches before failing", args, len(f.batches))
		}
	}
}

// send is a keyboard dev tool: a mouse key, a wheel notch or a button, is
// rejected before anything is sent, with or without --dry-run (ADR-0062).
func TestSendRejectsMouseKeys(t *testing.T) {
	for _, dryRun := range []bool{true, false} {
		f := &fakeSender{}
		useFakeSender(t, f)
		args := []string{"send", "Ctrl+Z", "wheelup", "Ctrl+WheelDown", "mouseleft", "Shift+RButton", "XButton2", "F13"}
		if dryRun {
			args = append(args[:1], append([]string{"--dry-run"}, args[1:]...)...)
		}
		code, stdout, stderr := runCmd(t, args...)
		want := strings.Join([]string{
			`padmatectl send: wheelup: WheelUp is a mouse key; send only taps keyboard keys`,
			`padmatectl send: Ctrl+WheelDown: WheelDown is a mouse key; send only taps keyboard keys`,
			`padmatectl send: mouseleft: MouseLeft is a mouse key; send only taps keyboard keys`,
			`padmatectl send: Shift+RButton: MouseRight is a mouse key; send only taps keyboard keys`,
			`padmatectl send: XButton2: MouseX2 is a mouse key; send only taps keyboard keys`,
			``,
		}, "\n")
		if code != 1 || stdout != "" || stderr != want {
			t.Errorf("%q = %d, stdout %q, stderr\n%s\nwant 1, empty stdout and\n%s", args, code, stdout, stderr, want)
		}
		if len(f.batches) != 0 {
			t.Errorf("%q sent %d batches before failing", args, len(f.batches))
		}
	}
}

func TestSendInjects(t *testing.T) {
	f := &fakeSender{}
	useFakeSender(t, f)
	code, stdout, stderr := runCmd(t, "send", "Ctrl+Shift+Z", "F13")
	if code != 0 || stdout != "" || stderr != "" {
		t.Fatalf("send = %d, stdout %q, stderr %q; want 0 and no output", code, stdout, stderr)
	}
	// With --hold 0 each chord is one batch: one SendInput call.
	if len(f.batches) != 2 || len(f.batches[0]) != 6 || len(f.batches[1]) != 2 {
		t.Fatalf("batches = %v", f.batches)
	}
	want := "down Ctrl sc=0x1D\ndown Shift sc=0x2A\ndown Z sc=0x2C\nup   Z sc=0x2C\nup   Shift sc=0x2A\nup   Ctrl sc=0x1D\n" +
		"down F13 sc=0x64\nup   F13 sc=0x64\n"
	if got := f.lines(); got != want {
		t.Errorf("sent\n%s\nwant\n%s", got, want)
	}
}

func TestSendHoldSplitsBatches(t *testing.T) {
	f := &fakeSender{}
	useFakeSender(t, f)
	start := time.Now()
	code, _, stderr := runCmd(t, "send", "--hold", "30ms", "Ctrl+Z")
	if code != 0 || stderr != "" {
		t.Fatalf("send --hold = %d, stderr %q", code, stderr)
	}
	if len(f.batches) != 2 || len(f.batches[0]) != 2 || len(f.batches[1]) != 2 || !f.batches[1][0].Up {
		t.Fatalf("batches = %v, want the presses then the releases", f.batches)
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Errorf("send --hold 30ms took %v", d)
	}
}

func TestSendDelay(t *testing.T) {
	f := &fakeSender{}
	useFakeSender(t, f)
	code, stdout, stderr := runCmd(t, "send", "--delay", "1ms", "F13")
	if code != 0 || stdout != "" || len(f.batches) != 1 {
		t.Errorf("send --delay 1ms = %d, stdout %q, %d batches", code, stdout, len(f.batches))
	}
	if want := "padmatectl send: sending in 1ms; switch to the target window now (Ctrl+C aborts)\n"; stderr != want {
		t.Errorf("stderr = %q, want %q", stderr, want)
	}
	_, _, stderr = runCmd(t, "send", "--dry-run", "--delay", "1ms", "F13")
	if !strings.HasPrefix(stderr, "padmatectl send: dry run in 1ms;") {
		t.Errorf("dry-run stderr = %q", stderr)
	}
}

func TestSendInterruptedDuringDelay(t *testing.T) {
	f := &fakeSender{}
	useFakeSender(t, f)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	code, stdout, stderr := runSendCtx(ctx, "--delay", "1h", "Ctrl+Z")
	if code != 1 || stdout != "" || !strings.HasSuffix(stderr, "padmatectl send: interrupted; nothing was sent\n") {
		t.Errorf("send = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if len(f.batches) != 0 {
		t.Errorf("sent %v after an interrupted delay", f.batches)
	}
}

func TestSendInterruptedDuringHold(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Interrupt while the presses of the first chord go out: Tap then
	// finds ctx done during the hold.
	f := &fakeSender{onSend: func(batch int) error {
		if batch == 0 {
			cancel()
		}
		return nil
	}}
	useFakeSender(t, f)
	start := time.Now()
	code, stdout, stderr := runSendCtx(ctx, "--hold", "1h", "Ctrl+Z", "F13")
	if time.Since(start) > 10*time.Second {
		t.Fatal("the interrupt did not end the hold")
	}
	if code != 1 || stdout != "" || !strings.Contains(stderr, "padmatectl send: interrupted at chord 1 of 2 (Ctrl+Z): context canceled") {
		t.Errorf("send = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	// The keys that went down came back up, and F13 was never sent.
	want := "down Ctrl sc=0x1D\ndown Z sc=0x2C\nup   Z sc=0x2C\nup   Ctrl sc=0x1D\n"
	if got := f.lines(); got != want {
		t.Errorf("sent\n%s\nwant\n%s", got, want)
	}
}

func TestSendSenderError(t *testing.T) {
	f := &fakeSender{onSend: func(int) error { return errors.New("blocked") }}
	useFakeSender(t, f)
	code, stdout, stderr := runCmd(t, "send", "Ctrl+Z", "F13")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "padmatectl send: Ctrl+Z: output: injected 0 of 4 key events: blocked") {
		t.Errorf("send = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if strings.Contains(stderr, "F13") {
		t.Errorf("send went on after a failure: %q", stderr)
	}

	prev := newSender
	newSender = func() (output.Sender, error) { return nil, errUnsupported }
	defer func() { newSender = prev }()
	code, _, stderr = runCmd(t, "send", "F13")
	if code != 1 || stderr != "padmatectl send: "+errUnsupported.Error()+"\n" {
		t.Errorf("send without a sender = %d, stderr %q", code, stderr)
	}
}

func TestUsageListsSend(t *testing.T) {
	_, stdout, _ := runCmd(t, "help")
	if !strings.Contains(stdout, "  send       tap key chords with SendInput (development tool)\n") {
		t.Errorf("usage does not list send:\n%s", stdout)
	}
}

// TestSendSignals checks that SIGTERM cancels send and monitor as Ctrl+C
// does: on Windows it is the signal of a closed console, and Tap needs
// the cancellation to release the keys of a hold.
func TestSendSignals(t *testing.T) {
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		if !slices.Contains(stopSignals, sig) {
			t.Errorf("stopSignals = %v, want it to contain %v", stopSignals, sig)
		}
	}
}
