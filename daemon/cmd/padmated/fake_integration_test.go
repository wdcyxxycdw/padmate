package main

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

func demoFunctions(t *testing.T, d *demoIntegration) (integration.Listing, map[string]integration.Function) {
	t.Helper()
	l, err := d.Functions("en")
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]integration.Function{}
	for _, f := range l.Functions {
		m[f.ID] = f
	}
	return l, m
}

// TestDemoIntegration walks Demo Paint: the tree places every function once
// (a tool is a node with children), a conflict and a cycle exist, and write,
// restore, check, compare and plan work in memory.
func TestDemoIntegration(t *testing.T) {
	d := newDemoIntegration()
	if d.ID() != "fakeapp" || d.Name() != "Demo Paint" || !slices.Equal(d.Exes(), []string{"DemoPaint.exe"}) || !d.Locate().Found {
		t.Fatal("identity")
	}
	l, m := demoFunctions(t, d)
	seen := map[string]int{}
	var walk func([]integration.TreeNode)
	walk = func(ns []integration.TreeNode) {
		for _, n := range ns {
			if n.Function != "" {
				seen[n.Function]++
			}
			walk(n.Children)
		}
	}
	walk(l.Tree)
	for _, f := range l.Functions {
		if seen[f.ID] != 1 {
			t.Errorf("%s placed %d times", f.ID, seen[f.ID])
		}
	}
	if len(seen) != len(l.Functions) || len(l.Tree) != 2 || l.Tree[1].Children[0].Function == "" || len(l.Tree[1].Children[0].Children) == 0 {
		t.Errorf("tree = %+v", l.Tree)
	}
	if !slices.Equal(m["demo:tool.pen"].Cycle, []string{"demo:tool.pen.pencil"}) || len(m["demo:file.export.png"].Keys) != 0 {
		t.Errorf("pen = %+v", m["demo:tool.pen"])
	}

	// A conflict and a cycle.
	if r, _ := d.Check("demo:edit.copy", "Ctrl+C", "en"); r.Status != integration.StatusSame || len(r.Owners) != 1 {
		t.Errorf("same = %+v", r)
	}
	if r, _ := d.Check("demo:edit.redo", "Ctrl+C", "en"); r.Status != integration.StatusConflict || len(r.Owners) != 2 {
		t.Errorf("conflict = %+v", r)
	}
	if r, _ := d.Check("demo:tool.pen.ink", "P", "en"); r.Status != integration.StatusCycle {
		t.Errorf("cycle = %+v", r)
	}
	if r, _ := d.Check("demo:edit.redo", "Ctrl+Q", "en"); r.Status != integration.StatusFree {
		t.Errorf("free = %+v", r)
	}
	var ce *integration.ChangeError
	if _, err := d.Check("nope", "Ctrl+Q", "en"); !errors.As(err, &ce) {
		t.Errorf("unknown function: %v", err)
	}

	// Compare and plan.
	items := []integration.Item{
		{Keys: "Ctrl+N", Function: "demo:file.new"},        // consistent
		{Keys: "Ctrl+Y", Function: "demo:file.open"},       // changed, Redo owns the key
		{Keys: "Ctrl+E", Function: "demo:file.export.png"}, // removed (no key at all)
		{Keys: "Ctrl+W", Function: "demo:gone"},            // missing
	}
	st, _, _ := d.Compare(items, "en")
	got := []string{st[0].State, st[1].State, st[2].State, st[3].State}
	if !slices.Equal(got, []string{"consistent", "changed", "removed", "missing"}) || len(st[1].Owners) != 1 || !slices.Equal(st[1].Current, []string{"Ctrl+O"}) {
		t.Fatalf("statuses = %+v", st)
	}
	plan, _ := d.Plan(items, "en")
	if len(plan.Entries) != 3 || len(plan.Entries[0].Displaced) != 1 || plan.Entries[2].Skipped != integration.SkipMissing {
		t.Fatalf("plan = %+v", plan)
	}

	// Write applies to memory and returns a backup; the software must not run.
	d.setRunning(true)
	if _, err := d.Write(plan.Entries[0].Changes); !errors.Is(err, integration.ErrRunning) {
		t.Fatalf("write while running: %v", err)
	}
	d.setRunning(false)
	res, err := d.Write(plan.Entries[0].Changes)
	if err != nil || res.Backup.ID == "" || res.Backup.Changes != 2 {
		t.Fatalf("write = %+v %v", res, err)
	}
	_, m = demoFunctions(t, d)
	if !slices.Equal(m["demo:file.open"].Keys, []string{"Ctrl+O", "Ctrl+Y"}) || len(m["demo:edit.redo"].Keys) != 0 {
		t.Errorf("after write: open %v redo %v", m["demo:file.open"].Keys, m["demo:edit.redo"].Keys)
	}
	if rev, _ := d.Revision(); rev == l.Revision {
		t.Error("revision did not change")
	}
	// A clash is refused as a whole and writes nothing.
	if _, err := d.Write([]integration.Change{{Function: "demo:edit.redo", Add: "Ctrl+N"}}); !errors.As(err, &ce) || ce.Reason != integration.ReasonConflict {
		t.Errorf("clash: %v", err)
	}
	// Nothing changes, no backup.
	if res, err := d.Write([]integration.Change{{Function: "demo:file.new", Add: "Ctrl+N"}}); err != nil || res.Backup.ID != "" {
		t.Errorf("no-op write = %+v %v", res, err)
	}

	// Restore puts the first backup back after backing up the current state.
	bk, _ := d.Backups()
	if len(bk) != 1 {
		t.Fatalf("backups = %+v", bk)
	}
	back, err := d.Restore(bk[0].ID)
	if err != nil || back.Backup.Reason != "before-restore" {
		t.Fatalf("restore = %+v %v", back, err)
	}
	_, m = demoFunctions(t, d)
	if !slices.Equal(m["demo:edit.redo"].Keys, []string{"Ctrl+Y"}) || len(m["demo:file.open"].Keys) != 1 {
		t.Errorf("after restore: %v", m["demo:edit.redo"].Keys)
	}
	if _, err := d.Restore("nope"); !errors.Is(err, integration.ErrNotFound) {
		t.Errorf("restore unknown: %v", err)
	}
	if bk, _ = d.Backups(); len(bk) != 2 {
		t.Errorf("backups after restore = %d", len(bk))
	}
}

