package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/tray"
)

// fakeIntegration is an integration.Integration over a map of function to
// keys; it touches no file. Its exe is fakeapp.exe.
type fakeIntegration struct {
	mu       sync.Mutex
	id       string
	loc      integration.Location
	running  bool
	revision int
	keys     map[string][]string // function ID to its current keys
	writeErr error
	written  [][]integration.Change
	backups  []integration.BackupInfo
	calls    int // Compare calls
}

func newFakeIntegration() *fakeIntegration {
	return &fakeIntegration{id: "fake", loc: integration.Location{Found: true, DataDir: `C:\fake\data`, InstallDir: `C:\fake`},
		keys: map[string][]string{"command:a/undo": {"Ctrl+Z"}, "command:a/redo": {"Ctrl+Y"}, "tool:" + strings.Repeat("a", 32): {"P"}}}
}

func (f *fakeIntegration) ID() string     { return f.id }
func (f *fakeIntegration) Name() string   { return "Fake App" }
func (f *fakeIntegration) Exes() []string { return []string{"FakeApp.exe"} }
func (f *fakeIntegration) Locate() integration.Location {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.loc.Found && f.loc.Problem == "" {
		f.loc.Problem = integration.ProblemNotFound
	}
	return f.loc
}
func (f *fakeIntegration) unavailable() error {
	if f.loc.Found {
		return nil
	}
	return &integration.UnavailableError{Problem: integration.ProblemNotFound}
}
func (f *fakeIntegration) Revision() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.unavailable(); err != nil {
		return "", err
	}
	return "rev" + string(rune('0'+f.revision)), nil
}
func (f *fakeIntegration) Running() (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running, nil
}
func (f *fakeIntegration) setRunning(v bool) { f.mu.Lock(); f.running = v; f.mu.Unlock() }
func (f *fakeIntegration) setKeys(function string, keys ...string) {
	f.mu.Lock()
	f.keys[function] = keys
	f.revision++
	f.mu.Unlock()
}

func (f *fakeIntegration) Functions(lang string) (integration.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.unavailable(); err != nil {
		return integration.Listing{}, err
	}
	l := integration.Listing{Revision: "rev" + string(rune('0'+f.revision)), Categories: []integration.Category{{ID: "edit", Name: lang}}}
	for id, k := range f.keys {
		l.Functions = append(l.Functions, integration.Function{ID: id, Kind: "command", Name: id, Path: []string{}, Category: "edit", Keys: k})
	}
	slices.SortFunc(l.Functions, func(a, b integration.Function) int { return strings.Compare(a.ID, b.ID) })
	return l, nil
}

func (f *fakeIntegration) Check(function, keys, lang string) (integration.CheckResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.unavailable(); err != nil {
		return integration.CheckResult{}, err
	}
	res := integration.CheckResult{Status: integration.StatusFree}
	for id, k := range f.keys {
		if slices.Contains(k, keys) {
			if id == function {
				res.Status = integration.StatusSame
			} else {
				res.Status = integration.StatusConflict
				res.Owners = append(res.Owners, integration.Owner{ID: id, Name: id})
			}
		}
	}
	return res, nil
}

func (f *fakeIntegration) compare(items []integration.Item) []integration.ItemStatus {
	var out []integration.ItemStatus
	for _, it := range items {
		st := integration.ItemStatus{Keys: it.Keys, Function: it.Function}
		cur, ok := f.keys[it.Function]
		switch {
		case !ok:
			st.State = integration.StateMissing
		case slices.Contains(cur, it.Keys):
			st.State = integration.StateConsistent
		default:
			st.State, st.Current = integration.StateChanged, slices.Clone(cur)
		}
		out = append(out, st)
	}
	return out
}

func (f *fakeIntegration) Compare(items []integration.Item, lang string) ([]integration.ItemStatus, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if err := f.unavailable(); err != nil {
		return nil, "", err
	}
	return f.compare(items), "rev", nil
}

func (f *fakeIntegration) Plan(items []integration.Item, lang string) (integration.Plan, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.unavailable(); err != nil {
		return integration.Plan{}, err
	}
	plan := integration.Plan{}
	for i, st := range f.compare(items) {
		if st.State == integration.StateConsistent {
			continue
		}
		e := integration.PlanEntry{Item: items[i]}
		if st.State == integration.StateMissing {
			e.Skipped = integration.SkipMissing
		} else {
			e.Changes = []integration.Change{{Function: items[i].Function, Add: items[i].Keys}}
		}
		plan.Entries = append(plan.Entries, e)
	}
	return plan, nil
}

