package cspnames

import (
	"encoding/binary"
	"reflect"
	"testing"
	"time"
)

func TestReadPEStrings(t *testing.T) {
	in := map[uint16]map[uint32]string{
		0x804: {96: "效果", 97: "马赛克", 98: "x"},
		0x009: {96: "Effect", 97: "Mosaic"},
		0x411: {96: "エフェクト"},
	}
	got, err := ReadPEStrings(synthPE(in))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(map[uint16]map[uint32]string(got), in) {
		t.Errorf("strings = %v, want %v", got, in)
	}
}

func TestPluginName(t *testing.T) {
	s := PEStrings{
		0x804: {96: "效果", 97: "马赛克"},
		0x009: {96: "Effect", 97: "Mosaic", 98: "Mosaic 2"},
	}
	tests := []struct {
		langs     []uint16
		name, cat string
		wantErr   bool
		strs      PEStrings
	}{
		{langs: pluginLangsZH, name: "马赛克", cat: "效果", strs: s},
		{langs: pluginLangsEN, name: "Mosaic", cat: "Effect", strs: s},
		// A language without the string falls back to the next one.
		{langs: pluginLangsZH, name: "Mosaic", cat: "Effect", strs: PEStrings{0x009: s[0x009]}},
		// File type descriptions of import and export plug-ins are not names.
		{langs: pluginLangsEN, wantErr: true, strs: PEStrings{0x009: {100: "x", 102: "psd|psd"}}},
		{langs: pluginLangsEN, wantErr: true, strs: PEStrings{0x009: {100: "only one"}}},
		{langs: pluginLangsEN, wantErr: true, strs: PEStrings{}},
	}
	for i, tt := range tests {
		name, cat, err := tt.strs.pluginName(tt.langs)
		if (err != nil) != tt.wantErr || name != tt.name || cat != tt.cat {
			t.Errorf("case %d: got %q, %q, %v; want %q, %q, error=%v", i, name, cat, err, tt.name, tt.cat, tt.wantErr)
		}
	}
}

func TestReadPEStringsBadInput(t *testing.T) {
	good := synthPE(map[uint16]map[uint32]string{0x9: {96: "a", 97: "b"}})
	if _, err := ReadPEStrings(nil); err == nil {
		t.Error("nil: want an error")
	}
	if _, err := ReadPEStrings([]byte("MZ not a pe file")); err == nil {
		t.Error("garbage: want an error")
	}
	for i := 0x1c0; i < len(good); i += 3 {
		bad := append([]byte(nil), good...)
		bad[i] ^= 0xff
		_, _ = ReadPEStrings(bad) // must not panic
	}
	for n := 0; n < len(good); n += 13 {
		_, _ = ReadPEStrings(good[:n])
	}
}

// TestReadPEStringsSelfReference: a directory whose entries all point back to
// itself, with the most entries allowed, ends quickly instead of walking
// every combination of its entries.
func TestReadPEStringsSelfReference(t *testing.T) {
	data := selfReferencingPE()
	start := time.Now()
	got, err := ReadPEStrings(data)
	if err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

// selfReferencingPE is a PE whose RT_STRING directory has maxPEEntries
// entries that all point back to the directory itself.
func selfReferencingPE() []byte {
	le := binary.LittleEndian
	const n = maxPEEntries
	rsrc := make([]byte, 24+16+8*n)
	le.PutUint16(rsrc[14:], 1)
	le.PutUint32(rsrc[16:], rtString)
	le.PutUint32(rsrc[20:], 0x80000000|24)
	le.PutUint16(rsrc[24+14:], n)
	for i := range n {
		le.PutUint32(rsrc[24+16+8*i:], uint32(i))
		le.PutUint32(rsrc[24+16+8*i+4:], 0x80000000|24)
	}
	return peWithRsrc(rsrc)
}

func FuzzReadPEStrings(f *testing.F) {
	f.Add(synthPE(map[uint16]map[uint32]string{0x804: {96: "效果", 97: "马赛克"}, 0x9: {96: "Effect", 97: "Mosaic"}}))
	f.Add([]byte("MZ"))
	f.Add(selfReferencingPE())
	f.Fuzz(func(t *testing.T, data []byte) {
		s, err := ReadPEStrings(data)
		if err == nil {
			_, _, _ = s.pluginName(pluginLangsZH)
		}
	})
}
