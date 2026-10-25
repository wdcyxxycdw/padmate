package installerpkg

import (
	"crypto/sha512"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files of testdata/latest")

// describe is the golden form of a parse: the fields it read, or the error.
func describe(text string) string {
	info, err := ParseUpdateInfo(text)
	if err != nil {
		return "error: " + err.Error() + "\n"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "version=%q\npath=%q\nsha512=%q\nreleaseDate=%q\n", info.Version, info.Path, info.SHA512, info.ReleaseDate)
	for i, f := range info.Files {
		fmt.Fprintf(&b, "files[%d] url=%q sha512=%q size=%d\n", i, f.URL, f.SHA512, f.Size)
	}
	return b.String()
}

// Every testdata/latest/<case>.yml has a <case>.golden next to it with what the
// parser reads from it, or why it refuses it.
func TestParseUpdateInfoGolden(t *testing.T) {
	inputs, err := filepath.Glob(filepath.Join("testdata", "latest", "*.yml"))
	if err != nil || len(inputs) == 0 {
		t.Fatalf("no inputs: %v", err)
	}
	for _, input := range inputs {
		name := strings.TrimSuffix(filepath.Base(input), ".yml")
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(input)
			if err != nil {
				t.Fatal(err)
			}
			got := describe(string(data))
			golden := strings.TrimSuffix(input, ".yml") + ".golden"
			if *update {
				if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Errorf("got\n%s\nwant\n%s", got, want)
			}
		})
	}
}

func TestParseUpdateInfoAcceptsWindowsLineEnds(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "latest", "electron-builder.yml"))
	if err != nil {
		t.Fatal(err)
	}
	crlf := strings.ReplaceAll(string(data), "\n", "\r\n")
	if got, want := describe(crlf), describe(string(data)); got != want || strings.HasPrefix(got, "error") {
		t.Errorf("CRLF: %s\nLF: %s", got, want)
	}
}

func TestParseUpdateInfoRefusesWhatIsNotUTF8(t *testing.T) {
	if _, err := ParseUpdateInfo("version: 1.0.0\npath: \xff\n"); err == nil {
		t.Error("invalid UTF-8 was read")
	}
}

