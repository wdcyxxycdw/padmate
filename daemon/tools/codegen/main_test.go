package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func schemaSandbox(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "schema"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"profile", "config", "state"} {
		data, err := os.ReadFile(filepath.Join("../../..", "schema", kind+".schema.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "schema", kind+".schema.json"), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func editProfileSchema(t *testing.T, root string, edit func(node)) {
	t.Helper()
	path := filepath.Join(root, "schema/profile.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var schema node
	if err := json.Unmarshal(data, &schema); err != nil {
		t.Fatal(err)
	}
	edit(schema)
	data, err = json.Marshal(schema)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaChangesDriveGoTSAndGenCheck(t *testing.T) {
	root := schemaSandbox(t)
	old, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(old, false); err != nil {
		t.Fatal(err)
	}
	editProfileSchema(t, root, func(schema node) { obj(schema["properties"])["extra"] = map[string]any{"type": "boolean"} })
	assertTypes := func(goFragment, tsFragment string) {
		t.Helper()
		files, err := generate(root)
		if err != nil {
			t.Fatal(err)
		}
		generatedGo := strings.Join(strings.Fields(string(files[filepath.Join(root, "daemon/internal/profile/wire_generated.go")])), " ")
		if !strings.Contains(generatedGo, goFragment) {
			t.Fatal("schema change missing from Go type", goFragment)
		}
		if !strings.Contains(string(files[filepath.Join(root, "app/src/shared/generated/schema.ts")]), tsFragment) {
			t.Fatal("schema change missing from TS type", tsFragment)
		}
		if err := writeFiles(files, true); err == nil {
			t.Fatal("old outputs survived a schema change")
		}
	}
	assertTypes("*bool `json:\"extra\"`", `"extra"?: boolean;`)
	editProfileSchema(t, root, func(schema node) { schema["required"] = append(arr(schema["required"]), "extra") })
	assertTypes("bool `json:\"extra\"`", `"extra": boolean;`)
	editProfileSchema(t, root, func(schema node) {
		obj(schema["properties"])["extra"] = map[string]any{"type": []any{"boolean", "null"}}
	})
	assertTypes("*bool `json:\"extra\"`", `"extra": boolean | null;`)
	editProfileSchema(t, root, func(schema node) { obj(schema["properties"])["extra"] = map[string]any{"$ref": "#/$defs/timing"} })
	assertTypes("wireTiming `json:\"extra\"`", `"extra": ProfileTiming;`)
}

// TestSkippedPropertyStaysValidButUntyped: a property marked x-go-skip and
// x-ts-never, such as a deprecated config.json member, has no Go field and
// may not be written from TypeScript; the schema still accepts it.
func TestSkippedPropertyStaysValidButUntyped(t *testing.T) {
	root := schemaSandbox(t)
	editProfileSchema(t, root, func(schema node) {
		obj(schema["properties"])["legacy"] = map[string]any{"type": "string", "deprecated": true, "x-go-skip": true, "x-ts-never": true}
	})
	files, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	if goTypes := string(files[filepath.Join(root, "daemon/internal/profile/wire_generated.go")]); strings.Contains(goTypes, `json:"legacy"`) {
		t.Fatal("skipped property has a Go field")
	}
	if tsTypes := string(files[filepath.Join(root, "app/src/shared/generated/schema.ts")]); !strings.Contains(tsTypes, `"legacy"?: never;`) {
		t.Fatal("skipped property is writable from TypeScript")
	}
}

func TestGoRepresentationsRejectSchemaDrift(t *testing.T) {
	for _, tc := range []struct {
		name     string
		property node
	}{
		{"scalar kind", node{"type": "boolean", "x-go-type": "string"}},
		{"nullable scalar", node{"type": []any{"string", "null"}, "x-go-type": "string"}},
		{"wrong reference", node{"$ref": "#/$defs/timing", "x-go-type": "wireFeedback"}},
		{"raw scalar", node{"type": "integer", "x-go-type": "json.RawMessage"}},
		{"unrepresented union", node{"oneOf": []any{map[string]any{"type": "boolean"}, map[string]any{"type": "string"}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := schemaSandbox(t)
			editProfileSchema(t, root, func(schema node) { obj(schema["properties"])["extra"] = map[string]any(tc.property) })
			if _, err := generate(root); err == nil {
				t.Fatal("incompatible representation silently accepted")
			}
		})
	}
}

func TestGenerateSchemaTypesAndDetectDrift(t *testing.T) {
	root := filepath.Clean("../../..")
	files, err := generate(root)
	if err != nil {
		t.Fatal(err)
	}
	goTypes := string(files[filepath.Join(root, "daemon/internal/profile/wire_generated.go")])
	tsTypes := string(files[filepath.Join(root, "app/src/shared/generated/schema.ts")])
	for _, name := range []string{"type wireProfile struct", "type wireConfig struct", "type State struct"} {
		if !strings.Contains(goTypes, name) {
			t.Fatal("missing consumed Go type", name)
		}
	}
	for _, name := range []string{"export type Profile =", "export type Config =", "export type State =", "ProfileKeysAction | ProfileLayerAction | ProfileScriptAction | ProfileProfileAction"} {
		if !strings.Contains(tsTypes, name) {
			t.Fatal("missing TS schema type", name)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "generated.go")
	if err := writeFiles(map[string][]byte{path: []byte("current")}, false); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(map[string][]byte{path: []byte("current")}, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("drift"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFiles(map[string][]byte{path: []byte("current")}, true); err == nil {
		t.Fatal("gen-check accepted stale output")
	}
}
