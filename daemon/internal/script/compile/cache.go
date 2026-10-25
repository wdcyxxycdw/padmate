package compile

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// esbuildVersion is kept in the cache key. Keep it in sync with the module
// version in go.mod when upgrading esbuild.
const esbuildVersion = "0.28.2"

// profileFile is the profile's own file in the script root. The daemon
// rewrites it at every save, so a source digest leaves it out unless the
// bundle reads it.
const profileFile = "profile.json"

// maxCacheEntries bounds a Cache: past it the least recently used entry
// goes. Each entry is one bundle of at most a few hundred kilobytes.
const maxCacheEntries = 256

// CacheStats describes cache activity since a Cache was created.
type CacheStats struct {
	Hits, Misses int
}

// Cache stores successful bundles. It keeps one entry for each script
// root, entry file and kind of build (a legacy script or a macro): a build
// of changed sources replaces it, so saving files does not grow the cache.
// An entry matches when the sources are the same: every file of the root
// except profile.json, the profile library's saved versions (.history)
// and the macros' type declarations (macros/.padmate), with the API and
// esbuild versions. profile.json counts too for a bundle that imports it,
// as esbuild can import JSON; otherwise saving the profile rebuilds no
// script. Failed builds are deliberately not cached: fixing a file and
// retrying must work without an explicit invalidation call. Builds run
// without the cache's lock: two concurrent builds of the same sources each
// run esbuild and store the same bundle.
type Cache struct {
	mu      sync.Mutex
	entries map[cacheSlot]*cacheEntry
	tick    uint64
	stats   CacheStats
}

// cacheSlot is what a Cache keeps one entry for.
type cacheSlot struct {
	root, entry string // canonical root; entry relative to it, with slashes
	macro       bool
}

type cacheEntry struct {
	sources string // sourceDigest of the build
	reads   bool   // the bundle imports profile.json
	profile string // profile.json's digest when reads
	result  Result
	used    uint64 // Cache.tick at the last use
}

// key is the key of the sources e was built from.
func (e *cacheEntry) key() string {
	if e.reads {
		return e.sources + "+" + e.profile
	}
	return e.sources
}

// NewCache returns an empty build cache.
func NewCache() *Cache { return &Cache{entries: make(map[cacheSlot]*cacheEntry)} }

// Stats returns a snapshot of cache activity.
func (c *Cache) Stats() CacheStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// Len returns the number of bundles the cache holds.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// Build returns a cached bundle when the source tree and build inputs are
// unchanged, otherwise it runs Build and remembers a successful result.
func (c *Cache) Build(opts BuildOptions) (Result, error) {
	if c == nil {
		return Build(opts)
	}
	root, entry, err := resolvePaths(opts)
	if err != nil {
		return Result{}, err
	}
	t, err := hashTree(root, opts.Macro)
	if err != nil {
		return Result{}, err
	}
	r, _, err := c.build(opts, root, entry, t)
	return r, err
}

// MacroResult is the build of one macro by BuildMacros.
type MacroResult struct {
	File string // the file name in the macros folder
	// Key identifies the sources the result was built from: the same Key
	// means the same Result or error, so a caller can keep what it learned
	// from the bundle, such as whether it loads, by Key.
	Key    string
	Result Result
	// Err is a *BuildError when esbuild reported errors, or a plain error
	// when the file cannot be a macro or the root cannot be read.
	Err error
}

// BuildMacros builds each of files, macro file names in the macros folder
// dir, as BuildOptions.Macro does with dir as the root, with the API api.
// It hashes the folder's sources once for all of them. A file that fails
// does not stop the others; each result has the file's bundle or its
// error.
func (c *Cache) BuildMacros(dir string, files []string, api string) []MacroResult {
	out := make([]MacroResult, len(files))
	croot, err := canonical(dir)
	if err != nil {
		err = fmt.Errorf("compile: macros folder: %w", err)
	}
	var t tree
	if err == nil {
		t, err = hashTree(croot, true)
	}
	for i, file := range files {
		out[i].File = file
		if err != nil {
			out[i].Err = err
			continue
		}
		opts := BuildOptions{Root: croot, Entry: file, API: api, Macro: true}
		r, entry, perr := resolvePaths(opts)
		if perr != nil {
			out[i].Err = perr
			continue
		}
		out[i].Result, out[i].Key, out[i].Err = c.build(opts, r, entry, t)
	}
	return out
}

