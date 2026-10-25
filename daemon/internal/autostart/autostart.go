// Package autostart manages padmate's per-user startup entry. Reading state
// never writes; Set is called only after an explicit user menu action.
package autostart

import (
	"errors"
	"strings"
)

type store interface {
	read() (string, error)
	write(string) error
	remove() error
}

type Entry struct {
	store      store
	command    string
	exe        string
	args       []string
	dir        string
	referenced bool
}

func New(exe string, args []string) (*Entry, error) {
	dir, _ := startupDirectory()
	return newEntry(exe, args, dir, systemStore{})
}

func newEntry(exe string, args []string, dir string, s store) (*Entry, error) {
	command, err := Command(exe, args)
	if err != nil {
		return nil, err
	}
	if err := validateArguments(exe, args); err != nil {
		return nil, err
	}
	return &Entry{store: s, command: command, exe: exe, args: append([]string(nil), args...), dir: dir}, nil
}

func (e *Entry) Enabled() (bool, error) {
	v, err := e.store.read()
	return v != "", err
}

// Current reports whether the entry is the command Set(true) writes for
// this executable and these arguments, not one another padmate (another
// path or mode) registered. Like Enabled it writes nothing.
func (e *Entry) Current() (bool, error) {
	v, err := e.store.read()
	if err != nil || v == "" {
		return false, err
	}
	want, err := e.wanted()
	if err != nil {
		return false, err
	}
	return v == want, nil
}

// wanted is the Run value Set(true) writes: the command, or the startup
// reference that stands for a command too long for Run. It computes the
// reference without writing its argument file.
func (e *Entry) wanted() (string, error) {
	if e.referenced || commandUnits(e.command) <= 260 {
		return e.command, nil
	}
	_, id, err := encodeArguments(argumentFile{Version: 1, Executable: e.exe, Args: e.args})
	if err != nil {
		return "", err
	}
	return Command(e.exe, []string{referenceFlag + id})
}

func (e *Entry) Set(enabled bool) error {
	if enabled {
		if commandUnits(e.command) > 260 || e.referenced {
			return e.setReference()
		}
		return e.store.write(e.command)
	}
	return e.store.remove()
}

// Command uses Windows argv quoting, not shell syntax. Callers supply absolute
// executable/profile paths because Run entries have no reliable working dir.
func Command(exe string, args []string) (string, error) {
	if exe == "" || strings.ContainsAny(exe, "\x00\"") {
		return "", errors.New("autostart: invalid executable path")
	}
	parts := []string{quoteArg(exe)}
	for _, arg := range args {
		if strings.ContainsRune(arg, 0) {
			return "", errors.New("autostart: NUL in argument")
		}
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " "), nil
}

func quoteArg(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\r\"") {
		return s
	}
	var b strings.Builder
	b.WriteByte('"')
	slashes := 0
	for _, r := range s {
		if r == '\\' {
			slashes++
			continue
		}
		if r == '"' {
			b.WriteString(strings.Repeat("\\", slashes*2+1))
		} else {
			b.WriteString(strings.Repeat("\\", slashes))
		}
		slashes = 0
		b.WriteRune(r)
	}
	b.WriteString(strings.Repeat("\\", slashes*2))
	b.WriteByte('"')
	return b.String()
}
