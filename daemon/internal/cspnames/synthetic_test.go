package cspnames

import (
	"encoding/binary"
	"fmt"
	"testing/fstest"
	"unicode/utf16"
)

// This file builds small containers in the format the parser reads. The
// tests never use files of a real CSP.

func putU32(b []byte, v uint32) { binary.BigEndian.PutUint32(b, v) }

func u32b(v uint32) []byte {
	b := make([]byte, 4)
	putU32(b, v)
	return b
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ent is a directory entry whose content may depend on its absolute offset.
type ent struct {
	id      uint32
	content func(off int) []byte
}

func lit(b []byte) func(int) []byte { return func(int) []byte { return b } }

// buildDir lays out a directory at absolute offset base: the table, then the
// contents one after another.
func buildDir(base int, ents []ent) []byte {
	hdr := 4 + 12*len(ents)
	out := make([]byte, hdr)
	putU32(out, uint32(len(ents)))
	off := base + hdr
	var body []byte
	for i, e := range ents {
		c := e.content(off)
		putU32(out[4+12*i:], e.id)
		putU32(out[8+12*i:], uint32(off))
		putU32(out[12+12*i:], uint32(len(c)))
		body = append(body, c...)
		off += len(c)
	}
	return append(out, body...)
}

func subDir(ents []ent) func(int) []byte {
	return func(off int) []byte { return buildDir(off, ents) }
}

func lenString(s string) []byte { return cat(u32b(uint32(len(s))), []byte(s)) }

var (
	testItemHdr = []byte{0, 0, 0, 0x10, 0, 0, 0, 0x1e}
	testMenuHdr = []byte{0, 0, 0, 0x10, 0, 0, 0, 0x1a}
)

// mitem is an item of a synthetic menu: a command (cmd > 0) or a submenu.
type mitem struct {
	label uint16 // string id
	cmd   uint32 // command number; 0 for a submenu
	form  int    // 0: [u32 cmd][u16 mnemonic]; 1: [u16 mnemonic][u32 cmd]
	sub   []mitem
}

func refRun(ids ...uint16) []byte {
	var out []byte
	for _, id := range ids {
		out = append(out, 0, 0x0d, byte(id>>8), byte(id))
	}
	return out
}

func encodeMenu(items []mitem) []byte {
	out := cat(testMenuHdr, []byte("PWMenu"))
	return append(out, encodeItems(items)...)
}

func encodeItems(items []mitem) []byte {
	var out []byte
	for _, it := range items {
		out = append(out, testItemHdr...)
		out = append(out, "PWMenuItem"...)
		if it.sub != nil {
			embedded := encodeMenu(it.sub)
			out = append(out, u32b(10)...)
			out = append(out, 1, 2, 3, 4)
			out = append(out, refRun(it.label)...)
			out = append(out, u32b(uint32(len(embedded)))...)
			out = append(out, embedded...)
			continue
		}
		out = append(out, u32b(5)...)
		out = append(out, 1, 2, 3, 4)
		out = append(out, refRun(it.label)...)
		if it.form == 0 {
			out = append(out, u32b(it.cmd)...)
			out = append(out, 0, 0x46)
		} else {
			out = append(out, 0, 0x46)
			out = append(out, u32b(it.cmd)...)
		}
	}
	return out
}

// synthResource builds a main resource.
//
// Command numbers 1 to 8 are filenew, exportpng, exportjpg, filequit,
// undo, noname, tablecmd and canvasscrolldown. String ids: 11 File, 21 Edit,
// 101 New, 102 Export, 103 PNG, 104 JPEG, 105 Quit, 106 Undo, 107 Dup,
// 108 Orphan; the menu with id 30 has no name (string 31 is absent).
func synthResource(opts ...func(*synth)) []byte {
	s := &synth{}
	for _, o := range opts {
		o(s)
	}
	cmds := []string{"filenew", "exportpng", "exportjpg", "filequit", "undo", "noname", "tablecmd", "canvasscrolldown"}
	if s.noCommands {
		cmds = nil
	}
	strs := map[uint32]string{11: "File", 21: "Edit", 101: "New", 102: "Export", 103: "PNG", 104: "JPEG",
		105: "Quit", 106: "Undo", 107: "Dup", 108: "Orphan"}
	if s.prefix != "" {
		for id, v := range strs {
			strs[id] = s.prefix + v
		}
	}
	menus := map[uint32][]mitem{
		10: {
			{label: 101, cmd: 1},
			{label: 102, sub: []mitem{{label: 103, cmd: 2}, {label: 104, cmd: 3, form: 1}}},
			{label: 105, cmd: 4, form: 1},
		},
		20: {{label: 106, cmd: 5}, {label: 107, cmd: 1}},
		30: {{label: 108, cmd: 6}},
		40: nil, 50: nil, 60: nil,
	}
	menuIDs := []uint32{10, 20, 30, 40, 50, 60}

	// Section 2: command records.
	var sec2 []byte
	for _, id := range cmds {
		guid := "742DEA58-ED6B-4402-BC11-20DFC6D08040"
		body := cat([]byte{0, 0, 0, 0x10, 0, 0, 0, 0x14, 0, 0, 0, 0x4a, 9, 9, 9, 9},
			[]byte{0, 0, 0, 0x24}, []byte(guid), []byte{0, 0, 0, byte(len(id))}, []byte(id))
		sec2 = append(sec2, cat(u32b(uint32(4+len(body))), body)...)
	}
	if s.noCommands {
		sec2 = []byte{0, 0, 0, 0, 0, 0, 0, 0}
	}

	// Section 1: block 333 with a string table, then the main block 13.
	var sdir []ent
	for id := uint32(1); id <= 1100; id++ {
		if id == 31 {
			continue // the menu with id 30 has no name
		}
		v, ok := strs[id]
		if !ok {
			v = fmt.Sprintf("s%d", id)
		}
		sdir = append(sdir, ent{id, lit(lenString(v))})
	}
	var mdir []ent
	for _, id := range menuIDs {
		mdir = append(mdir, ent{id, lit(encodeMenu(menus[id]))})
	}
	mainBlock := func(off int) []byte {
		marker := []byte("PWCommandShortcut\x00\x00\x00\x00")
		sd := buildDir(off+len(marker), sdir)
		md := buildDir(off+len(marker)+len(sd), mdir)
		return cat(marker, sd, md)
	}
	stringBlock := subDir([]ent{{1, subDir([]ent{
		{1, lit(lenString("text one"))},
		{2, lit(lenString("text two"))},
		{3, lit([]byte{0, 0, 0, 9, 'x'})}, // size does not match: skipped
	})}})
	blocks := []ent{{333, stringBlock}, {13, mainBlock}}
	if s.badMain {
		blocks = blocks[:1]
	}
	sec1Off := 4 + 12*3
	sec1 := buildDir(sec1Off, blocks)
	sec2Off := sec1Off + len(sec1)
	sec3Off := sec2Off + len(sec2)
	sec3 := make([]byte, 48)
	out := make([]byte, sec1Off)
	putU32(out, 3)
	for i, sc := range [][3]int{{1, sec1Off, len(sec1)}, {2, sec2Off, len(sec2)}, {3, sec3Off, len(sec3)}} {
		putU32(out[4+12*i:], uint32(sc[0]))
		putU32(out[8+12*i:], uint32(sc[1]))
		putU32(out[12+12*i:], uint32(sc[2]))
	}
	return cat(out, sec1, sec2, sec3)
}

type synth struct {
	noCommands bool
	badMain    bool
	prefix     string
}

// synthPE builds a minimal 64-bit PE file whose .rsrc section holds RT_STRING
// resources: strings[lang][id].
func synthPE(strings map[uint16]map[uint32]string) []byte {
	// Group the strings in blocks per language.
	type key struct {
		block uint32
		lang  uint16
	}
	blocks := map[key]*[16]string{}
	for lang, m := range strings {
		for id, v := range m {
			k := key{id/16 + 1, lang}
			if blocks[k] == nil {
				blocks[k] = &[16]string{}
			}
			blocks[k][id%16] = v
		}
	}
	// Order: by block, then language (all small).
	var ks []key
	for k := range blocks {
		ks = append(ks, k)
	}
	for i := range ks {
		for j := i + 1; j < len(ks); j++ {
			if ks[j].block < ks[i].block || ks[j].block == ks[i].block && ks[j].lang < ks[i].lang {
				ks[i], ks[j] = ks[j], ks[i]
			}
		}
	}
	le := binary.LittleEndian
	dirHdr := func(n int) []byte {
		b := make([]byte, 16)
		le.PutUint16(b[14:], uint16(n))
		return b
	}
	// Distinct block ids in order.
	var blockIDs []uint32
	for _, k := range ks {
		if len(blockIDs) == 0 || blockIDs[len(blockIDs)-1] != k.block {
			blockIDs = append(blockIDs, k.block)
		}
	}
	const secVA = 0x1000
	// Layout inside the section: root dir, type dir, block dirs, data entries, data.
	rootSize := 16 + 8
	typeSize := 16 + 8*len(blockIDs)
	var langCount = map[uint32]int{}
	for _, k := range ks {
		langCount[k.block]++
	}
	blockDirOff := map[uint32]int{}
	off := rootSize + typeSize
	for _, b := range blockIDs {
		blockDirOff[b] = off
		off += 16 + 8*langCount[b]
	}
	dataEntOff := off
	off += 16 * len(ks)
	var data []byte
	dataOff := map[key]int{}
	var datas [][]byte
	for _, k := range ks {
		var body []byte
		for _, s := range blocks[k] {
			u := utf16.Encode([]rune(s))
			b := make([]byte, 2+2*len(u))
			le.PutUint16(b, uint16(len(u)))
			for i, c := range u {
				le.PutUint16(b[2+2*i:], c)
			}
			body = append(body, b...)
		}
		dataOff[k] = off + len(data)
		data = append(data, body...)
		datas = append(datas, body)
	}
	var rsrc []byte
	rsrc = append(rsrc, dirHdr(1)...)
	e := make([]byte, 8)
	le.PutUint32(e, rtString)
	le.PutUint32(e[4:], 0x80000000|uint32(rootSize))
	rsrc = append(rsrc, e...)
	rsrc = append(rsrc, dirHdr(len(blockIDs))...)
	for _, b := range blockIDs {
		e := make([]byte, 8)
		le.PutUint32(e, b)
		le.PutUint32(e[4:], 0x80000000|uint32(blockDirOff[b]))
		rsrc = append(rsrc, e...)
	}
	idx := 0
	langEntry := map[key]int{}
	for _, k := range ks {
		langEntry[k] = dataEntOff + 16*idx
		idx++
	}
	for _, b := range blockIDs {
		rsrc = append(rsrc, dirHdr(langCount[b])...)
		for _, k := range ks {
			if k.block != b {
				continue
			}
			e := make([]byte, 8)
			le.PutUint32(e, uint32(k.lang))
			le.PutUint32(e[4:], uint32(langEntry[k]))
			rsrc = append(rsrc, e...)
		}
	}
	for i, k := range ks {
		e := make([]byte, 16)
		le.PutUint32(e, uint32(secVA+dataOff[k]))
		le.PutUint32(e[4:], uint32(len(datas[i])))
		rsrc = append(rsrc, e...)
	}
	rsrc = append(rsrc, data...)

	const (
		peOff    = 0x40
		optSize  = 240
		secTable = peOff + 4 + 20 + optSize
		rawOff   = 0x200
	)
	hdr := make([]byte, rawOff)
	copy(hdr, "MZ")
	le.PutUint32(hdr[0x3c:], peOff)
	copy(hdr[peOff:], "PE\x00\x00")
	c := hdr[peOff+4:]
	le.PutUint16(c, 0x8664)
	le.PutUint16(c[2:], 1)
	le.PutUint16(c[16:], optSize)
	le.PutUint16(c[18:], 0x2022)
	o := hdr[peOff+24:]
	le.PutUint16(o, 0x20b)
	le.PutUint64(o[24:], 0x180000000)                              // ImageBase
	le.PutUint32(o[32:], 0x1000)                                   // SectionAlignment
	le.PutUint32(o[36:], 0x200)                                    // FileAlignment
	le.PutUint32(o[56:], uint32(secVA+((len(rsrc)+0xfff)&^0xfff))) // SizeOfImage
	le.PutUint32(o[60:], rawOff)                                   // SizeOfHeaders
	le.PutUint32(o[108:], 16)                                      // NumberOfRvaAndSizes
	le.PutUint32(o[112+8*2:], secVA)                               // resource directory
	le.PutUint32(o[112+8*2+4:], uint32(len(rsrc)))
	sh := hdr[secTable:]
	copy(sh, ".rsrc")
	le.PutUint32(sh[8:], uint32(len(rsrc)))
	le.PutUint32(sh[12:], secVA)
	le.PutUint32(sh[16:], uint32(len(rsrc)))
	le.PutUint32(sh[20:], rawOff)
	le.PutUint32(sh[36:], 0x40000040)
	return append(hdr, rsrc...)
}

// synthInstall is an install folder: resources for the languages that are
// given and a plug-in with a GUID command id.
func synthInstall(langs map[string][]byte, plugins map[string][]byte) fstest.MapFS {
	fsys := fstest.MapFS{}
	for l, data := range langs {
		fsys["resource/"+l+"/"+MainResourceName] = &fstest.MapFile{Data: data}
	}
	for name, data := range plugins {
		fsys["PlugIn/PAINT/"+name] = &fstest.MapFile{Data: data}
	}
	return fsys
}
