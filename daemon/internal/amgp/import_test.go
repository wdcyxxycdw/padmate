package amgp

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// TestGolden imports every testdata/<case>.amgp and compares the profile
// with <case>.profile.json and the report with <case>.report.txt; a file
// that is refused has <case>.error.txt with the reason and the message.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.amgp"))
	if err != nil || len(files) == 0 {
		t.Fatal("no test files", err)
	}
	for _, file := range files {
		name := strings.TrimSuffix(filepath.Base(file), ".amgp")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			base := strings.TrimSuffix(file, ".amgp")
			out, report, err := Import(data, Options{FileName: filepath.Base(file)})
			if err != nil {
				var refused *Error
				if !errors.As(err, &refused) || refused.Reason == ReasonInternal {
					t.Fatalf("unexpected error: %v", err)
				}
				golden(t, base+".error.txt", refused.Reason+"\n"+refused.Message+"\n")
				mustNotExist(t, base+".profile.json", base+".report.txt")
				return
			}
			mustNotExist(t, base+".error.txt")
			golden(t, base+".profile.json", string(out))
			golden(t, base+".report.txt", FormatReport(report))
			checkProfile(t, out)
		})
	}
}

// checkProfile checks that the profile is one padmate loads and compiles.
func checkProfile(t *testing.T, out []byte) {
	t.Helper()
	p, ds := profile.Parse(out)
	if err := ds.Err(); err != nil {
		t.Fatalf("the profile does not validate: %v\n%s", err, out)
	}
	for _, d := range ds {
		if d.Severity == profile.SeverityWarning {
			t.Errorf("warning: %s %s", d.Pointer, d.Message)
		}
	}
	if _, err := engine.Compile(p); err != nil {
		t.Fatalf("the profile does not compile: %v", err)
	}
	checkTimingBounds(t, out)
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to make it)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), []byte(got)) {
		t.Errorf("%s differs; run with -update and look at the diff\n--- got\n%s", path, got)
	}
}

func mustNotExist(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			if *update {
				_ = os.Remove(p)
				continue
			}
			t.Errorf("%s should not exist", p)
		}
	}
}
