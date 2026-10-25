package authenticode

import (
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// windows-subjects.json: distinguished names as DER and the string Windows'
// X500DistinguishedName.Name prints for them after decoding, which is what
// Get-AuthenticodeSignature's SignerCertificate.Subject is. The file was made
// by Windows PowerShell 5.1 on Windows 11 (docs/build-installer.md, "测试夹具").
func TestWindowsSubjectIsWhatWindowsPrints(t *testing.T) {
	var cases []struct {
		Input   string `json:"input"`
		DER     string `json:"der"`
		Subject string `json:"subject"`
	}
	readJSON(t, "windows-subjects.json", &cases)
	if len(cases) < 20 {
		t.Fatalf("only %d cases", len(cases))
	}
	for _, c := range cases {
		der, err := hex.DecodeString(c.DER)
		if err != nil {
			t.Fatalf("%q: %v", c.Input, err)
		}
		if got := windowsSubject(der); got != c.Subject {
			t.Errorf("windowsSubject(%q)\n got %q\nwant %q", c.Input, got, c.Subject)
		}
	}
}

// updater-publisher.json: whether electron-updater 6.8.9 accepts a signer
// (subject as above) for a publisherName. The answers were computed with the
// real parseDn of builder-util-runtime and the comparison of
// windowsExecutableCodeSignatureVerifier.js; app/scripts/publisher-name.test.mjs
// recomputes them on every run, so the two sides cannot drift apart.
func TestUpdaterAcceptsAsElectronUpdaterDoes(t *testing.T) {
	var cases []struct {
		Subject       string `json:"subject"`
		PublisherName string `json:"publisherName"`
		Accepts       bool   `json:"accepts"`
	}
	readJSON(t, "updater-publisher.json", &cases)
	if len(cases) < 50 {
		t.Fatalf("only %d cases", len(cases))
	}
	accepted := 0
	for _, c := range cases {
		if c.Accepts {
			accepted++
		}
		if got := updaterAccepts(c.Subject, c.PublisherName); got != c.Accepts {
			t.Errorf("updaterAccepts(%q, %q) = %v, electron-updater says %v", c.Subject, c.PublisherName, got, c.Accepts)
		}
	}
	if accepted == 0 || accepted == len(cases) {
		t.Errorf("%d of %d accepted: the table must have both answers", accepted, len(cases))
	}
}

// The real fixture: the exact comparison, not the lenient one of Matches.
func TestUpdaterAcceptsTheFixtureSigner(t *testing.T) {
	sig, err := readBytes(fixture(t, "signed.pe"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := sig.WindowsSubject(), "CN=padmate fixture, O=padmate contributors, C=JP"; got != want {
		t.Fatalf("WindowsSubject = %q, want %q", got, want)
	}
	for _, c := range []struct {
		name string
		ok   bool
	}{
		{"CN=padmate fixture, O=padmate contributors, C=JP", true},
		{"CN=padmate fixture", true},
		{"padmate fixture", true},
		// What Matches accepts and the updater does not.
		{"PADMATE FIXTURE", false},
		{" padmate fixture ", false},
		{"cn=padmate fixture, O=padmate contributors, C=JP", false},
		{"CN=padmate fixture, O=Padmate Contributors, C=JP", false},
		{"", false},
	} {
		if got := sig.UpdaterAccepts(c.name); got != c.ok {
			t.Errorf("UpdaterAccepts(%q) = %v, want %v", c.name, got, c.ok)
		}
		// Everything the updater accepts is also a name Matches knows, never the reverse.
		if c.ok && c.name != "CN=padmate fixture" && !sig.Matches(c.name) {
			t.Errorf("Matches(%q) = false for a name the updater accepts", c.name)
		}
	}
}

// A state written as ST= is how Go and openssl write it; Windows writes S=, so
// that is the key electron-updater looks up.
func TestUpdaterAcceptsTheStateAsWindowsWritesIt(t *testing.T) {
	cert := makeCert(t, nil, pkix.Name{CommonName: "Example", Organization: []string{"Example"}, Province: []string{"Tokyo"}, Country: []string{"JP"}})
	sig := &Signature{Signer: cert.cert}
	if got, want := sig.WindowsSubject(), "CN=Example, O=Example, S=Tokyo, C=JP"; got != want {
		t.Fatalf("WindowsSubject = %q, want %q", got, want)
	}
	if !sig.UpdaterAccepts("CN=Example, O=Example, S=Tokyo, C=JP") {
		t.Error("the state as Windows writes it was not accepted")
	}
	for _, bad := range []string{"CN=Example, O=Example, ST=Tokyo, C=JP", "ST=Tokyo", "S=Osaka"} {
		if sig.UpdaterAccepts(bad) {
			t.Errorf("%q was accepted", bad)
		}
	}
	// ...which the lenient match of -signed accepts all the same: it is not the
	// updater's rule and must not stand in for it.
	if !sig.Matches("CN=Example, O=Example, ST=Tokyo, C=JP") {
		t.Error("Matches no longer accepts ST=")
	}
}

func TestWindowsSubjectOfAnUnreadableName(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, {0x30}, {0x30, 0x03, 0x01, 0x01}, []byte("not der")} {
		if got := windowsSubject(raw); got != "" {
			t.Errorf("windowsSubject(% x) = %q, want empty", raw, got)
		}
	}
	// No name, no signer: nothing is accepted, not even an empty publisherName.
	if updaterAccepts("", "") || updaterAccepts("", "CN=x") {
		t.Error("an empty subject was accepted")
	}
}

func FuzzUpdaterAccepts(f *testing.F) {
	f.Add("CN=a, O=b", "CN=a")
	f.Add(`CN="a, b"`, `CN=a\, b`)
	f.Add("CN=a", `\`)
	f.Add("CN=a", `CN=\1`)
	f.Add("CN=a", `"`)
	f.Fuzz(func(t *testing.T, subject, name string) {
		// The parser is a port of JavaScript with its own index arithmetic: it
		// must never panic, whatever the two strings are.
		_ = updaterAccepts(subject, name)
	})
}

func readJSON(t *testing.T, name string, v any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}
