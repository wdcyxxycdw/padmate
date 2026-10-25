package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The hud API and pad.label came with API 0.16 (SCR-22, ADR-0064): 0.8's
// declarations have neither, 0.16's have both, and hud.radial is not declared
// before the API of M19 (after 1.0, numbered 0.22 for now).
func TestHUDAPIBoundary(t *testing.T) {
	for _, tc := range []struct {
		api  Version
		want bool
	}{{Version{Major: 0, Minor: 8}, false}, {HostAPI, true}} {
		data, err := Filter(defaultDeclarations, tc.api)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"export const hud:", "toast(text: string", "panel(id: string", "label(c: Control): string"} {
			if strings.Contains(string(data), name) != tc.want {
				t.Errorf("API %s: has %q = %v, want %v", tc.api, name, !tc.want, tc.want)
			}
		}
		if strings.Contains(string(data), "radial(") {
			t.Errorf("API %s declares hud.radial (M19, after 1.0)", tc.api)
		}
	}
}

func TestHUDTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	const probe = `import {host, hud, on, pad} from "padmate";
on("south", "press", () => {
  if (host.has("hud")) hud.toast("笔刷 2");
  hud.toast("长一点", { ms: 3000 });
  hud.panel("brush", { title: "画笔", rows: [[pad.label("south"), "12"]] });
  hud.panel("brush", { rows: [] });
  hud.panel("brush", null);
  const name: string = pad.label("rb");
  // @ts-expect-error the text is a string
  hud.toast(12);
  // @ts-expect-error a row is two strings
  hud.panel("brush", { rows: [["only one"]] });
  // @ts-expect-error rows are required
  hud.panel("brush", { title: "画笔" });
  // @ts-expect-error a control name
  pad.label("nothing");
  // @ts-expect-error hud.radial is not in this API (M19, after 1.0)
  hud.radial(null);
});
`
	if err := os.WriteFile(filepath.Join(dir, "main.ts"), []byte(probe), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("hud types: %v\n%s", err, output)
	}
}
