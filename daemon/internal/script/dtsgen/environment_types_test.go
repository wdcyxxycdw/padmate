package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvironmentAPIBoundary(t *testing.T) {
	for _, version := range []Version{{Major: 0, Minor: 7}, HostAPI} {
		data, err := Filter(defaultDeclarations, version)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"interface AppInfo", "export const app:", "export const profiles:"} {
			if strings.Contains(string(data), name) != (version == HostAPI) {
				t.Fatalf("%s: wrong boundary for %s", version, name)
			}
		}
	}
}

func TestEnvironmentTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	const probe = `import {app,profiles,type AppInfo} from "padmate";
const a: AppInfo | null = app.current;
if (a) {
  const exe: string = a.exe; const path: string = a.path;
  const pid: number = a.pid; const elevated: boolean = a.elevated;
  // @ts-expect-error titles are excluded
  a.title;
  // @ts-expect-error metadata is read-only
  a.exe = "bad";
}
const matched: boolean = app.is("paint.exe");
app.onChange(next => { const a: AppInfo | null = next; }).dispose();
const current: string = profiles.current;
const ids: readonly string[] = profiles.list();
profiles.switchTo("paint");
// @ts-expect-error switching requires a library ID
profiles.switchTo(null);
// @ts-expect-error current is read-only
profiles.current = "other";
// @ts-expect-error list is read-only
ids.push("bad");
`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("environment types: %v\n%s", err, output)
	}
}
