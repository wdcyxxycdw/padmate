package cspint

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/csp"
	"github.com/wdcyxxycdw/padmate/daemon/internal/cspfile"
	"github.com/wdcyxxycdw/padmate/daemon/internal/integration"
)

// snapshot is CSP's settings as read from copies of its files.
type snapshot struct {
	files     files
	cmds      *csp.CommandSet // nil without default.khc
	tools     *csp.ToolTree   // nil without EditImageTool.todb
	toolShift bool
	revision  string
	an        *csp.Analyzer
}

// cached remembers the newest snapshot while the files keep their size and
// modification time.
type cached struct {
	key  string
	snap *snapshot
}

// snapshot returns the current settings, reading them again only when a
// file's size or modification time changed.
func (in *Integration) snapshot() (*snapshot, error) {
	f, err := in.locate()
	if err != nil {
		return nil, err
	}
	key := statKey(f)
	in.cacheMu.Lock()
	defer in.cacheMu.Unlock()
	if in.cache != nil && in.cache.key == key {
		return in.cache.snap, nil
	}
	s, err := readSnapshot(f)
	if err != nil {
		return nil, err
	}
	in.cache = &cached{key: key, snap: s}
	return s, nil
}

// fresh reads the settings again, whatever the cache says.
func (in *Integration) fresh() (*snapshot, error) {
	in.invalidate()
	return in.snapshot()
}

func (in *Integration) invalidate() {
	in.cacheMu.Lock()
	in.cache = nil
	in.cacheMu.Unlock()
}

func statKey(f files) string {
	var b strings.Builder
	b.WriteString(f.dir)
	for _, p := range []string{f.khc, f.todb, f.appConf} {
		b.WriteByte('|')
		if fi, err := os.Stat(p); err == nil {
			b.WriteString(strconv.FormatInt(fi.Size(), 10) + ":" + strconv.FormatInt(fi.ModTime().UnixNano(), 10))
		}
	}
	return b.String()
}

// readCopy copies src to dst and returns the fingerprint of src before the
// copy; it fails with ErrBusy when the copy differs, which means CSP wrote
// meanwhile.
func readCopy(dst, src string) (cspfile.Fingerprint, error) {
	for _, suffix := range []string{"-journal", "-wal"} {
		if _, err := os.Lstat(src + suffix); err == nil {
			return cspfile.Fingerprint{}, fmt.Errorf("%w: %s%s exists", integration.ErrBusy, src, suffix)
		}
	}
	for range 3 {
		before, err := cspfile.FingerprintOf(src)
		if err != nil {
			return cspfile.Fingerprint{}, err
		}
		if err := copyTo(dst, src); err != nil {
			return cspfile.Fingerprint{}, err
		}
		after, err := cspfile.FingerprintOf(dst)
		if err != nil {
			return cspfile.Fingerprint{}, err
		}
		if before.SHA256 == after.SHA256 {
			return before, nil
		}
	}
	return cspfile.Fingerprint{}, fmt.Errorf("%w: %s keeps changing", integration.ErrBusy, src)
}

func copyTo(dst, src string) (err error) {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, in)
	return err
}

// withCopy copies src into a private temporary folder, calls fn with the
// copy's path and removes the folder.
func withCopy(src string, fn func(copyPath string) error) error {
	tmp, err := os.MkdirTemp("", "padmate-csp-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	dst := filepath.Join(tmp, filepath.Base(src))
	if _, err := readCopy(dst, src); err != nil {
		return err
	}
	return fn(dst)
}

func unavailable(problem string, err error) error {
	return &integration.UnavailableError{Problem: problem, Err: err}
}

func readSnapshot(f files) (*snapshot, error) {
	s := &snapshot{files: f}
	tmp, err := os.MkdirTemp("", "padmate-csp-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	var sum strings.Builder
	fp := func(label string, fp cspfile.Fingerprint) {
		fmt.Fprintf(&sum, "%s:%d:%d:%s\n", label, fp.Size, fp.ModTime.UnixNano(), fp.SHA256)
	}
	if f.hasKHC {
		dst := filepath.Join(tmp, "default.khc")
		p, err := readCopy(dst, f.khc)
		if err != nil {
			return nil, err
		}
		fp("khc", p)
		if s.cmds, err = csp.ReadKHC(dst); err != nil {
			return nil, unavailable(integration.ProblemUnknownFormat, err)
		}
	}
	if f.hasTODB {
		dst := filepath.Join(tmp, "EditImageTool.todb")
		p, err := readCopy(dst, f.todb)
		if err != nil {
			return nil, err
		}
		fp("todb", p)
		if s.tools, err = csp.ReadTODB(dst); err != nil {
			return nil, unavailable(integration.ProblemUnknownFormat, err)
		}
	}
	if f.hasAConf {
		dst := filepath.Join(tmp, "Config.sqlite")
		// The preference is only a hint: a file that cannot be read means no
		// toolShift note.
		if _, err := readCopy(dst, f.appConf); err == nil {
			s.toolShift, _ = csp.ReadUseToolShift(dst)
		}
	}
	h := sha256.Sum256([]byte(sum.String()))
	s.revision = hex.EncodeToString(h[:])
	s.an = csp.NewAnalyzer(s.cmds, s.tools)
	return s, nil
}
