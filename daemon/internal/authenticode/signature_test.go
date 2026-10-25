package authenticode

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/binary"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func readBytes(data []byte) (*Signature, error) { return Read(bytes.NewReader(data), int64(len(data))) }

// signed.pe is a 1 KB PE file signed by signtool with a throwaway self-signed
// certificate (see docs/build-installer.md), without a timestamp.
func TestReadsWhatSigntoolWrote(t *testing.T) {
	sig, err := readBytes(fixture(t, "signed.pe"))
	if err != nil {
		t.Fatal(err)
	}
	if got := sig.Subject(); !strings.Contains(got, "CN=padmate fixture") || !strings.Contains(got, "O=padmate contributors") || !strings.Contains(got, "C=JP") {
		t.Errorf("subject %q", got)
	}
	if sig.CommonName() != "padmate fixture" {
		t.Errorf("common name %q", sig.CommonName())
	}
	if sig.Digest != "sha256" {
		t.Errorf("digest %q", sig.Digest)
	}
	if sig.Timestamped {
		t.Error("the fixture was signed without a timestamp")
	}
	if sig.Certificates != 1 {
		t.Errorf("%d certificates", sig.Certificates)
	}
}

func TestUnsignedFile(t *testing.T) {
	if _, err := readBytes(fixture(t, "unsigned.pe")); !errors.Is(err, ErrUnsigned) {
		t.Errorf("unsigned.pe: %v", err)
	}
}

func TestMatches(t *testing.T) {
	sig, err := readBytes(fixture(t, "signed.pe"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		want string
		ok   bool
	}{
		{"padmate fixture", true},
		{"PADMATE FIXTURE", true},
		{" padmate fixture ", true},
		{"CN=padmate fixture, O=padmate contributors, C=JP", true},
		{"C=JP,O=padmate contributors,CN=padmate fixture", true},
		{"cn=Padmate Fixture; ", false},
		// A distinguished name names every attribute of the subject.
		{"CN=padmate fixture", false},
		{"CN=padmate fixture, O=padmate contributors", false},
		{"CN=padmate fixture, O=padmate contributors, C=JP, L=Tokyo", false},
		{"CN=padmate fixture, O=someone else, C=JP", false},
		{"padmate", false},
		{"fixture", false},
		{"", false},
		{"   ", false},
		{"CN=", false},
		{"=x", false},
		{"CN=padmate fixture,,", false},
	} {
		if got := sig.Matches(c.want); got != c.ok {
			t.Errorf("Matches(%q) = %v, want %v", c.want, got, c.ok)
		}
	}
}

func TestSplitDN(t *testing.T) {
	got, ok := splitDN(`CN=Open Source Developer\, Jane Doe, o=Org , C=JP`, goStyle)
	want := []string{"CN=Open Source Developer, Jane Doe", "O=Org", "C=JP"}
	if !ok || strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("splitDN = %q, %v", got, ok)
	}
}

