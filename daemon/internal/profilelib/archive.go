package profilelib

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/wdcyxxycdw/padmate/daemon/internal/profile"
)

// An exported profile (PRO-20) is a zip file, "<name>.padmate", holding
// ArchiveManifest and the profile folder below ArchiveFolder: profile.json
// and every other folder and file in it except HistoryDir and a macros
// folder, the files only scripts, JSON and text (archiveFileProblem), and
// in its macros folder the library's macros that profile.json runs
// (SCR-51). An archive from before the library has the profile's whole
// macros folder there instead; both import the same way (Import). The
// import limits are the copy limits (MaxCopyEntries, MaxCopyBytes,
// MaxCopyDepth) and MaxArchiveBytes for the file itself.
const (
	ArchiveExt      = ".padmate"
	ArchiveManifest = "manifest.json"
	ArchiveFolder   = "profile/"
	// ArchiveType is the manifest's type.
	ArchiveType = "padmate-profile"
	// ArchiveFormat is the manifest's format, the version of this layout.
	// An archive with a higher format comes from a newer padmate.
	ArchiveFormat = 1
	// MaxArchiveBytes bounds the archive file. Its base64 still fits one
	// IPC frame of 8 MiB with room to spare.
	MaxArchiveBytes = 4 << 20
	// MaxArchivePath bounds a path below ArchiveFolder in bytes, and
	// maxArchiveElement one element of it.
	MaxArchivePath    = 512
	maxArchiveElement = 255
	maxManifestBytes  = 64 << 10
)

// GeneratedMacroDir is the folder of generated declarations for a
// profile's macros, "macros/.padmate" (profile.MacroTypesDir). padmate
// writes it again when needed, so an export leaves it out and an import
// skips it.
const GeneratedMacroDir = profile.MacroDir + "/" + profile.MacroTypesDir

// Manifest describes an archive. Name and Padmate are for people reading
// the file; Import takes the name from profile.json.
type Manifest struct {
	Type    string `json:"type"`
	Format  int    `json:"format"`
	Name    string `json:"name"`
	Padmate string `json:"padmate"`
}

// Reasons of an ArchiveError.
const (
	// ArchiveFormatReason: not a profile archive, or a damaged one.
	ArchiveFormatReason = "format"
	// ArchiveVersionReason: written by a newer padmate (a higher format).
	ArchiveVersionReason = "version"
	// ArchiveEntryReason: an entry that is unsafe or not a regular file
	// or folder, such as an absolute path, "..", a link, a duplicate or a
	// name Windows does not allow; Path names it.
	ArchiveEntryReason = "entry"
	// ArchiveLimitReason: beyond the import limits.
	ArchiveLimitReason = "limit"
	// ArchiveEncodingReason: a text file (script or JSON) that is not
	// UTF-8; Path names it.
	ArchiveEncodingReason = "encoding"
	// ArchiveProfileReason: profile.json does not validate; Diagnostics
	// says why.
	ArchiveProfileReason = "profile"
)

// ArchiveError refuses a file that ReadArchive does not accept. Path, when
// set, is the entry's name in the archive.
type ArchiveError struct {
	Reason      string
	Path        string
	Detail      string
	Diagnostics profile.Diagnostics
}

func (e *ArchiveError) Error() string {
	if e.Path != "" {
		return fmt.Sprintf("profile archive: %s: %s", e.Path, e.Detail)
	}
	return "profile archive: " + e.Detail
}

func archiveError(reason, path, detail string) *ArchiveError {
	return &ArchiveError{Reason: reason, Path: path, Detail: detail}
}

// Archive is a checked archive: its manifest and the profile folder's
// folders and files, parents first, profile.json among them.
type Archive struct {
	Manifest Manifest
	// Profile is profile.json, validated.
	Profile *profile.Profile
	items   []copyItem
}

// profileData returns the bytes of profile.json.
func (a *Archive) profileData() []byte {
	for _, item := range a.items {
		if item.path == "profile.json" {
			return item.data
		}
	}
	return nil
}

// Paths lists the archive's folders and files below ArchiveFolder.
func (a *Archive) Paths() []string {
	paths := make([]string, len(a.items))
	for i, item := range a.items {
		paths[i] = item.path
		if item.dir {
			paths[i] += "/"
		}
	}
	return paths
}

// Exported is an archive that Export wrote.
type Exported struct {
	Data []byte
	// Name is the profile's name in profile.json.
	Name string
}

