package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

// claudeMDMax keeps CLAUDE.md, which AGENTS.md links to, well inside the
// 32 KiB of it that Codex reads by default (project_doc_max_bytes); Codex
// silently drops the rest. Pitfalls that only one area of the code needs go
// to docs/PITFALLS.md.
const claudeMDMax = 24 << 10

// TestClaudeMD checks the size of CLAUDE.md, and that the table in its
// 踩坑记录 names the sections of docs/PITFALLS.md, all of them, in order.
// go test runs in the package directory, so the paths go up to the root.
// Both files are outside the module, and go test does not recheck those
// for a cached result, so task roadmap:check runs this test with -count=1.
func TestClaudeMD(t *testing.T) {
	publicmirror.SkipIfMirror(t, "CLAUDE.md and docs/PITFALLS.md")
	claude, err := os.ReadFile("../../../CLAUDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(claude) > claudeMDMax {
		t.Errorf("CLAUDE.md is %d bytes, over %d: move what only one area of the code needs to docs/PITFALLS.md",
			len(claude), claudeMDMax)
	}
	pitfalls, err := os.ReadFile("../../../docs/PITFALLS.md")
	if err != nil {
		t.Fatal(err)
	}
	_, sec, ok := strings.Cut(string(claude), "\n## 已验证的技术事实（踩坑记录）\n")
	if !ok {
		t.Fatal("CLAUDE.md has no 踩坑记录 section")
	}
	sec, _, _ = strings.Cut(sec, "\n## ")
	var rows, heads []string
	for _, m := range regexp.MustCompile(`(?m)^\| ([^|]+?) \| .+ \|$`).FindAllStringSubmatch(sec, -1) {
		if m[1] != "小节" {
			rows = append(rows, m[1])
		}
	}
	for _, m := range regexp.MustCompile(`(?m)^## (.+)$`).FindAllStringSubmatch(string(pitfalls), -1) {
		heads = append(heads, m[1])
	}
	if len(heads) == 0 || !slices.Equal(rows, heads) {
		t.Errorf("the table in CLAUDE.md's 踩坑记录 lists %q; docs/PITFALLS.md has sections %q", rows, heads)
	}
}
