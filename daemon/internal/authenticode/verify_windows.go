package authenticode

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Verify asks Windows whether the signature of the file at path is valid:
// WinVerifyTrust with the generic Authenticode policy. It checks no
// revocation and fetches nothing from the network (cache-only URL retrieval),
// so the answer does not depend on the connection. It executes nothing.
//
// The error is nil for every Verdict but Other.
func Verify(path string) (Verdict, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return Other, err
	}
	info := &windows.WinTrustFileInfo{Size: uint32(unsafe.Sizeof(windows.WinTrustFileInfo{})), FilePath: name}
	data := &windows.WinTrustData{
		Size:             uint32(unsafe.Sizeof(windows.WinTrustData{})),
		UIChoice:         windows.WTD_UI_NONE,
		RevocationChecks: windows.WTD_REVOKE_NONE,
		UnionChoice:      windows.WTD_CHOICE_FILE,
		StateAction:      windows.WTD_STATEACTION_VERIFY,
		ProvFlags:        windows.WTD_REVOCATION_CHECK_NONE | windows.WTD_CACHE_ONLY_URL_RETRIEVAL,

		FileOrCatalogOrBlobOrSgnrOrCert: unsafe.Pointer(info),
	}
	verdict := windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	// The state must be closed whatever the verdict was.
	data.StateAction = windows.WTD_STATEACTION_CLOSE
	_ = windows.WinVerifyTrustEx(windows.InvalidHWND, &windows.WINTRUST_ACTION_GENERIC_VERIFY_V2, data)
	switch {
	case verdict == nil:
		return Valid, nil
	case errors.Is(verdict, windows.Errno(windows.TRUST_E_NOSIGNATURE)):
		return NotSigned, nil
	case errors.Is(verdict, windows.Errno(windows.TRUST_E_BAD_DIGEST)):
		return BadDigest, nil
	case errors.Is(verdict, windows.Errno(windows.CERT_E_UNTRUSTEDROOT)), errors.Is(verdict, windows.Errno(windows.CERT_E_CHAINING)):
		return UntrustedRoot, nil
	}
	return Other, fmt.Errorf("authenticode: WinVerifyTrust %s: %w", path, verdict)
}
