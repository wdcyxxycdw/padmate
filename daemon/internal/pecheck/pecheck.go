// Package pecheck is the offline hygiene check of padmate's Windows
// executables (PKG-11, ADR-0073). It reads a PE file with debug/pe, so it
// builds and tests on Linux, and reports what an antivirus engine's heuristics
// and a reviewer would care about: how the file is laid out, what it imports,
// and which Windows functions padmate binds at run time.
//
// A Go program imports almost nothing from Windows: its import table is a
// short list of kernel32.dll functions, and every other function (SendInput,
// SetWindowsHookExW, GetAsyncKeyState) is looked up at run time through a
// package-level "proc<Name>" variable. Those variables are the real inventory,
// and they survive in the COFF symbol table because the build does not strip
// it. The check compares both lists with committed goldens, so a new Windows
// API shows up in a diff instead of in a user's quarantine folder.
//
// The package never executes, signs or uploads anything.
package pecheck

import (
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/tc-hib/winres"
	resversion "github.com/tc-hib/winres/version"
)

// Section header flags and DLL characteristics, spelled out so that the check
// does not depend on which of them debug/pe names.
const (
	scnMemExecute uint32 = 0x20000000
	scnMemWrite   uint32 = 0x80000000

	dllHighEntropyVA uint16 = 0x0020
	dllDynamicBase   uint16 = 0x0040
	dllNXCompat      uint16 = 0x0100
)

// EntropyCeiling is the most Shannon entropy (bits per byte) the code and data
// sections of a Go executable may have. Today's binaries measure 5.5 to 6.2;
// packed or encrypted code is close to 8. The compressed DWARF sections
// (.zdebug_*: 7.9 to 8.0 for the large ones, 5.9 to 7.9 for the small ones) and
// the resources (7.5) are compressed by nature and are not measured.
const EntropyCeiling = 6.8

// entropyChecked are the sections the ceiling applies to.
var entropyChecked = map[string]bool{
	".text": true, ".rdata": true, ".data": true, ".pdata": true, ".reloc": true, ".symtab": true,
}

// goSections is the closed set of section names a padmate executable has: what
// the Go linker writes for a Windows program, with the DWARF sections it
// compresses (.zdebug_*, matched by prefix). A new name means a new tool in the
// build or a packer.
var goSections = map[string]bool{
	".text": true, ".rdata": true, ".data": true, ".pdata": true, ".xdata": true,
	".debug_gdb_scripts": true, ".idata": true, ".reloc": true, ".symtab": true, ".rsrc": true,
}

func goSection(name string) bool { return goSections[name] || strings.HasPrefix(name, ".zdebug_") }

// packerSection matches the section names of well-known executable packers and
// protectors: UPX, ASPack, Themida and WinLicense, VMProtect, Enigma, MPRESS,
// Petite, NsPack, PECompact, FSG. It is matched against whole names, never
// searched for in the file: the bytes "UPX" occur by chance in compressed
// debug information.
var packerSection = regexp.MustCompile(`(?i)^(\.?upx[0-9!]?|\.aspack|\.adata|\.themida|\.winlice|\.vmp[0-9]+|\.enigma[0-9]+|\.?mpress[0-9]+|\.petite|\.nsp[0-9]|pec[0-9]|pecompact[0-9]*|fsg!|\.packed)$`)

// Denied are the Windows functions padmate must never bind or import. They
// are the staples of injection, input sniffing and clipboard spying; none has
// a use in a program that sends shortcuts and watches the foreground window.
// RegisterRawInputDevices and GetRawInputData would read every keyboard,
// SetClipboardViewer the clipboard.
//
// Check finds them in the import table and in the proc<Name> variables of the
// symbol table, and nowhere else: a function bound under another variable name,
// or looked up by a name built at run time, is not seen in the executable. For
// padmate's own code TestSourceBindsWindowsFunctionsByProcVariable closes that
// gap in the source (every NewProc is a proc<Name> variable of the same name,
// and no string of the code is one of these names); for the dependencies
// (golang.org/x/sys, go-winio, purego) the golden lists are the review.
var Denied = []string{
	"CreateRemoteThread", "VirtualAllocEx", "WriteProcessMemory", "QueueUserAPC",
	"RegisterRawInputDevices", "GetRawInputData", "BlockInput", "NtSetInformationThread",
	"SetClipboardViewer",
}

