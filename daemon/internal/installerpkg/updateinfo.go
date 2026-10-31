package installerpkg

import (
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

// UpdateFile is the file electron-builder writes for electron-updater next to
// the installer (always latest.yml, even for a release candidate, because the
// publish block pins channel: latest, UpdateChannel) and that a release
// uploads to the download site, last, after the installer and its .sha256
// (PKG-10, ADR-0078). It is the contract with the Worker that redirects the
// address of the site to the installer it names (its path), and with the
// application that reads it.
const UpdateFile = "latest.yml"

// UpdateInfo is what latest.yml says about the release.
type UpdateInfo struct {
	Version     string
	Path        string
	SHA512      string
	ReleaseDate string
	Files       []UpdateInfoFile
}

// UpdateInfoFile is one entry of the files list.
type UpdateInfoFile struct {
	URL    string
	SHA512 string
	// Size is the size in bytes, or negative when the entry has none:
	// electron-builder 26.17.0 writes no size for the nsis target (measured on
	// a build with the github provider, PKG-10; the generic provider goes
	// through the same createUpdateInfo, which gives the same shape and only
	// rewrites the url of the files entry for github), only a differential
	// package adds one.
	Size int64
}

// ParseUpdateInfo reads a latest.yml as electron-builder writes it: a few
// plain `key: value` lines and a `files:` list of `- url:` entries. It is not
// a YAML parser. It accepts only that shape, rejects what it does not
// understand in the lines it needs (flow collections, anchors, block scalars,
// tabs, duplicate keys, an unterminated quote), and skips the indented lines
// below a key it does not know (releaseNotes).
func ParseUpdateInfo(text string) (UpdateInfo, error) {
	if !utf8.ValidString(text) {
		return UpdateInfo{}, errors.New("latest.yml is not UTF-8")
	}
	var info UpdateInfo
	seen := map[string]bool{}
	skipping := false // inside the indented body of a key that is not known
	inFiles := false
	var cur *UpdateInfoFile
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	for n, line := range lines {
		fail := func(format string, args ...any) (UpdateInfo, error) {
			return UpdateInfo{}, fmt.Errorf("latest.yml line %d: %s", n+1, fmt.Sprintf(format, args...))
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if strings.Contains(line, "\t") && !skipping {
			return fail("a tab")
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		body := line[indent:]
		if indent == 0 {
			skipping, inFiles, cur = false, false, nil
			key, value, ok := strings.Cut(body, ":")
			if !ok || key == "" || strings.ContainsAny(key, " \"'[]{}&*!|>%@`") || (value != "" && value[0] != ' ') {
				return fail("expected `key: value`, got %q", body)
			}
			value = strings.TrimSpace(value)
			if seen[key] {
				return fail("the key %s twice", key)
			}
			seen[key] = true
			switch key {
			case "files":
				if value != "" {
					return fail("files must be a list on the lines below")
				}
				inFiles = true
			case "version", "path", "sha512", "releaseDate":
				s, err := scalar(value)
				if err != nil {
					return fail("%s: %v", key, err)
				}
				switch key {
				case "version":
					info.Version = s
				case "path":
					info.Path = s
				case "sha512":
					info.SHA512 = s
				case "releaseDate":
					info.ReleaseDate = s
				}
			default:
				// A key padmate does not read (releaseNotes, ...): its value and
				// any indented lines below it are not looked at.
				skipping = true
			}
			continue
		}
		if skipping {
			continue
		}
		if !inFiles {
			return fail("indented line outside the files list: %q", body)
		}
		if rest, ok := strings.CutPrefix(body, "- "); ok {
			if indent != 2 {
				return fail("a files entry must be indented by two spaces")
			}
			info.Files = append(info.Files, UpdateInfoFile{Size: -1})
			cur = &info.Files[len(info.Files)-1]
			body = strings.TrimLeft(rest, " ")
		} else if cur == nil || indent != 4 {
			return fail("expected a `- url: ...` entry, got %q", body)
		}
		key, value, ok := strings.Cut(body, ":")
		if !ok || (value != "" && value[0] != ' ') {
			return fail("expected `key: value`, got %q", body)
		}
		s, err := scalar(strings.TrimSpace(value))
		if err != nil {
			return fail("%s: %v", key, err)
		}
		switch key {
		case "url":
			cur.URL = s
		case "sha512":
			cur.SHA512 = s
		case "size":
			size, err := strconv.ParseInt(s, 10, 64)
			if err != nil || size < 0 {
				return fail("size %q is not a number of bytes", s)
			}
			cur.Size = size
		case "blockMapSize", "isAdminRightsRequired":
		default:
			return fail("unknown key %s in a files entry", key)
		}
	}
	return info, nil
}

// scalar reads a plain, 'single-quoted' or "double-quoted" YAML scalar that
// holds no escape.
func scalar(v string) (string, error) {
	if v == "" {
		return "", errors.New("no value")
	}
	switch v[0] {
	case '\'':
		if len(v) < 2 || v[len(v)-1] != '\'' {
			return "", errors.New("unterminated quote")
		}
		inner := v[1 : len(v)-1]
		if strings.Contains(strings.ReplaceAll(inner, "''", ""), "'") {
			return "", errors.New("unterminated quote")
		}
		return strings.ReplaceAll(inner, "''", "'"), nil
	case '"':
		if len(v) < 2 || v[len(v)-1] != '"' || strings.ContainsAny(v[1:len(v)-1], `"\`) {
			return "", errors.New("unterminated quote or an escape")
		}
		return v[1 : len(v)-1], nil
	case '[', '{', '&', '*', '!', '|', '>', '%', '@', '`', '?', '#':
		return "", fmt.Errorf("%q is not a plain value", v)
	case '-':
		if len(v) > 1 && v[1] == ' ' {
			return "", fmt.Errorf("%q is not a plain value", v)
		}
	}
	if strings.Contains(v, " #") || strings.HasSuffix(v, ":") {
		return "", fmt.Errorf("%q is not a plain value", v)
	}
	return v, nil
}

// CheckUpdateInfo checks the latest.yml of a release against its installer:
// the version, the file name and the SHA-512 (base64), which is what
// electron-updater trusts when the installer has no signature to check, and
// the size when the file states one (electron-builder's own does not).
func CheckUpdateInfo(latest, installer string, tag Tag) []string {
	data, err := os.ReadFile(latest)
	if err != nil {
		return []string{UpdateFile + ": " + err.Error()}
	}
	info, err := ParseUpdateInfo(string(data))
	if err != nil {
		return []string{err.Error()}
	}
	f, err := os.Open(installer)
	if err != nil {
		return []string{UpdateFile + ": " + err.Error()}
	}
	defer func() { _ = f.Close() }()
	hash := sha512.New()
	size, err := io.Copy(hash, f)
	if err != nil {
		return []string{filepath.Base(installer) + ": " + err.Error()}
	}
	return checkUpdateInfo(info, tag, base64.StdEncoding.EncodeToString(hash.Sum(nil)), size)
}

// checkUpdateInfo compares info with the facts about the installer.
func checkUpdateInfo(info UpdateInfo, tag Tag, sum string, size int64) []string {
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, UpdateFile+": "+fmt.Sprintf(format, args...))
	}
	name := tag.InstallerName()
	if info.Version != tag.App {
		add("version is %q, want %q", info.Version, tag.App)
	}
	if info.Path != name {
		add("path is %q, want %q", info.Path, name)
	}
	if info.SHA512 != sum {
		add("sha512 is %q, the installer's is %q", info.SHA512, sum)
	}
	if len(info.Files) == 0 {
		add("files is empty")
		return problems
	}
	file := info.Files[0]
	if file.URL != name {
		add("files[0].url is %q, want %q", file.URL, name)
	}
	if file.SHA512 != sum {
		add("files[0].sha512 is %q, the installer's is %q", file.SHA512, sum)
	}
	// A size that is not there is not compared; one that is there must be right
	// (a size of 0 is a size).
	if file.Size >= 0 && file.Size != size {
		add("files[0].size is %d, the installer has %d bytes", file.Size, size)
	}
	if len(info.Files) > 1 {
		add("files has %d entries, want the installer only", len(info.Files))
	}
	return problems
}