func TestScalar(t *testing.T) {
	for in, want := range map[string]string{
		"1.0.0":             "1.0.0",
		"'quoted'":          "quoted",
		"'it''s'":           "it's",
		`"double"`:          "double",
		"a-b c":             "a-b c",
		"-1":                "-1",
		"2026-10-24T05:3:0": "2026-10-24T05:3:0",
	} {
		if got, err := scalar(in); err != nil || got != want {
			t.Errorf("scalar(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "'open", "'a'b'", `"a\"b"`, `"open`, "[a]", "{a: b}", "&anchor", "*alias", "!tag", "|", ">", "%x", "@x", "`x", "?x", "#c", "- item", "a # comment", "key:"} {
		if got, err := scalar(in); err == nil {
			t.Errorf("scalar(%q) = %q, want an error", in, got)
		}
	}
}

func installerWith(t *testing.T, content string) (path, sum string, size int64) {
	t.Helper()
	path = filepath.Join(t.TempDir(), mustTag("v1.0.0").InstallerName())
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	h := sha512.Sum512([]byte(content))
	return path, base64.StdEncoding.EncodeToString(h[:]), int64(len(content))
}

func latestFor(name, sum string, size int64, version string) string {
	return fmt.Sprintf("version: %s\nfiles:\n  - url: %s\n    sha512: %s\n    size: %d\npath: %s\nsha512: %s\nreleaseDate: '2026-10-24T05:33:03.311Z'\n", version, name, sum, size, name, sum)
}

func TestCheckUpdateInfo(t *testing.T) {
	tag := mustTag("v1.0.0")
	installer, sum, size := installerWith(t, "installer bytes")
	name := tag.InstallerName()
	write := func(text string) string {
		p := filepath.Join(filepath.Dir(installer), UpdateFile)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := CheckUpdateInfo(write(latestFor(name, sum, size, "1.0.0")), installer, tag); len(p) != 0 {
		t.Errorf("a right latest.yml: %v", p)
	}
	for _, c := range []struct {
		name, text, want string
	}{
		{"another version", latestFor(name, sum, size, "1.0.1"), `version is "1.0.1", want "1.0.0"`},
		{"another file name", latestFor("padmate-setup.exe", sum, size, "1.0.0"), `files[0].url is "padmate-setup.exe"`},
		{"another hash", latestFor(name, "AAAA", size, "1.0.0"), "files[0].sha512 is \"AAAA\""},
		{"another size", latestFor(name, sum, size+1, "1.0.0"), fmt.Sprintf("files[0].size is %d, the installer has %d bytes", size+1, size)},
		{"no files", "version: 1.0.0\npath: " + name + "\nsha512: " + sum + "\n", "files is empty"},
		{"not a latest.yml", "version 1.0.0\n", "latest.yml line 1"},
	} {
		got := strings.Join(CheckUpdateInfo(write(c.text), installer, tag), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, got, c.want)
		}
	}
	// The top-level path and sha512 are held to the same account.
	text := strings.Replace(latestFor(name, sum, size, "1.0.0"), "path: "+name, "path: other.exe", 1)
	if got := strings.Join(CheckUpdateInfo(write(text), installer, tag), "\n"); !strings.Contains(got, `path is "other.exe"`) {
		t.Errorf("another path: %q", got)
	}
	if got := CheckUpdateInfo(filepath.Join(filepath.Dir(installer), "missing.yml"), installer, tag); len(got) != 1 || !strings.Contains(got[0], "missing.yml") {
		t.Errorf("a missing latest.yml: %v", got)
	}
	if got := CheckUpdateInfo(write(latestFor(name, sum, size, "1.0.0")), filepath.Join(filepath.Dir(installer), "gone.exe"), tag); len(got) != 1 {
		t.Errorf("a missing installer: %v", got)
	}
	// Two entries: electron-updater takes the first, but the release has one installer.
	two := latestFor(name, sum, size, "1.0.0") + "  - url: x\n"
	_ = two
	info, err := ParseUpdateInfo("version: 1.0.0\nfiles:\n  - url: " + name + "\n    sha512: " + sum + "\n    size: 1\n  - url: b.exe\n    sha512: B\n    size: 2\npath: " + name + "\nsha512: " + sum + "\n")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(checkUpdateInfo(info, tag, sum, 1), "\n"); !strings.Contains(got, "files has 2 entries") {
		t.Errorf("two entries: %q", got)
	}
}

// latestWithoutSize is latest.yml as electron-builder 26.17.0 writes it: the
// files entry has a url and a sha512 and nothing else (testdata/latest/no-size.yml).
func latestWithoutSize(name, sum, version string) string {
	return fmt.Sprintf("version: %s\nfiles:\n  - url: %s\n    sha512: %s\npath: %s\nsha512: %s\nreleaseDate: '2026-10-24T06:56:06.100Z'\n", version, name, sum, name, sum)
}

// electron-builder writes no size (PKG-10, measured on a real build): the check
// then has the version, the file name and the SHA-512 to go on, and all of them
// are still held to the installer. A size that is written is still compared.
func TestCheckUpdateInfoWithoutSize(t *testing.T) {
	tag := mustTag("v1.0.0")
	installer, sum, size := installerWith(t, "installer bytes")
	name := tag.InstallerName()
	write := func(text string) string {
		p := filepath.Join(filepath.Dir(installer), UpdateFile)
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := CheckUpdateInfo(write(latestWithoutSize(name, sum, "1.0.0")), installer, tag); len(p) != 0 {
		t.Errorf("a latest.yml without size, as electron-builder writes it: %v", p)
	}
	withSizeZero := strings.Replace(latestWithoutSize(name, sum, "1.0.0"), "    sha512: "+sum+"\npath", "    sha512: "+sum+"\n    size: 0\npath", 1)
	for _, c := range []struct {
		name, text, want string
	}{
		{"another version", latestWithoutSize(name, sum, "1.0.1"), `version is "1.0.1", want "1.0.0"`},
		{"another file name", latestWithoutSize("padmate-setup.exe", sum, "1.0.0"), `files[0].url is "padmate-setup.exe"`},
		{"another hash", latestWithoutSize(name, "AAAA", "1.0.0"), `files[0].sha512 is "AAAA"`},
		{"another top-level path", strings.Replace(latestWithoutSize(name, sum, "1.0.0"), "path: "+name, "path: other.exe", 1), `path is "other.exe"`},
		{"a size of 0 is a size", withSizeZero, fmt.Sprintf("files[0].size is 0, the installer has %d bytes", size)},
	} {
		got := strings.Join(CheckUpdateInfo(write(c.text), installer, tag), "\n")
		if !strings.Contains(got, c.want) {
			t.Errorf("%s: %q does not contain %q", c.name, got, c.want)
		}
	}
	// The same through the parsed form: an absent size is not a size of 0.
	info, err := ParseUpdateInfo(latestWithoutSize(name, sum, "1.0.0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Files[0].Size; got >= 0 {
		t.Errorf("a missing size reads as %d, want a negative number (absent)", got)
	}
	if p := checkUpdateInfo(info, tag, sum, size); len(p) != 0 {
		t.Errorf("checkUpdateInfo without a size: %v", p)
	}
	// A size that is written but wrong is still a problem.
	info.Files[0].Size = size + 1
	if p := strings.Join(checkUpdateInfo(info, tag, sum, size), "\n"); !strings.Contains(p, "files[0].size") {
		t.Errorf("a wrong size was accepted: %q", p)
	}
}

func FuzzParseUpdateInfo(f *testing.F) {
	for _, name := range []string{"electron-builder", "no-size", "with-block-map", "two-files", "tab", "flow-files", "duplicate-key"} {
		data, err := os.ReadFile(filepath.Join("testdata", "latest", name+".yml"))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(string(data))
	}
	f.Fuzz(func(t *testing.T, text string) {
		info, err := ParseUpdateInfo(text)
		if err != nil {
			return
		}
		// What was read can be checked.
		_ = checkUpdateInfo(info, mustTag("v1.0.0"), "x", 1)
	})
}
