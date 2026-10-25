package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fixtures maps each testdata file to the exact problems it must produce,
// formatted "<line>: <message>" and sorted as the tool prints them.
var fixtures = map[string][]string{
	"valid.md":    nil,
	"tolerant.md": nil,

	// Rule 1: IDs.
	"dup_id.md": {
		"28: ENG-01 is defined more than once (first at line 23)",
	},
	"gap.md": {
		"18: IN-11 is missing; IDs are never removed (strike the row through instead)",
		"27: ENG-05 至 ENG-06 are missing; IDs are never removed (strike the rows through instead)",
	},
	"id_form.md": {
		"18: XYZ-01 has unknown prefix XYZ",
		"19: in-12 is not a valid ID (want PREFIX-NN)",
		"25: ENG-02 is struck through but its row does not say 废弃",
		"28: OUT-01 is in the §3.2 table, which is for ENG IDs",
	},
	"spike_table.md": {
		"19: SPK-02 must be defined in the §5.1 spike table",
		"89: IN-12 is in the §5.1 spike table, which is only for SPK IDs",
	},

	// Rule 2: checklist references and placement.
	"undefined_ref.md": {
		"63: IN-99 is not defined",
		"93: reference to undefined ID OUT-77",
	},
	"bad_range.md": {
		"72: range IN-06 至 IN-05: end must be greater than start",
		"73: range IN-05 至 ENG-06: ends have different prefixes",
		"74: range IN-05 至 IN-13: IN-13 is not defined",
	},
	"missing_checklist.md": {
		"27: ENG-05 is not listed in the M28 checklist",
	},
	"wrong_milestone.md": {
		"66: ENG-03 is listed under M14 but its milestone is M1a",
	},
	"duplicate_listing.md": {
		"72: IN-06 is already listed at line 71",
		"77: ENG-04 is already listed at line 76",
	},
	"backlog_listed.md": {
		"71: IN-07 is listed under M15 but its milestone is Backlog",
	},
	"deprecated_listed.md": {
		"67: ENG-02 is deprecated and must not be listed in a milestone",
	},
	"backlog.md": {
		"15: IN-08 has milestone Backlog but is not listed in the Backlog section",
		"16: IN-09 has milestone Backlog but is not listed in the Backlog section",
		"79: Backlog lists IN-03, whose milestone is M1a",
		"79: Backlog lists IN-07 twice (first at line 79)",
		"79: Backlog lists IN-42, which is not defined",
		"79: Backlog lists deprecated ENG-02",
	},

	// Rule 3: priorities.
	"plain_p1.md": {
		"10: IN-03 is plain P1 in M1a; P1 items in milestones ≤ M14 must be written P1（提前）",
	},
	"early_after_m14.md": {
		"11: IN-04 is P1（提前） in M15; P1（提前） is only for milestones ≤ M14, write P1",
	},
	"plain_p2.md": {
		"13: IN-06 is plain P2 in M15; a P2 item in a milestone must be written P2（可选）",
	},
	"priority_extras.md": {
		"14: IN-07 is P1 in Backlog; Backlog items must be P2",
		"26: ENG-04 is P0 in M28; P0 items belong to milestones ≤ M14",
		"76: ENG-05 is P1 but its checklist line is marked （可选）; only P2 items are optional",
	},

	// Rule 4: milestone list and status.
	"order_mismatch.md": {
		"64: milestone sections are out of order: §4.1 has M14 here, found M15",
	},
	"overview_order.md": {
		"46: M14 is listed after M15 in §4.1; milestones must be in ascending order",
		"64: milestone sections are out of order: §4.1 has M15 here, found M14",
	},
	"milestone_sets.md": {
		"46: M16 has no milestone section",
		"48: M0a is listed twice in §4.1 (first at line 43)",
		"69: M15 is not listed in the §4.1 overview",
		"95: second section for M0a (first at line 50)",
	},
	"title_version.md": {
		`58: M1a title "无头引擎 II" differs from §4.1 ("无头引擎")`,
		"58: M1a version v0.1.0-b differs from §4.1 (v0.1.0-a)",
	},
	"status_done_unchecked.md": {
		"44: M1a is 完成 but required items are unchecked: IN-11, SPK-02",
	},
	"status_not_started_checked.md": {
		"46: M15 is 未开始 but has checked items (want 进行中 or 完成)",
		"47: M28 is 未开始 but has checked items (want 进行中)",
	},
	"status_in_progress_complete.md": {
		"43: M0a is 进行中 but all items are checked (want 完成)",
	},
	// Only optional items are unchecked: 进行中 is accepted as well as 完成
	// (valid.md has M0a 完成 with the same checklist).
	"status_in_progress_optional_left.md": nil,
	"status_in_progress_idle.md": {
		"45: M14 is 进行中 but no item is checked (want 未开始)",
	},
	"status_unknown.md": {
		`46: M15 has unknown status "done" (want 未开始, 进行中, 完成 or 1.0 之后)`,
	},
	// 1.0 之后 (deferred): a valid status that keeps the milestone's number,
	// section and items, but allows no checked item.
	"deferred_ok.md": nil,
	"deferred_checked.md": {
		"46: M15 is 1.0 之后 but has checked items",
	},
	"deferred_optional_checked.md": {
		"46: M15 is 1.0 之后 but has checked items",
	},
	// A deferred milestone still needs a required checklist item.
	"deferred_no_items.md": {
		"68: M15 has no non-optional checklist items",
	},
	"no_required.md": {
		"64: M14 has no non-optional checklist items",
		"66: ENG-03 is P0 but its checklist line is marked （可选）; only P2 items are optional",
	},

	// Rule 5: release gates.
	"release_gate.md": {
		"60: M28 is 完成 but P1（提前） item IN-03 is not checked",
		"66: M28 is 完成 but P0 item ENG-03 is not checked",
		"70: M28 is 完成 but P1 item IN-04 is not checked",
	},
	"m14_gate.md": {
		"61: M14 is 完成 but P0 item IN-11 is not checked",
	},
	// M28 完成 does not require the P0 and P1 items of a 1.0 之后 milestone
	// (IN-04 in M15), but still requires those of every other milestone.
	"release_gate_deferred.md": {
		"60: M28 is 完成 but P1（提前） item IN-03 is not checked",
		"66: M28 is 完成 but P0 item ENG-03 is not checked",
	},
	// The M14 installer gate is unchanged: IN-11 is in a 1.0 之后 milestone
	// but is still a P0 item of a milestone ≤ M14.
	"m14_gate_deferred.md": {
		"61: M14 is 完成 but P0 item IN-11 is not checked",
	},

	// Rule 6: the §0 当前里程碑 line. Fixtures without a §0 skip it.
	"current_ok.md":       nil,
	"current_all_done.md": nil,
	"current_wrong_id.md": {
		`7: §0 当前里程碑 should be "M1a 无头引擎（进行中）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},
	"current_wrong_name.md": {
		`7: §0 当前里程碑 should be "M1a 无头引擎（进行中）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},
	"current_wrong_status.md": {
		`7: §0 当前里程碑 should be "M1a 无头引擎（进行中）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},
	"current_none_early.md": {
		`7: §0 当前里程碑 should be "M1a 无头引擎（进行中）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},
	"current_all_done_named.md": {
		`7: §0 当前里程碑 should be "无（全部完成）" (every milestone in §4.1 is 完成 or 1.0 之后)`,
	},
	"current_missing.md": {
		`3: §0 has no 当前里程碑 line (want "**当前里程碑**：M1a 无头引擎（进行中）。")`,
	},
	"current_duplicate.md": {
		"9: second 当前里程碑 line in §0 (first at line 7)",
	},
	"current_malformed.md": {
		`7: malformed 当前里程碑 line (want "**当前里程碑**：<ID> <name>（<status>）。" or "**当前里程碑**：无（全部完成）。")`,
	},
	"current_list_item.md": {
		`7: malformed 当前里程碑 line (want "**当前里程碑**：<ID> <name>（<status>）。" or "**当前里程碑**：无（全部完成）。")`,
	},
	"current_outside.md": {
		`47: 当前里程碑 line outside §0 (want it under a "## 0. <title>" heading)`,
	},
	// 1.0 之后 rows are skipped: the current milestone is the first row that
	// is neither 完成 nor 1.0 之后 (M1a here is deferred, M14 is next).
	"current_deferred_skipped.md": nil,
	"current_deferred_named.md": {
		`7: §0 当前里程碑 should be "M14 首个安装包（未开始）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},
	// Every row 完成 or 1.0 之后 reads 无（全部完成）.
	"current_all_done_deferred.md": nil,
	"current_all_done_deferred_named.md": {
		`7: §0 当前里程碑 should be "无（全部完成）" (every milestone in §4.1 is 完成 or 1.0 之后)`,
	},
	// "## 0.一页概览" is not a §0 heading, so the line is outside §0.
	"current_bad_heading.md": {
		`7: 当前里程碑 line outside §0 (want it under a "## 0. <title>" heading)`,
	},
	// §4.1 has no 名称 column: the line needs no name.
	"current_no_name.md": {
		`7: §0 当前里程碑 should be "M1a（进行中）" (first milestone in §4.1 that is not 完成 or 1.0 之后)`,
	},

	// Parse errors.
	"parse_table.md": {
		`13: IN-06: unrecognized priority "P3" (want P0, P1, P1（提前）, P2 or P2（可选）)`,
		`14: IN-07: unrecognized milestone "以后" (want M<N>, M<N><letter> or Backlog)`,
		"19: table row has 5 cells, header has 6",
		`20: cannot parse ID cell "IN 13"`,
		"23: table header is not followed by a |---| separator row",
		"31: table in §3.3 has no ID column",
	},
	"parse_lists.md": {
		"9: IN-02 is not listed in the M0a checklist", // its checklist line is malformed
		"39: checklist item outside a milestone section",
		`54: malformed checklist line (want "- [ ] ID name" or "- [x] ID name")`,
		`57: checklist item does not start with an ID: "没有 ID 的条目"`,
		`81: cannot parse Backlog entry "见下文" (want ID or "ID 至 ID")`,
		"96: second Backlog section (first at line 80)",
	},
	"empty.md": {
		"1: no feature table (| ID | … | 优先级 | 里程碑 | … |) found in §3",
		"1: no milestone sections (### M<N> …) found",
		"1: no overview table (| 里程碑 | … | 状态 |) found in §4.1",
		"1: no spike table (| ID | 问题 | 里程碑 | … |) found in §5.1",
	},
}

func readFixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestFixtures(t *testing.T) {
	for name, want := range fixtures {
		t.Run(strings.TrimSuffix(name, ".md"), func(t *testing.T) {
			_, problems := lint(readFixture(t, name))
			var got []string
			for _, p := range problems {
				got = append(got, fmt.Sprintf("%d: %s", p.Line, p.Msg))
			}
			if !slices.Equal(got, want) {
				t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
			}
		})
	}
}

// Every fixture on disk must have expectations, so none goes unchecked.
func TestFixturesCovered(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("testdata", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if _, ok := fixtures[filepath.Base(f)]; !ok {
			t.Errorf("%s has no entry in fixtures", f)
		}
	}
	if len(files) != len(fixtures) {
		t.Errorf("%d fixture files, %d fixture entries", len(files), len(fixtures))
	}
}

func TestParseValid(t *testing.T) {
	rm, problems := parse(bytes.NewReader(readFixture(t, "valid.md")))
	if len(problems) != 0 {
		t.Fatalf("parse problems: %v", problems)
	}
	if got := len(rm.Items); got != 18 {
		t.Errorf("items = %d, want 18", got)
	}
	var names []string
	for _, m := range rm.Milestones {
		names = append(names, m.Name)
	}
	if want := []string{"M0a", "M1a", "M14", "M15", "M28"}; !slices.Equal(names, want) {
		t.Errorf("milestones = %v, want %v (the fenced M99 must be ignored)", names, want)
	}
	m14 := rm.Milestones[2]
	if m14.Title != "首个安装包" || m14.Version != "v0.14.0" {
		t.Errorf("M14 title, version = %q, %q", m14.Title, m14.Version)
	}
	r := rm.Milestones[3].Entries[1]
	if r.From != "IN-05" || r.To != "IN-06" || !r.Optional || r.Checked {
		t.Errorf("M15 range entry = %+v", *r)
	}
	if got := rm.Milestones[0].Entries[2]; !got.Checked {
		t.Errorf("[X] must count as checked: %+v", *got)
	}
	var backlogIDs []string
	for _, e := range rm.Backlog {
		backlogIDs = append(backlogIDs, e.String())
	}
	if want := []string{"IN-07 至 IN-08", "IN-09"}; !slices.Equal(backlogIDs, want) {
		t.Errorf("Backlog = %v, want %v", backlogIDs, want)
	}
	in05 := rm.Items[4]
	if in05.ID != "IN-05" || in05.Priority != P2Optional {
		t.Errorf("IN-05 = %+v (escaped pipe or ASCII parentheses mishandled)", *in05)
	}
	eng02 := rm.Items[12]
	if eng02.ID != "ENG-02" || !eng02.Deprecated || !eng02.DeprecationNote || eng02.Milestone != "" {
		t.Errorf("ENG-02 = %+v", *eng02)
	}
	spk := rm.Items[16]
	if spk.ID != "SPK-01" || !spk.Spike || spk.Milestone != "M0a" {
		t.Errorf("SPK-01 = %+v", *spk)
	}
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		code       int
		stdout     string
		stderrPart string
	}{
		{
			name:   "valid",
			args:   []string{"-file", "testdata/valid.md"},
			code:   0,
			stdout: "valid.md: ok: 18 IDs (1 deprecated), 5 milestones, 5 of 14 checklist items checked\n",
		},
		{
			name: "problems",
			args: []string{"-file", "testdata/status_not_started_checked.md"},
			code: 1,
			stdout: "status_not_started_checked.md:46: M15 is 未开始 but has checked items (want 进行中 or 完成)\n" +
				"status_not_started_checked.md:47: M28 is 未开始 but has checked items (want 进行中)\n",
		},
		{
			name:       "missing file",
			args:       []string{"-file", "testdata/no-such-file.md"},
			code:       2,
			stderrPart: "no-such-file.md",
		},
		{
			name:       "extra argument",
			args:       []string{"ROADMAP.md"},
			code:       2,
			stderrPart: `unexpected argument "ROADMAP.md"`,
		},
		{
			name:       "bad flag",
			args:       []string{"-nope"},
			code:       2,
			stderrPart: "-nope",
		},
		{
			name:       "help",
			args:       []string{"-h"},
			code:       0,
			stderrPart: "-file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
			if code != tt.code {
				t.Errorf("exit code = %d, want %d (stderr %q)", code, tt.code, stderr.String())
			}
			if stdout.String() != tt.stdout {
				t.Errorf("stdout = %q, want %q", stdout.String(), tt.stdout)
			}
			if !strings.Contains(stderr.String(), tt.stderrPart) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), tt.stderrPart)
			}
		})
	}
}

// A byte order mark, as some Windows editors write it, must not hide the
// heading on line 1.
func TestParseBOM(t *testing.T) {
	data := append([]byte{0xEF, 0xBB, 0xBF}, "### M0a 骨架与键表\n"...)
	rm, _ := parse(bytes.NewReader(data))
	if len(rm.Milestones) != 1 || rm.Milestones[0].Name != "M0a" {
		t.Errorf("milestones = %+v, want M0a from line 1", rm.Milestones)
	}
}

// CRLF line endings, as a Windows checkout without .gitattributes would
// have them, must not change the result.
func TestParseCRLF(t *testing.T) {
	data := bytes.ReplaceAll(readFixture(t, "valid.md"), []byte("\n"), []byte("\r\n"))
	if _, problems := lint(data); len(problems) != 0 {
		t.Errorf("problems: %v", problems)
	}
}

func TestSplitRow(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"| a | b |", []string{"a", "b"}},
		{"|a|b", []string{"a", "b"}},
		{"| a \\| b | c |", []string{"a \\| b", "c"}},
		{"| a | |", []string{"a", ""}},
		{"|---|:---:|", []string{"---", ":---:"}},
	}
	for _, tt := range tests {
		if got := splitRow(tt.in); !slices.Equal(got, tt.want) {
			t.Errorf("splitRow(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePriority(t *testing.T) {
	tests := []struct {
		in   string
		want Priority
		ok   bool
	}{
		{"P0", P0, true},
		{"**P0**", P0, true},
		{"P1", P1, true},
		{"P1（提前）", P1Early, true},
		{"P1 (提前)", P1Early, true},
		{"P1（ 提前 ）", P1Early, true},
		{"P2", P2, true},
		{"P2（可选）", P2Optional, true},
		{"P2(可选)", P2Optional, true},
		{"P1（可选）", PriorityNone, false},
		{"P3", PriorityNone, false},
		{"", PriorityNone, false},
	}
	for _, tt := range tests {
		got, ok := parsePriority(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parsePriority(%q) = %v, %v; want %v, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

func TestExpandEntry(t *testing.T) {
	tests := []struct {
		from, to string
		want     []string
		err      string
	}{
		{"IN-01", "", []string{"IN-01"}, ""},
		{"ANA-19", "ANA-23", []string{"ANA-19", "ANA-20", "ANA-21", "ANA-22", "ANA-23"}, ""},
		{"SCR-09", "SCR-11", []string{"SCR-09", "SCR-10", "SCR-11"}, ""},
		{"IN-05", "ENG-06", nil, "ends have different prefixes"},
		{"IN-06", "IN-05", nil, "end must be greater than start"},
		{"IN-06", "IN-06", nil, "end must be greater than start"},
		{"in-01", "in-02", nil, "in-01 is not a valid ID"},
		{"IN-01", "IN-2", nil, "IN-2 is not a valid ID"},
		{"IN-01", "IN-101", nil, "more than 100 IDs; list them separately"},
		{"IN-01", "IN-99999999999999999999", nil, "IN-99999999999999999999 is not a valid ID"},
	}
	for _, tt := range tests {
		got, err := expandEntry(&Entry{From: tt.from, To: tt.to})
		gotErr := ""
		if err != nil {
			gotErr = err.Error()
		}
		if !slices.Equal(got, tt.want) || gotErr != tt.err {
			t.Errorf("expandEntry(%s, %s) = %v, %q; want %v, %q", tt.from, tt.to, got, gotErr, tt.want, tt.err)
		}
	}
}

func TestChecklistEntry(t *testing.T) {
	tests := []struct {
		line     string
		from, to string
		checked  bool
		optional bool
	}{
		{"  - [ ] ENG-01 仓库初始化", "ENG-01", "", false, false},
		{"  - [x] ENG-22 `task roadmap:check`", "ENG-22", "", true, false},
		{"- [X] IN-01", "IN-01", "", true, false},
		{"  * [ ] ANA-19 至 ANA-23（可选）", "ANA-19", "ANA-23", false, true},
		{"  + [ ] ANA-19~ANA-23 (可选)", "ANA-19", "ANA-23", false, true},
		{"  - [ ] ANA-19 – ANA-23", "ANA-19", "ANA-23", false, false},
		{"  - [ ] DMN-16 诊断包（ 可选 ）", "DMN-16", "", false, true},
		{"  - [ ] ~~MAP-24~~ 脉冲", "MAP-24", "", false, false},
	}
	for _, tt := range tests {
		p := &parser{rm: &Roadmap{}, milestone: &Milestone{Name: "M1a"}}
		if !p.checklist(tt.line) || len(p.problems) != 0 || len(p.milestone.Entries) != 1 {
			t.Errorf("checklist(%q): problems %v, entries %d", tt.line, p.problems, len(p.milestone.Entries))
			continue
		}
		e := p.milestone.Entries[0]
		if e.From != tt.from || e.To != tt.to || e.Checked != tt.checked || e.Optional != tt.optional {
			t.Errorf("checklist(%q) = %+v", tt.line, *e)
		}
	}
}

func TestCurrentLine(t *testing.T) {
	tests := []struct {
		line                     string
		milestone, title, status string
		ok                       bool
	}{
		{"**当前里程碑**：M0b SendInput 验证（未开始）。", "M0b", "SendInput 验证", "未开始", true},
		{"**当前里程碑**: M0b SendInput 验证 (未开始)", "M0b", "SendInput 验证", "未开始", true},
		{"**当前里程碑** ： M14 首个安装包（ 进行中 ）.", "M14", "首个安装包", "进行中", true},
		{"**当前里程碑**：M24 分享安全与 API 1.0（未开始）。", "M24", "分享安全与 API 1.0", "未开始", true},
		{"**当前里程碑**：无（全部完成）。", "", "", "全部完成", true},
		{"**当前里程碑**:无(全部完成)", "", "", "全部完成", true},
		{"**当前里程碑**：M0b（未开始）。", "M0b", "", "未开始", true}, // no name; check compares it
		{"**当前里程碑**：M0b　SendInput 验证（未开始）。", "M0b", "SendInput 验证", "未开始", true},
		{"**当前里程碑**：　M0b SendInput 验证　（未开始）　。", "M0b", "SendInput 验证", "未开始", true},
		{"**当前里程碑**：SendInput 验证（未开始）。", "", "", "", false},     // no ID
		{"**当前里程碑**：M0b SendInput 验证。", "", "", "", false},      // no status
		{"**当前里程碑** M0b SendInput 验证（未开始）。", "", "", "", false}, // no colon
		{"**当前里程碑**：M0b SendInput 验证（未开始）。见下文", "", "", "", false},
		{"**当前里程碑：**M0b SendInput 验证（未开始）。", "", "", "", false},   // colon inside the bold
		{"- **当前里程碑**：M0b SendInput 验证（未开始）。", "", "", "", false}, // list item
	}
	for _, tt := range tests {
		p := &parser{rm: &Roadmap{}, chapter: "0", line: 5}
		p.currentLine(tt.line)
		cur := p.rm.Current
		if !tt.ok {
			if cur != nil || len(p.problems) != 1 || p.rm.CurrentLine != 5 {
				t.Errorf("currentLine(%q): current %+v, problems %v; want one malformed-line problem", tt.line, cur, p.problems)
			}
			continue
		}
		if cur == nil || len(p.problems) != 0 {
			t.Errorf("currentLine(%q): problems %v", tt.line, p.problems)
			continue
		}
		if cur.Milestone != tt.milestone || cur.Title != tt.title || cur.Status != tt.status || cur.Line != 5 {
			t.Errorf("currentLine(%q) = %+v", tt.line, *cur)
		}
	}
}

// Lines that look like a 当前里程碑 line go to currentLine, which parses
// them or reports them at that line; other lines are running text.
func TestCurrentLike(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"**当前里程碑**：M0b SendInput 验证（未开始）。", true},
		{"**当前里程碑：**M0b SendInput 验证（未开始）。", true},
		{"** 当前里程碑**：M0b", true},
		{"- **当前里程碑**：M0b", true},
		{"* **当前里程碑**：M0b", true},
		{"> **当前里程碑**：M0b", true},
		{">**当前里程碑**：M0b", true},
		{"**怎么读本文件**：平时只看本节和当前里程碑的小节。", false},
		{"- 每次会话先读 CLAUDE.md 和当前里程碑。", false},
		{`§0 的"当前里程碑"写第一个未完成的里程碑。`, false},
		{"当前里程碑：M0b", false},
	}
	for _, tt := range tests {
		if got := currentLikeRe.MatchString(tt.line); got != tt.want {
			t.Errorf("currentLikeRe.MatchString(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestSplitMilestoneTitle(t *testing.T) {
	tests := []struct{ in, title, version string }{
		{"骨架与键表", "骨架与键表", ""},
		{"无头引擎（v0.1.0-a）", "无头引擎", "v0.1.0-a"},
		{"无头引擎 (v0.1.0-a)", "无头引擎", "v0.1.0-a"},
		{"首个安装包（v0.14.0，首个公开预览）", "首个安装包", "v0.14.0"},
		{"1.0（v1.0.0）", "1.0", "v1.0.0"},
		{"两段扳机（可选）", "两段扳机（可选）", ""},
	}
	for _, tt := range tests {
		title, version := splitMilestoneTitle(tt.in)
		if title != tt.title || version != tt.version {
			t.Errorf("splitMilestoneTitle(%q) = %q, %q; want %q, %q", tt.in, title, version, tt.title, tt.version)
		}
	}
}

func TestMilestoneOrder(t *testing.T) {
	order := []string{"M0a", "M0b", "M0d", "M1a", "M1d", "M2", "M9", "M10", "M14", "M28"}
	for i := 1; i < len(order); i++ {
		a, okA := parseMilestone(order[i-1])
		b, okB := parseMilestone(order[i])
		if !okA || !okB || !a.less(b) || b.less(a) {
			t.Errorf("want %s < %s", order[i-1], order[i])
		}
	}
	for _, bad := range []string{"M", "m1", "M1A", "M1ab", "Backlog", "M-1"} {
		if _, ok := parseMilestone(bad); ok {
			t.Errorf("parseMilestone(%q) accepted", bad)
		}
	}
}

// FuzzParse checks that arbitrary input never panics and that every problem
// points at a line of the input (or line 1 for whole-file problems).
func FuzzParse(f *testing.F) {
	for name := range fixtures {
		f.Add(readFixture(f, name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		_, problems := lint(data)
		lines := bytes.Count(data, []byte("\n")) + 1
		for _, p := range problems {
			if p.Line < 1 || p.Line > lines+1 {
				t.Errorf("problem at line %d of %d: %s", p.Line, lines, p.Msg)
			}
		}
	})
}
