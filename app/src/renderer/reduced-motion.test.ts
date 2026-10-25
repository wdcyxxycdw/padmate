// UI-24: with the system asking for reduced motion nothing in the window
// moves on its own. The rule is one for every element, so a transition added
// later is covered without anyone remembering to.
import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { expect, it } from "vitest";

const here = dirname(fileURLToPath(import.meta.url));
const theme = readFileSync(join(here, "theme.css"), "utf8");

it("ends every transition and animation at once when reduced motion is asked for", () => {
  const block =
    /@media\s*\(prefers-reduced-motion:\s*reduce\)\s*{([^@]*?)}\s*$/m.exec(
      theme,
    );
  expect(block, "theme.css has the rule").not.toBeNull();
  const body = block?.[1] ?? "";
  expect(body).toMatch(/\*,\s*\*::before,\s*\*::after/);
  expect(body).toMatch(/transition-duration:\s*0\.01ms\s*!important/);
  expect(body).toMatch(/animation-duration:\s*0\.01ms\s*!important/);
  expect(body).toMatch(/animation-iteration-count:\s*1\s*!important/);
});
