package pecheck

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/buildtool"
	"github.com/wdcyxxycdw/padmate/daemon/internal/exeresources"
)

var (
	tinyDir      string
	tinyOnce     sync.Once
	tinyData     []byte
	tinyErr      error
	strippedOnce sync.Once
	strippedData []byte
	strippedErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if tinyDir != "" {
		_ = os.RemoveAll(tinyDir)
	}
	os.Exit(code)
}

// crossBuild builds testdata/tiny for Windows x64 with the given linker flags
// and gives it the icon and version resource padmate gives its own executables
// (exeresources.Write).
func crossBuild(name string, ldflags string) ([]byte, error) {
	dir, err := os.MkdirTemp(tinyDir, name)
	if err != nil {
		return nil, err
	}
	raw := filepath.Join(dir, "tiny.raw")
	build := exec.Command("go", "build", "-trimpath", "-ldflags", ldflags, "-o", raw, "./internal/pecheck/testdata/tiny")
	build.Dir = filepath.Join("..", "..")
	build.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0")
	buildtool.HideWindow(build)
	if out, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("cross-build: %w\n%s", err, out)
	}
	src, err := os.Open(raw)
	if err != nil {
		return nil, err
	}
	defer func() { _ = src.Close() }()
	final := filepath.Join(dir, "tiny.exe")
	dst, err := os.Create(final)
	if err != nil {
		return nil, err
	}
	err = exeresources.Write(dst, src, "tiny.exe", "v0.0.1")
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(final)
}

// tinyExe returns a copy of a small Windows x64 executable that was
// cross-built the way padmate's are. Every test mutates its own copy.
func tinyExe(t *testing.T) []byte {
	t.Helper()
	tinyOnce.Do(func() {
		var err error
		if tinyDir, err = os.MkdirTemp("", "pecheck-"); err != nil {
			tinyErr = err
			return
		}
		tinyData, tinyErr = crossBuild("full", "")
	})
	if tinyErr != nil {
		t.Fatal(tinyErr)
	}
	return slices.Clone(tinyData)
}

// strippedExe is the same program built with -s -w: no symbol table, no DWARF.
func strippedExe(t *testing.T) []byte {
	t.Helper()
	tinyExe(t)
	strippedOnce.Do(func() { strippedData, strippedErr = crossBuild("stripped", "-s -w") })
	if strippedErr != nil {
		t.Fatal(strippedErr)
	}
	return slices.Clone(strippedData)
}

