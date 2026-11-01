package jc2win

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// What can reach a controller must stay where the review looked at it. These
// tests read the package's own source (not its tests; comments removed) and
// fail if that changes. They run on every OS.

// sources returns the non-test Go files of the package, comments removed,
// by file name.
func sources(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var keep []string
		for _, l := range strings.Split(string(b), "\n") {
			if i := strings.Index(l, "//"); i >= 0 {
				l = l[:i]
			}
			keep = append(keep, l)
		}
		out[f] = strings.Join(keep, "\n")
	}
	if len(out) < 5 {
		t.Fatalf("found only %d source files", len(out))
	}
	return out
}

// funcBody returns the text of the top-level function whose declaration
// contains decl.
func funcBody(t *testing.T, src, decl string) string {
	t.Helper()
	i := strings.Index(src, decl)
	if i < 0 {
		t.Fatalf("no function %q", decl)
	}
	rest := src[i+len(decl):]
	if j := strings.Index(rest, "\nfunc "); j >= 0 {
		rest = rest[:j]
	}
	return rest
}

// count returns how many times re matches in each file, without zeros.
func count(srcs map[string]string, re *regexp.Regexp) (perFile map[string]int, total int) {
	perFile = map[string]int{}
	for name, src := range srcs {
		if n := len(re.FindAllString(src, -1)); n > 0 {
			perFile[name] = n
			total += n
		}
	}
	return perFile, total
}

func TestCommandWriteIsInTheGateWriter(t *testing.T) {
	srcs := sources(t)
	// Any write method of a characteristic, whatever its name.
	_, total := count(srcs, regexp.MustCompile(`\.WriteValue\w*\(`))
	if total != 1 {
		t.Fatalf("%d calls of a WriteValue method, want exactly 1", total)
	}
	perFile, _ := count(srcs, regexp.MustCompile(`\.WriteValueWithOptionAsync\(`))
	if perFile["connect_windows.go"] != 1 {
		t.Fatalf("WriteValueWithOptionAsync calls: %v, want one in connect_windows.go", perFile)
	}
	body := funcBody(t, srcs["connect_windows.go"], "func (t *Transport) commandWriter(")
	if !strings.Contains(body, ".WriteValueWithOptionAsync(") {
		t.Error("the write is not inside commandWriter")
	}
	if !strings.Contains(body, "GattWriteOptionWriteWithoutResponse") {
		t.Error("commandWriter does not write without response")
	}
	// The allowlist runs in the writer, on the bytes it is about to send,
	// before the write.
	if i, j := strings.Index(body, "jc2.CheckRequest("), strings.Index(body, ".WriteValueWithOptionAsync("); i < 0 || i > j {
		t.Error("commandWriter does not check the bytes before it writes")
	}
}

func TestOnlyTheGateReachesTheWriter(t *testing.T) {
	srcs := sources(t)
	// The writer function exists in one place and is called in one place.
	perFile, total := count(srcs, regexp.MustCompile(`\bcommandWriter\(`))
	if total != 2 || perFile["connect_windows.go"] != 2 { // its declaration and its use
		t.Errorf("commandWriter appears %v, want its declaration and one use in connect_windows.go", perFile)
	}
	// A gate is built once, with that writer, and its write is called once.
	perFile, total = count(srcs, regexp.MustCompile(`&?gate\{`))
	if total != 1 || perFile["connect_windows.go"] != 1 {
		t.Errorf("gate is constructed %v, want once in connect_windows.go", perFile)
	}
	perFile, total = count(srcs, regexp.MustCompile(`\bg\.write\(|\bgate\.write\(`))
	if total != 1 || perFile["gate.go"] != 1 {
		t.Errorf("the gate's write is called %v, want once in gate.go", perFile)
	}
	body := funcBody(t, srcs["gate.go"], "func (g *gate) send(")
	if i, j := strings.Index(body, "jc2.CheckRequest("), strings.Index(body, "g.write("); i < 0 || i > j {
		t.Error("gate.send does not check the request before it writes")
	}
	body = funcBody(t, srcs["connect_windows.go"], "func (t *Transport) commandGate(")
	if !strings.Contains(body, "t.commandWriter(l, c)") {
		t.Error("commandGate does not use commandWriter")
	}
}

