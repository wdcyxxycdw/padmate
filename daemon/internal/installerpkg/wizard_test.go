package installerpkg

import (
	"strings"
	"testing"
)

// wizardCase is one press of Next on the folder page of the wizard.
type wizardCase struct {
	name, offered, typed string
	dir                  string
	added                bool
}

// The cases are shared with TestWizardDirNSIS, which runs the same table
// through the NSIS code.
var wizardCases = []wizardCase{
	{"the default, left alone", `C:\Users\me\AppData\Local\Programs\padmate`, `C:\Users\me\AppData\Local\Programs\padmate`,
		`C:\Users\me\AppData\Local\Programs\padmate`, false},
	{"an old folder with another name, left alone (an upgrade keeps it)", `D:\Old\Foo`, `D:\Old\Foo`, `D:\Old\Foo`, false},
	{"an old folder, left alone but for a backslash", `D:\Old\Foo`, `D:\Old\Foo\`, `D:\Old\Foo`, false},
	{"an offered folder with a backslash, left alone", `D:\Old\Foo\`, `D:\Old\Foo`, `D:\Old\Foo`, false},
	{"an old folder, left alone but for the case", `D:\Old\Foo`, `d:\old\FOO`, `d:\old\FOO`, false},
	{"a parent", `C:\a\padmate`, `D:\Apps`, `D:\Apps\padmate`, true},
	{"a parent with a backslash", `C:\a\padmate`, `D:\Apps\`, `D:\Apps\padmate`, true},
	{"a parent with Chinese and spaces", `C:\a\padmate`, `D:\我的 软件\画画`, `D:\我的 软件\画画\padmate`, true},
	{"the folder itself", `C:\a\padmate`, `D:\Apps\padmate`, `D:\Apps\padmate`, false},
	{"the folder itself, in capitals and with a backslash", `C:\a\padmate`, `D:\Apps\PADMATE\`, `D:\Apps\PADMATE`, false},
	{"a name that starts like the folder", `C:\a\padmate`, `D:\Apps\padmate-tools`, `D:\Apps\padmate-tools\padmate`, true},
	{"a folder inside a folder of that name", `C:\a\padmate`, `D:\padmate\tools`, `D:\padmate\tools\padmate`, true},
	{"a drive", `C:\a\padmate`, `D:\`, `D:\padmate`, true},
	{"a drive without a backslash", `C:\a\padmate`, `D:`, `D:\padmate`, true},
	{"a network folder", `C:\a\padmate`, `\\server\share`, `\\server\share\padmate`, true},
	{"quotes around a pasted path", `C:\a\padmate`, `"D:\我的 软件"`, `D:\我的 软件\padmate`, true},
	{"quotes and a backslash around the folder", `C:\a\padmate`, `"D:\Apps\padmate\"`, `D:\Apps\padmate`, false},
	{"spaces around the path", `C:\a\padmate`, `  D:\Apps  `, `D:\Apps\padmate`, true},
	{"nothing", `C:\a\padmate`, ``, ``, false},
	{"only spaces", `C:\a\padmate`, `   `, ``, false},
	{"only backslashes and quotes", `C:\a\padmate`, ` "\\" `, ``, false},
	{"nothing offered", ``, `D:\Apps`, `D:\Apps\padmate`, true},
}

func TestWizardDir(t *testing.T) {
	for _, c := range wizardCases {
		t.Run(c.name, func(t *testing.T) {
			dir, added := WizardDir(c.offered, c.typed)
			if dir != c.dir || added != c.added {
				t.Errorf("WizardDir(%q, %q) = %q, %v; want %q, %v", c.offered, c.typed, dir, added, c.dir, c.added)
			}
		})
	}
}

// What the wizard shows after the first Next is what the second Next accepts:
// the folder it gives is left alone when it is typed again (it ends with the
// folder name), and when it is offered.
func TestWizardDirIsStable(t *testing.T) {
	for _, c := range wizardCases {
		dir, _ := WizardDir(c.offered, c.typed)
		if dir == "" {
			continue
		}
		if again, added := WizardDir(c.offered, dir); again != dir || added {
			t.Errorf("%s: pressing Next again on %q gives %q, %v", c.name, dir, again, added)
		}
		if again, added := WizardDir(dir, dir); again != dir || added {
			t.Errorf("%s: %q offered and left alone gives %q, %v", c.name, dir, again, added)
		}
	}
}

// pathCase is a folder the page is about to use, and what the checks of the
// page say about it. TestWizardDirNSIS runs the same table through the NSIS
// code.
type pathCase struct {
	name, dir string
	full      bool
	fits      bool
}

var pathCases = []pathCase{
	{"a folder on a drive", `D:\Apps\padmate`, true, true},
	{"a drive", `D:`, true, true},
	{"a drive in lower case", `d:\x`, true, true},
	{"a drive with a backslash", `D:\`, true, true},
	{"Chinese and spaces", `D:\我的 软件\padmate`, true, true},
	{"a network folder", `\\server\share\padmate`, true, true},
	{"only a server", `\\server`, true, true},
	{"nothing", ``, false, true},
	{"two backslashes", `\\`, false, true},
	{"a bare name", `padmate`, false, true},
	{"a relative path", `Apps\padmate`, false, true},
	{"a path from the root of the current drive", `\Apps\padmate`, false, true},
	{"a path relative to the current folder of a drive", `D:Apps`, false, true},
	{"forward slashes", `D:/Apps/padmate`, false, true},
	{"a digit for a drive", `1:\x`, false, true},
	{"a second colon", `D:\a:b`, false, true},
	{"a question mark", `D:\Ap?ps`, false, true},
	{"an asterisk", `D:\Ap*ps`, false, true},
	{"a quote inside", `D:\Ap"ps`, false, true},
	{"an angle bracket", `D:\Ap<ps`, false, true},
	{"a bar", `D:\Ap|ps`, false, true},
	{"a colon in a network folder", `\\server\sha:re`, false, true},
	// The longest folder that fits leaves MaxRelativePath of the 259.
	{"the longest folder that fits", `D:\` + strings.Repeat("a", MaxPath-MaxRelativePath-3), true, true},
	{"one character too long", `D:\` + strings.Repeat("a", MaxPath-MaxRelativePath-2), true, false},
	// The length is in UTF-16 units, as NSIS counts, not in bytes.
	{"Chinese counts one character each", `D:\` + strings.Repeat("向", 70), true, true},
	{"Chinese too long", `D:\` + strings.Repeat("向", MaxPath-MaxRelativePath-2), true, false},
}

func TestIsFullPath(t *testing.T) {
	for _, c := range pathCases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsFullPath(c.dir); got != c.full {
				t.Errorf("IsFullPath(%q) = %v, want %v", c.dir, got, c.full)
			}
		})
	}
}

func TestFitsPath(t *testing.T) {
	for _, c := range pathCases {
		t.Run(c.name, func(t *testing.T) {
			if got := FitsPath(c.dir); got != c.fits {
				t.Errorf("FitsPath(%q) = %v, want %v", c.dir, got, c.fits)
			}
		})
	}
}