func inspectBytes(t *testing.T, data []byte, level Level) Report {
	t.Helper()
	rep, err := Inspect(bytes.NewReader(data), level)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

func ownGolden(rep Report) Golden { return Golden{Imports: rep.ImportLines(), Procs: rep.ProcLines()} }

func TestInspectReadsACrossBuiltProgram(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	if !rep.PE32Plus || rep.Machine != 0x8664 {
		t.Errorf("machine %#x, PE32+ %v", rep.Machine, rep.PE32Plus)
	}
	if rep.EntrySection != ".text" {
		t.Errorf("entry section %q", rep.EntrySection)
	}
	if !rep.HasSymtab || rep.Symbols == 0 {
		t.Errorf("symbols %d, .symtab %v", rep.Symbols, rep.HasSymtab)
	}
	if got := rep.Imports["kernel32.dll"]; !slices.Contains(got, "GetProcAddress") || !slices.IsSorted(got) {
		t.Errorf("kernel32.dll imports %v", got)
	}
	for dll := range rep.Imports {
		if dll != "kernel32.dll" {
			t.Errorf("the import table names %s", dll)
		}
	}
	// SendInput is not imported: Go binds it at run time, and only the
	// variable in the symbol table tells.
	if slices.Contains(rep.Imports["kernel32.dll"], "SendInput") {
		t.Error("SendInput is in the import table")
	}
	for _, want := range []string{"windows.OpenProcess", "main.SendInput"} {
		if !slices.Contains(rep.Procs, want) {
			t.Errorf("%s is not among the bound functions %v", want, rep.Procs)
		}
	}
	for _, p := range rep.Procs {
		if strings.HasPrefix(p, "syscall.") || strings.HasPrefix(p, "internal/syscall") {
			t.Errorf("the standard library's %s is listed", p)
		}
	}
	if !slices.IsSorted(rep.Procs) {
		t.Error("procs are not sorted")
	}
	for _, key := range versionKeys {
		if rep.Version[key] == "" {
			t.Errorf("version string %s is empty (%q)", key, rep.VersionErr)
		}
	}
	for _, s := range rep.Sections {
		if s.Name == ".text" && (s.Entropy < 4 || s.Entropy > EntropyCeiling) {
			t.Errorf(".text entropy %.2f", s.Entropy)
		}
	}
}

func TestCheckPassesAProgramAgainstItsOwnGolden(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	if problems := Check(rep, GoPolicy(ownGolden(rep))); len(problems) != 0 {
		t.Errorf("problems: %v", problems)
	}
	// The shipped files get the version and packer checks only.
	light := inspectBytes(t, tinyExe(t), Light)
	if problems := Check(light, ShippedPolicy()); len(problems) != 0 {
		t.Errorf("shipped policy: %v", problems)
	}
}

// layout finds the pieces of a PE file the tests patch.
type layout struct {
	pe, optional, sections, count int
}

func layoutOf(b []byte) layout {
	pe := int(binary.LittleEndian.Uint32(b[0x3c:]))
	size := int(binary.LittleEndian.Uint16(b[pe+20:]))
	return layout{pe: pe, optional: pe + 24, sections: pe + 24 + size, count: int(binary.LittleEndian.Uint16(b[pe+6:]))}
}

// header returns the offset of the section header called name.
func (l layout) header(t *testing.T, b []byte, name string) int {
	t.Helper()
	for i := range l.count {
		at := l.sections + 40*i
		if string(bytes.TrimRight(b[at:at+8], "\x00")) == name {
			return at
		}
	}
	t.Fatalf("no section %s", name)
	return 0
}

func TestCheckFindsWhatIsWrongWithTheFile(t *testing.T) {
	for _, c := range []struct {
		name   string
		patch  func(t *testing.T, b []byte, l layout)
		expect string
	}{
		{"a packer's section name", func(t *testing.T, b []byte, l layout) {
			copy(b[l.header(t, b, ".rdata"):], "UPX0\x00\x00\x00\x00")
		}, `section "UPX0" is the name of an executable packer`},
		{"a section the Go linker does not write", func(t *testing.T, b []byte, l layout) {
			copy(b[l.header(t, b, ".pdata"):], ".evil\x00\x00\x00")
		}, `section ".evil" is not one the Go linker writes`},
		{"a section that is writable and executable", func(t *testing.T, b []byte, l layout) {
			at := l.header(t, b, ".rdata") + 36
			binary.LittleEndian.PutUint32(b[at:], binary.LittleEndian.Uint32(b[at:])|scnMemExecute|scnMemWrite)
		}, `section ".rdata" is writable and executable`},
		{"random bytes in .rdata", func(t *testing.T, b []byte, l layout) {
			at := l.header(t, b, ".rdata")
			offset := int(binary.LittleEndian.Uint32(b[at+20:]))
			size := int(binary.LittleEndian.Uint32(b[at+16:]))
			rng := rand.New(rand.NewPCG(1, 2))
			for i := offset; i < offset+size; i++ {
				b[i] = byte(rng.Uint32())
			}
		}, `section ".rdata" has entropy`},
		{"an address space layout that is not randomized", func(t *testing.T, b []byte, l layout) {
			at := l.optional + 70
			binary.LittleEndian.PutUint16(b[at:], binary.LittleEndian.Uint16(b[at:])&^dllDynamicBase)
		}, "lacks DYNAMIC_BASE"},
		{"no data execution prevention", func(t *testing.T, b []byte, l layout) {
			at := l.optional + 70
			binary.LittleEndian.PutUint16(b[at:], binary.LittleEndian.Uint16(b[at:])&^dllNXCompat)
		}, "lacks NX_COMPAT"},
		{"no high-entropy address space", func(t *testing.T, b []byte, l layout) {
			at := l.optional + 70
			binary.LittleEndian.PutUint16(b[at:], binary.LittleEndian.Uint16(b[at:])&^dllHighEntropyVA)
		}, "lacks HIGH_ENTROPY_VA"},
		{"an entry point outside .text", func(t *testing.T, b []byte, l layout) {
			rdata := l.header(t, b, ".rdata")
			binary.LittleEndian.PutUint32(b[l.optional+16:], binary.LittleEndian.Uint32(b[rdata+12:])+16)
		}, `is in section ".rdata", want .text`},
		{"another machine", func(t *testing.T, b []byte, l layout) {
			binary.LittleEndian.PutUint16(b[l.pe+4:], 0xAA64)
		}, "AMD64"},
		{"no symbol table section", func(t *testing.T, b []byte, l layout) {
			copy(b[l.header(t, b, ".symtab"):], ".other\x00\x00")
		}, "no .symtab section"},
	} {
		t.Run(c.name, func(t *testing.T) {
			data := tinyExe(t)
			c.patch(t, data, layoutOf(data))
			rep := inspectBytes(t, data, Full)
			// The golden is the file's own, so that only c's problem is left.
			problems := Check(rep, GoPolicy(ownGolden(rep)))
			if len(problems) == 0 || !strings.Contains(strings.Join(problems, "\n"), c.expect) {
				t.Errorf("problems %q, want one with %q", problems, c.expect)
			}
		})
	}
}

// A packer is found by the name of a section, not by the bytes "UPX!": the
// compressed debug information of a Go program has them by chance.
func TestPackerIsFoundByNameNotByBytes(t *testing.T) {
	data := tinyExe(t)
	at := bytes.Index(data, []byte(".rsrc"))
	_ = at
	l := layoutOf(data)
	h := l.header(t, data, ".rdata")
	offset := int(binary.LittleEndian.Uint32(data[h+20:]))
	copy(data[offset:], "UPX! UPX0 UPX1 MPRESS1 .themida")
	rep := inspectBytes(t, data, Full)
	for _, p := range Check(rep, GoPolicy(ownGolden(rep))) {
		if strings.Contains(p, "packer") {
			t.Errorf("a file with packer names in its data was reported: %s", p)
		}
	}
}

func TestPackerSectionNames(t *testing.T) {
	for _, name := range []string{"UPX0", "UPX1", ".UPX0", "UPX!", ".aspack", ".adata", ".themida", ".winlice", ".vmp0", ".vmp12", ".enigma1", ".MPRESS1", "MPRESS2", ".petite", ".nsp0", "PEC2", "PECompact2", ".packed", "FSG!"} {
		if !packerSection.MatchString(name) {
			t.Errorf("%q is a packer's section name", name)
		}
	}
	// The sections Go, Electron and NSIS write.
	for _, name := range []string{".text", ".rdata", ".data", ".pdata", ".xdata", ".idata", ".reloc", ".symtab", ".rsrc", ".zdebug_info", ".debug_gdb_scripts", ".ndata", ".tls", ".CRT", ".00cfg", ".gxfg", ".retplne", ".bss", ".edata", ".didat"} {
		if packerSection.MatchString(name) {
			t.Errorf("%q is not a packer's section name", name)
		}
	}
}

func TestCheckFindsAFileWithoutAVersionResource(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	rep.Version, rep.VersionErr = nil, "no version resource"
	if p := strings.Join(Check(rep, ShippedPolicy()), "\n"); !strings.Contains(p, "version resource: no version resource") {
		t.Errorf("a file without a version resource passed: %q", p)
	}
	rep.Version = map[string]string{"CompanyName": "padmate contributors", "ProductName": "padmate", "FileDescription": " ", "LegalCopyright": ""}
	p := strings.Join(Check(rep, ShippedPolicy()), "\n")
	for _, want := range []string{"FileDescription is empty", "LegalCopyright is empty"} {
		if !strings.Contains(p, want) {
			t.Errorf("%q is not reported in %q", want, p)
		}
	}
	if strings.Contains(p, "CompanyName") || strings.Contains(p, "ProductName") {
		t.Errorf("strings that are set were reported: %q", p)
	}
}

func TestCheckComparesTheListsWithTheGoldens(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	golden := ownGolden(rep)

	more := rep
	more.Procs = append(slices.Clone(rep.Procs), "internal/winhook.SetWindowsHookExW")
	more.Imports = map[string][]string{"kernel32.dll": append(slices.Clone(rep.Imports["kernel32.dll"]), "CreateFileA"), "ws2_32.dll": {"WSAStartup"}}
	p := strings.Join(Check(more, GoPolicy(golden)), "\n")
	for _, want := range []string{
		`new Windows function (proc variable) "internal/winhook.SetWindowsHookExW" is not in the golden`,
		`new import "kernel32.dll CreateFileA" is not in the golden`,
		`new import "ws2_32.dll WSAStartup" is not in the golden`,
	} {
		if !strings.Contains(p, want) {
			t.Errorf("%q is not in %q", want, p)
		}
	}

	fewer := rep
	fewer.Procs = rep.Procs[1:]
	if p := strings.Join(Check(fewer, GoPolicy(golden)), "\n"); !strings.Contains(p, "is in the golden but no longer in the file") {
		t.Errorf("a function that went away was not reported: %q", p)
	}
}

// An executable of another build than the goldens' (the earlier release of an
// upgrade test) may bind other functions: only the comparison with the lists is
// off, everything else about the file is still held to the policy.
func TestNoGoldenSkipsOnlyTheComparison(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	policy := GoPolicy(Golden{Imports: []string{"kernel32.dll Nothing"}, Procs: []string{"main.Nothing"}})
	if p := strings.Join(Check(rep, policy), "\n"); !strings.Contains(p, "is not in the golden") || !strings.Contains(p, "is in the golden but no longer in the file") {
		t.Fatalf("the unrelated golden was not held against the file: %q", p)
	}
	policy.NoGolden = true
	if p := Check(rep, policy); len(p) != 0 {
		t.Errorf("NoGolden still compares with the golden: %v", p)
	}

	// The denylist, the layout and the version resource are checked all the same.
	bad := rep
	bad.Procs = append(slices.Clone(rep.Procs), "windows.WriteProcessMemory")
	bad.Version = map[string]string{"ProductName": "x"}
	bad.Sections = append(slices.Clone(rep.Sections), Section{Name: ".upx0"})
	p := strings.Join(Check(bad, policy), "\n")
	for _, want := range []string{"windows.WriteProcessMemory is bound, which padmate must never use", "CompanyName is empty", `section ".upx0" is the name of an executable packer`} {
		if !strings.Contains(p, want) {
			t.Errorf("%q is not in %q", want, p)
		}
	}
	if strings.Contains(p, "golden") {
		t.Errorf("NoGolden still mentions the golden: %q", p)
	}
}

func TestCheckDeniesFunctionsWhateverTheGolden(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	for _, name := range Denied {
		bad := rep
		bad.Procs = append(slices.Clone(rep.Procs), "windows."+name)
		bad.Imports = map[string][]string{"kernel32.dll": append(slices.Clone(rep.Imports["kernel32.dll"]), name)}
		// The golden lists it: the denial does not depend on the list.
		problems := strings.Join(Check(bad, GoPolicy(ownGolden(bad))), "\n")
		for _, want := range []string{"windows." + name + " is bound, which padmate must never use", "kernel32.dll imports " + name + ", which padmate must never use"} {
			if !strings.Contains(problems, want) {
				t.Errorf("%s: %q is not in %q", name, want, problems)
			}
		}
	}
	// A function that merely has such a name inside is not denied.
	ok := rep
	ok.Procs = append(slices.Clone(rep.Procs), "windows.OpenProcessToken", "windows.VirtualAlloc")
	if p := Check(ok, GoPolicy(ownGolden(ok))); len(p) != 0 {
		t.Errorf("OpenProcessToken and VirtualAlloc were denied: %v", p)
	}
}

func TestStrictPolicyNeedsAFullReport(t *testing.T) {
	light := inspectBytes(t, tinyExe(t), Light)
	if p := strings.Join(Check(light, GoPolicy(Golden{})), "\n"); !strings.Contains(p, "Light level") {
		t.Errorf("a light report was judged strictly: %q", p)
	}
	if light.Imports != nil || light.Procs != nil || light.Sections[0].Entropy != 0 {
		t.Error("a light report has imports, procs or entropy")
	}
}

func TestInspectRefusesWhatIsNotAPEFile(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("MZ"), bytes.Repeat([]byte{0}, 4096), []byte("#!/bin/sh\necho hi\n")} {
		if _, err := Inspect(bytes.NewReader(data), Full); err == nil {
			t.Errorf("%q was read", data)
		}
	}
}