func TestCCCDWriteIsInSubscribe(t *testing.T) {
	srcs := sources(t)
	perFile, total := count(srcs, regexp.MustCompile(`WriteClientCharacteristicConfigurationDescriptor\w*\(`))
	if total != 1 || perFile["connect_windows.go"] != 1 {
		t.Fatalf("CCCD writes: %v, want exactly one in connect_windows.go", perFile)
	}
	body := funcBody(t, srcs["connect_windows.go"], "func (t *Transport) subscribe(")
	if !strings.Contains(body, "WriteClientCharacteristicConfigurationDescriptorAsync(") {
		t.Error("the CCCD write is not inside subscribe")
	}
	if i, j := strings.Index(body, "checkNotify("), strings.Index(body, "WriteClientCharacteristicConfigurationDescriptorAsync("); i < 0 || i > j {
		t.Error("subscribe does not check the characteristic before it writes")
	}
	// subscribe is called with uuids of jc2.NotifyUUIDs only.
	perFile, total = count(srcs, regexp.MustCompile(`\bt\.subscribe\(`))
	if total != 1 || perFile["connect_windows.go"] != 1 {
		t.Errorf("subscribe is called %v, want once in connect_windows.go", perFile)
	}
	if body := funcBody(t, srcs["connect_windows.go"], "func (t *Transport) subscribeAll("); !strings.Contains(body, "range jc2.NotifyUUIDs(l.side)") {
		t.Error("subscribeAll does not iterate over jc2.NotifyUUIDs")
	}
	// Notifications are never switched off with a write either: closing the
	// session ends them.
	if _, n := count(srcs, regexp.MustCompile(`DescriptorValueNone|DescriptorValueIndicate`)); n != 0 {
		t.Error("a CCCD value other than Notify is used")
	}
}

func TestNoPairingAndNoCallbacks(t *testing.T) {
	forbidden := regexp.MustCompile(`\w*Pair\w*|NewCallback\w*`)
	for name, src := range sources(t) {
		if m := forbidden.FindString(src); m != "" {
			t.Errorf("%s uses %s", name, m)
		}
	}
}

func TestNoSideEffectsOnTheSystem(t *testing.T) {
	// Pairing lives in devices/enumeration (DeviceInformation.Pairing,
	// custom pairing); the registry and the settings are not this package's
	// business.
	banned := []string{
		"winrt-go/windows/devices/enumeration",
		"winrt-go/windows/devices/custom",
		"golang.org/x/sys/windows/registry",
		"golang.org/x/sys/windows/svc",
		"tinygo.org/x/bluetooth",
		"os/exec",
	}
	for name, src := range sources(t) {
		for _, b := range banned {
			if strings.Contains(src, `"`+b) || strings.Contains(src, b+`"`) {
				t.Errorf("%s imports %s", name, b)
			}
		}
	}
	mod, err := os.ReadFile(filepath.Join("..", "..", "..", "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(mod), "tinygo.org/x/bluetooth") {
		t.Error("go.mod requires tinygo.org/x/bluetooth")
	}
}

// Callbacks run on pool threads and must not wait: no mutex of the transport
// is taken in them except the queue's, which is held only to append.
func TestCallbacksDoNotWait(t *testing.T) {
	srcs := sources(t)
	for _, c := range []struct{ file, decl string }{
		{"connect_windows.go", "func (t *Transport) onStatus("},
		{"connect_windows.go", "func (t *Transport) onReport("},
		{"connect_windows.go", "func (t *Transport) onReply("},
		{"connect_windows.go", "func valueBytes("},
		{"scan_windows.go", "func (t *Transport) onAdvert("},
		{"scan_windows.go", "func (t *Transport) advertFrom("},
		{"scan_windows.go", "func (t *Transport) onScanStopped("},
	} {
		body := funcBody(t, srcs[c.file], c.decl)
		// The connection parameters are read by the transport goroutine only.
		for _, bad := range []string{".Lock()", ".RLock()", "time.Sleep", "t.await(", "emitWait(", "GetConnectionParameters(", "readParams(", "sampleParams(", "pollParams("} {
			if strings.Contains(body, bad) {
				t.Errorf("%s contains %s", c.decl, bad)
			}
		}
		if strings.Contains(c.decl, "valueBytes") || strings.Contains(c.decl, "advertFrom") {
			continue
		}
		if !strings.Contains(body, "defer recoverCallback()") {
			t.Errorf("%s does not recover from panics", c.decl)
		}
	}
}
