package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"
	"sync"

	"github.com/dop251/goja"

	"github.com/wdcyxxycdw/padmate/daemon/internal/clock"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script"
	"github.com/wdcyxxycdw/padmate/daemon/internal/script/compile"
)

// maxMacroChecks bounds macroChecks' cache; past it the cache starts over.
const maxMacroChecks = 1024

// macroChecks finds the build and load problems of macros for macros.list
// (design §7) without a gamepad: it builds them and runs their top level
// in a describe-mode runtime, which has no output. It keeps each result by
// its bundle (checkKey), so a listing runs again only the top level of the
// macros whose bundle changed: their file or a module they import. It is
// safe for concurrent use.
type macroChecks struct {
	mu       sync.Mutex
	problems map[string]string // by checkKey: "" for a macro that loads
	loaded   int               // macros whose top level ran, for the tests
}

// check returns the problem of each of files, macros of the macros folder
// of root built with the API api: "" when it builds and loads. A macro whose
// load another's wall-clock wedge stopped has no entry; the next check
// tries it again, without the wedged one, whose problem is kept.
func (c *macroChecks) check(ctx context.Context, root string, files []string, api string) (map[string]string, error) {
	problems := make(map[string]string, len(files))
	if len(files) == 0 {
		return problems, nil
	}
	results := compile.BuildMacros(filepath.Join(root, profile.MacroDir), files, api)
	keys := make(map[string]string, len(results))
	var load []script.MacroProgram
	c.mu.Lock()
	for _, res := range results {
		key := checkKey(res)
		if p, ok := c.problems[key]; ok && key != "" {
			problems[res.File] = p
			continue
		}
		mp := script.MacroProgram{File: res.File, Err: res.Err, Version: key}
		if res.Err == nil {
			mp.Program, mp.Err = goja.Compile(res.File, string(res.Result.JS), false)
		}
		load = append(load, mp)
		keys[res.File] = key
	}
	c.mu.Unlock()
	if len(load) == 0 {
		return problems, nil
	}
	found, complete, err := describeMacros(ctx, load)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, mp := range load {
		if mp.Program != nil {
			c.loaded++
		}
	}
	if c.problems == nil || len(c.problems)+len(load) > maxMacroChecks {
		c.problems = map[string]string{}
	}
	for _, mp := range load {
		p, ok := found[mp.File]
		if !ok && !complete {
			continue
		}
		problems[mp.File] = p
		if key := keys[mp.File]; key != "" {
			c.problems[key] = p
		}
	}
	return problems, nil
}

// describeMacros loads the macros in a describe-mode runtime on a worker
// and returns the problem of each that has one. complete is false when a
// wall-clock wedge stopped the load: the macros after it did not load.
func describeMacros(ctx context.Context, macros []script.MacroProgram) (found map[string]string, complete bool, err error) {
	work, cancel := context.WithCancel(ctx)
	defer cancel()
	life := script.NewLifecycle(clock.Real(), script.HostOptions{Mode: "describe"})
	defer func() { _ = life.Dispose() }()
	result := life.LoadPrograms(work, func(context.Context) (script.Programs, error) {
		return script.Programs{Macros: macros}, nil
	})
	var loaded script.LoadResult
	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case loaded = <-result:
	}
	var errs []*script.MacroError
	if loaded.Err != nil {
		errs = script.MacroErrorsOf(loaded.Err)
	} else {
		defer func() { _ = loaded.Host.Discard() }()
		errs = loaded.Host.MacroErrors()
	}
	texts := map[string][]string{}
	for _, me := range errs {
		texts[me.File] = append(texts[me.File], errorText(me))
	}
	found = make(map[string]string, len(texts))
	for file, t := range texts {
		found[file] = strings.Join(t, "\n")
	}
	return found, loaded.Err == nil, nil
}

// checkKey is the key of a check of the build res: the bundle's sha256,
// which covers every module it imports and the macro's file name, so a
// change of another file keeps it; for a failed build, the key of its
// sources. "" is no key.
func checkKey(res compile.MacroResult) string {
	if res.Err != nil {
		if res.Key == "" {
			return ""
		}
		return "build:" + res.Key
	}
	sum := sha256.Sum256(res.Result.JS)
	return "bundle:" + hex.EncodeToString(sum[:])
}
