import { spawn } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import {
  developmentLinkerFlags,
  resolveRuntimeOptions,
} from "../src/main/runtime-options.ts";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const options = resolveRuntimeOptions({
  packaged: false,
  root,
  resources: "",
  appData: "",
  env: process.env,
});
await mkdir(options.bin, { recursive: true });
for (const target of ["padmated", "padmatectl", "padmate-probe"]) {
  const args = ["build", "-o", resolve(options.bin, `${target}.exe`)];
  if (target === "padmated")
    args.push("-ldflags", developmentLinkerFlags(options));
  args.push(`./cmd/${target}`);
  await new Promise((done, reject) => {
    const child = spawn("go", args, {
      cwd: resolve(root, "daemon"),
      windowsHide: true,
      shell: false,
      stdio: "inherit",
    });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0
        ? done()
        : reject(new Error(`go build ${target} failed (${code})`)),
    );
  });
}
await new Promise((done, reject) => {
  const child = spawn("go", ["run", "./tools/sdlfetch", "-out", options.bin], {
    cwd: resolve(root, "daemon"),
    windowsHide: true,
    shell: false,
    stdio: "inherit",
  });
  child.on("error", reject);
  child.on("exit", (code) =>
    code === 0
      ? done()
      : reject(new Error(`Pinned SDL fetch failed (${code})`)),
  );
});
console.log(
  "Prepared private development daemon and CLI in .cache/ui-bin (dry-run).",
);
