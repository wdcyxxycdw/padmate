// Loads the layouts preview (npm run preview:layouts, port 5199) in a headless
// Chromium (no window), once for each Joy-Con placement and stage size, lets the
// page check what it drew (?check=1, layout-check.ts), prints the result and
// saves a PNG of each. Not part of CI: it needs the dev server and a browser.
//
//   node tests/preview/check-layouts.mjs [--out <folder>] [--sizes 1100x640,880x600]
//        [--only left-vertical,pair] [--gen 1|2] [--lang en|zh-CN] [--notice]
//        [--url http://127.0.0.1:5199]
//
// Exit code 1 if any placement has a problem.
import { mkdirSync } from "node:fs";
import { createRequire } from "node:module";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const require = createRequire(join(here, "../../package.json"));
const { chromium } = require("playwright");

const args = process.argv.slice(2);
const option = (name, fallback) => {
  const at = args.indexOf(`--${name}`);
  return at >= 0 ? args[at + 1] : fallback;
};
const out = resolve(option("out", join(here, "shots")));
const url = option("url", "http://127.0.0.1:5199");
const sizes = option("sizes", "1100x640,880x600,640x640,1400x900").split(",");
const only = option("only", "left-vertical,right-vertical,pair").split(",");
const gens = option("gen", "1,2").split(",");
const lang = option("lang", "zh-CN");
const notice = args.includes("--notice");
mkdirSync(out, { recursive: true });

const browser = await chromium.launch({
  headless: true,
  args: [
    "--use-angle=swiftshader",
    "--enable-unsafe-swiftshader",
    "--ignore-gpu-blocklist",
  ],
});
let failed = 0;
let total = 0;
try {
  for (const size of sizes) {
    const [width, height] = size.split("x").map(Number);
    // One placement to a page: a controller below the fold is not drawn.
    const page = await browser.newPage({
      viewport: { width: width + 80, height: height + 200 },
      deviceScaleFactor: 1,
    });
    for (const gen of gens)
      for (const placement of only) {
        const query = new URLSearchParams({
          check: "1",
          gen,
          only: placement,
          size,
          lang,
          ...(notice ? { notice: "1" } : {}),
        });
        await page.goto(`${url}/controller-layouts.html?${query}`);
        await page.waitForSelector("#layout-check[data-done]", {
          timeout: 120000,
        });
        const text = await page.textContent("#layout-check");
        const report = JSON.parse(text ?? "{}");
        const name = `gen${gen}-${placement}-${size}${notice ? "-notice" : ""}`;
        await page.screenshot({
          path: join(out, `${name}.png`),
          fullPage: true,
        });
        total++;
        if (!report.ok) failed++;
        console.log(
          `${report.ok ? "ok    " : "FAILED"} ${name}` +
            (report.results
              ? ` (${report.results
                  .map((r) => `${r.chips} chips, ${r.leaders} leaders`)
                  .join("; ")})`
              : ""),
        );
        if (report.error) console.log(`       ${report.error}`);
        for (const r of report.results ?? [])
          for (const problem of r.problems) console.log(`       ${problem}`);
      }
    await page.close();
  }
} finally {
  await browser.close();
}
console.log(`${total - failed} of ${total} placements pass; PNGs in ${out}`);
process.exit(failed ? 1 : 0);
