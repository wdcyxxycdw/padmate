package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// identifyRig replaces rpcInvoke with a daemon that lists rows and answers
// devices.identify with answer, and records the calls it got.
type identifyRig struct {
	methods []string
	params  []any
	list    string
	listErr error
	answer  string
}

func newIdentifyRig(t *testing.T, list string) *identifyRig {
	t.Helper()
	rig := &identifyRig{list: list, answer: `{"rumble":true,"led":false,"player":false}`}
	old := rpcInvoke
	t.Cleanup(func() { rpcInvoke = old })
	rpcInvoke = func(_ context.Context, _ string, method string, p any, out io.Writer, _ bool) error {
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
		switch method {
		case "devices.list":
			if rig.listErr != nil {
				return rig.listErr
			}
			_, err = io.WriteString(out, rig.list)
		case "devices.identify":
			_, err = io.WriteString(out, rig.answer)
		}
		return err
	}
	return rig
}

const onePad = `[{"info":{"id":3,"name":"Pad"},"key":"0300/p:fake3","slot":0,"status":"mapped","disabled":false}]`

// TestIdentifyWithoutDevice (IN-13): with one controller connected the flag
// is optional, since the daemon needs one: padmatectl asks devices.list and
// identifies the only controller, whatever its status, by its connection ID.
// With none or several it does not pick one, and asks nothing of any.
func TestIdentifyWithoutDevice(t *testing.T) {
	rig := newIdentifyRig(t, onePad)
	code, stdout, stderr := runCmd(t, "identify")
	if code != 0 || stderr != "" || strings.TrimSpace(stdout) != rig.answer {
		t.Fatalf("identify = %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if want := []string{"devices.list", "devices.identify"}; !reflect.DeepEqual(rig.methods, want) {
		t.Errorf("calls %v, want %v", rig.methods, want)
	}
	if want := (map[string]any{"device": "3"}); !reflect.DeepEqual(rig.params[1], want) {
		t.Errorf("identify params %v, want %v", rig.params[1], want)
	}

	// A flag given needs no lookup.
	rig = newIdentifyRig(t, onePad)
	if code, _, stderr := runCmd(t, "identify", "--device", "7"); code != 0 {
		t.Fatalf("identify --device 7 = %d, %s", code, stderr)
	}
	if want := []string{"devices.identify"}; !reflect.DeepEqual(rig.methods, want) {
		t.Errorf("calls %v, want only %v", rig.methods, want)
	}

	for _, tc := range []struct {
		name  string
		list  string
		err   error
		code  int
		stder string
	}{
		{"none", `[]`, nil, 1, "no controller is connected"},
		{"several", `[{"info":{"id":1}},{"info":{"id":2}}]`, nil, 2, "--device"},
		{"not a list", `{"oops":1}`, nil, 1, "devices.list"},
		{"no id", `[{"info":{"name":"x"}}]`, nil, 1, "devices.list"},
		{"list fails", ``, errors.New("daemon is gone"), 1, "daemon is gone"},
	} {
		rig = newIdentifyRig(t, tc.list)
		rig.listErr = tc.err
		code, stdout, stderr := runCmd(t, "identify")
		if code != tc.code || stdout != "" || !strings.Contains(stderr, tc.stder) {
			t.Errorf("%s: identify = %d, stdout %q, stderr %q; want %d and %q", tc.name, code, stdout, stderr, tc.code, tc.stder)
		}
		if want := []string{"devices.list"}; !reflect.DeepEqual(rig.methods, want) {
			t.Errorf("%s: calls %v, want only %v", tc.name, rig.methods, want)
		}
	}

	// A refusal of the daemon is a failure, with its message.
	newIdentifyRig(t, onePad) // for its cleanup of rpcInvoke
	rpcInvoke = func(_ context.Context, _ string, method string, _ any, out io.Writer, _ bool) error {
		if method == "devices.list" {
			_, err := io.WriteString(out, onePad)
			return err
		}
		return errors.New("device is not connected")
	}
	if code, _, stderr := runCmd(t, "identify"); code != 1 || !strings.Contains(stderr, "device is not connected") {
		t.Errorf("identify of a controller that left = %d, %q", code, stderr)
	}
}

// padmatectl identify is a command with a summary, and usage names it.
func TestIdentifyIsListed(t *testing.T) {
	if _, ok := commands["identify"]; !ok {
		t.Fatal("identify is not a command")
	}
	_, stdout, _ := runCmd(t, "identify", "--help")
	for _, want := range []string{"identify [--device ID]", "fake effects [--clear]", "fake battery STATE [PERCENT]"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("usage lacks %q:\n%s", want, stdout)
		}
	}
}