func (f *fakeIntegration) Write(changes []integration.Change) (integration.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return integration.WriteResult{}, integration.ErrRunning
	}
	if f.writeErr != nil {
		return integration.WriteResult{}, f.writeErr
	}
	for _, c := range changes {
		if _, ok := f.keys[c.Function]; !ok {
			return integration.WriteResult{}, &integration.ChangeError{Function: c.Function, Reason: integration.ReasonUnknownFunction}
		}
	}
	f.written = append(f.written, changes)
	for _, c := range changes {
		if c.Add != "" {
			f.keys[c.Function] = append(f.keys[c.Function], c.Add)
		}
		if c.Remove != "" {
			f.keys[c.Function] = slices.DeleteFunc(f.keys[c.Function], func(k string) bool { return k == c.Remove })
		}
	}
	f.revision++
	b := integration.BackupInfo{ID: "b" + string(rune('0'+len(f.backups))), Created: time.Date(2026, 11, 4, 12, 0, len(f.backups), 0, time.UTC), Reason: "write", Changes: len(changes)}
	f.backups = append([]integration.BackupInfo{b}, f.backups...)
	return integration.WriteResult{Backup: b, Revision: "rev" + string(rune('0'+f.revision))}, nil
}

func (f *fakeIntegration) Backups() ([]integration.BackupInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.backups), nil
}

func (f *fakeIntegration) Restore(id string) (integration.WriteResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return integration.WriteResult{}, integration.ErrRunning
	}
	if !slices.ContainsFunc(f.backups, func(b integration.BackupInfo) bool { return b.ID == id }) {
		return integration.WriteResult{}, integration.ErrNotFound
	}
	f.revision++
	pre := integration.BackupInfo{ID: "pre", Created: time.Date(2026, 11, 4, 13, 0, 0, 0, time.UTC), Reason: "before-restore"}
	f.backups = append([]integration.BackupInfo{pre}, f.backups...)
	return integration.WriteResult{Backup: pre, Revision: "rev" + string(rune('0'+f.revision))}, nil
}

func (f *fakeIntegration) compareCalls() int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls }

// integrationLive is a daemon with the fake integration. Profile "plain" is
// active; "paint" (extends "base") and "base" are bound by a rule to
// FakeApp.exe in the config.
type integrationLive struct {
	*testLive
	fake    *fakeIntegration
	client  *jrpc2.Client
	ctx     context.Context
	dir     string
	mu      sync.Mutex
	notices []tray.Notification
	events  chan rawEvent
}

type rawEvent struct {
	topic string
	data  json.RawMessage
}

const integrationConfig = `{"schemaVersion":0,"rules":[{"exe":"C:\\Apps\\fakeapp.exe","profile":"other"},{"exe":"FakeApp.exe","profile":"paint"},{"exe":"FakeApp.exe","profile":"off","enabled":false},{"exe":"notepad.exe","profile":"plain"}]}`

