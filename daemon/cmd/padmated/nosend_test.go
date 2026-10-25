package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// injectFile is the one file of padmated that may construct
// output.SendInput.
const injectFile = "sender_windows.go"

// TestNoSendInput checks that padmated injects only through
// sender_windows.go (CLAUDE.md invariants 3 and 6), whose newInjectSender
// only --inject calls (TestChooseSender): no other non-test file of the
// command refers to an identifier declared in
// internal/output/sendinput_windows.go, such as output.SendInput. No
// non-test file, sender_windows.go included, imports x/sys/windows or
// purego, or uses package syscall for more than its signals, so the
// printer and SendInput are the only Senders. A dependency check cannot
// show this, because padmated imports package output for the T-output
// loop. forbidigo checks output.SendInput across the module.
func TestNoSendInput(t *testing.T) {
	fset := token.NewFileSet()
	src, err := parser.ParseFile(fset, filepath.Join("..", "..", "internal", "output", "sendinput_windows.go"), nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	banned := topLevelNames(src)
	if !slices.Contains(banned, "SendInput") {
		t.Fatalf("sendinput_windows.go declares %v, not SendInput: update this test", banned)
	}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		checked++
		allowed := banned
		if name == injectFile {
			allowed = nil // the imports and package syscall are still checked
			if refs := injectRefs(fset, f, banned); len(refs) == 0 {
				t.Errorf("%s refers to nothing of sendinput_windows.go: update this test", name)
			}
		}
		for _, p := range injectRefs(fset, f, allowed) {
			t.Errorf("%s: padmated may inject only in %s", p, injectFile)
		}
	}
	if checked == 0 {
		t.Fatal("no Go files checked")
	}

	// The check finds what it looks for.
	const bad = `package main

import (
	"syscall"

	"github.com/ebitengine/purego"
	out "github.com/wdcyxxycdw/padmate/daemon/internal/output"
	"golang.org/x/sys/windows"
)

var s out.Sender = out.SendInput{}

var user32 = syscall.NewLazyDLL("user32.dll")

var stop = []syscall.Signal{syscall.SIGTERM}
`
	f, err := parser.ParseFile(fset, "bad.go", bad, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if got := injectRefs(fset, f, banned); len(got) != 4 {
		t.Errorf("injectRefs(bad.go) = %q, want the imports of purego and windows, out.SendInput and syscall.NewLazyDLL", got)
	}
}

// injectRefs returns the places in f that import x/sys/windows or purego,
// refer to one of the banned names of package output, or to a name of
// package syscall other than its signals.
func injectRefs(fset *token.FileSet, f *ast.File, banned []string) []string {
	var refs []string
	var outName, sysName string // the names packages output and syscall are imported as
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		switch path {
		case "golang.org/x/sys/windows", "github.com/ebitengine/purego":
			refs = append(refs, fmt.Sprintf("%s: imports %s", fset.Position(imp.Pos()), path))
			continue
		case "github.com/wdcyxxycdw/padmate/daemon/internal/output":
			outName = local
		case "syscall":
			sysName = local
		default:
			continue
		}
		if local == "." || local == "_" {
			refs = append(refs, fmt.Sprintf("%s: imports %s as %s; import it by name so that this check works", fset.Position(imp.Pos()), path, local))
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		switch name := sel.Sel.Name; {
		case !ok:
		case outName != "" && x.Name == outName && slices.Contains(banned, name),
			sysName != "" && x.Name == sysName && name != "Signal" && !strings.HasPrefix(name, "SIG"):
			refs = append(refs, fmt.Sprintf("%s: %s.%s", fset.Position(sel.Pos()), x.Name, name))
		}
		return true
	})
	return refs
}

// topLevelNames returns the names of f's top-level types, functions,
// variables and constants.
func topLevelNames(f *ast.File) []string {
	var names []string
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil {
				names = append(names, d.Name.Name)
			}
		case *ast.GenDecl:
			for _, s := range d.Specs {
				switch s := s.(type) {
				case *ast.TypeSpec:
					names = append(names, s.Name.Name)
				case *ast.ValueSpec:
					for _, n := range s.Names {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// TestInjectSenderOnlyToLoop checks that startLive hands the Sender that
// chooseSender returns to output.StartLoop only, as its second argument,
// so that T-output is the only caller of SendInput in padmated (CLAUDE.md
// invariant 3). The startup sweep of the held-key log (OUT-14) runs on
// T-output too (Loop.Swept), not on startLive's goroutine.
func TestInjectSenderOnlyToLoop(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "live.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	bad, loops := senderUses(fset, f)
	for _, p := range bad {
		t.Errorf("%s: the Sender of chooseSender may only go to output.StartLoop", p)
	}
	if loops != 1 {
		t.Errorf("startLive passes the Sender of chooseSender to output.StartLoop %d times, want 1: update this test", loops)
	}

	// The check finds what it looks for.
	const leak = `package main

import out "github.com/wdcyxxycdw/padmate/daemon/internal/output"

func startLive() {
	s, err := chooseSender(true, nil, nil)
	if err != nil {
		return
	}
	sweep(s)
	l := out.StartLoop(nil, s, out.LoopOptions{})
	_ = out.StartLoop(s, nil, out.LoopOptions{})
	_ = l
}
`
	f, err = parser.ParseFile(fset, "leak.go", leak, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	if bad, loops := senderUses(fset, f); len(bad) != 2 || loops != 1 {
		t.Errorf("senderUses(leak.go) = %q, %d; want the call of sweep and the first argument of StartLoop, and 1", bad, loops)
	}
}

// senderUses returns the places in startLive of f that use the Sender
// chooseSender returns other than as the second argument of
// output.StartLoop, and how many times it is that argument.
func senderUses(fset *token.FileSet, f *ast.File) (bad []string, loops int) {
	outName := ""
	for _, imp := range f.Imports {
		if path, _ := strconv.Unquote(imp.Path.Value); path == "github.com/wdcyxxycdw/padmate/daemon/internal/output" {
			outName = "output"
			if imp.Name != nil {
				outName = imp.Name.Name
			}
		}
	}
	var body *ast.BlockStmt
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == "startLive" {
			body = fd.Body
		}
	}
	if body == nil || outName == "" {
		return []string{"no startLive, or no import of package output"}, 0
	}
	var sender *ast.Ident
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || sender != nil || len(as.Rhs) != 1 || len(as.Lhs) == 0 {
			return true
		}
		if call, ok := as.Rhs[0].(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "chooseSender" {
				sender, _ = as.Lhs[0].(*ast.Ident)
			}
		}
		return true
	})
	if sender == nil {
		return []string{"startLive does not call chooseSender"}, 0
	}
	allowed := map[*ast.Ident]bool{sender: true}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "StartLoop" {
			return true
		}
		if x, ok := sel.X.(*ast.Ident); !ok || x.Name != outName {
			return true
		}
		if id, ok := call.Args[1].(*ast.Ident); ok && id.Name == sender.Name {
			allowed[id] = true
			loops++
		}
		return true
	})
	ast.Inspect(body, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == sender.Name && !allowed[id] {
			bad = append(bad, fset.Position(id.Pos()).String())
		}
		return true
	})
	return bad, loops
}
