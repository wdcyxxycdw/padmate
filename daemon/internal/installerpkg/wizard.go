package installerpkg

import (
	"strings"
	"unicode/utf16"
)

// FolderName is the name of the folder padmate is installed in: the install
// folder is a folder with this name (app/build/padmate-wizard.nsh, ADR-0068).
const FolderName = ProductName

// MaxRelativePath is the length the wizard allows for the longest path inside
// an installation, the backslash in front of it included: the install folder
// and this must fit in a Windows path (260 characters with the end mark), as
// the installer writes its files without the \\?\ prefix. CheckTree holds the
// installed files to it, and padmate-wizard.nsh defines the same number
// (PADMATE_MAX_REL; TestWizardScriptDefinesMaxRelativePath keeps them equal).
const MaxRelativePath = 140

// MaxPath is the longest path Windows takes without the \\?\ prefix, the end
// mark not counted.
const MaxPath = 259

// WizardDir is the folder the installation wizard installs into (PKG-13).
// offered is the folder its page started with, typed the text of the page's
// field when Next is pressed. added reports that the folder name was added to
// what was typed: the wizard then shows the folder and waits for another Next.
//
// The folder is what was typed when its last component is FolderName (any
// case), and the folder FolderName inside what was typed otherwise. A field
// left as the page offered it is used as it is (an upgrade keeps the folder of
// the old installation, whatever it is called). Spaces and quotes around the
// text, a pasted path often has some, and backslashes at its end do not count.
// An empty field gives an empty folder: the wizard stays on the page.
//
// This is the function app/build/padmate-wizard.nsh implements in NSIS;
// TestWizardDirNSIS compiles that and compares the two.
func WizardDir(offered, typed string) (dir string, added bool) {
	t := trimPath(typed)
	if t == "" || strings.EqualFold(t, trimPath(offered)) {
		return t, false
	}
	last := t
	if i := strings.LastIndexByte(t, '\\'); i >= 0 {
		last = t[i+1:]
	}
	if strings.EqualFold(last, FolderName) {
		return t, false
	}
	return t + `\` + FolderName, true
}

// trimPath cuts spaces and quotes off both ends of a path and backslashes off
// its end, so that `D:\Apps\` and ` "D:\Apps" ` name the same folder. A drive
// `D:\` becomes `D:`.
func trimPath(s string) string {
	return strings.TrimLeft(strings.TrimRight(s, `\ "`), ` "`)
}

// IsFullPath reports whether dir names a folder by a drive letter (D:, D:\x)
// or a network share (\\server), and holds none of the characters Windows
// refuses in a name. The folder page checks the folder it is about to use with
// it, so that a relative or a mistyped path gets a message instead of a failure
// halfway through the installation. app/build/padmate-wizard.nsh implements it
// in NSIS (PadmateIsFullPath); TestWizardDirNSIS compares the two.
func IsFullPath(dir string) bool {
	var rest string
	switch {
	case strings.HasPrefix(dir, `\\`):
		if rest = dir[2:]; rest == "" {
			return false
		}
	case len(dir) >= 2 && dir[1] == ':' && isASCIILetter(dir[0]):
		if rest = dir[2:]; rest != "" && rest[0] != '\\' {
			return false
		}
	default:
		return false
	}
	return !strings.ContainsAny(rest, `/:*?"<>|`)
}

func isASCIILetter(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

// FitsPath reports whether the longest file of an installation, MaxRelativePath
// long, fits in dir: the wizard refuses a longer folder, as the installation
// would fail halfway through (and after the old version was removed). It is
// what PadmateFitsPath does in NSIS.
func FitsPath(dir string) bool {
	return len(utf16.Encode([]rune(dir)))+MaxRelativePath <= MaxPath
}
