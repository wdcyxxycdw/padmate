package cspfile

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestInstallFromEntryPicksNewestVersion(t *testing.T) {
	exeIn := func(root, folder string) string {
		return filepath.Join(root, folder, "CLIP STUDIO PAINT", ExeName)
	}
	tests := []struct {
		name    string
		withExe []string // folders under the install location that hold the exe
		empty   []string // CLIP STUDIO* folders without the exe
		noise   int      // unrelated folders created first
		want    string   // folder expected; "" = not found
	}{
		{name: "1.10 beats 1.5", withExe: []string{"CLIP STUDIO 1.5", "CLIP STUDIO 1.10"}, want: "CLIP STUDIO 1.10"},
		{name: "1.5 beats no number", withExe: []string{"CLIP STUDIO", "CLIP STUDIO 1.5"}, want: "CLIP STUDIO 1.5"},
		{name: "no number alone", withExe: []string{"CLIP STUDIO"}, want: "CLIP STUDIO"},
		{name: "higher version without exe is skipped", withExe: []string{"CLIP STUDIO 1.5"}, empty: []string{"CLIP STUDIO 2.0"}, want: "CLIP STUDIO 1.5"},
		{name: "only folders without exe", empty: []string{"CLIP STUDIO 1.5"}, want: ""},
		{name: "three parts", withExe: []string{"CLIP STUDIO 1.9.9", "CLIP STUDIO 1.10.1", "CLIP STUDIO 1.10"}, want: "CLIP STUDIO 1.10.1"},
		{name: "many unrelated entries first", withExe: []string{"CLIP STUDIO 1.5"}, noise: 3 * maxInstallProbe, want: "CLIP STUDIO 1.5"},
		{name: "lowercase prefix", withExe: []string{"clip studio 1.5", "clip studio 1.7"}, want: "clip studio 1.7"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			loc := t.TempDir()
			for i := range tc.noise {
				writeFile(t, filepath.Join(loc, fmt.Sprintf("A%05d", i), "f"), "x")
			}
			for _, f := range tc.withExe {
				writeFile(t, exeIn(loc, f), "x")
			}
			for _, f := range tc.empty {
				writeFile(t, filepath.Join(loc, f, "CLIP STUDIO PAINT", "other.txt"), "x")
			}
			in, ok := InstallFromEntry("CLIP STUDIO PAINT", loc, "")
			if tc.want == "" {
				if ok {
					t.Fatalf("found %+v", in)
				}
				return
			}
			if !ok || in.Exe != exeIn(loc, tc.want) {
				t.Fatalf("got %+v %v, want %s", in, ok, tc.want)
			}
		})
	}
}

func TestInstallFromEntryIconStaysAuthoritative(t *testing.T) {
	loc := t.TempDir()
	old := filepath.Join(loc, "CLIP STUDIO 1.5", "CLIP STUDIO PAINT", ExeName)
	writeFile(t, old, "x")
	writeFile(t, filepath.Join(loc, "CLIP STUDIO 1.10", "CLIP STUDIO PAINT", ExeName), "x")
	in, ok := InstallFromEntry("CLIP STUDIO PAINT", loc, `"`+old+`",0`)
	if !ok || in.Exe != old {
		t.Fatalf("got %+v %v, want the DisplayIcon exe", in, ok)
	}
}

func TestFolderVersion(t *testing.T) {
	tests := []struct {
		in   string
		want []int
	}{
		{"CLIP STUDIO", nil},
		{"CLIP STUDIO 1.5", []int{1, 5}},
		{"CLIP STUDIO 1.10", []int{1, 10}},
		{"CLIP STUDIO 1.10.2", []int{1, 10, 2}},
		{"CLIP STUDIO 2", []int{2}},
		{"CLIP STUDIO 1.x", []int{1}},
	}
	for _, tc := range tests {
		got := folderVersion(tc.in)
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("folderVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	if compareVersion([]int{1, 10}, []int{1, 5}) <= 0 || compareVersion(nil, []int{0}) >= 0 ||
		compareVersion([]int{1}, []int{1, 0}) != 0 {
		t.Error("compareVersion")
	}
}

func TestFindDataDirsManyUnrelatedEntries(t *testing.T) {
	appData, local := t.TempDir(), t.TempDir()
	celsys := filepath.Join(appData, "CELSYSUserData", "CELSYS")
	// Far more unrelated folders than any cap, all sorting before the data folder
	// and (on most file systems) listed before it.
	for i := range 3 * maxVersionDirs {
		writeFile(t, filepath.Join(celsys, fmt.Sprintf("A%05d", i), "f"), "x")
	}
	want := filepath.Join(celsys, "CLIPStudioPaintVer5_0_0")
	writeFile(t, filepath.Join(want, "Tool", "EditImageTool.todb"), "t")

	// Store packages: many without the CELSYS folder, one with it.
	pkgs := filepath.Join(local, "Packages")
	for i := range 2 * maxPackageDirs {
		writeFile(t, filepath.Join(pkgs, fmt.Sprintf("A%05d", i), "f"), "x")
	}
	store := filepath.Join(pkgs, "Z.Store_1", "LocalCache", "Roaming", "CELSYSUserData", "CELSYS", "CLIPStudioPaintVer5_0_0")
	writeFile(t, filepath.Join(store, "Tool", "EditImageTool.todb"), "t")

	got := FindDataDirs(Roots{AppData: appData, LocalAppData: local})
	if len(got) != 2 {
		t.Fatalf("found %+v", got)
	}
	seen := map[string]bool{got[0].Path: true, got[1].Path: true}
	if !seen[want] || !seen[store] {
		t.Errorf("found %+v, want %s and %s", got, want, store)
	}
}
