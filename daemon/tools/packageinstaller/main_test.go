package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runTool(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestVersionCommand(t *testing.T) {
	code, out, errOut := runTool("version", "-version", "v0.14.0-rc.1")
	if code != 0 || out != "0.14.0-rc.1\npadmate-v0.14.0-rc.1-windows-amd64-setup.exe\n" || errOut != "" {
		t.Errorf("version v0.14.0-rc.1 = %d %q %q", code, out, errOut)
	}
}

func TestRefusesVersionsThatAreNotTags(t *testing.T) {
	for _, label := range []string{"", "dev", "de58d1d-dirty", "0.14.0", "v0.14", "v1.2.3+meta"} {
		for _, command := range []string{"version", "stage", "verify"} {
			code, out, errOut := runTool(command, "-version", label)
			if code != 1 || out != "" || !strings.Contains(errOut, "not a release tag") && !strings.Contains(errOut, "leading zero") {
				t.Errorf("%s -version %q = %d %q %q, want exit 1 and a message", command, label, code, out, errOut)
			}
		}
	}
}

func TestUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		nil,
		{"build"},
		{"version", "-version", "v1.0.0", "extra"},
		{"version", "-nope"},
	} {
		if code, _, _ := runTool(args...); code != 2 {
			t.Errorf("%v exit %d, want 2", args, code)
		}
	}
}

func TestVerifyFlags(t *testing.T) {
	dist := t.TempDir()

	// The signing flags that qualify -signed need it.
	for _, flag := range []string{"-allow-untrusted-root", "-timestamp=false"} {
		code, _, errOut := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false", flag)
		if code != 2 || !strings.Contains(errOut, "only go with -signed") {
			t.Errorf("verify %s without -signed = %d %q, want exit 2", flag, code, errOut)
		}
	}

	// An empty -signed is not "-signed was not given": a script that passes
	// "$PADMATE_SIGN_SUBJECT" with an empty variable would otherwise get a green
	// verify of a tree whose signatures were never looked at.
	for _, value := range []string{"", "  ", "\t"} {
		code, out, errOut := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false", "-signed", value)
		if code != 2 || out != "" || !strings.Contains(errOut, "-signed needs") {
			t.Errorf("verify -signed %q = %d %q %.120q, want exit 2 and a message", value, code, out, errOut)
		}
		code, _, errOut = runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false", "-signed="+value, "-allow-untrusted-root")
		if code != 2 || !strings.Contains(errOut, "-signed needs") {
			t.Errorf("verify -signed=%q -allow-untrusted-root = %d %.120q, want exit 2 and the -signed message", value, code, errOut)
		}
	}

	// An empty folder has every problem; the ones this test looks for are
	// reported only when asked for.
	_, _, plain := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false")
	if strings.Contains(plain, "latest.yml") || strings.Contains(plain, "is not signed") {
		t.Errorf("verify without flags checks signatures or latest.yml:\n%s", plain)
	}
	code, _, withInfo := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false", "-update-info")
	if code != 1 || !strings.Contains(withInfo, "latest.yml:") {
		t.Errorf("verify -update-info = %d, latest.yml is not reported:\n%s", code, withInfo)
	}
	code, _, signed := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false", "-signed", "padmate", "-allow-untrusted-root", "-timestamp=false")
	if code != 1 || !strings.Contains(signed, "padmate.exe: signature:") || !strings.Contains(signed, "padmate-v0.14.0-windows-amd64-setup.exe: signature:") {
		t.Errorf("verify -signed = %d, the files are not named:\n%s", code, signed)
	}
}

// latest.yml is checked when it is there, even without -update-info.
func TestVerifyChecksLatestYMLWhenItExists(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "latest.yml"), []byte("version 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, errOut := runTool("verify", "-version", "v0.14.0", "-dist", dist, "-run=false")
	if !strings.Contains(errOut, "latest.yml line 1") {
		t.Errorf("a latest.yml that is there was not read:\n%s", errOut)
	}
}
