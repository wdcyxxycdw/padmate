package dtsgen

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFilteredActionTypesWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	const probe = `import { defineScript, defineAction, param, key, type ScriptContext } from "padmate";
const params = { size: param.number({default: 3}), tool: param.choice({options: ["pen", "eraser"], default: "pen"}) };
export default defineScript({
  params,
  actions: {
    cycleBrush: defineAction({
      args: {dir: param.number({default: 1})},
      run(args, ctx: ScriptContext<typeof params>, e) {
        const n: number = args.dir;
        // @ts-expect-error action arguments preserve their types
        const bad: string = args.dir;
        const size: number = ctx.params.size;
        // @ts-expect-error action context parameter names are checked
        ctx.params.typo;
        if (e.held) e.holdKeys("Ctrl");
        void n; void bad; void size;
      }
    })
  },
  setup(ctx) {
    const n: number = ctx.params.size;
    const tool: "pen" | "eraser" = ctx.params.tool;
    // @ts-expect-error parameters are read-only
    ctx.params.size = 4;
    // @ts-expect-error parameter names are checked
    ctx.params.typo;
    void n; void tool;
  },
  async onActivate(ctx) { await key.tap(ctx.params.tool === "pen" ? "B" : "E"); }
});
`
	for _, api := range []Version{{Major: 0, Minor: 6}, HostAPI} {
		t.Run(api.String(), func(t *testing.T) {
			filtered, err := Filter(defaultDeclarations, api)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			for name, data := range map[string][]byte{"padmate.d.ts": filtered, "probe.ts": []byte(probe)} {
				if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			cmd := exec.Command("node", tsc, "--strict", "--noEmit", "--target", "es2022", "--module", "esnext", "--moduleResolution", "bundler", "probe.ts", "padmate.d.ts")
			cmd.Dir = dir
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("tsc action types: %v\n%s", err, output)
			}
		})
	}
}

func TestScaffoldWithTSC(t *testing.T) {
	tsc := os.Getenv("PADMATE_TEST_TSC")
	if tsc == "" {
		t.Skip("set PADMATE_TEST_TSC to the TypeScript compiler entry point")
	}
	dir := t.TempDir()
	if err := Scaffold(dir, HostAPI, nil, nil); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", tsc, "--project", dir, "--noEmit")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scaffold tsc: %v\n%s", err, output)
	}
}