// Windows does not escape a backslash, so in its text one is a character of the
// value (a name that ends in one is odd, but the text must still be read).
func TestSplitDNReadsBackslashesInTheStyleOfTheText(t *testing.T) {
	for _, c := range []struct {
		dn    string
		style dnStyle
		want  []string
	}{
		{`CN=a\, b, C=JP`, goStyle, []string{"CN=a, b", "C=JP"}},
		{`CN=a\, b, C=JP`, windowsStyle, nil},
		{`CN=\, O=","`, windowsStyle, []string{`CN=\`, "O=,"}},
		{`CN="a\", C=JP`, windowsStyle, []string{`CN=a\`, "C=JP"}},
	} {
		got, ok := splitDN(c.dn, c.style)
		if c.want == nil {
			if ok {
				t.Errorf("splitDN(%s, %d) = %q, want it refused", c.dn, c.style, got)
			}
			continue
		}
		if !ok || strings.Join(got, "|") != strings.Join(c.want, "|") {
			t.Errorf("splitDN(%s, %d) = %q, %v; want %q", c.dn, c.style, got, ok, c.want)
		}
	}
}

// Windows quotes a value that has a comma (or one of ,+="<>#; and so on) and
// doubles the quotes inside it. That is the form sign.go tells the owner to put in
// PADMATE_SIGN_SUBJECT, so it must split where Windows means it to.
func TestSplitDNHonoursWindowsQuotes(t *testing.T) {
	for _, c := range []struct {
		dn   string
		want []string
		ok   bool
	}{
		{`CN="Open Source Developer, Jane Doe", O=Org, C=JP`, []string{"CN=Open Source Developer, Jane Doe", "O=Org", "C=JP"}, true},
		{`CN="a ""b"" c", O=Org`, []string{`CN=a "b" c`, "O=Org"}, true},
		{`S="Tokyo, Japan", C=JP`, []string{"ST=Tokyo, Japan", "C=JP"}, true},
		{`CN="", C=JP`, []string{"CN=", "C=JP"}, true},
		{`CN="a=b;c+d", O="x"`, []string{"CN=a=b;c+d", "O=x"}, true},
		// A quote opens a quoted value only at its start, as Windows writes it.
		{`CN=5" pipe, C=JP`, []string{`CN=5" pipe`, "C=JP"}, true},
		// A quoted value that never ends is not a distinguished name.
		{`CN="a, b`, nil, false},
		{`CN="a, b""`, nil, false},
	} {
		for _, style := range []dnStyle{goStyle, windowsStyle} {
			got, ok := splitDN(c.dn, style)
			if ok != c.ok || (ok && strings.Join(got, "|") != strings.Join(c.want, "|")) {
				t.Errorf("splitDN(%s, %d) = %q, %v; want %q, %v", c.dn, style, got, ok, c.want, c.ok)
			}
		}
	}
}

// The owner copies the subject from the message that names the signer
// (WindowsSubject), puts it in PADMATE_SIGN_SUBJECT, and -signed must accept it:
// for every kind of value, not only the ones without a comma.
func TestMatchesTheSubjectAsWindowsWritesIt(t *testing.T) {
	for _, name := range []pkix.Name{
		{CommonName: "Open Source Developer, Jane Doe", Organization: []string{"Open Source Developer"}, Locality: []string{"Tokyo"}, Country: []string{"JP"}},
		{CommonName: `Quote "Dev"`, Organization: []string{"Example, Inc."}, Province: []string{"Tokyo"}, Country: []string{"JP"}},
		{CommonName: " padded ", Organization: []string{"a+b;c=d<e>"}, Country: []string{"JP"}},
		{CommonName: "plain", Organization: []string{"plain"}, Country: []string{"JP"}},
	} {
		cert := makeCert(t, nil, name)
		sig := &Signature{Signer: cert.cert}
		windows, goForm := sig.WindowsSubject(), sig.Subject()
		for _, want := range []string{windows, goForm} {
			if !sig.Matches(want) {
				t.Errorf("Matches(%q) = false for the signer %q", want, goForm)
			}
		}
		// The two checks agree: what the updater accepts of the name, -signed does too.
		if !sig.UpdaterAccepts(windows) {
			t.Errorf("UpdaterAccepts(%q) = false for its own subject", windows)
		}
		// A different value is still a different signer.
		if other := strings.Replace(windows, "JP", "US", 1); sig.Matches(other) {
			t.Errorf("Matches(%q) = true for another country", other)
		}
		if sig.Matches(windows + ", L=Osaka") {
			t.Errorf("Matches accepts an extra attribute on %q", windows)
		}
	}
}

// A subject whose commas Go escapes still matches the plain name.
func TestMatchesAnEscapedSubject(t *testing.T) {
	cert := makeCert(t, nil, pkix.Name{CommonName: "Developer, Example", Organization: []string{"Example, Inc."}, Country: []string{"JP"}})
	sig := &Signature{Signer: cert.cert}
	for _, want := range []string{"Developer, Example", `CN=Developer\, Example, O=Example\, Inc., C=JP`} {
		if !sig.Matches(want) {
			t.Errorf("%q does not match %q", want, sig.Subject())
		}
	}
}

// Windows PowerShell writes the state of a distinguished name as S=, Go as ST=.
func TestMatchesTheStateAsPowerShellWritesIt(t *testing.T) {
	cert := makeCert(t, nil, pkix.Name{CommonName: "Example", Organization: []string{"Example"}, Province: []string{"Tokyo"}, Country: []string{"JP"}})
	sig := &Signature{Signer: cert.cert}
	for _, want := range []string{"CN=Example, O=Example, S=Tokyo, C=JP", "CN=Example, O=Example, ST=Tokyo, C=JP"} {
		if !sig.Matches(want) {
			t.Errorf("%q does not match %q", want, sig.Subject())
		}
	}
	if sig.Matches("CN=Example, O=Example, S=Osaka, C=JP") {
		t.Error("another state matched")
	}
}

type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// makeCert makes a certificate with a fresh P-256 key; with a parent it is
// issued by it, without it is self-signed.
func makeCert(t testing.TB, parent *testCert, name pkix.Name) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: name,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true, IsCA: parent == nil,
	}
	issuer, issuerKey := template, key
	if parent != nil {
		issuer, issuerKey = parent.cert, parent.key
	}
	der, err := x509.CreateCertificate(rand.Reader, template, issuer, &key.PublicKey, issuerKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{cert, key}
}

func mustMarshal(t testing.TB, v any) []byte {
	t.Helper()
	b, err := asn1.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// der encodes one value of the given class and tag around content.
func der(t testing.TB, class, tag int, compound bool, content ...[]byte) []byte {
	t.Helper()
	return mustMarshal(t, asn1.RawValue{Class: class, Tag: tag, IsCompound: compound, Bytes: bytes.Join(content, nil)})
}

var (
	oidSHA256   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA1     = asn1.ObjectIdentifier{1, 3, 14, 3, 2, 26}
	oidECDSA    = asn1.ObjectIdentifier{1, 2, 840, 10045, 4, 3, 2}
	oidData     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSpcIndir = asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 1, 4}
)

// signatureOptions shape the PKCS#7 blob that buildSignature makes.
type signatureOptions struct {
	digest asn1.ObjectIdentifier
	// timestamp adds an unauthenticated attribute of this type.
	timestamp asn1.ObjectIdentifier
	// certs are the certificates in the blob, signer is the one named in the
	// signerInfo (it need not be in certs).
	certs  []*x509.Certificate
	signer *x509.Certificate
}

func buildSignature(t testing.TB, o signatureOptions) []byte {
	t.Helper()
	if o.digest == nil {
		o.digest = oidSHA256
	}
	algorithm := func(oid asn1.ObjectIdentifier) []byte {
		return mustMarshal(t, pkix.AlgorithmIdentifier{Algorithm: oid, Parameters: asn1.NullRawValue})
	}
	var certs [][]byte
	for _, c := range o.certs {
		certs = append(certs, c.Raw)
	}
	signerFields := [][]byte{
		mustMarshal(t, 1),
		der(t, 0, 16, true, o.signer.RawIssuer, mustMarshal(t, o.signer.SerialNumber)),
		algorithm(o.digest),
		algorithm(oidECDSA),
		mustMarshal(t, []byte("signature")),
	}
	if o.timestamp != nil {
		attribute := der(t, 0, 16, true, mustMarshal(t, o.timestamp), der(t, 0, 17, true, der(t, 0, 16, true)))
		signerFields = append(signerFields, der(t, 2, 1, true, attribute))
	}
	signedData := der(t, 0, 16, true,
		mustMarshal(t, 1),
		der(t, 0, 17, true, algorithm(o.digest)),
		der(t, 0, 16, true, mustMarshal(t, oidSpcIndir), der(t, 2, 0, true, der(t, 0, 16, true, mustMarshal(t, oidData)))),
		der(t, 2, 0, true, certs...),
		der(t, 0, 17, true, der(t, 0, 16, true, signerFields...)),
	)
	return der(t, 0, 16, true, mustMarshal(t, oidSignedData), der(t, 2, 0, true, signedData))
}

// withSignature appends blob to the 1 KB PE file as its certificate table and
// points the security directory at it, as signtool does.
func withSignature(t testing.TB, blob []byte) []byte {
	t.Helper()
	pe := fixture(t, "unsigned.pe")
	entry := 0x58 + 112 + 8*4
	cert := make([]byte, 8+len(blob))
	binary.LittleEndian.PutUint32(cert, uint32(len(cert)))
	binary.LittleEndian.PutUint16(cert[4:], 0x0200)
	binary.LittleEndian.PutUint16(cert[6:], 0x0002)
	copy(cert[8:], blob)
	for len(cert)%8 != 0 {
		cert = append(cert, 0)
	}
	out := append(bytes.Clone(pe), cert...)
	binary.LittleEndian.PutUint32(out[entry:], uint32(len(pe)))
	binary.LittleEndian.PutUint32(out[entry+4:], uint32(len(cert)))
	return out
}

func TestReadsASignatureBuiltHere(t *testing.T) {
	root := makeCert(t, nil, pkix.Name{CommonName: "Test Root"})
	leaf := makeCert(t, &root, pkix.Name{CommonName: "Example Publisher", Organization: []string{"Example"}, Country: []string{"JP"}})
	other := makeCert(t, &root, pkix.Name{CommonName: "Someone Else"})
	for _, c := range []struct {
		name        string
		options     signatureOptions
		timestamped bool
		digest      string
	}{
		{"the leaf alone", signatureOptions{certs: []*x509.Certificate{leaf.cert}, signer: leaf.cert}, false, "sha256"},
		// The signer is found by issuer and serial among the chain, wherever it is.
		{"the leaf last", signatureOptions{certs: []*x509.Certificate{root.cert, other.cert, leaf.cert}, signer: leaf.cert}, false, "sha256"},
		{"the leaf first", signatureOptions{certs: []*x509.Certificate{leaf.cert, root.cert}, signer: leaf.cert}, false, "sha256"},
		{"an RFC 3161 timestamp", signatureOptions{certs: []*x509.Certificate{leaf.cert, root.cert}, signer: leaf.cert, timestamp: oidMSTimestamp}, true, "sha256"},
		{"a legacy counter-signature", signatureOptions{certs: []*x509.Certificate{leaf.cert}, signer: leaf.cert, timestamp: oidCounterSignature}, true, "sha256"},
		{"another unauthenticated attribute", signatureOptions{certs: []*x509.Certificate{leaf.cert}, signer: leaf.cert, timestamp: asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 311, 2, 4, 1}}, false, "sha256"},
		{"sha1", signatureOptions{certs: []*x509.Certificate{leaf.cert}, signer: leaf.cert, digest: oidSHA1}, false, "sha1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			sig, err := readBytes(withSignature(t, buildSignature(t, c.options)))
			if err != nil {
				t.Fatal(err)
			}
			if sig.CommonName() != "Example Publisher" || sig.Signer.SerialNumber.Cmp(leaf.cert.SerialNumber) != 0 {
				t.Errorf("signer %q", sig.Subject())
			}
			if sig.Timestamped != c.timestamped || sig.Digest != c.digest || sig.Certificates != len(c.options.certs) {
				t.Errorf("timestamped %v, digest %q, %d certificates", sig.Timestamped, sig.Digest, sig.Certificates)
			}
			if !sig.Matches("Example Publisher") || !sig.Matches("CN=Example Publisher, O=Example, C=JP") || sig.Matches("Test Root") {
				t.Errorf("Matches is wrong for %q", sig.Subject())
			}
		})
	}
}