// build returns the cached bundle of the canonical entry in the canonical
// root whose tree is t, or builds and remembers it, and the key of its
// sources. A nil Cache only builds.
func (c *Cache) build(opts BuildOptions, root, entry string, t tree) (Result, string, error) {
	rel, err := filepath.Rel(root, entry)
	if err != nil {
		return Result{}, "", err
	}
	slot := cacheSlot{root: root, entry: filepath.ToSlash(rel), macro: opts.Macro}
	sources := sourceDigest(t, opts.API, slot)
	if c != nil {
		c.mu.Lock()
		if e := c.entries[slot]; e != nil && e.sources == sources && (!e.reads || e.profile == t.profile) {
			c.tick++
			e.used = c.tick
			c.stats.Hits++
			r, key := cloneResult(e.result), e.key()
			c.mu.Unlock()
			return r, key, nil
		}
		c.stats.Misses++
		c.mu.Unlock()
	}
	r, reads, err := build(opts, root, entry)
	if err != nil {
		// Whether a failed bundle would read profile.json is unknown.
		return r, sources + "+" + t.profile, err
	}
	e := &cacheEntry{sources: sources, reads: reads, result: cloneResult(r)}
	if reads {
		e.profile = t.profile
	}
	if c != nil {
		c.store(slot, e)
	}
	return r, e.key(), nil
}

// store puts e in slot, replacing the older bundle there, and drops the
// least recently used entry past maxCacheEntries.
func (c *Cache) store(slot cacheSlot, e *cacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tick++
	e.used = c.tick
	c.entries[slot] = e
	if len(c.entries) <= maxCacheEntries {
		return
	}
	var oldest cacheSlot
	var used uint64
	for s, e := range c.entries {
		if used == 0 || e.used < used {
			oldest, used = s, e.used
		}
	}
	delete(c.entries, oldest)
}

// BuildCached uses a process-wide cache. Hosts that need an independent
// lifetime can use NewCache instead.
func BuildCached(opts BuildOptions) (Result, error) { return defaultCache.Build(opts) }

// BuildMacros builds macros with the process-wide cache (Cache.BuildMacros).
func BuildMacros(root string, files []string, api string) []MacroResult {
	return defaultCache.BuildMacros(root, files, api)
}

var defaultCache = NewCache()

func cloneResult(r Result) Result {
	r.JS = append([]byte(nil), r.JS...)
	r.Diagnostics = append([]Diagnostic(nil), r.Diagnostics...)
	return r
}

// tree is the digest of a script root's sources.
type tree struct {
	files   string // every file but profile.json, .history and macros/.padmate
	profile string // profile.json, or "" when there is none
}

// sourceDigest is the digest of the sources of the build of slot with the
// API api from the tree t: t without profile.json, the versions, the kind
// and the entry.
func sourceDigest(t tree, api string, slot cacheSlot) string {
	h := sha256.New()
	fmt.Fprintf(h, "esbuild=%s\napi=%s\nmacro=%v\nentry=%s\nfiles=%s\n", esbuildVersion, api, slot.macro, slot.entry, t.files)
	return hex.EncodeToString(h.Sum(nil))
}

// hashTree hashes the files of the canonical root: their relative paths and
// bytes, in lexical order, so the digest is independent of the order the
// file system lists them in, and a rename changes it. profile.json is
// hashed apart; the profile library's saved versions (PRO-17) and the
// macros' type declarations are left out: those of the root's macros
// folder, or, for macro, a macros folder that is the root, its own. A link
// to outside the root is an error.
func hashTree(root string, macro bool) (tree, error) {
	type file struct {
		name string
		data []byte
	}
	var files []file
	var t tree
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		parent := filepath.Dir(p)
		if d.IsDir() {
			// The saved versions of profile.json are never script source;
			// Build refuses them. Reading them would cost up to
			// MaxSnapshots documents per build and race a save that prunes
			// or replaces them. The daemon rewrites the macros' types.
			types := strings.EqualFold(d.Name(), profile.MacroTypesDir)
			if macro && parent == root && types ||
				!macro && parent == root && profile.IsHistoryDir(d.Name()) ||
				!macro && types && filepath.Dir(parent) == root && strings.EqualFold(filepath.Base(parent), profile.MacroDir) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 && !inRoot(root, p) {
			return fmt.Errorf("compile: script root contains link outside root: %s", p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		if parent == root && strings.EqualFold(d.Name(), profileFile) {
			t.profile = digest(b)
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		files = append(files, file{name: filepath.ToSlash(rel), data: b})
		return nil
	})
	if err != nil {
		return tree{}, fmt.Errorf("compile: hash script sources: %w", err)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].name < files[j].name })
	h := sha256.New()
	for _, f := range files {
		fmt.Fprintf(h, "\n%s\n%d\n", f.name, len(f.data))
		_, _ = h.Write(f.data)
	}
	t.files = sum(h)
	return t, nil
}

func digest(b []byte) string {
	h := sha256.New()
	_, _ = h.Write(b)
	return sum(h)
}

func sum(h hash.Hash) string { return hex.EncodeToString(h.Sum(nil)) }
