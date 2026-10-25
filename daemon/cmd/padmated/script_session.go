package main

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
)

// BindProfileSession is called by the supervisor before the first assignment.
func (r *scriptRuntime) BindProfileSession(store *script.SessionStore, key string) {
	r.sessions = store
	r.profileKey = key
}
func (r *scriptRuntime) SetProfileKey(key string) error {
	if key == r.profileKey {
		return nil
	}
	// A failed hot reload can keep its own script, but a different profile
	// must never continue running the old application's handlers or timers.
	err := r.stopCurrent()
	r.profileKey = key
	// The library's macros are the same files in every profile (SCR-51):
	// one disabled after an interruption stays so until its file changes.
	r.macroErrors = nil
	return err
}
func (r *scriptRuntime) profileSession() *script.Session {
	if r.sessions == nil {
		r.sessions = &script.SessionStore{}
		root := r.def.spec().root
		r.profileKey = canonicalProfileKey(filepath.Join(root, "profile.json"))
	}
	return r.sessions.ForProfile(r.profileKey)
}

func (r *scriptRuntime) profileRequester(gen uint32) func(string) error {
	if r.def.profileSwitch == nil {
		return nil
	}
	return func(id string) error { return r.def.profileSwitch(gen, id) }
}

func canonicalProfileKey(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
