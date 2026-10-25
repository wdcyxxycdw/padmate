// Package exeresources adds icons and version metadata to built Windows files.
// It never produces source-tree .syso files or invokes a native resource compiler.
package exeresources

import (
	"bytes"
	"debug/pe"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/tc-hib/winres"
	resversion "github.com/tc-hib/winres/version"

	"github.com/wdcyxxycdw/padmate/daemon/internal/branding"
)

var (
	safeLabel = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)
	semver    = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:\.([0-9]+))?(?:[-+][A-Za-z0-9._+-]+)?$`)
)

// FixedVersion validates a build label and obtains its Windows numeric tuple.
// Untagged revisions and dev retain their exact string and use numeric 0.0.0.0.
func FixedVersion(label string) ([4]uint16, error) {
	var fixed [4]uint16
	if !safeLabel.MatchString(label) {
		return fixed, fmt.Errorf("invalid build version %q: use 1-128 ASCII letters, numbers, dots, underscores, pluses or hyphens", label)
	}
	parts := semver.FindStringSubmatch(label)
	if parts == nil {
		return fixed, nil
	}
	for i, part := range parts[1:] {
		if part == "" {
			continue
		}
		n, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return [4]uint16{}, fmt.Errorf("build version %q: numeric components must be in [0, 65535]", label)
		}
		fixed[i] = uint16(n)
	}
	return fixed, nil
}

// Write copies src into dst with fresh icon and version resources. The streams
// must refer to different files. The caller closes them before replacing a file.
func Write(dst io.Writer, src io.ReadSeeker, filename, label string) error {
	fixed, err := FixedVersion(label)
	if err != nil {
		return err
	}
	if filepath.Base(filename) != filename || strings.ContainsAny(filename, `\/:`) || !strings.HasSuffix(filename, ".exe") {
		return fmt.Errorf("invalid executable filename %q", filename)
	}
	ico, err := branding.ICO()
	if err != nil {
		return err
	}
	icon, err := winres.LoadICO(bytes.NewReader(ico))
	if err != nil {
		return fmt.Errorf("load application icon: %w", err)
	}
	resources := &winres.ResourceSet{}
	if err := resources.SetIcon(winres.ID(1), icon); err != nil {
		return fmt.Errorf("set application icon: %w", err)
	}
	info := resversion.Info{FileVersion: fixed, ProductVersion: fixed, Type: resversion.App}
	for key, value := range map[string]string{
		resversion.FileVersion: label, resversion.ProductVersion: label,
		resversion.ProductName: "padmate", resversion.FileDescription: strings.TrimSuffix(filename, ".exe"),
		resversion.InternalName: strings.TrimSuffix(filename, ".exe"), resversion.OriginalFilename: filename,
		resversion.LegalCopyright: "Copyright (c) 2026 padmate contributors", resversion.CompanyName: "padmate contributors",
	} {
		if err := info.Set(resversion.LangNeutral, key, value); err != nil {
			return err
		}
	}
	info.Flags.Prerelease = strings.Contains(label, "-")
	resources.SetVersionInfo(info)
	if err := resources.WriteToEXE(dst, src); err != nil {
		return fmt.Errorf("write executable resources: %w", err)
	}
	return nil
}

// Check validates a Windows x64 executable before build publication or packing.
func Check(src interface {
	io.ReadSeeker
	io.ReaderAt
}, filename, label string, gui bool) error {
	p, err := pe.NewFile(src)
	if err != nil {
		return err
	}
	header, ok := p.OptionalHeader.(*pe.OptionalHeader64)
	if !ok || p.Machine != pe.IMAGE_FILE_MACHINE_AMD64 {
		return fmt.Errorf("%s is not a Windows x64 executable", filename)
	}
	wantSubsystem := uint16(pe.IMAGE_SUBSYSTEM_WINDOWS_CUI)
	if gui {
		wantSubsystem = pe.IMAGE_SUBSYSTEM_WINDOWS_GUI
	}
	if header.Subsystem != wantSubsystem {
		return fmt.Errorf("%s subsystem=%d, want %d", filename, header.Subsystem, wantSubsystem)
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}
	resources, err := winres.LoadFromEXE(src)
	if err != nil {
		return fmt.Errorf("%s resources: %w", filename, err)
	}
	if _, err := resources.GetIcon(winres.ID(1)); err != nil {
		return fmt.Errorf("%s icon: %w", filename, err)
	}
	info, err := resversion.FromBytes(resources.Get(winres.RT_VERSION, winres.ID(1), resversion.LangNeutral))
	if err != nil {
		return fmt.Errorf("%s version resource: %w", filename, err)
	}
	table := info.Table()[resversion.LangNeutral]
	if table == nil || (*table)[resversion.FileVersion] != label || (*table)[resversion.ProductVersion] != label || (*table)[resversion.OriginalFilename] != filename {
		return fmt.Errorf("%s version resource does not match build version %q", filename, label)
	}
	fixed, err := FixedVersion(label)
	if err != nil || info.FileVersion != fixed || info.ProductVersion != fixed {
		return fmt.Errorf("%s numeric version resource does not match %q", filename, label)
	}
	return nil
}