// Export writes profiles/<id> as an archive, at most MaxArchiveBytes;
// padmate is the version recorded in the manifest. The folder is read
// under the writer lock, like Copy, within the copy limits; links and
// special files are refused, not skipped. HistoryDir and the folder's
// macros folder are left out; the macros of the library that profile.json
// runs, as the library lists them exactly, go into the archive's macros
// folder, and one the library lacks is left out, as the runtime finds
// none. Every export imports: a name that
// ReadArchive refuses, a file that is not a script, JSON or text file, a
// script or JSON file that is not UTF-8, a profile name that
// CheckProfileName refuses, a profile.json that does not validate
// (*profile.Error) or an archive beyond MaxArchiveBytes refuses the export
// (UnsupportedError, Path the file or the profile).
func (l *Library) Export(id, padmate string) (Exported, error) {
	if !ValidID(id) {
		return Exported{}, fmt.Errorf("profile library: invalid id %q", id)
	}
	root, err := os.OpenRoot(filepath.Join(l.dir, "profiles"))
	if err != nil {
		return Exported{}, err
	}
	defer root.Close()
	items, macros, err := l.readExport(root, id)
	if err != nil {
		return Exported{}, err
	}
	var data []byte
	for i, item := range slices.Concat(items, macros) {
		where := id + "/" + item.path
		if i >= len(items) {
			where = item.path // the library's
		}
		if reason := archivePathProblem(item.path); reason != "" {
			return Exported{}, &UnsupportedError{Path: where, Reason: reason + "; rename it to export the profile"}
		}
		if reason := archiveFileProblem(item.path); !item.dir && reason != "" {
			return Exported{}, &UnsupportedError{Path: where, Reason: reason + "; remove it to export the profile"}
		}
		if !item.dir && isTextFile(item.path) && !utf8.Valid(item.data) {
			return Exported{}, &UnsupportedError{Path: where, Reason: "is not UTF-8; save it as UTF-8 to export the profile"}
		}
		if item.path == "profile.json" {
			data = item.data
		}
	}
	items = slices.Concat(items, macros)
	p, ds := l.Parse(id, data)
	if err := ds.Err(); err != nil {
		return Exported{}, err
	}
	// An import would refuse the name (Import), so the export does.
	if err := CheckProfileName(p.Name); err != nil {
		return Exported{}, profile.Diagnostics{{Severity: profile.SeverityError, Pointer: "/name", Message: err.Error()}}.Err()
	}
	manifest := Manifest{Type: ArchiveType, Format: ArchiveFormat, Name: p.Name, Padmate: padmate}
	archive, err := writeArchive(manifest, items, time.Now())
	if errors.Is(err, errArchiveTooLarge) {
		return Exported{}, &UnsupportedError{Path: id, Reason: fmt.Sprintf("makes an archive larger than %d MiB", MaxArchiveBytes>>20)}
	}
	if err != nil {
		return Exported{}, err
	}
	return Exported{Data: archive, Name: p.Name}, nil
}

