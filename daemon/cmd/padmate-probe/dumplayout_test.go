package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/kblayout"
)

func TestDumpLayoutFlagErrors(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"409"}, `bad KLID "409"`},
		{[]string{"00000409", "0000040G"}, `bad KLID "0000040G"`},
		{[]string{"--bogus"}, "flag provided but not defined: -bogus"},
		{[]string{"--out"}, "flag needs an argument: -out"},
	} {
		code, stdout, stderr := runProbe(t, append([]string{"dump-layout"}, tc.args...)...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, tc.want) || !strings.Contains(stderr, "usage: padmate-probe dump-layout") {
			t.Errorf("dump-layout %q = %d, stdout %q, stderr %q; want 2 and %q with the usage", tc.args, code, stdout, stderr, tc.want)
		}
	}
}

func TestDefaultKLIDs(t *testing.T) {
	want := []string{"00000409", "0000040C", "00000411"}
	if strings.Join(defaultKLIDs, " ") != strings.Join(want, " ") {
		t.Errorf("defaultKLIDs = %q, want %q", defaultKLIDs, want)
	}
}

// fakeDumper serves fixtures from memory and can change its layout list
// halfway, as a buggy dump would.
type fakeDumper struct {
	lists   []string // returned by successive LayoutList calls
	calls   int
	dumped  []string
	failFor string
}

func (d *fakeDumper) LayoutList() (string, error) {
	i := min(d.calls, len(d.lists)-1)
	d.calls++
	return d.lists[i], nil
}

func (d *fakeDumper) Dump(klid string) (*kblayout.Fixture, error) {
	d.dumped = append(d.dumped, klid)
	if klid == d.failFor {
		return nil, errors.New("not installed")
	}
	f := &kblayout.Fixture{
		KLID: klid, HKL: "0x0" + klid[4:] + klid[4:], LayoutFile: "KBDTEST.DLL", LayoutText: "Test",
		Windows: "10.0.26200",
		Scancodes: []kblayout.ScanEntry{
			{Scan: 0x1E, VK: 0x41, Base: &kblayout.Output{Text: "a"}},
		},
		VKToScan: []kblayout.VKEntry{{VK: 0x41, Scan: 0x1E}},
	}
	for c := 0x20; c <= 0x7E; c++ {
		f.Chars = append(f.Chars, kblayout.CharEntry{Char: string(rune(c)), Code: kblayout.Code(c), Absent: true})
	}
	return f, nil
}

func TestDumpLayouts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "layouts")
	d := &fakeDumper{lists: []string{"[0x04090409]"}}
	var out, errOut bytes.Buffer
	code := dumpLayouts(d, []string{"00000409", "0000040C"}, dir, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code %d, stderr %q", code, errOut.String())
	}
	if errOut.String() != "padmate-probe dump-layout: keyboard layout list unchanged: [0x04090409]\n" {
		t.Errorf("stderr = %q, want the unchanged layout list", errOut.String())
	}
	for _, klid := range []string{"00000409", "0000040C"} {
		path := filepath.Join(dir, klid+".json")
		if !strings.Contains(out.String(), path) {
			t.Errorf("stdout %q does not name %s", out.String(), path)
		}
		f, err := kblayout.Load(path)
		if err != nil {
			t.Fatalf("Load(%s): %v", path, err)
		}
		if f.Generator != kblayout.Generator || f.Version == "" {
			t.Errorf("%s: generator %q, version %q", klid, f.Generator, f.Version)
		}
		data, _ := os.ReadFile(path)
		again, _ := f.Marshal()
		if !bytes.Equal(data, again) {
			t.Errorf("%s is not in the Marshal format", path)
		}
	}
}

// useDumper makes dump-layout use d, or fail with err, for the rest of the
// test.
func useDumper(t *testing.T, d layoutDumper, err error) {
	t.Helper()
	prev := openLayoutDumper
	openLayoutDumper = func() (layoutDumper, error) { return d, err }
	t.Cleanup(func() { openLayoutDumper = prev })
}

func TestRunDumpLayout(t *testing.T) {
	// The default KLIDs are dumped, and the defaults stay as they were.
	d := &fakeDumper{lists: []string{"[0x04090409]"}}
	useDumper(t, d, nil)
	before := strings.Join(defaultKLIDs, " ")
	if code, _, stderr := runProbe(t, "dump-layout", "--out", t.TempDir()); code != 0 {
		t.Fatalf("dump-layout = %d, stderr %q", code, stderr)
	}
	if got := strings.Join(d.dumped, " "); got != before {
		t.Errorf("dumped %q, want the defaults %q", got, before)
	}
	if got := strings.Join(defaultKLIDs, " "); got != before {
		t.Errorf("defaultKLIDs changed to %q", got)
	}

	// KLIDs are upper-cased.
	d = &fakeDumper{lists: []string{"[0x04090409]"}}
	useDumper(t, d, nil)
	if code, _, stderr := runProbe(t, "dump-layout", "--out", t.TempDir(), "0000040c"); code != 0 || strings.Join(d.dumped, " ") != "0000040C" {
		t.Errorf("dump-layout 0000040c = %d, stderr %q, dumped %q", code, stderr, d.dumped)
	}

	// An error other than errUnsupported is reported, not ignored.
	useDumper(t, nil, errors.New("boom"))
	code, stdout, stderr := runProbe(t, "dump-layout", "--out", t.TempDir())
	if code != 1 || stdout != "" || stderr != "padmate-probe dump-layout: boom\n" {
		t.Errorf("dump-layout with a failing dumper = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestDumpLayoutsFailures(t *testing.T) {
	t.Run("layout list changed", func(t *testing.T) {
		d := &fakeDumper{lists: []string{"[0x04090409]", "[0x04090409 0x040C040C]"}}
		var out, errOut bytes.Buffer
		code := dumpLayouts(d, []string{"0000040C"}, t.TempDir(), &out, &errOut)
		want := "the keyboard layout list changed from [0x04090409] to [0x04090409 0x040C040C]"
		if code != 1 || !strings.Contains(errOut.String(), want) {
			t.Errorf("exit code %d, stderr %q; want 1 and %q", code, errOut.String(), want)
		}
	})
	t.Run("one layout fails", func(t *testing.T) {
		d := &fakeDumper{lists: []string{"[0x04090409]"}, failFor: "0000040C"}
		dir := t.TempDir()
		var out, errOut bytes.Buffer
		code := dumpLayouts(d, []string{"0000040C", "00000411"}, dir, &out, &errOut)
		if code != 1 || !strings.Contains(errOut.String(), "0000040C: not installed") {
			t.Errorf("exit code %d, stderr %q; want 1 and the failure", code, errOut.String())
		}
		// The other layouts are still written.
		if strings.Join(d.dumped, " ") != "0000040C 00000411" {
			t.Errorf("dumped %q", d.dumped)
		}
		if _, err := os.Stat(filepath.Join(dir, "00000411.json")); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "0000040C.json")); err == nil {
			t.Error("the failed layout was written")
		}
	})
}
