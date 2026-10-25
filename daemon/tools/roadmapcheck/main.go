// Command roadmapcheck validates ROADMAP.md (ENG-22, `task roadmap:check`).
//
// Run it from daemon/:
//
//	go run ./tools/roadmapcheck [-file ../ROADMAP.md]
//
// It checks that every ID is defined once in the §3 feature tables or the
// §5.1 spike table; that the milestone checklists list every scheduled ID
// exactly once, under the milestone its 里程碑 column names; that Backlog
// and deprecated IDs stay out of the checklists; that priorities match their
// milestone (P1（提前） and no plain P1 in milestones ≤ M14, P2（可选） for
// P2 items in a milestone); that §4.1 and the milestone sections agree and
// each status matches its checkboxes (未开始 with nothing checked, 完成 once
// every required item is checked, 进行中 in between and also while only
// optional items are left); that a milestone deferred to after 1.0 (status
// 1.0 之后, which keeps its number, section and items) has no checked item;
// that no P0 or P1 item is unchecked once M28 (1.0) is 完成, except those of
// 1.0 之后 milestones; and, when there is a "## 0." chapter, that it has
// exactly one "**当前里程碑**：<ID> <name>（<status>）。" line naming the
// first milestone in §4.1 that is neither 完成 nor 1.0 之后, with that row's
// 名称 and status, or "**当前里程碑**：无（全部完成）。" once every milestone
// is one of the two. A 当前里程碑 line outside §0 is an error, so a mistyped
// "## 0." heading cannot switch that check off.
//
// In the public code mirror (the file .padmate-public at the repository root),
// which has no ROADMAP.md, a run without -file prints that it is skipped and
// exits 0, so that `task ci` runs everything else.
//
// On success it prints a one-line summary and exits 0. Otherwise it prints
// each problem as "ROADMAP.md:<line>: <message>" and exits 1. It exits 2
// when it cannot run (bad flags, unreadable file).
package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("roadmapcheck", flag.ContinueOnError)
	fs.SetOutput(stderr)
	file := fs.String("file", "../ROADMAP.md", "path to ROADMAP.md")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "roadmapcheck: unexpected argument %q\n", fs.Arg(0))
		return 2
	}
	explicit := false
	fs.Visit(func(*flag.Flag) { explicit = true })
	if !explicit && skippedInMirror(*file) {
		fmt.Fprintf(stdout, "roadmapcheck: skipped: public code mirror (%s at the repository root), which has no %s\n",
			publicmirror.Marker, filepath.Base(*file))
		return 0
	}
	data, err := os.ReadFile(*file)
	if err != nil {
		fmt.Fprintf(stderr, "roadmapcheck: %v\n", err)
		return 2
	}

	rm, problems := lint(data)
	name := filepath.Base(*file)
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stdout, "%s:%d: %s\n", name, p.Line, p.Msg)
		}
		return 1
	}
	s := summarize(rm)
	fmt.Fprintf(stdout, "%s: ok: %d IDs (%d deprecated), %d milestones, %d of %d checklist items checked\n",
		name, s.ids, s.deprecated, s.milestones, s.checked, s.listed)
	return 0
}

// skippedInMirror reports whether the default roadmap file is missing because
// the checkout is the public code mirror. In the development repository a
// missing file stays an error.
func skippedInMirror(file string) bool {
	if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
		return false
	}
	abs, err := filepath.Abs(file)
	if err != nil {
		return false
	}
	root, err := publicmirror.Root(filepath.Dir(abs))
	return err == nil && publicmirror.Is(root)
}

// lint parses and checks a roadmap and returns its problems sorted by line,
// without exact duplicates.
func lint(data []byte) (*Roadmap, []Problem) {
	rm, problems := parse(bytes.NewReader(data))
	problems = append(problems, check(rm)...)
	sort.SliceStable(problems, func(i, j int) bool {
		if problems[i].Line != problems[j].Line {
			return problems[i].Line < problems[j].Line
		}
		return problems[i].Msg < problems[j].Msg
	})
	out := problems[:0]
	for i, p := range problems {
		if i == 0 || p != problems[i-1] {
			out = append(out, p)
		}
	}
	return rm, out
}

type summary struct {
	ids, deprecated int
	milestones      int
	listed, checked int
}

func summarize(rm *Roadmap) summary {
	var s summary
	seen := make(map[string]bool)
	for _, it := range rm.Items {
		if seen[it.ID] {
			continue
		}
		seen[it.ID] = true
		s.ids++
		if it.Deprecated {
			s.deprecated++
		}
	}
	s.milestones = len(rm.Milestones)
	for _, m := range rm.Milestones {
		for _, e := range m.Entries {
			ids, err := expandEntry(e)
			if err != nil {
				ids = []string{e.String()}
			}
			s.listed += len(ids)
			if e.Checked {
				s.checked += len(ids)
			}
		}
	}
	return s
}
