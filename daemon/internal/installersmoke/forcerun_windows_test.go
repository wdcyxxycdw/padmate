package installersmoke

import (
	"strings"
	"testing"
)

// An installer run with --force-run starts padmate.exe through Explorer, as
// the user, in the real environment: the real data folder, a daemon that sends
// keys. The smoke test must never run one (ADR-0073, PKG-10); the check is
// here so that it can not be talked out of by a later edit.
func TestRefuseForceRun(t *testing.T) {
	for _, args := range []string{
		forceRunFlag,
		"/S " + forceRunFlag,
		"--updated /S " + strings.ToUpper(forceRunFlag),
		"/S  " + forceRunFlag + ` /D=C:\somewhere`,
	} {
		if err := refuseForceRun(args); err == nil {
			t.Errorf("%q was not refused", args)
		}
	}
	for _, args := range []string{
		"",
		"/S",
		`/S /D=C:\a folder\padmate`,
		"--updated /S",
		`--updated /S /D=C:\force run`,
	} {
		if err := refuseForceRun(args); err != nil {
			t.Errorf("%q was refused: %v", args, err)
		}
	}
}

func TestRunCommandRefusesForceRunBeforeStartingAnything(t *testing.T) {
	s := &smoke{t: t}
	code, out, err := s.runCommand(`C:\does\not\exist\installer.exe`, "/S "+forceRunFlag, 0)
	if err == nil || code != -1 || out != "" || !strings.Contains(err.Error(), "refus") {
		t.Errorf("runCommand: exit code %d, output %q, error %v; want a refusal that starts nothing", code, out, err)
	}
}