// TestFakeIntegrationMethod: under --fake-input the daemon has Demo Paint and
// fake.integration controls whether it runs.
func TestFakeIntegrationMethod(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "first", `{"schemaVersion":0,"name":"first","layers":{"base":{}}}`)
	tl := startLibraryLiveOpen(t, `{"schemaVersion":0}`, options{dataDir: dir, dryRun: true, fakeInput: "ipc"}, nil)
	client := rpcTestClient(t, tl.liveRun)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var list []rpcIntegration
	if err := client.CallResult(ctx, "integrations.list", nil, &list); err != nil || len(list) != 1 || list[0].ID != "fakeapp" || !list[0].Found || list[0].Running {
		t.Fatalf("list = %+v %v", list, err)
	}
	var r struct{ Running bool }
	if err := client.CallResult(ctx, "fake.integration", map[string]any{"running": true}, &r); err != nil || !r.Running {
		t.Fatalf("fake.integration = %+v %v", r, err)
	}
	if err := client.CallResult(ctx, "integrations.list", nil, &list); err != nil || !list[0].Running {
		t.Fatalf("running not shown: %+v %v", list, err)
	}
	err := client.CallResult(ctx, "integrations.write", map[string]any{"integration": "fakeapp",
		"changes": []map[string]string{{"function": "demo:edit.redo", "remove": "Ctrl+Y"}}}, new(any))
	if rpcCode(err) != rpcCodeIntegrationRunning {
		t.Fatalf("write while running: %v", err)
	}
	if err := client.CallResult(ctx, "fake.integration", map[string]any{"running": false}, &r); err != nil || r.Running {
		t.Fatalf("stop = %+v %v", r, err)
	}
	var fl struct {
		Tree      []integration.TreeNode
		Functions []integration.Function
	}
	if err := client.CallResult(ctx, "integrations.functions", map[string]any{"integration": "fakeapp"}, &fl); err != nil || len(fl.Tree) != 2 || len(fl.Functions) != len(demoFns) {
		t.Fatalf("functions = %+v %v", fl, err)
	}
}
