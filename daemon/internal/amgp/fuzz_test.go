package amgp

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/engine"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// FuzzImport checks what Import promises whatever the file: it never panics;
// it either refuses the file with an *Error (never an internal one) or returns
// a profile that profile.Parse and engine.Compile accept without a warning and
// whose times hold no key down for longer than a key press time can, and a
// report that is not nil, no longer than the limit and of tidy lines (the file
// cannot write lines of its own); the same file
// gives the same answer twice; and a file over the limit is refused.
func FuzzImport(f *testing.F) {
	files, _ := filepath.Glob(filepath.Join("testdata", "*.amgp"))
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}
	for _, seed := range []string{
		"", "<", "<gamecontroller/>", `<gamecontroller configversion="19"><sets><set index="1"/></sets></gamecontroller>`,
		`<gamecontroller configversion="19"><sets><set index="2"><button index="1"><setselect>2</setselect><setselectcondition>while-held</setselectcondition></button></set></sets></gamecontroller>`,
		`<gamecontroller configversion="19"><sets><set index="1"><button index="5"><setselect>2</setselect><setselectcondition>while-held</setselectcondition></button></set><set index="2"/></sets></gamecontroller>`,
		`<gamecontroller configversion="19"><sets><set index="1"><button index="1"><slots><slot><slots><slot><code>0x41</code><mode>keyboard</mode></slot></slots><mode>mix</mode></slot></slots></button></set></sets></gamecontroller>`,
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		out, report, err := Import(data, Options{FileName: "fuzz.amgp"})
		if err != nil {
			var e *Error
			if !errors.As(err, &e) {
				t.Fatalf("error %v is not an *Error", err)
			}
			if e.Reason == ReasonInternal {
				t.Fatalf("internal error: %v", err)
			}
			if out != nil || report != nil {
				t.Fatal("a refused file returns neither profile nor report")
			}
			checkLineIsTidy(t, ReportLine{Code: "error:" + e.Reason, Message: e.Message})
			return
		}
		if len(data) > MaxBytes {
			t.Fatal("a file over the limit was read")
		}
		if report == nil {
			t.Fatal("the report is nil")
		}
		if len(report) > maxReportLines+1 {
			t.Fatalf("the report has %d lines", len(report))
		}
		p, ds := profile.Parse(out)
		if err := ds.Err(); err != nil {
			t.Fatalf("the profile does not validate: %v\n%s", err, out)
		}
		for _, d := range ds {
			if d.Severity == profile.SeverityWarning {
				t.Fatalf("warning %s: %s\n%s", d.Pointer, d.Message, out)
			}
		}
		if _, err := engine.Compile(p); err != nil {
			t.Fatalf("the profile does not compile: %v\n%s", err, out)
		}
		checkTimingBounds(t, out)
		for _, l := range report {
			if l.Message == "" || l.Code == "" || l.Set < 0 || l.Set > setCount || l.Slot < 0 {
				t.Fatalf("bad report line %+v", l)
			}
			checkLineIsTidy(t, l)
		}
		out2, report2, err2 := Import(data, Options{FileName: "fuzz.amgp"})
		if err2 != nil || !bytes.Equal(out, out2) || FormatReport(report) != FormatReport(report2) {
			t.Fatal("two imports of the same file differ")
		}
	})
}
