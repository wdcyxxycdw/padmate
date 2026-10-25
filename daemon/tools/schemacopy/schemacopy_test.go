package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCopy(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.json")
	dst := filepath.Join(dir, "dst.json")
	// Bytes are copied as they are: CRLF, a BOM-less UTF-8 label, no
	// trailing newline.
	want := []byte("{\r\n  \"label\": \"撤销\"\r\n}")
	writeFile(t, src, want)

	var stderr bytes.Buffer
	if code := run([]string{"-src", src, "-dst", dst}, &stderr); code != 0 {
		t.Fatalf("run = %d, stderr %q", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr %q, want nothing", stderr.String())
	}
	if got := readFile(t, dst); !bytes.Equal(got, want) {
		t.Errorf("dst = %q, want %q", got, want)
	}

	// A current copy is left alone.
	old := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(dst, old, old); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"-src", src, "-dst", dst}, &stderr); code != 0 {
		t.Fatalf("second run = %d, stderr %q", code, stderr.String())
	}
	if fi, err := os.Stat(dst); err != nil || !fi.ModTime().Equal(old) {
		t.Errorf("an up-to-date dst was rewritten: %v, %v", fi.ModTime(), err)
	}

	// A stale copy is replaced.
	writeFile(t, dst, []byte(`{"stale": true}`))
	if code := run([]string{"-src", src, "-dst", dst}, &stderr); code != 0 {
		t.Fatalf("third run = %d, stderr %q", code, stderr.String())
	}
	if got := readFile(t, dst); !bytes.Equal(got, want) {
		t.Errorf("dst = %q, want %q", got, want)
	}
}

func TestErrors(t *testing.T) {
	dir := t.TempDir()
	valid := filepath.Join(dir, "valid.json")
	writeFile(t, valid, []byte(`{}`))
	invalid := filepath.Join(dir, "invalid.json")
	writeFile(t, invalid, []byte(`{"a": 1,}`))
	dst := filepath.Join(dir, "dst.json")

	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stderr string // substring
	}{
		{"help", []string{"-h"}, 0, "-src"},
		{"no flags", nil, 2, "-src and -dst are required"},
		{"no dst", []string{"-src", valid}, 2, "-src and -dst are required"},
		{"extra argument", []string{"-src", valid, "-dst", dst, "x"}, 2, `unexpected argument "x"`},
		{"bad flag", []string{"-out", dst}, 2, "-out"},
		{"missing src", []string{"-src", filepath.Join(dir, "none.json"), "-dst", dst}, 1, "none.json"},
		{"invalid JSON", []string{"-src", invalid, "-dst", dst}, 1, "invalid.json is not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stderr bytes.Buffer
			if code := run(tc.args, &stderr); code != tc.code {
				t.Errorf("run = %d, want %d; stderr %q", code, tc.code, stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.stderr) {
				t.Errorf("stderr %q does not contain %q", stderr.String(), tc.stderr)
			}
			if _, err := os.Stat(dst); !os.IsNotExist(err) {
				t.Errorf("dst was written: %v", err)
			}
		})
	}
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
