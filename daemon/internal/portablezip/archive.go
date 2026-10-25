// Package portablezip assembles a checked Windows x64 preview archive.
package portablezip

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"
)

type entry struct {
	name string
	data []byte
}

var archiveTime = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

func safeArchivePath(name string) bool {
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, `\:"*?<>|`) {
		return false
	}
	for _, ch := range name {
		if unicode.IsControl(ch) {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base, _, _ := strings.Cut(strings.ToUpper(part), ".")
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func writeArchive(ctx context.Context, file, prefix string, entries []entry) error {
	if !safeArchivePath(prefix) || strings.Contains(prefix, "/") {
		return fmt.Errorf("unsafe archive folder %q", prefix)
	}
	entries = slices.Clone(entries)
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	seen := map[string]bool{"sha256sums.txt": true}
	var sums strings.Builder
	for _, e := range entries {
		key := strings.ToLower(e.name)
		if !safeArchivePath(e.name) || seen[key] {
			return fmt.Errorf("unsafe or duplicate archive entry %q", e.name)
		}
		seen[key] = true
		_, _ = fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(e.data), e.name)
	}
	entries = append(entries, entry{name: "SHA256SUMS.txt", data: []byte(sums.String())})
	slices.SortFunc(entries, func(a, b entry) int { return strings.Compare(a.name, b.name) })
	f, err := os.CreateTemp(filepath.Dir(file), ".padmate-zip-*.tmp")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() { _ = os.Remove(temporary) }()
	z := zip.NewWriter(f)
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			_ = z.Close()
			_ = f.Close()
			return err
		}
		h := &zip.FileHeader{Name: prefix + "/" + e.name, Method: zip.Deflate, Modified: archiveTime}
		h.SetMode(0o644)
		w, err := z.CreateHeader(h)
		if err == nil {
			_, err = w.Write(e.data)
		}
		if err != nil {
			_ = z.Close()
			_ = f.Close()
			return err
		}
	}
	if err := errors.Join(z.Close(), f.Close()); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(temporary, file)
}
