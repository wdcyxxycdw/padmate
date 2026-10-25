package authenticode

import (
	"os"
	"path/filepath"
	"testing"
)

// writeCopy writes data to a new file in a temporary folder and returns its path.
func writeCopy(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// The fixture is signed by a self-signed certificate nobody trusts: Windows
// finds the signature intact and the chain unfinished. Nothing here makes the
// machine trust it.
func TestVerifyTheFixtures(t *testing.T) {
	signed := fixture(t, "signed.pe")
	for _, c := range []struct {
		name string
		data []byte
		want Verdict
	}{
		{"signed by an untrusted root", signed, UntrustedRoot},
		{"not signed", fixture(t, "unsigned.pe"), NotSigned},
	} {
		got, err := Verify(writeCopy(t, "x.exe", c.data))
		if got != c.want || err != nil {
			t.Errorf("%s: %v, %v; want %v", c.name, got, err, c.want)
		}
	}

	// One byte of a section, after the signature: the digest no longer matches,
	// and that is reported before the untrusted root.
	changed := append([]byte(nil), signed...)
	changed[0x200+16] ^= 0xFF
	if got, err := Verify(writeCopy(t, "changed.exe", changed)); got != BadDigest || err != nil {
		t.Errorf("a changed file: %v, %v", got, err)
	}
}

func TestVerifyErrors(t *testing.T) {
	if got, err := Verify(filepath.Join(t.TempDir(), "missing.exe")); got != Other || err == nil {
		t.Errorf("a missing file: %v, %v", got, err)
	}
	if got, err := Verify("bad\x00name"); got != Other || err == nil {
		t.Errorf("a name with NUL: %v, %v", got, err)
	}
}
