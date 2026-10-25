package installerpkg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf16"
)

// envTestNSIS turns on TestWizardDirNSIS; envMakensis names the makensis.exe
// to use when electron-builder's cache has none.
const (
	envTestNSIS = "PADMATE_TEST_NSIS"
	envMakensis = "PADMATE_MAKENSIS"
)

// findMakensis returns the makensis.exe electron-builder downloaded the first
// time it built the installer (docs/build-installer.md), or the one named by
// PADMATE_MAKENSIS.
func findMakensis(t *testing.T) string {
	t.Helper()
	if exe := os.Getenv(envMakensis); exe != "" {
		return exe
	}
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		t.Skip("no LOCALAPPDATA to look for electron-builder's NSIS in")
	}
	found, _ := filepath.Glob(filepath.Join(local, "electron-builder", "Cache", "nsis*", "nsis*", "makensis.exe"))
	if len(found) == 0 {
		t.Skip("electron-builder has not downloaded NSIS yet (run task package:installer once), or set " + envMakensis)
	}
	return found[len(found)-1]
}

// nsisString quotes s for a double-quoted NSIS string.
func nsisString(s string) string {
	return `"` + strings.NewReplacer(`$`, `$$`, `"`, `$\"`).Replace(s) + `"`
}

// TestWizardDirNSIS compiles the folder rule of the wizard and its checks of a
// path (app/build/padmate-wizard.nsh) into a small program of its own, as
// electron-builder's makensis does, and runs every case of TestWizardDir and of
// pathCases through it. The program only reads strings and writes one file
// under the test's folder; it is not the installer and touches nothing else.
//
//	PADMATE_TEST_NSIS=1 go test -run TestWizardDirNSIS ./internal/installerpkg
func TestWizardDirNSIS(t *testing.T) {
	if os.Getenv(envTestNSIS) != "1" {
		t.Skip("set " + envTestNSIS + "=1 to compile and run the NSIS folder rule")
	}
	makensis := findMakensis(t)
	build, err := filepath.Abs(filepath.Join("..", "..", "..", "app", "build"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	exe, out := filepath.Join(dir, "rule.exe"), filepath.Join(dir, "rule.txt")

	var script strings.Builder
	fmt.Fprintf(&script, "Unicode true\n!include \"LogicLib.nsh\"\n!addincludedir %s\n!define APP_FILENAME %s\n", nsisString(build), nsisString(FolderName))
	script.WriteString("!include \"padmate-wizard.nsh\"\nName \"rule\"\n")
	fmt.Fprintf(&script, "OutFile %s\nRequestExecutionLevel user\nSilentInstall silent\nShowInstDetails nevershow\n", nsisString(exe))
	script.WriteString("!insertmacro PadmatePathFunctions\nSection\n")
	fmt.Fprintf(&script, "  FileOpen $0 %s w\n  FileWriteUTF16LE /BOM $0 \"\"\n", nsisString(out))
	for _, c := range wizardCases {
		fmt.Fprintf(&script, "  StrCpy $R9 %s\n  StrCpy $R8 %s\n  Call PadmateWizardDir\n  FileWriteUTF16LE $0 \"$R8|$R9$\\r$\\n\"\n",
			nsisString(c.offered), nsisString(c.typed))
	}
	for _, c := range pathCases {
		fmt.Fprintf(&script, "  StrCpy $R8 %s\n  Call PadmateIsFullPath\n  Pop $1\n  Call PadmateFitsPath\n  Pop $2\n  FileWriteUTF16LE $0 \"$1|$2$\\r$\\n\"\n",
			nsisString(c.dir))
	}
	script.WriteString("  FileClose $0\nSectionEnd\n")
	nsi := filepath.Join(dir, "rule.nsi")
	if err := os.WriteFile(nsi, []byte(script.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// -WX: electron-builder treats NSIS warnings as errors, and so does this.
	compile := exec.CommandContext(ctx, makensis, "-WX", "-INPUTCHARSET", "UTF8", nsi)
	compile.Dir = dir
	if output, err := compile.CombinedOutput(); err != nil {
		t.Fatalf("makensis: %v\n%s", err, output)
	}
	run := exec.CommandContext(ctx, exe)
	run.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := run.CombinedOutput(); err != nil {
		t.Fatalf("%s: %v\n%s", exe, err, output)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(decodeUTF16LE(t, data), "\r\n"), "\r\n")
	if len(lines) != len(wizardCases)+len(pathCases) {
		t.Fatalf("the program wrote %d lines for %d cases:\n%q", len(lines), len(wizardCases)+len(pathCases), lines)
	}
	for i, c := range wizardCases {
		want := c.dir + "|0"
		if c.added {
			want = c.dir + "|1"
		}
		if lines[i] != want {
			t.Errorf("%s: NSIS gives %q for offered %q, typed %q; WizardDir gives %q", c.name, lines[i], c.offered, c.typed, want)
		}
	}
	// "1" for yes and "0" for no: IsFullPath, then FitsPath.
	flag := func(b bool) string {
		if b {
			return "1"
		}
		return "0"
	}
	for i, c := range pathCases {
		want := flag(c.full) + "|" + flag(c.fits)
		if got := lines[len(wizardCases)+i]; got != want {
			t.Errorf("%s: NSIS gives %q for %q (full path, fits); IsFullPath and FitsPath give %q", c.name, got, c.dir, want)
		}
	}
}

// decodeUTF16LE reads what FileWriteUTF16LE wrote: a byte order mark, then
// UTF-16 little endian.
func decodeUTF16LE(t *testing.T, data []byte) string {
	t.Helper()
	data = bytes.TrimPrefix(data, []byte{0xFF, 0xFE})
	if len(data)%2 != 0 {
		t.Fatalf("%d bytes are not UTF-16", len(data))
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = uint16(data[2*i]) | uint16(data[2*i+1])<<8
	}
	return string(utf16.Decode(units))
}
