package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wdcyxxycdw/padmate/daemon/internal/control"
)

// TestStaleJoyConSettingsFallBackToDefaults: a config.json written when a
// single Joy-Con could be held sideways has values that a Joy-Con no longer
// has: a paddle as the function key, a combo of the sideways shoulders, the
// retired panic families. The file is not repaired (no backup, no notice):
// those values are warnings, the Joy-Con runs the defaults of its family, and
// the rest of the file applies (here, the other controller's own function key).
func TestStaleJoyConSettingsFallBackToDefaults(t *testing.T) {
	cfg := `{"schemaVersion":0,
  "safety":{"panic":{"joyconRight":{"buttons":["leftshoulder","rightshoulder"],"holdMs":900},"joyconRightVertical":{"buttons":["paddle3","paddle1"],"holdMs":900}}},
  "devices":{
    "` + padJoyConKey + `":{"functionKey":"paddle1","panic":{"buttons":["leftshoulder","rightshoulder"],"holdMs":1200},"feedback":{"rumble":false}},
    "` + padXboxKey + `":{"functionKey":"back"}}}`
	tl, _, c := startPadLive(t, cfg)

	if x, j := tl.slotFunctionKey(t, 0), tl.slotFunctionKey(t, 1); x != control.Back || j != control.RT {
		t.Errorf("function keys of the Xbox pad and the Joy-Con: %s, %s; want back and rt", x, j)
	}
	j := c.pad(padJoyConKey)
	if f := j.FunctionKey; f.Stored != "paddle1" || f.Effective != "rt" || f.Source != "default" {
		t.Errorf("function key = %+v; the stale paddle is kept in the file but not used", f)
	}
	if p := j.Panic; strings.Join(p.Effective.Buttons, "+") != "paddle3+paddle1" || p.Effective.HoldMs != 2000 || p.Source != "default" {
		t.Errorf("panic = %+v; the built-in combo applies", p)
	}
	if j.Feedback.Effective.Rumble {
		t.Errorf("feedback = %+v; the rest of its entry applies", j.Feedback)
	}
	combos := tl.combos(c)
	if got := combos[2]; got.Family != "joyconRight" || strings.Join(got.Buttons, "+") != "paddle3+paddle1" || got.HoldMs != 2000 {
		t.Errorf("combo of the Joy-Con = %+v; want the built-in one, which T-input detects", got)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got := statusRepair(ctx, t, c.client); got != nil {
		t.Errorf("the file was repaired: %+v", got)
	}
	if backups, _ := filepath.Glob(filepath.Join(tl.cfg.opts.dataDir, "config.broken-*.json")); len(backups) != 0 {
		t.Errorf("backups %v", backups)
	}
}

// combos returns the combos of status.get by device.
func (tl *testLive) combos(c *rpcTestConn) map[int]rpcCombo {
	c.t.Helper()
	var st rpcStatus
	if err := c.call("status.get", nil, &st); err != nil {
		c.t.Fatal(err)
	}
	out := map[int]rpcCombo{}
	for _, combo := range st.Combos {
		out[int(combo.Device)] = combo
	}
	return out
}
