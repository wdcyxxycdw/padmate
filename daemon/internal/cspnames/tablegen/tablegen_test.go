package tablegen

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"io/fs"
	"reflect"
	"testing"
)

const testBase = 0x140000000

// testImage maps data at RVA 0x1000 as one section.
func testImage(data []byte) *image {
	return &image{
		data: data,
		base: testBase,
		secs: []*pe.Section{{SectionHeader: pe.SectionHeader{
			Name: ".rdata", VirtualAddress: 0x1000, VirtualSize: uint32(len(data)), Size: uint32(len(data)), Offset: 0,
		}}},
	}
}

func rel(target, next uint64) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, uint32(int32(int64(target)-int64(next))))
	return b
}

func TestFindStubsAndRefs(t *testing.T) {
	data := make([]byte, 0x200)
	copy(data[0x10:], "toolsizeplus\x00")
	copy(data[0x40:], "742DEA58-ED6B-4402-BC11-20DFC6D08040\x00")
	idVA, guidVA := uint64(testBase+0x1000+0x10), uint64(testBase+0x1000+0x40)
	const textVA = testBase + 0x2000
	global := uint64(testBase + 0x3000)

	var text []byte
	text = append(text, 0x90, 0x90, 0x90) // padding before the stub
	at := textVA + uint64(len(text))
	text = append(text, 0x41, 0xb9, 1, 0, 0, 0)
	text = append(text, 0x4c, 0x8d, 0x05)
	text = append(text, rel(idVA, at+13)...)
	text = append(text, 0x48, 0x8d, 0x15)
	text = append(text, rel(guidVA, at+20)...)
	text = append(text, 0x48, 0x8d, 0x0d)
	text = append(text, rel(global, at+27)...)
	text = append(text, 0xe9, 0, 0, 0, 0)
	// A near miss: the same bytes with a different last opcode.
	miss := append([]byte(nil), text[3:3+28]...)
	miss[27] = 0xe8
	text = append(text, miss...)
	// lea rcx,[global], and an instruction with a 4-byte immediate after the
	// displacement: RIP is 4 bytes past the field, so the operand is 4 bytes
	// below the global in the encoding.
	refAt := textVA + uint64(len(text))
	text = append(text, 0x48, 0x8d, 0x0d)
	text = append(text, rel(global, refAt+3+4)...)
	ref2 := textVA + uint64(len(text))
	text = append(text, 0x48, 0x8d, 0x15)
	text = append(text, rel(global-4, ref2+3+4)...)
	text = append(text, 0x90)

	im := testImage(data)
	stubs := findStubs(im, text, textVA)
	if want := []stub{{id: "toolsizeplus", guid: "742DEA58-ED6B-4402-BC11-20DFC6D08040", global: global}}; !reflect.DeepEqual(stubs, want) {
		t.Fatalf("stubs = %+v, want %+v", stubs, want)
	}
	refs := findRefs(text, textVA, map[uint64]struct{}{global: {}})
	// The stub's own lea rcx is a reference too.
	var vas []uint64
	for _, r := range refs {
		if r.global != global {
			t.Errorf("ref to %#x", r.global)
		}
		vas = append(vas, r.va)
	}
	want := []uint64{at + 23, refAt + 3, ref2 + 3}
	if !reflect.DeepEqual(vas, want) {
		t.Errorf("ref addresses = %#x, want %#x", vas, want)
	}
}

func TestImageOffsetAndString(t *testing.T) {
	data := []byte("\x00\x00abc\x00def")
	im := testImage(data)
	if o, ok := im.offset(testBase + 0x1002); !ok || o != 2 {
		t.Errorf("offset = %d, %v", o, ok)
	}
	if _, ok := im.offset(testBase + 0x1000 + uint64(len(data))); ok {
		t.Error("offset past the section")
	}
	if _, ok := im.offset(0x1000); ok {
		t.Error("offset below the image base")
	}
	if s := im.cstr(testBase + 0x1002); s != "abc" {
		t.Errorf("cstr = %q", s)
	}
	if s := im.cstr(testBase + 0x1006); s != "" { // no terminator
		t.Errorf("cstr without NUL = %q", s)
	}
}

func TestArgsOf(t *testing.T) {
	val := func(p *uint32) int {
		if p == nil {
			return -1
		}
		return int(*p)
	}
	tests := []struct {
		name string
		seg  []byte
		want [3]int
	}{
		{"group at depth 0", []byte{0x33, 0xd2, 0x45, 0x33, 0xc0, 0x41, 0xb9, 1, 0, 0, 0, 0x48, 0x8b, 0xce, 0xe8, 0, 0, 0, 0}, [3]int{0, 0, 1}},
		{"leaf at depth 2", []byte{0xba, 1, 0, 0, 0, 0x41, 0xb8, 2, 0, 0, 0, 0x45, 0x33, 0xc9, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{1, 2, 0}},
		{"lea from r9", []byte{0x41, 0xb9, 3, 0, 0, 0, 0x41, 0x8d, 0x51, 0x02, 0x45, 0x8d, 0x41, 0xff, 0x48, 0x8b, 0xce, 0xe8}, [3]int{5, 2, 3}},
		{"mov r8d,r9d", []byte{0x45, 0x33, 0xc9, 0x45, 0x8b, 0xc1, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{-1, 0, 0}},
		// The real bytes of three registrations of CSP 5.1.5 that the first scanner
		// could not read: mov edx,r13d; mov r8d,edx (a leaf at depth 1), xor edx,edx;
		// lea r8d,[r14+1] (a group at depth 1) and lea edx,[r14+1]; lea r8d,[r14+2]
		// (a leaf at depth 2).
		{"r13d", []byte{0x45, 0x33, 0xc9, 0x41, 0x8b, 0xd5, 0x44, 0x8b, 0xc2, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{1, 1, 0}},
		{"group with r14d", []byte{0x45, 0x33, 0xc9, 0x33, 0xd2, 0x45, 0x8d, 0x46, 0x01, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{0, 1, 0}},
		{"leaf with r14d", []byte{0x45, 0x33, 0xc9, 0x41, 0x8d, 0x56, 0x01, 0x45, 0x8d, 0x46, 0x02, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{1, 2, 0}},
		{"unknown source", []byte{0x41, 0x8b, 0xd4, 0x48, 0x8b, 0xcb, 0xe8}, [3]int{-1, -1, -1}},
		{"nothing", nil, [3]int{-1, -1, -1}},
		{"truncated mov", []byte{0x41, 0xb9, 1}, [3]int{-1, -1, -1}},
		{"stops at the call", []byte{0x33, 0xd2, 0x48, 0x8b, 0xce, 0xe8, 0xba, 7, 0, 0, 0}, [3]int{0, -1, -1}},
	}
	for _, tt := range tests {
		got := argsOf(tt.seg)
		g := [3]int{val(got[0]), val(got[1]), val(got[2])}
		if g != tt.want {
			t.Errorf("%s: args = %v, want %v", tt.name, g, tt.want)
		}
	}
}

func TestRegion(t *testing.T) {
	r := Region{10, 20}
	if !r.contains(10) || !r.contains(19) || r.contains(20) || r.contains(9) {
		t.Error("contains")
	}
}

func TestGenerateErrors(t *testing.T) {
	_, _, err := Generate(Options{InstallDir: t.TempDir()})
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("empty folder: err = %v", err)
	}
	if _, err := openImage([]byte("MZ")); err == nil {
		t.Error("openImage accepted junk")
	}
}
