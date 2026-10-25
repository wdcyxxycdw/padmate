import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import {
  isMirror,
  marker,
  refusal,
  refusePackaging,
  repoRoot,
} from "./public-mirror.mjs";

const folders = [];
afterEach(() => {
  for (const dir of folders.splice(0))
    rmSync(dir, { recursive: true, force: true });
});
const folder = () => {
  const dir = mkdtempSync(join(tmpdir(), "padmate-mirror-"));
  folders.push(dir);
  return dir;
};

describe("the public code mirror", () => {
  it("is the tree that has the marker at its root", () => {
    const root = folder();
    expect(isMirror(root)).toBe(false);
    expect(() => refusePackaging(root)).not.toThrow();
    writeFileSync(join(root, marker), "x\n");
    expect(isMirror(root)).toBe(true);
    expect(() => refusePackaging(root)).toThrow(refusal);
  });

  it("asks the exists function it is given", () => {
    const seen = [];
    expect(
      isMirror("R", (file) => {
        seen.push(file.replaceAll("\\", "/"));
        return true;
      }),
    ).toBe(true);
    expect(seen).toEqual([`R/${marker}`]);
  });

  it("says in the Taskfile what it says here", () => {
    const taskfile = readFileSync(resolve(repoRoot, "Taskfile.yml"), "utf8");
    expect(taskfile).toContain(`msg: "${refusal}"`);
    expect(refusal).toContain("public code mirror");
    expect(refusal).toContain("development repository");
  });
});