// versionKeys are the strings of the version resource that must not be empty:
// a file without a publisher or a description is what an engine's heuristics
// score as suspicious.
var versionKeys = []string{"CompanyName", "ProductName", "FileDescription", "LegalCopyright"}

// Level says how much of a file Inspect reads.
type Level int

const (
	// Light reads the headers, the section table and the version resource.
	// It is what the Electron executable (250 MB) and the installer get.
	Light Level = iota
	// Full also reads the sections for their entropy, the import table and
	// the COFF symbol table.
	Full
)

// Section is one entry of the section table.
type Section struct {
	Name            string
	VirtualSize     uint32
	RawSize         uint32
	Characteristics uint32
	// Entropy in bits per byte, measured at Full for every section with data.
	Entropy float64
}

// Report is what Inspect found.
type Report struct {
	Level              Level
	Machine            uint16
	PE32Plus           bool
	Subsystem          uint16
	DllCharacteristics uint16
	EntryRVA           uint32
	// EntrySection is the section the entry point lies in, "" when none.
	EntrySection string
	Sections     []Section
	// Imports maps a DLL (lower case) to its imported functions, sorted and
	// unique. Full only.
	Imports map[string][]string
	// Procs are the proc<Name> variables of padmate, golang.org/x/sys and
	// go-winio in the symbol table, as "internal/output.SendInput",
	// "windows.OpenProcess": sorted and unique. Full only.
	Procs []string
	// Symbols is how many COFF symbols the file has; HasSymtab says that it
	// has a .symtab section. Full only.
	Symbols   int
	HasSymtab bool
	// Version holds the strings of the version resource (the lowest language),
	// nil when it could not be read, and then VersionErr says why. A caller
	// that has read them with a more tolerant reader may set both.
	Version    map[string]string
	VersionErr string
}

// Reader is what Inspect needs from a file.
type Reader interface {
	io.ReaderAt
	io.ReadSeeker
}

// Inspect reads the facts Check judges. It fails when r is not a PE file at
// all; a PE file that is odd in some way is reported by Check instead.
func Inspect(r Reader, level Level) (Report, error) {
	f, err := pe.NewFile(r)
	if err != nil {
		return Report{}, err
	}
	defer func() { _ = f.Close() }()
	rep := Report{Level: level, Machine: f.Machine}
	switch h := f.OptionalHeader.(type) {
	case *pe.OptionalHeader64:
		rep.PE32Plus, rep.Subsystem, rep.DllCharacteristics, rep.EntryRVA = true, h.Subsystem, h.DllCharacteristics, h.AddressOfEntryPoint
	case *pe.OptionalHeader32:
		rep.Subsystem, rep.DllCharacteristics, rep.EntryRVA = h.Subsystem, h.DllCharacteristics, h.AddressOfEntryPoint
	default:
		return Report{}, errors.New("pecheck: no optional header")
	}
	for _, s := range f.Sections {
		sec := Section{Name: s.Name, VirtualSize: s.VirtualSize, RawSize: s.Size, Characteristics: s.Characteristics}
		if level == Full && s.Size > 0 {
			data, err := s.Data()
			if err != nil {
				return Report{}, fmt.Errorf("pecheck: section %s: %w", s.Name, err)
			}
			sec.Entropy = entropy(data)
		}
		if s.Name == ".symtab" {
			rep.HasSymtab = true
		}
		rep.Sections = append(rep.Sections, sec)
		extent := max(s.VirtualSize, s.Size)
		if rep.EntrySection == "" && rep.EntryRVA >= s.VirtualAddress && rep.EntryRVA-s.VirtualAddress < extent {
			rep.EntrySection = s.Name
		}
	}
	if level == Full {
		rep.Imports, err = imports(f)
		if err != nil {
			return Report{}, err
		}
		rep.Symbols = len(f.Symbols)
		rep.Procs = procs(f)
	}
	rep.Version, rep.VersionErr = readVersion(r)
	return rep, nil
}

func entropy(b []byte) float64 {
	if len(b) == 0 {
		return 0
	}
	var count [256]int
	for _, c := range b {
		count[c]++
	}
	var e float64
	for _, n := range count {
		if n != 0 {
			p := float64(n) / float64(len(b))
			e -= p * math.Log2(p)
		}
	}
	return e
}