func TestEntropy(t *testing.T) {
	if got := entropy(nil); got != 0 {
		t.Errorf("entropy of nothing is %v", got)
	}
	if got := entropy(bytes.Repeat([]byte{7}, 1000)); got != 0 {
		t.Errorf("entropy of one byte value is %v", got)
	}
	all := make([]byte, 256*4)
	for i := range all {
		all[i] = byte(i)
	}
	if got := entropy(all); got < 7.999 || got > 8.001 {
		t.Errorf("entropy of every byte value is %v, want 8", got)
	}
}

// The committed goldens are what Go built, so a hand edit is easy to spot: they
// are sorted, have no duplicates, deny nothing, and show where the hook lives.
func TestCommittedGoldens(t *testing.T) {
	for _, name := range Executables {
		g, err := Embedded(name)
		if err != nil {
			t.Fatal(err)
		}
		for what, lines := range map[string][]string{"imports": g.Imports, "procs": g.Procs} {
			if len(lines) == 0 {
				t.Errorf("%s %s golden is empty", name, what)
			}
			if !slices.IsSorted(lines) {
				t.Errorf("%s %s golden is not sorted", name, what)
			}
			if len(slices.Compact(slices.Clone(lines))) != len(lines) {
				t.Errorf("%s %s golden has a duplicate", name, what)
			}
			for _, l := range lines {
				if i := strings.LastIndexAny(l, ". "); i >= 0 && denied(l[i+1:]) {
					t.Errorf("%s %s golden lists %s", name, what, l)
				}
			}
		}
		for _, l := range g.Imports {
			if !strings.HasPrefix(l, "kernel32.dll ") {
				t.Errorf("%s imports %s: Go links kernel32.dll only; another DLL means cgo or a new tool in the build", name, l)
			}
		}
	}
	// The low-level keyboard hook is in the CLI only, for recording a shortcut
	// (docs/known-limitations.md says so); the daemon must not have it.
	daemon, _ := Embedded("padmated")
	for _, l := range daemon.Procs {
		if strings.Contains(l, "SetWindowsHookEx") {
			t.Errorf("padmated binds %s: the keyboard hook belongs to padmatectl record", l)
		}
	}
	ctl, _ := Embedded("padmatectl")
	if !slices.Contains(ctl.Procs, "internal/winhook.SetWindowsHookExW") || !slices.Contains(daemon.Procs, "internal/output.SendInput") {
		t.Error("the goldens do not show the hook in padmatectl and SendInput in padmated")
	}
}

