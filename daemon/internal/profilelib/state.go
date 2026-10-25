package profilelib

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/input"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

func (l *Library) readData(name string) ([]byte, error) {
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDocumentBytes {
		return nil, fmt.Errorf("profile library: %s exceeds size limit", name)
	}
	return data, nil
}

// ReadConfig returns defaults for a missing config and otherwise strictly
// validates it. Revision refers to the original bytes; missing files use "".
func (l *Library) ReadConfig() (*profile.Config, string, error) {
	data, err := l.readData("config.json")
	if errors.Is(err, os.ErrNotExist) {
		return profile.DefaultConfig(), "", nil
	}
	if err != nil {
		return nil, "", err
	}
	c, ds := profile.ParseConfig(data)
	if err := ds.Err(); err != nil {
		return nil, Revision(data), err
	}
	for key := range c.Devices {
		if _, err := input.ParseDeviceKey(key); err != nil {
			return nil, Revision(data), err
		}
	}
	return c, Revision(data), nil
}

// ErrInvalidState is the error of a state.json with errors, which
// padmated replaces by an empty state (PRO-29), unlike one it cannot read.
var ErrInvalidState = errors.New("state.json has errors")

// ReadState returns the saved state, empty for a missing state.json. A
// file with errors returns an error that is ErrInvalidState.
func (l *Library) ReadState() (profile.State, error) {
	data, err := l.readData("state.json")
	if errors.Is(err, os.ErrNotExist) {
		return profile.State{}, nil
	}
	if err != nil {
		return profile.State{}, err
	}
	state, err := decodeState(data)
	if err != nil {
		return profile.State{}, fmt.Errorf("%w: %w", ErrInvalidState, err)
	}
	return state, nil
}

func decodeState(data []byte) (profile.State, error) {
	state, ds := profile.ParseState(data)
	if err := ds.Err(); err != nil {
		return profile.State{}, err
	}
	for key := range state.Devices {
		if _, err := input.ParseDeviceKey(key); err != nil {
			return profile.State{}, err
		}
	}
	return state, nil
}

// WriteState validates before opening any file and replaces the destination
// only after the entire new document has been written, synced and closed.
func (l *Library) WriteState(state profile.State) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if _, err := decodeState(data); err != nil {
		return err
	}
	data = append(data, '\n')
	if len(data) > MaxDocumentBytes {
		return errors.New("profile library: state exceeds size limit")
	}
	return l.writeData("state.json", data)
}

func (l *Library) writeData(name string, data []byte) (err error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if err := os.MkdirAll(l.dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(l.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	return atomicReplace(root, name, data)
}

// atomicReplace writes beside the destination, so rename stays on the same
// volume. Callers serialize revision checks and replacements with writeMu.
func atomicReplace(root *os.Root, name string, data []byte) error {
	return atomicWrite(root, name, data, time.Time{})
}

// atomicWrite is atomicReplace that also gives the new file modTime,
// unless it is zero, before renaming it into place.
func atomicWrite(root *os.Root, name string, data []byte, modTime time.Time) error {
	return atomicWriteBorn(root, name, data, modTime, time.Time{})
}

// atomicWriteBorn is atomicWrite that also gives the new file the creation
// time born, unless it is zero, so that replacing a file keeps the creation
// order of its folder. Windows gives the replacement the creation time of
// its temporary file; setting it is best effort.
func atomicWriteBorn(root *os.Root, name string, data []byte, modTime, born time.Time) (err error) {
	tmp := ".padmate-" + rand.Text() + ".tmp"
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if removeErr := root.Remove(tmp); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}
	}()
	_, writeErr := f.Write(data)
	if writeErr == nil && !born.IsZero() {
		_ = setBirthTime(f, born)
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	if err := errors.Join(writeErr, f.Close()); err != nil {
		return err
	}
	if !modTime.IsZero() {
		if err := root.Chtimes(tmp, modTime, modTime); err != nil {
			return err
		}
	}
	return root.Rename(tmp, name)
}

// Memory is shared by slot owners, but never performs disk I/O from Save/Load.
// Flush is for the daemon's persistence worker. Returned snapshots are copies.
type Memory struct {
	mu      sync.Mutex
	flushMu sync.Mutex
	state   profile.State
	restore bool
	version uint64
	saved   uint64
}

func NewMemory(state profile.State, restore bool) *Memory {
	return &Memory{state: state.Clone(), restore: restore}
}

func (m *Memory) Load(device, id string) (profile.LayerMemory, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.restore {
		return profile.LayerMemory{}, false
	}
	v, ok := m.state.Devices[device].Profiles[id]
	return v.Clone(), ok
}

func (m *Memory) Save(device, id string, state profile.LayerMemory) {
	if device == "" || !ValidID(id) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.Devices == nil {
		m.state.Devices = make(map[string]profile.DeviceMemory)
	}
	d := m.state.Devices[device]
	if d.Profiles == nil {
		d.Profiles = make(map[string]profile.LayerMemory)
	}
	if reflect.DeepEqual(d.Profiles[id], state) {
		return
	}
	d.Profiles[id] = state.Clone()
	m.state.Devices[device] = d
	m.version++
}

func (m *Memory) SetLastProfile(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state.LastProfile != id || !m.state.LastProfileSet {
		m.state.LastProfile = id
		m.state.LastProfileSet = true
		m.version++
	}
}

func (m *Memory) Flush(l *Library) error {
	m.flushMu.Lock()
	defer m.flushMu.Unlock()
	m.mu.Lock()
	if m.version == m.saved {
		m.mu.Unlock()
		return nil
	}
	state, version := m.state.Clone(), m.version
	m.mu.Unlock()
	if err := l.WriteState(state); err != nil {
		return err
	}
	m.mu.Lock()
	m.saved = version
	m.mu.Unlock()
	return nil
}
