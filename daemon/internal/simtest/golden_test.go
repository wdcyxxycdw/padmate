package simtest_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/simtest"
)

func TestLineDiff(t *testing.T) {
	tests := []struct{ want, got, diff string }{
		{"", "", ""},
		{"a\nb\n", "a\nb\n", ""},
		{"a\r\nb\r\n", "a\nb", ""},
		{"", "a\n", "+ a\n"},
		{"a\n", "", "- a\n"},
		{"a\nb\nc\n", "a\nx\nc\n", "- b\n+ x\n"},
		{"a\nc\n", "a\nb\nc\n", "+ b\n"},
		// The prefix and the suffix do not overlap.
		{"a\na\n", "a\na\na\n", "+ a\n"},
		{"a\nb\n", "b\na\n", "- a\n- b\n+ b\n+ a\n"},
	}
	for _, tt := range tests {
		if got := simtest.LineDiff(tt.want, tt.got); got != tt.diff {
			t.Errorf("LineDiff(%q, %q) = %q, want %q", tt.want, tt.got, got, tt.diff)
		}
	}
}

func TestGoldenComments(t *testing.T) {
	for text, want := range map[string]string{
		"":                     "",
		"# a":                  "# a",
		"# a\n#b\n+x@0\n# c\n": "# a\n#b\n",
		"+x@0\n# c\n":          "",
		" # a\n":               "",
	} {
		if got := simtest.GoldenComments(text); got != want {
			t.Errorf("GoldenComments(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestReadGolden(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.trace")
	const cmd = "go test ./internal/x -run TestGolden"
	_, err := simtest.ReadGolden(missing, false, cmd)
	if !errors.Is(err, os.ErrNotExist) || !strings.Contains(err.Error(), cmd+" -update") {
		t.Errorf("ReadGolden of a missing file = %v; want ErrNotExist and %q", err, cmd+" -update")
	}
	if got, err := simtest.ReadGolden(missing, true, cmd); got != "" || err != nil {
		t.Errorf("ReadGolden of a missing file with update = %q, %v; want empty", got, err)
	}
	crlf := filepath.Join(dir, "crlf.trace")
	if err := os.WriteFile(crlf, []byte("# c\r\n+a@0\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, update := range []bool{false, true} {
		if got, err := simtest.ReadGolden(crlf, update, cmd); got != "# c\n+a@0\n" || err != nil {
			t.Errorf("ReadGolden(update %v) = %q, %v; want LF line ends", update, got, err)
		}
	}
	if _, err := simtest.ReadGolden(dir, true, cmd); err == nil {
		t.Error("ReadGolden of a directory: no error")
	}
}

// logTB records Logf.
type logTB struct {
	testing.TB
	logs []string
}

func (l *logTB) Logf(format string, args ...any) {
	l.logs = append(l.logs, fmt.Sprintf(format, args...))
}

func TestRewriteGolden(t *testing.T) {
	path := filepath.Join(t.TempDir(), "output.trace")
	tb := &logTB{TB: t}
	// Nothing to write: no file, no log.
	simtest.RewriteGolden(tb, path, "", "")
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) || len(tb.logs) > 0 {
		t.Fatalf("RewriteGolden without a change: Stat %v, logs %q; want neither", err, tb.logs)
	}
	simtest.RewriteGolden(tb, path, "", "+a@0\n")
	check := func(want string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Errorf("file = %q, %v; want %q", data, err, want)
		}
	}
	check("+a@0\n")
	// The comments stay; the rest is replaced.
	simtest.RewriteGolden(tb, path, "# c\n+a@0\n# d\n", "+b@0\n")
	check("# c\n+b@0\n")
	if n := len(tb.logs); n != 2 || !strings.Contains(tb.logs[1], "- +a@0\n- # d\n+ +b@0\n") {
		t.Errorf("logs = %q; want 2, the last with the diff", tb.logs)
	}
	simtest.RewriteGolden(tb, path, "# c\n+b@0\n", "+b@0\n")
	if len(tb.logs) != 2 {
		t.Errorf("a rewrite without a change logged %q", tb.logs[2:])
	}
}
