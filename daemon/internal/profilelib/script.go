package profilelib

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

// MaxScriptBytes bounds one script file that WriteScript writes into a
// profile folder: a hand-written source file, not a bundle or an asset.
const MaxScriptBytes = 1 << 20

// replaceAttempts and replaceRetry bound how long WriteScript retries a
// replacement that Windows refuses because a reader, such as the script
// watcher or a build, holds the old file open without FILE_SHARE_DELETE
// (PITFALLS "rename 替换文件").
const (
	replaceAttempts = 10
	replaceRetry    = 20 * time.Millisecond
)

// ScriptExistsError refuses to write a script over an existing name
// without replace.
type ScriptExistsError struct{ ID, Name string }

func (e *ScriptExistsError) Error() string {
	return fmt.Sprintf("profile library: %s/%s already exists", e.ID, e.Name)
}

// CheckScriptName accepts the bare file name of a script that a client
// writes into a profile folder: 1..64 ASCII letters, digits, '.', '-' or
// '_', not starting with a dot and without "..", ending in .ts or .js
// (lowercase) but not a .d.ts declaration file. Its stem before the first
// dot must not be a Windows device name, which Windows opens as the device
// whatever the extension ("con.ts"). profile.json and the other library
// files are excluded by the extension.
func CheckScriptName(name string) error {
	if name == "" || len(name) > 64 {
		return errors.New("script name must be 1 to 64 characters")
	}
	for _, c := range name {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_' {
			continue
		}
		return errors.New("script name may contain only ASCII letters, digits, '.', '-' and '_'")
	}
	switch {
	case strings.HasPrefix(name, "."):
		return errors.New("script name must not start with a dot")
	case strings.Contains(name, ".."):
		return errors.New("script name must not contain \"..\"")
	case !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".js"):
		return errors.New("script name must end in .ts or .js")
	case strings.HasSuffix(strings.ToLower(name), ".d.ts"):
		return errors.New("a .d.ts declaration file cannot be a script")
	}
	if stem, _, _ := strings.Cut(name, "."); isDeviceName(strings.ToLower(stem)) {
		return fmt.Errorf("script name %q is reserved by Windows", name)
	}
	return nil
}

func isDeviceName(stem string) bool {
	switch stem {
	case "con", "prn", "aux", "nul":
		return true
	}
	return len(stem) == 4 && (strings.HasPrefix(stem, "com") || strings.HasPrefix(stem, "lpt")) && stem[3] >= '0' && stem[3] <= '9'
}

// Folder returns the absolute path of the existing folder profiles/<id> for
// a client that shows it to the user, such as the UI opening it in
// Explorer. Like Remove and Copy it refuses a folder that is a link or
// junction (UnsupportedError) instead of naming its target. A missing folder
// is fs.ErrNotExist. Files are written through UpdateFolder, never by path.
func (l *Library) Folder(id string) (string, error) {
	if !ValidID(id) {
		return "", fmt.Errorf("profile library: invalid id %q", id)
	}
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return "", err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, id); err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(l.dir, "profiles", id))
}

// UpdateFolder runs write with a root confined to the existing folder
// profiles/<id>, under the library's writer lock, so it is atomic with Put,
// Remove and Copy's reads: a delete cannot move the folder halfway through.
// It never creates the profile: a missing folder is fs.ErrNotExist, and a
// folder that is a link or junction is an UnsupportedError rather than a
// way into another folder. The root keeps every write below the folder.
func (l *Library) UpdateFolder(id string, write func(folder *os.Root) error) error {
	if !ValidID(id) {
		return fmt.Errorf("profile library: invalid id %q", id)
	}
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	profiles, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return err
	}
	defer profiles.Close()
	if err := checkFolder(profiles, id); err != nil {
		return err
	}
	folder, err := profiles.OpenRoot(id)
	if err != nil {
		return err
	}
	defer folder.Close()
	return write(folder)
}

func checkFolder(profiles *os.Root, id string) error {
	info, err := profiles.Lstat(id)
	if err != nil {
		return err
	}
	if info.Mode().Type() != fs.ModeDir {
		return &UnsupportedError{Path: id, Reason: "is a link or special file, not a profile folder"}
	}
	return nil
}

// WriteScript writes the script file name, checked by CheckScriptName, with
// data of at most MaxScriptBytes of UTF-8 into folder, a profile folder from
// UpdateFolder; id names the folder in errors. A new file is created
// exclusively. An existing regular file is a ScriptExistsError without
// replace; with replace it is replaced atomically: data goes to a temporary
// file in the same folder, synced, and is renamed over the old file, which
// stays as it was if anything fails. An existing link, folder or special
// file is an UnsupportedError either way, never replaced or followed, so a
// client does not offer to replace it. WriteScript reports whether it
// replaced a file.
func WriteScript(folder *os.Root, id, name string, data []byte, replace bool) (bool, error) {
	if err := CheckScriptName(name); err != nil {
		return false, err
	}
	if len(data) > MaxScriptBytes {
		return false, fmt.Errorf("profile library: script exceeds %d bytes", MaxScriptBytes)
	}
	if !utf8.Valid(data) {
		return false, errors.New("profile library: script is not valid UTF-8")
	}
	info, err := folder.Lstat(name)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return false, createScript(folder, id, name, data)
	case err != nil:
		return false, err
	case !info.Mode().IsRegular():
		return false, &UnsupportedError{Path: id + "/" + name, Reason: "is a link, folder or special file, not a script file"}
	case !replace:
		return false, &ScriptExistsError{ID: id, Name: name}
	}
	for attempt := 1; ; attempt++ {
		err = atomicReplace(folder, name, data)
		if err == nil || !errors.Is(err, fs.ErrPermission) || attempt == replaceAttempts {
			return err == nil, err
		}
		time.Sleep(replaceRetry)
	}
}

// createScript creates name exclusively and syncs it; a failed write
// removes the partial file.
func createScript(folder *os.Root, id, name string, data []byte) error {
	out, err := folder.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return &ScriptExistsError{ID: id, Name: name}
	}
	if err != nil {
		return err
	}
	_, err = out.Write(data)
	if err == nil {
		err = out.Sync()
	}
	if err := errors.Join(err, out.Close()); err != nil {
		if removeErr := folder.Remove(name); removeErr != nil {
			err = errors.Join(err, removeErr)
		}
		return err
	}
	return nil
}
