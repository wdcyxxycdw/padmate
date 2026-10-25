package main

import (
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"testing"
)

// latencyRig replaces rpcInvoke with a daemon that answers latency.get and
// latency.reset, and records the calls it got.
type latencyRig struct {
	methods []string
	params  []any
	compact []bool
}

const latencyAnswer = `{"devices":[{"device":"/p:fake1","id":1,"name":"Pad","samples":3,"p50":120,"p90":200,"p99":310,"max":410,"warn":false}],"dryRun":false,"warnP99Us":1500,"warnMinSamples":50}`

func newLatencyRig(t *testing.T) *latencyRig {
	t.Helper()
	rig := &latencyRig{}
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	rpcInvoke = func(_ context.Context, _ string, method string, p any, out io.Writer, compact bool) error {
		rig.methods = append(rig.methods, method)
		data, err := json.Marshal(p)
		if err != nil {
			return err
		}
		var decoded any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return err
		}
		rig.params = append(rig.params, decoded)
		rig.compact = append(rig.compact, compact)
		switch method {
		case "latency.get":
			_, err = io.WriteString(out, latencyAnswer)
		case "latency.reset":
			_, err = io.WriteString(out, `{"reset":true}`)
		}
		return err
	}
	return rig
}

// TestLatencyCommand (DMN-15, ADR-0071): padmatectl latency prints the result of
// latency.get as JSON like the other daemon commands; --device selects one
// controller, --reset calls latency.reset (of that controller, else of all)
// instead, and --json is compact. It takes no positional arguments.
func TestLatencyCommand(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		method string
		params map[string]any
		answer string
		json   bool
	}{
		{"get", []string{"latency"}, "latency.get", map[string]any{}, latencyAnswer, false},
		{"device", []string{"latency", "--device", "/p:fake1"}, "latency.get", map[string]any{"device": "/p:fake1"}, latencyAnswer, false},
		{"compact", []string{"latency", "--json"}, "latency.get", map[string]any{}, latencyAnswer, true},
		{"reset", []string{"latency", "--reset"}, "latency.reset", map[string]any{}, `{"reset":true}`, false},
		{"reset one", []string{"latency", "--reset", "--device", "3"}, "latency.reset", map[string]any{"device": "3"}, `{"reset":true}`, false},
	} {
		rig := newLatencyRig(t)
		code, stdout, stderr := runCmd(t, tc.args...)
		if code != 0 || stderr != "" || strings.TrimSpace(stdout) != tc.answer {
			t.Errorf("%s: %v = %d, stdout %q, stderr %q", tc.name, tc.args, code, stdout, stderr)
			continue
		}
		if !reflect.DeepEqual(rig.methods, []string{tc.method}) || !reflect.DeepEqual(rig.params, []any{tc.params}) {
			t.Errorf("%s: calls %v %v, want %s %v", tc.name, rig.methods, rig.params, tc.method, tc.params)
		}
		if rig.compact[0] != tc.json {
			t.Errorf("%s: compact %v, want %v", tc.name, rig.compact[0], tc.json)
		}
	}

	for _, args := range [][]string{{"latency", "extra"}, {"latency", "--reset", "extra"}} {
		rig := newLatencyRig(t)
		code, stdout, stderr := runCmd(t, args...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, "invalid daemon command") || len(rig.methods) != 0 {
			t.Errorf("%v = %d, stdout %q, stderr %q, calls %v; want 2 and the usage, no call", args, code, stdout, stderr, rig.methods)
		}
	}
}

// TestLatencyIsListed: padmatectl latency is a command with a summary, and
// usage names it.
func TestLatencyIsListed(t *testing.T) {
	if _, ok := commands["latency"]; !ok {
		t.Fatal("latency is not a command")
	}
	_, stdout, _ := runCmd(t, "latency", "--help")
	if !strings.Contains(stdout, "latency [--device ID] [--reset]") {
		t.Errorf("usage lacks the latency line:\n%s", stdout)
	}
}
