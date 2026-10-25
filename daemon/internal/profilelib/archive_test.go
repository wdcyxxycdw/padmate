package profilelib

import (
	"archive/zip"
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

const testManifest = `{"type":"padmate-profile","format":1,"name":"画画","padmate":"test"}`

// zipEntry is one entry of a hand-made archive. mode, when set, is the
// entry's file mode; flags are its general purpose flags. A raw entry
// stores data as it is under method, claiming size bytes when size is set.
type zipEntry struct {
	name   string
	data   string
	mode   fs.FileMode
	method uint16
	flags  uint16
	raw    bool
	size   uint64
}

func makeZip(t testing.TB, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: e.method, Flags: e.flags}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		create := w.CreateHeader
		if e.raw {
			h.CRC32 = crc32.ChecksumIEEE([]byte(e.data))
			h.CompressedSize64, h.UncompressedSize64 = uint64(len(e.data)), uint64(len(e.data))
			if e.size > 0 {
				h.UncompressedSize64 = e.size
			}
			create = w.CreateRaw
		}
		f, err := create(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(e.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// validEntries is a small valid archive with a script in a subfolder.
func validEntries() []zipEntry {
	return []zipEntry{
		{name: ArchiveManifest, data: testManifest},
		{name: "profile/profile.json", data: validProfile},
		{name: "profile/main.ts", data: "import {x} from \"./lib/util\";\n"},
		{name: "profile/lib/util.ts", data: "export const x = 1;\n"},
	}
}

func TestReadArchive(t *testing.T) {
	a, err := ReadArchive(makeZip(t, validEntries()...))
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest.Name != "画画" || a.Manifest.Padmate != "test" || a.Profile.Name != "画画" {
		t.Fatalf("%+v %+v", a.Manifest, a.Profile)
	}
	if got, want := a.Paths(), []string{"lib/", "lib/util.ts", "main.ts", "profile.json"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
	// Folders listed after their contents, the profile folder itself, and
	// history and generated macro declarations, which are skipped.
	entries := append(validEntries(),
		zipEntry{name: "profile/"},
		zipEntry{name: "profile/lib/"},
		zipEntry{name: "profile/empty/"},
		zipEntry{name: "profile/.history/20261001-120000-0123abcd.json", data: validProfile},
		zipEntry{name: "profile/.HISTORY/"},
		zipEntry{name: "profile/macros/a.ts", data: "export {};\n"},
		zipEntry{name: "profile/macros/.padmate/gen.d.ts", data: "declare const x: 1;\n"},
		zipEntry{name: "profile/notes.txt", data: "\xff\xfe notes need not be UTF-8"},
	)
	a, err = ReadArchive(makeZip(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := a.Paths(), []string{"empty/", "lib/", "lib/util.ts", "macros/", "macros/a.ts", "main.ts", "notes.txt", "profile.json"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
}

func TestReadArchiveRefusals(t *testing.T) {
	replace := func(name string, e zipEntry) []zipEntry {
		entries := validEntries()
		for i := range entries {
			if entries[i].name == name {
				entries[i] = e
			}
		}
		return entries
	}
	with := func(extra ...zipEntry) []zipEntry { return append(validEntries(), extra...) }
	many := validEntries()
	for i := range MaxCopyEntries {
		many = append(many, zipEntry{name: fmt.Sprintf("profile/f%03d.ts", i)})
	}
	implicit := validEntries()
	for i := range MaxCopyEntries / 2 {
		implicit = append(implicit, zipEntry{name: fmt.Sprintf("profile/d%03d/f.ts", i)})
	}
	big := strings.Repeat("x", MaxCopyBytes/2+1)
	crc := makeZip(t, replace("profile/main.ts", zipEntry{name: "profile/main.ts", data: "export const secret = 1;\n", method: zip.Store})...)
	crc[bytes.Index(crc, []byte("secret"))] = 'S'
	for _, tc := range []struct {
		name   string
		data   []byte
		reason string
		path   string
	}{
		{"not a zip", []byte("not a zip"), ArchiveFormatReason, ""},
		{"empty", nil, ArchiveFormatReason, ""},
		{"too large", make([]byte, MaxArchiveBytes+1), ArchiveLimitReason, ""},
		{"no manifest", makeZip(t, validEntries()[1:]...), ArchiveFormatReason, ""},
		{"no profile.json", makeZip(t, replace("profile/profile.json", zipEntry{name: "profile/other.json", data: "{}"})...), ArchiveFormatReason, ""},
		{"profile.json case", makeZip(t, replace("profile/profile.json", zipEntry{name: "profile/Profile.json", data: validProfile})...), ArchiveFormatReason, ""},
		{"wrong type", makeZip(t, replace(ArchiveManifest, zipEntry{name: ArchiveManifest, data: `{"type":"other","format":1}`})...), ArchiveFormatReason, ArchiveManifest},
		{"manifest not json", makeZip(t, replace(ArchiveManifest, zipEntry{name: ArchiveManifest, data: `[`})...), ArchiveFormatReason, ArchiveManifest},
		{"format 0", makeZip(t, replace(ArchiveManifest, zipEntry{name: ArchiveManifest, data: `{"type":"padmate-profile"}`})...), ArchiveFormatReason, ArchiveManifest},
		{"newer format", makeZip(t, replace(ArchiveManifest, zipEntry{name: ArchiveManifest, data: `{"type":"padmate-profile","format":2}`})...), ArchiveVersionReason, ArchiveManifest},
		{"two manifests", makeZip(t, with(zipEntry{name: ArchiveManifest, data: testManifest})...), ArchiveEntryReason, ArchiveManifest},
		{"outside the folder", makeZip(t, with(zipEntry{name: "evil.ts"})...), ArchiveEntryReason, "evil.ts"},
		{"parent", makeZip(t, with(zipEntry{name: "profile/../evil.ts"})...), ArchiveEntryReason, "profile/../evil.ts"},
		{"dot", makeZip(t, with(zipEntry{name: "profile/./evil.ts"})...), ArchiveEntryReason, "profile/./evil.ts"},
		{"absolute", makeZip(t, with(zipEntry{name: "/profile/evil.ts"})...), ArchiveEntryReason, "/profile/evil.ts"},
		{"empty element", makeZip(t, with(zipEntry{name: "profile//evil.ts"})...), ArchiveEntryReason, "profile//evil.ts"},
		{"backslash", makeZip(t, with(zipEntry{name: `profile/..\evil.ts`})...), ArchiveEntryReason, `profile/..\evil.ts`},
		{"drive", makeZip(t, with(zipEntry{name: "profile/C:evil.ts"})...), ArchiveEntryReason, "profile/C:evil.ts"},
		{"stream", makeZip(t, with(zipEntry{name: "profile/main.ts:stream"})...), ArchiveEntryReason, "profile/main.ts:stream"},
		{"device", makeZip(t, with(zipEntry{name: "profile/lib/CON.ts"})...), ArchiveEntryReason, "profile/lib/CON.ts"},
		{"device superscript", makeZip(t, with(zipEntry{name: "profile/com¹.txt"})...), ArchiveEntryReason, "profile/com¹.txt"},
		{"device console", makeZip(t, with(zipEntry{name: "profile/conin$"})...), ArchiveEntryReason, "profile/conin$"},
		{"trailing dot", makeZip(t, with(zipEntry{name: "profile/lib./a.ts"})...), ArchiveEntryReason, "profile/lib./a.ts"},
		{"trailing space", makeZip(t, with(zipEntry{name: "profile/a.ts "})...), ArchiveEntryReason, "profile/a.ts "},
		{"control", makeZip(t, with(zipEntry{name: "profile/a\x01.ts"})...), ArchiveEntryReason, "profile/a\x01.ts"},
		{"not utf-8", makeZip(t, with(zipEntry{name: "profile/\xff.ts"})...), ArchiveEntryReason, "profile/\xff.ts"},
		{"long element", makeZip(t, with(zipEntry{name: "profile/" + strings.Repeat("a", 256)})...), ArchiveEntryReason, "profile/" + strings.Repeat("a", 256)},
		{"long path", makeZip(t, with(zipEntry{name: "profile/" + strings.Repeat("a/", MaxArchivePath/2) + "b"})...), ArchiveEntryReason, "profile/" + strings.Repeat("a/", MaxArchivePath/2) + "b"},
		{"short name", makeZip(t, with(zipEntry{name: "profile/abcdef~1.ts"})...), ArchiveEntryReason, "profile/abcdef~1.ts"},
		{"shell folder", makeZip(t, with(zipEntry{name: "profile/x.{20D04FE0-3AEA-1069-A2D8-08002B30309D}/a.ts"})...), ArchiveEntryReason, "profile/x.{20D04FE0-3AEA-1069-A2D8-08002B30309D}/a.ts"},
		{"program", makeZip(t, with(zipEntry{name: "profile/run.bat", data: "calc"})...), ArchiveEntryReason, "profile/run.bat"},
		{"shortcut", makeZip(t, with(zipEntry{name: "profile/lib/x.lnk"})...), ArchiveEntryReason, "profile/lib/x.lnk"},
		{"internet shortcut", makeZip(t, with(zipEntry{name: "profile/x.URL", data: "[InternetShortcut]"})...), ArchiveEntryReason, "profile/x.URL"},
		{"no extension", makeZip(t, with(zipEntry{name: "profile/LICENSE"})...), ArchiveEntryReason, "profile/LICENSE"},
		{"skipped folder", makeZip(t, with(zipEntry{name: "profile/.history/x.exe"})...), ArchiveEntryReason, "profile/.history/x.exe"},
		{"symlink", makeZip(t, with(zipEntry{name: "profile/link.ts", data: "../../secret", mode: fs.ModeSymlink | 0o777})...), ArchiveEntryReason, "profile/link.ts"},
		{"named pipe", makeZip(t, with(zipEntry{name: "profile/pipe.ts", mode: fs.ModeNamedPipe | 0o666})...), ArchiveEntryReason, "profile/pipe.ts"},
		{"duplicate", makeZip(t, with(zipEntry{name: "profile/main.ts"})...), ArchiveEntryReason, "profile/main.ts"},
		{"duplicate case", makeZip(t, with(zipEntry{name: "profile/MAIN.ts"})...), ArchiveEntryReason, "profile/MAIN.ts"},
		{"file as folder", makeZip(t, with(zipEntry{name: "profile/main.ts/a.ts"})...), ArchiveEntryReason, "profile/main.ts/a.ts"},
		{"folder as file", makeZip(t, with(zipEntry{name: "profile/lib"})...), ArchiveEntryReason, "profile/lib"},
		{"folder with data", makeZip(t, with(zipEntry{name: "profile/data/", raw: true, size: 1})...), ArchiveFormatReason, "profile/data/"},
		{"encrypted", makeZip(t, with(zipEntry{name: "profile/secret.ts", flags: 0x1})...), ArchiveFormatReason, "profile/secret.ts"},
		{"method", makeZip(t, with(zipEntry{name: "profile/x.ts", data: "x", method: 99, raw: true})...), ArchiveFormatReason, "profile/x.ts"},
		{"checksum", crc, ArchiveFormatReason, "profile/main.ts"},
		{"entries", makeZip(t, many...), ArchiveLimitReason, ""},
		{"implicit folders", makeZip(t, implicit...), ArchiveLimitReason, fmt.Sprintf("profile/d%03d/f.ts", MaxCopyEntries/2-2)},
		{"bytes", makeZip(t, with(zipEntry{name: "profile/a.txt", data: big, method: zip.Deflate}, zipEntry{name: "profile/b.txt", data: big, method: zip.Deflate})...), ArchiveLimitReason, ""},
		{"depth", makeZip(t, with(zipEntry{name: "profile/1/2/3/4/5/6/7/8/9/deep.ts"})...), ArchiveLimitReason, "profile/1/2/3/4/5/6/7/8/9/deep.ts"},
		{"folder depth", makeZip(t, with(zipEntry{name: "profile/1/2/3/4/5/6/7/8/9/"})...), ArchiveLimitReason, "profile/1/2/3/4/5/6/7/8/9/"},
		{"profile.json size", makeZip(t, replace("profile/profile.json", zipEntry{name: "profile/profile.json", data: strings.Repeat(" ", MaxDocumentBytes+1), method: zip.Deflate})...), ArchiveLimitReason, "profile/profile.json"},
		{"script encoding", makeZip(t, with(zipEntry{name: "profile/bad.ts", data: "const a = \"\xff\";"})...), ArchiveEncodingReason, "profile/bad.ts"},
		{"json encoding", makeZip(t, with(zipEntry{name: "profile/tsconfig.json", data: "\xff\xfe{\x00}\x00"})...), ArchiveEncodingReason, "profile/tsconfig.json"},
		{"invalid profile", makeZip(t, replace("profile/profile.json", zipEntry{name: "profile/profile.json", data: `{"schemaVersion":0}`})...), ArchiveProfileReason, "profile/profile.json"},
		{"utf-16 profile", makeZip(t, replace("profile/profile.json", zipEntry{name: "profile/profile.json", data: "\xff\xfe{\x00}\x00"})...), ArchiveEncodingReason, "profile/profile.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := ReadArchive(tc.data)
			var archiveErr *ArchiveError
			if !errors.As(err, &archiveErr) || archiveErr.Reason != tc.reason || archiveErr.Path != tc.path {
				t.Fatalf("got %+v %v, want %s %q", a, err, tc.reason, tc.path)
			}
			if tc.reason == ArchiveProfileReason && len(archiveErr.Diagnostics) == 0 {
				t.Fatal("no diagnostics")
			}
		})
	}
}

// A zip bomb stops at the copy limit, whatever sizes its headers claim.
func TestReadArchiveBomb(t *testing.T) {
	data := makeZip(t, append(validEntries(), zipEntry{name: "profile/zeros.txt", data: strings.Repeat("\x00", MaxCopyBytes+1), method: zip.Deflate})...)
	if len(data) > MaxArchiveBytes {
		t.Fatal("bomb too large for the test", len(data))
	}
	var archiveErr *ArchiveError
	if _, err := ReadArchive(data); !errors.As(err, &archiveErr) || archiveErr.Reason != ArchiveLimitReason {
		t.Fatal(err)
	}
	// The same with sizes in the headers that understate the file.
	lie := bytes.ReplaceAll(data, le32(MaxCopyBytes+1), le32(1))
	if bytes.Equal(lie, data) {
		t.Fatal("size not found")
	}
	if _, err := ReadArchive(lie); !errors.As(err, &archiveErr) || archiveErr.Reason != ArchiveLimitReason && archiveErr.Reason != ArchiveFormatReason {
		t.Fatal(err)
	}
}

func le32(v uint32) []byte { return []byte{byte(v), byte(v >> 8), byte(v >> 16), byte(v >> 24)} }

func TestArchivePathProblem(t *testing.T) {
	for _, p := range []string{"main.ts", "lib/util.ts", ".vscode/settings.json", "画画/说明.md", "a b/c.d.ts", "con1.ts", "comx.ts", "lpt10.ts", ".history2/a", "abcdef~x.ts", "abcdefgh~1.ts", "a~1.json5", "~1.ts", "a~.ts", "x{a}.ts"} {
		if problem := archivePathProblem(p); problem != "" {
			t.Errorf("%q: %s", p, problem)
		}
	}
	for _, p := range []string{"", "/a", "a/", "a//b", "../a", "a/..", ".", "a\\b", "a:b", "a*b", "a?b", "a|b", "a<b", "a>b", `a"b`, "nul", "NUL.txt", "aux .ts", "lpt3", "COM9.json", "lpt²", "conout$.log", "a.", "a ", "a\tb", "a\x7fb", "abcdef~1.ts", "ABCDE~12", "a~1.ts", "x.{a", "a~1/b.ts", "x.{20D04FE0-3AEA-1069-A2D8-08002B30309D}", "x.{}/a.ts"} {
		if archivePathProblem(p) == "" {
			t.Errorf("accepted %q", p)
		}
	}
}

// Every macro the scripts page can create exports (ADR-0054, ADR-0055):
// the macro rule refuses whatever name the archive refuses below macros/.
// The seeds include the shared cases of daemon/testdata/macro-names.json.
func FuzzMacroFileExports(f *testing.F) {
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "macro-names.json"))
	if err != nil {
		f.Fatal(err)
	}
	var cases struct {
		Files []struct {
			File string `json:"file"`
		} `json:"files"`
	}
	if err := json.Unmarshal(data, &cases); err != nil || len(cases.Files) == 0 {
		f.Fatal(err, len(cases.Files))
	}
	for _, tc := range cases.Files {
		f.Add(tc.File)
	}
	for _, file := range []string{"conin$.ts", "Conout$.js", "abc~1.ts", "a.{x}.ts", "com².js", "nul .ts"} {
		f.Add(file)
	}
	f.Fuzz(func(t *testing.T, file string) {
		if profile.CheckMacroFile(file) != nil {
			return
		}
		p := profile.MacroDir + "/" + file
		if problem := archivePathProblem(p); problem != "" {
			t.Fatalf("macro %q does not export: %s", file, problem)
		}
		if problem := archiveFileProblem(p); problem != "" {
			t.Fatalf("macro %q does not export: %s", file, problem)
		}
	})
}

func TestExportImportRoundTrip(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	fixture(t, src, "paint", validProfile)
	files := map[string]string{
		"main.ts":                   "import {x} from \"./lib/util\";\n",
		"lib/util.ts":               "export const x = 1;\n",
		"lib/deep/a/b/c/d/e/f/g.ts": "// seven folders deep\n",
		"assets/notes.md":           "\x00\xff\x10",
	}
	for name, data := range files {
		writeFile(t, filepath.Join(src, "profiles", "paint", filepath.FromSlash(name)), data)
	}
	// Left out: history, and a macros folder from before the library,
	// with what the migration left in it.
	writeFile(t, filepath.Join(src, "profiles", "paint", ".history", "20261001-120000-0123abcd.json"), validProfile)
	writeFile(t, filepath.Join(src, "profiles", "paint", "macros", "lib", "left.ts"), "export {};\n")
	writeFile(t, filepath.Join(src, "profiles", "paint", "macros", ".padmate", "macros.d.ts"), "declare const m: 1;\n")
	if err := os.Mkdir(filepath.Join(src, "profiles", "paint", "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	exported, err := New(src).Export("paint", "v1.2.3")
	if err != nil || exported.Name != "画画" || len(exported.Data) == 0 {
		t.Fatal(exported.Name, err)
	}
	// The manifest comes first, then the folder in walk order.
	zr, err := zip.NewReader(bytes.NewReader(exported.Data), int64(len(exported.Data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	want := []string{ArchiveManifest, "profile/assets/", "profile/assets/notes.md", "profile/empty/", "profile/lib/", "profile/lib/deep/",
		"profile/lib/deep/a/", "profile/lib/deep/a/b/", "profile/lib/deep/a/b/c/", "profile/lib/deep/a/b/c/d/", "profile/lib/deep/a/b/c/d/e/",
		"profile/lib/deep/a/b/c/d/e/f/", "profile/lib/deep/a/b/c/d/e/f/g.ts", "profile/lib/util.ts",
		"profile/main.ts", "profile/profile.json"}
	if !slices.Equal(names, want) {
		t.Fatal(names)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest != (Manifest{Type: ArchiveType, Format: ArchiveFormat, Name: "画画", Padmate: "v1.2.3"}) {
		t.Fatalf("%+v", a.Manifest)
	}
	for _, p := range a.Paths() {
		if strings.Contains(p, ".history") || strings.Contains(p, ".padmate") {
			t.Fatal("exported", p)
		}
	}
	l := New(dst)
	imported, err := l.Import(a, ImportOptions{})
	if err != nil || imported.ID != "profile" || imported.Name != "画画" || imported.Revision != Revision([]byte(validProfile)) || imported.HistoryErr != nil {
		t.Fatalf("%+v %v", imported, err)
	}
	files["profile.json"] = validProfile
	for name, want := range files {
		data, err := os.ReadFile(filepath.Join(dst, "profiles", "profile", filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Fatal(name, string(data), err)
		}
	}
	for _, missing := range []string{".history", "macros"} {
		if _, err := os.Lstat(filepath.Join(dst, "profiles", "profile", filepath.FromSlash(missing))); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal(missing, err)
		}
	}
	if info, err := os.Stat(filepath.Join(dst, "profiles", "profile", "empty")); err != nil || !info.IsDir() {
		t.Fatal("empty folder not imported", err)
	}
	assertNoCopyLeftovers(t, dst, "profile")
}

func TestExportRefusals(t *testing.T) {
	dir := t.TempDir()
	l := New(dir)
	if _, err := l.Export("missing", "v"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err := l.Export("../paint", "v"); err == nil {
		t.Fatal("exported an invalid id")
	}
	fixture(t, dir, "broken", `{"schemaVersion":0}`)
	if _, err := l.Export("broken", "v"); err == nil || errors.As(err, new(*UnsupportedError)) {
		t.Fatal("exported an invalid profile", err)
	}
	fixture(t, dir, "latin1", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "latin1", "main.ts"), "const a = \"\xe9\";")
	var unsupported *UnsupportedError
	if _, err := l.Export("latin1", "v"); !errors.As(err, &unsupported) || unsupported.Path != "latin1/main.ts" {
		t.Fatal(err)
	}
	// Random bytes do not compress below the archive limit.
	fixture(t, dir, "big", validProfile)
	noise := make([]byte, MaxArchiveBytes+1)
	_, _ = rand.Read(noise)
	writeFile(t, filepath.Join(dir, "profiles", "big", "noise.txt"), string(noise))
	if _, err := l.Export("big", "v"); !errors.As(err, &unsupported) || unsupported.Path != "big" {
		t.Fatal(err)
	}
	// Only scripts, JSON and text, and no name an import would refuse.
	fixture(t, dir, "program", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "program", "lib", "run.exe"), "MZ")
	if _, err := l.Export("program", "v"); !errors.As(err, &unsupported) || unsupported.Path != "program/lib/run.exe" {
		t.Fatal(err)
	}
	fixture(t, dir, "short", validProfile)
	writeFile(t, filepath.Join(dir, "profiles", "short", "abcdef~1.ts"), "")
	if _, err := l.Export("short", "v"); !errors.As(err, &unsupported) || unsupported.Path != "short/abcdef~1.ts" {
		t.Fatal(err)
	}
	var invalid *profile.Error
	for _, name := range []string{" ", strings.Repeat("名", MaxNameRunes+1)} {
		fixture(t, dir, "badname", profileNamed(name))
		if _, err := l.Export("badname", "v"); !errors.As(err, &invalid) || invalid.Diagnostics[0].Pointer != "/name" {
			t.Fatalf("%q: %v", name, err)
		}
	}
	if runtime.GOOS != "windows" {
		fixture(t, dir, "colon", validProfile)
		writeFile(t, filepath.Join(dir, "profiles", "colon", "a:b.ts"), "")
		if _, err := l.Export("colon", "v"); !errors.As(err, &unsupported) || unsupported.Path != "colon/a:b.ts" {
			t.Fatal(err)
		}
	}
}

func FuzzReadArchive(f *testing.F) {
	f.Add(makeZip(f, validEntries()...))
	f.Add(makeZip(f, append(validEntries(), zipEntry{name: "profile/../x"}, zipEntry{name: "profile/l", mode: fs.ModeSymlink | 0o777})...))
	f.Add(makeZip(f, zipEntry{name: ArchiveManifest, data: testManifest}, zipEntry{name: "profile/profile.json", data: validProfile, method: zip.Store}))
	f.Add([]byte("PK\x05\x06" + strings.Repeat("\x00", 18)))
	f.Fuzz(func(t *testing.T, data []byte) {
		a, err := ReadArchive(data)
		if err != nil {
			var archiveErr *ArchiveError
			if !errors.As(err, &archiveErr) || archiveErr.Reason == "" {
				t.Fatalf("not an ArchiveError: %v", err)
			}
			return
		}
		if a.Profile == nil || a.Manifest.Type != ArchiveType || a.Manifest.Format != ArchiveFormat || len(a.items) > MaxCopyEntries {
			t.Fatalf("%+v", a)
		}
		var total int
		seen := map[string]bool{}
		hasProfile := false
		for _, item := range a.items {
			if problem := archivePathProblem(item.path); problem != "" || skippedOnImport(item.path) || seen[strings.ToLower(item.path)] {
				t.Fatalf("accepted %q: %s", item.path, problem)
			}
			seen[strings.ToLower(item.path)] = true
			if parent := filepath.ToSlash(filepath.Dir(item.path)); parent != "." && !seen[strings.ToLower(parent)] {
				t.Fatalf("%q before its folder", item.path)
			}
			total += len(item.data)
			hasProfile = hasProfile || item.path == "profile.json" && !item.dir
		}
		if total > MaxCopyBytes || !hasProfile {
			t.Fatal(total, hasProfile)
		}
		// What ReadArchive accepts, Import writes and Export writes again
		// (when stored data cannot make the new archive too large).
		dir := t.TempDir()
		imported, err := New(dir).Import(a, ImportOptions{})
		// Import refuses a name a client could not choose, and an item the
		// file system has under another name (an 8.3 short name).
		var archiveErr *ArchiveError
		if errors.As(err, &archiveErr) && (archiveErr.Reason == ArchiveProfileReason || archiveErr.Reason == ArchiveEntryReason) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		if _, err := New(dir).Export(imported.ID, "fuzz"); err != nil && len(data) < MaxArchiveBytes/2 {
			t.Fatal(err)
		}
	})
}

// testArchive exports a library profile with id and data from a fresh
// library for an import test.
func testArchive(t *testing.T, data string, files map[string]string) *Archive {
	t.Helper()
	dir := t.TempDir()
	fixture(t, dir, "src", data)
	for name, text := range files {
		writeFile(t, filepath.Join(dir, "profiles", "src", filepath.FromSlash(name)), text)
	}
	exported, err := New(dir).Export("src", "test")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func profileNamed(name string) string {
	data, _ := json.Marshal(name)
	return `{"schemaVersion":0,"name":` + string(data) + `,"layers":{"base":{}}}`
}

// A profile's macros (SCR-50) travel in its archive: the library's
// macros that it runs (SCR-51), with Chinese and spaced names, go into the
// archive's macros folder, and an import puts them into its library. The
// library's other macros, the modules no macro imports (TestExportMacroModules
// has the ones that travel) and generated declarations stay behind.
func TestExportImportMacros(t *testing.T) {
	// The folder dtsgen.MacroProject writes, below the macros folder.
	if GeneratedMacroDir != "macros/.padmate" {
		t.Fatal("generated macro folder", GeneratedMacroDir)
	}
	src, dst := t.TempDir(), t.TempDir()
	const text = `{"schemaVersion":0,"name":"画画","layers":{"base":{
  "south":{"tap":{"type":"macro","file":"复制全部.ts"}},
  "east":{"tap":{"type":"macro","file":"不存在.ts"}},
  "leftshoulder":{"hold":{"type":"macro","file":"按住 Shift.js"}}}}}`
	fixture(t, src, "paint", text)
	l := New(src)
	types := []MacroFile{
		{Name: "tsconfig.json", Data: []byte("{}\n"), Keep: true},
		{Name: profile.MacroTypesDir + "/padmate.d.ts", Data: []byte("export declare const key: unknown;\n")},
	}
	macros := map[string]string{
		"复制全部.ts":     "import { key } from \"padmate\";\nexport default async () => {};\n",
		"按住 Shift.js": "export default function () {}\n",
	}
	if err := l.CreateMacro("复制全部.ts", []byte(macros["复制全部.ts"]), types); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.ImportMacro("按住 Shift.js", []byte(macros["按住 Shift.js"]), false, types); err != nil {
		t.Fatal(err)
	}
	putMacroFiles(t, src, map[string]string{"没用.ts": "export default () => {};\n", "lib/工具.ts": "export const x = 1;\n"})
	exported, err := l.Export("paint", "v")
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(exported.Data), int64(len(exported.Data)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		// Non-ASCII names are marked as UTF-8 for other zip tools.
		if strings.ContainsFunc(f.Name, func(r rune) bool { return r > 0x7f }) && f.Flags&0x800 == 0 {
			t.Fatal("name not marked UTF-8:", f.Name)
		}
	}
	want := []string{ArchiveManifest, "profile/profile.json", "profile/macros/", "profile/macros/复制全部.ts", "profile/macros/按住 Shift.js"}
	if !slices.Equal(names, want) {
		t.Fatal(names)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := New(dst).Import(a, ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := macroNames(t, New(dst)), []string{"复制全部.ts", "按住 Shift.js"}; !slices.Equal(got, want) {
		t.Fatal(got)
	}
	if len(imported.Macros) != 2 || imported.Macros[0] != (ImportedMacro{File: "复制全部.ts", Name: "复制全部.ts"}) {
		t.Fatalf("%+v", imported.Macros)
	}
	for name, data := range macros {
		if got := readMacro(t, dst, name); got != data {
			t.Fatal(name, got)
		}
	}
	if data, err := New(dst).Read(imported.ID); err != nil || string(data) != text {
		t.Fatal(string(data), err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "profiles", imported.ID, profile.MacroDir)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("macros imported into the profile folder", err)
	}
	// An archive from elsewhere that carries them: skipped, in any case.
	entries := append(validEntries(),
		zipEntry{name: "profile/macros/复制全部.ts", data: macros["复制全部.ts"]},
		zipEntry{name: "profile/macros/.padmate/padmate.d.ts", data: "declare const x: 1;\n"},
		zipEntry{name: "profile/Macros/.PADMATE/math.d.ts", data: "declare const y: 1;\n"},
	)
	a, err = ReadArchive(makeZip(t, entries...))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range a.Paths() {
		if strings.Contains(strings.ToLower(p), ".padmate") {
			t.Fatal("kept", p)
		}
	}
	if !slices.Contains(a.Paths(), "macros/复制全部.ts") {
		t.Fatal(a.Paths())
	}
}

// A macro's imports travel with it (SCR-51): the modules it imports from
// the library, and the ones they import in turn, below the archive's
// macros folder; a sibling macro it imports too. What no macro imports,
// and what an import cannot reach (outside the macros folder, or a file
// that is not there), stays behind. An import puts the modules into the
// library where it has none.
func TestExportMacroModules(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	const text = `{"schemaVersion":0,"name":"画画","layers":{"base":{
  "south":{"tap":{"type":"macro","file":"a.ts"}},
  "east":{"tap":{"type":"macro","file":"plain.ts"}}}}}`
	fixture(t, src, "paint", text)
	putMacroFiles(t, src, map[string]string{
		"a.ts":             "import { u } from \"./lib/util\";\nimport b from './b.js';\nimport \"../outside\";\nimport x from \"./missing\";\nexport default async () => { await import(\"./lib/dyn\"); };\n",
		"b.ts":             "export default 1;\n",
		"plain.ts":         "export default () => {};\n",
		"unused.ts":        "export default () => {};\n",
		"lib/util.ts":      "import { deep } from \"./deep/x\";\nexport const u = deep;\nexport * from \"../data.json\";\n",
		"lib/deep/x.ts":    "import { u } from \"../util\";\nexport const deep = 1;\nexport const back = u;\n",
		"lib/dyn/index.ts": "export default 2;\n",
		"data.json":        "{}\n",
		"lib/unused.ts":    "export const nobody = 1;\n",
		"tsconfig.json":    "{}\n",
	})
	exported, err := New(src).Export("paint", "v")
	if err != nil {
		t.Fatal(err)
	}
	a, err := ReadArchive(exported.Data)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, p := range a.Paths() {
		if strings.HasPrefix(p, "macros/") {
			got = append(got, p)
		}
	}
	want := []string{"macros/", "macros/a.ts", "macros/b.ts", "macros/data.json", "macros/lib/", "macros/lib/deep/", "macros/lib/deep/x.ts", "macros/lib/dyn/", "macros/lib/dyn/index.ts", "macros/lib/util.ts", "macros/plain.ts"}
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("archive holds %q, want %q", got, want)
	}
	if _, err := New(dst).Import(a, ImportOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"lib/util.ts", "lib/deep/x.ts", "lib/dyn/index.ts", "data.json", "b.ts"} {
		if readMacro(t, dst, name) != readMacro(t, src, name) {
			t.Error("not imported:", name)
		}
	}
	for _, name := range []string{"unused.ts", "lib/unused.ts", "tsconfig.json"} {
		if _, err := os.Lstat(filepath.Join(dst, profile.MacroDir, filepath.FromSlash(name))); !errors.Is(err, fs.ErrNotExist) {
			t.Error("imported:", name, err)
		}
	}
}

// A module the archive cannot hold stops the export with its name, rather
// than leaving the macro to fail where the archive is imported.
func TestExportMacroModuleRefused(t *testing.T) {
	src := t.TempDir()
	fixture(t, src, "paint", `{"schemaVersion":0,"name":"p","layers":{"base":{"south":{"tap":{"type":"macro","file":"a.ts"}}}}}`)
	putMacroFiles(t, src, map[string]string{
		"a.ts":            "import \"./lib/abcdef~1\";\n",
		"lib/abcdef~1.ts": "export {};\n",
	})
	var unsupported *UnsupportedError
	if _, err := New(src).Export("paint", "v"); !errors.As(err, &unsupported) || unsupported.Path != "macros/lib/abcdef~1.ts" {
		t.Fatal(err)
	}
}

// relativeImports and resolveImport: what an import names.
func TestResolveImports(t *testing.T) {
	src := "import a from \"./a\"; import {b} from '../b.js';\nexport * from `./c`;\nconst d = require(\"./d\");\nimport(\"./e\");\nimport \"./f\";\n" +
		"import g from \"pkg\"; import h from \"/abs\"; const i = `./${x}`; import a2 from \"./a\";\n"
	if got, want := relativeImports([]byte(src)), []string{"./a", "../b.js", "./c", "./d", "./e", "./f"}; !slices.Equal(got, want) {
		t.Fatalf("%q, want %q", got, want)
	}
	files := map[string]bool{"lib/x.ts": true, "lib/y.tsx": true, "lib/z/index.js": true, "lib/w.json": true, "lib/v.ts": true}
	isFile := func(p string) bool { return files[p] }
	for _, tc := range []struct{ from, spec, want string }{
		{"a.ts", "./lib/x", "lib/x.ts"},
		{"a.ts", "./lib/x.ts", "lib/x.ts"},
		{"a.ts", "./lib/y", "lib/y.tsx"},
		{"a.ts", "./lib/z", "lib/z/index.js"},
		{"a.ts", "./lib/w.json", "lib/w.json"},
		{"a.ts", "./lib/v.js", "lib/v.ts"},
		{"lib/x.ts", "./y", "lib/y.tsx"},
		{"lib/z/index.js", "../x", "lib/x.ts"},
		{"a.ts", "./lib/none", ""},
		{"a.ts", "../lib/x", ""},
		{"lib/x.ts", "../../x", ""},
		{"a.ts", "./", ""},
	} {
		got, ok := resolveImport(tc.from, tc.spec, isFile)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("resolveImport(%q, %q) = %q, %v; want %q", tc.from, tc.spec, got, ok, tc.want)
		}
	}
}
