package main

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2"

	"github.com/wdcyxxycdw/padmate/daemon/internal/amgp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

const amgpFile = `<?xml version="1.0" encoding="UTF-8"?>
<gamecontroller configversion="19" appversion="3.6.1">
    <sdlname>Test pad</sdlname>
    <profilename>From a pad</profilename>
    <sets>
        <set index="1">
            <button index="1">
                <slots>
                    <slot><code>0x1000021</code><mode>keyboard</mode></slot>
                    <slot><code>0x5a</code><mode>keyboard</mode></slot>
                </slots>
            </button>
            <button index="5">
                <slots>
                    <slot><code>0x41</code><mode>keyboard</mode></slot>
                </slots>
            </button>
        </set>
    </sets>
</gamecontroller>
`

// profiles.importAmgp (PRO-21) makes a new library profile from an AntiMicroX
// file, and says what it could not carry over.
func TestIPCImportAmgp(t *testing.T) {
	dir := t.TempDir()
	writeLibraryProfile(t, dir, "paint", `{"schemaVersion":0,"name":"paint","layers":{"base":{}}}`)
	tl, _ := startFocusLive(t, dir, "paint", `{"schemaVersion":0}`, false)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, events := libraryEvents(ctx, t, tl)
	encoded := base64.StdEncoding.EncodeToString([]byte(amgpFile))

	var imported rpcAmgpImported
	if err := client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": encoded}, &imported); err != nil {
		t.Fatal(err)
	}
	if imported.ID != "from-a-pad" || imported.Name != "From a pad" || imported.Applied {
		t.Fatalf("%+v", imported)
	}
	// The report says what it did. View is an ordinary control since ADR-0075,
	// so its key is imported and nothing is dropped.
	if len(imported.Report) == 0 || imported.Report[len(imported.Report)-1].Message == "" {
		t.Fatalf("report %+v", imported.Report)
	}
	for _, l := range imported.Report {
		if l.Severity == amgp.SeverityDropped {
			t.Errorf("a dropped line in the report: %+v", l)
		}
	}
	// The profile is in the library, as the importer made it, and loads.
	text, err := os.ReadFile(filepath.Join(dir, "profiles", "from-a-pad", "profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	p, ds := profile.Parse(text)
	if err := ds.Err(); err != nil || p.Name != "From a pad" || len(p.Layers) != 1 || len(p.Layers[0].Bindings) != 2 {
		t.Fatalf("%v %+v", err, p)
	}
	if e := waitLibraryEvent(ctx, t, events, "saved", "from-a-pad"); e["revision"] != imported.Revision || e["applied"] != false {
		t.Fatal(e)
	}

	// The same file again: the name is the file's, so it is numbered.
	if err := client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": encoded}, &imported); err != nil ||
		imported.ID != "from-a-pad-2" || imported.Name != "From a pad 2" {
		t.Fatalf("%+v %v", imported, err)
	}
	// A name the client chose is not: it is the client's to change.
	err = client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": encoded, "name": "paint"}, new(any))
	if data := rpcErrorData(t, err, rpcCodeProfileNameTaken); data != `{"id":"paint","name":"paint"}` {
		t.Fatal(data)
	}
	if err := client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": encoded, "name": "Mine"}, &imported); err != nil ||
		imported.ID != "mine" || imported.Name != "Mine" {
		t.Fatalf("%+v %v", imported, err)
	}
	// With neither profilename nor name, the file's name is used.
	noName := strings.Replace(amgpFile, "<profilename>From a pad</profilename>", "", 1)
	if err := client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(noName)), "fileName": "Left hand.gamecontroller.amgp"}, &imported); err != nil ||
		imported.Name != "Left hand" {
		t.Fatalf("%+v %v", imported, err)
	}

	if imported.Report == nil {
		t.Error("the report is never null")
	}
	// A file name given with the data names the profile when the file does not.
	if err := client.CallResult(ctx, "profiles.importAmgp", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(noName)), "fileName": "Sketch.amgp"}, &imported); err != nil || imported.Name != "Sketch" {
		t.Fatalf("%+v %v", imported, err)
	}

	// The daemon opens no file: a path, a UNC path and a device path are not
	// accepted whatever they point at (PRO-20), and nothing is read.
	local := filepath.Join(t.TempDir(), "Sketch.amgp")
	if err := os.WriteFile(local, []byte(noName), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name   string
		params map[string]any
		code   jrpc2.Code
		data   string
	}{
		{"neither", map[string]any{}, jrpc2.InvalidParams, ""},
		{"a path next to data", map[string]any{"data": encoded, "path": local}, jrpc2.InvalidParams, ""},
		{"a path", map[string]any{"path": local}, jrpc2.InvalidParams, ""},
		{"a network path", map[string]any{"path": `\\attacker\share\a.amgp`}, jrpc2.InvalidParams, ""},
		{"a device path", map[string]any{"path": `\\.\pipe\a`}, jrpc2.InvalidParams, ""},
		{"not base64", map[string]any{"data": "not base64!"}, jrpc2.InvalidParams, ""},
		{"blank name", map[string]any{"data": encoded, "name": " "}, jrpc2.InvalidParams, ""},
		{"extra member", map[string]any{"data": encoded, "extra": 1}, jrpc2.InvalidParams, ""},
		{"not xml", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte("hello"))}, rpcCodeAmgpInvalid, `{"reason":"xml"}`},
		{"joystick", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(`<joystick configversion="19"/>`))}, rpcCodeAmgpInvalid, `{"reason":"joystick"}`},
		{"old version", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(`<gamecontroller configversion="5"/>`))}, rpcCodeAmgpInvalid, `{"reason":"version"}`},
		{"doctype", map[string]any{"data": base64.StdEncoding.EncodeToString([]byte(`<!DOCTYPE a><gamecontroller configversion="19"/>`))}, rpcCodeAmgpInvalid, `{"reason":"doctype"}`},
		{"too large", map[string]any{"data": strings.Repeat("A", base64.StdEncoding.EncodedLen(amgp.MaxBytes)+4)}, rpcCodeAmgpInvalid, `{"reason":"size"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := client.CallResult(ctx, "profiles.importAmgp", tc.params, new(any))
			if data := rpcErrorData(t, err, tc.code); tc.data != "" && data != tc.data {
				t.Fatalf("%s", data)
			}
		})
	}
	// The refused imports left nothing behind.
	entries, err := os.ReadDir(filepath.Join(dir, "profiles"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if err != nil || len(names) != 7 {
		t.Fatalf("profiles/ holds %v (%v)", names, err)
	}
}
