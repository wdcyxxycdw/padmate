// Package authenticode reads the Authenticode signature of a Windows
// executable, to check that a release is signed by the right publisher
// (PKG-09, ADR-0073).
//
// Two questions are answered in two ways. Who signed it, whether it is
// timestamped and with which hash is read here in pure Go from the PE file's
// security directory (a PKCS#7 SignedData), so it is tested on Linux and
// fuzzed. Whether the signature is valid (the file is not changed since it was
// signed, the chain builds) is Windows' own verdict, WinVerifyTrust, in
// verify_windows.go.
//
// Nothing here signs, installs a certificate or touches a certificate store.
package authenticode

import (
	"bytes"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
)

// maxDirectory is the most the security directory may hold. A signature is
// 10 to 20 KB; the limit keeps a damaged file from asking for gigabytes.
const maxDirectory = 4 << 20

// Signature is the first signature of a file.
type Signature struct {
	// Signer is the certificate of the signerInfo: the leaf, found among the
	// certificates of the signature by issuer and serial number.
	Signer *x509.Certificate
	// Digest is the hash algorithm of the signature: "sha256", "sha1", ...
	Digest string
	// Timestamped says that the signerInfo carries a counter-signature or an
	// RFC 3161 timestamp token. Whether it is valid is Windows' to say.
	Timestamped bool
	// Certificates is how many certificates the signature carries.
	Certificates int
}

// Subject is the signer's distinguished name, "CN=...,O=...".
func (s *Signature) Subject() string { return s.Signer.Subject.String() }

// CommonName is the CN of the signer.
func (s *Signature) CommonName() string { return s.Signer.Subject.CommonName }

var (
	oidSignedData       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidCounterSignature = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 6}
	oidMSTimestamp      = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 3, 3, 1}

	digestNames = map[string]string{
		"1.3.14.3.2.26":           "sha1",
		"2.16.840.1.101.3.4.2.1":  "sha256",
		"2.16.840.1.101.3.4.2.2":  "sha384",
		"2.16.840.1.101.3.4.2.3":  "sha512",
		"1.2.840.113549.2.5":      "md5",
		"2.16.840.1.101.3.4.2.4":  "sha224",
		"2.16.840.1.101.3.4.2.12": "sha3-256",
	}
)

// ErrUnsigned is returned by ReadFile and Read when the file has no signature
// at all; a damaged one is any other error.
var ErrUnsigned = errors.New("authenticode: the file is not signed")

// ReadFile reads the signature of the PE file at path. It returns ErrUnsigned
// for a file without one.
func ReadFile(path string) (*Signature, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return Read(f, info.Size())
}

// Read reads the first signature in the security directory of the PE file r
// (size bytes long). It reads the headers and the directory only, not the
// file.
func Read(r io.ReaderAt, size int64) (*Signature, error) {
	blob, err := securityBlob(r, size)
	if err != nil {
		return nil, err
	}
	if blob == nil {
		return nil, ErrUnsigned
	}
	return parse(blob)
}

// securityBlob returns the PKCS#7 bytes of the first WIN_CERTIFICATE of the
// file, nil when the file has no security directory.
func securityBlob(r io.ReaderAt, size int64) ([]byte, error) {
	var dos [0x40]byte
	if _, err := r.ReadAt(dos[:], 0); err != nil || dos[0] != 'M' || dos[1] != 'Z' {
		return nil, errors.New("authenticode: not a PE file (no MZ header)")
	}
	pe := int64(binary.LittleEndian.Uint32(dos[0x3c:]))
	// The signature, the file header (20) and the optional header up to the
	// end of the data directories: 112 + 16*8 for PE32+.
	var head [4 + 20 + 240]byte
	n, _ := r.ReadAt(head[:], pe)
	if n < 4+20+2 || string(head[:4]) != "PE\x00\x00" {
		return nil, errors.New("authenticode: not a PE file (no PE signature)")
	}
	optionalSize := int(binary.LittleEndian.Uint16(head[4+16:]))
	opt := head[4+20 : n]
	var directories int
	switch magic := binary.LittleEndian.Uint16(opt); magic {
	case 0x10b:
		directories = 96
	case 0x20b:
		directories = 112
	default:
		return nil, fmt.Errorf("authenticode: unknown optional header magic %#x", magic)
	}
	const security = 4
	if directories+8*(security+1) > optionalSize || directories+8*(security+1) > len(opt) {
		return nil, nil
	}
	if count := int(binary.LittleEndian.Uint32(opt[directories-4:])); count <= security {
		return nil, nil
	}
	entry := opt[directories+8*security:]
	offset, length := int64(binary.LittleEndian.Uint32(entry)), int64(binary.LittleEndian.Uint32(entry[4:]))
	if length == 0 {
		return nil, nil
	}
	if offset < 0 || length < 8 || length > maxDirectory || offset+length > size {
		return nil, fmt.Errorf("authenticode: the security directory (offset %d, %d bytes) is outside the %d-byte file", offset, length, size)
	}
	dir := make([]byte, length)
	if _, err := r.ReadAt(dir, offset); err != nil {
		return nil, fmt.Errorf("authenticode: security directory: %w", err)
	}
	certLength := int64(binary.LittleEndian.Uint32(dir))
	revision, kind := binary.LittleEndian.Uint16(dir[4:]), binary.LittleEndian.Uint16(dir[6:])
	if revision != 0x0200 || kind != 0x0002 || certLength < 8 || certLength > length {
		return nil, fmt.Errorf("authenticode: unexpected WIN_CERTIFICATE (revision %#x, type %#x, length %d)", revision, kind, certLength)
	}
	return dir[8:certLength], nil
}

