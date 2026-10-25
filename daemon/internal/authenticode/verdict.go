package authenticode

import "errors"

// Verdict is Windows' judgment of a file's signature (WinVerifyTrust).
type Verdict int

const (
	// Valid: signed, not changed since, and the chain ends in a root this
	// machine trusts.
	Valid Verdict = iota
	// NotSigned: the file has no signature (TRUST_E_NOSIGNATURE).
	NotSigned
	// BadDigest: the file was changed after it was signed (TRUST_E_BAD_DIGEST).
	BadDigest
	// UntrustedRoot: the signature is intact, but the chain ends in a root
	// this machine does not trust (CERT_E_UNTRUSTEDROOT, CERT_E_CHAINING). A
	// self-signed test certificate gives this.
	UntrustedRoot
	// Other: any other error; Verify returns it.
	Other
)

func (v Verdict) String() string {
	switch v {
	case Valid:
		return "valid"
	case NotSigned:
		return "not signed"
	case BadDigest:
		return "the file was changed after it was signed"
	case UntrustedRoot:
		return "signed, but the certificate chain does not end in a trusted root"
	}
	return "unknown"
}

// ErrUnsupported is returned by Verify on a system other than Windows.
var ErrUnsupported = errors.New("authenticode: WinVerifyTrust is only on Windows")