func startIntegrationLive(t *testing.T, start string) *integrationLive {
	t.Helper()
	dir := t.TempDir()
	for _, id := range []string{"plain", "base", "other", "off"} {
		writeLibraryProfile(t, dir, id, `{"schemaVersion":0,"name":"`+id+`","layers":{"base":{}}}`)
	}
	// base (and so paint) sends the keys the items of the tests use; the
	// activation check and the comparison look at those only.
	writeLibraryProfile(t, dir, "base", `{"schemaVersion":0,"name":"base","layers":{"base":{"south":{"tap":{"type":"keys","keys":"Ctrl+Z"}},"east":{"tap":{"type":"keys","keys":"ctrl+y"}},"north":{"tap":{"type":"keys","keys":"F5"}}}}}`)
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"Paint","extends":"base","layers":{"base":{}}}`)
	il := &integrationLive{fake: newFakeIntegration(), dir: dir, events: make(chan rawEvent, 64)}
	reg := integration.NewRegistry()
	reg.Register(il.fake)
	if start != "" {
		writeLastProfile(t, dir, start)
	}
	var err error
	il.testLive, _ = startLibraryLive(t, integrationConfig, options{dataDir: dir, dryRun: true}, func(c *liveConfig) {
		c.integrations = reg
		c.integrationEvery = 20 * time.Millisecond
		c.integrationNotify = func(n tray.Notification) { il.mu.Lock(); il.notices = append(il.notices, n); il.mu.Unlock() }
	})
	_ = err
	il.client = rpcTestClient(t, il.liveRun, &jrpc2.ClientOptions{OnNotify: func(r *jrpc2.Request) {
		var raw json.RawMessage
		_ = r.UnmarshalParams(&raw)
		select {
		case il.events <- rawEvent{r.Method(), raw}:
		default:
		}
	}})
	ctx, cancel := context.WithTimeout(context.Background(), liveWait)
	t.Cleanup(cancel)
	il.ctx = ctx
	return il
}

func (il *integrationLive) subscribe(t *testing.T, topics ...string) {
	t.Helper()
	if err := il.client.CallResult(il.ctx, "subscribe", map[string]any{"topics": topics}, new(any)); err != nil {
		t.Fatal(err)
	}
}

func (il *integrationLive) call(method string, params, result any) error {
	return il.client.CallResult(il.ctx, method, params, result)
}

func (il *integrationLive) noticeList() []tray.Notification {
	il.mu.Lock()
	defer il.mu.Unlock()
	return slices.Clone(il.notices)
}

// nextEvent waits for an event of the topic and returns its data.
func (il *integrationLive) nextEvent(t *testing.T, topic string, d time.Duration) (json.RawMessage, bool) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case e := <-il.events:
			if e.topic == topic {
				return e.data, true
			}
		case <-deadline:
			return nil, false
		}
	}
}

func (il *integrationLive) putItems(t *testing.T, profileID, rev string, items ...integration.Item) string {
	t.Helper()
	var res struct{ Revision string }
	if err := il.call("integrations.putItems", map[string]any{"profile": profileID, "integration": "fake", "revision": rev, "items": items}, &res); err != nil {
		t.Fatal(err)
	}
	return res.Revision
}

func (il *integrationLive) activate(t *testing.T, id string) {
	t.Helper()
	if err := il.call("profiles.activate", map[string]any{"id": id}, new(any)); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationList(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.fake.setRunning(true)
	var got []rpcIntegration
	if err := il.call("integrations.list", nil, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("list = %+v", got)
	}
	g := got[0]
	// The rule with a path and the file name in another case bind "other";
	// the disabled rule and notepad do not bind anything.
	if g.ID != "fake" || g.Name != "Fake App" || !slices.Equal(g.Exe, []string{"FakeApp.exe"}) || !g.Found || g.Problem != "" ||
		g.DataDir != `C:\fake\data` || g.InstallDir != `C:\fake` || !g.Running || !slices.Equal(g.Profiles, []string{"other", "paint"}) {
		t.Fatalf("entry = %+v", g)
	}
	il.fake.mu.Lock()
	il.fake.loc = integration.Location{Problem: integration.ProblemUnknownFormat}
	il.fake.mu.Unlock()
	if err := il.call("integrations.list", nil, &got); err != nil || got[0].Found || got[0].Problem != "unknownFormat" || got[0].Profiles == nil {
		t.Fatalf("not found: %+v %v", got, err)
	}
	if err := il.call("integrations.list", map[string]any{"x": 1}, &got); rpcCode(err) != int(jrpc2.InvalidParams) {
		t.Fatalf("unknown member: %v", err)
	}
}

func TestIntegrationParamErrors(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	for _, tt := range []struct {
		method string
		params any
		code   int
	}{
		{"integrations.functions", map[string]any{"integration": "nope"}, -32004},
		{"integrations.functions", map[string]any{"integration": "Bad Id"}, -32602},
		{"integrations.functions", map[string]any{"integration": "fake", "language": "zh_CN!"}, -32602},
		{"integrations.functions", map[string]any{"integration": "fake", "extra": 1}, -32602},
		{"integrations.check", map[string]any{"integration": "fake", "keys": "Ctrl+Nonsense"}, -32602},
		{"integrations.check", map[string]any{"integration": "fake", "keys": "Ctrl+Z Ctrl+X"}, -32602},
		{"integrations.items", map[string]any{"profile": "../x", "integration": "fake"}, -32602},
		{"integrations.items", map[string]any{"profile": "missing", "integration": "fake"}, -32004},
		{"integrations.putItems", map[string]any{"profile": "paint", "integration": "fake", "items": []any{}}, -32602},
		{"integrations.putItems", map[string]any{"profile": "paint", "integration": "fake", "revision": "", "items": []any{map[string]any{"keys": "Bogus+", "function": "f"}}}, -32602},
		{"integrations.putItems", map[string]any{"profile": "paint", "integration": "fake", "revision": "", "items": []any{map[string]any{"keys": "A", "function": "f", "x": 1}}}, -32602},
		{"integrations.putItems", map[string]any{"profile": "paint", "integration": "fake", "revision": "", "items": []any{
			map[string]any{"keys": "A", "function": "f"}, map[string]any{"keys": "a", "function": "g"}}}, -32602},
		{"integrations.plan", map[string]any{"profile": "paint", "integration": "nope"}, -32004},
		{"integrations.write", map[string]any{"integration": "fake", "changes": []any{map[string]any{"function": "x"}}}, -32602},
		{"integrations.write", map[string]any{"integration": "fake", "changes": []any{map[string]any{"function": "x", "add": "Nope++"}}}, -32602},
		{"integrations.write", map[string]any{"integration": "fake", "changes": []any{map[string]any{"function": "x", "add": "A", "mystery": 1}}}, -32602},
		{"integrations.restore", map[string]any{"integration": "fake", "backup": ""}, -32602},
		{"integrations.restore", map[string]any{"integration": "fake", "backup": "nobackup"}, -32004},
		{"integrations.dismiss", map[string]any{"profile": "paint", "integration": "fake", "differenceId": "xyz"}, -32602},
		{"integrations.backups", map[string]any{"integration": "fake", "x": 1}, -32602},
	} {
		err := il.call(tt.method, tt.params, new(any))
		if rpcCode(err) != tt.code {
			t.Errorf("%s %v: err = %v, want code %d", tt.method, tt.params, err, tt.code)
		}
	}
}

func TestIntegrationFunctionsAndCheck(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	var l struct {
		Revision   string
		Categories []struct{ ID, Name string }
		Functions  []struct {
			ID   string
			Keys []string
		}
	}
	// The language is optional; the last one named is remembered.
	if err := il.call("integrations.functions", map[string]any{"integration": "fake"}, &l); err != nil || l.Categories[0].Name != "zh-CN" || len(l.Functions) != 3 || l.Revision == "" {
		t.Fatalf("functions = %+v %v", l, err)
	}
	if err := il.call("integrations.functions", map[string]any{"integration": "fake", "language": "en-US"}, &l); err != nil || l.Categories[0].Name != "en" {
		t.Fatalf("en = %+v %v", l, err)
	}
	if err := il.call("integrations.functions", map[string]any{"integration": "fake"}, &l); err != nil || l.Categories[0].Name != "en" {
		t.Fatalf("remembered language = %+v %v", l, err)
	}
	var c integration.CheckResult
	// Keys come back canonical and the answer has arrays, not nulls.
	if err := il.call("integrations.check", map[string]any{"integration": "fake", "function": "command:a/undo", "keys": "ctrl+z"}, &c); err != nil || c.Status != "same" || c.Notes == nil {
		t.Fatalf("check same = %+v %v", c, err)
	}
	if err := il.call("integrations.check", map[string]any{"integration": "fake", "keys": "Ctrl+Z", "language": "en"}, &c); err != nil || c.Status != "conflict" || len(c.Owners) != 1 {
		t.Fatalf("check conflict = %+v %v", c, err)
	}
	raw := json.RawMessage{}
	if err := il.call("integrations.check", map[string]any{"integration": "fake", "keys": "F9"}, &raw); err != nil || !strings.Contains(string(raw), `"owners":[]`) || !strings.Contains(string(raw), `"notes":[]`) {
		t.Fatalf("check free = %s %v", raw, err)
	}
	// An unavailable integration is -32029 with the problem.
	il.fake.mu.Lock()
	il.fake.loc = integration.Location{Problem: integration.ProblemNotFound}
	il.fake.mu.Unlock()
	err := il.call("integrations.functions", map[string]any{"integration": "fake"}, &l)
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32029 || !strings.Contains(string(rpcErr.Data), `"problem":"notFound"`) {
		t.Fatalf("unavailable: %v", err)
	}
}

func TestIntegrationItemsPutItemsPlan(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	type itemsResult struct {
		Revision     string
		Items        []integration.Item
		Statuses     []integration.ItemStatus
		Consistent   bool
		DifferenceID string
		Dismissed    bool
	}
	var got itemsResult
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &got); err != nil ||
		got.Revision != "" || len(got.Items) != 0 || !got.Consistent || got.DifferenceID != "" || got.Statuses == nil {
		t.Fatalf("empty = %+v %v", got, err)
	}

	// The parent's items are merged in; the child's item wins for a key.
	il.putItems(t, "base", "", integration.Item{Keys: "ctrl+z", Function: "command:a/undo", Name: "Undo"}, integration.Item{Keys: "Ctrl+Y", Function: "command:a/redo"})
	rev := il.putItems(t, "paint", "", integration.Item{Keys: "Ctrl+Y", Function: "command:a/undo"}, integration.Item{Keys: "F5", Function: "command:a/gone"})
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Revision != rev || len(got.Items) != 3 || got.Items[0].Keys != "Ctrl+Z" || got.Items[1].Function != "command:a/undo" || got.Items[1].Keys != "Ctrl+Y" {
		t.Fatalf("items = %+v (revision %s)", got, rev)
	}
	states := map[string]string{}
	for _, s := range got.Statuses {
		states[s.Keys] = s.State
	}
	if states["Ctrl+Z"] != "consistent" || states["Ctrl+Y"] != "changed" || states["F5"] != "missing" || got.Consistent || len(got.DifferenceID) != 64 || got.Dismissed {
		t.Fatalf("statuses = %+v", got)
	}

	// A stale revision is -32009 with the current one.
	err := il.call("integrations.putItems", map[string]any{"profile": "paint", "integration": "fake", "revision": "", "items": []any{}}, new(any))
	var rpcErr *jrpc2.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != -32009 || !strings.Contains(string(rpcErr.Data), rev) {
		t.Fatalf("conflict: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(il.dir, "profiles", "paint", "integrations", "fake.json")); err != nil || !strings.Contains(string(data), `"keys": "Ctrl+Y"`) {
		t.Fatalf("file = %s %v", data, err)
	}

	// The plan has an entry for every item that is not consistent.
	var plan struct {
		Entries []struct {
			Item    integration.Item
			Changes []integration.Change
			Skipped string
		}
	}
	if err := il.call("integrations.plan", map[string]any{"profile": "paint", "integration": "fake", "language": "en"}, &plan); err != nil || len(plan.Entries) != 2 {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if e := plan.Entries[0]; e.Item.Function != "command:a/undo" || len(e.Changes) != 1 || e.Changes[0].Add != "Ctrl+Y" {
		t.Fatalf("plan[0] = %+v", e)
	}
	if e := plan.Entries[1]; e.Skipped != "missing" {
		t.Fatalf("plan[1] = %+v", e)
	}
	raw := json.RawMessage{}
	if err := il.call("integrations.plan", map[string]any{"profile": "plain", "integration": "fake"}, &raw); err != nil || string(raw) != `{"entries":[]}` {
		t.Fatalf("empty plan = %s %v", raw, err)
	}
}

func TestIntegrationWriteBackupsRestore(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	var w struct {
		Backup   *rpcIntegrationBackup
		Revision string
	}
	changes := []map[string]any{{"function": "command:a/undo", "add": "ctrl+u"}, {"function": "command:a/redo", "remove": "Ctrl+Y"}}
	if err := il.call("integrations.write", map[string]any{"integration": "fake", "changes": changes}, &w); err != nil {
		t.Fatal(err)
	}
	if w.Backup == nil || w.Backup.ID != "b0" || w.Backup.Reason != "write" || w.Backup.Changes != 2 || w.Backup.Created != "2026-11-04T12:00:00Z" || w.Revision == "" {
		t.Fatalf("write = %+v", w)
	}
	il.fake.mu.Lock()
	got := il.fake.written[0]
	il.fake.mu.Unlock()
	if got[0].Add != "Ctrl+U" {
		t.Fatalf("keys not canonical: %+v", got)
	}
	var list []rpcIntegrationBackup
	if err := il.call("integrations.backups", map[string]any{"integration": "fake"}, &list); err != nil || len(list) != 1 || list[0].ID != "b0" {
		t.Fatalf("backups = %+v %v", list, err)
	}
	var pre rpcIntegrationBackup
	if err := il.call("integrations.restore", map[string]any{"integration": "fake", "backup": "b0"}, &pre); err != nil || pre.ID != "pre" || pre.Reason != "beforeRestore" || pre.Changes != 0 {
		t.Fatalf("restore = %+v %v", pre, err)
	}
	if err := il.call("integrations.backups", map[string]any{"integration": "fake"}, &list); err != nil || len(list) != 2 || list[0].ID != "pre" {
		t.Fatalf("backups after = %+v %v", list, err)
	}

	// The refusals.
	check := func(what string, code int, dataHas string, f func(), method string, params any) {
		t.Helper()
		f()
		err := il.call(method, params, new(any))
		var rpcErr *jrpc2.Error
		if !errors.As(err, &rpcErr) || int(rpcErr.Code) != code || !strings.Contains(string(rpcErr.Data), dataHas) {
			t.Errorf("%s: err = %v (data %s), want %d containing %q", what, err, func() string {
				if rpcErr != nil {
					return string(rpcErr.Data)
				}
				return ""
			}(), code, dataHas)
		}
	}
	write := map[string]any{"integration": "fake", "changes": changes}
	check("running", -32028, `"integration":"fake"`, func() { il.fake.setRunning(true) }, "integrations.write", write)
	check("running restore", -32028, `"integration":"fake"`, func() {}, "integrations.restore", map[string]any{"integration": "fake", "backup": "b0"})
	check("busy", -32030, "", func() {
		il.fake.setRunning(false)
		il.fake.mu.Lock()
		il.fake.writeErr = integration.ErrBusy
		il.fake.mu.Unlock()
	}, "integrations.write", write)
	check("unavailable", -32029, `"problem":"unknownFormat"`, func() {
		il.fake.mu.Lock()
		il.fake.writeErr = &integration.UnavailableError{Problem: integration.ProblemUnknownFormat}
		il.fake.mu.Unlock()
	}, "integrations.write", write)
	check("unknown function", -32602, `"reason":"unknownFunction"`, func() {
		il.fake.mu.Lock()
		il.fake.writeErr = nil
		il.fake.mu.Unlock()
	}, "integrations.write", map[string]any{"integration": "fake", "changes": []map[string]any{{"function": "command:a/nothing", "add": "F1"}}})
	// Nothing was written by the refusals.
	il.fake.mu.Lock()
	n := len(il.fake.written)
	il.fake.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d writes reached the integration", n)
	}
}

func TestIntegrationDismissPersists(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "F5", Function: "command:a/undo"})
	var got struct {
		DifferenceID string
		Dismissed    bool
	}
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &got); err != nil || got.DifferenceID == "" || got.Dismissed {
		t.Fatalf("before = %+v %v", got, err)
	}
	if err := il.call("integrations.dismiss", map[string]any{"profile": "paint", "integration": "fake", "differenceId": got.DifferenceID}, new(any)); err != nil {
		t.Fatal(err)
	}
	if id := il.cfg.opts.memory.Dismissed("paint", "fake"); id != got.DifferenceID {
		t.Fatalf("memory has %q", id)
	}
	var again struct {
		DifferenceID string
		Dismissed    bool
	}
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &again); err != nil || !again.Dismissed || again.DifferenceID != got.DifferenceID {
		t.Fatalf("after = %+v %v", again, err)
	}
	// Another set of differences is not the dismissed one.
	il.fake.setKeys("command:a/undo", "F6")
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &again); err != nil || again.Dismissed || again.DifferenceID == got.DifferenceID {
		t.Fatalf("other differences = %+v %v", again, err)
	}
}

type differenceEvent struct {
	Integration  string `json:"integration"`
	Kind         string `json:"kind"`
	Profile      string `json:"profile"`
	DifferenceID string `json:"differenceId"`
	Count        int    `json:"count"`
}

func (il *integrationLive) nextChanged(t *testing.T, kind string, d time.Duration) (differenceEvent, bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		data, ok := il.nextEvent(t, "integration.changed", time.Until(deadline))
		if !ok {
			return differenceEvent{}, false
		}
		var e differenceEvent
		if err := json.Unmarshal(data, &e); err != nil {
			t.Fatal(err)
		}
		if e.Kind == kind {
			return e, true
		}
	}
}

// TestIntegrationActivationCheck: a bound profile that becomes active with
// differences is announced once per set of differences: the topic, one
// overlay toast and one tray notice; not again for the same set, not after
// the person dismissed it, and again for a new set.
func TestIntegrationActivationCheck(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "F5", Function: "command:a/undo"}, integration.Item{Keys: "Ctrl+Y", Function: "command:a/redo"})
	il.subscribe(t, "integration.changed", "hud")

	il.activate(t, "paint")
	e, ok := il.nextChanged(t, "differences", 5*time.Second)
	if !ok || e.Integration != "fake" || e.Profile != "paint" || e.Count != 1 || len(e.DifferenceID) != 64 {
		t.Fatalf("event = %+v ok=%v", e, ok)
	}
	var toast struct {
		Kind, Text string
	}
	for {
		data, ok := il.nextEvent(t, "hud", time.Second)
		if !ok {
			t.Fatal("no overlay toast")
		}
		_ = json.Unmarshal(data, &toast)
		if toast.Kind == "toast" {
			break
		}
	}
	const want = "Paint：1 个快捷键和 Fake App 里的不同，打开 padmate 处理"
	if toast.Text != want {
		t.Fatalf("toast = %q", toast.Text)
	}
	il.waitUntil(t, "the tray notice", func() bool { return len(il.noticeList()) == 1 })
	if n := il.noticeList()[0]; n.Body != want || n.Title == "" || !strings.Contains(n.Key, e.DifferenceID) {
		t.Fatalf("notice = %+v", n)
	}

	// The same set again (the profile is activated again): nothing.
	il.activate(t, "plain")
	il.activate(t, "paint")
	if e, ok := il.nextChanged(t, "differences", 500*time.Millisecond); ok {
		t.Fatalf("announced again: %+v", e)
	}
	if n := len(il.noticeList()); n != 1 {
		t.Fatalf("%d notices", n)
	}

	// A new set while it is active: the files changed.
	il.fake.setKeys("command:a/redo", "F7")
	if f, ok := il.nextChanged(t, "files", 5*time.Second); !ok || f.Integration != "fake" {
		t.Fatalf("files = %+v ok=%v", f, ok)
	}
	e2, ok := il.nextChanged(t, "differences", 5*time.Second)
	if !ok || e2.DifferenceID == e.DifferenceID || e2.Count != 2 {
		t.Fatalf("second = %+v ok=%v", e2, ok)
	}

	// Dismissed: not announced when it comes back.
	if err := il.call("integrations.dismiss", map[string]any{"profile": "paint", "integration": "fake", "differenceId": e.DifferenceID}, new(any)); err != nil {
		t.Fatal(err)
	}
	il.fake.setKeys("command:a/redo", "Ctrl+Y")
	il.fake.setKeys("command:a/undo", "Ctrl+Z")
	il.fake.setKeys("command:a/redo", "Ctrl+Y")
	if e3, ok := il.nextChanged(t, "differences", 600*time.Millisecond); ok && e3.DifferenceID == e.DifferenceID {
		t.Fatalf("dismissed set announced: %+v", e3)
	}
}

// TestIntegrationWatcherIdleAndRunning: nothing is looked at while no bound
// profile is active and nobody subscribes; with a subscriber the running
// state is followed.
func TestIntegrationWatcherIdleAndRunning(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "Ctrl+Z", Function: "command:a/undo"})
	time.Sleep(200 * time.Millisecond)
	if n := il.fake.compareCalls(); n != 0 {
		t.Fatalf("%d comparisons while idle", n)
	}
	il.subscribe(t, "integration.changed")
	il.fake.setRunning(true) // not seen before the first look; give it one
	time.Sleep(100 * time.Millisecond)
	il.fake.setRunning(false)
	if e, ok := il.nextChanged(t, "running", 5*time.Second); !ok || e.Integration != "fake" {
		t.Fatalf("running = %+v ok=%v", e, ok)
	}
	// A consistent bound profile announces nothing.
	il.activate(t, "paint")
	if e, ok := il.nextChanged(t, "differences", 500*time.Millisecond); ok {
		t.Fatalf("consistent profile announced: %+v", e)
	}
	if len(il.noticeList()) != 0 {
		t.Fatal("notice for a consistent profile")
	}
}

// TestIntegrationNoNotifyWithoutNeed: the check of an unavailable software is
// quiet (logged once, nothing shown).
func TestIntegrationUnavailableIsQuiet(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "F5", Function: "command:a/undo"})
	il.fake.mu.Lock()
	il.fake.loc = integration.Location{Problem: integration.ProblemNotFound}
	il.fake.mu.Unlock()
	il.subscribe(t, "integration.changed", "hud")
	il.activate(t, "paint")
	if _, ok := il.nextChanged(t, "differences", 600*time.Millisecond); ok {
		t.Fatal("announced although the software is unavailable")
	}
	if len(il.noticeList()) != 0 {
		t.Fatal("notice although the software is unavailable")
	}
	if n := strings.Count(il.log.String(), "integration not readable"); n != 1 {
		t.Fatalf("logged %d times", n)
	}
}

// TestIntegrationUnusedItemsIgnored: an item whose keys no binding sends is
// returned by integrations.items but is not compared, planned or announced
// (ARCHITECTURE section 16.2).
func TestIntegrationUnusedItemsIgnored(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "F9", Function: "command:a/undo"}, integration.Item{Keys: "Ctrl+Z", Function: "command:a/undo"})
	var got struct {
		Items        []integration.Item
		Statuses     []integration.ItemStatus
		Consistent   bool
		DifferenceID string
	}
	if err := il.call("integrations.items", map[string]any{"profile": "paint", "integration": "fake"}, &got); err != nil ||
		len(got.Items) != 2 || len(got.Statuses) != 1 || got.Statuses[0].Keys != "Ctrl+Z" || !got.Consistent || got.DifferenceID != "" {
		t.Fatalf("items = %+v %v", got, err)
	}
	var plan struct{ Entries []any }
	if err := il.call("integrations.plan", map[string]any{"profile": "paint", "integration": "fake"}, &plan); err != nil || len(plan.Entries) != 0 {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	il.subscribe(t, "integration.changed")
	il.activate(t, "paint")
	if e, ok := il.nextChanged(t, "differences", 500*time.Millisecond); ok {
		t.Fatalf("announced an unused item: %+v", e)
	}
}

// integrations.setDataDir writes integrations.<id>.dataDir by byte position,
// applies it at once, and takes it out again with null.
func TestIntegrationSetDataDir(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	folder := t.TempDir()
	var saved rpcSaved
	if err := il.call("integrations.setDataDir", map[string]any{"integration": "fake", "dataDir": folder}, &saved); err != nil || !saved.Applied || saved.RestartRequired || saved.Revision == "" {
		t.Fatalf("set = %+v %v", saved, err)
	}
	var doc rpcDocument
	if err := il.call("config.get", nil, &doc); err != nil {
		t.Fatal(err)
	}
	cfg, _ := profile.ParseConfig([]byte(doc.Text))
	if cfg == nil || cfg.Integrations["fake"].DataDir != folder || len(cfg.Rules) != 4 {
		t.Fatalf("config after set: %s", doc.Text)
	}
	if got := il.selectionConfig().Integrations["fake"].DataDir; got != folder {
		t.Fatalf("live data folder = %q, want %q", got, folder)
	}
	var listed []rpcIntegration
	if err := il.call("integrations.list", nil, &listed); err != nil || len(listed) != 1 || !listed[0].Custom {
		t.Fatalf("list after set = %+v %v", listed, err)
	}
	// The same folder again changes nothing; a stale revision is refused.
	var again rpcSaved
	if err := il.call("integrations.setDataDir", map[string]any{"integration": "fake", "dataDir": folder}, &again); err != nil || again.Revision != saved.Revision {
		t.Fatalf("again = %+v %v", again, err)
	}
	if err := il.call("integrations.setDataDir", map[string]any{"integration": "fake", "dataDir": nil, "revision": "stale"}, new(any)); err == nil {
		t.Fatal("stale revision accepted")
	}
	if err := il.call("integrations.setDataDir", map[string]any{"integration": "fake", "dataDir": nil}, &saved); err != nil || !saved.Applied {
		t.Fatalf("clear = %+v %v", saved, err)
	}
	if got := il.selectionConfig().Integrations["fake"].DataDir; got != "" {
		t.Fatalf("live data folder after clear = %q", got)
	}
	listed = nil
	if err := il.call("integrations.list", nil, &listed); err != nil || listed[0].Custom {
		t.Fatalf("list after clear = %+v %v", listed, err)
	}
	if err := il.call("config.get", nil, &doc); err != nil || strings.Contains(doc.Text, "integrations") {
		t.Fatalf("config after clear: %s %v", doc.Text, err)
	}
}

func TestIntegrationSetDataDirRefuses(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		params map[string]any
		code   int
	}{
		{"unknown integration", map[string]any{"integration": "nope", "dataDir": nil}, -32004},
		{"bad id", map[string]any{"integration": "Bad Id", "dataDir": nil}, -32602},
		{"dataDir absent", map[string]any{"integration": "fake"}, -32602},
		{"empty", map[string]any{"integration": "fake", "dataDir": ""}, -32602},
		{"not a string", map[string]any{"integration": "fake", "dataDir": 5}, -32602},
		{"relative", map[string]any{"integration": "fake", "dataDir": "some/dir"}, -32602},
		{"missing folder", map[string]any{"integration": "fake", "dataDir": filepath.Join(t.TempDir(), "gone")}, -32602},
		{"a file", map[string]any{"integration": "fake", "dataDir": file}, -32602},
		{"too long", map[string]any{"integration": "fake", "dataDir": filepath.Join(t.TempDir(), strings.Repeat("a", 1100))}, -32602},
		{"extra member", map[string]any{"integration": "fake", "dataDir": nil, "x": 1}, -32602},
	} {
		if err := il.call("integrations.setDataDir", tt.params, new(any)); rpcCode(err) != tt.code {
			t.Errorf("%s: err = %v, want code %d", tt.name, err, tt.code)
		}
	}
}

// TestIntegrationDismissForgottenWhenConsistent: once the differences are
// gone, the "leave it alone" of that set is forgotten, so the same set
// coming back later is announced again.
func TestIntegrationDismissForgottenWhenConsistent(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.putItems(t, "paint", "", integration.Item{Keys: "F5", Function: "command:a/undo"})
	il.subscribe(t, "integration.changed")
	il.activate(t, "paint")
	e, ok := il.nextChanged(t, "differences", 5*time.Second)
	if !ok {
		t.Fatal("no first announcement")
	}
	if err := il.call("integrations.dismiss", map[string]any{"profile": "paint", "integration": "fake", "differenceId": e.DifferenceID}, new(any)); err != nil {
		t.Fatal(err)
	}
	// The software now has the key: consistent.
	il.fake.setKeys("command:a/undo", "F5")
	il.waitUntil(t, "the dismissal to be forgotten", func() bool { return il.cfg.opts.memory.Dismissed("paint", "fake") == "" })
	// The same difference again.
	il.fake.setKeys("command:a/undo", "Ctrl+Z")
	e2, ok := il.nextChanged(t, "differences", 5*time.Second)
	if !ok || e2.DifferenceID != e.DifferenceID {
		t.Fatalf("second = %+v ok=%v", e2, ok)
	}
}

// TestIntegrationActionIsQuiet: differences that the person's own action
// over IPC causes are published to the topic, but neither the overlay nor
// the tray is told.
func TestIntegrationActionIsQuiet(t *testing.T) {
	il := startIntegrationLive(t, "plain")
	il.fake.setKeys("command:a/undo", "Ctrl+Z")
	il.subscribe(t, "integration.changed", "hud")
	il.activate(t, "paint")
	time.Sleep(200 * time.Millisecond)
	// Saving an item the software does not have yet: a difference.
	il.putItems(t, "paint", "", integration.Item{Keys: "F5", Function: "command:a/undo"})
	e, ok := il.nextChanged(t, "differences", 5*time.Second)
	if !ok || e.Count != 1 {
		t.Fatalf("event = %+v ok=%v", e, ok)
	}
	time.Sleep(200 * time.Millisecond)
	if n := len(il.noticeList()); n != 0 {
		t.Fatalf("%d tray notices after the person's own action", n)
	}
	for {
		data, ok := il.nextEvent(t, "hud", 200*time.Millisecond)
		if !ok {
			break
		}
		var toast struct{ Kind string }
		if _ = json.Unmarshal(data, &toast); toast.Kind == "toast" {
			t.Fatal("overlay toast after the person's own action")
		}
	}
	// An outside change afterwards is announced as usual.
	il.fake.setKeys("command:a/undo", "F9")
	if _, ok := il.nextChanged(t, "differences", 5*time.Second); !ok {
		t.Fatal("outside change not published")
	}
	il.waitUntil(t, "the tray notice", func() bool { return len(il.noticeList()) == 1 })
}
