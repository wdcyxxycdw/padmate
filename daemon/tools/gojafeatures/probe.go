package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// probeTimeout bounds one probe script. The scripts are tiny; hitting the
// limit means a goja bug, and the feature is reported as unsupported.
const probeTimeout = 5 * time.Second

// featureResult is the outcome for one esbuild feature.
type featureResult struct {
	supported bool
	// note explains a false value: the static reason, or goja's error.
	note string
}

// probeFeatures decides every feature in the table.
func probeFeatures(table []feature) map[string]featureResult {
	out := make(map[string]featureResult, len(table))
	for _, f := range table {
		if f.probe == "" {
			out[f.name] = featureResult{note: f.reason}
			continue
		}
		ok, note := runProbe(f.probe)
		out[f.name] = featureResult{supported: ok, note: note}
	}
	return out
}

// runProbe runs src in a fresh Runtime and reports whether it left ok ===
// true. RunScript drains the promise job queue before it returns, so async
// probes have finished by then.
func runProbe(src string) (ok bool, note string) {
	vm := goja.New()
	timer := time.AfterFunc(probeTimeout, func() { vm.Interrupt("timeout") })
	defer timer.Stop()
	if _, err := vm.RunScript("probe.js", src); err != nil {
		msg := firstLine(err.Error())
		// goja prefixes compile errors twice ("SyntaxError: SyntaxError: ...").
		for strings.HasPrefix(msg, "SyntaxError: SyntaxError: ") {
			msg = strings.TrimPrefix(msg, "SyntaxError: ")
		}
		return false, "goja: " + msg
	}
	if v := vm.Get("ok"); v == nil || !v.StrictEquals(vm.ToValue(true)) {
		return false, "goja: the probe ran but gave the wrong result"
	}
	return true, ""
}

// libResult is the outcome for one lib component.
type libResult struct {
	component libComponent
	missing   []string // failed probes (required)
	optional  []string // failed optional probes
}

func (r libResult) included() bool {
	return !r.component.unreferenced && (len(r.missing) == 0 || r.component.lowered != "")
}

// probeLibs checks every component, each in a fresh Runtime.
func probeLibs(table []libComponent) []libResult {
	out := make([]libResult, 0, len(table))
	for _, c := range table {
		vm := goja.New()
		r := libResult{component: c}
		for _, p := range c.probes {
			r.missing = addMissing(r.missing, probeMembers(vm, p))
		}
		for _, p := range c.optional {
			r.optional = addMissing(r.optional, probeMembers(vm, p))
		}
		out = append(out, r)
	}
	return out
}

// addMissing appends the names in add to list, skipping a name that is
// already listed or whose object is (X.prototype when X is listed).
func addMissing(list, add []string) []string {
	for _, name := range add {
		if !covered(list, name) {
			list = append(list, name)
		}
	}
	return list
}

func covered(list []string, name string) bool {
	for _, l := range list {
		if name == l || strings.HasPrefix(name, l+".") || strings.HasPrefix(name, l+"[") {
			return true
		}
	}
	return false
}

// probeMembers returns the members of one probe (see libComponent) that
// goja lacks, in a form that reads well in the generated comments.
func probeMembers(vm *goja.Runtime, probe string) []string {
	if expr, ok := strings.CutPrefix(probe, "js:"); ok {
		if !evalTrue(vm, expr) {
			return []string{expr}
		}
		return nil
	}
	base, members, ok := strings.Cut(probe, ": ")
	if !ok {
		panic(fmt.Sprintf("gojafeatures: probe %q has no \"Base: \" prefix", probe))
	}
	baseExpr := base
	if strings.HasPrefix(base, "%") {
		if baseExpr, ok = intrinsics[base]; !ok {
			panic(fmt.Sprintf("gojafeatures: unknown intrinsic %s", base))
		}
	}
	if !evalTrue(vm, "(function () { var b = "+baseExpr+"; return b !== undefined && b !== null; })()") {
		return []string{base}
	}
	var missing []string
	for _, m := range strings.Fields(members) {
		key, name := memberKey(m)
		if !evalTrue(vm, "("+key+") in Object("+baseExpr+")") {
			switch {
			case base == "globalThis":
				missing = append(missing, name)
			case strings.HasPrefix(name, "["):
				missing = append(missing, base+name)
			default:
				missing = append(missing, base+"."+name)
			}
		}
	}
	return missing
}

// memberKey turns a member from a probe into a JS key expression and the
// name used in reports.
func memberKey(m string) (key, name string) {
	if sym, ok := strings.CutPrefix(m, "@@"); ok {
		return "Symbol." + sym, "[Symbol." + sym + "]"
	}
	q, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(q), m
}

// evalTrue reports whether expr evaluates to true without throwing.
func evalTrue(vm *goja.Runtime, expr string) bool {
	timer := time.AfterFunc(probeTimeout, func() { vm.Interrupt("timeout") })
	defer timer.Stop()
	v, err := vm.RunString("(" + expr + ")")
	if err != nil {
		vm.ClearInterrupt()
		return false
	}
	return v.StrictEquals(vm.ToValue(true))
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return strings.TrimSpace(s)
}