// readExport checks that profiles/<id> is a folder and reads its tree,
// leaving out what skipExported selects, and the library's macros that its
// profile.json runs, below "macros/", with the files they import from the
// library, the macros' own imports too (libraryImports), all under the
// writer lock, so that a rename of a macro is in both or neither. The
// macros count against the copy limits too.
func (l *Library) readExport(root *os.Root, id string) (items, macros []copyItem, err error) {
	l.writeMu.Lock()
	defer l.writeMu.Unlock()
	if err := checkFolder(root, id); err != nil {
		return nil, nil, err
	}
	items, err = readTree(root, id, skipExported)
	if err != nil {
		return nil, nil, err
	}
	var files []string
	budget := int64(MaxCopyBytes)
	for i, item := range items {
		budget -= int64(len(item.data))
		if item.path == "profile.json" {
			r, ds := l.Resolve(id, item.data)
			if ds.Err() != nil {
				continue // Export reports it
			}
			files = r.Profile.MacroFiles()
			if r.Merged != nil {
				// A profile that extends another is exported whole, so that
				// whoever imports it needs no parent (PRO-19, ADR-0070).
				flat, err := l.flatten(id, item.data)
				if errors.Is(err, ErrInheritedScript) {
					return nil, nil, &UnsupportedError{Path: id + "/profile.json", Reason: err.Error()}
				}
				if err != nil {
					return nil, nil, err
				}
				items[i].data = flat
			}
		}
	}
	if len(files) == 0 {
		return items, nil, nil
	}
	dir, err := l.openMacroDir(false)
	if errors.Is(err, fs.ErrNotExist) {
		return items, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	defer dir.Close()
	names, err := listMacros(dir)
	if err != nil {
		return nil, nil, err
	}
	files = slices.DeleteFunc(files, func(file string) bool { return !slices.Contains(names, file) })
	if macros, err = libraryImports(dir, id, files, budget); err != nil {
		return nil, nil, err
	}
	if len(items)+len(macros) > MaxCopyEntries {
		return nil, nil, &UnsupportedError{Path: id, Reason: fmt.Sprintf("holds more than %d files and folders with its macros", MaxCopyEntries)}
	}
	return items, macros, nil
}

// libraryImports reads the macros files of the library's macros folder dir,
// and every file they import, directly or through each other (a module
// such as lib/util.ts, a macro beside them, a JSON file; relativeImports),
// as the items of an archive's macros folder, with their folders. budget is
// what the archive may still hold. A file the archive could not hold, or
// nested too deep, is an *UnsupportedError that names it: the macro would
// fail to build where the archive is imported. Nothing is read of a profile
// without macros.
func libraryImports(dir *os.Root, id string, files []string, budget int64) ([]copyItem, error) {
	if len(files) == 0 {
		return nil, nil
	}
	macros := []copyItem{{path: profile.MacroDir, dir: true}}
	listed := map[string]bool{} // lower-cased paths already in macros
	isFile := func(p string) bool {
		info, err := dir.Lstat(filepath.FromSlash(p))
		return err == nil && info.Mode().IsRegular()
	}
	// follow lists the files whose imports are still to be read.
	var follow []copyItem
	add := func(p string) error {
		key := strings.ToLower(p)
		if listed[key] {
			return nil
		}
		listed[key] = true
		refuse := func(reason string) error {
			return &UnsupportedError{Path: profile.MacroDir + "/" + p, Reason: reason}
		}
		if problem := archivePathProblem(p); problem != "" {
			return refuse(problem)
		}
		if problem := archiveFileProblem(p); problem != "" {
			return refuse(problem)
		}
		if strings.Count(p, "/")+1 > MaxCopyDepth {
			return refuse(fmt.Sprintf("is nested more than %d folders deep", MaxCopyDepth))
		}
		data, err := readSmall(dir, filepath.FromSlash(p), budget)
		if err != nil {
			return err
		}
		budget -= int64(len(data))
		if budget < 0 {
			return &UnsupportedError{Path: id, Reason: fmt.Sprintf("holds more than %d MiB of files with its macros", MaxCopyBytes>>20)}
		}
		for i := range len(p) {
			if p[i] != '/' {
				continue
			}
			if parent := p[:i]; !listed[strings.ToLower(parent)+"/"] {
				listed[strings.ToLower(parent)+"/"] = true
				macros = append(macros, copyItem{path: profile.MacroDir + "/" + parent, dir: true})
			}
		}
		macros = append(macros, copyItem{path: profile.MacroDir + "/" + p, data: data})
		if scriptExtensions[strings.ToLower(path.Ext(p))] {
			follow = append(follow, copyItem{path: p, data: data})
		}
		return nil
	}
	for _, file := range files {
		if err := add(file); err != nil {
			return nil, err
		}
	}
	for len(follow) > 0 && len(macros) <= MaxCopyEntries {
		next := follow[0]
		follow = follow[1:]
		for _, spec := range relativeImports(next.data) {
			if p, ok := resolveImport(next.path, spec, isFile); ok {
				if err := add(p); err != nil {
					return nil, err
				}
			}
		}
	}
	return macros, nil
}

// skipExported leaves out what an export does not take from the profile
// folder: its saved versions and a macros folder, from before the library
// (MigrateMacros leaves one only with files it could not move).
func skipExported(p string) bool {
	return profile.IsHistoryDir(p) || strings.EqualFold(p, profile.MacroDir)
}

// skippedOnImport reports whether p, a checked path below ArchiveFolder,
// is in a folder an import does not write.
func skippedOnImport(p string) bool {
	first, _, _ := strings.Cut(p, "/")
	return profile.IsHistoryDir(first) || strings.EqualFold(p, GeneratedMacroDir) ||
		len(p) > len(GeneratedMacroDir) && p[len(GeneratedMacroDir)] == '/' && strings.EqualFold(p[:len(GeneratedMacroDir)], GeneratedMacroDir)
}

var errArchiveTooLarge = errors.New("profile archive: larger than MaxArchiveBytes")

// limitedBuffer fails a write that would grow it beyond MaxArchiveBytes.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > MaxArchiveBytes {
		return 0, errArchiveTooLarge
	}
	return b.Buffer.Write(p)
}

