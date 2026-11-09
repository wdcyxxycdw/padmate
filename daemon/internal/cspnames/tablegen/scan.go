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

// Registers the dialog-tree function keeps across its entries: the code that
// runs a registration has r13d = 1 and r14d = 0 (they are the guards of the
// conditional blocks, and entries load edx and r8d from them: mov edx,r13d;
// lea r8d,[r14+1]).
const (
	regEDX  = 2
	regR8   = 8
	regR9   = 9
	regR13  = 13
	regR14  = 14
	valR13  = 1
	valR14  = 0
	numRegs = 16
)

// argsOf walks the bytes of one registration up to its final call and tracks
// the constants loaded into edx, r8d and r9d: (is leaf, depth, flag). Besides
// immediates and xor, it follows moves and lea with an 8-bit displacement
// between 32-bit registers, with r13d and r14d as the constants above.
func argsOf(seg []byte) [3]*uint32 {
	var reg [numRegs]*uint32
	one, zero := uint32(valR13), uint32(valR14)
	reg[regR13], reg[regR14] = &one, &zero
	set := func(i int, v uint32) { reg[i] = &v }
	n := len(seg)
	for i := 0; i < n; {
		at := func(b ...byte) bool { return bytes.HasPrefix(seg[i:], b) }
		switch {
		case at(0x33, 0xd2): // xor edx,edx
			set(regEDX, 0)
			i += 2
		case at(0x45, 0x33, 0xc0): // xor r8d,r8d
			set(regR8, 0)
			i += 3
		case at(0x45, 0x33, 0xc9): // xor r9d,r9d
			set(regR9, 0)
			i += 3
		case at(0x41, 0xb9) && i+6 <= n: // mov r9d,imm32
			set(regR9, binary.LittleEndian.Uint32(seg[i+2:]))
			i += 6
		case at(0x41, 0xb8) && i+6 <= n: // mov r8d,imm32
			set(regR8, binary.LittleEndian.Uint32(seg[i+2:]))
			i += 6
		case seg[i] == 0xba && i+5 <= n: // mov edx,imm32
			set(regEDX, binary.LittleEndian.Uint32(seg[i+1:]))
			i += 5
		case (at(0x48, 0x8b, 0xce) || at(0x48, 0x8b, 0xcb)) && i+3 < n && seg[i+3] == 0xe8:
			return [3]*uint32{reg[regEDX], reg[regR8], reg[regR9]} // mov rcx,rsi|rbx ; call
		default:
			if used, ok := regMove(seg[i:], &reg); ok {
				i += used
			} else {
				i++
			}
		}
	}
	return [3]*uint32{reg[regEDX], reg[regR8], reg[regR9]}
}

// regMove decodes, at the start of b, a 32-bit register move or a lea with an
// 8-bit displacement whose destination is edx, r8d or r9d, and applies it. It
// returns the instruction's length. A source of unknown value makes the
// destination unknown.
func regMove(b []byte, reg *[numRegs]*uint32) (int, bool) {
	j, rex := 0, byte(0)
	if len(b) > 0 && b[0]&0xf0 == 0x40 {
		rex, j = b[0], 1
	}
	if len(b) < j+2 {
		return 0, false
	}
	op, modrm := b[j], b[j+1]
	mod, rg, rm := modrm>>6, int(modrm>>3&7), int(modrm&7)
	if rex&4 != 0 {
		rg += 8
	}
	if rex&1 != 0 {
		rm += 8
	}
	var dst int
	var val *uint32
	var length int
	switch {
	case op == 0x8b && mod == 3: // mov r32,r/m32
		dst, val, length = rg, reg[rm], j+2
	case op == 0x89 && mod == 3: // mov r/m32,r32
		dst, val, length = rm, reg[rg], j+2
	case op == 0x8d && mod == 1 && rm&7 != 4 && len(b) >= j+3: // lea r32,[r+disp8]
		dst, length = rg, j+3
		if base := reg[rm]; base != nil {
			v := *base + uint32(int32(int8(b[j+2])))
			val = &v
		}
	default:
		return 0, false
	}
	if dst != regEDX && dst != regR8 && dst != regR9 {
		return 0, false
	}
	if val != nil {
		v := *val
		val = &v
	}
	reg[dst] = val
	return length, true
}

var errNoText = errors.New("tablegen: the executable has no .text section")
