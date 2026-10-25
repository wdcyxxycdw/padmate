package profile

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestMigrateChain(t *testing.T) {
	if CurrentSchema != 1 {
		t.Fatalf("CurrentSchema = %d; the profile format is frozen at 1 (PRO-01)", CurrentSchema)
	}
	if len(migrations) != CurrentSchema {
		t.Fatal("migration chain and schema version differ")
	}
}

// A document of the current version keeps its exact bytes: whitespace, BOM,
// line endings, and the spelling of the number.
func TestMigrateCurrent(t *testing.T) {
	for _, version := range []string{"1", "1.0", "1e0", "1E+0", "10e-1"} {
		data := []byte("\xef\xbb\xbf{\r\n  \"schemaVersion\": " + version + ", \"layers\": {\"base\": {}}\r\n}")
		before := bytes.Clone(data)
		got, ds := Migrate(data)
		if ds.HasErrors() || !bytes.Equal(got, before) || !bytes.Equal(data, before) {
			t.Fatalf("current document changed: %q, %v", got, ds)
		}
		got[0] = 'x'
		if !bytes.Equal(data, before) {
			t.Fatal("migration result aliases input")
		}
	}
}

// The 0 to 1 migration (PRO-06): the formats are identical, so only the
// value of schemaVersion changes. Every other byte is retained and the
// document keeps its length, so a position in the old text is a position in
// the new one.
func TestMigrateFromZero(t *testing.T) {
	for _, tc := range []struct{ name, old, want string }{
		{"compact", `{"schemaVersion":0,"name":"n","layers":{"base":{}}}`,
			`{"schemaVersion":1,"name":"n","layers":{"base":{}}}`},
		{"spaced", "{\n  \"schemaVersion\" :  0 ,\n  \"name\": \"n\"\n}\n",
			"{\n  \"schemaVersion\" :  1 ,\n  \"name\": \"n\"\n}\n"},
		{"bom and crlf", "\xef\xbb\xbf{\r\n  \"schemaVersion\": 0,\r\n  \"name\": \"绘画\",\r\n  \"layers\": {\"base\": {}}\r\n}\r\n",
			"\xef\xbb\xbf{\r\n  \"schemaVersion\": 1,\r\n  \"name\": \"绘画\",\r\n  \"layers\": {\"base\": {}}\r\n}\r\n"},
		{"version last", `{"name":"n","layers":{"base":{}},"schemaVersion":0}`,
			`{"name":"n","layers":{"base":{}},"schemaVersion":1}`},
		{"no trailing newline, leading space", ` {"schemaVersion":0}`, ` {"schemaVersion":1}`},
		{"other zero", `{"schemaVersion":0,"x":{"schemaVersion":0}}`, `{"schemaVersion":1,"x":{"schemaVersion":0}}`},
		// Longer spellings of zero: the new number is padded with spaces.
		{"0.0", `{"schemaVersion":0.0,"name":"n"}`, `{"schemaVersion":1  ,"name":"n"}`},
		{"0e0", `{"schemaVersion": 0e0 ,"name":"n"}`, `{"schemaVersion": 1   ,"name":"n"}`},
		{"-0", `{"schemaVersion":-0}`, `{"schemaVersion":1 }`},
		{"0E+00", `{"schemaVersion":0E+00,"a":1}`, `{"schemaVersion":1    ,"a":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := []byte(tc.old)
			got, ds := Migrate(data)
			if ds.HasErrors() || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, ds, tc.want)
			}
			if string(data) != tc.old {
				t.Fatal("the input changed")
			}
			if len(got) != len(data) {
				t.Fatalf("length %d, want %d", len(got), len(data))
			}
			again, ds := Migrate(got)
			if ds.HasErrors() || !bytes.Equal(again, got) {
				t.Fatalf("a migrated document is not current: %q, %v", again, ds)
			}
		})
	}
}

func TestMigrateRejects(t *testing.T) {
	for _, data := range []string{
		`{"schemaVersion":-1}`, `{"schemaVersion":0.5}`, `{"schemaVersion":1.5}`,
		`{"schemaVersion":"0"}`, `{"schemaVersion":"1"}`, `{"schemaVersion":null}`, `{}`, `[]`,
		`{"schemaVersion":0,"schemaVersion":0}`, `{"schemaVersion":0,}`, `{"schemaVersion":1,}`,
		`{"schemaVersion":1e999}`, `{"schemaVersion":0} {}`,
		`{"schemaVersion":1e-999}`, `{"schemaVersion":-1e-999}`,
	} {
		t.Run(data, func(t *testing.T) {
			got, ds := Migrate([]byte(data))
			if got != nil || !ds.HasErrors() || ds[0].Line == 0 {
				t.Fatalf("got %q, %v", got, ds)
			}
		})
	}
}

// A document of a version newer than this build is refused, never read
// partially (PRO-06); the message says what to do.
func TestMigrateRefusesNewer(t *testing.T) {
	for _, version := range []string{"2", "3", "2.0", "2e0", "100"} {
		data := []byte(`{"schemaVersion": ` + version + `, "name": "n", "layers": {"base": {}}}`)
		got, ds := Migrate(data)
		if got != nil || !ds.HasErrors() || ds[0].Pointer != "/schemaVersion" ||
			!strings.Contains(ds[0].Message, "is newer than supported version 1; upgrade padmate") {
			t.Fatalf("schemaVersion %s: got %q, %v", version, got, ds)
		}
		if p, ds := Parse(data); p != nil || !ds.HasErrors() || !strings.Contains(ds[0].Message, "upgrade padmate") {
			t.Fatalf("Parse of schemaVersion %s: got %v, %v", version, p, ds)
		}
	}
}

func TestMigrationChain(t *testing.T) {
	old := []byte(`{"schemaVersion":0,"name":"old"}`)
	steps := []migration{
		func(data []byte) ([]byte, error) {
			return bytes.Replace(data, []byte(`"schemaVersion":0`), []byte(`"schemaVersion":1`), 1), nil
		},
		func(data []byte) ([]byte, error) {
			data = bytes.ReplaceAll(data, []byte(`"old"`), []byte(`"new"`))
			return bytes.Replace(data, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":2`), 1), nil
		},
	}
	got, ds := migrate(old, steps)
	if ds.HasErrors() || string(got) != `{"schemaVersion":2,"name":"new"}` {
		t.Fatalf("got %s, %v", got, ds)
	}
	again, ds := migrate(got, steps)
	if ds.HasErrors() || !bytes.Equal(got, again) || string(old) != `{"schemaVersion":0,"name":"old"}` {
		t.Fatalf("not idempotent or input changed: %s, %v", again, ds)
	}
	for _, step := range []migration{
		func(b []byte) ([]byte, error) { b[0] = '!'; return nil, errors.New("broken step") },
		func(b []byte) ([]byte, error) { return b, nil },
		func([]byte) ([]byte, error) { return []byte(`{"schemaVersion":2}`), nil },
		func([]byte) ([]byte, error) { return []byte(`{"schemaVersion":1,}`), nil },
		nil,
	} {
		got, ds = migrate(old, []migration{step})
		if got != nil || !ds.HasErrors() || !strings.Contains(string(old), `"name":"old"`) || old[0] != '{' {
			t.Fatalf("failed migration exposed partial data: %q, %v, original %q", got, ds, old)
		}
	}
}

