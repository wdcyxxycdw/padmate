package tablegen

import (
	"bytes"
	"encoding/binary"
	"errors"
	"sort"
)

// stub is a static initialiser of a command object:
//
//	mov r9d,1 ; lea r8,[command id] ; lea rdx,[resource GUID] ; lea rcx,[global] ; jmp ctor
type stub struct {
	id, guid string
	global   uint64
}

// stubPrefix is `mov r9d,1` followed by the opcode of `lea r8,[rip+rel32]`.
var stubPrefix = []byte{0x41, 0xb9, 1, 0, 0, 0, 0x4c, 0x8d, 0x05}

const stubLen = 28 // mov (6) + three lea (7 each) + jmp rel32 opcode (1)

// findStubs scans the code for stubs.
func findStubs(im *image, text []byte, textVA uint64) []stub {
	var out []stub
	for pos := 0; ; {
		i := bytes.Index(text[pos:], stubPrefix)
		if i < 0 {
			return out
		}
		p := pos + i
		pos = p + 1
		if p+stubLen > len(text) {
			continue
		}
		s := text[p : p+stubLen]
		if !bytes.Equal(s[13:16], []byte{0x48, 0x8d, 0x15}) || !bytes.Equal(s[20:23], []byte{0x48, 0x8d, 0x0d}) || s[27] != 0xe9 {
			continue
		}
		at := textVA + uint64(p)
		r8 := at + 13 + uint64(rel32(s[9:]))
		rdx := at + 20 + uint64(rel32(s[16:]))
		rcx := at + 27 + uint64(rel32(s[23:]))
		out = append(out, stub{id: im.cstr(r8), guid: im.cstr(rdx), global: rcx})
		pos = p + stubLen
	}
}

// ref is a reference in the code to a command's global object.
type ref struct {
	va     uint64 // address of the disp32 field
	global uint64
}

// findRefs finds every RIP-relative operand in the code that points at one of
// the globals (or a few bytes into it, for instructions with an immediate).
func findRefs(text []byte, textVA uint64, globals map[uint64]struct{}) []ref {
	var out []ref
	for i := 1; i+4 <= len(text); i++ {
		switch text[i-1] {
		case 0x05, 0x0d, 0x15, 0x1d, 0x25, 0x2d, 0x35, 0x3d:
		default:
			continue
		}
		v := textVA + uint64(i) + 4 + uint64(rel32(text[i:]))
		for _, imm := range [...]uint64{0, 1, 4} {
			if _, ok := globals[v+imm]; ok {
				out = append(out, ref{va: textVA + uint64(i), global: v + imm})
				break
			}
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].va < out[b].va })
	return out
}

// entry is one registration of a command in the dialog tree or in the
// command table.
type entry struct {
	id     string
	region byte // 'A' or 'B'
	c7     []uint32
	mov    []uint32
	args   [3]*uint32 // edx, r8d, r9d at the call; region A only
}

// extract collects, for every reference to a command global of the main
// resource inside the two code regions, the immediate operands that follow it.
func extract(im *image, refs []ref, global map[uint64]string, a, b Region) []entry {
	var out []entry
	for n, r := range refs {
		id, ok := global[r.global]
		if !ok {
			continue
		}
		var region byte
		switch {
		case a.contains(r.va):
			region = 'A'
		case b.contains(r.va):
			region = 'B'
		default:
			continue
		}
		end := r.va + 300
		if n+1 < len(refs) && refs[n+1].va < end {
			end = refs[n+1].va
		}
		o, ok := im.offset(r.va)
		if !ok {
			continue
		}
		stop := min(o+int(end-r.va), len(im.data))
		seg := im.data[o:stop]
		e := entry{id: id, region: region}
		if len(seg) > 8 {
			for i := 0; i+4 <= len(seg)-8; i++ {
				if seg[i] == 0xc7 && seg[i+1] == 0x44 && seg[i+2] == 0x24 && isStackSlot(seg[i+3]) {
					e.c7 = append(e.c7, le32(seg[i+4:]))
				}
			}
		}
		limit := len(seg) - 5
		for i := 0; i < limit; {
			switch {
			case seg[i] == 0x41 && i+6 <= limit && seg[i+1] >= 0xb8 && seg[i+1] <= 0xbf:
				e.mov = append(e.mov, le32(seg[i+2:]))
				i += 6
			case seg[i] >= 0xb8 && seg[i] <= 0xbf && i+5 <= limit:
				e.mov = append(e.mov, le32(seg[i+1:]))
				i += 5
			default:
				i++
			}
		}
		if region == 'A' {
			e.args = argsOf(seg)
		}
		out = append(out, e)
	}
	return out
}

