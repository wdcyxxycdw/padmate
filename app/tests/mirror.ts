// The public code mirror (daemon/internal/publicmirror): the public repository
// holds the code only, and the file .padmate-public at its root says so. The
// tests that read a file the mirror leaves out (the documents) skip there, and
// only there: the development repository has no marker, so a missing document
// stays a failure.
import { existsSync } from "node:fs";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

/** The repository root. */
const root = fileURLToPath(new URL("../../", import.meta.url));

/** Whether this tree is the public code mirror. */
export const inMirror: boolean = existsSync(join(root, ".padmate-public"));
