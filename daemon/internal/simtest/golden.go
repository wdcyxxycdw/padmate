package simtest

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

// ReadGolden reads the golden file path, with CRLF line ends read as LF.
// With update a missing file reads as empty, so that -update can create
// it; without, the error says to run cmd, such as "go test
// ./internal/slot -run TestGolden", with -update.
func ReadGolden(path string, update bool, cmd string) (string, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		return strings.ReplaceAll(string(data), "\r\n", "\n"), nil
	case !errors.Is(err, os.ErrNotExist):
		return "", err
	case update:
		return "", nil
	}
	return "", fmt.Errorf("%w; run %s -update to create it", err, cmd)
}

// RewriteGolden writes the golden file path for -update: the comments of
// old, the text ReadGolden returned, then text. It writes only a change,
// and logs it as a LineDiff.
func RewriteGolden(t testing.TB, path, old, text string) {
	t.Helper()
	if text = GoldenComments(old) + text; text == old {
		return
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("updated %s:\n%s", path, LineDiff(old, text))
}

// GoldenComments returns the leading "#" lines of text: the comments of a
// golden file, which RewriteGolden keeps.
func GoldenComments(text string) string {
	n := 0
	for line := range strings.Lines(text) {
		if !strings.HasPrefix(line, "#") {
			break
		}
		n += len(line)
	}
	return text[:n]
}

// LineDiff lists the lines only in want as "- " lines and those only in
// got as "+ " lines, around their longest common prefix and suffix. It is
// empty when the lines are equal; CRLF and LF line ends are equal.
func LineDiff(want, got string) string {
	w, g := splitLines(want), splitLines(got)
	p := 0
	for p < len(w) && p < len(g) && w[p] == g[p] {
		p++
	}
	s := 0
	for s < len(w)-p && s < len(g)-p && w[len(w)-1-s] == g[len(g)-1-s] {
		s++
	}
	var b strings.Builder
	for _, l := range w[p : len(w)-s] {
		b.WriteString("- " + l + "\n")
	}
	for _, l := range g[p : len(g)-s] {
		b.WriteString("+ " + l + "\n")
	}
	return b.String()
}

func splitLines(s string) []string {
	if s = strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"); s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