func TestRefusesWhatIsDamaged(t *testing.T) {
	root := makeCert(t, nil, pkix.Name{CommonName: "Test Root"})
	leaf := makeCert(t, &root, pkix.Name{CommonName: "Example Publisher"})
	other := makeCert(t, &root, pkix.Name{CommonName: "Someone Else"})
	good := buildSignature(t, signatureOptions{certs: []*x509.Certificate{leaf.cert}, signer: leaf.cert})

	// The signer's certificate is not among the certificates.
	missing := buildSignature(t, signatureOptions{certs: []*x509.Certificate{other.cert, root.cert}, signer: leaf.cert})
	// Not signedData.
	notSigned := bytes.Replace(good, mustMarshal(t, oidSignedData), mustMarshal(t, oidData), 1)
	// The same blob with an indefinite length, which BER allows and DER does not.
	indefinite := append([]byte{0x30, 0x80}, append(bytes.Clone(good[bytes.IndexByte(good, 0x06):]), 0, 0)...)

	for name, blob := range map[string][]byte{
		"the signer is not among the certificates": missing,
		"not signedData":                notSigned,
		"BER with an indefinite length": indefinite,
		"cut short":                     good[:len(good)/2],
		"garbage":                       bytes.Repeat([]byte{0xAB}, 64),
		"empty":                         {0x30, 0x00},
	} {
		sig, err := readBytes(withSignature(t, blob))
		if err == nil || sig != nil || errors.Is(err, ErrUnsigned) {
			t.Errorf("%s: %v, %v", name, sig, err)
		}
	}
}

