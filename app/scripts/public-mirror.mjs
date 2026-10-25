// The public code mirror (daemon/internal/publicmirror): the public repository
// holds the code only, and the file .padmate-public at its root says so. The
// scripts that pack the documentation into a package refuse in the mirror; the
// tests that read the documentation skip there (isMirror), and only there: the
// development repository has no marker, so a missing document stays a failure.
import { existsSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

export const marker = ".padmate-public";

/** What the package tasks say in the mirror; the Taskfile and publicmirror.go say the same. */
export const refusal =
  "this is the public code mirror: packages are built from the development repository, which has the documentation that ships with them";

const here = dirname(fileURLToPath(import.meta.url));

/** The repository root. */
export const repoRoot = resolve(here, "..", "..");

/**
 * Whether the repository at `root` is the public code mirror.
 *
 * @param {string} [root]
 * @param {(file: string) => boolean} [exists]
 * @returns {boolean}
 */
export function isMirror(root = repoRoot, exists = existsSync) {
  return exists(join(root, marker));
}

/**
 * Throws the refusal in the public code mirror.
 *
 * @param {string} [root]
 * @param {(file: string) => boolean} [exists]
 */
export function refusePackaging(root = repoRoot, exists = existsSync) {
  if (isMirror(root, exists)) throw new Error(refusal);
}
