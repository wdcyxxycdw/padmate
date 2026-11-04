package cspfile

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

func appendEdit(s string) func(string) error {
	return func(tmp string) error {
		f, err := os.OpenFile(tmp, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		_, err = f.WriteString(s)
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		return err
	}
}

func TestIsCSPImage(t *testing.T) {
	names := map[string]bool{
		"CLIPStudioPaint.exe":  true,
		"clipstudiopaint.EXE":  true,
		"CLIPStudio.exe":       false,
		"CLIPStudioPaint.exe2": false,
		"":                     false,
		"notepad.exe":          false,
	}
	for name, want := range names {
		if got := IsCSPImage(name); got != want {
			t.Errorf("IsCSPImage(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestUpdate(t *testing.T) {
	boom := errors.New("boom")
	tests := []struct {
		name     string
		setup    func(target string)
		edit     func(string) error
		opts     func(target string) Options
		wantErr  error
		wantBody string
	}{
		{name: "success", edit: appendEdit("+new"), wantBody: "orig+new"},
		{
			name:     "journal",
			setup:    func(target string) { writeFile(t, target+"-journal", "x") },
			edit:     appendEdit("+new"),
			wantErr:  ErrJournal,
			wantBody: "orig",
		},
		{
			name:     "wal",
			setup:    func(target string) { writeFile(t, target+"-wal", "x") },
			edit:     appendEdit("+new"),
			wantErr:  ErrJournal,
			wantBody: "orig",
		},
		{name: "edit error", edit: func(tmp string) error { _ = appendEdit("junk")(tmp); return boom }, wantErr: boom, wantBody: "orig"},
		{
			name: "target changed during edit",
			edit: func(tmp string) error {
				_ = appendEdit("+new")(tmp)
				return os.WriteFile(strings.TrimSuffix(tmp, tmpSuffix), []byte("other"), 0o644)
			},
			wantErr:  ErrChanged,
			wantBody: "other",
		},
		{
			name: "recheck fails",
			edit: appendEdit("+new"),
			opts: func(string) Options {
				return Options{Recheck: func() error { return boom }}
			},
			wantErr:  boom,
			wantBody: "orig",
		},
		{
			name:  "stale tmp is replaced",
			setup: func(target string) { writeFile(t, target+tmpSuffix, "stale") },
			edit:  appendEdit("+new"), wantBody: "orig+new",
		},
		{
			name: "verify fails restores original",
			edit: appendEdit("+new"),
			opts: func(string) Options {
				return Options{Verify: func(string) error { return boom }}
			},
			wantErr:  boom,
			wantBody: "orig",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := filepath.Join(t.TempDir(), "default.khc")
			writeFile(t, target, "orig")
			if tt.setup != nil {
				tt.setup(target)
			}
			var opts Options
			if tt.opts != nil {
				opts = tt.opts(target)
			}
			err := Update(target, tt.edit, opts)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got := readFile(t, target); got != tt.wantBody {
				t.Errorf("target = %q, want %q", got, tt.wantBody)
			}
			for _, s := range []string{tmpSuffix, undoSuffix} {
				if exists(target + s) {
					t.Errorf("%s left behind", s)
				}
			}
		})
	}
}

func TestUpdateNonRegularTmp(t *testing.T) {
	target := filepath.Join(t.TempDir(), "a.todb")
	writeFile(t, target, "orig")
	if err := os.Mkdir(target+tmpSuffix, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Update(target, appendEdit("x"), Options{}); err == nil {
		t.Fatal("want error for a folder in the tmp place")
	}
	if got := readFile(t, target); got != "orig" {
		t.Errorf("target = %q", got)
	}
}

func TestUpdateManyRollback(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.khc"), filepath.Join(dir, "b.todb")
	writeFile(t, a, "A")
	writeFile(t, b, "B")
	boom := errors.New("second replace failed")
	n := 0
	opts := Options{replace: func(target, tmp string) error {
		n++
		if n == 2 {
			return boom
		}
		return replaceAtomic(target, tmp)
	}}
	err := UpdateMany([]FileUpdate{
		{Target: a, Edit: appendEdit("+1")},
		{Target: b, Edit: appendEdit("+2")},
	}, opts)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if got := readFile(t, a); got != "A" {
		t.Errorf("a = %q, want original", got)
	}
	if got := readFile(t, b); got != "B" {
		t.Errorf("b = %q, want original", got)
	}
	for _, p := range []string{a, b} {
		for _, s := range []string{tmpSuffix, undoSuffix} {
			if exists(p + s) {
				t.Errorf("%s%s left behind", p, s)
			}
		}
	}
}

func TestUpdateManyRollbackFails(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, a, "A")
	writeFile(t, b, "B")
	n := 0
	opts := Options{replace: func(target, tmp string) error {
		n++
		if n >= 2 {
			return errors.New("fail")
		}
		return replaceAtomic(target, tmp)
	}}
	err := UpdateMany([]FileUpdate{{Target: a, Edit: appendEdit("+1")}, {Target: b, Edit: appendEdit("+2")}}, opts)
	if err == nil || !strings.Contains(err.Error(), a+undoSuffix) {
		t.Fatalf("err = %v, want it to name the kept original", err)
	}
	if got := readFile(t, a+undoSuffix); got != "A" {
		t.Errorf("kept original = %q", got)
	}
}

func TestUpdateManyAbortsBeforeAnyReplace(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	writeFile(t, a, "A")
	writeFile(t, b, "B")
	boom := errors.New("edit b")
	err := UpdateMany([]FileUpdate{
		{Target: a, Edit: appendEdit("+1")},
		{Target: b, Edit: func(string) error { return boom }},
	}, Options{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if readFile(t, a) != "A" || readFile(t, b) != "B" {
		t.Error("a target changed")
	}
	if exists(a+tmpSuffix) || exists(b+tmpSuffix) {
		t.Error("tmp left behind")
	}
}

func TestUpdateManyCreate(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "new")
	writeFile(t, a, "A")
	err := UpdateMany([]FileUpdate{
		{Target: a, Edit: appendEdit("+1")},
		{Target: b, Create: true, Edit: appendEdit("made")},
	}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, a) != "A+1" || readFile(t, b) != "made" {
		t.Error("wrong content")
	}
}

func fixedClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := times[min(i, len(times)-1)]
		i++
		return t
	}
}

func TestBackupCreateAndList(t *testing.T) {
	src := t.TempDir()
	a, b := filepath.Join(src, "default.khc"), filepath.Join(src, "EditImageTool.todb")
	writeFile(t, a, "khc")
	writeFile(t, b, "todb")
	at := time.Date(2026, 11, 4, 1, 2, 3, 456_000_000, time.UTC)
	bk := &Backups{Root: filepath.Join(t.TempDir(), "backups"), Now: fixedClock(at)}

	b1, err := bk.Create([]string{a, b}, json.RawMessage(`{"why":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if b1.ID != "20261104T010203.456Z" {
		t.Errorf("id = %q", b1.ID)
	}
	b2, err := bk.Create([]string{a}, nil) // same millisecond
	if err != nil {
		t.Fatal(err)
	}
	b3, err := bk.Create([]string{a}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b2.ID != b1.ID+"-2" || b3.ID != b1.ID+"-3" {
		t.Errorf("ids = %q %q", b2.ID, b3.ID)
	}
	if got := readFile(t, b1.FilePath("EditImageTool.todb")); got != "todb" {
		t.Errorf("copy = %q", got)
	}
	m := b1.Manifest
	if m.Format != 1 || len(m.Files) != 2 || m.Files[0].Source != a || m.Files[0].Size != 3 || m.Files[0].SHA256 == "" ||
		string(m.Note) != `{"why":"x"}` || !m.Created.Equal(at) {
		t.Errorf("manifest = %+v", m)
	}

	// An unfinished folder (no manifest) and a stranger are ignored.
	writeFile(t, filepath.Join(bk.Root, "20261105T000000.000Z", "default.khc"), "partial")
	writeFile(t, filepath.Join(bk.Root, "notes", "x"), "x")
	list, err := bk.List()
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, l := range list {
		ids = append(ids, l.ID)
	}
	if strings.Join(ids, ",") != b3.ID+","+b2.ID+","+b1.ID {
		t.Errorf("list = %v", ids)
	}
	if _, err := bk.Get("20261105T000000.000Z"); err == nil {
		t.Error("Get of an unfinished backup succeeded")
	}
	if got, err := bk.Get(b2.ID); err != nil || got.ID != b2.ID {
		t.Errorf("Get = %v, %v", got.ID, err)
	}
}

func TestBackupErrors(t *testing.T) {
	src := t.TempDir()
	a := filepath.Join(src, "a")
	writeFile(t, a, "a")
	bk := NewBackups(filepath.Join(t.TempDir(), "b"))
	if _, err := bk.Create(nil, nil); err == nil {
		t.Error("no files accepted")
	}
	if _, err := bk.Create([]string{a}, json.RawMessage(`{bad`)); err == nil {
		t.Error("bad note accepted")
	}
	if _, err := bk.Create([]string{a, filepath.Join(src, "missing")}, nil); err == nil {
		t.Error("missing file accepted")
	}
	list, _ := bk.List()
	if len(list) != 0 {
		t.Errorf("failed backups listed: %v", list)
	}
	entries, _ := os.ReadDir(bk.Root)
	if len(entries) != 0 {
		t.Errorf("failed backup left %d folders", len(entries))
	}
}

func TestBackupPrune(t *testing.T) {
	src := t.TempDir()
	a := filepath.Join(src, "a")
	writeFile(t, a, "a")
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var times []time.Time
	for i := range 35 {
		times = append(times, start.Add(time.Duration(i)*time.Second))
	}
	bk := &Backups{Root: t.TempDir(), Now: fixedClock(times...)}
	// An old unfinished folder goes with the pruning.
	old := filepath.Join(bk.Root, "20200101T000000.000Z")
	writeFile(t, filepath.Join(old, "a"), "x")
	var all []Backup
	for range 35 {
		b, err := bk.Create([]string{a}, nil)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, b)
	}
	list, err := bk.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 30 {
		t.Fatalf("kept %d, want 30", len(list))
	}
	if list[0].ID != all[34].ID || list[29].ID != all[5].ID {
		t.Errorf("kept %s .. %s", list[0].ID, list[29].ID)
	}
	if exists(all[0].Dir) || exists(old) {
		t.Error("old folders not deleted")
	}
}

func TestBackupPruneIgnoresLinkTarget(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "keep"), "keep")
	link := filepath.Join(root, "20200101T000000.000Z")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("no symlinks: %v", err)
	}
	bk := &Backups{Root: root}
	if err := bk.removeBackupDir("20200101T000000.000Z"); err != nil {
		t.Fatal(err)
	}
	if exists(link) {
		t.Error("link not removed")
	}
	if readFile(t, filepath.Join(outside, "keep")) != "keep" {
		t.Error("the link's target was touched")
	}
}

func TestRestore(t *testing.T) {
	src := t.TempDir()
	a, b := filepath.Join(src, "default.khc"), filepath.Join(src, "EditImageTool.todb")
	writeFile(t, a, "khc-v1")
	writeFile(t, b, "todb-v1")
	bk := &Backups{Root: t.TempDir(), Now: fixedClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))}
	orig, err := bk.Create([]string{a, b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, a, "khc-v2")
	writeFile(t, b, "todb-v2")

	running := true
	opts := RestoreOptions{Recheck: func() error {
		if running {
			return errors.New("CSP is running")
		}
		return nil
	}}
	if _, err := bk.Restore(orig, opts); err == nil {
		t.Fatal("restore ran with CSP running")
	}
	if readFile(t, a) != "khc-v2" {
		t.Error("target written despite Recheck")
	}
	if l, _ := bk.List(); len(l) != 1 {
		t.Error("a backup was made despite Recheck")
	}

	running = false
	if _, err := bk.Restore(orig, RestoreOptions{}); err == nil {
		t.Error("restore without Recheck accepted")
	}
	pre, err := bk.Restore(orig, opts)
	if err != nil {
		t.Fatal(err)
	}
	if readFile(t, a) != "khc-v1" || readFile(t, b) != "todb-v1" {
		t.Error("bytes not restored")
	}
	if got := readFile(t, pre.FilePath("default.khc")); got != "khc-v2" {
		t.Errorf("pre-restore backup holds %q", got)
	}
	var note map[string]string
	if err := json.Unmarshal(pre.Manifest.Note, &note); err != nil || note["reason"] != "before-restore" || note["from"] != orig.ID {
		t.Errorf("note = %s", pre.Manifest.Note)
	}
}

func TestRestoreRollsBackAndChecksFolder(t *testing.T) {
	src := t.TempDir()
	a, b := filepath.Join(src, "a"), filepath.Join(src, "b")
	writeFile(t, a, "a1")
	writeFile(t, b, "b1")
	bk := &Backups{Root: t.TempDir()}
	orig, err := bk.Create([]string{a, b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, a, "a2")
	writeFile(t, b, "b2")
	ok := func() error { return nil }

	n := 0
	_, err = bk.Restore(orig, RestoreOptions{Recheck: ok, replace: func(target, tmp string) error {
		n++
		if n == 2 {
			return errors.New("fail")
		}
		return replaceAtomic(target, tmp)
	}})
	if err == nil {
		t.Fatal("want error")
	}
	if readFile(t, a) != "a2" || readFile(t, b) != "b2" {
		t.Error("failed restore left files changed")
	}

	// A damaged copy fails before anything is written.
	writeFile(t, orig.FilePath("b"), "tampered")
	if _, err := bk.Restore(orig, RestoreOptions{Recheck: ok}); err == nil {
		t.Error("damaged backup restored")
	}
	if readFile(t, a) != "a2" {
		t.Error("a written")
	}

	// Missing folder: error, nothing written.
	writeFile(t, orig.FilePath("b"), "b1")
	orig.Manifest.Files[0].Source = filepath.Join(src, "gone", "a")
	if _, err := bk.Restore(orig, RestoreOptions{Recheck: ok}); err == nil {
		t.Error("restore into a missing folder succeeded")
	}
	if readFile(t, b) != "b2" {
		t.Error("b written")
	}
}

func mkData(t *testing.T, base string, khc, todb bool, mod time.Time) string {
	t.Helper()
	if khc {
		p := filepath.Join(base, "Shortcut", "default.khc")
		writeFile(t, p, "k")
		if err := os.Chtimes(p, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	if todb {
		writeFile(t, filepath.Join(base, "Tool", "EditImageTool.todb"), "t")
	}
	return base
}

func TestFindDataDirs(t *testing.T) {
	appData, docs, local := t.TempDir(), t.TempDir(), t.TempDir()
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	celsys := filepath.Join(appData, "CELSYSUserData", "CELSYS")
	older := mkData(t, filepath.Join(celsys, "CLIPStudioPaintVer3_0_0"), true, true, t0)
	newer := mkData(t, filepath.Join(celsys, "CLIPStudioPaintVer4_0_0"), true, false, t0.Add(48*time.Hour))
	mkData(t, filepath.Join(celsys, "CLIPStudioPaintVer5_0_0"), false, false, t0) // empty: not a candidate
	toolOnly := mkData(t, filepath.Join(docs, "CELSYS", "CLIPStudioPaintVer1_5_0"), false, true, t0)
	store := mkData(t, filepath.Join(local, "Packages", "CELSYS.ClipStudio_abc", "LocalCache", "Roaming",
		"CELSYSUserData", "CELSYS", "CLIPStudioPaintVer4_1_0"), true, true, t0.Add(24*time.Hour))
	// Other places inside a package are not searched.
	mkData(t, filepath.Join(local, "Packages", "x", "deeper", "LocalCache", "Roaming", "CELSYSUserData", "CELSYS", "CLIPStudioPaintVer9"), true, true, t0)

	got := FindDataDirs(Roots{AppData: appData, Documents: docs, LocalAppData: local})
	var paths []string
	for _, d := range got {
		paths = append(paths, d.Path)
	}
	want := []string{newer, store, older, toolOnly}
	if strings.Join(paths, "|") != strings.Join(want, "|") {
		t.Fatalf("order:\n got %v\nwant %v", paths, want)
	}
	if !got[0].HasShortcut || got[0].HasTool || got[3].HasShortcut || !got[3].HasTool {
		t.Errorf("flags: %+v", got)
	}
	if got[0].Shortcut != filepath.Join(newer, "Shortcut", "default.khc") || got[2].Tool != filepath.Join(older, "Tool", "EditImageTool.todb") {
		t.Errorf("paths: %+v", got)
	}
	if len(FindDataDirs(Roots{})) != 0 {
		t.Error("empty roots found something")
	}
}

func TestFindDataDirsPackageBound(t *testing.T) {
	local := t.TempDir()
	for i := range maxPackageDirs + 20 {
		base := filepath.Join(local, "Packages", fmt.Sprintf("p%04d", i), "LocalCache", "Roaming",
			"CELSYSUserData", "CELSYS", "CLIPStudioPaintVer4")
		writeFile(t, filepath.Join(base, "Tool", "EditImageTool.todb"), "t")
	}
	if got := FindDataDirs(Roots{LocalAppData: local}); len(got) != maxPackageDirs {
		t.Errorf("found %d, want the cap %d", len(got), maxPackageDirs)
	}
}

func TestLocateInstall(t *testing.T) {
	flat := t.TempDir()
	writeFile(t, filepath.Join(flat, ExeName), "x")
	if err := os.Mkdir(filepath.Join(flat, "resource"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, ok := LocateInstall(flat)
	if !ok || in.Root != flat || in.Resource != filepath.Join(flat, "resource") {
		t.Errorf("flat: %+v %v", in, ok)
	}

	nested := t.TempDir()
	sub := filepath.Join(nested, "CLIP STUDIO PAINT")
	writeFile(t, filepath.Join(sub, ExeName), "x")
	in, ok = LocateInstall(nested)
	if !ok || in.Root != sub || in.Exe != filepath.Join(sub, ExeName) || in.Resource != "" {
		t.Errorf("nested: %+v %v", in, ok)
	}

	if _, ok := LocateInstall(t.TempDir()); ok {
		t.Error("empty folder is not an install")
	}
}

func TestIsInstallEntry(t *testing.T) {
	for name, want := range map[string]bool{
		"CLIP STUDIO PAINT":          true,
		"clip studio paint 3.0.0":    true,
		"CLIP STUDIO":                false,
		"CLIP STUDIO PAIN":           false,
		"Other CLIP STUDIO PAINT":    false,
		"":                           false,
		"CLIP STUDIO PAINT EX 4.0.0": true,
	} {
		if got := isInstallEntry(name); got != want {
			t.Errorf("isInstallEntry(%q) = %v", name, got)
		}
	}
}

func TestFingerprint(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	writeFile(t, p, "hello")
	a, err := FingerprintOf(p)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := FingerprintOf(p)
	if !Same(a, b) || a.Size != 5 || len(a.SHA256) != 64 {
		t.Errorf("%+v", a)
	}
	writeFile(t, p, "hellO")
	if err := os.Chtimes(p, a.ModTime, a.ModTime); err != nil {
		t.Fatal(err)
	}
	c, _ := FingerprintOf(p)
	if Same(a, c) {
		t.Error("different content, same fingerprint")
	}
	if _, err := FingerprintOf(filepath.Dir(p)); err == nil {
		t.Error("folder fingerprinted")
	}
}
