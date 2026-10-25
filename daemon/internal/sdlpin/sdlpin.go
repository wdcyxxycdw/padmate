// Package sdlpin pins the SDL3 release that padmate ships next to its
// executables (PKG-01, ADR-0007). It is the single source of truth for the
// version and the hashes: tools/sdlfetch (`task sdl`) downloads and checks
// the DLL against them, and internal/input checks the loaded library's
// version against Major, Minor and Patch.
//
// Only Windows x64 is pinned. arm64 is future work: the release has an
// SDL3-<version>-win32-arm64.zip asset, which needs its own zip and DLL
// hashes selected by GOARCH.
//
// To bump SDL:
//  1. Update Major, Minor, Patch and Version, and set ZipSize and
//     ZipSHA256 from the release asset (GitHub shows each asset's SHA-256
//     digest, e.g. gh api repos/libsdl-org/SDL/releases/tags/release-X.Y.Z).
//  2. Download the zip, check it against those values, and set DLLSHA256
//     to the SHA-256 of the SDL3.dll inside it.
//  3. Run `task sdl`, then rerun the hardware matrix
//     (docs/hardware-matrix.md, ENG-05) with the new DLL.
package sdlpin

// The pinned SDL release.
const (
	Major = 3
	Minor = 4
	Patch = 16

	// Version is Major.Minor.Patch.
	Version = "3.4.16"

	// VersionNum is SDL_VERSIONNUM(Major, Minor, Patch), the value
	// SDL_GetVersion returns for this release.
	VersionNum = Major*1000000 + Minor*1000 + Patch
)

// The release asset that contains the DLL.
const (
	ZipName = "SDL3-" + Version + "-win32-x64.zip"
	ZipURL  = "https://github.com/libsdl-org/SDL/releases/download/release-" + Version + "/" + ZipName

	// ZipSize and ZipSHA256 are the asset's size in bytes and GitHub's
	// SHA-256 digest of it. A download of the asset on 2026-09-24 matched
	// both.
	ZipSize   = 1163613
	ZipSHA256 = "4217944b4e51457af4a59c82d883f8443b3e65964b2acd8943484c492756c4b6"
)

// The DLL inside the zip, at its root.
const (
	DLLName = "SDL3.dll"

	// DLLSHA256 is the SHA-256 of SDL3.dll extracted from the verified
	// zip (2844160 bytes, file version 3.4.16.0; the zip's .git-hash is
	// fa2c02bb6e21974a89ea9824bc53c9932abe5f9c). Computed on 2026-09-24
	// with `unzip -p SDL3-3.4.16-win32-x64.zip SDL3.dll | sha256sum` and
	// cross-checked with PowerShell's Get-FileHash on the extracted file.
	DLLSHA256 = "1f98969319302a100931f4385e5918a0bd53ab07773040682d22e7edb54858c0"
)