func TestListFormat(t *testing.T) {
	lines := []string{"a b", "c.d"}
	text := FormatList(lines)
	if !strings.HasPrefix(text, "# ") || strings.Contains(text, "\r") {
		t.Errorf("list header or line ends: %q", text)
	}
	if got := ParseList(text); !slices.Equal(got, lines) {
		t.Errorf("round trip %v", got)
	}
	if got := ParseList("\n# comment\n  x  \n\n y\r\n"); !slices.Equal(got, []string{"x", "y"}) {
		t.Errorf("ParseList = %q", got)
	}
}

func TestWriteAndReadGolden(t *testing.T) {
	rep := inspectBytes(t, tinyExe(t), Full)
	dir := t.TempDir()
	if err := WriteGolden(dir, "tiny", rep); err != nil {
		t.Fatal(err)
	}
	g, err := ReadGolden(os.DirFS(dir), "tiny")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(g.Imports, rep.ImportLines()) || !slices.Equal(g.Procs, rep.ProcLines()) {
		t.Error("what was written is not what was read")
	}
	if _, err := ReadGolden(os.DirFS(dir), "missing"); err == nil {
		t.Error("a missing golden was read")
	}
}

// miniPE builds the smallest PE32+ file debug/pe accepts, with sections of the
// given names, for the fuzz test's seed.
func miniPE(names ...string) []byte {
	const headers = 0x200
	b := make([]byte, headers+0x200*len(names))
	copy(b, "MZ")
	binary.LittleEndian.PutUint32(b[0x3c:], 0x40)
	copy(b[0x40:], "PE\x00\x00")
	binary.LittleEndian.PutUint16(b[0x44:], 0x8664)
	binary.LittleEndian.PutUint16(b[0x46:], uint16(len(names)))
	binary.LittleEndian.PutUint16(b[0x54:], 240)
	binary.LittleEndian.PutUint16(b[0x56:], 0x22)
	opt := 0x58
	binary.LittleEndian.PutUint16(b[opt:], 0x20b)
	binary.LittleEndian.PutUint32(b[opt+16:], 0x1000)
	binary.LittleEndian.PutUint32(b[opt+32:], 0x1000)
	binary.LittleEndian.PutUint32(b[opt+36:], 0x200)
	binary.LittleEndian.PutUint32(b[opt+56:], 0x1000+0x1000*uint32(len(names)))
	binary.LittleEndian.PutUint32(b[opt+60:], headers)
	binary.LittleEndian.PutUint16(b[opt+68:], 3)
	binary.LittleEndian.PutUint16(b[opt+70:], dllHighEntropyVA|dllDynamicBase|dllNXCompat)
	binary.LittleEndian.PutUint32(b[opt+108:], 16)
	for i, name := range names {
		at := opt + 240 + 40*i
		copy(b[at:], name)
		binary.LittleEndian.PutUint32(b[at+8:], 0x200)
		binary.LittleEndian.PutUint32(b[at+12:], 0x1000+0x1000*uint32(i))
		binary.LittleEndian.PutUint32(b[at+16:], 0x200)
		binary.LittleEndian.PutUint32(b[at+20:], uint32(headers+0x200*i))
		binary.LittleEndian.PutUint32(b[at+36:], 0x60000020)
	}
	return b
}

