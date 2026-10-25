package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestFlags(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		code   int
		stdout bool // usage on stdout rather than stderr
		msg    string
	}{
		{args: []string{"-h"}, code: 0, stdout: true},
		{args: []string{"-nope"}, code: 2},
		{args: []string{"extra"}, code: 2, msg: `unexpected argument "extra"`},
		{args: []string{"-dispatch", "0"}, code: 2, msg: "-dispatch and -runs must be at least 1"},
		{args: []string{"-allocs", "-1"}, code: 2, msg: "-warmup, -allocs and -idle must not be negative"},
		{args: []string{"-idle", "-1"}, code: 2, msg: "-warmup, -allocs and -idle must not be negative"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tc.args, &stdout, &stderr); code != tc.code {
			t.Errorf("%q: exit %d, want %d; stderr %s", tc.args, code, tc.code, stderr.String())
		}
		usageOn := &stderr
		if tc.stdout {
			usageOn = &stdout
		}
		if !strings.Contains(usageOn.String(), "usage: spk03") {
			t.Errorf("%q: no usage in %q", tc.args, usageOn.String())
		}
		if !strings.Contains(stderr.String(), tc.msg) {
			t.Errorf("%q: stderr %q lacks %q", tc.args, stderr.String(), tc.msg)
		}
	}
}

func TestDefaults(t *testing.T) {
	var stdout, stderr bytes.Buffer
	opts, _, ok := parseFlags(nil, &stdout, &stderr)
	if !ok || opts.Dispatch.N != 100000 || opts.Dispatch.Warmup != 10000 || opts.Dispatch.IdleN != 100 || opts.Runs != 200 || opts.Cycles == nil {
		t.Errorf("defaults: %+v, ok %v", opts, ok)
	}
	if opts, _, _ := parseFlags([]string{"-cycles=false"}, &stdout, &stderr); opts.Cycles != nil {
		t.Error("-cycles=false kept the calibration")
	}
}

func TestSmallRun(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the watchdog checks, about 2 s")
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-dispatch", "100", "-warmup", "10", "-allocs", "10", "-idle", "2", "-runs", "2", "-cycles=false"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit %d; stderr %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"## SPK-03 results\n", "| dispatch noop | 100 |", "| dispatch tap after 100ms idle | 2 |", "| esbuild build, warm: every build after the first | 2 |", "### Watchdog", "Skipped.\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