// imports lists the import table by DLL. debug/pe gives "Function:dll.dll".
func imports(f *pe.File) (map[string][]string, error) {
	symbols, err := f.ImportedSymbols()
	if err != nil {
		return nil, fmt.Errorf("pecheck: import table: %w", err)
	}
	by := map[string]map[string]bool{}
	for _, s := range symbols {
		i := strings.LastIndex(s, ":")
		if i < 0 {
			continue
		}
		dll := strings.ToLower(s[i+1:])
		if by[dll] == nil {
			by[dll] = map[string]bool{}
		}
		by[dll][s[:i]] = true
	}
	out := make(map[string][]string, len(by))
	for dll, set := range by {
		out[dll] = sortedKeys(set)
	}
	return out, nil
}

// procVariable matches the variables that bind a Windows function at run time:
// mkwinsyscall and golang.org/x/sys name them proc<Function>. Only the
// packages padmate answers for are listed: the standard library has the same
// variables, and its list moves with the Go version.
var procVariable = regexp.MustCompile(`^(?:` +
	`github\.com/wdcyxxycdw/padmate/daemon/([A-Za-z0-9_/-]+)` + // padmate's own packages
	`|golang\.org/x/sys/(windows(?:/[a-z]+)*)` + // x/sys/windows, x/sys/windows/registry
	`|github\.com/Microsoft/(go-winio(?:/[A-Za-z0-9_/]+)?)` + // go-winio and its internal packages
	`|(main)` + // a command's own package
	`)\.proc([A-Z][A-Za-z0-9_]*)$`)

func procs(f *pe.File) []string {
	set := map[string]bool{}
	for _, s := range f.Symbols {
		m := procVariable.FindStringSubmatch(s.Name)
		if m == nil {
			continue
		}
		pkg := m[1] + m[2] + m[3] + m[4]
		set[pkg+"."+m[5]] = true
	}
	return sortedKeys(set)
}

func sortedKeys(set map[string]bool) []string {
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// readVersion reads the strings of the first language's table of the version
// resource. NSIS and Electron label theirs with code pages that this library
// refuses; the installer package then sets Report.Version from its own reader.
func readVersion(r io.ReadSeeker) (version map[string]string, problem string) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err.Error()
	}
	resources, err := winres.LoadFromEXE(r)
	if err != nil {
		return nil, "resources: " + err.Error()
	}
	var data []byte
	resources.WalkType(winres.RT_VERSION, func(_ winres.Identifier, _ uint16, d []byte) bool {
		data = d
		return false
	})
	if data == nil {
		return nil, "no version resource"
	}
	info, err := resversion.FromBytes(data)
	if err != nil {
		return nil, "version resource: " + err.Error()
	}
	var lowest uint16
	var table *resversion.StringTable
	for lang, t := range info.Table() {
		if table == nil || lang < lowest {
			lowest, table = lang, t
		}
	}
	out := map[string]string{}
	if table != nil {
		for k, v := range *table {
			out[k] = v
		}
	}
	return out, ""
}

// ImportLines is the import table as the golden lists it: "kernel32.dll
// GetProcAddress", sorted.
func (r Report) ImportLines() []string {
	var lines []string
	for dll, names := range r.Imports {
		for _, n := range names {
			lines = append(lines, dll+" "+n)
		}
	}
	sort.Strings(lines)
	return lines
}

// ProcLines are the proc variables as the golden lists them.
func (r Report) ProcLines() []string { return append([]string(nil), r.Procs...) }

// Golden is what the files in testdata hold for one executable: the lines of
// its import table and of its proc variables.
type Golden struct {
	Imports, Procs []string
}

// Policy says what Check demands of a file.
type Policy struct {
	// Version demands the four strings of versionKeys.
	Version bool
	// Strict is the check of a padmate daemon or CLI: x64, the closed set of
	// section names, protections, entropy, the symbol table and both goldens.
	Strict bool
	Golden Golden
	// NoGolden leaves out the comparison with Golden, for an executable of
	// another build than the one the goldens were written from (the first
	// installer of an upgrade test is an earlier release, which may bind other
	// functions). The denylist and everything else of Strict still apply.
	NoGolden bool
}

// GoPolicy is the policy of padmated.exe and padmatectl.exe.
func GoPolicy(g Golden) Policy { return Policy{Version: true, Strict: true, Golden: g} }

