package main

import (
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/creachadair/jrpc2"
	"github.com/creachadair/jrpc2/handler"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// The fake integration of --fake-input (ARCHITECTURE section 16.5): "Demo
// Paint", an in-memory piece of software for the E2E, which uses the private
// fake-input daemon and so has no CLIP STUDIO PAINT. It is registered only
// when padmated runs with --fake-input --dry-run, touches no file, and its
// "running" flag is set with fake.integration.

const (
	demoID  = "fakeapp"
	demoExe = "DemoPaint.exe"
)

// demoFn is a function of Demo Paint.
type demoFn struct {
	id, name, kind string
	path           []string
	keys           []string // the keys it starts with
}

// The functions, in tree order. Ctrl+C is held by two commands (a conflict);
// the tools Pen and Pencil share P (the cycle shortcut); some have no key.
var demoFns = []demoFn{
	{"demo:file.new", "New", integration.KindCommand, []string{"File"}, []string{"Ctrl+N"}},
	{"demo:file.open", "Open", integration.KindCommand, []string{"File"}, []string{"Ctrl+O"}},
	{"demo:file.export.png", "PNG", integration.KindCommand, []string{"File", "Export"}, nil},
	{"demo:file.export.jpg", "JPEG", integration.KindCommand, []string{"File", "Export"}, nil},
	{"demo:edit.undo", "Undo", integration.KindCommand, []string{"Edit"}, []string{"Ctrl+Z"}},
	{"demo:edit.redo", "Redo", integration.KindCommand, []string{"Edit"}, []string{"Ctrl+Y"}},
	{"demo:edit.copy", "Copy", integration.KindCommand, []string{"Edit"}, []string{"Ctrl+C"}},
	{"demo:edit.duplicate", "Duplicate", integration.KindCommand, []string{"Edit"}, []string{"Ctrl+C"}},
	{"demo:tool.pen", "Pen", integration.KindTool, []string{"Pen"}, []string{"P"}},
	{"demo:tool.pen.pencil", "Pencil", integration.KindTool, []string{"Pen", "Pencil"}, []string{"P"}},
	{"demo:tool.pen.ink", "Ink", integration.KindTool, []string{"Pen", "Ink"}, nil},
	{"demo:tool.eraser", "Eraser", integration.KindTool, []string{"Eraser"}, []string{"E"}},
	{"demo:tool.eraser.soft", "Soft", integration.KindTool, []string{"Eraser", "Soft"}, nil},
}

func demoTree() []integration.TreeNode {
	fn := func(id, name string, children ...integration.TreeNode) integration.TreeNode {
		return integration.TreeNode{ID: id, Name: name, Function: id, Children: children}
	}
	return []integration.TreeNode{
		{ID: "g:menu", Name: "Main menu", Children: []integration.TreeNode{
			{ID: "g:file", Name: "File", Children: []integration.TreeNode{
				fn("demo:file.new", "New"), fn("demo:file.open", "Open"),
				{ID: "g:export", Name: "Export", Children: []integration.TreeNode{fn("demo:file.export.png", "PNG"), fn("demo:file.export.jpg", "JPEG")}},
			}},
			{ID: "g:edit", Name: "Edit", Children: []integration.TreeNode{
				fn("demo:edit.undo", "Undo"), fn("demo:edit.redo", "Redo"), fn("demo:edit.copy", "Copy"), fn("demo:edit.duplicate", "Duplicate"),
			}},
		}},
		{ID: "tools", Name: "Tools", Children: []integration.TreeNode{
			fn("demo:tool.pen", "Pen", fn("demo:tool.pen.pencil", "Pencil"), fn("demo:tool.pen.ink", "Ink")),
			fn("demo:tool.eraser", "Eraser", fn("demo:tool.eraser.soft", "Soft")),
		}},
	}
}

// demoIntegration is Demo Paint. All state is in memory.
type demoIntegration struct {
	mu       sync.Mutex
	running  bool
	revision int
	keys     map[string][]string
	backups  []demoBackup
	now      func() time.Time
}

type demoBackup struct {
	info integration.BackupInfo
	keys map[string][]string
}

var _ integration.Integration = (*demoIntegration)(nil)

func newDemoIntegration() *demoIntegration {
	d := &demoIntegration{keys: map[string][]string{}, now: time.Now}
	for _, f := range demoFns {
		d.keys[f.id] = slices.Clone(f.keys)
	}
	return d
}

// newDemoRegistry is the registry of a --fake-input run: Demo Paint alone.
func newDemoRegistry() *integration.Registry {
	reg := integration.NewRegistry()
	reg.Register(newDemoIntegration())
	return reg
}

func (*demoIntegration) ID() string     { return demoID }
func (*demoIntegration) Name() string   { return "Demo Paint" }
func (*demoIntegration) Exes() []string { return []string{demoExe} }
func (*demoIntegration) Locate() integration.Location {
	return integration.Location{Found: true, DataDir: "(in memory)"}
}

func (d *demoIntegration) Revision() (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.rev(), nil
}

func (d *demoIntegration) rev() string { return fmt.Sprintf("demo-%d", d.revision) }

func (d *demoIntegration) Running() (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running, nil
}

func (d *demoIntegration) setRunning(v bool) {
	d.mu.Lock()
	d.running = v
	d.mu.Unlock()
}

func demoFind(id string) (demoFn, bool) {
	for _, f := range demoFns {
		if f.id == id {
			return f, true
		}
	}
	return demoFn{}, false
}

// holders lists the functions that hold keys, in tree order.
func (d *demoIntegration) holders(keys string) []demoFn {
	var out []demoFn
	for _, f := range demoFns {
		if slices.Contains(d.keys[f.id], keys) {
			out = append(out, f)
		}
	}
	return out
}

func (d *demoIntegration) owners(fs []demoFn, except string) []integration.Owner {
	out := []integration.Owner{}
	for _, f := range fs {
		if f.id != except {
			out = append(out, integration.Owner{ID: f.id, Name: f.name, Path: slices.Clone(f.path)})
		}
	}
	return out
}

func (d *demoIntegration) Functions(string) (integration.Listing, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	l := integration.Listing{Revision: d.rev(), Tree: demoTree(), Functions: []integration.Function{}}
	for _, f := range demoFns {
		fn := integration.Function{ID: f.id, Kind: f.kind, Name: f.name, Path: slices.Clone(f.path), Keys: slices.Clone(d.keys[f.id])}
		if fn.Keys == nil {
			fn.Keys = []string{}
		}
		if f.kind == integration.KindTool && len(fn.Keys) > 0 {
			for _, o := range d.holders(fn.Keys[0]) {
				if o.id != f.id && o.kind == integration.KindTool {
					fn.Cycle = append(fn.Cycle, o.id)
				}
			}
		}
		l.Functions = append(l.Functions, fn)
	}
	return l, nil
}

func (d *demoIntegration) Check(function, keys, _ string) (integration.CheckResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var target demoFn
	if function != "" {
		f, ok := demoFind(function)
		if !ok {
			return integration.CheckResult{}, &integration.ChangeError{Function: function, Keys: keys, Reason: integration.ReasonUnknownFunction}
		}
		target = f
	}
	res := integration.CheckResult{Status: integration.StatusFree, Notes: []string{}}
	held := d.holders(keys)
	res.Owners = d.owners(held, function)
	switch {
	case slices.ContainsFunc(held, func(f demoFn) bool { return f.id == function && function != "" }):
		res.Status = integration.StatusSame
	case len(res.Owners) == 0:
	case function != "" && target.kind == integration.KindTool && !slices.ContainsFunc(held, func(f demoFn) bool { return f.kind != integration.KindTool }):
		res.Status = integration.StatusCycle
	default:
		res.Status = integration.StatusConflict
	}
	return res, nil
}

func (d *demoIntegration) compare(items []integration.Item) []integration.ItemStatus {
	out := make([]integration.ItemStatus, 0, len(items))
	for _, it := range items {
		st := integration.ItemStatus{Keys: it.Keys, Function: it.Function}
		cur, ok := d.keys[it.Function]
		_, known := demoFind(it.Function)
		switch {
		case !ok || !known:
			st.State = integration.StateMissing
		case slices.Contains(cur, it.Keys):
			st.State = integration.StateConsistent
		case len(cur) == 0:
			st.State = integration.StateRemoved
		default:
			st.State = integration.StateChanged
			st.Current = slices.Clone(cur)
			if o := d.owners(d.holders(it.Keys), it.Function); len(o) > 0 {
				st.Owners = o
			}
		}
		out = append(out, st)
	}
	return out
}

func (d *demoIntegration) Compare(items []integration.Item, _ string) ([]integration.ItemStatus, string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.compare(items), d.rev(), nil
}

func (d *demoIntegration) Plan(items []integration.Item, _ string) (integration.Plan, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	plan := integration.Plan{Entries: []integration.PlanEntry{}}
	for i, st := range d.compare(items) {
		if st.State == integration.StateConsistent {
			continue
		}
		it := items[i]
		e := integration.PlanEntry{Item: it, Changes: []integration.Change{}, Displaced: []integration.Displaced{}}
		if st.State == integration.StateMissing {
			e.Skipped = integration.SkipMissing
			plan.Entries = append(plan.Entries, e)
			continue
		}
		f, _ := demoFind(it.Function)
		e.Changes = append(e.Changes, integration.Change{Function: it.Function, Add: it.Keys})
		for _, o := range d.holders(it.Keys) {
			// Tools sharing a key are the cycle shortcut, not a conflict.
			if o.id == it.Function || o.kind == integration.KindTool && f.kind == integration.KindTool {
				continue
			}
			e.Changes = append(e.Changes, integration.Change{Function: o.id, Remove: it.Keys})
			e.Displaced = append(e.Displaced, integration.Displaced{Function: o.id, Name: o.name, Keys: it.Keys})
		}
		plan.Entries = append(plan.Entries, e)
	}
	return plan, nil
}

// apply carries changes out on a copy of the keys; a change that cannot be
// made refuses the whole set.
func (d *demoIntegration) apply(changes []integration.Change) (map[string][]string, error) {
	next := map[string][]string{}
	for id, k := range d.keys {
		next[id] = slices.Clone(k)
	}
	for _, c := range changes {
		f, ok := demoFind(c.Function)
		if !ok {
			return nil, &integration.ChangeError{Function: c.Function, Keys: c.Add + c.Remove, Reason: integration.ReasonUnknownFunction}
		}
		if c.Remove != "" {
			i := slices.Index(next[f.id], c.Remove)
			if i < 0 {
				return nil, &integration.ChangeError{Function: f.id, Keys: c.Remove, Reason: integration.ReasonNoSuchKey}
			}
			next[f.id] = slices.Delete(next[f.id], i, i+1)
		}
		if c.Add != "" && !slices.Contains(next[f.id], c.Add) {
			if f.kind == integration.KindTool {
				next[f.id] = []string{c.Add} // a tool has a single key
			} else {
				next[f.id] = append(next[f.id], c.Add)
			}
		}
	}
	// A key may not end up on two functions unless both are tools.
	for _, f := range demoFns {
		for _, k := range next[f.id] {
			for _, g := range demoFns {
				if g.id != f.id && slices.Contains(next[g.id], k) && (f.kind != integration.KindTool || g.kind != integration.KindTool) &&
					!slices.Contains(d.keys[f.id], k) {
					return nil, &integration.ChangeError{Function: f.id, Keys: k, Reason: integration.ReasonConflict}
				}
			}
		}
	}
	return next, nil
}

func (d *demoIntegration) snapshot(reason string, changes int) integration.BackupInfo {
	info := integration.BackupInfo{ID: fmt.Sprintf("b%d", len(d.backups)+1), Created: d.now(), Reason: reason, Changes: changes}
	cp := map[string][]string{}
	for id, k := range d.keys {
		cp[id] = slices.Clone(k)
	}
	d.backups = append(d.backups, demoBackup{info: info, keys: cp})
	return info
}

func (d *demoIntegration) Write(changes []integration.Change) (integration.WriteResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return integration.WriteResult{}, integration.ErrRunning
	}
	next, err := d.apply(changes)
	if err != nil {
		return integration.WriteResult{}, err
	}
	changed := false
	for id := range next {
		if !slices.Equal(next[id], d.keys[id]) {
			changed = true
		}
	}
	if !changed {
		return integration.WriteResult{Revision: d.rev()}, nil
	}
	info := d.snapshot("write", len(changes))
	d.keys = next
	d.revision++
	return integration.WriteResult{Backup: info, Revision: d.rev()}, nil
}

