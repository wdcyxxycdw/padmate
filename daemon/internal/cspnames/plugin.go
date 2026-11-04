package cspnames

import (
	"bytes"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
)

const (
	// pluginDir is the folder of the filter plug-ins under the install folder.
	pluginDir = "PlugIn"
	// maxPluginFile bounds the plug-in DLLs read.
	maxPluginFile = 64 << 20
)

// Language ids of the RT_STRING resources, in the order they are tried.
var (
	pluginLangsZH = []uint16{0x804, 0x9, 0x409}
	pluginLangsEN = []uint16{0x9, 0x409}
)

type pluginName struct {
	name, category string
}

// plugins finds filter plug-ins by the GUID that is their command id. A
// plug-in DLL contains the GUID as ASCII text, and its own string table has
// the name of the filter. The index of GUIDs is built on the first lookup.
type plugins struct {
	fsys fs.FS
	zh   bool

	once  sync.Once
	index map[string][]string // upper-case GUID to DLL paths

	mu    sync.Mutex
	cache map[string]*pluginName // upper-case GUID; nil value = no name
}

func newPlugins(fsys fs.FS, zh bool) *plugins {
	return &plugins{fsys: fsys, zh: zh, cache: map[string]*pluginName{}}
}

// guidPrefix returns the upper-case GUID at the start of a command id.
func guidPrefix(id string) (string, bool) {
	if len(id) < 36 || !isGUID([]byte(id[:36])) {
		return "", false
	}
	return strings.ToUpper(id[:36]), true
}

// lookup returns the name of the filter plug-in whose command id is id.
func (p *plugins) lookup(id string) (pluginName, bool) {
	g, ok := guidPrefix(id)
	if !ok {
		return pluginName{}, false
	}
	p.once.Do(p.buildIndex)
	p.mu.Lock()
	defer p.mu.Unlock()
	if n, done := p.cache[g]; done {
		if n == nil {
			return pluginName{}, false
		}
		return *n, true
	}
	langs := pluginLangsEN
	if p.zh {
		langs = pluginLangsZH
	}
	var found *pluginName
	for _, dll := range p.index[g] {
		data, err := readLimited(p.fsys, dll, maxPluginFile)
		if err != nil {
			continue
		}
		strs, err := ReadPEStrings(data)
		if err != nil {
			continue
		}
		name, cat, err := strs.pluginName(langs)
		if err != nil {
			continue
		}
		found = &pluginName{name: name, category: cat}
		break
	}
	p.cache[g] = found
	if found == nil {
		return pluginName{}, false
	}
	return *found, true
}

// buildIndex scans the plug-in DLLs for GUID text.
func (p *plugins) buildIndex() {
	p.index = map[string][]string{}
	var files []string
	_ = fs.WalkDir(p.fsys, pluginDir, func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // an unreadable folder just has no plug-ins
		}
		ext := strings.ToLower(path.Ext(name))
		if !d.IsDir() && (ext == ".dll" || ext == ".cfpi") {
			files = append(files, name)
		}
		return nil
	})
	sort.Strings(files)
	for _, f := range files {
		data, err := readLimited(p.fsys, f, maxPluginFile)
		if err != nil {
			continue
		}
		seen := map[string]bool{}
		for _, g := range findGUIDs(data) {
			if !seen[g] {
				seen[g] = true
				p.index[g] = append(p.index[g], f)
			}
		}
	}
}

// findGUIDs returns the upper-case ASCII GUIDs in data.
func findGUIDs(data []byte) []string {
	var out []string
	for i := 0; ; {
		j := bytes.IndexByte(data[i:], '-')
		if j < 0 {
			return out
		}
		dash := i + j
		i = dash + 1
		start := dash - 8
		if start < 0 || start+36 > len(data) {
			continue
		}
		g := data[start : start+36]
		if isGUID(g) {
			out = append(out, strings.ToUpper(string(g)))
			i = start + 36
		}
	}
}

// readLimited reads a file of at most limit bytes.
func readLimited(fsys fs.FS, name string, limit int64) ([]byte, error) {
	st, err := fs.Stat(fsys, name)
	if err != nil {
		return nil, err
	}
	if st.Size() > limit {
		return nil, fs.ErrInvalid
	}
	return fs.ReadFile(fsys, name)
}

// isGUID reports whether g is a GUID in the form 8-4-4-4-12 hexadecimal
// digits.
func isGUID(g []byte) bool {
	if len(g) != 36 {
		return false
	}
	for i, c := range g {
		switch {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}
