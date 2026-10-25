package exeresources

import (
	"debug/pe"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestResourcePatchRealPEKeepsSubsystem(t *testing.T) {
	for _, gui := range []bool{false, true} {
		name := "console"
		wantSubsystem := uint16(3)
		if gui {
			name, wantSubsystem = "gui", 2
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			entry := filepath.Join(dir, "main.go")
			if err := os.WriteFile(entry, []byte(`package main
import "fmt"
var Version="dev"
func main(){fmt.Println(Version)}
`), 0o600); err != nil {
				t.Fatal(err)
			}
			exe := filepath.Join(dir, "fixture.exe")
			flags := "-X main.Version=v0.7.0-preview"
			if gui {
				flags += " -H=windowsgui"
			}
			build := exec.Command("go", "build", "-ldflags", flags, "-o", exe, entry)
			build.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			src, err := os.Open(exe)
			if err != nil {
				t.Fatal(err)
			}
			dst, err := os.Create(filepath.Join(dir, "patched.exe"))
			if err != nil {
				_ = src.Close()
				t.Fatal(err)
			}
			err = Write(dst, src, "fixture.exe", "v0.7.0-preview")
			_ = src.Close()
			_ = dst.Close()
			if err != nil {
				t.Fatal(err)
			}
			patched := filepath.Join(dir, "patched.exe")
			f, err := os.Open(patched)
			if err != nil {
				t.Fatal(err)
			}
			err = Check(f, "fixture.exe", "v0.7.0-preview", gui)
			_ = f.Close()
			if err != nil {
				t.Fatal(err)
			}
			p, err := pe.Open(patched)
			if err != nil {
				t.Fatal(err)
			}
			if got := p.OptionalHeader.(*pe.OptionalHeader64).Subsystem; got != wantSubsystem {
				t.Errorf("subsystem=%d, want %d", got, wantSubsystem)
			}
			_ = p.Close()
			run := exec.Command(patched)
			run.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
			if output, err := run.CombinedOutput(); err != nil || strings.TrimSpace(string(output)) != "v0.7.0-preview" {
				t.Fatalf("patched binary: %v: %s", err, output)
			}
		})
	}
}