func TestMiniPE(t *testing.T) {
	rep := inspectBytes(t, miniPE(".text", ".rdata"), Full)
	if len(rep.Sections) != 2 || rep.EntrySection != ".text" || !rep.PE32Plus {
		t.Errorf("mini PE: %+v", rep)
	}
	// No symbol table, and no version resource.
	p := strings.Join(Check(rep, GoPolicy(Golden{})), "\n")
	for _, want := range []string{"no .symtab section", "version resource"} {
		if !strings.Contains(p, want) {
			t.Errorf("%q is not in %q", want, p)
		}
	}
}

func FuzzInspect(f *testing.F) {
	f.Add(miniPE(".text", ".rdata", ".symtab"))
	f.Add(miniPE())
	f.Add([]byte("MZ"))
	f.Fuzz(func(t *testing.T, data []byte) {
		rep, err := Inspect(bytes.NewReader(data), Full)
		if err != nil {
			return
		}
		// Whatever the file holds, judging it must not fail.
		_ = Check(rep, GoPolicy(Golden{}))
		_ = Check(rep, ShippedPolicy())
	})
}

// A build with -s has no symbol table, so the list of bound functions would be
// empty and every new function would go unseen: that is an error, not a pass.
func TestCheckRefusesAStrippedBuild(t *testing.T) {
	rep := inspectBytes(t, strippedExe(t), Full)
	if rep.Symbols != 0 || len(rep.Procs) != 0 {
		t.Fatalf("stripped build: %d symbols, procs %v", rep.Symbols, rep.Procs)
	}
	p := strings.Join(Check(rep, GoPolicy(ownGolden(rep))), "\n")
	if !strings.Contains(p, "no .symtab section or no COFF symbols") || !strings.Contains(p, "the build must not strip them") {
		t.Errorf("a stripped build passed: %q", p)
	}
}
