package cspnames

import (
	"errors"
	"reflect"
	"testing"
)

func TestParseResourceSynthetic(t *testing.T) {
	r, err := ParseResource(synthResource())
	if err != nil {
		t.Fatal(err)
	}
	wantCmds := []string{"filenew", "exportpng", "exportjpg", "filequit", "undo", "noname", "tablecmd", "canvasscrolldown"}
	if !reflect.DeepEqual(r.Commands, wantCmds) {
		t.Errorf("Commands = %v, want %v", r.Commands, wantCmds)
	}
	want := []MenuCommand{
		{ID: "filenew", Name: "New", Path: []string{"File"}, Menu: 10},
		{ID: "exportpng", Name: "PNG", Path: []string{"File", "Export"}, Menu: 10},
		{ID: "exportjpg", Name: "JPEG", Path: []string{"File", "Export"}, Menu: 10},
		{ID: "filequit", Name: "Quit", Path: []string{"File"}, Menu: 10},
		{ID: "undo", Name: "Undo", Path: []string{"Edit"}, Menu: 20},
		{ID: "filenew", Name: "Dup", Path: []string{"Edit"}, Menu: 20},
		{ID: "noname", Name: "Orphan", Path: nil, Menu: 30},
	}
	if !reflect.DeepEqual(r.Menu, want) {
		t.Errorf("Menu =\n%+v\nwant\n%+v", r.Menu, want)
	}
	if len(r.Tops) != 6 || r.Tops[0] != (MenuTop{10, "File"}) || r.Tops[2] != (MenuTop{30, ""}) {
		t.Errorf("Tops = %+v", r.Tops)
	}
}

func TestStringRef(t *testing.T) {
	r, err := ParseResource(synthResource())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		ref  uint32
		want string
		ok   bool
	}{
		{333<<16 | 1, "text one", true},
		{333<<16 | 2, "text two", true},
		{333<<16 | 3, "", false}, // length does not match the size of the entry
		{333<<16 | 4, "", false},
		{13<<16 | 11, "File", true}, // the big table is the main block's string directory
		{1, "", false},              // block 0 is not a block
		{999<<16 | 1, "", false},
	}
	for _, tt := range tests {
		got, ok := r.StringRef(tt.ref)
		if got != tt.want || ok != tt.ok {
			t.Errorf("StringRef(%#x) = %q, %v; want %q, %v", tt.ref, got, ok, tt.want, tt.ok)
		}
	}
}

func TestParseResourceErrors(t *testing.T) {
	good := synthResource()
	tests := map[string][]byte{
		"empty":         nil,
		"short":         {0, 0, 0, 3},
		"garbage":       []byte("this is not a CELSYS resource at all, just text"),
		"truncated":     good[:len(good)/2],
		"no commands":   synthResource(func(s *synth) { s.noCommands = true }),
		"no main block": synthResource(func(s *synth) { s.badMain = true }),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseResource(data); !errors.Is(err, ErrUnknownFormat) {
				t.Errorf("err = %v, want ErrUnknownFormat", err)
			}
		})
	}
	big := make([]byte, maxResourceSize+1)
	if _, err := ParseResource(big); !errors.Is(err, ErrUnknownFormat) {
		t.Errorf("oversize: err = %v", err)
	}
}

func TestParseResourceCorruption(t *testing.T) {
	good := synthResource()
	// Every single-byte corruption and every truncation must be handled
	// without a panic; the result is either a parse or ErrUnknownFormat.
	for i := 0; i < len(good); i += 7 {
		for _, v := range []byte{0x00, 0xff, 0x0d} {
			bad := append([]byte(nil), good...)
			bad[i] = v
			checkNoPanic(t, bad)
		}
	}
	for n := 0; n < len(good); n += 11 {
		checkNoPanic(t, good[:n])
	}
}

func checkNoPanic(t *testing.T, data []byte) {
	t.Helper()
	r, err := ParseResource(data)
	if err != nil {
		if !errors.Is(err, ErrUnknownFormat) {
			t.Fatalf("err = %v, want ErrUnknownFormat", err)
		}
		return
	}
	for _, c := range r.Menu {
		_ = c.Name
	}
	r.StringRef(333<<16 | 1)
}

func FuzzParseResource(f *testing.F) {
	f.Add(synthResource())
	f.Add(synthResource(func(s *synth) { s.noCommands = true }))
	f.Add([]byte{})
	f.Add([]byte{0, 0, 0, 3, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := ParseResource(data)
		if err != nil {
			if !errors.Is(err, ErrUnknownFormat) {
				t.Fatalf("err = %v, want ErrUnknownFormat", err)
			}
			return
		}
		if len(r.Menu) == 0 {
			t.Fatal("parsed without menu commands")
		}
		for _, c := range r.Menu {
			if c.ID == "" || c.Name == "" {
				t.Fatalf("empty id or name: %+v", c)
			}
		}
		r.StringRef(333<<16 | 1)
		r.StringRef(13<<16 | 11)
	})
}