type contentInfo struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue `asn1:"explicit,tag:0"`
}

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type signerInfo struct {
	Version   int
	IAS       issuerAndSerial
	DigestAlg pkix.AlgorithmIdentifier
	AuthAttrs asn1.RawValue `asn1:"optional,tag:0"`
	EncAlg    pkix.AlgorithmIdentifier
	Sig       []byte
	Unauth    asn1.RawValue `asn1:"optional,tag:1"`
}

type signedData struct {
	Version    int
	DigestAlgs asn1.RawValue
	Content    asn1.RawValue
	Certs      asn1.RawValue `asn1:"optional,tag:0"`
	CRLs       asn1.RawValue `asn1:"optional,tag:1"`
	Signers    []signerInfo  `asn1:"set"`
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue
}

// parse reads a DER-encoded PKCS#7 ContentInfo holding a SignedData. BER with
// indefinite lengths, which some signers write, is not supported and is
// reported as an error rather than guessed at.
func parse(blob []byte) (*Signature, error) {
	var ci contentInfo
	if rest, err := asn1.Unmarshal(blob, &ci); err != nil {
		return nil, fmt.Errorf("authenticode: PKCS#7: %w", err)
	} else if len(bytes.TrimRight(rest, "\x00")) != 0 {
		return nil, errors.New("authenticode: PKCS#7: data after the ContentInfo")
	}
	if !ci.Type.Equal(oidSignedData) {
		return nil, fmt.Errorf("authenticode: PKCS#7 content type %v is not signedData", ci.Type)
	}
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("authenticode: SignedData: %w", err)
	}
	if len(sd.Signers) == 0 {
		return nil, errors.New("authenticode: the signature has no signerInfo")
	}
	var certs []*x509.Certificate
	for rest := sd.Certs.Bytes; len(rest) > 0; {
		var raw asn1.RawValue
		var err error
		if rest, err = asn1.Unmarshal(rest, &raw); err != nil {
			return nil, fmt.Errorf("authenticode: certificates: %w", err)
		}
		// A certificate Go cannot parse (an unusual algorithm) is skipped; the
		// signer is looked for among the others.
		if c, err := x509.ParseCertificate(raw.FullBytes); err == nil {
			certs = append(certs, c)
		}
	}
	si := sd.Signers[0]
	sig := &Signature{Certificates: len(certs), Digest: digestNames[si.DigestAlg.Algorithm.String()]}
	if sig.Digest == "" {
		sig.Digest = si.DigestAlg.Algorithm.String()
	}
	for _, c := range certs {
		if bytes.Equal(c.RawIssuer, si.IAS.Issuer.FullBytes) && c.SerialNumber.Cmp(si.IAS.Serial) == 0 {
			sig.Signer = c
			break
		}
	}
	if sig.Signer == nil {
		return nil, errors.New("authenticode: the signer's certificate is not in the signature")
	}
	sig.Timestamped = hasTimestamp(si.Unauth.Bytes)
	return sig, nil
}

