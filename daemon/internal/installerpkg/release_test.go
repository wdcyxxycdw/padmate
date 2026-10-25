package installerpkg

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/wdcyxxycdw/padmate/daemon/internal/publicmirror"
)

func releaseWorkflow(t *testing.T) string {
	t.Helper()
	publicmirror.SkipIfMirror(t, ".github/workflows/release.yml")
	data, err := os.ReadFile(filepath.Join("..", "..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// The release workflow has never run on GitHub (remote CI is off), so its
// strings are pinned here: what a release holds, how it is marked, and where
// the signing secrets are read (PKG-07, PKG-09, PKG-10, ADR-0073).
func TestReleaseWorkflow(t *testing.T) {
	text := releaseWorkflow(t)

	// latest.yml goes up with every release, in the workflow's artifact and in
	// the draft release, next to the installer and its checksum.
	if n := strings.Count(text, "dist/installer/latest.yml"); n < 2 {
		t.Errorf("latest.yml is named %d times, want it in the artifact and in the release files", n)
	}

	// A version with a prerelease part is a pre-release, any other the latest
	// release; either is a draft, which the owner publishes.
	for _, want := range []string{
		"case \"$VERSION\" in",
		"*-*) kind=--prerelease ;;",
		"*) kind=--latest ;;",
		`--draft "$kind"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}
	if strings.Contains(text, "--draft --prerelease") || strings.Contains(text, "--prerelease \\") {
		t.Error("release.yml marks every release a pre-release: the stable 1.0.0 would not reach the installed applications (they follow the latest release)")
	}
	if !strings.Contains(text, "gh release upload \"$VERSION\" \"${files[@]}\" --clobber") || !strings.Contains(text, "is published already") {
		t.Error("release.yml no longer fills only a draft")
	}

	// The release is checked against its latest.yml, and against its signer
	// when it is signed, before anything is drafted.
	verify := strings.Index(text, "go run ./tools/packageinstaller verify -version \"$VERSION\" -dist ../dist/installer \"${flags[@]}\"")
	draft := strings.Index(text, "gh release create")
	if verify < 0 || draft < 0 || verify > draft {
		t.Error("release.yml does not verify the release files before it drafts the release")
	}
	for _, want := range []string{"flags=(-update-info)", `flags+=(-signed "$PADMATE_SIGN_SUBJECT")`, `if [ "$PADMATE_SIGN" != "none" ]; then`} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}

	// The notes say whether the build is signed.
	for _, want := range []string{`signing="Signed by $PADMATE_SIGN_SUBJECT."`, `signing="Not code-signed yet, so Windows SmartScreen may warn."`, "$signing"} {
		if !strings.Contains(text, want) {
			t.Errorf("release.yml lacks %q in the notes", want)
		}
	}
}

// Signing is off unless the owner turns it on with a repository variable, and
// its secrets are the environment's, read by the build step alone.
func TestReleaseWorkflowSigningIsOptIn(t *testing.T) {
	text := releaseWorkflow(t)
	if n := strings.Count(text, "PADMATE_SIGN: ${{ vars.PADMATE_SIGN || 'none' }}"); n < 3 {
		t.Errorf("PADMATE_SIGN defaults to none in %d steps, want it in the build, the check and the draft", n)
	}
	if !regexp.MustCompile(`(?m)^    environment: release$`).MatchString(text) {
		t.Error("the job does not use the environment release, where the owner protects the signing secrets")
	}
	// The workflow reads no token but its own, and takes no OIDC identity.
	for _, forbidden := range []string{"id-token", "secrets.GITHUB_TOKEN", "pull_request"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("release.yml has %q", forbidden)
		}
	}
	// Every secret is handed to a step as an environment variable of the build
	// step, never put on a command line or in another step.
	build := strings.Index(text, "- name: Build the installer")
	run := strings.Index(text, "run: task package:installer")
	if build < 0 || run < build {
		t.Fatal("release.yml has no build step")
	}
	secret := regexp.MustCompile(`^\s+[A-Z][A-Z0-9_]*: \$\{\{ secrets\.[A-Z][A-Z0-9_]* \}\}$`)
	for i, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "secrets.") || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !secret.MatchString(line) {
			t.Errorf("line %d uses a secret otherwise than as an environment variable of a step: %s", i+1, strings.TrimSpace(line))
			continue
		}
		pos := strings.Index(text, line)
		if pos < build || pos > run {
			t.Errorf("line %d reads a secret outside the build step: %s", i+1, strings.TrimSpace(line))
		}
	}
	// Each step that runs a script gets the version through the environment.
	for i, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(line, "${{ github.event.inputs") && !strings.HasPrefix(trimmed, "VERSION:") && !strings.HasPrefix(trimmed, "group:") && !strings.HasPrefix(trimmed, "#") {
			t.Errorf("line %d uses the input of a manual run outside the one place that turns it into VERSION: %s", i+1, trimmed)
		}
	}
}

// stepIndex is where the step called name starts in the workflow text, or -1.
func stepIndex(text, name string) int {
	return strings.Index(text, "- name: "+name+"\n")
}

// A GitHub runner can sign only with Azure Artifact Signing. Of the modes of
// package-installer.mjs, store needs a certificate in the runner's own
// Windows store, and pfx needs the absolute path of a file on the machine,
// which no secret is (signingPlan refuses a URL and a base64 value). So the
// workflow carries no PFX secrets, and a repository variable that asks for
// another mode is an error before anything is built, not a build that fails
// late or quietly makes no signature (PKG-09, ADR-0073).
func TestReleaseWorkflowSignsOnlyWithAzure(t *testing.T) {
	text := releaseWorkflow(t)
	for _, forbidden := range []string{"secrets.WIN_CSC", "secrets.CSC_"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("release.yml hands out %q: a PFX cannot be signed with in this workflow (signingPlan wants a local file)", forbidden)
		}
	}

	check := stepIndex(text, "Check the signing settings")
	build := stepIndex(text, "Build the installer")
	if check < 0 || build < 0 || check > build {
		t.Fatalf("the signing settings are checked at %d, the build starts at %d: the check must come first", check, build)
	}
	guard := text[check:build]
	for _, want := range []string{
		`PADMATE_SIGN: ${{ vars.PADMATE_SIGN || 'none' }}`,
		`case "$PADMATE_SIGN" in`,
		"none) ;;",
		"azure)",
		`[ -z "$PADMATE_SIGN_SUBJECT" ]`,
		"*)",
		"exit 1",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("the step that checks the signing settings lacks %q", want)
		}
	}
	// store and pfx get no branch of their own: they fall into the refusal.
	if label := regexp.MustCompile(`(?m)^\s*(?:[a-z*]+\|)*(?:store|pfx)(?:\|[a-z*]+)*\)`).FindString(guard); label != "" {
		t.Errorf("the step that checks the signing settings has a branch for %q", strings.TrimSpace(label))
	}
	if !strings.Contains(guard, "store") || !strings.Contains(guard, "pfx") {
		t.Error("the refusal does not say which modes it means")
	}
}

// The installed files and the uninstaller of a signed build are checked in the
// smoke test, because electron-builder deletes the uninstaller from
// dist/installer once it has signed it. The variable that turns the check on
// is set from the publisher the build was signed as, in the step that runs the
// smoke test and nowhere else (PKG-09, ADR-0073).
func TestReleaseWorkflowChecksTheSignedUninstaller(t *testing.T) {
	text := releaseWorkflow(t)
	smoke := stepIndex(text, "Smoke-test install, upgrade and uninstall")
	next := stepIndex(text, "Check the release files")
	if smoke < 0 || next < 0 || smoke > next {
		t.Fatalf("the smoke test starts at %d, the check of the release files at %d", smoke, next)
	}
	step := text[smoke:next]
	for _, want := range []string{
		`PADMATE_SIGN: ${{ vars.PADMATE_SIGN || 'none' }}`,
		`PADMATE_SIGN_SUBJECT: ${{ vars.PADMATE_SIGN_SUBJECT }}`,
		`if [ "$PADMATE_SIGN" != "none" ]; then`,
		`export PADMATE_INSTALLER_SIGNED_SUBJECT="$PADMATE_SIGN_SUBJECT"`,
		"task test:installer",
	} {
		if !strings.Contains(step, want) {
			t.Errorf("the smoke test step lacks %q", want)
		}
	}
	if n := strings.Count(text, "PADMATE_INSTALLER_SIGNED_SUBJECT"); n != 1 {
		t.Errorf("PADMATE_INSTALLER_SIGNED_SUBJECT appears %d times in release.yml, want it once, in the smoke test step", n)
	}
	// Only a self-signed test certificate needs these, and CI has none.
	for _, forbidden := range []string{"ALLOW_UNTRUSTED_ROOT", "SIGNED_NO_TIMESTAMP"} {
		if strings.Contains(text, forbidden) {
			t.Errorf("release.yml sets %s: a release is signed by a trusted certificate with a timestamp", forbidden)
		}
	}
}