// writeArchive writes the manifest first, then items in order, every
// entry dated at.
func writeArchive(m Manifest, items []copyItem, at time.Time) ([]byte, error) {
	manifest, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	var buf limitedBuffer
	w := zip.NewWriter(&buf)
	add := func(name string, method uint16, data []byte) error {
		f, err := w.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: at})
		if err == nil && data != nil {
			_, err = f.Write(data)
		}
		return err
	}
	if err := add(ArchiveManifest, zip.Deflate, append(manifest, '\n')); err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.dir {
			err = add(ArchiveFolder+item.path+"/", zip.Store, nil)
		} else {
			err = add(ArchiveFolder+item.path, zip.Deflate, item.data)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// isTextFile reports whether p is a script or JSON file, which must be
// UTF-8 like a script that script.import writes.
func isTextFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs", ".json":
		return true
	}
	return false
}

// archiveFileProblem says why an archive cannot hold the file p, or
// returns "". It holds only what a profile folder is for: scripts, JSON
// (profile.json, tsconfig.json, data a script reads) and notes (.md,
// .txt). An imported file loses the Mark of the Web that the downloaded
// archive had, and a user may open the folder in Explorer, so a program,
// shortcut or other file Windows would open or follow is never written
// there.
func archiveFileProblem(p string) string {
	switch ext := strings.ToLower(path.Ext(p)); {
	case isTextFile(p), ext == ".md", ext == ".txt":
		return ""
	}
	return "is not a script, JSON or text file (.ts, .js, .json, .md, .txt and the like)"
}

// archivePathProblem checks p, a slash-separated path below the profile
// folder, and says what is wrong with it, or returns "". It accepts only
// paths every platform can create inside the folder: relative, without
// empty, "." or ".." elements, no element longer than 255 bytes, no
// control characters or <>:"\|?*, no element ending in a dot or space and
// no Windows device name (whatever the extension, as Windows opens
// "con.ts" as the console). An element shaped like an 8.3 short name
// ("abcdef~1.ts") could be another file's short name on NTFS, so that the
// import could not create it, and one ending in ".{…}" is a shell folder
// to Explorer: both are refused too.
func archivePathProblem(p string) string {
	switch {
	case p == "":
		return "is empty"
	case len(p) > MaxArchivePath:
		return fmt.Sprintf("is longer than %d bytes", MaxArchivePath)
	case !utf8.ValidString(p):
		return "is not UTF-8"
	}
	for element := range strings.SplitSeq(p, "/") {
		if problem := archiveElementProblem(element); problem != "" {
			return problem
		}
	}
	return ""
}

func archiveElementProblem(e string) string {
	switch {
	case e == "":
		return "has an empty folder name"
	case e == "." || e == "..":
		return "leaves its folder"
	case len(e) > maxArchiveElement:
		return fmt.Sprintf("has a name longer than %d bytes", maxArchiveElement)
	case strings.ContainsFunc(e, func(r rune) bool { return r < 0x20 || r == 0x7f || strings.ContainsRune(`<>:"\|?*`, r) }):
		return `has a control character or one of <>:"\|?*`
	case strings.HasSuffix(e, ".") || strings.HasSuffix(e, " "):
		return "has a name that ends in a dot or space"
	}
	stem, _, _ := strings.Cut(strings.ToLower(e), ".")
	stem = strings.TrimRight(stem, " ")
	switch {
	case isDeviceName(stem) || stem == "conin$" || stem == "conout$" || isSuperscriptDevice(stem):
		return "has a name Windows reserves for a device"
	case isShortNameLike(e):
		return "has a name like a Windows short name (such as ABCDEF~1.TS)"
	case strings.HasPrefix(path.Ext(e), ".{"):
		return "has a name Explorer opens as a shell folder (ending in .{…})"
	}
	return ""
}

