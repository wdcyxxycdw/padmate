package installerpkg

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/authenticode"
)

// The fixtures of internal/authenticode: a 1 KB PE file unsigned, and signed
// by "CN=padmate fixture, O=padmate contributors, C=JP" with SHA-256 and no
// timestamp.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "authenticode", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func verdict(v authenticode.Verdict) func(string) (authenticode.Verdict, error) {
	return func(string) (authenticode.Verdict, error) { return v, nil }
}

func joined(problems []string) string { return strings.Join(problems, "\n") }

func TestCheckSignedFile(t *testing.T) {
	dir := t.TempDir()
	signed, unsigned := filepath.Join(dir, "signed.exe"), filepath.Join(dir, "unsigned.exe")
	for path, name := range map[string]string{signed: "signed.pe", unsigned: "unsigned.pe"} {
		if err := os.WriteFile(path, fixture(t, name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	valid := verdict(authenticode.Valid)
	for _, c := range []struct {
		name string
		path string
		opts SignOptions
		want string // "" for a pass
	}{
		{"right publisher, no timestamp wanted", signed, SignOptions{Subject: "padmate fixture", Verify: valid}, ""},
		{"right distinguished name", signed, SignOptions{Subject: "CN=padmate fixture, O=padmate contributors, C=JP", Verify: valid}, ""},
		{"a timestamp is wanted", signed, SignOptions{Subject: "padmate fixture", Timestamp: true, Verify: valid}, "has no timestamp"},
		{"another publisher", signed, SignOptions{Subject: "someone else", Verify: valid}, `is signed by "CN=padmate fixture`},
		{"not signed", unsigned, SignOptions{Subject: "padmate fixture", Verify: valid}, "is not signed"},
		{"untrusted root refused", signed, SignOptions{Subject: "padmate fixture", Verify: verdict(authenticode.UntrustedRoot)}, "Windows says: signed, but the certificate chain does not end in a trusted root"},
		{"untrusted root allowed", signed, SignOptions{Subject: "padmate fixture", AllowUntrustedRoot: true, Verify: verdict(authenticode.UntrustedRoot)}, ""},
		{"changed file never allowed", signed, SignOptions{Subject: "padmate fixture", AllowUntrustedRoot: true, Verify: verdict(authenticode.BadDigest)}, "Windows says: the file was changed after it was signed"},
		{"windows cannot judge", signed, SignOptions{Subject: "padmate fixture", Verify: func(string) (authenticode.Verdict, error) { return authenticode.Other, errors.New("boom") }}, "Windows could not judge the signature: boom"},
		{"missing file", filepath.Join(dir, "missing.exe"), SignOptions{Subject: "padmate fixture", Verify: valid}, "signature:"},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := joined(CheckSignedFile(c.path, c.opts))
			switch {
			case c.want == "" && got != "":
				t.Errorf("problems: %s", got)
			case c.want != "" && !strings.Contains(got, c.want):
				t.Errorf("problems %q, want one with %q", got, c.want)
			}
		})
	}
	// An unsigned file is reported once, without Windows' verdict as well.
	if got := CheckSignedFile(unsigned, SignOptions{Subject: "x", Verify: verdict(authenticode.NotSigned)}); len(got) != 1 {
		t.Errorf("unsigned: %v", got)
	}
}

// signedAppUpdate is the resources/app-update.yml of a signed build, naming the
// publisher of the fixture.
const signedAppUpdate = "provider: github\nowner: wdcyxxycdw\nrepo: padmate\nupdaterCacheDirName: padmate-updater\npublisherName:\n  - CN=padmate fixture, O=padmate contributors, C=JP\n"

// tree lays out an installation of 1 KB files; signedNames get the signature.
// resources/app-update.yml is the one a signed build has, naming the publisher
// of the fixture.
func tree(t *testing.T, signedNames ...string) (root, installer string) {
	t.Helper()
	dir := t.TempDir()
	root = filepath.Join(dir, "win-unpacked")
	put := func(path, fixtureName string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, fixture(t, fixtureName), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range append(slices.Clone(SignedFiles), "resources/bin/SDL3.dll") {
		kind := "unsigned.pe"
		if slices.Contains(signedNames, name) || slices.Contains(signedNames, "*") && name != "resources/bin/SDL3.dll" {
			kind = "signed.pe"
		}
		put(filepath.Join(root, filepath.FromSlash(name)), kind)
	}
	update := filepath.Join(root, "resources", "app-update.yml")
	if err := os.MkdirAll(filepath.Dir(update), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(update, []byte(signedAppUpdate), 0o644); err != nil {
		t.Fatal(err)
	}
	installer = filepath.Join(dir, "padmate-v1.0.0-windows-amd64-setup.exe")
	put(installer, "signed.pe")
	if slices.Contains(signedNames, "unsigned installer") {
		put(installer, "unsigned.pe")
	}
	return root, installer
}

func TestCheckSignatures(t *testing.T) {
	opts := SignOptions{Subject: "padmate fixture", Verify: verdict(authenticode.Valid)}
	root, installer := tree(t, "*")
	if got := CheckSignatures(root, installer, opts); len(got) != 0 {
		t.Errorf("a signed tree: %v", got)
	}

	// One file unsigned, and the installer unsigned: both are named.
	root, installer = tree(t, SignedFiles[0], SignedFiles[1], "unsigned installer")
	problems := CheckSignatures(root, installer, opts)
	got := joined(problems)
	if len(problems) != 2 {
		t.Errorf("%d problems, want 2: %q", len(problems), got)
	}
	for _, want := range []string{"resources/bin/padmatectl.exe: is not signed", "padmate-v1.0.0-windows-amd64-setup.exe: is not signed"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q is not in %q", want, got)
		}
	}

	// SDL3.dll is shipped as it is.
	root, installer = tree(t, "*")
	if err := os.WriteFile(filepath.Join(root, "resources", "bin", "SDL3.dll"), fixture(t, "signed.pe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := joined(CheckSignatures(root, installer, opts)); !strings.Contains(got, "resources/bin/SDL3.dll: is signed") {
		t.Errorf("a signed SDL3.dll: %q", got)
	}
}

func TestCheckSignaturesReadsPublisherName(t *testing.T) {
	opts := SignOptions{Subject: "padmate fixture", Verify: verdict(authenticode.Valid)}
	root, installer := tree(t, "*")
	update := filepath.Join(root, "resources", "app-update.yml")
	write := func(text string) {
		t.Helper()
		if err := os.WriteFile(update, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(update); err != nil {
		t.Fatal(err)
	}
	// Since the publish block of electron-builder.yml (PKG-10) every build has the
	// file. Without it the updater has no publisher to compare a downloaded
	// update's signer with, so a signed build that lacks it is a mistake.
	if got := joined(CheckSignatures(root, installer, opts)); !strings.Contains(got, "resources/app-update.yml: ") {
		t.Errorf("no app-update.yml: %q", got)
	}
	for name, c := range map[string]struct {
		text string
		want string // "" for a pass
	}{
		"a list with the common name": {"owner: x\nrepo: y\nprovider: github\npublisherName:\n  - padmate fixture\nupdaterCacheDirName: padmate-updater\n", ""},
		"a distinguished name":        {"publisherName:\n  - CN=padmate fixture, O=padmate contributors, C=JP\n", ""},
		"a scalar":                    {"publisherName: 'padmate fixture'\n", ""},
		"CRLF":                        {"provider: github\r\npublisherName:\r\n  - padmate fixture\r\n", ""},
		"not there":                   {"owner: x\nrepo: y\nprovider: github\nupdaterCacheDirName: padmate-updater\n", "has no publisherName"},
		"empty":                       {"publisherName:\nupdaterCacheDirName: x\n", "has no publisherName"},
		"another publisher":           {"publisherName:\n  - someone else\n", `publisherName "someone else" does not name the installer's signer`},
		"in a later key":              {"publisherName:\n  - someone else\nlater:\n  - padmate fixture\n", "does not name the installer's signer"},
		// electron-updater compares exactly, and only the attributes listed.
		"a subset of the distinguished name": {"publisherName:\n  - CN=padmate fixture\n", ""},
		"one name of several is enough":      {"publisherName:\n  - someone else\n  - padmate fixture\n", ""},
		"another case":                       {"publisherName:\n  - Padmate Fixture\n", "does not name the installer's signer"},
		"another case in a value":            {"publisherName:\n  - CN=padmate fixture, O=Padmate Contributors, C=JP\n", "does not name the installer's signer"},
		"a lower case key":                   {"publisherName:\n  - cn=padmate fixture, O=padmate contributors, C=JP\n", "does not name the installer's signer"},
		"spaces inside quotes are kept":      {"publisherName:\n  - ' padmate fixture '\n", "does not name the installer's signer"},
		"a state the signer does not have":   {"publisherName:\n  - CN=padmate fixture, ST=Tokyo\n", "does not name the installer's signer"},
		"an attribute the signer lacks":      {"publisherName:\n  - CN=padmate fixture, L=Tokyo\n", "does not name the installer's signer"},
	} {
		write(c.text)
		got := joined(CheckSignatures(root, installer, opts))
		switch {
		case c.want == "" && got != "":
			t.Errorf("%s: %s", name, got)
		case c.want != "" && !strings.Contains(got, c.want):
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

// The failure shows the signer the way Windows writes it, which is the form
// to copy into PADMATE_SIGN_SUBJECT.
func TestCheckSignaturesShowsTheSignerAsWindowsWritesIt(t *testing.T) {
	opts := SignOptions{Subject: "padmate fixture", Verify: verdict(authenticode.Valid)}
	root, installer := tree(t, "*")
	if err := os.WriteFile(filepath.Join(root, "resources", "app-update.yml"), []byte("publisherName: Padmate Fixture\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := joined(CheckSignatures(root, installer, opts))
	if !strings.Contains(got, `signer "CN=padmate fixture, O=padmate contributors, C=JP"`) {
		t.Errorf("the signer in Windows form is not in %q", got)
	}
}

func TestPublisherNames(t *testing.T) {
	for in, want := range map[string][]string{
		// A quoted scalar is literal: the quotes' own escapes, nothing trimmed.
		"publisherName: ' a '\n":                      {" a "},
		"publisherName: 'O''Brien'\n":                 {"O'Brien"},
		"publisherName:\n  - 'CN=O''Brien, O=x'\n":    {"CN=O'Brien, O=x"},
		`publisherName: "CN=\"a, b\", O=x"` + "\n":    {`CN="a, b", O=x`},
		`publisherName: "a\\b"` + "\n":                {`a\b`},
		"publisherName:\n  - a\n  - 'b c'\nx: y\n":    {"a", "b c"},
		"publisherName: \"CN=a, O=b\"\n":              {"CN=a, O=b"},
		"publisherName:\n- a\n":                       {"a"},
		"# publisherName:\nowner: x\n":                nil,
		"owner: x\n":                                  nil,
		"publisherName:\n  - a\nother:\n  - not me\n": {"a"},
	} {
		if got := publisherNames(in); !slices.Equal(got, want) {
			t.Errorf("publisherNames(%q) = %q, want %q", in, got, want)
		}
	}
}

// The two kinds of build write two kinds of resources\app-update.yml (PKG-09,
// PKG-10): the unsigned one has the source and the cache folder only, the
// signed one adds publisherName as a list. CheckTree reads the source and does
// not need publisherName (an unsigned build verifies); -signed is the check
// that needs it, and both read the very same text.
func TestAppUpdateYMLOfUnsignedAndSignedBuilds(t *testing.T) {
	unsigned := "provider: github\nowner: wdcyxxycdw\nrepo: padmate\nupdaterCacheDirName: padmate-updater\n"
	signed := unsigned + "publisherName:\n  - CN=padmate fixture, O=padmate contributors, C=JP\n"
	opts := SignOptions{Subject: "padmate fixture", Verify: verdict(authenticode.Valid)}
	for _, c := range []struct {
		name, text     string
		treeOK, signOK bool
	}{
		{"unsigned build", unsigned, true, false},
		{"signed build", signed, true, true},
	} {
		root, installer := tree(t, "*")
		update := filepath.Join(root, "resources", "app-update.yml")
		if err := os.WriteFile(update, []byte(c.text), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := joined(updateConfigProblems(update)); (got == "") != c.treeOK {
			t.Errorf("%s: the update source check says %q, want ok=%v", c.name, got, c.treeOK)
		}
		got := joined(CheckSignatures(root, installer, opts))
		if (got == "") != c.signOK {
			t.Errorf("%s: -signed says %q, want ok=%v", c.name, got, c.signOK)
		}
		if !c.signOK && !strings.Contains(got, "has no publisherName") {
			t.Errorf("%s: -signed does not name the missing publisherName: %q", c.name, got)
		}
	}
}
