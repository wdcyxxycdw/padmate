package autostart

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestLongStartupRoundTripAndNoReadWrites(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "not-created")
	exe := filepath.Join(t.TempDir(), "padmated.exe")
	args := []string{"--profile", filepath.Join(t.TempDir(), strings.Repeat("p", 190)+".json"), "--dry-run", "--log-file", filepath.Join(t.TempDir(), "画画 😀 log.jsonl"), "--hint", "SDL_TEST=" + strings.Repeat("x", 100), "--watch=false", "--device", "引号\"\\"}
	s := &memoryStore{}
	e, err := newEntry(exe, args, dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Enabled(); err != nil {
		t.Fatal(err)
	}
	if s.writes != 0 {
		t.Fatal("New or Enabled wrote registry")
	}
	if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("read/New wrote startup directory")
	}
	if err = e.Set(true); err != nil {
		t.Fatal(err)
	}
	if commandUnits(e.command) > 260 {
		t.Fatal(e.command)
	}
	id := strings.Split(e.command, "--startup-id=")[1]
	got, err := expandAt([]string{"--startup-id=" + id}, exe, dir)
	if err != nil || !reflect.DeepEqual(got, args) {
		t.Fatalf("args %q err %v", got, err)
	}
	if err = e.Set(false); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Join(dir, id+".json")); err != nil {
		t.Fatal("disable deleted immutable args")
	}
}

func TestStartupUTF16Boundary(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "p.exe")
	for _, extra := range []int{0, 1} {
		// Emoji occupies two UTF16 code units.
		padding := 260 - commandUnits(exe+" ") - 2 + extra
		e, err := newEntry(exe, []string{strings.Repeat("x", padding) + "😀"}, t.TempDir(), &memoryStore{})
		if err != nil {
			t.Fatal(err)
		}
		if err = e.Set(true); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(e.command, "--startup-id=") != (extra == 1) {
			t.Fatal("wrong UTF16 boundary", e.command)
		}
	}
}

func TestRegistrationFailureKeepsOldReference(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "p.exe")
	s := &memoryStore{}
	old, err := newEntry(exe, []string{strings.Repeat("a", 364)}, dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if err = old.Set(true); err != nil {
		t.Fatal(err)
	}
	value := s.value
	s.err = errors.New("registry denied")
	next, err := newEntry(exe, []string{strings.Repeat("b", 364)}, dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if next.Set(true) == nil || s.value != value {
		t.Fatal("old value changed")
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 1 {
		t.Fatalf("old/new files %v %v", files, err)
	}
	if old.Set(true) == nil {
		t.Fatal("expected registration failure")
	}
	files, _ = os.ReadDir(dir)
	if len(files) != 1 {
		t.Fatal("existing reference deleted on failure")
	}
}

func TestStartupRejectsBadReferences(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "p.exe")
	for _, args := range [][]string{{"--startup-id=../x"}, {"--startup-id=" + strings.Repeat("a", 64)}, {"--startup-id=" + strings.Repeat("0", 64), "--inject"}, {"-startup-id=xyz"}} {
		if _, err := expandAt(args, exe, dir); err == nil {
			t.Fatal(args)
		}
	}
	for _, doc := range []argumentFile{{Version: 2, Executable: exe, Args: []string{"--dry-run"}}, {Version: 1, Executable: exe, Args: []string{"--startup-id=" + strings.Repeat("0", 64)}}, {Version: 1, Executable: "relative.exe", Args: []string{"--dry-run"}}} {
		data, id, err := encodeArguments(doc)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, id+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err = expandAt([]string{"--startup-id=" + id}, exe, dir); err == nil {
			t.Fatal(doc)
		}
	}
	id := strings.Repeat("a", 64)
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := expandAt([]string{"--startup-id=" + id}, exe, dir); err == nil {
		t.Fatal("corrupt file accepted")
	}
}

func TestTooLongExecutableDoesNotTouchStore(t *testing.T) {
	s := &memoryStore{value: "old"}
	dir := filepath.Join(t.TempDir(), "untouched")
	e, err := newEntry(filepath.Join(t.TempDir(), strings.Repeat("e", 250)+".exe"), []string{strings.Repeat("a", 364)}, dir, s)
	if err != nil {
		t.Fatal(err)
	}
	if e.Set(true) == nil || s.value != "old" {
		t.Fatal("too-long reference changed Run")
	}
	if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("too-long reference wrote file")
	}
}

func TestObserved364CharacterCommand(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "padmated.exe")
	args := []string{strings.Repeat("p", 364-commandUnits(exe)-1)}
	command, err := Command(exe, args)
	if err != nil || commandUnits(command) != 364 {
		t.Fatal(command, err)
	}
	s := &memoryStore{}
	e, err := newEntry(exe, args, t.TempDir(), s)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.Set(true); err != nil || commandUnits(s.value) > 260 || !strings.Contains(s.value, referenceFlag) {
		t.Fatal(s.value, err)
	}
}

func FuzzReferenceID(f *testing.F) {
	f.Add(referenceFlag + strings.Repeat("a", 64))
	f.Add(referenceFlag + "../bad")
	f.Add("")
	f.Fuzz(func(t *testing.T, arg string) {
		id, err := referenceID([]string{arg})
		if err == nil && (len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "") {
			t.Fatal(id)
		}
	})
}

func TestMalformedArgumentDocument(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "p.exe")
	for _, data := range [][]byte{[]byte("{broken"), []byte(`{"version":1,"executable":"p.exe","args":[],"unknown":true}`), []byte(`{} {}`), []byte(strings.Repeat("x", maxArgumentBytes+1))} {
		sum := sha256.Sum256(data)
		id := hex.EncodeToString(sum[:])
		if err := os.WriteFile(filepath.Join(dir, id+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := expandAt([]string{referenceFlag + id}, exe, dir); err == nil {
			t.Fatal("invalid document accepted")
		}
	}
}

func TestUnreadableDirectoryKeepsRun(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file-not-directory")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &memoryStore{value: "old"}
	e, err := newEntry(filepath.Join(dir, "p.exe"), []string{strings.Repeat("a", 364)}, path, s)
	if err != nil {
		t.Fatal(err)
	}
	if e.Set(true) == nil || s.value != "old" || s.writes != 0 {
		t.Fatal("storage failure changed Run")
	}
}
