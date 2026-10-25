package installerpkg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/wdcyxxycdw/padmate/daemon/internal/authenticode"
	"github.com/wdcyxxycdw/padmate/daemon/internal/sdlpin"
)

// AppUpdateFile is electron-builder's description of where the app looks for
// updates, in resources; it is where publisherName is written (PKG-09, PKG-10).
const AppUpdateFile = "resources/app-update.yml"

// SignedFiles are the files of an installation that are signed, relative to
// its root: the Electron app, the daemon and the CLI. The uninstaller
// ("Uninstall padmate.exe") is signed too, but electron-builder deletes it
// from the output folder, so it is checked in an installed tree (see
// UninstallerFile). SDL3.dll is not signed: its SHA-256 is pinned (CheckTree).
var SignedFiles = []string{AppFile, binFolder + "/padmated.exe", binFolder + "/padmatectl.exe"}

// UninstallerFile is the uninstaller electron-builder's NSIS template writes
// into an installation.
const UninstallerFile = "Uninstall " + ProductName + ".exe"

// SignOptions says what is demanded of a signature.
type SignOptions struct {
	// Subject is the publisher the files must be signed by: a common name or a
	// distinguished name (authenticode.Signature.Matches).
	Subject string
	// AllowUntrustedRoot accepts a signature whose chain does not end in a
	// trusted root: a self-signed test certificate. A file that was changed
	// after it was signed, or is not signed, is never accepted.
	AllowUntrustedRoot bool
	// Timestamp demands an RFC 3161 timestamp, so that the signature stays
	// valid after the certificate expires. Off only for the offline test.
	Timestamp bool
	// Verify is Windows' verdict on a file; nil means authenticode.Verify.
	Verify func(path string) (authenticode.Verdict, error)
}

// CheckSignedFile checks that the file at path is signed as o demands and
// returns what is wrong, one message per problem, none when it is right.
func CheckSignedFile(path string, o SignOptions) []string {
	var problems []string
	add := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	sig, err := authenticode.ReadFile(path)
	switch {
	case errors.Is(err, authenticode.ErrUnsigned):
		add("is not signed")
	case err != nil:
		add("signature: %v", err)
	default:
		if !sig.Matches(o.Subject) {
			add("is signed by %q, want %q", sig.Subject(), o.Subject)
		}
		if sig.Digest != "sha256" {
			add("is signed with %s, want sha256", sig.Digest)
		}
		if o.Timestamp && !sig.Timestamped {
			add("has no timestamp: the signature would end with the certificate")
		}
		// Windows' verdict: that the file is as it was signed and that the
		// chain ends well. It is asked only of a signature that could be read.
		verify := o.Verify
		if verify == nil {
			verify = authenticode.Verify
		}
		switch verdict, err := verify(path); {
		case err != nil:
			add("Windows could not judge the signature: %v", err)
		case verdict == authenticode.Valid, verdict == authenticode.UntrustedRoot && o.AllowUntrustedRoot:
		default:
			add("Windows says: %s", verdict)
		}
	}
	return problems
}

// CheckSignatures checks the signatures of a built installation (electron-builder's
// win-unpacked) and of the installer: the Electron app, the daemon, the CLI
// and the installer are signed by o.Subject; SDL3.dll is not signed; and
// resources/app-update.yml names the publisher the updater will accept
// (PKG-09, PKG-10).
func CheckSignatures(root, installer string, o SignOptions) []string {
	var problems []string
	for _, name := range SignedFiles {
		for _, p := range CheckSignedFile(filepath.Join(root, filepath.FromSlash(name)), o) {
			problems = append(problems, name+": "+p)
		}
	}
	for _, p := range CheckSignedFile(installer, o) {
		problems = append(problems, filepath.Base(installer)+": "+p)
	}
	dll := binFolder + "/" + sdlpin.DLLName
	if _, err := authenticode.ReadFile(filepath.Join(root, filepath.FromSlash(dll))); err == nil {
		problems = append(problems, dll+": is signed, but the pinned SDL3.dll is shipped as it is (its SHA-256 is checked)")
	}
	problems = append(problems, checkPublisherName(filepath.Join(root, filepath.FromSlash(AppUpdateFile)), installer, o.Subject)...)
	return problems
}

// checkPublisherName reads app-update.yml. electron-updater checks the
// signature of a downloaded update only when publisherName is there, and
// quietly installs without checking when it is not, so a signed build without
// it is a mistake. So is a build without the file: the publish block of
// electron-builder.yml (PKG-10) makes electron-builder write it into every
// build, signed or not, and only the builds before PKG-10 lacked it (they are
// never checked with -signed).
func checkPublisherName(file, installer, subject string) []string {
	data, err := os.ReadFile(file)
	name := filepath.ToSlash(AppUpdateFile)
	if errors.Is(err, os.ErrNotExist) {
		return []string{name + ": is missing: electron-updater would have no publisher to check the signer of an update against (the publish block of electron-builder.yml makes electron-builder write the file)"}
	}
	if err != nil {
		return []string{name + ": " + err.Error()}
	}
	names := publisherNames(string(data))
	if len(names) == 0 {
		return []string{name + ": has no publisherName: electron-updater would install an update without checking who signed it (set PADMATE_SIGN_SUBJECT)"}
	}
	sig, err := authenticode.ReadFile(installer)
	if err != nil {
		// The installer's own check says why.
		return nil
	}
	// Not Matches: that is the lenient comparison of -signed. The updater
	// compares exactly (case, S= for the state, every listed attribute), and a
	// publisherName it rejects makes every client refuse every update.
	for _, n := range names {
		if sig.UpdaterAccepts(n) {
			return nil
		}
	}
	return []string{fmt.Sprintf("%s: publisherName %q does not name the installer's signer %q: electron-updater compares exactly (case, S= for the state, every attribute listed) and would refuse every update; set PADMATE_SIGN_SUBJECT to the subject as shown", name, strings.Join(names, ", "), sig.WindowsSubject())}
}

// publisherNames lists the entries of publisherName in an app-update.yml,
// which electron-builder writes as a scalar or as a list:
//
//	publisherName:
//	  - CN=padmate
func publisherNames(text string) []string {
	var names []string
	in := false
	for line := range strings.SplitSeq(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "publisherName:"):
			in = true
			if v := unquote(strings.TrimSpace(strings.TrimPrefix(line, "publisherName:"))); v != "" {
				names = append(names, v)
			}
		case in && strings.HasPrefix(strings.TrimLeft(line, " "), "- "):
			if v := unquote(strings.TrimSpace(strings.TrimPrefix(strings.TrimLeft(line, " "), "- "))); v != "" {
				names = append(names, v)
			}
		case line != "" && line[0] != ' ' && line[0] != '#':
			in = false
		}
	}
	return names
}

// unquote reads a YAML scalar as a YAML parser does: inside
// single quotes ” is one quote, inside double quotes \" and \\ are escapes,
// and nothing inside quotes is trimmed (publisherName is compared exactly).
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	inner := s[1 : len(s)-1]
	switch {
	case s[0] == '\'' && s[len(s)-1] == '\'':
		return strings.ReplaceAll(inner, "''", "'")
	case s[0] == '"' && s[len(s)-1] == '"':
		return strings.NewReplacer(`\"`, `"`, `\\`, `\`).Replace(inner)
	}
	return s
}
