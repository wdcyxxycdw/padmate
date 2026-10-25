package amgp

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorpus imports every .amgp and .xml file under the folder that
// PADMATE_AMGP_CORPUS names. Real files that are under the GPL, or have no
// license, cannot be kept in the repository (testdata/THIRD-PARTY.txt), so this
// runs on a folder of the developer's own. It checks that nothing breaks
// and that what comes out is a profile; it prints what each file lost.
func TestCorpus(t *testing.T) {
	dir := os.Getenv("PADMATE_AMGP_CORPUS")
	if dir == "" {
		t.Skip("PADMATE_AMGP_CORPUS is not set")
	}
	n := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if ext := strings.ToLower(filepath.Ext(path)); ext != ".amgp" && ext != ".xml" {
			return nil
		}
		n++
		rel, _ := filepath.Rel(dir, path)
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			out, report, err := Import(data, Options{FileName: filepath.Base(path)})
			if err != nil {
				var e *Error
				if !errors.As(err, &e) || e.Reason == ReasonInternal {
					t.Fatalf("unexpected error: %v", err)
				}
				t.Logf("refused (%s): %s", e.Reason, e.Message)
				return
			}
			checkProfile(t, out)
			dropped, approx, info := Counts(report)
			t.Logf("%d dropped, %d approximate, %d info", dropped, approx, info)
			if testing.Verbose() {
				t.Log("\n" + FormatReport(report))
			}
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("no .amgp or .xml files under %s", dir)
	}
}