func isStackSlot(b byte) bool {
	switch b {
	case 0x20, 0x28, 0x30, 0x38, 0x40:
		return true
	}
	return false
}

// argsOf walks the bytes of one registration up to its final call and tracks
// the constants loaded into edx, r8d and r9d: (is leaf, depth, flag).
func argsOf(seg []byte) [3]*uint32 {
	var reg [3]*uint32 // edx, r8d, r9d
	set := func(i int, v uint32) { reg[i] = &v }
	const (
		edx = 0
		r8  = 1
		r9  = 2
	)
	n := len(seg)
	for i := 0; i < n; {
		at := func(b ...byte) bool { return bytes.HasPrefix(seg[i:], b) }
		switch {
		case at(0x33, 0xd2): // xor edx,edx
			set(edx, 0)
			i += 2
		case at(0x45, 0x33, 0xc0): // xor r8d,r8d
			set(r8, 0)
			i += 3
		case at(0x45, 0x33, 0xc9): // xor r9d,r9d
			set(r9, 0)
			i += 3
		case at(0x41, 0xb9) && i+6 <= n: // mov r9d,imm32
			set(r9, binary.LittleEndian.Uint32(seg[i+2:]))
			i += 6
		case at(0x41, 0xb8) && i+6 <= n: // mov r8d,imm32
			set(r8, binary.LittleEndian.Uint32(seg[i+2:]))
			i += 6
		case seg[i] == 0xba && i+5 <= n: // mov edx,imm32
			set(edx, binary.LittleEndian.Uint32(seg[i+1:]))
			i += 5
		case at(0x41, 0x8d, 0x51) && reg[r9] != nil && i+4 <= n: // lea edx,[r9+d8]
			set(edx, *reg[r9]+uint32(int32(int8(seg[i+3]))))
			i += 4
		case at(0x45, 0x8d, 0x41) && reg[r9] != nil && i+4 <= n: // lea r8d,[r9+d8]
			set(r8, *reg[r9]+uint32(int32(int8(seg[i+3]))))
			i += 4
		case at(0x41, 0x8d, 0x50) && reg[r8] != nil && i+4 <= n: // lea edx,[r8+d8]
			set(edx, *reg[r8]+uint32(int32(int8(seg[i+3]))))
			i += 4
		case at(0x45, 0x8d, 0x40) && reg[r8] != nil && i+4 <= n: // lea r8d,[r8+d8]
			set(r8, *reg[r8]+uint32(int32(int8(seg[i+3]))))
			i += 4
		case at(0x45, 0x8b, 0xc1): // mov r8d,r9d
			reg[r8] = reg[r9]
			i += 3
		case at(0x44, 0x8b, 0xc2): // mov r8d,edx
			reg[r8] = reg[edx]
			i += 3
		case (at(0x48, 0x8b, 0xce) || at(0x48, 0x8b, 0xcb)) && i+3 < n && seg[i+3] == 0xe8:
			return reg // mov rcx,rsi|rbx ; call
		default:
			i++
		}
	}
	return reg
}

var errNoText = errors.New("tablegen: the executable has no .text section")