// hasTimestamp scans the unauthenticated attributes of a signerInfo for a
// counter-signature or an RFC 3161 timestamp token. An attribute that cannot be
// read ends the scan: the signature itself is judged by Windows, and a damaged
// attribute cannot make a file look timestamped.
func hasTimestamp(attributes []byte) bool {
	for rest := attributes; len(rest) > 0; {
		var a attribute
		var err error
		if rest, err = asn1.Unmarshal(rest, &a); err != nil {
			return false
		}
		if a.Type.Equal(oidCounterSignature) || a.Type.Equal(oidMSTimestamp) {
			return true
		}
	}
	return false
}

// Matches says whether the signer is the subject want names: a common name
// ("padmate contributors") or a distinguished name ("CN=padmate
// contributors, O=padmate, C=JP"). A distinguished name matches when it lists
// the same attributes as the certificate's subject, in any order, ignoring case
// and the spaces after commas. It may be written the way Go writes a subject
// (a comma in a value after a backslash) or the way Windows does (the value in
// double quotes, a quote inside it doubled), which is what WindowsSubject
// prints and the message of a wrong publisher tells the owner to use.
func (s *Signature) Matches(want string) bool {
	want = strings.TrimSpace(want)
	if want == "" {
		return false
	}
	if !strings.Contains(want, "=") {
		return strings.EqualFold(s.CommonName(), want)
	}
	// Both spellings of the signer are tried, each with the reading of
	// backslashes that goes with it: Windows names attributes Go does not (E=,
	// T=, G=, OID.1.2.3), so only its own text matches those.
	for _, form := range []struct {
		have  string
		style dnStyle
	}{{s.Subject(), goStyle}, {s.WindowsSubject(), windowsStyle}} {
		wantPairs, ok := splitDN(want, form.style)
		if !ok {
			continue
		}
		if havePairs, ok := splitDN(form.have, form.style); ok && samePairs(wantPairs, havePairs) {
			return true
		}
	}
	return false
}

// samePairs says whether two lists hold the same attributes in any order,
// ignoring case.
func samePairs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	used := make([]bool, len(b))
next:
	for _, w := range a {
		for i, h := range b {
			if !used[i] && strings.EqualFold(w, h) {
				used[i] = true
				continue next
			}
		}
		return false
	}
	return true
}

// dnStyle is how a distinguished name treats a backslash.
type dnStyle int

const (
	// goStyle is the text of crypto/x509/pkix and RFC 4514: a backslash
	// escapes the character after it.
	goStyle dnStyle = iota
	// windowsStyle is the text of CertNameToStr: a backslash is a character
	// like any other (Windows quotes a value instead of escaping it).
	windowsStyle
)

// splitDN splits "CN=a, O=b\, c" at the commas that are not escaped or quoted,
// trims each attribute and normalizes the type to upper case: "CN=a", "O=b, c".
//
// A value that begins with a double quote runs to the next quote that is not
// doubled, as Windows writes a value that has a comma, a quote or the like in
// it: CN="a, ""b""" is the attribute CN=a, "b". A quote anywhere else is a
// character of the value, and a quote that is never closed is not a name.
func splitDN(dn string, style dnStyle) ([]string, bool) {
	var pairs []string
	var cur strings.Builder
	flush := func() bool {
		part := strings.TrimSpace(cur.String())
		cur.Reset()
		typ, value, found := strings.Cut(part, "=")
		if !found || strings.TrimSpace(typ) == "" {
			return false
		}
		typ = strings.ToUpper(strings.TrimSpace(typ))
		// Windows PowerShell writes the state as S=, Go as ST=.
		if typ == "S" {
			typ = "ST"
		}
		pairs = append(pairs, typ+"="+strings.TrimSpace(value))
		return true
	}
	// valueBegins says that the attribute so far is "TYPE=" and spaces.
	valueBegins := func() bool {
		_, value, found := strings.Cut(cur.String(), "=")
		return found && strings.TrimSpace(value) == ""
	}
	quoted := false
	for i := 0; i < len(dn); i++ {
		c := dn[i]
		switch {
		case quoted && c == '"' && i+1 < len(dn) && dn[i+1] == '"':
			cur.WriteByte('"')
			i++
		case quoted && c == '"':
			quoted = false
		case quoted:
			cur.WriteByte(c)
		case c == '"' && valueBegins():
			quoted = true
		case style == goStyle && c == '\\' && i+1 < len(dn):
			cur.WriteByte(dn[i+1])
			i++
		case c == ',':
			if !flush() {
				return nil, false
			}
		default:
			cur.WriteByte(c)
		}
	}
	if quoted || !flush() {
		return nil, false
	}
	return pairs, true
}