func TestRefusesADirectoryThatIsNotOneThatFits(t *testing.T) {
	root := makeCert(t, nil, pkix.Name{CommonName: "Test Root"})
	good := withSignature(t, buildSignature(t, signatureOptions{certs: []*x509.Certificate{root.cert}, signer: root.cert}))
	entry := 0x58 + 112 + 8*4
	for name, patch := range map[string]func([]byte){
		"an offset beyond the file": func(b []byte) { binary.LittleEndian.PutUint32(b[entry:], uint32(len(b))) },
		"a size beyond the file":    func(b []byte) { binary.LittleEndian.PutUint32(b[entry+4:], uint32(len(b))) },
		"a size over the limit":     func(b []byte) { binary.LittleEndian.PutUint32(b[entry+4:], maxDirectory+8) },
		"a size under a header":     func(b []byte) { binary.LittleEndian.PutUint32(b[entry+4:], 4) },
		"another revision": func(b []byte) {
			binary.LittleEndian.PutUint16(b[int(binary.LittleEndian.Uint32(b[entry:]))+4:], 0x0100)
		},
		"another certificate type": func(b []byte) {
			binary.LittleEndian.PutUint16(b[int(binary.LittleEndian.Uint32(b[entry:]))+6:], 0x0001)
		},
		"a certificate longer":        func(b []byte) { binary.LittleEndian.PutUint32(b[int(binary.LittleEndian.Uint32(b[entry:])):], 1<<20) },
		"a certificate shorter":       func(b []byte) { binary.LittleEndian.PutUint32(b[int(binary.LittleEndian.Uint32(b[entry:])):], 4) },
		"a PE signature that is none": func(b []byte) { b[0x40] = 'X' },
		"no MZ":                       func(b []byte) { b[0] = 'X' },
		"an unknown optional header":  func(b []byte) { binary.LittleEndian.PutUint16(b[0x58:], 0x1234) },
	} {
		b := bytes.Clone(good)
		patch(b)
		if sig, err := readBytes(b); err == nil || errors.Is(err, ErrUnsigned) {
			t.Errorf("%s: %v, %v", name, sig, err)
		}
	}
	// A file with fewer data directories than the security one is unsigned, not damaged.
	b := bytes.Clone(good)
	binary.LittleEndian.PutUint32(b[0x58+108:], 2)
	if _, err := readBytes(b); !errors.Is(err, ErrUnsigned) {
		t.Errorf("two data directories: %v", err)
	}
	// So is a file too short to have an optional header at all.
	if _, err := readBytes(good[:0x60]); err == nil {
		t.Error("a truncated header was read")
	}
}