func (d *demoIntegration) Backups() ([]integration.BackupInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]integration.BackupInfo, 0, len(d.backups))
	for _, b := range slices.Backward(d.backups) {
		out = append(out, b.info)
	}
	return out, nil
}

func (d *demoIntegration) Restore(id string) (integration.WriteResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.running {
		return integration.WriteResult{}, integration.ErrRunning
	}
	i := slices.IndexFunc(d.backups, func(b demoBackup) bool { return b.info.ID == id })
	if i < 0 {
		return integration.WriteResult{}, fmt.Errorf("backup %q: %w", id, integration.ErrNotFound)
	}
	target := d.backups[i].keys
	before := d.snapshot("before-restore", 0)
	d.keys = map[string][]string{}
	for k, v := range target {
		d.keys[k] = slices.Clone(v)
	}
	d.revision++
	return integration.WriteResult{Backup: before, Revision: d.rev()}, nil
}

// fakeIntegrationMethod adds fake.integration {running?: bool}: it sets
// whether Demo Paint is running, and answers {running}. Without running it
// only reads. Like the other fake methods it needs --fake-input --dry-run.
func (r *liveRun) fakeIntegrationMethod(methods handler.Map) {
	methods["fake.integration"] = func(_ context.Context, req *jrpc2.Request) (any, error) {
		if _, err := r.fakeInput(); err != nil {
			return nil, err
		}
		var p struct {
			Running *bool `json:"running"`
		}
		if err := req.UnmarshalParams(jrpc2.StrictFields(&p)); err != nil {
			return nil, err
		}
		in, ok := r.integrations().Get(demoID)
		d, isDemo := in.(*demoIntegration)
		if !ok || !isDemo {
			return nil, &jrpc2.Error{Code: -32004, Message: "no fake integration"}
		}
		if p.Running != nil {
			d.setRunning(*p.Running)
			r.integKick()
		}
		running, _ := d.Running()
		return map[string]bool{"running": running}, nil
	}
}