// isShortNameLike reports whether e has the form of an 8.3 short name
// that Windows makes for a longer name: a base of at most 8 characters
// that ends in "~" and digits, and an extension of at most 3.
func isShortNameLike(e string) bool {
	base, ext, dotted := strings.Cut(e, ".")
	if len(base) > 8 || dotted && (len(ext) > 3 || strings.Contains(ext, ".")) {
		return false
	}
	tilde := strings.LastIndexByte(base, '~')
	if tilde < 1 || tilde == len(base)-1 {
		return false
	}
	for _, c := range base[tilde+1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// isSuperscriptDevice reports COM¹ to LPT³, which Windows also opens as
// devices.
func isSuperscriptDevice(stem string) bool {
	for _, prefix := range []string{"com", "lpt"} {
		if rest, ok := strings.CutPrefix(stem, prefix); ok && (rest == "¹" || rest == "²" || rest == "³") {
			return true
		}
	}
	return false
}

// ReadArchive checks data, an archive that Export wrote or one made by
// hand, and reads it into memory. It refuses, with an *ArchiveError, a
// file over MaxArchiveBytes, anything but a zip with one manifest of this
// format and a profile folder, an entry outside ArchiveFolder or with a
// path archivePathProblem refuses, a file archiveFileProblem refuses, a
// link or other special entry, an
// encrypted entry, a compression other than store and deflate, entries
// whose names differ only in case, a file where a folder is needed,
// more than MaxCopyEntries files and folders, MaxCopyBytes of files
// (counted as decompressed, so a zip bomb stops there), folders more than
// MaxCopyDepth deep, a profile.json over MaxDocumentBytes, a script or
// JSON file that is not UTF-8, a damaged entry (checksum or size), and a
// profile.json that does not validate. Entries in HistoryDir and
// GeneratedMacroDir are checked and then skipped.
func ReadArchive(data []byte) (*Archive, error) {
	if len(data) > MaxArchiveBytes {
		return nil, archiveError(ArchiveLimitReason, "", fmt.Sprintf("the file is larger than %d MiB", MaxArchiveBytes>>20))
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	// ErrInsecurePath still returns the reader; the names are checked below.
	if err != nil && !errors.Is(err, zip.ErrInsecurePath) {
		return nil, archiveError(ArchiveFormatReason, "", "not a zip file: "+err.Error())
	}
	if len(zr.File) > MaxCopyEntries+1 {
		return nil, archiveError(ArchiveLimitReason, "", fmt.Sprintf("holds more than %d files and folders", MaxCopyEntries))
	}
	r := archiveReader{budget: MaxCopyBytes, kinds: map[string]bool{}}
	for _, f := range zr.File {
		if err := r.add(f); err != nil {
			return nil, err
		}
	}
	return r.finish()
}

type archiveReader struct {
	manifest *Manifest
	items    []copyItem
	kinds    map[string]bool // lower-case path → is a folder
	budget   int64
	profile  []byte
}

func (r *archiveReader) add(f *zip.File) error {
	if f.Flags&0x1 != 0 {
		return archiveError(ArchiveFormatReason, f.Name, "is encrypted")
	}
	if f.Name == ArchiveManifest {
		return r.readManifest(f)
	}
	rel, ok := strings.CutPrefix(f.Name, ArchiveFolder)
	if !ok {
		return archiveError(ArchiveEntryReason, f.Name, "is outside the profile folder")
	}
	dir := strings.HasSuffix(f.Name, "/")
	rel = strings.TrimSuffix(rel, "/")
	if rel == "" && dir {
		return nil // the profile folder itself
	}
	if problem := archivePathProblem(rel); problem != "" {
		return archiveError(ArchiveEntryReason, f.Name, problem)
	}
	if !dir {
		if problem := archiveFileProblem(rel); problem != "" {
			return archiveError(ArchiveEntryReason, f.Name, problem)
		}
	}
	want := fs.FileMode(0)
	if dir {
		want = fs.ModeDir
	}
	if f.Mode().Type() != want {
		return archiveError(ArchiveEntryReason, f.Name, "is a link or special file; an archive holds only files and folders")
	}
	depth := strings.Count(rel, "/")
	if dir {
		depth++
	}
	if depth > MaxCopyDepth {
		return archiveError(ArchiveLimitReason, f.Name, fmt.Sprintf("is nested more than %d folders deep", MaxCopyDepth))
	}
	if err := r.addParents(f.Name, rel); err != nil {
		return err
	}
	key := strings.ToLower(rel)
	if isDir, seen := r.kinds[key]; seen {
		if isDir && dir {
			return nil // a folder listed after its contents
		}
		return archiveError(ArchiveEntryReason, f.Name, "duplicates another entry (names are compared ignoring case)")
	}
	r.kinds[key] = dir
	if dir {
		if f.UncompressedSize64 != 0 {
			return archiveError(ArchiveFormatReason, f.Name, "is a folder with data")
		}
		return r.append(f.Name, copyItem{path: rel, dir: true})
	}
	data, err := r.read(f, r.budget, func() *ArchiveError {
		return archiveError(ArchiveLimitReason, "", fmt.Sprintf("holds more than %d MiB of files", MaxCopyBytes>>20))
	})
	if err != nil {
		return err
	}
	r.budget -= int64(len(data))
	switch {
	case rel == "profile.json" && len(data) > MaxDocumentBytes:
		return archiveError(ArchiveLimitReason, f.Name, fmt.Sprintf("exceeds %d MiB", MaxDocumentBytes>>20))
	case isTextFile(rel) && !utf8.Valid(data):
		return archiveError(ArchiveEncodingReason, f.Name, "is not UTF-8")
	case rel == "profile.json":
		r.profile = data
	}
	return r.append(f.Name, copyItem{path: rel, data: data})
}

// addParents adds the folders above rel that no entry has listed yet.
func (r *archiveReader) addParents(name, rel string) error {
	for i := range len(rel) {
		if rel[i] != '/' {
			continue
		}
		parent := rel[:i]
		isDir, seen := r.kinds[strings.ToLower(parent)]
		if seen && !isDir {
			return archiveError(ArchiveEntryReason, name, "is inside a file")
		}
		if !seen {
			r.kinds[strings.ToLower(parent)] = true
			if err := r.append(name, copyItem{path: parent, dir: true}); err != nil {
				return err
			}
		}
	}
	return nil
}

func (r *archiveReader) append(name string, item copyItem) error {
	if skippedOnImport(item.path) {
		return nil
	}
	if len(r.items) == MaxCopyEntries {
		return archiveError(ArchiveLimitReason, name, fmt.Sprintf("is beyond %d files and folders", MaxCopyEntries))
	}
	r.items = append(r.items, item)
	return nil
}

// read decompresses f, at most limit bytes; tooLarge is the error beyond
// it. A file read to its end has its size and checksum checked.
func (r *archiveReader) read(f *zip.File, limit int64, tooLarge func() *ArchiveError) ([]byte, error) {
	if f.UncompressedSize64 > uint64(limit) {
		return nil, tooLarge()
	}
	rc, err := f.Open()
	if err != nil {
		return nil, archiveError(ArchiveFormatReason, f.Name, err.Error())
	}
	defer rc.Close()
	data, err := io.ReadAll(io.LimitReader(rc, limit+1))
	if err != nil {
		return nil, archiveError(ArchiveFormatReason, f.Name, err.Error())
	}
	if int64(len(data)) > limit {
		return nil, tooLarge()
	}
	return data, nil
}

func (r *archiveReader) readManifest(f *zip.File) error {
	if r.manifest != nil {
		return archiveError(ArchiveEntryReason, f.Name, "duplicates another entry")
	}
	data, err := r.read(f, maxManifestBytes, func() *ArchiveError {
		return archiveError(ArchiveFormatReason, f.Name, fmt.Sprintf("exceeds %d KiB", maxManifestBytes>>10))
	})
	if err != nil {
		return err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil || m.Type != ArchiveType {
		return archiveError(ArchiveFormatReason, f.Name, "is not the manifest of a padmate profile")
	}
	if m.Format > ArchiveFormat {
		return archiveError(ArchiveVersionReason, f.Name, fmt.Sprintf("is format %d; this padmate reads format %d", m.Format, ArchiveFormat))
	}
	if m.Format != ArchiveFormat {
		return archiveError(ArchiveFormatReason, f.Name, fmt.Sprintf("has an unknown format %d", m.Format))
	}
	r.manifest = &m
	return nil
}

func (r *archiveReader) finish() (*Archive, error) {
	if r.manifest == nil {
		return nil, archiveError(ArchiveFormatReason, "", "has no "+ArchiveManifest+"; it is not an exported padmate profile")
	}
	if r.profile == nil {
		return nil, archiveError(ArchiveFormatReason, "", "has no "+ArchiveFolder+"profile.json")
	}
	p, ds := profile.Parse(r.profile)
	if ds.Err() != nil {
		e := archiveError(ArchiveProfileReason, ArchiveFolder+"profile.json", "does not validate")
		e.Diagnostics = append(profile.Diagnostics{}, ds...)
		return nil, e
	}
	slices.SortFunc(r.items, func(a, b copyItem) int { return strings.Compare(a.path, b.path) })
	return &Archive{Manifest: *r.manifest, Profile: p, items: r.items}, nil
}
