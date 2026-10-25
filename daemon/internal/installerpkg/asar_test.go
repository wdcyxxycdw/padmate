package installerpkg

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// buildAsar writes files (name to content, folders separated by "/") the way
// @electron/asar does, with its header pickle padded to 4 bytes.
func buildAsar(t testing.TB, files map[string]string, order []string) []byte {
	t.Helper()
	root := map[string]any{}
	var content []byte
	for _, name := range order {
		data := files[name]
		node := map[string]any{"size": len(data), "offset": strconv.Itoa(len(content))}
		content = append(content, data...)
		parts := strings.Split(name, "/")
		dir := root
		for _, part := range parts[:len(parts)-1] {
			d, ok := dir[part].(map[string]any)
			if !ok {
				d = map[string]any{"files": map[string]any{}}
				dir[part] = d
			}
			dir = d["files"].(map[string]any)
		}
		dir[parts[len(parts)-1]] = node
	}
	index, err := json.Marshal(map[string]any{"files": root})
	if err != nil {
		t.Fatal(err)
	}
	padded := (len(index) + 3) &^ 3
	var out bytes.Buffer
	for _, v := range []uint32{4, uint32(padded + 8), uint32(padded + 4), uint32(len(index))} {
		_ = binary.Write(&out, binary.LittleEndian, v)
	}
	out.Write(index)
	out.Write(make([]byte, padded-len(index)))
	out.Write(content)
	return out.Bytes()
}

func TestReadAsarFile(t *testing.T) {
	files := map[string]string{
		"package.json": `{"name":"padmate","version":"0.14.0-rc.1"}`,
		"out/a.js":     "console.log(1)",
		"out/b.json":   "{}",
	}
	archive := bytes.NewReader(buildAsar(t, files, []string{"out/a.js", "package.json", "out/b.json"}))
	for name, want := range files {
		got, err := ReadAsarFile(archive, name)
		if err != nil || string(got) != want {
			t.Errorf("ReadAsarFile(%q) = %q, %v; want %q", name, got, err, want)
		}
	}
	for _, name := range []string{"missing", "out", "out/nope.js", "package.json/x"} {
		if got, err := ReadAsarFile(archive, name); err == nil {
			t.Errorf("ReadAsarFile(%q) = %q, want an error", name, got)
		}
	}
}

func TestReadAsarFileRejectsNonArchives(t *testing.T) {
	good := buildAsar(t, map[string]string{"package.json": "{}"}, []string{"package.json"})
	for name, data := range map[string][]byte{
		"empty":     nil,
		"short":     good[:10],
		"zeros":     make([]byte, 64),
		"truncated": good[:len(good)-1],
		"huge":      append([]byte{4, 0, 0, 0, 0xff, 0xff, 0xff, 0x7f, 0xff, 0xff, 0xff, 0x7f, 0xf0, 0xff, 0xff, 0x7f}, make([]byte, 32)...),
	} {
		if got, err := ReadAsarFile(bytes.NewReader(data), "package.json"); err == nil {
			t.Errorf("%s: ReadAsarFile = %q, want an error", name, got)
		}
	}
}

func FuzzReadAsarFile(f *testing.F) {
	f.Add(buildAsar(f, map[string]string{"package.json": "{}"}, []string{"package.json"}), "package.json")
	f.Add([]byte{4, 0, 0, 0, 8, 0, 0, 0, 4, 0, 0, 0, 2, 0, 0, 0, '{', '}'}, "x")
	f.Fuzz(func(t *testing.T, data []byte, name string) {
		_, _ = ReadAsarFile(bytes.NewReader(data), name)
	})
}
