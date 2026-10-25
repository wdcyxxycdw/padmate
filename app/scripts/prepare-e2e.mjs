import { spawn } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const output = resolve(root, ".cache/m10-e2e/bin");
await mkdir(output, { recursive: true });
const commands = [
  [
    "test",
    "-c",
    "-o",
    resolve(output, "m10-daemon.test.exe"),
    "./cmd/padmated",
  ],
  ["build", "-o", resolve(output, "padmatectl.exe"), "./cmd/padmatectl"],
  ["build", "-o", resolve(output, "padmate-probe.exe"), "./cmd/padmate-probe"],
];
for (const args of commands) {
  await new Promise((done, reject) => {
    const child = spawn("go", args, {
      cwd: resolve(root, "daemon"),
      windowsHide: true,
      shell: false,
      stdio: "inherit",
    });
    child.on("error", reject);
    child.on("exit", (code) =>
      code === 0 ? done() : reject(new Error(`E2E Go build failed (${code})`)),
    );
  });
}