func FuzzRead(f *testing.F) {
	root := makeCert(f, nil, pkix.Name{CommonName: "Test Root"})
	f.Add(fixture(f, "signed.pe"))
	f.Add(fixture(f, "unsigned.pe"))
	f.Add(withSignature(f, buildSignature(f, signatureOptions{certs: []*x509.Certificate{root.cert}, signer: root.cert, timestamp: oidMSTimestamp})))
	f.Add([]byte("MZ"))
	f.Fuzz(func(t *testing.T, data []byte) {
		sig, err := Read(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			if sig != nil {
				t.Fatal("a signature and an error")
			}
			return
		}
		// Whatever was read can be used.
		_ = sig.Subject()
		_ = sig.Matches("CN=x, O=y")
		_ = sig.Matches(sig.CommonName())
		// The text the owner is told to use names the signer it was read from.
		if w := sig.WindowsSubject(); w != "" && !sig.Matches(w) {
			t.Fatalf("Matches(%q) = false for its own WindowsSubject", w)
		}
	})
}

// Whatever the names are, the signer is named by the two texts the owner meets:
// Go's subject and Windows' (the one the wrong-publisher message prints).
func FuzzMatchesOwnSubject(f *testing.F) {
	f.Add("padmate", "padmate contributors")
	f.Add("Open Source Developer, Jane Doe", "Open Source Developer")
	f.Add(`Quote "Dev"`, `"x", y`)
	f.Add(` padded `, `a+b;c=d<e>#`)
	f.Add(`back\slash\`, `\,`)
	// The case the fuzzer found first: Windows writes a backslash as it is.
	f.Add(`\`, `,`)
	f.Add(`"`, `""`)
	f.Add("a=b", "S=c")
	root := makeCert(f, nil, pkix.Name{CommonName: "Test Root"})
	f.Fuzz(func(t *testing.T, cn, org string) {
		if len(cn) > 64 || len(org) > 64 || !utf8.ValidString(cn) || !utf8.ValidString(org) {
			t.Skip()
		}
		name := pkix.Name{CommonName: cn, Country: []string{"JP"}}
		if org != "" {
			name.Organization = []string{org}
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: name, NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
		der, err := x509.CreateCertificate(rand.Reader, template, root.cert, &root.key.PublicKey, root.key)
		if err != nil {
			t.Skip()
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Skip()
		}
		sig := &Signature{Signer: cert}
		for _, text := range []string{sig.Subject(), sig.WindowsSubject()} {
			if !sig.Matches(text) {
				t.Fatalf("Matches(%q) = false for a signer with CN %q and O %q", text, cn, org)
			}
		}
		if w := sig.WindowsSubject(); !sig.UpdaterAccepts(w) {
			t.Fatalf("UpdaterAccepts(%q) = false for its own subject", w)
		}
	})
}