// ShippedPolicy is the policy of the files that are not Go: padmate.exe
// (Electron) and the installer. They have a version resource and none of the
// section names of a packer; their imports are not padmate's to list.
func ShippedPolicy() Policy { return Policy{Version: true} }

// Check returns what is wrong with the file r describes, one message per
// problem, none when it passes.
func Check(r Report, p Policy) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	for _, s := range r.Sections {
		if packerSection.MatchString(s.Name) {
			add("section %q is the name of an executable packer", s.Name)
		}
	}
	if p.Version {
		if r.Version == nil {
			add("version resource: %s", r.VersionErr)
		} else {
			for _, key := range versionKeys {
				if strings.TrimSpace(r.Version[key]) == "" {
					add("version resource: %s is empty", key)
				}
			}
		}
	}
	if !p.Strict {
		return problems
	}
	if r.Level != Full {
		return append(problems, "the report was read at Light level: imports, symbols and entropy are missing")
	}
	if !r.PE32Plus || r.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		add("not a PE32+ executable for AMD64 (machine %#x)", r.Machine)
	}
	if r.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_GUI && r.Subsystem != pe.IMAGE_SUBSYSTEM_WINDOWS_CUI {
		add("subsystem %d is neither Windows GUI nor console", r.Subsystem)
	}
	for _, flag := range []struct {
		bit  uint16
		name string
	}{{dllDynamicBase, "DYNAMIC_BASE"}, {dllNXCompat, "NX_COMPAT"}, {dllHighEntropyVA, "HIGH_ENTROPY_VA"}} {
		if r.DllCharacteristics&flag.bit == 0 {
			add("DllCharacteristics %#x lacks %s", r.DllCharacteristics, flag.name)
		}
	}
	for _, s := range r.Sections {
		if !goSection(s.Name) && !packerSection.MatchString(s.Name) {
			add("section %q is not one the Go linker writes (the closed set is %s and .zdebug_*)", s.Name, strings.Join(sortedKeys(goSections), " "))
		}
		if s.Characteristics&scnMemExecute != 0 && s.Characteristics&scnMemWrite != 0 {
			add("section %q is writable and executable", s.Name)
		}
		if entropyChecked[s.Name] && s.Entropy > EntropyCeiling {
			add("section %q has entropy %.2f, above %.1f: it looks packed or encrypted", s.Name, s.Entropy, EntropyCeiling)
		}
	}
	if r.EntrySection != ".text" {
		add("the entry point (RVA %#x) is in section %q, want .text", r.EntryRVA, r.EntrySection)
	}
	if !r.HasSymtab || r.Symbols == 0 {
		add("no .symtab section or no COFF symbols: the build must not strip them, the list of Windows functions is read from them")
	}
	dlls := make([]string, 0, len(r.Imports))
	for dll := range r.Imports {
		dlls = append(dlls, dll)
	}
	sort.Strings(dlls)
	for _, dll := range dlls {
		for _, n := range r.Imports[dll] {
			if denied(n) {
				add("%s imports %s, which padmate must never use", dll, n)
			}
		}
	}
	for _, line := range r.Procs {
		if i := strings.LastIndex(line, "."); i >= 0 && denied(line[i+1:]) {
			add("%s is bound, which padmate must never use", line)
		}
	}
	if !p.NoGolden {
		problems = append(problems, diff("import", r.ImportLines(), p.Golden.Imports)...)
		problems = append(problems, diff("Windows function (proc variable)", r.ProcLines(), p.Golden.Procs)...)
	}
	return problems
}

func denied(name string) bool {
	for _, d := range Denied {
		if name == d {
			return true
		}
	}
	return false
}

// diff describes how got differs from the golden, in sorted lists.
func diff(what string, got, want []string) []string {
	have := map[string]bool{}
	for _, l := range want {
		have[l] = true
	}
	seen := map[string]bool{}
	var problems []string
	for _, l := range got {
		seen[l] = true
		if !have[l] {
			problems = append(problems, fmt.Sprintf("new %s %q is not in the golden: review it, then run go run ./tools/pecheck -update", what, l))
		}
	}
	for _, l := range want {
		if !seen[l] {
			problems = append(problems, fmt.Sprintf("%s %q is in the golden but no longer in the file: run go run ./tools/pecheck -update", what, l))
		}
	}
	return problems
}
