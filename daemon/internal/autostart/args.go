package autostart

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const referenceFlag = "--startup-id="
const maxArgumentBytes = 1024 * 1024

type argumentFile struct {
	Version    int      `json:"version"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

func startupDirectory() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dir) {
		return "", errors.New("autostart: user config directory is not absolute")
	}
	return filepath.Join(dir, "padmate", "startup"), nil
}

func commandUnits(command string) int { return len(utf16.Encode([]rune(command))) }
func isReference(arg string) bool {
	return strings.HasPrefix(arg, "--startup-id") || strings.HasPrefix(arg, "-startup-id")
}
func validateArguments(exe string, args []string) error {
	if !utf8.ValidString(exe) {
		return errors.New("autostart: invalid UTF8 executable")
	}
	if !filepath.IsAbs(exe) {
		return errors.New("autostart: executable must be absolute")
	}
	if _, err := Command(exe, args); err != nil {
		return err
	}
	for _, arg := range args {
		if !utf8.ValidString(arg) {
			return errors.New("autostart: invalid UTF8 argument")
		}
		if isReference(arg) {
			return errors.New("autostart: recursive startup reference")
		}
	}
	return nil
}
func encodeArguments(doc argumentFile) ([]byte, string, error) {
	data, err := json.Marshal(doc)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func (e *Entry) setReference() error {
	if e.dir == "" {
		return errors.New("autostart: user startup directory unavailable")
	}
	data, id, err := encodeArguments(argumentFile{Version: 1, Executable: e.exe, Args: e.args})
	if err != nil {
		return err
	}
	if len(data) > maxArgumentBytes {
		return errors.New("autostart: arguments exceed file size limit")
	}
	command, err := Command(e.exe, []string{referenceFlag + id})
	if err != nil {
		return err
	}
	if commandUnits(command) > 260 {
		return errors.New("autostart: executable path plus startup reference exceeds 260 UTF16 characters")
	}
	if err = os.MkdirAll(e.dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(e.dir, id+".json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	created := err == nil
	switch {
	case errors.Is(err, os.ErrExist):
		existing, readErr := readArguments(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(existing, data) {
			return errors.New("autostart: existing argument file differs")
		}
	case err != nil:
		return err
	default:
		n, writeErr := f.Write(data)
		if writeErr == nil && n != len(data) {
			writeErr = io.ErrShortWrite
		}
		syncErr := f.Sync()
		closeErr := f.Close()
		if err = errors.Join(writeErr, syncErr, closeErr); err != nil {
			_ = os.Remove(path)
			return err
		}
	}
	if err = e.store.write(command); err != nil {
		if created {
			return errors.Join(err, os.Remove(path))
		}
		return err
	}
	e.command = command
	e.referenced = true
	return nil
}

func readArguments(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxArgumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxArgumentBytes {
		return nil, errors.New("autostart: argument file too large")
	}
	return data, nil
}

// Expand restores a dedicated startup reference before normal flag validation.
// It only reads the fixed user directory and never writes files or the registry.
func Expand(args []string, exe string) ([]string, error) {
	found := false
	for _, arg := range args {
		found = found || isReference(arg)
	}
	if !found {
		return args, nil
	}
	dir, err := startupDirectory()
	if err != nil {
		return nil, err
	}
	return expandAt(args, exe, dir)
}

func expandAt(args []string, exe, dir string) ([]string, error) {
	id, err := referenceID(args)
	if err != nil {
		return nil, err
	}
	data, err := readArguments(filepath.Join(dir, id+".json"))
	if err != nil {
		return nil, fmt.Errorf("autostart: read reference: %w", err)
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != id {
		return nil, errors.New("autostart: argument file digest mismatch")
	}
	var doc argumentFile
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&doc); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("autostart: trailing JSON data")
	}
	if doc.Version != 1 {
		return nil, errors.New("autostart: unsupported argument file version")
	}
	if !strings.EqualFold(filepath.Clean(doc.Executable), filepath.Clean(exe)) {
		return nil, errors.New("autostart: executable does not match reference")
	}
	if err = validateArguments(doc.Executable, doc.Args); err != nil {
		return nil, err
	}
	return doc.Args, nil
}

func referenceID(args []string) (string, error) {
	if len(args) != 1 || !strings.HasPrefix(args[0], referenceFlag) {
		return "", errors.New("autostart: startup reference must be the sole argument")
	}
	id := strings.TrimPrefix(args[0], referenceFlag)
	if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
		return "", errors.New("autostart: invalid startup ID")
	}
	return id, nil
}