// migrateV0 on a document that has no version to change fails instead of
// returning the input as if it were upgraded.
func TestMigrateV0Fails(t *testing.T) {
	for _, data := range []string{`{}`, `{"schemaVersion":`, `[]`, `{"name":"n"}`} {
		if got, err := migrateV0([]byte(data)); err == nil {
			t.Errorf("migrateV0(%q) = %q, want an error", data, got)
		}
	}
}

func FuzzMigrate(f *testing.F) {
	for _, seed := range []string{
		`{"schemaVersion":0}`, `[]`, `{"schemaVersion":1}`, `{"schemaVersion":2}`,
		`{"schemaVersion":0,"schemaVersion":1}`, `{"schemaVersion":0.0}`, "\xef\xbb\xbf{\"schemaVersion\":-0e0}",
	} {
		f.Add([]byte(seed))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		before := bytes.Clone(data)
		got, ds := Migrate(data)
		if !bytes.Equal(data, before) {
			t.Fatal("input changed")
		}
		if ds.HasErrors() {
			if got != nil {
				t.Fatal("partial result on failure")
			}
			return
		}
		if len(got) != len(data) {
			t.Fatal("a migration moved the offsets of the document")
		}
		again, ds := Migrate(got)
		if ds.HasErrors() || !bytes.Equal(got, again) {
			t.Fatal("not idempotent")
		}
	})
}
