package script

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

func TestDiagnosticsMapCompileError(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte("const = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err == nil {
		t.Fatal("Build succeeded for invalid TypeScript")
	}
	ds := DiagnosticsOf(Diagnose(PhaseCompile, err))
	if len(ds) != 1 {
		t.Fatalf("diagnostics = %#v, want one", ds)
	}
	if ds[0].File != "main.ts" || ds[0].Line != 1 || ds[0].Column != 7 || ds[0].Severity != SeverityError {
		t.Fatalf("diagnostic = %+v, want main.ts:1:7 error", ds[0])
	}
	if !strings.Contains(ds[0].String(), "main.ts:1:7: error:") {
		t.Errorf("diagnostic string = %q", ds[0])
	}
}

func TestHostDiagnosticsKeepSourcePositionForSetupHandlerAndTeardown(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		run   func(*Host, *goja.Program) error
		phase Phase
		line  int
	}{
		{"setup", `globalThis.__padmate_bundle = { default: { setup() {
  throw new Error("setup boom");
} } };`, func(h *Host, p *goja.Program) error { return h.Activate(p) }, PhaseSetup, 2},
		{"handler", `globalThis.__padmate_bundle = { default: {} };
globalThis.__padmate.on("south", "press", () => { throw new Error("handler boom"); });`, func(h *Host, p *goja.Program) error {
			if err := h.Activate(p); err != nil {
				return err
			}
			return h.Dispatch(Event{Control: "south", Gesture: "press"})
		}, PhaseHandler, 2},
		{"teardown", `globalThis.__padmate_bundle = { default: { teardown() {
  throw new Error("teardown boom");
} } };`, func(h *Host, p *goja.Program) error {
			if err := h.Activate(p); err != nil {
				return err
			}
			return h.Dispose()
		}, PhaseTeardown, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHost(clock.NewFake())
			p, err := goja.Compile(tc.name+".js", tc.src, false)
			if err != nil {
				t.Fatal(err)
			}
			err = tc.run(h, p)
			if err == nil {
				t.Fatal("call succeeded")
			}
			ds := DiagnosticsOf(err)
			if len(ds) != 1 {
				t.Fatalf("diagnostics = %#v", ds)
			}
			if ds[0].Phase != tc.phase || ds[0].Line != tc.line || ds[0].Column == 0 {
				t.Fatalf("diagnostic = %+v, want phase %s line %d with column", ds[0], tc.phase, tc.line)
			}
		})
	}
}

func TestDiagnoseUnwrapsOriginalError(t *testing.T) {
	cause := errors.New("boom")
	err := Diagnose(PhaseHandler, cause)
	if !errors.Is(err, cause) {
		t.Fatalf("Diagnose(%v) does not unwrap cause", err)
	}
	if ds := DiagnosticsOf(err); len(ds) != 1 || ds[0].Message != "boom" {
		t.Fatalf("diagnostics = %#v", ds)
	}
}

func TestLifecycleLoadMapsCompileErrors(t *testing.T) {
	l := NewLifecycle(clock.NewFake())
	got := <-l.Load(context.Background(), func(context.Context) (*goja.Program, error) {
		return nil, &compile.BuildError{Diagnostics: []compile.Diagnostic{{File: "main.ts", Line: 4, Column: 3, Severity: compile.SeverityError, Text: "bad"}}}
	})
	if got.Err == nil || len(DiagnosticsOf(got.Err)) != 1 {
		t.Fatalf("Load error = %v, want one diagnostic", got.Err)
	}
	if d := DiagnosticsOf(got.Err)[0]; d.File != "main.ts" || d.Line != 4 || d.Column != 3 || d.Phase != PhaseCompile {
		t.Fatalf("diagnostic = %+v", d)
	}
}

func TestRuntimeDiagnosticUsesInlineSourceMap(t *testing.T) {
	root := t.TempDir()
	src := "import { defineScript } from \"padmate\";\n" +
		"export default defineScript({ setup() { throw new Error(\"mapped\"); } });\n"
	if err := os.WriteFile(filepath.Join(root, "main.ts"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := compile.Build(compile.BuildOptions{Root: root, Entry: "main.ts", API: "0.8"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := goja.Compile("bundle.js", string(bundle.JS), false)
	if err != nil {
		t.Fatal(err)
	}
	err = NewHost(clock.NewFake()).Activate(p)
	if err == nil {
		t.Fatal("mapped setup unexpectedly succeeded")
	}
	ds := DiagnosticsOf(err)
	if len(ds) != 1 || ds[0].File != "main.ts" || ds[0].Line != 2 || ds[0].Column == 0 {
		t.Fatalf("diagnostics = %#v, want main.ts:2:<col>", ds)
	}
}
