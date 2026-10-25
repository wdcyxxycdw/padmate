package pecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// The proc list in the goldens is read from variables named proc<Name>, and the
// denylist is checked against it and the import table. That reads what the
// linker kept under the usual names; it cannot see a Windows function that a
// program binds under another name, or one whose name it builds at run time.
// So the other half of the guarantee is here, in the source: every function
// padmate binds goes through a package-level proc<Name> variable (what
// mkwinsyscall writes for a //sys line), and no file of padmate's names a
// denied function at all. Dependencies are covered by the goldens only.

// bindingProblems reports what is wrong in one parsed file of padmate's own
// code, one message per problem.
func bindingProblems(fset *token.FileSet, f *ast.File) []string {
	var problems []string
	add := func(pos token.Pos, format string, args ...any) {
		problems = append(problems, fset.Position(pos).String()+": "+fmt.Sprintf(format, args...))
	}
	// The calls of NewProc that are the whole value of a package-level
	// proc<Name> variable of the same Name.
	allowed := map[*ast.CallExpr]bool{}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			for i, value := range vs.Values {
				call, name, ok := newProc(value)
				if ok && i < len(vs.Names) && vs.Names[i].Name == "proc"+name {
					allowed[call] = true
				}
			}
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.CallExpr:
			call, name, literal := newProc(n)
			switch {
			case call == nil || allowed[call]:
			case !literal:
				add(n.Pos(), "NewProc is called with a name that is not a string literal: pecheck cannot list it")
			default:
				add(n.Pos(), "NewProc(%q) is not the value of a package-level variable named proc%s: pecheck lists the functions a program binds by that name", name, name)
			}
		case *ast.BasicLit:
			if n.Kind != token.STRING {
				break
			}
			if s, err := strconv.Unquote(n.Value); err == nil && slices.Contains(Denied, s) {
				add(n.Pos(), "the string %q names a function padmate must never use (pecheck.Denied)", s)
			}
		}
		return true
	})
	return problems
}

// newProc recognizes x.NewProc(arg): the call is nil when e is no such call;
// literal says that arg is a string literal, whose value is name.
func newProc(e ast.Expr) (call *ast.CallExpr, name string, literal bool) {
	c, ok := e.(*ast.CallExpr)
	if !ok {
		return nil, "", false
	}
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "NewProc" {
		return nil, "", false
	}
	if len(c.Args) == 1 {
		if lit, ok := c.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			if s, err := strconv.Unquote(lit.Value); err == nil {
				return c, s, true
			}
		}
	}
	return c, "", false
}

func TestSourceBindsWindowsFunctionsByProcVariable(t *testing.T) {
	// The rule finds what it looks for.
	const bad = `package x

import "golang.org/x/sys/windows"

var user32 = windows.NewLazySystemDLL("user32.dll")

var (
	procGetDlgItem = user32.NewProc("GetDlgItem")
	blockInput     = user32.NewProc("GetDlgCtrlID")
	procWrongName  = user32.NewProc("FindWindowW")
	procComputed   = user32.NewProc(name)
)

func f() {
	procLocal := user32.NewProc("IsWindow")
	_ = procLocal
	_ = "WriteProcessMemory"
}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bad.go", bad, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(bindingProblems(fset, f), "\n")
	for _, want := range []string{
		`NewProc("GetDlgCtrlID") is not the value of a package-level variable named procGetDlgCtrlID`,
		`NewProc("FindWindowW") is not the value of a package-level variable named procFindWindowW`,
		`NewProc is called with a name that is not a string literal`,
		`NewProc("IsWindow") is not the value of a package-level variable named procIsWindow`,
		`the string "WriteProcessMemory" names a function padmate must never use`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not reported in\n%s", want, got)
		}
	}
	if strings.Contains(got, "GetDlgItem") {
		t.Errorf("a correctly named variable is reported:\n%s", got)
	}
	if n := len(strings.Split(got, "\n")); n != 5 {
		t.Errorf("%d problems reported, want 5:\n%s", n, got)
	}

	// And padmate's own code keeps to it. The package's own files (which name the
	// denied functions to forbid them) and test data are not production code;
	// _test.go files are not shipped.
	root := filepath.Join("..", "..")
	own := filepath.Join(root, "internal", "pecheck")
	checked := 0
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" || d.Name() == "vendor" || path == own {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		checked++
		for _, p := range bindingProblems(fset, f) {
			t.Error(p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 {
		t.Errorf("only %d Go files were checked: is the module root %s right?", checked, root)
	}
}
